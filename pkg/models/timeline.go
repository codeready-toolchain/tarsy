package models

import (
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

// CreateTimelineEventRequest contains fields for creating a timeline event
type CreateTimelineEventRequest struct {
	SessionID      string                  `json:"session_id"`
	StageID        string                  `json:"stage_id"`
	ExecutionID    string                  `json:"execution_id"`
	SequenceNumber int                     `json:"sequence_number"`
	EventType      timelineevent.EventType `json:"event_type"`
	Content        string                  `json:"content"`
	Metadata       map[string]any          `json:"metadata,omitempty"`
}

// TimelineEventResponse wraps a TimelineEvent
type TimelineEventResponse struct {
	*ent.TimelineEvent
}
