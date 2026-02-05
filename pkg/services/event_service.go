package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/event"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// EventService manages WebSocket event distribution
type EventService struct {
	client *ent.Client
}

// NewEventService creates a new EventService
func NewEventService(client *ent.Client) *EventService {
	return &EventService{client: client}
}

// CreateEvent creates a new event
func (s *EventService) CreateEvent(httpCtx context.Context, req models.CreateEventRequest) (*ent.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	evt, err := s.client.Event.Create().
		SetSessionID(req.SessionID).
		SetChannel(req.Channel).
		SetPayload(req.Payload).
		SetCreatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}

	return evt, nil
}

// GetEventsSince retrieves events since a given ID
func (s *EventService) GetEventsSince(ctx context.Context, channel string, sinceID int) ([]*ent.Event, error) {
	events, err := s.client.Event.Query().
		Where(
			event.ChannelEQ(channel),
			event.IDGT(sinceID),
		).
		Order(ent.Asc(event.FieldID)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	return events, nil
}

// CleanupSessionEvents removes all events for a session
func (s *EventService) CleanupSessionEvents(ctx context.Context, sessionID string) (int, error) {
	writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	count, err := s.client.Event.Delete().
		Where(event.SessionIDEQ(sessionID)).
		Exec(writeCtx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup session events: %w", err)
	}

	return count, nil
}

// CleanupOrphanedEvents removes events older than TTL
func (s *EventService) CleanupOrphanedEvents(ctx context.Context, ttlDays int) (int, error) {
	cutoff := time.Now().Add(-time.Duration(ttlDays) * 24 * time.Hour)

	writeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := s.client.Event.Delete().
		Where(event.CreatedAtLT(cutoff)).
		Exec(writeCtx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned events: %w", err)
	}

	return count, nil
}
