package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// TimelineService manages timeline events
type TimelineService struct {
	client *ent.Client
}

// NewTimelineService creates a new TimelineService
func NewTimelineService(client *ent.Client) *TimelineService {
	return &TimelineService{client: client}
}

// CreateTimelineEvent creates a new timeline event
func (s *TimelineService) CreateTimelineEvent(httpCtx context.Context, req models.CreateTimelineEventRequest) (*ent.TimelineEvent, error) {
	// Validate request
	if req.SessionID == "" {
		return nil, NewValidationError("SessionID", "required")
	}
	if req.StageID == "" {
		return nil, NewValidationError("StageID", "required")
	}
	if req.ExecutionID == "" {
		return nil, NewValidationError("ExecutionID", "required")
	}
	if req.SequenceNumber <= 0 {
		return nil, NewValidationError("SequenceNumber", "must be positive")
	}
	if string(req.EventType) == "" {
		return nil, NewValidationError("EventType", "required")
	}
	// Content may be empty for streaming events (filled in later via UpdateTimelineEvent/CompleteTimelineEvent)

	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	eventID := uuid.New().String()
	event, err := s.client.TimelineEvent.Create().
		SetID(eventID).
		SetSessionID(req.SessionID).
		SetStageID(req.StageID).
		SetExecutionID(req.ExecutionID).
		SetSequenceNumber(req.SequenceNumber).
		SetEventType(req.EventType).
		SetStatus(timelineevent.StatusStreaming).
		SetContent(req.Content).
		SetMetadata(req.Metadata).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create timeline event: %w", err)
	}

	return event, nil
}

// UpdateTimelineEvent updates event content during streaming
func (s *TimelineService) UpdateTimelineEvent(ctx context.Context, eventID string, content string) error {
	if eventID == "" {
		return NewValidationError("eventID", "required")
	}
	if content == "" {
		return NewValidationError("content", "required")
	}

	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := s.client.TimelineEvent.UpdateOneID(eventID).
		SetContent(content).
		SetUpdatedAt(time.Now()).
		Exec(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to update timeline event: %w", err)
	}

	return nil
}

// CompleteTimelineEvent marks an event as completed and sets debug links.
// llmInteractionID and mcpInteractionID are optional debug links (pass nil if not applicable).
func (s *TimelineService) CompleteTimelineEvent(ctx context.Context, eventID string, content string, llmInteractionID *string, mcpInteractionID *string) error {
	if eventID == "" {
		return NewValidationError("eventID", "required")
	}
	if content == "" {
		return NewValidationError("content", "required")
	}

	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	update := s.client.TimelineEvent.UpdateOneID(eventID).
		SetContent(content).
		SetStatus(timelineevent.StatusCompleted).
		SetUpdatedAt(time.Now())

	if llmInteractionID != nil {
		update = update.SetLlmInteractionID(*llmInteractionID)
	}
	if mcpInteractionID != nil {
		update = update.SetMcpInteractionID(*mcpInteractionID)
	}

	err := update.Exec(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to complete timeline event: %w", err)
	}

	return nil
}

// FailTimelineEvent marks an event as failed with an error message.
// Used to clean up streaming events that were interrupted by an error.
func (s *TimelineService) FailTimelineEvent(ctx context.Context, eventID string, content string) error {
	if eventID == "" {
		return NewValidationError("eventID", "required")
	}

	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := s.client.TimelineEvent.UpdateOneID(eventID).
		SetStatus(timelineevent.StatusFailed).
		SetContent(content).
		SetUpdatedAt(time.Now()).
		Exec(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to mark timeline event as failed: %w", err)
	}

	return nil
}

// GetSessionTimeline retrieves all events for a session
func (s *TimelineService) GetSessionTimeline(ctx context.Context, sessionID string) ([]*ent.TimelineEvent, error) {
	if sessionID == "" {
		return nil, NewValidationError("sessionID", "required")
	}

	events, err := s.client.TimelineEvent.Query().
		Where(timelineevent.SessionIDEQ(sessionID)).
		Order(ent.Asc(timelineevent.FieldSequenceNumber)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get session timeline: %w", err)
	}

	return events, nil
}

// GetStageTimeline retrieves all events for a stage
func (s *TimelineService) GetStageTimeline(ctx context.Context, stageID string) ([]*ent.TimelineEvent, error) {
	if stageID == "" {
		return nil, NewValidationError("stageID", "required")
	}

	events, err := s.client.TimelineEvent.Query().
		Where(timelineevent.StageIDEQ(stageID)).
		Order(ent.Asc(timelineevent.FieldSequenceNumber)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stage timeline: %w", err)
	}

	return events, nil
}

// GetAgentTimeline retrieves all events for an agent execution
func (s *TimelineService) GetAgentTimeline(ctx context.Context, executionID string) ([]*ent.TimelineEvent, error) {
	if executionID == "" {
		return nil, NewValidationError("executionID", "required")
	}

	events, err := s.client.TimelineEvent.Query().
		Where(timelineevent.ExecutionIDEQ(executionID)).
		Order(ent.Asc(timelineevent.FieldSequenceNumber)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent timeline: %w", err)
	}

	return events, nil
}
