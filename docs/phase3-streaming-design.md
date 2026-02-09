# Phase 3.4: Real-time Streaming — Detailed Design

**Status**: ✅ Design Complete — Ready for Implementation  
**Last Updated**: 2026-02-09

## Overview

This document details the Real-time Streaming system for the new TARSy. Phase 3.4 connects the agent execution pipeline (Phase 3.1–3.3) to WebSocket clients, enabling real-time delivery of timeline events and LLM streaming tokens to the frontend dashboard.

**Phase 3.4 Scope**: WebSocket endpoint (Echo + coder/websocket), PostgreSQL NOTIFY/LISTEN for cross-pod event delivery, event publishing from agent controllers, frontend event protocol (create → stream chunks → complete), and reconnection/catchup support.

**Key Design Principles:**
- Progressive delivery — events appear on the frontend as they happen (not batched at session end)
- Database as source of truth — persistent events written to DB first, then broadcast
- Transient streaming for tokens — LLM token chunks use NOTIFY/WebSocket only (no per-token DB writes)
- Cross-pod distribution — PostgreSQL NOTIFY delivers events to all pods in multi-replica deployments
- Event ID tracking — frontend uses `event_id` to track updates, eliminating de-duplication complexity
- Catchup support — reconnecting clients can request missed events by `last_event_id`
- Channel-based routing — events routed to subscribers by session channel

**What This Phase Delivers:**
- WebSocket handler with channel-based subscriptions (Echo + coder/websocket)
- PostgreSQL NOTIFY/LISTEN integration for cross-pod event delivery
- EventPublisher called from controllers during agent execution
- Event protocol: `timeline_event.created` → `stream.chunk` → `timeline_event.completed`
- Session lifecycle events (`session.status`, `session.completed`, etc.)
- Reconnection and catchup mechanism
- Connection manager for WebSocket client tracking
- Integration with existing EventService for persistence and cleanup

**What This Phase Does NOT Deliver:**
- MCP tool execution events (Phase 4 — controllers already create `tool_call`/`tool_result` events, but without real MCP they use stubs)
- Debug page WebSocket (`/ws/sessions/{id}/debug`) — deferred to Phase 10
- Frontend rendering of streaming events (Phase 10 — dashboard enhancement)
- Prometheus metrics for WebSocket connections (Phase 9 — observability)

---

## Architecture Overview

### High-Level Architecture

```
┌───────────────────────────────────────────────────────────────┐
│                     Go Process (Pod)                           │
│                                                               │
│  ┌──────────────┐    ┌───────────────────┐                    │
│  │  Controller   │───▶│  EventPublisher   │                    │
│  │  (ReAct, NT,  │    │  - Persist to DB  │                    │
│  │   Synthesis)  │    │  - pg_notify()    │                    │
│  └──────────────┘    └────────┬──────────┘                    │
│                               │                               │
│                               │ NOTIFY                        │
│                               ▼                               │
│  ┌───────────────────────────────────────────────────────┐    │
│  │               PostgreSQL                               │    │
│  │  ┌─────────────────────┐  ┌────────────────────────┐  │    │
│  │  │    events table     │  │  NOTIFY/LISTEN          │  │    │
│  │  │  (persistent events)│  │  (all subscribed pods)  │  │    │
│  │  └─────────────────────┘  └────────────────────────┘  │    │
│  └───────────────────────────────────────────────────────┘    │
│                               │                               │
│                               │ LISTEN callback               │
│                               ▼                               │
│  ┌───────────────────────────────────────────────────────┐    │
│  │              NotifyListener                            │    │
│  │  - Dedicated DB connection                             │    │
│  │  - Dispatches to local WebSocket clients               │    │
│  └────────────────────────────┬──────────────────────────┘    │
│                               │                               │
│                               ▼                               │
│  ┌───────────────────────────────────────────────────────┐    │
│  │           ConnectionManager (Hub)                      │    │
│  │  - Tracks WebSocket connections                        │    │
│  │  - Routes events by channel subscription               │    │
│  │  - Handles subscribe/unsubscribe/catchup               │    │
│  └────────────────────────────┬──────────────────────────┘    │
│                               │                               │
│                               ▼                               │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐                      │
│  │  WS #1   │ │  WS #2   │ │  WS #3   │                      │
│  │ (browser)│ │ (browser)│ │ (browser)│                      │
│  └──────────┘ └──────────┘ └──────────┘                      │
└───────────────────────────────────────────────────────────────┘
```

### Cross-Pod Event Delivery

```
Pod A (processing session)          Pod B (WebSocket clients)
─────────────────────────          ──────────────────────────
Controller creates event
  → EventPublisher.Publish()
    → INSERT into events table
    → NOTIFY "session:abc-123"
                                    NotifyListener receives NOTIFY
                                      → ConnectionManager.Broadcast()
                                        → WebSocket clients see event
```

### Event Lifecycle

```
1. Controller calls EventPublisher.Publish() or PublishTransient()
   │
   ├── Publish() [persistent events]:
   │   → INSERT event into events table
   │   → NOTIFY "session:{session_id}" with payload
   │
   └── PublishTransient() [streaming chunks]:
       → NOTIFY "session:{session_id}" with payload only
       → No database INSERT (avoids write amplification)
   │
2. PostgreSQL broadcasts NOTIFY to all LISTENing connections
   │
3. NotifyListener on each pod receives notification
   │
4. ConnectionManager finds WebSocket clients subscribed to channel
   │
5. Event sent to each subscribed WebSocket client
```

---

## Package Structure

```
pkg/
├── api/
│   ├── server.go              # Existing — add WebSocket route
│   ├── handler_ws.go          # NEW: WebSocket endpoint handler
│   └── ...
├── events/
│   ├── publisher.go           # NEW: EventPublisher (publish + notify)
│   ├── listener.go            # NEW: NotifyListener (LISTEN + dispatch)
│   ├── manager.go             # NEW: ConnectionManager (WebSocket hub)
│   ├── types.go               # NEW: Event types and protocol messages
│   ├── publisher_test.go      # NEW
│   ├── listener_test.go       # NEW
│   └── manager_test.go        # NEW
├── services/
│   └── event_service.go       # Existing — DB persistence (used by publisher)
├── agent/
│   ├── context.go             # Updated — add EventPublisher to ExecutionContext
│   └── controller/
│       ├── helpers.go         # Updated — publish events during execution
│       ├── react.go           # Updated — integrate streaming events
│       ├── native_thinking.go # Updated — integrate streaming events
│       └── synthesis.go       # Updated — integrate streaming events
└── queue/
    └── executor.go            # Updated — wire EventPublisher
```

---

## WebSocket Endpoint

### Route Registration

```go
// pkg/api/server.go — add WebSocket route

func (s *Server) setupRoutes() {
    // ... existing routes ...

    // WebSocket endpoint for real-time event streaming
    s.echo.GET("/ws", s.wsHandler)
}
```

### WebSocket Handler

The WebSocket handler uses `coder/websocket` (already a project dependency) for RFC 6455 compliant WebSocket support, integrated with Echo v5.

```go
// pkg/api/handler_ws.go

package api

import (
    "context"
    "encoding/json"
    "log/slog"
    "time"

    "github.com/coder/websocket"
    "github.com/labstack/echo/v5"

    "github.com/codeready-toolchain/tarsy/pkg/events"
)

// wsHandler upgrades HTTP connections to WebSocket and delegates to ConnectionManager.
func (s *Server) wsHandler(c echo.Context) error {
    // Upgrade HTTP to WebSocket
    conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
        // Allow all origins in development; restrict in production via config
        InsecureSkipVerify: true,
    })
    if err != nil {
        return err
    }

    // Register connection with the ConnectionManager
    s.connManager.HandleConnection(c.Request().Context(), conn)
    return nil
}
```

### Client Protocol (JSON Messages)

**Client → Server:**

```json
// Subscribe to a session's events
{"action": "subscribe", "channel": "session:abc-123"}

// Unsubscribe from a channel
{"action": "unsubscribe", "channel": "session:abc-123"}

// Catchup: request missed events since last_event_id
{"action": "catchup", "channel": "session:abc-123", "last_event_id": 42}

// Keepalive
{"action": "ping"}
```

**Server → Client:**

```json
// Connection established
{"type": "connection.established", "connection_id": "uuid"}

// Subscription confirmed
{"type": "subscription.confirmed", "channel": "session:abc-123"}

// Keepalive response
{"type": "pong"}

// Event delivery (persistent events from DB + transient events)
{
    "type": "timeline_event.created",
    "event_id": "evt-uuid",
    "session_id": "abc-123",
    "stage_id": "stg-uuid",
    "execution_id": "exec-uuid",
    "event_type": "llm_thinking",
    "status": "streaming",
    "content": "",
    "metadata": {"source": "native"},
    "sequence_number": 5,
    "timestamp": "2026-02-09T10:30:00Z",
    "db_event_id": 42
}

// LLM token streaming chunk (transient — NOTIFY only, no DB persistence)
{
    "type": "stream.chunk",
    "event_id": "evt-uuid",
    "content": "Analyzing the pod status...",
    "timestamp": "2026-02-09T10:30:01Z"
}

// Timeline event completed (final content written to DB)
{
    "type": "timeline_event.completed",
    "event_id": "evt-uuid",
    "content": "The pod is in CrashLoopBackOff due to...",
    "status": "completed",
    "timestamp": "2026-02-09T10:30:05Z",
    "db_event_id": 43
}

// Session status change
{
    "type": "session.status",
    "session_id": "abc-123",
    "status": "completed",
    "timestamp": "2026-02-09T10:35:00Z",
    "db_event_id": 44
}
```

---

## ConnectionManager (WebSocket Hub)

The ConnectionManager tracks all active WebSocket connections on the local pod, manages channel subscriptions, and broadcasts events to subscribed clients.

```go
// pkg/events/manager.go

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

// ConnectionManager manages WebSocket connections and channel subscriptions.
// Each Go process (pod) has one ConnectionManager instance.
type ConnectionManager struct {
    // Active connections: connection_id → *Connection
    connections map[string]*Connection
    mu          sync.RWMutex

    // Channel subscriptions: channel → set of connection_ids
    channels map[string]map[string]bool
    channelMu sync.RWMutex

    // EventService for catchup queries
    eventService EventService

    // Write timeout for WebSocket sends
    writeTimeout time.Duration
}

// Connection represents a single WebSocket client.
type Connection struct {
    ID             string
    Conn           *websocket.Conn
    Subscriptions  map[string]bool // channels this connection is subscribed to
    ctx            context.Context
    cancel         context.CancelFunc
}

// NewConnectionManager creates a new ConnectionManager.
func NewConnectionManager(eventService EventService, writeTimeout time.Duration) *ConnectionManager {
    return &ConnectionManager{
        connections:  make(map[string]*Connection),
        channels:    make(map[string]map[string]bool),
        eventService: eventService,
        writeTimeout: writeTimeout,
    }
}

// HandleConnection manages the lifecycle of a single WebSocket connection.
// Called by the WebSocket HTTP handler after upgrade.
func (m *ConnectionManager) HandleConnection(parentCtx context.Context, conn *websocket.Conn) {
    connID := uuid.New().String()
    ctx, cancel := context.WithCancel(parentCtx)

    c := &Connection{
        ID:            connID,
        Conn:          conn,
        Subscriptions: make(map[string]bool),
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

    // Read loop — process client messages
    for {
        _, data, err := conn.Read(ctx)
        if err != nil {
            // Connection closed or error
            return
        }

        var msg ClientMessage
        if err := json.Unmarshal(data, &msg); err != nil {
            slog.Warn("Invalid WebSocket message", "connection_id", connID, "error", err)
            continue
        }

        m.handleClientMessage(ctx, c, &msg)
    }
}

// Broadcast sends an event to all connections subscribed to the given channel.
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

    m.mu.RLock()
    defer m.mu.RUnlock()

    for _, id := range ids {
        if conn, ok := m.connections[id]; ok {
            if err := m.sendRaw(conn, event); err != nil {
                slog.Warn("Failed to send to WebSocket client",
                    "connection_id", id, "error", err)
            }
        }
    }
}
```

### Client Message Handling

```go
// ClientMessage is the JSON structure for client → server messages.
type ClientMessage struct {
    Action      string `json:"action"`        // "subscribe", "unsubscribe", "catchup", "ping"
    Channel     string `json:"channel"`       // Channel name (e.g., "session:abc-123")
    LastEventID *int   `json:"last_event_id"` // For catchup
}

func (m *ConnectionManager) handleClientMessage(ctx context.Context, c *Connection, msg *ClientMessage) {
    switch msg.Action {
    case "subscribe":
        m.subscribe(c, msg.Channel)
        m.sendJSON(c, map[string]string{
            "type":    "subscription.confirmed",
            "channel": msg.Channel,
        })

    case "unsubscribe":
        m.unsubscribe(c, msg.Channel)

    case "catchup":
        if msg.LastEventID != nil {
            m.handleCatchup(ctx, c, msg.Channel, *msg.LastEventID)
        }

    case "ping":
        m.sendJSON(c, map[string]string{"type": "pong"})
    }
}

const catchupLimit = 200

func (m *ConnectionManager) handleCatchup(ctx context.Context, c *Connection, channel string, lastEventID int) {
    // Query events from DB since lastEventID (capped at catchupLimit + 1 to detect overflow)
    events, err := m.eventService.GetEventsSince(ctx, channel, lastEventID, catchupLimit+1)
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
    for _, event := range events {
        payload, err := json.Marshal(event.Payload)
        if err != nil {
            continue
        }
        if err := m.sendRaw(c, payload); err != nil {
            slog.Warn("Failed to send catchup event", "connection_id", c.ID, "error", err)
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
```

---

## PostgreSQL NOTIFY/LISTEN

### NotifyListener

The NotifyListener maintains a dedicated PostgreSQL connection for LISTEN and dispatches received notifications to the local ConnectionManager.

```go
// pkg/events/listener.go

package events

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "sync"
    "time"

    "github.com/jackc/pgx/v5"
)

// NotifyListener listens for PostgreSQL NOTIFY events and dispatches
// them to the local ConnectionManager.
type NotifyListener struct {
    connString string
    conn       *pgx.Conn           // Dedicated connection for LISTEN
    connMu     sync.Mutex
    manager    *ConnectionManager
    channels   map[string]bool      // Currently LISTENing channels
    channelsMu sync.RWMutex
}

// NewNotifyListener creates a new PostgreSQL NOTIFY listener.
func NewNotifyListener(connString string, manager *ConnectionManager) *NotifyListener {
    return &NotifyListener{
        connString: connString,
        manager:    manager,
        channels:   make(map[string]bool),
    }
}

// Start establishes the dedicated LISTEN connection and begins receiving notifications.
func (l *NotifyListener) Start(ctx context.Context) error {
    conn, err := pgx.Connect(ctx, l.connString)
    if err != nil {
        return fmt.Errorf("failed to connect for LISTEN: %w", err)
    }
    l.conn = conn

    // Start the notification receive loop
    go l.receiveLoop(ctx)

    slog.Info("NotifyListener started")
    return nil
}

// Subscribe sends LISTEN for a channel on the dedicated connection.
func (l *NotifyListener) Subscribe(ctx context.Context, channel string) error {
    l.channelsMu.Lock()
    defer l.channelsMu.Unlock()

    if l.channels[channel] {
        return nil // Already listening
    }

    l.connMu.Lock()
    defer l.connMu.Unlock()

    // LISTEN requires a direct SQL command (not parameterized)
    _, err := l.conn.Exec(ctx, fmt.Sprintf("LISTEN %q", channel))
    if err != nil {
        return fmt.Errorf("LISTEN %q failed: %w", channel, err)
    }

    l.channels[channel] = true
    slog.Debug("Subscribed to NOTIFY channel", "channel", channel)
    return nil
}

// Unsubscribe sends UNLISTEN for a channel.
func (l *NotifyListener) Unsubscribe(ctx context.Context, channel string) error {
    l.channelsMu.Lock()
    defer l.channelsMu.Unlock()

    if !l.channels[channel] {
        return nil
    }

    l.connMu.Lock()
    defer l.connMu.Unlock()

    _, err := l.conn.Exec(ctx, fmt.Sprintf("UNLISTEN %q", channel))
    if err != nil {
        return fmt.Errorf("UNLISTEN %q failed: %w", channel, err)
    }

    delete(l.channels, channel)
    return nil
}

// receiveLoop continuously receives notifications from PostgreSQL
// and dispatches them to the ConnectionManager.
func (l *NotifyListener) receiveLoop(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        notification, err := l.conn.WaitForNotification(ctx)
        if err != nil {
            if ctx.Err() != nil {
                return // Context cancelled — shutting down
            }
            slog.Error("NOTIFY receive error", "error", err)
            // Attempt reconnection
            l.reconnect(ctx)
            continue
        }

        // Dispatch to ConnectionManager
        l.manager.Broadcast(notification.Channel, []byte(notification.Payload))
    }
}

// reconnect attempts to re-establish the LISTEN connection.
func (l *NotifyListener) reconnect(ctx context.Context) {
    l.connMu.Lock()
    defer l.connMu.Unlock()

    // Close old connection
    if l.conn != nil {
        l.conn.Close(ctx)
    }

    // Exponential backoff reconnection
    backoff := time.Second
    for {
        select {
        case <-ctx.Done():
            return
        case <-time.After(backoff):
        }

        conn, err := pgx.Connect(ctx, l.connString)
        if err != nil {
            slog.Error("LISTEN reconnect failed", "error", err, "backoff", backoff)
            backoff = min(backoff*2, 30*time.Second)
            continue
        }
        l.conn = conn

        // Re-subscribe to all channels
        l.channelsMu.RLock()
        for ch := range l.channels {
            if _, err := conn.Exec(ctx, fmt.Sprintf("LISTEN %q", ch)); err != nil {
                slog.Error("Re-LISTEN failed", "channel", ch, "error", err)
            }
        }
        l.channelsMu.RUnlock()

        slog.Info("NotifyListener reconnected")
        return
    }
}

// Stop closes the LISTEN connection.
func (l *NotifyListener) Stop(ctx context.Context) {
    l.connMu.Lock()
    defer l.connMu.Unlock()
    if l.conn != nil {
        l.conn.Close(ctx)
    }
}
```

---

## EventPublisher

The EventPublisher is the single entry point for controllers to broadcast events to WebSocket clients. It provides two methods:

1. **`Publish()`** — Persist to DB + NOTIFY (for state transitions and persistent events)
2. **`PublishTransient()`** — NOTIFY only (for high-frequency streaming chunks)

```go
// pkg/events/publisher.go

package events

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/codeready-toolchain/tarsy/pkg/services"
    "github.com/codeready-toolchain/tarsy/pkg/services/models"

    "entgo.io/ent/dialect/sql"
)

// EventPublisher publishes events for WebSocket delivery.
// Persistent events are stored in the events table then broadcast via NOTIFY.
// Transient events (streaming chunks) are broadcast via NOTIFY only.
type EventPublisher struct {
    eventService *services.EventService
    dbClient     *ent.Client  // For transactions (INSERT + pg_notify in same tx)
}

// NewEventPublisher creates a new EventPublisher.
func NewEventPublisher(eventService *services.EventService, dbClient *ent.Client) *EventPublisher {
    return &EventPublisher{
        eventService: eventService,
        dbClient:     dbClient,
    }
}

// Publish persists an event to the database and broadcasts via NOTIFY
// in a single transaction (pg_notify is transactional — held until COMMIT).
// Used for persistent events (timeline_event.created, timeline_event.completed,
// session.status, etc.) that must survive reconnection.
func (p *EventPublisher) Publish(ctx context.Context, sessionID, channel string, payload map[string]interface{}) error {
    // Single transaction: INSERT event + pg_notify()
    // pg_notify() is transactional — notification is held until COMMIT,
    // so WebSocket clients never receive events for uncommitted data.
    tx, err := p.dbClient.Tx(ctx)
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %w", err)
    }

    // 1. Persist to events table (within transaction)
    event, err := p.eventService.CreateEventTx(ctx, tx, models.CreateEventRequest{
        SessionID: sessionID,
        Channel:   channel,
        Payload:   payload,
    })
    if err != nil {
        tx.Rollback()
        return fmt.Errorf("failed to persist event: %w", err)
    }

    // Include the DB event ID in the payload for catchup tracking
    payload["db_event_id"] = event.ID

    // 2. pg_notify within same transaction — held until COMMIT
    if err := p.notifyTx(ctx, tx, channel, payload); err != nil {
        tx.Rollback()
        return fmt.Errorf("pg_notify failed: %w", err)
    }

    // 3. Commit — INSERT is persisted and NOTIFY fires atomically
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("failed to commit event transaction: %w", err)
    }

    return nil
}

// PublishTransient broadcasts an event via NOTIFY without persisting to DB.
// Used for high-frequency events like LLM streaming token chunks where
// per-event DB writes would cause write amplification.
// These events are ephemeral — lost if the client is disconnected.
func (p *EventPublisher) PublishTransient(ctx context.Context, channel string, payload map[string]interface{}) error {
    payloadStr, err := p.marshalPayload(payload)
    if err != nil {
        return err
    }
    // No transaction needed — transient events are fire-and-forget
    _, err = p.dbClient.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payloadStr)
    if err != nil {
        return fmt.Errorf("pg_notify failed: %w", err)
    }
    return nil
}

// notifyTx sends pg_notify within an existing transaction.
// The notification is held by PostgreSQL until the transaction commits.
func (p *EventPublisher) notifyTx(ctx context.Context, tx *ent.Tx, channel string, payload map[string]interface{}) error {
    payloadStr, err := p.marshalPayload(payload)
    if err != nil {
        return err
    }
    _, err = tx.Client().ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, payloadStr)
    if err != nil {
        return fmt.Errorf("pg_notify failed: %w", err)
    }
    return nil
}

// marshalPayload serializes the payload to JSON, truncating if it exceeds
// PostgreSQL's 8000-byte NOTIFY payload limit.
func (p *EventPublisher) marshalPayload(payload map[string]interface{}) (string, error) {
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        return "", fmt.Errorf("failed to marshal NOTIFY payload: %w", err)
    }

    payloadStr := string(payloadBytes)

    // PostgreSQL NOTIFY payload limit is 8000 bytes.
    // If payload exceeds limit, send a truncated notification with a flag
    // indicating the client should fetch the full event from DB.
    if len(payloadStr) > 7900 {
        truncated := map[string]interface{}{
            "type":       payload["type"],
            "event_id":   payload["event_id"],
            "session_id": payload["session_id"],
            "truncated":  true,
        }
        payloadBytes, _ = json.Marshal(truncated)
        payloadStr = string(payloadBytes)
    }

    return payloadStr, nil
}
```

---

## Event Types and Protocol

### Event Type Constants

```go
// pkg/events/types.go

package events

// Persistent event types (stored in DB + NOTIFY)
const (
    // Timeline event lifecycle
    EventTypeTimelineCreated   = "timeline_event.created"
    EventTypeTimelineCompleted = "timeline_event.completed"

    // Session lifecycle
    EventTypeSessionStatus    = "session.status"
    EventTypeSessionCompleted = "session.completed"

    // Stage lifecycle
    EventTypeStageStarted   = "stage.started"
    EventTypeStageCompleted = "stage.completed"
)

// Transient event types (NOTIFY only, no DB persistence)
const (
    // LLM streaming chunks
    EventTypeStreamChunk = "stream.chunk"
)
```

### Channel Naming Convention

```go
// GlobalSessionsChannel is the channel for session-level status events.
// The session list page subscribes to this for real-time updates.
const GlobalSessionsChannel = "sessions"

// SessionChannel returns the channel name for a session's events.
// Format: "session:{session_id}"
func SessionChannel(sessionID string) string {
    return "session:" + sessionID
}
```

**Two channel types:**
- `"sessions"` (global) — session-level status events only (`session.created`, `session.status`, `session.completed`). Subscribed by the session list page for real-time updates.
- `"session:{session_id}"` (per-session) — all events for a specific session (timeline events, streaming chunks, session status). Subscribed when viewing a specific session.

---

## Controller Integration

### Updated ExecutionContext

```go
// pkg/agent/context.go — add EventPublisher

type ExecutionContext struct {
    // ... existing fields ...

    // EventPublisher for real-time event delivery (injected by executor)
    EventPublisher *events.EventPublisher
}
```

### Updated Controller Helpers

The existing `createTimelineEvent` helper in `pkg/agent/controller/helpers.go` is updated to publish events for WebSocket delivery. The approach is:

1. **On TimelineEvent creation** → `EventPublisher.Publish()` with `timeline_event.created`
2. **During LLM streaming** → `EventPublisher.PublishTransient()` with `stream.chunk` for each token batch
3. **On TimelineEvent completion** → `EventPublisher.Publish()` with `timeline_event.completed`

```go
// pkg/agent/controller/helpers.go — updated createTimelineEvent

func createTimelineEvent(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    eventType timelineevent.EventType,
    content string,
    metadata map[string]any,
    eventSeq *int,
) (*ent.TimelineEvent, error) {
    *eventSeq++

    // 1. Create TimelineEvent in DB (existing behavior)
    event, err := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
        SessionID:      execCtx.SessionID,
        StageID:        execCtx.StageID,
        ExecutionID:    execCtx.ExecutionID,
        SequenceNumber: *eventSeq,
        EventType:      eventType,
        Content:        content,
        Metadata:       metadata,
    })
    if err != nil {
        return nil, err
    }

    // 2. Publish to WebSocket clients (non-blocking — don't fail execution on publish error)
    if execCtx.EventPublisher != nil {
        channel := events.SessionChannel(execCtx.SessionID)
        publishErr := execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
            "type":            events.EventTypeTimelineCreated,
            "event_id":        event.EventID,
            "session_id":      execCtx.SessionID,
            "stage_id":        execCtx.StageID,
            "execution_id":    execCtx.ExecutionID,
            "event_type":      string(eventType),
            "status":          "completed", // Phase 3.2 creates with final content (no streaming yet)
            "content":         content,
            "metadata":        metadata,
            "sequence_number": *eventSeq,
            "timestamp":       event.CreatedAt.Format(time.RFC3339Nano),
        })
        if publishErr != nil {
            slog.Warn("Failed to publish timeline event", "event_id", event.EventID, "error", publishErr)
        }
    }

    return event, nil
}
```

### LLM Streaming Integration

The key change for real-time LLM streaming is to move from the Phase 3.2 "buffered" pattern (collect all chunks, then create events) to a "streaming" pattern where:

1. When the **first chunk of a type** arrives → create TimelineEvent with `status: streaming` + publish `timeline_event.created`
2. As **chunks arrive** → publish `stream.chunk` events with accumulated content (transient, no DB)
3. When the **stream completes** → update TimelineEvent with final content + publish `timeline_event.completed`

This requires updating `collectStream` to accept a callback for streaming chunk delivery:

```go
// pkg/agent/controller/helpers.go — streaming-aware stream collection

// StreamCallback is called for each chunk during stream collection.
// Used by controllers to publish real-time updates to WebSocket clients.
type StreamCallback func(chunkType agent.ChunkType, content string)

// collectStreamWithCallback collects a stream while calling back for real-time delivery.
// The callback is optional (nil = buffered mode, same as Phase 3.2 collectStream).
func collectStreamWithCallback(
    stream <-chan agent.Chunk,
    callback StreamCallback,
) (*LLMResponse, error) {
    resp := &LLMResponse{}
    var textBuf, thinkingBuf strings.Builder

    for chunk := range stream {
        switch c := chunk.(type) {
        case *agent.TextChunk:
            textBuf.WriteString(c.Content)
            if callback != nil {
                callback(agent.ChunkTypeText, textBuf.String())
            }
        case *agent.ThinkingChunk:
            thinkingBuf.WriteString(c.Content)
            if callback != nil {
                callback(agent.ChunkTypeThinking, thinkingBuf.String())
            }
        case *agent.ToolCallChunk:
            resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
                ID:        c.CallID,
                Name:      c.Name,
                Arguments: c.Arguments,
            })
        case *agent.CodeExecutionChunk:
            resp.CodeExecutions = append(resp.CodeExecutions, agent.CodeExecutionChunk{
                Code:   c.Code,
                Result: c.Result,
            })
        case *agent.GroundingChunk:
            resp.Groundings = append(resp.Groundings, *c)
        case *agent.UsageChunk:
            resp.Usage = &agent.TokenUsage{
                InputTokens:    c.InputTokens,
                OutputTokens:   c.OutputTokens,
                TotalTokens:    c.TotalTokens,
                ThinkingTokens: c.ThinkingTokens,
            }
        case *agent.ErrorChunk:
            return nil, fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
                c.Message, c.Code, c.Retryable)
        }
    }

    resp.Text = textBuf.String()
    resp.ThinkingText = thinkingBuf.String()
    return resp, nil
}
```

### Streaming Event Flow in Controllers

For controllers, the integration looks like:

```go
// In NativeThinkingController.Run() — Phase 3.4 streaming pattern

// 1. Call LLM and stream chunks in real-time
var thinkingEventID, textEventID *string

streamCallback := func(chunkType agent.ChunkType, content string) {
    channel := events.SessionChannel(execCtx.SessionID)

    switch chunkType {
    case agent.ChunkTypeThinking:
        if thinkingEventID == nil {
            // First thinking chunk → create streaming TimelineEvent
            event, _ := execCtx.Services.Timeline.CreateStreamingTimelineEvent(ctx, ...)
            thinkingEventID = &event.EventID
            execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
                "type":       events.EventTypeTimelineCreated,
                "event_id":   event.EventID,
                "event_type": "llm_thinking",
                "status":     "streaming",
                "content":    "",
                // ...
            })
        }
        // Publish streaming chunk (transient — no DB)
        execCtx.EventPublisher.PublishTransient(ctx, channel, map[string]interface{}{
            "type":     events.EventTypeStreamChunk,
            "event_id": *thinkingEventID,
            "content":  content, // Accumulated content
        })

    case agent.ChunkTypeText:
        // Same pattern for text chunks
        // ...
    }
}

resp, err := callLLMWithCallback(iterCtx, execCtx.LLMClient, input, streamCallback)

// 2. After stream completes — finalize timeline events
if thinkingEventID != nil {
    execCtx.Services.Timeline.CompleteTimelineEvent(ctx, *thinkingEventID, resp.ThinkingText)
    execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
        "type":     events.EventTypeTimelineCompleted,
        "event_id": *thinkingEventID,
        "content":  resp.ThinkingText,
        "status":   "completed",
    })
}
```

---

## Frontend Event Protocol

### State Machine Per Event

The frontend maintains a map of timeline events keyed by `event_id`. The state machine for each event is:

```
1. timeline_event.created (status: "streaming")
   → Frontend creates placeholder for event_id
   → Shows spinner/typing indicator

2. stream.chunk (event_id matches)
   → Frontend updates content for event_id (replace, not append)
   → Content is accumulated server-side, sent as full content each time

3. timeline_event.completed (event_id matches)
   → Frontend replaces content with final version
   → Marks event as complete, removes spinner

4. stream.chunk arrives AFTER completed
   → Ignored (stale — status never goes backward)
```

**Key design: accumulated content, not incremental deltas.**

Each `stream.chunk` contains the full accumulated content so far (not just the new delta). This means:
- Frontend always has the latest complete content by simple replacement
- Out-of-order delivery doesn't cause missing text
- Reconnecting clients get the full content in the next chunk or `timeline_event.completed`
- No client-side concatenation logic needed

### Session Page Data Loading

When a user opens a session page:

1. **REST API**: `GET /api/v1/sessions/{id}` → returns session metadata + all timeline events (current state from DB)
2. **WebSocket**: Subscribe to `session:{session_id}` channel
3. **Active session**: Real-time updates arrive via WebSocket (`timeline_event.created`, `stream.chunk`, `timeline_event.completed`)
4. **Completed session**: No WebSocket events (session events already cleaned up)

### Reconnection Flow

1. WebSocket disconnects
2. Frontend tracks `lastEventID` per channel (from `db_event_id` field)
3. On reconnect:
   a. Resubscribe to channel
   b. Send catchup: `{"action": "catchup", "channel": "session:abc-123", "last_event_id": 42}`
   c. Server queries `events` table: `WHERE channel = ? AND id > ? ORDER BY id LIMIT 201` (200 + 1 to detect overflow)
   d. Server sends up to 200 missed persistent events
   e. If more than 200 events were missed, server sends `{"type": "catchup.overflow", "has_more": true}` — client automatically falls back to full REST reload via `GET /api/v1/sessions/{id}`
4. Frontend applies missed events to its state
5. Any streaming events missed during disconnect are ephemeral — the next `timeline_event.completed` will provide the final content

### Event Ordering Guarantee

**Events within a single session are delivered in order.** The full pipeline is sequential:
- Controllers create events one at a time (one goroutine per session)
- `pg_notify()` fires in transaction commit order
- `NotifyListener.receiveLoop` processes notifications sequentially
- `ConnectionManager.Broadcast` sends to each client sequentially

No ordering guarantee exists across different sessions (nor is one needed). The frontend should use `sequence_number` for display ordering and `db_event_id` for catchup tracking.

---

## Session Executor Integration

### Wiring EventPublisher

```go
// pkg/queue/executor.go — updated NewRealSessionExecutor

func NewRealSessionExecutor(
    cfg *config.Config,
    dbClient *ent.Client,
    llmClient agent.LLMClient,
    eventPublisher *events.EventPublisher, // NEW
) *RealSessionExecutor {
    // ...
    return &RealSessionExecutor{
        // ... existing fields ...
        eventPublisher: eventPublisher,
    }
}

// In executeStage — add EventPublisher to ExecutionContext:
execCtx := &agent.ExecutionContext{
    // ... existing fields ...
    EventPublisher: e.eventPublisher,
}
```

### Session Status Events

The worker publishes session status events for key lifecycle transitions:

```go
// In worker.go — after session status changes

statusPayload := map[string]interface{}{
    "type":       events.EventTypeSessionStatus,
    "session_id": session.ID,
    "status":     "in_progress",
    "timestamp":  time.Now().Format(time.RFC3339Nano),
}

// Publish to per-session channel (session detail page)
eventPublisher.Publish(ctx, session.ID, events.SessionChannel(session.ID), statusPayload)

// Publish to global sessions channel (session list page)
eventPublisher.Publish(ctx, session.ID, events.GlobalSessionsChannel, statusPayload)

// Session completed/failed/timed_out/cancelled
completedPayload := map[string]interface{}{
    "type":       events.EventTypeSessionCompleted,
    "session_id": session.ID,
    "status":     result.Status,
    "timestamp":  time.Now().Format(time.RFC3339Nano),
}

eventPublisher.Publish(ctx, session.ID, events.SessionChannel(session.ID), completedPayload)
eventPublisher.Publish(ctx, session.ID, events.GlobalSessionsChannel, completedPayload)
```

---

## Startup Sequence

```go
// cmd/tarsy/main.go — updated startup

func main() {
    // ... existing: load config, initialize DB, run migrations ...

    // Initialize services
    eventService := services.NewEventService(dbClient.Client)

    // Create EventPublisher (uses ent client for single-transaction INSERT + pg_notify)
    eventPublisher := events.NewEventPublisher(eventService, dbClient.Client)

    // Create ConnectionManager
    connManager := events.NewConnectionManager(eventService, 10*time.Second)

    // Create NotifyListener (dedicated connection for LISTEN)
    notifyListener := events.NewNotifyListener(dbConnString, connManager)
    if err := notifyListener.Start(ctx); err != nil {
        log.Fatal("Failed to start NotifyListener", "error", err)
    }
    defer notifyListener.Stop(ctx)

    // Create session executor with EventPublisher
    executor := queue.NewRealSessionExecutor(cfg, dbClient.Client, llmClient, eventPublisher)

    // Start worker pool (existing)
    workerPool := queue.NewWorkerPool(podID, dbClient.Client, cfg.Queue, executor)
    workerPool.Start(ctx)

    // Create HTTP server with WebSocket support
    httpServer := api.NewServer(cfg, dbClient, alertService, sessionService, workerPool, connManager)
    // ...
}
```

---

## NOTIFY Listener Channel Management

### Dynamic Channel Subscription

When a WebSocket client subscribes to a session channel, the ConnectionManager needs to ensure the NotifyListener is LISTENing on that PostgreSQL channel:

```go
// In ConnectionManager.subscribe()

func (m *ConnectionManager) subscribe(c *Connection, channel string) {
    // ... existing subscription tracking ...

    // If this is the first subscriber on this channel, start LISTEN
    m.channelMu.Lock()
    if _, exists := m.channels[channel]; !exists {
        m.channels[channel] = make(map[string]bool)
        // Notify the LISTEN connection
        if m.listener != nil {
            go m.listener.Subscribe(context.Background(), channel)
        }
    }
    m.channels[channel][c.ID] = true
    m.channelMu.Unlock()

    c.Subscriptions[channel] = true
}
```

When the last subscriber unsubscribes, UNLISTEN is called to free the PostgreSQL channel:

```go
func (m *ConnectionManager) unsubscribe(c *Connection, channel string) {
    m.channelMu.Lock()
    if subs, exists := m.channels[channel]; exists {
        delete(subs, c.ID)
        if len(subs) == 0 {
            delete(m.channels, channel)
            if m.listener != nil {
                go m.listener.Unsubscribe(context.Background(), channel)
            }
        }
    }
    m.channelMu.Unlock()

    delete(c.Subscriptions, channel)
}
```

---

## Error Handling

### Event Publishing Failures

Event publishing is **non-blocking** for agent execution. If a publish fails:
- The event is still persisted in the DB (for persistent events)
- A warning is logged
- Agent execution continues uninterrupted
- Clients can catch up via the REST API or catchup mechanism

```go
// Publishing is fire-and-forget from the controller's perspective
if publishErr != nil {
    slog.Warn("Failed to publish event", "event_id", eventID, "error", publishErr)
    // Don't return error — agent execution must continue
}
```

### WebSocket Send Failures

If a WebSocket send fails for a specific client:
- The error is logged
- Other clients on the same channel still receive the event
- The failing client is NOT disconnected immediately (may recover)
- If the client's connection is truly broken, the read loop will detect it and clean up

### NOTIFY Payload Size

PostgreSQL NOTIFY payload is limited to 8000 bytes. For large events:
- The publisher checks payload size before NOTIFY
- If over 7900 bytes, sends a truncated notification with `"truncated": true`
- The client fetches the full event from the REST API using the event_id

---

## Event Cleanup

Events are cleaned up with a **60-second grace period** after session completion:

1. **On session completion**: Worker schedules event cleanup with a 60-second delay using `time.AfterFunc(60 * time.Second, ...)`. This gives connected WebSocket clients time to receive the final events and complete rendering before catchup data disappears.
2. **TTL fallback**: Orphaned events older than 7 days cleaned by periodic job (existing Phase 2 behavior).

```go
// In worker.go — after session completes
time.AfterFunc(60*time.Second, func() {
    cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if err := eventService.CleanupSessionEvents(cleanupCtx, sessionID); err != nil {
        slog.Error("Failed to cleanup session events", "session_id", sessionID, "error", err)
    }
})
```

**Note**: The `timeline_events` table retains data regardless of cleanup — this only affects the `events` (notification) table used for WebSocket catchup.

---

## Configuration

### WebSocket Configuration

```yaml
# deploy/config/tarsy.yaml

streaming:
  # WebSocket write timeout for sending events to clients
  write_timeout: 10s

  # Keepalive ping interval (client-side, not configurable here)
  # Client sends ping every 30s, server responds with pong

  # Maximum WebSocket connections per pod (0 = unlimited)
  max_connections: 1000

  # Enable LLM token streaming to WebSocket clients
  enable_llm_streaming: true
```

---

## Testing Strategy

### Unit Tests

1. **ConnectionManager**:
   - Subscribe/unsubscribe tracking
   - Broadcast to correct subscribers
   - Catchup query and delivery
   - Connection lifecycle (register, unregister, cleanup)

2. **EventPublisher**:
   - Publish creates DB event + NOTIFY
   - PublishTransient sends NOTIFY without DB
   - Payload truncation for oversized events
   - Error handling (DB failure, NOTIFY failure)

3. **Event Protocol**:
   - Correct event type payloads
   - Channel naming convention
   - Timestamp formatting

### Integration Tests (Real PostgreSQL)

1. **NOTIFY/LISTEN round-trip**:
   - Publish event → NOTIFY → LISTEN → callback invoked
   - Multiple channels
   - Reconnection after connection loss

2. **Catchup mechanism**:
   - Persist events → query since lastEventID → correct events returned

3. **End-to-end**:
   - WebSocket client → subscribe → agent creates timeline event → client receives event
   - Session completion → event cleanup

### Manual Testing

- Open dashboard → submit alert → see events appear in real-time
- Open multiple tabs → all see same events
- Kill and restart server → reconnect → catchup delivers missed events

---

## Implementation Checklist

### Phase 3.4 Implementation Order

1. **Event types and protocol** (foundation):
   - [ ] `pkg/events/types.go` — event type constants, channel naming, protocol message types
   - [ ] Write tests for event type helpers

2. **EventPublisher** (publishing layer):
   - [ ] `pkg/events/publisher.go` — Publish, PublishTransient, NOTIFY integration
   - [ ] Update `pkg/services/event_service.go` if needed for `GetEventsSince` improvements
   - [ ] Write unit tests

3. **ConnectionManager** (WebSocket hub):
   - [ ] `pkg/events/manager.go` — connection tracking, subscribe/unsubscribe, broadcast, catchup
   - [ ] Write unit tests

4. **NotifyListener** (cross-pod delivery):
   - [ ] `pkg/events/listener.go` — dedicated connection, LISTEN/UNLISTEN, reconnection
   - [ ] Write integration tests with real PostgreSQL

5. **WebSocket handler**:
   - [ ] `pkg/api/handler_ws.go` — HTTP→WebSocket upgrade, client message handling
   - [ ] Register route in `pkg/api/server.go`
   - [ ] Write handler tests

6. **Controller integration**:
   - [ ] Add `EventPublisher` to `ExecutionContext`
   - [ ] Update `createTimelineEvent` helper to publish events
   - [ ] Add streaming callback support to `collectStream`
   - [ ] Update controllers (ReAct, NativeThinking, Synthesis) for streaming events
   - [ ] Write integration tests

7. **Worker integration**:
   - [ ] Publish session status events (started, completed, failed, etc.)
   - [ ] Wire EventPublisher in `queue/executor.go`

8. **Startup wiring**:
   - [ ] Update `cmd/tarsy/main.go` — initialize EventPublisher, ConnectionManager, NotifyListener
   - [ ] Pass ConnectionManager to HTTP server
   - [ ] Pass EventPublisher to session executor
   - [ ] Add streaming configuration

9. **Testing**:
   - [ ] Unit tests for all new packages
   - [ ] Integration tests with real PostgreSQL (NOTIFY/LISTEN)
   - [ ] End-to-end test: WebSocket subscribe → agent execution → event delivery
   - [ ] Reconnection and catchup tests

---

## Design Decisions

### What Changed from Old TARSy

| Aspect | Old TARSy (Python) | New TARSy (Go) | Reason |
|---|---|---|---|
| WebSocket library | FastAPI WebSockets (ASGI) | coder/websocket (RFC 6455) | Already a project dependency; integrates with Echo v5 |
| Event listener | asyncpg LISTEN (async) | pgx WaitForNotification (goroutine) | Go-native; pgx is the PostgreSQL driver already in use |
| Connection manager | Python class with asyncio | Go struct with sync.RWMutex + goroutines | Go concurrency primitives; simpler than Python async |
| Streaming content | Incremental deltas | Accumulated content per chunk | Simpler client logic; handles out-of-order delivery |
| Frontend dedup | Match streaming items to DB items by `llm_interaction_id` | Use `event_id` from TimelineEvent (created before streaming starts) | Phase 2 TimelineEvent design eliminates dedup entirely |
| Event model | Events published to both `"sessions"` and `"session:{id}"` channels | Same dual-channel model: `"sessions"` (status only) + `"session:{id}"` (all events) | Same approach; global channel carries only lightweight session status events, not timeline/streaming |
| Payload limit | No explicit handling | Truncation + `truncated` flag for >8KB payloads | PostgreSQL NOTIFY limit is 8000 bytes |

### What Stayed the Same

- Database-backed event persistence for catchup support
- PostgreSQL NOTIFY/LISTEN for cross-pod event delivery
- Dedicated LISTEN connection (not from pool)
- Transient-only streaming for LLM tokens (no per-token DB writes)
- Channel-based subscription model
- Catchup mechanism using `last_event_id`
- Event cleanup on session completion + TTL fallback
- Non-blocking event publishing (doesn't fail agent execution)

---

## Decided Against

**Redis Pub/Sub for event distribution**: Not using Redis. PostgreSQL NOTIFY/LISTEN is sufficient for expected scale (10-100 concurrent sessions, <1000 WebSocket connections). Adding Redis introduces operational complexity (additional service to deploy, monitor, and maintain) with no clear benefit at current scale. Can reconsider if PostgreSQL NOTIFY becomes a bottleneck.

**Server-Sent Events (SSE) instead of WebSocket**: Not using SSE. WebSocket provides bidirectional communication needed for subscribe/unsubscribe/catchup/ping messages. SSE is unidirectional (server → client only) and would require a separate API for client actions. WebSocket is already used in the existing dashboard code.

**Per-token DB writes during streaming**: Not writing each LLM token to the database. At ~10-50 tokens/sec across 5-15 concurrent sessions, this would be 50-750 DB writes/sec of pure overhead. Transient NOTIFY delivers the same UX. Final content written once on stream completion.

**WebSocket authentication**: Not implementing WebSocket authentication in Phase 3.4. The WebSocket endpoint is behind the same network as the REST API. Phase 7 (Security) will add OAuth2-proxy integration for both REST and WebSocket endpoints.

**Message queue between publisher and NOTIFY**: Not buffering events in an in-memory queue before NOTIFY. The NOTIFY call is fast (~1ms) and happens inline with the DB transaction for persistent events. Adding a queue would complicate error handling and add latency without clear benefit.

**Binary WebSocket protocol (Protocol Buffers over WS)**: Not using binary encoding for WebSocket messages. JSON is human-readable, easy to debug, and sufficient for the event volume (~10-100 events/sec per session). Browser DevTools can inspect JSON messages directly. Can switch to binary later if bandwidth becomes an issue.

---

## References

- Old TARSy WebSocket Controller: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/controllers/websocket_controller.py`
- Old TARSy Event System: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/events/`
- Old TARSy Streaming Publisher: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/integrations/llm/streaming.py`
- Old TARSy Frontend WebSocket: `/home/igels/Projects/AI/tarsy-bot/dashboard/src/services/websocketService.ts`
- Phase 2 Database Design (Event table): `docs/phase2-database-persistence-design.md`
- Phase 2 Queue/Worker Design (Progressive writes & streaming): `docs/phase2-queue-worker-system-design.md`
- Phase 3.1 Base Agent Architecture: `docs/phase3-base-agent-architecture-design.md`
- Phase 3.2 Iteration Controllers: `docs/phase3-iteration-controllers-design.md`
- Phase 3.3 Prompt System: `docs/phase3-prompt-system-design.md`
- coder/websocket: https://github.com/coder/websocket
- PostgreSQL NOTIFY: https://www.postgresql.org/docs/current/sql-notify.html
- pgx library: https://github.com/jackc/pgx
