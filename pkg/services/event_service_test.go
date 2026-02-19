package services

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventService_CreateEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	eventService := NewEventService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	t.Run("creates event successfully", func(t *testing.T) {
		req := models.CreateEventRequest{
			SessionID: session.ID,
			Channel:   "session:" + session.ID,
			Payload:   map[string]any{"type": "update", "data": "test"},
		}

		event, err := eventService.CreateEvent(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.Channel, event.Channel)
		assert.NotNil(t, event.Payload)
		assert.NotNil(t, event.CreatedAt)
	})
}

func TestEventService_GetEventsSince(t *testing.T) {
	client := testdb.NewTestClient(t)
	eventService := NewEventService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	channel := "session:" + session.ID

	// Create events
	evt1, err := eventService.CreateEvent(ctx, models.CreateEventRequest{
		SessionID: session.ID,
		Channel:   channel,
		Payload:   map[string]any{"seq": 1},
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	evt2, err := eventService.CreateEvent(ctx, models.CreateEventRequest{
		SessionID: session.ID,
		Channel:   channel,
		Payload:   map[string]any{"seq": 2},
	})
	require.NoError(t, err)

	t.Run("retrieves events since ID", func(t *testing.T) {
		events, err := eventService.GetEventsSince(ctx, channel, evt1.ID, 0)
		require.NoError(t, err)
		assert.Len(t, events, 1)
		assert.Equal(t, evt2.ID, events[0].ID)
	})

	t.Run("retrieves all events when sinceID is 0", func(t *testing.T) {
		events, err := eventService.GetEventsSince(ctx, channel, 0, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(events), 2)
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		events, err := eventService.GetEventsSince(ctx, channel, 0, 1)
		require.NoError(t, err)
		assert.Len(t, events, 1)
	})
}

func TestEventService_CleanupSessionEvents(t *testing.T) {
	client := testdb.NewTestClient(t)
	eventService := NewEventService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Create events
	for i := 0; i < 3; i++ {
		_, err := eventService.CreateEvent(ctx, models.CreateEventRequest{
			SessionID: session.ID,
			Channel:   "session:" + session.ID,
			Payload:   map[string]any{"seq": i},
		})
		require.NoError(t, err)
	}

	t.Run("cleans up all session events", func(t *testing.T) {
		count, err := eventService.CleanupSessionEvents(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, count)

		// Verify deleted
		events, err := eventService.GetEventsSince(ctx, "session:"+session.ID, 0, 0)
		require.NoError(t, err)
		assert.Len(t, events, 0)
	})
}

func TestEventService_CleanupOrphanedEvents(t *testing.T) {
	client := testdb.NewTestClient(t)
	eventService := NewEventService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Create event directly with old created_at (bypassing service)
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	_, err = client.Event.Create().
		SetSessionID(session.ID).
		SetChannel("test").
		SetPayload(map[string]any{}).
		SetCreatedAt(oldTime).
		Save(ctx)
	require.NoError(t, err)

	t.Run("cleans up old events", func(t *testing.T) {
		count, err := eventService.CleanupOrphanedEvents(ctx, 7*24*time.Hour)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1)
	})
}
