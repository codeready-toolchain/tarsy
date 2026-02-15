package events

import (
	"context"
	"encoding/json"
	"fmt"
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

// listenTimeout bounds how long a LISTEN command may block when subscribing to
// a new PG channel. Without this, a stalled connection would block the
// subscribing goroutine (and thus the client's read loop) indefinitely.
const listenTimeout = 10 * time.Second

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
//
// subscriptions is accessed WITHOUT a lock. This is safe because all reads and
// writes (subscribe, unsubscribe, unregisterConnection) happen on the single
// goroutine that owns this connection (HandleConnection's read loop and its
// deferred cleanup). If a Connection is ever mutated from a different goroutine
// (e.g. an admin "kick" feature), subscriptions must be protected by a mutex.
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

// subscriberCount returns the number of subscribers for a channel.
// Unexported — used by tests to poll instead of sleeping.
func (m *ConnectionManager) subscriberCount(channel string) int {
	m.channelMu.RLock()
	defer m.channelMu.RUnlock()
	return len(m.channels[channel])
}

// handleClientMessage dispatches a client message to the appropriate handler.
func (m *ConnectionManager) handleClientMessage(ctx context.Context, c *Connection, msg *ClientMessage) {
	switch msg.Action {
	case "subscribe":
		if msg.Channel == "" {
			m.sendJSON(c, map[string]string{"type": "error", "message": "channel is required for subscribe"})
			return
		}
		if err := m.subscribe(c, msg.Channel); err != nil {
			m.sendJSON(c, map[string]string{
				"type":    "subscription.error",
				"channel": msg.Channel,
				"message": "failed to subscribe to channel",
			})
			return
		}
		m.sendJSON(c, map[string]string{
			"type":    "subscription.confirmed",
			"channel": msg.Channel,
		})
		// Auto catch-up: deliver all prior events so late subscribers don't miss anything.
		m.handleCatchup(ctx, c, msg.Channel, 0)

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
// LISTEN is synchronous so it completes before subscribe returns — this guarantees
// that the subsequent auto-catchup runs with LISTEN already active, closing the gap
// where events published between catchup and LISTEN would be lost.
//
// Returns an error if LISTEN fails so the caller can inform the client instead of
// sending a false subscription.confirmed.
func (m *ConnectionManager) subscribe(c *Connection, channel string) error {
	m.channelMu.Lock()
	needsListen := false
	if _, exists := m.channels[channel]; !exists {
		m.channels[channel] = make(map[string]bool)
		needsListen = true
	}
	m.channels[channel][c.ID] = true
	m.channelMu.Unlock()

	if needsListen {
		m.listenerMu.RLock()
		l := m.listener
		m.listenerMu.RUnlock()
		if l != nil {
			listenCtx, listenCancel := context.WithTimeout(context.Background(), listenTimeout)
			defer listenCancel()
			if err := l.Subscribe(listenCtx, channel); err != nil {
				slog.Error("Failed to LISTEN on channel", "channel", channel, "error", err)
				m.cleanupFailedChannel(c, channel)
				return fmt.Errorf("LISTEN on channel %s: %w", channel, err)
			}
		}
	}

	c.subscriptions[channel] = true
	return nil
}

// cleanupFailedChannel removes ALL subscribers from a channel after a LISTEN
// failure and notifies every affected connection (except the triggering one,
// which is notified by the caller via the returned error).
//
// Between unlocking channelMu (after creating the channel entry) and l.Subscribe
// completing, other goroutines may have subscribed to the same channel. Because
// they saw the channel already existed they skipped LISTEN and returned success.
// Those connections are now orphaned — they received subscription.confirmed but
// the underlying PG LISTEN was never established. This helper cleans them up.
//
// Client-side contract: an orphaned connection may observe the sequence
// subscription.confirmed → catchup events → subscription.error. This is an
// inherent artefact of the concurrent subscribe/LISTEN window and only occurs
// during transient LISTEN failures. Clients MUST treat subscription.error as
// authoritative: discard any previously received events for that channel and
// either re-subscribe (with back-off) or fall back to REST polling.
//
// Note: affected connections may retain a stale c.subscriptions[channel] entry.
// This is harmless: Broadcast uses m.channels (now deleted), and unsubscribe /
// unregisterConnection handle missing channel entries gracefully.
func (m *ConnectionManager) cleanupFailedChannel(triggering *Connection, channel string) {
	// Collect all affected connection IDs and delete the channel entirely.
	m.channelMu.Lock()
	affectedIDs := make([]string, 0, len(m.channels[channel]))
	for connID := range m.channels[channel] {
		if connID != triggering.ID {
			affectedIDs = append(affectedIDs, connID)
		}
	}
	delete(m.channels, channel)
	m.channelMu.Unlock()

	if len(affectedIDs) == 0 {
		return
	}

	// Look up connection pointers (without holding channelMu).
	m.mu.RLock()
	conns := make([]*Connection, 0, len(affectedIDs))
	for _, id := range affectedIDs {
		if conn, ok := m.connections[id]; ok {
			conns = append(conns, conn)
		}
	}
	m.mu.RUnlock()

	// Notify each affected connection that the subscription failed.
	for _, conn := range conns {
		slog.Warn("Removing orphaned subscriber after LISTEN failure",
			"connection_id", conn.ID, "channel", channel)
		m.sendJSON(conn, map[string]string{
			"type":    "subscription.error",
			"channel": channel,
			"message": "channel listen failed; subscription removed",
		})
	}
}

// unsubscribe removes a connection from a channel and stops LISTEN if last subscriber.
func (m *ConnectionManager) unsubscribe(c *Connection, channel string) {
	m.channelMu.Lock()
	if subs, exists := m.channels[channel]; exists {
		delete(subs, c.ID)
		if len(subs) == 0 {
			delete(m.channels, channel)
			// Last subscriber left — stop LISTEN.
			// The goroutine re-checks m.channels before issuing UNLISTEN to
			// prevent a race where a rapid unsubscribe/resubscribe cycle
			// (e.g. React StrictMode double-render) would drop the LISTEN:
			//   subscribe → LISTEN active
			//   unsubscribe → goroutine: UNLISTEN (deferred)
			//   resubscribe → channel re-added to m.channels
			//   goroutine → sees resubscribed → skips UNLISTEN
			m.listenerMu.RLock()
			l := m.listener
			m.listenerMu.RUnlock()
			if l != nil {
				go func() {
					m.channelMu.RLock()
					_, resubscribed := m.channels[channel]
					m.channelMu.RUnlock()
					if resubscribed {
						return
					}
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

	// Send missed events in order, injecting db_event_id for position tracking.
	// The stored payload doesn't contain db_event_id (it's only added to the
	// NOTIFY payload at publish time), so we add it here from the DB row ID.
	for _, evt := range events {
		evt.Payload["db_event_id"] = evt.ID
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
