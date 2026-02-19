package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSessionService(t *testing.T) (*database.Client, *services.SessionService) {
	t.Helper()
	client := testdb.NewTestClient(t)
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{
		"k8s-analysis": {
			AlertTypes: []string{"kubernetes"},
			Stages: []config.StageConfig{
				{
					Name:   "analysis",
					Agents: []config.StageAgentConfig{{Name: "KubernetesAgent"}},
				},
			},
		},
	})
	mcpRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{})
	return client, services.NewSessionService(client.Client, chainRegistry, mcpRegistry)
}

func TestService_SoftDeletesOldCompletedSessions(t *testing.T) {
	client, sessionService := setupSessionService(t)
	eventService := services.NewEventService(client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	err = client.AlertSession.UpdateOneID(session.ID).
		SetStatus(alertsession.StatusCompleted).
		SetCompletedAt(time.Now().Add(-400 * 24 * time.Hour)).
		Exec(ctx)
	require.NoError(t, err)

	cfg := &config.RetentionConfig{
		SessionRetentionDays: 365,
		EventTTL:             1 * time.Hour,
		CleanupInterval:      1 * time.Hour,
	}
	svc := NewService(cfg, sessionService, eventService)
	svc.runAll(ctx)

	updated, err := sessionService.GetSession(ctx, session.ID, false)
	require.NoError(t, err)
	assert.NotNil(t, updated.DeletedAt)
}

func TestService_SoftDeletesOldPendingSessions(t *testing.T) {
	client, sessionService := setupSessionService(t)
	eventService := services.NewEventService(client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test-pending",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	err = client.AlertSession.UpdateOneID(session.ID).
		SetCreatedAt(time.Now().Add(-400 * 24 * time.Hour)).
		Exec(ctx)
	require.NoError(t, err)

	cfg := &config.RetentionConfig{
		SessionRetentionDays: 365,
		EventTTL:             1 * time.Hour,
		CleanupInterval:      1 * time.Hour,
	}
	svc := NewService(cfg, sessionService, eventService)
	svc.runAll(ctx)

	updated, err := sessionService.GetSession(ctx, session.ID, false)
	require.NoError(t, err)
	assert.NotNil(t, updated.DeletedAt)
}

func TestService_PreservesRecentSessions(t *testing.T) {
	client, sessionService := setupSessionService(t)
	eventService := services.NewEventService(client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test-recent",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	err = client.AlertSession.UpdateOneID(session.ID).
		SetStatus(alertsession.StatusCompleted).
		SetCompletedAt(time.Now()).
		Exec(ctx)
	require.NoError(t, err)

	cfg := &config.RetentionConfig{
		SessionRetentionDays: 365,
		EventTTL:             1 * time.Hour,
		CleanupInterval:      1 * time.Hour,
	}
	svc := NewService(cfg, sessionService, eventService)
	svc.runAll(ctx)

	updated, err := sessionService.GetSession(ctx, session.ID, false)
	require.NoError(t, err)
	assert.Nil(t, updated.DeletedAt)
}

func TestService_CleansUpOldEvents(t *testing.T) {
	client, sessionService := setupSessionService(t)
	eventService := services.NewEventService(client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test-events",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Create an old event (2 hours ago)
	_, err = client.Event.Create().
		SetSessionID(session.ID).
		SetChannel("test").
		SetPayload(map[string]any{}).
		SetCreatedAt(time.Now().Add(-2 * time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	// Create a recent event
	_, err = client.Event.Create().
		SetSessionID(session.ID).
		SetChannel("test").
		SetPayload(map[string]any{}).
		SetCreatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	cfg := &config.RetentionConfig{
		SessionRetentionDays: 365,
		EventTTL:             1 * time.Hour,
		CleanupInterval:      1 * time.Hour,
	}
	svc := NewService(cfg, sessionService, eventService)
	svc.runAll(ctx)

	events, err := eventService.GetEventsSince(ctx, "test", 0, 0)
	require.NoError(t, err)
	assert.Len(t, events, 1, "old event should be deleted, recent event preserved")
}
