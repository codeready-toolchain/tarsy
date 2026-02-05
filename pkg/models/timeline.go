package models

import "github.com/codeready-toolchain/tarsy/ent"

// CreateTimelineEventRequest contains fields for creating a timeline event
type CreateTimelineEventRequest struct {
	SessionID      string         `json:"session_id"`
	StageID        string         `json:"stage_id"`
	ExecutionID    string         `json:"execution_id"`
	SequenceNumber int            `json:"sequence_number"`
	EventType      string         `json:"event_type"`
	Content        string         `json:"content"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// UpdateTimelineEventRequest contains fields for updating event during streaming
type UpdateTimelineEventRequest struct {
	Content string `json:"content"`
}

// CompleteTimelineEventRequest contains fields for completing a timeline event
type CompleteTimelineEventRequest struct {
	Content          string  `json:"content"`
	LLMInteractionID *string `json:"llm_interaction_id,omitempty"`
	MCPInteractionID *string `json:"mcp_interaction_id,omitempty"`
}

// TimelineEventResponse wraps a TimelineEvent
type TimelineEventResponse struct {
	*ent.TimelineEvent
}
