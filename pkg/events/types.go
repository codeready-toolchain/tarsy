// Package events provides real-time event delivery via WebSocket and
// PostgreSQL NOTIFY/LISTEN for cross-pod distribution.
//
// ════════════════════════════════════════════════════════════════
// Timeline Event Lifecycle Patterns
// ════════════════════════════════════════════════════════════════
//
// Timeline events follow one of two lifecycle patterns. Clients
// differentiate them by the "status" field in the created payload.
//
// Pattern 1 — STREAMING (status: "streaming"):
//
//   timeline_event.created   {status: "streaming", content: ""}
//   stream.chunk             {delta: "..."}  (repeated, not persisted)
//   timeline_event.completed {status: "completed", content: "full text"}
//
//   The event is created empty while the LLM is still producing output.
//   Deltas arrive via stream.chunk (transient — lost on reconnect, but
//   the final content is delivered by the completed event). Clients
//   concatenate deltas locally for a live typing effect.
//
//   Event types using this pattern:
//     - llm_thinking  (NativeThinking strategy — thinking text streams)
//     - llm_response  (all strategies — assistant text streams)
//     - llm_tool_call (tool execution in progress → completed with result)
//     - mcp_tool_summary (summarization LLM call streams)
//
// Pattern 2 — FIRE-AND-FORGET (status: "completed"):
//
//   timeline_event.created   {status: "completed", content: "full text"}
//
//   The event is created with its final content in a single message.
//   There is NO subsequent timeline_event.completed — this IS the
//   terminal state. Clients should render the content immediately.
//
//   Event types using this pattern:
//     - final_analysis   (all strategies — the agent's conclusion)
//     - llm_thinking     (ReAct strategy only — thought is parsed from
//                         the llm_response text after the stream ends,
//                         not itself streamed)
//
// Note: the same event_type (llm_thinking) follows different patterns
// depending on the iteration strategy. The "status" field is the only
// reliable discriminator.
//
// Note: executive_summary is a DB-only timeline event. It is NOT
// published via WebSocket. See pkg/queue/executor.go for details.
//
// ════════════════════════════════════════════════════════════════
package events

// Persistent event types (stored in DB + NOTIFY).
const (
	// Timeline event lifecycle — see package doc for the two lifecycle patterns.
	EventTypeTimelineCreated   = "timeline_event.created"
	EventTypeTimelineCompleted = "timeline_event.completed"

	// Session lifecycle
	EventTypeSessionStatus = "session.status"

	// Stage lifecycle — single event type for all stage status transitions
	EventTypeStageStatus = "stage.status"
)

// Stage lifecycle status values (used in StageStatusPayload.Status).
const (
	StageStatusStarted   = "started"
	StageStatusCompleted = "completed"
	StageStatusFailed    = "failed"
	StageStatusTimedOut  = "timed_out"
	StageStatusCancelled = "cancelled"
)

// Chat event types (stored in DB + NOTIFY).
const (
	EventTypeChatCreated     = "chat.created"
	EventTypeChatUserMessage = "chat.user_message"
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
	Action      string `json:"action"`                  // "subscribe", "unsubscribe", "catchup", "ping"
	Channel     string `json:"channel,omitempty"`       // Channel name (e.g., "session:abc-123")
	LastEventID *int   `json:"last_event_id,omitempty"` // For catchup
}
