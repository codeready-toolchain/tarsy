package events

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// EventServiceAdapter wraps services.EventService to implement CatchupQuerier.
type EventServiceAdapter struct {
	eventService *services.EventService
}

// NewEventServiceAdapter creates a CatchupQuerier from an EventService.
func NewEventServiceAdapter(es *services.EventService) *EventServiceAdapter {
	return &EventServiceAdapter{eventService: es}
}

// GetCatchupEvents queries events since sinceID up to limit for the catchup mechanism.
func (a *EventServiceAdapter) GetCatchupEvents(ctx context.Context, channel string, sinceID, limit int) ([]CatchupEvent, error) {
	events, err := a.eventService.GetEventsSince(ctx, channel, sinceID, limit)
	if err != nil {
		return nil, err
	}

	result := make([]CatchupEvent, len(events))
	for i, evt := range events {
		result[i] = CatchupEvent{
			ID:      evt.ID,
			Payload: evt.Payload,
		}
	}
	return result, nil
}
