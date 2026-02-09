// Package events provides real-time event delivery via WebSocket and
// PostgreSQL NOTIFY/LISTEN for cross-pod distribution.
package events

// Persistent event types (stored in DB + NOTIFY).
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

// Transient event types (NOTIFY only, no DB persistence).
const (
	// LLM streaming chunks — high-frequency, ephemeral.
	EventTypeStreamChunk = "stream.chunk"
)

// GlobalSessionsChannel is the channel for session-level status events.
// The session list page subscribes to this for real-time updates.
const GlobalSessionsChannel = "sessions"

// SessionChannel returns the channel name for a specific session's events.
// Format: "session:{session_id}"
func SessionChannel(sessionID string) string {
	return "session:" + sessionID
}

// ClientMessage is the JSON structure for client → server WebSocket messages.
type ClientMessage struct {
	Action      string `json:"action"`                 // "subscribe", "unsubscribe", "catchup", "ping"
	Channel     string `json:"channel,omitempty"`       // Channel name (e.g., "session:abc-123")
	LastEventID *int   `json:"last_event_id,omitempty"` // For catchup
}
