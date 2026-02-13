package events

import (
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

// TimelineCreatedPayload is the payload for timeline_event.created events.
// Published when a new timeline event is created (streaming or completed).
type TimelineCreatedPayload struct {
	Type           string                  `json:"type"`                   // always EventTypeTimelineCreated
	EventID        string                  `json:"event_id"`               // timeline event UUID
	SessionID      string                  `json:"session_id"`             // owning session
	StageID        string                  `json:"stage_id,omitempty"`     // owning stage (empty for session-level events)
	ExecutionID    string                  `json:"execution_id,omitempty"` // owning agent execution (empty for session-level events)
	EventType      timelineevent.EventType `json:"event_type"`             // llm_thinking, llm_response, llm_tool_call, mcp_tool_summary, etc.
	Status         timelineevent.Status    `json:"status"`                 // streaming, completed, failed, cancelled, timed_out
	Content        string                  `json:"content"`                // event content (may be empty for streaming)
	Metadata       map[string]any          `json:"metadata,omitempty"`
	SequenceNumber int                     `json:"sequence_number"` // order in timeline
	Timestamp      string                  `json:"timestamp"`       // RFC3339Nano
}

// TimelineCompletedPayload is the payload for timeline_event.completed events.
// Published when a streaming timeline event transitions to a terminal status.
type TimelineCompletedPayload struct {
	Type      string                  `json:"type"`       // always EventTypeTimelineCompleted
	EventID   string                  `json:"event_id"`   // timeline event UUID
	EventType timelineevent.EventType `json:"event_type"` // llm_thinking, llm_response, llm_tool_call, etc.
	Content   string                  `json:"content"`    // final content
	Status    timelineevent.Status    `json:"status"`     // completed, failed, cancelled, timed_out
	Metadata  map[string]any          `json:"metadata,omitempty"`
	Timestamp string                  `json:"timestamp"` // RFC3339Nano
}

// StreamChunkPayload is the payload for stream.chunk transient events.
// Published for each LLM streaming token — high frequency, ephemeral.
type StreamChunkPayload struct {
	Type      string `json:"type"`      // always EventTypeStreamChunk
	EventID   string `json:"event_id"`  // parent timeline event UUID
	Delta     string `json:"delta"`     // incremental text chunk
	Timestamp string `json:"timestamp"` // RFC3339Nano
}

// SessionStatusPayload is the payload for session.status events.
// Published when a session transitions between lifecycle states.
type SessionStatusPayload struct {
	Type      string              `json:"type"`       // always EventTypeSessionStatus
	SessionID string              `json:"session_id"` // session UUID
	Status    alertsession.Status `json:"status"`     // pending, in_progress, cancelling, completed, failed, cancelled, timed_out
	Timestamp string              `json:"timestamp"`  // RFC3339Nano
}

// StageStatusPayload is the payload for stage.status events.
// Single event type for all stage lifecycle transitions (started, completed, failed, etc.).
type StageStatusPayload struct {
	Type       string `json:"type"`               // always EventTypeStageStatus
	SessionID  string `json:"session_id"`         // session UUID
	StageID    string `json:"stage_id,omitempty"` // may be empty on "started" if stage creation hasn't happened yet
	StageName  string `json:"stage_name"`         // human-readable stage name from config
	StageIndex int    `json:"stage_index"`        // 1-based
	Status     string `json:"status"`             // started, completed, failed, timed_out, cancelled
	Timestamp  string `json:"timestamp"`          // RFC3339Nano
}

// ChatCreatedPayload is the payload for chat.created events.
// Published when the first message creates a new chat for a session.
type ChatCreatedPayload struct {
	Type      string `json:"type"`       // always EventTypeChatCreated
	SessionID string `json:"session_id"` // owning session UUID
	ChatID    string `json:"chat_id"`    // new chat UUID
	CreatedBy string `json:"created_by"` // author who initiated the chat
	Timestamp string `json:"timestamp"`  // RFC3339Nano
}

// InteractionCreatedPayload is the payload for interaction.created events.
// Fired once when an LLM or MCP interaction record is saved to DB.
// Used by trace view for live updates via event-notification + REST re-fetch.
type InteractionCreatedPayload struct {
	Type            string `json:"type"`                       // always EventTypeInteractionCreated
	SessionID       string `json:"session_id"`                 // session UUID
	StageID         string `json:"stage_id,omitempty"`         // stage UUID (empty for session-level, e.g. executive summary)
	ExecutionID     string `json:"execution_id,omitempty"`     // execution UUID (empty for session-level)
	InteractionID   string `json:"interaction_id"`             // LLM or MCP interaction UUID
	InteractionType string `json:"interaction_type"`           // "llm" or "mcp"
	Timestamp       string `json:"timestamp"`                  // RFC3339Nano
}

// SessionProgressPayload is the payload for session.progress transient events.
// Published to GlobalSessionsChannel for the active alerts panel.
type SessionProgressPayload struct {
	Type              string `json:"type"`                // always EventTypeSessionProgress
	SessionID         string `json:"session_id"`          // session UUID
	CurrentStageName  string `json:"current_stage_name"`  // human-readable stage name
	CurrentStageIndex int    `json:"current_stage_index"` // 1-based
	TotalStages       int    `json:"total_stages"`        // total configured stages
	ActiveExecutions  int    `json:"active_executions"`   // number of agents running
	StatusText        string `json:"status_text"`         // human-readable status
	Timestamp         string `json:"timestamp"`           // RFC3339Nano
}

// ExecutionProgressPayload is the payload for execution.progress transient events.
// Published to SessionChannel(sessionID) for per-agent progress display.
type ExecutionProgressPayload struct {
	Type        string `json:"type"`         // always EventTypeExecutionProgress
	SessionID   string `json:"session_id"`   // session UUID
	StageID     string `json:"stage_id"`     // stage UUID
	ExecutionID string `json:"execution_id"` // agent execution UUID
	Phase       string `json:"phase"`        // ProgressPhase constant
	Message     string `json:"message"`      // human-readable message
	Timestamp   string `json:"timestamp"`    // RFC3339Nano
}
