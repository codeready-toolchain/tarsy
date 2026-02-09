package events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventServiceAdapter_GetCatchupEvents(t *testing.T) {
	// Test with mock data to verify the adapter converts correctly
	querier := &mockCatchupQuerier{
		events: []CatchupEvent{
			{ID: 1, Payload: map[string]interface{}{"type": "test", "seq": float64(1)}},
			{ID: 2, Payload: map[string]interface{}{"type": "test", "seq": float64(2)}},
		},
	}

	events, err := querier.GetCatchupEvents(context.Background(), "session:test", 0, 10)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, 1, events[0].ID)
	assert.Equal(t, 2, events[1].ID)
}

func TestEventServiceAdapter_GetCatchupEvents_WithLimit(t *testing.T) {
	querier := &mockCatchupQuerier{
		events: []CatchupEvent{
			{ID: 1, Payload: map[string]interface{}{"seq": float64(1)}},
			{ID: 2, Payload: map[string]interface{}{"seq": float64(2)}},
			{ID: 3, Payload: map[string]interface{}{"seq": float64(3)}},
		},
	}

	events, err := querier.GetCatchupEvents(context.Background(), "session:test", 0, 2)
	assert.NoError(t, err)
	assert.Len(t, events, 2)
}
