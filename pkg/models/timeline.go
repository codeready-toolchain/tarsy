package models

import (
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

// CreateTimelineEventRequest contains fields for creating a timeline event.
// StageID and ExecutionID are nil for session-level events (e.g. executive_summary).
type CreateTimelineEventRequest struct {
	SessionID      string                  `json:"session_id"`
	StageID        *string                 `json:"stage_id,omitempty"`
	ExecutionID    *string                 `json:"execution_id,omitempty"`
	SequenceNumber int                     `json:"sequence_number"`
	EventType      timelineevent.EventType `json:"event_type"`
	Content        string                  `json:"content"`
	Metadata       map[string]any          `json:"metadata,omitempty"`
}

// TimelineEventResponse wraps a TimelineEvent
type TimelineEventResponse struct {
	*ent.TimelineEvent
}
