package events

import (
	"context"

	"github.com/codeready-toolchain/tarsy/ent"
)

// eventQuerier abstracts the event query method needed by EventServiceAdapter.
// Implemented by *services.EventService.
type eventQuerier interface {
	GetEventsSince(ctx context.Context, channel string, sinceID, limit int) ([]*ent.Event, error)
}

// EventServiceAdapter wraps an eventQuerier to implement CatchupQuerier.
type EventServiceAdapter struct {
	querier eventQuerier
}

// NewEventServiceAdapter creates a CatchupQuerier from an EventService.
func NewEventServiceAdapter(es eventQuerier) *EventServiceAdapter {
	return &EventServiceAdapter{querier: es}
}

// GetCatchupEvents queries events since sinceID up to limit for the catchup mechanism.
func (a *EventServiceAdapter) GetCatchupEvents(ctx context.Context, channel string, sinceID, limit int) ([]CatchupEvent, error) {
	events, err := a.querier.GetEventsSince(ctx, channel, sinceID, limit)
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
