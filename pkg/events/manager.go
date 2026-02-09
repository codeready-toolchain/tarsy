package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// catchupLimit is the maximum number of events returned in a catchup response.
// If more events are missed, a catchup.overflow message tells the client to
// do a full REST reload.
const catchupLimit = 200

// CatchupEvent holds the data returned by the catchup query.
type CatchupEvent struct {
	ID      int
	Payload map[string]interface{}
}

// CatchupQuerier queries events for catchup. Implemented by EventService.
type CatchupQuerier interface {
	GetCatchupEvents(ctx context.Context, channel string, sinceID, limit int) ([]CatchupEvent, error)
}

// ConnectionManager manages WebSocket connections and channel subscriptions.
// Each Go process (pod) has one ConnectionManager instance.
type ConnectionManager struct {
	// Active connections: connection_id → *Connection
	connections map[string]*Connection
	mu          sync.RWMutex

	// Channel subscriptions: channel → set of connection_ids
	channels  map[string]map[string]bool
	channelMu sync.RWMutex

	// CatchupQuerier for catchup queries
	catchupQuerier CatchupQuerier

	// NotifyListener for dynamic LISTEN/UNLISTEN (set after construction)
	listener   *NotifyListener
	listenerMu sync.RWMutex

	// Write timeout for WebSocket sends
	writeTimeout time.Duration
}

// Connection represents a single WebSocket client.
type Connection struct {
	ID            string
	Conn          *websocket.Conn
	subscriptions map[string]bool // channels this connection is subscribed to
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewConnectionManager creates a new ConnectionManager.
func NewConnectionManager(catchupQuerier CatchupQuerier, writeTimeout time.Duration) *ConnectionManager {
	return &ConnectionManager{
		connections:    make(map[string]*Connection),
		channels:       make(map[string]map[string]bool),
		catchupQuerier: catchupQuerier,
		writeTimeout:   writeTimeout,
	}
}

// SetListener sets the NotifyListener for dynamic LISTEN/UNLISTEN.
// Called once during startup after both ConnectionManager and NotifyListener are created.
func (m *ConnectionManager) SetListener(l *NotifyListener) {
	m.listenerMu.Lock()
	defer m.listenerMu.Unlock()
	m.listener = l
}

// HandleConnection manages the lifecycle of a single WebSocket connection.
// Called by the WebSocket HTTP handler after upgrade. Blocks until the
// connection closes.
func (m *ConnectionManager) HandleConnection(parentCtx context.Context, conn *websocket.Conn) {
	connID := uuid.New().String()
	ctx, cancel := context.WithCancel(parentCtx)

	c := &Connection{
		ID:            connID,
		Conn:          conn,
		subscriptions: make(map[string]bool),
		ctx:           ctx,
		cancel:        cancel,
	}

	m.registerConnection(c)
	defer m.unregisterConnection(c)

	// Send connection established message
	m.sendJSON(c, map[string]string{
		"type":          "connection.established",
		"connection_id": connID,
	})

	// Read loop — process client messages until connection closes
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			// Connection closed or error — exit read loop
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("Invalid WebSocket message",
				"connection_id", connID, "error", err)
			continue
		}

		m.handleClientMessage(ctx, c, &msg)
	}
}

// Broadcast sends an event payload to all connections subscribed to the given channel.
func (m *ConnectionManager) Broadcast(channel string, event []byte) {
	m.channelMu.RLock()
	connIDs, exists := m.channels[channel]
	if !exists {
		m.channelMu.RUnlock()
		return
	}
	// Copy IDs to avoid holding lock during sends
	ids := make([]string, 0, len(connIDs))
	for id := range connIDs {
		ids = append(ids, id)
	}
	m.channelMu.RUnlock()

	// Snapshot connection pointers under the lock, then release before
	// sending. This avoids holding mu.RLock during potentially slow
	// writes (up to writeTimeout per connection), which would stall
	// connection register/unregister operations.
	m.mu.RLock()
	conns := make([]*Connection, 0, len(ids))
	for _, id := range ids {
		if conn, ok := m.connections[id]; ok {
			conns = append(conns, conn)
		}
	}
	m.mu.RUnlock()

	for _, conn := range conns {
		if err := m.sendRaw(conn, event); err != nil {
			slog.Warn("Failed to send to WebSocket client",
				"connection_id", conn.ID, "error", err)
		}
	}
}

// ActiveConnections returns the count of active WebSocket connections.
func (m *ConnectionManager) ActiveConnections() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connections)
}

// handleClientMessage dispatches a client message to the appropriate handler.
func (m *ConnectionManager) handleClientMessage(ctx context.Context, c *Connection, msg *ClientMessage) {
	switch msg.Action {
	case "subscribe":
		if msg.Channel == "" {
			m.sendJSON(c, map[string]string{"type": "error", "message": "channel is required for subscribe"})
			return
		}
		m.subscribe(c, msg.Channel)
		m.sendJSON(c, map[string]string{
			"type":    "subscription.confirmed",
			"channel": msg.Channel,
		})

	case "unsubscribe":
		if msg.Channel == "" {
			m.sendJSON(c, map[string]string{"type": "error", "message": "channel is required for unsubscribe"})
			return
		}
		m.unsubscribe(c, msg.Channel)

	case "catchup":
		if msg.Channel == "" {
			m.sendJSON(c, map[string]string{"type": "error", "message": "channel is required for catchup"})
			return
		}
		if msg.LastEventID != nil {
			m.handleCatchup(ctx, c, msg.Channel, *msg.LastEventID)
		}

	case "ping":
		m.sendJSON(c, map[string]string{"type": "pong"})
	}
}

// subscribe registers a connection for a channel and starts LISTEN if first subscriber.
func (m *ConnectionManager) subscribe(c *Connection, channel string) {
	m.channelMu.Lock()
	if _, exists := m.channels[channel]; !exists {
		m.channels[channel] = make(map[string]bool)
		// First subscriber on this channel — start LISTEN
		m.listenerMu.RLock()
		l := m.listener
		m.listenerMu.RUnlock()
		if l != nil {
			go func() {
				if err := l.Subscribe(context.Background(), channel); err != nil {
					slog.Error("Failed to LISTEN on channel", "channel", channel, "error", err)
					// Remove the channel entry so the next subscriber retries LISTEN.
					// Existing subscribers for this channel won't receive real-time
					// events until LISTEN succeeds on a future subscribe attempt.
					m.channelMu.Lock()
					delete(m.channels, channel)
					m.channelMu.Unlock()
				}
			}()
		}
	}
	m.channels[channel][c.ID] = true
	m.channelMu.Unlock()

	c.subscriptions[channel] = true
}

// unsubscribe removes a connection from a channel and stops LISTEN if last subscriber.
func (m *ConnectionManager) unsubscribe(c *Connection, channel string) {
	m.channelMu.Lock()
	if subs, exists := m.channels[channel]; exists {
		delete(subs, c.ID)
		if len(subs) == 0 {
			delete(m.channels, channel)
			// Last subscriber left — stop LISTEN
			m.listenerMu.RLock()
			l := m.listener
			m.listenerMu.RUnlock()
			if l != nil {
				go func() {
					if err := l.Unsubscribe(context.Background(), channel); err != nil {
						slog.Error("Failed to UNLISTEN channel", "channel", channel, "error", err)
					}
				}()
			}
		}
	}
	m.channelMu.Unlock()

	delete(c.subscriptions, channel)
}

// handleCatchup sends missed events since lastEventID to the client.
func (m *ConnectionManager) handleCatchup(ctx context.Context, c *Connection, channel string, lastEventID int) {
	if m.catchupQuerier == nil {
		return
	}

	// Query events from DB since lastEventID (capped at catchupLimit + 1 to detect overflow)
	events, err := m.catchupQuerier.GetCatchupEvents(ctx, channel, lastEventID, catchupLimit+1)
	if err != nil {
		slog.Error("Catchup query failed", "channel", channel, "error", err)
		return
	}

	// Check if more events exist beyond the limit
	hasMore := len(events) > catchupLimit
	if hasMore {
		events = events[:catchupLimit]
	}

	// Send missed events in order
	for _, evt := range events {
		payload, err := json.Marshal(evt.Payload)
		if err != nil {
			continue
		}
		if err := m.sendRaw(c, payload); err != nil {
			slog.Warn("Failed to send catchup event",
				"connection_id", c.ID, "error", err)
			return
		}
	}

	// If more events were missed than the catchup limit, tell the client
	// to do a full REST reload instead of paginating catchup requests.
	if hasMore {
		m.sendJSON(c, map[string]interface{}{
			"type":     "catchup.overflow",
			"channel":  channel,
			"has_more": true,
		})
	}
}

// registerConnection adds a connection to the tracking map.
func (m *ConnectionManager) registerConnection(c *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connections[c.ID] = c
}

// unregisterConnection removes a connection and all its subscriptions.
func (m *ConnectionManager) unregisterConnection(c *Connection) {
	// Remove from all channel subscriptions
	for ch := range c.subscriptions {
		m.unsubscribe(c, ch)
	}

	m.mu.Lock()
	delete(m.connections, c.ID)
	m.mu.Unlock()

	c.cancel()
	_ = c.Conn.Close(websocket.StatusNormalClosure, "")
}

// sendJSON marshals and sends a JSON message to a single connection.
func (m *ConnectionManager) sendJSON(c *Connection, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Warn("Failed to marshal WebSocket message",
			"connection_id", c.ID, "error", err)
		return
	}
	if err := m.sendRaw(c, data); err != nil {
		slog.Warn("Failed to send WebSocket message",
			"connection_id", c.ID, "error", err)
	}
}

// sendRaw sends raw bytes to a single connection with a write timeout.
func (m *ConnectionManager) sendRaw(c *Connection, data []byte) error {
	writeCtx, cancel := context.WithTimeout(c.ctx, m.writeTimeout)
	defer cancel()
	return c.Conn.Write(writeCtx, websocket.MessageText, data)
}
