package events

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEventQuerier implements eventQuerier for testing the adapter.
type mockEventQuerier struct {
	events []*ent.Event
	err    error
}

func (m *mockEventQuerier) GetEventsSince(_ context.Context, _ string, _ int, limit int) ([]*ent.Event, error) {
	if m.err != nil {
		return nil, m.err
	}
	if limit > 0 && len(m.events) > limit {
		return m.events[:limit], nil
	}
	return m.events, nil
}

func TestEventServiceAdapter_GetCatchupEvents(t *testing.T) {
	// Test that the adapter correctly maps ent.Event fields to CatchupEvent.
	querier := &mockEventQuerier{
		events: []*ent.Event{
			{ID: 10, Payload: map[string]interface{}{"type": "timeline_event.created", "seq": float64(1)}},
			{ID: 20, Payload: map[string]interface{}{"type": "stream.chunk", "seq": float64(2)}},
		},
	}

	adapter := NewEventServiceAdapter(querier)
	events, err := adapter.GetCatchupEvents(context.Background(), "session:test", 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)

	// Verify ID mapping
	assert.Equal(t, 10, events[0].ID)
	assert.Equal(t, 20, events[1].ID)

	// Verify Payload mapping
	assert.Equal(t, "timeline_event.created", events[0].Payload["type"])
	assert.Equal(t, float64(1), events[0].Payload["seq"])
	assert.Equal(t, "stream.chunk", events[1].Payload["type"])
	assert.Equal(t, float64(2), events[1].Payload["seq"])
}

func TestEventServiceAdapter_GetCatchupEvents_WithLimit(t *testing.T) {
	querier := &mockEventQuerier{
		events: []*ent.Event{
			{ID: 1, Payload: map[string]interface{}{"seq": float64(1)}},
			{ID: 2, Payload: map[string]interface{}{"seq": float64(2)}},
			{ID: 3, Payload: map[string]interface{}{"seq": float64(3)}},
		},
	}

	adapter := NewEventServiceAdapter(querier)
	events, err := adapter.GetCatchupEvents(context.Background(), "session:test", 0, 2)
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, 1, events[0].ID)
	assert.Equal(t, 2, events[1].ID)
}

func TestEventServiceAdapter_GetCatchupEvents_Error(t *testing.T) {
	querier := &mockEventQuerier{
		err: fmt.Errorf("database connection lost"),
	}

	adapter := NewEventServiceAdapter(querier)
	events, err := adapter.GetCatchupEvents(context.Background(), "session:test", 0, 10)
	assert.Error(t, err)
	assert.Nil(t, events)
	assert.Contains(t, err.Error(), "database connection lost")
}

func TestEventServiceAdapter_GetCatchupEvents_Empty(t *testing.T) {
	querier := &mockEventQuerier{
		events: []*ent.Event{},
	}

	adapter := NewEventServiceAdapter(querier)
	events, err := adapter.GetCatchupEvents(context.Background(), "session:test", 0, 10)
	require.NoError(t, err)
	assert.Empty(t, events)
}
