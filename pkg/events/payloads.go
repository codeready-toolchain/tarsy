package events

import (
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

// BasePayload contains fields required by ALL event payloads.
//
// The frontend WebSocket client routes incoming events by inspecting
// data.session_id in the JSON payload (see websocket.ts handleEvent).
// Any payload missing session_id is silently dropped by the frontend.
// Embedding BasePayload in every payload struct prevents this bug class.
type BasePayload struct {
	Type      string `json:"type"`       // event type constant (e.g. EventTypeTimelineCreated)
	SessionID string `json:"session_id"` // owning session — REQUIRED for frontend WS routing
	Timestamp string `json:"timestamp"`  // RFC3339Nano
}

// TimelineCreatedPayload is the payload for timeline_event.created events.
// Published when a new timeline event is created (streaming or completed).
type TimelineCreatedPayload struct {
	BasePayload
	EventID           string                  `json:"event_id"`                          // timeline event UUID
	StageID           string                  `json:"stage_id,omitempty"`                // owning stage (empty for session-level events)
	ExecutionID       string                  `json:"execution_id,omitempty"`            // owning agent execution (empty for session-level events)
	ParentExecutionID string                  `json:"parent_execution_id,omitempty"`     // parent orchestrator execution (empty for non-sub-agents)
	EventType         timelineevent.EventType `json:"event_type"`                        // llm_thinking, llm_response, llm_tool_call, mcp_tool_summary, etc.
	Status            timelineevent.Status    `json:"status"`                            // streaming, completed, failed, cancelled, timed_out
	Content           string                  `json:"content"`                           // event content (may be empty for streaming)
	Metadata          map[string]any          `json:"metadata,omitempty"`
	SequenceNumber    int                     `json:"sequence_number"`                   // order in timeline
}

// TimelineCompletedPayload is the payload for timeline_event.completed events.
// Published when a streaming timeline event transitions to a terminal status.
type TimelineCompletedPayload struct {
	BasePayload
	EventID           string                  `json:"event_id"`                      // timeline event UUID
	ParentExecutionID string                  `json:"parent_execution_id,omitempty"` // parent orchestrator execution (empty for non-sub-agents)
	EventType         timelineevent.EventType `json:"event_type"`                    // llm_thinking, llm_response, llm_tool_call, etc.
	Content           string                  `json:"content"`                       // final content
	Status            timelineevent.Status    `json:"status"`                        // completed, failed, cancelled, timed_out
	Metadata          map[string]any          `json:"metadata,omitempty"`
}

// StreamChunkPayload is the payload for stream.chunk transient events.
// Published for each LLM streaming token — high frequency, ephemeral.
type StreamChunkPayload struct {
	BasePayload
	EventID           string `json:"event_id"`                      // parent timeline event UUID
	ParentExecutionID string `json:"parent_execution_id,omitempty"` // parent orchestrator execution (empty for non-sub-agents)
	Delta             string `json:"delta"`                         // incremental text chunk
}

// SessionStatusPayload is the payload for session.status events.
// Published when a session transitions between lifecycle states.
type SessionStatusPayload struct {
	BasePayload
	Status alertsession.Status `json:"status"` // pending, in_progress, cancelling, completed, failed, cancelled, timed_out
}

// StageStatusPayload is the payload for stage.status events.
// Single event type for all stage lifecycle transitions (started, completed, failed, etc.).
type StageStatusPayload struct {
	BasePayload
	StageID    string `json:"stage_id,omitempty"` // may be empty on "started" if stage creation hasn't happened yet
	StageName  string `json:"stage_name"`         // human-readable stage name from config
	StageIndex int    `json:"stage_index"`        // 1-based
	Status     string `json:"status"`             // started, completed, failed, timed_out, cancelled
}

// ChatCreatedPayload is the payload for chat.created events.
// Published when the first message creates a new chat for a session.
type ChatCreatedPayload struct {
	BasePayload
	ChatID    string `json:"chat_id"`    // new chat UUID
	CreatedBy string `json:"created_by"` // author who initiated the chat
}

// InteractionCreatedPayload is the payload for interaction.created events.
// Fired once when an LLM or MCP interaction record is saved to DB.
// Used by trace view for live updates via event-notification + REST re-fetch.
type InteractionCreatedPayload struct {
	BasePayload
	StageID         string `json:"stage_id,omitempty"`     // stage UUID (empty for session-level, e.g. executive summary)
	ExecutionID     string `json:"execution_id,omitempty"` // execution UUID (empty for session-level)
	InteractionID   string `json:"interaction_id"`         // LLM or MCP interaction UUID
	InteractionType string `json:"interaction_type"`       // "llm" or "mcp"
}

// SessionProgressPayload is the payload for session.progress transient events.
// Published to GlobalSessionsChannel for the active alerts panel.
type SessionProgressPayload struct {
	BasePayload
	CurrentStageName  string `json:"current_stage_name"`  // human-readable stage name
	CurrentStageIndex int    `json:"current_stage_index"` // 1-based
	TotalStages       int    `json:"total_stages"`        // total configured stages
	ActiveExecutions  int    `json:"active_executions"`   // number of agents running
	StatusText        string `json:"status_text"`         // human-readable status
}

// ExecutionProgressPayload is the payload for execution.progress transient events.
// Published to SessionChannel(sessionID) for per-agent progress display.
type ExecutionProgressPayload struct {
	BasePayload
	StageID           string `json:"stage_id"`                      // stage UUID
	ExecutionID       string `json:"execution_id"`                  // agent execution UUID
	ParentExecutionID string `json:"parent_execution_id,omitempty"` // parent orchestrator execution (empty for non-sub-agents)
	Phase             string `json:"phase"`                         // ProgressPhase constant
	Message           string `json:"message"`                       // human-readable message
}

// ExecutionStatusPayload is the payload for execution.status transient events.
// Published to SessionChannel(sessionID) when an agent execution transitions
// to a new status. Allows the frontend to update individual agent cards
// independently of stage completion.
type ExecutionStatusPayload struct {
	BasePayload
	StageID           string `json:"stage_id"`                      // stage UUID
	ExecutionID       string `json:"execution_id"`                  // agent execution UUID
	ParentExecutionID string `json:"parent_execution_id,omitempty"` // parent orchestrator execution (empty for non-sub-agents)
	AgentIndex        int    `json:"agent_index"`                   // 1-based index preserving chain config order
	Status            string `json:"status"`                        // active, completed, failed, timed_out, cancelled
	ErrorMessage      string `json:"error_message,omitempty"`       // populated on failure
}
