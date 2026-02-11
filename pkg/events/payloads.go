package events

// TimelineCreatedPayload is the payload for timeline_event.created events.
// Published when a new timeline event is created (streaming or completed).
type TimelineCreatedPayload struct {
	Type           string         `json:"type"`                   // always EventTypeTimelineCreated
	EventID        string         `json:"event_id"`               // timeline event UUID
	SessionID      string         `json:"session_id"`             // owning session
	StageID        string         `json:"stage_id,omitempty"`     // owning stage (empty for session-level events)
	ExecutionID    string         `json:"execution_id,omitempty"` // owning agent execution (empty for session-level events)
	EventType      string         `json:"event_type"`             // e.g. "llm_thinking", "llm_tool_call"
	Status         string         `json:"status"`                 // "streaming" or "completed"
	Content        string         `json:"content"`                // event content (may be empty for streaming)
	Metadata       map[string]any `json:"metadata,omitempty"`
	SequenceNumber int            `json:"sequence_number"` // order in timeline
	Timestamp      string         `json:"timestamp"`       // RFC3339Nano
}

// TimelineCompletedPayload is the payload for timeline_event.completed events.
// Published when a streaming timeline event transitions to a terminal status.
type TimelineCompletedPayload struct {
	Type      string         `json:"type"`     // always EventTypeTimelineCompleted
	EventID   string         `json:"event_id"` // timeline event UUID
	Content   string         `json:"content"`  // final content
	Status    string         `json:"status"`   // "completed" or "failed"
	Metadata  map[string]any `json:"metadata,omitempty"`
	Timestamp string         `json:"timestamp"` // RFC3339Nano
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
	Type      string `json:"type"`       // always EventTypeSessionStatus
	SessionID string `json:"session_id"` // session UUID
	Status    string `json:"status"`     // new status (e.g. "in_progress", "completed")
	Timestamp string `json:"timestamp"`  // RFC3339Nano
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
