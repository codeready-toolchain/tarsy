package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSessionService(t *testing.T) {
	client := testdb.NewTestClient(t)

	t.Run("panics when chainRegistry is nil", func(t *testing.T) {
		mcpRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{})
		assert.Panics(t, func() {
			NewSessionService(client.Client, nil, mcpRegistry)
		})
	})

	t.Run("panics when mcpServerRegistry is nil", func(t *testing.T) {
		chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{})
		assert.Panics(t, func() {
			NewSessionService(client.Client, chainRegistry, nil)
		})
	})

	t.Run("succeeds with valid registries", func(t *testing.T) {
		chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{})
		mcpRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{})
		service := NewSessionService(client.Client, chainRegistry, mcpRegistry)
		assert.NotNil(t, service)
	})
}

func TestSessionService_CreateSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("creates session with initial stage and agent", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert data",
			AgentType: "kubernetes",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			Author:    "test@example.com",
		}

		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.SessionID, session.ID)
		assert.Equal(t, req.AlertData, session.AlertData)
		assert.Equal(t, req.AgentType, session.AgentType)
		assert.Equal(t, alertsession.StatusPending, session.Status)
		assert.NotZero(t, session.CreatedAt, "created_at should be set at submission")
		assert.Nil(t, session.StartedAt, "started_at should be nil until worker claims session")
		assert.NotNil(t, session.CurrentStageIndex)
		assert.Equal(t, 0, *session.CurrentStageIndex)

		// Verify stage created
		stages, err := client.Stage.Query().Where(stage.SessionIDEQ(session.ID)).All(ctx)
		require.NoError(t, err)
		assert.Len(t, stages, 1)
		assert.Equal(t, "Initial Analysis", stages[0].StageName)
		assert.Equal(t, 0, stages[0].StageIndex)
		assert.Equal(t, 1, stages[0].ExpectedAgentCount)

		// Verify agent execution created with correct defaults
		executions, err := client.AgentExecution.Query().All(ctx)
		require.NoError(t, err)
		assert.Len(t, executions, 1)
		assert.Equal(t, stages[0].ID, executions[0].StageID)
		assert.Equal(t, 1, executions[0].AgentIndex)
		assert.Equal(t, string(config.LLMBackendLangChain), executions[0].LlmBackend)
		assert.Equal(t, req.AgentType, executions[0].AgentName)
	})

	t.Run("validates required fields", func(t *testing.T) {
		tests := []struct {
			name    string
			req     models.CreateSessionRequest
			wantErr string
		}{
			{
				name:    "missing session_id",
				req:     models.CreateSessionRequest{AlertData: "data", AgentType: "k8s", ChainID: "chain"},
				wantErr: "session_id",
			},
			{
				name:    "missing alert_data",
				req:     models.CreateSessionRequest{SessionID: "sid", AgentType: "k8s", ChainID: "chain"},
				wantErr: "alert_data",
			},
			{
				name:    "missing agent_type",
				req:     models.CreateSessionRequest{SessionID: "sid", AlertData: "data", ChainID: "chain"},
				wantErr: "agent_type",
			},
			{
				name:    "missing chain_id",
				req:     models.CreateSessionRequest{SessionID: "sid", AlertData: "data", AgentType: "k8s"},
				wantErr: "chain_id",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := service.CreateSession(ctx, tt.req)
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
			})
		}
	})

	t.Run("rejects duplicate session_id", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}

		_, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Try to create again with same ID
		_, err = service.CreateSession(ctx, req)
		require.Error(t, err)
		assert.Equal(t, ErrAlreadyExists, err)
	})

	t.Run("handles MCP selection", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
			MCPSelection: &models.MCPSelectionConfig{
				Servers: []models.MCPServerSelection{
					{Name: "kubernetes-server", Tools: []string{"kubectl-get"}},
				},
			},
		}

		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, session.McpSelection)
	})
}

func TestSessionService_GetSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("retrieves existing session", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		created, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		session, err := service.GetSession(ctx, created.ID, false)
		require.NoError(t, err)
		assert.Equal(t, created.ID, session.ID)
	})

	t.Run("loads edges when requested", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		created, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		session, err := service.GetSession(ctx, created.ID, true)
		require.NoError(t, err)
		assert.NotNil(t, session.Edges.Stages)
		assert.Len(t, session.Edges.Stages, 1)
		assert.Len(t, session.Edges.Stages[0].Edges.AgentExecutions, 1)
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		_, err := service.GetSession(ctx, "nonexistent", false)
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestSessionService_ListSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Create test sessions
	for i := 0; i < 5; i++ {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		_, err := service.CreateSession(ctx, req)
		require.NoError(t, err)
	}

	t.Run("lists all sessions", func(t *testing.T) {
		result, err := service.ListSessions(ctx, models.SessionFilters{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, result.TotalCount, 5)
		assert.Len(t, result.Sessions, result.TotalCount)
	})

	t.Run("applies pagination", func(t *testing.T) {
		result, err := service.ListSessions(ctx, models.SessionFilters{
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		assert.Len(t, result.Sessions, 2)
		assert.Equal(t, 2, result.Limit)
	})

	t.Run("filters by status", func(t *testing.T) {
		result, err := service.ListSessions(ctx, models.SessionFilters{
			Status: string(alertsession.StatusPending),
		})
		require.NoError(t, err)
		for _, session := range result.Sessions {
			assert.Equal(t, alertsession.StatusPending, session.Status)
		}
	})

	t.Run("excludes soft-deleted by default", func(t *testing.T) {
		// Create and soft-delete a session
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "to delete",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		created, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = client.AlertSession.UpdateOneID(created.ID).
			SetDeletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		// List should exclude it
		result, err := service.ListSessions(ctx, models.SessionFilters{})
		require.NoError(t, err)
		for _, session := range result.Sessions {
			assert.NotEqual(t, created.ID, session.ID)
		}

		// List with include_deleted should show it
		resultWithDeleted, err := service.ListSessions(ctx, models.SessionFilters{
			IncludeDeleted: true,
		})
		require.NoError(t, err)
		found := false
		for _, session := range resultWithDeleted.Sessions {
			if session.ID == created.ID {
				found = true
				break
			}
		}
		assert.True(t, found)
	})
}

func TestSessionService_UpdateSessionStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("updates status", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = service.UpdateSessionStatus(ctx, session.ID, alertsession.StatusInProgress)
		require.NoError(t, err)

		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusInProgress, updated.Status)
		assert.NotNil(t, updated.LastInteractionAt)
	})

	t.Run("sets completed_at for terminal states", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = service.UpdateSessionStatus(ctx, session.ID, alertsession.StatusCompleted)
		require.NoError(t, err)

		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCompleted, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		err := service.UpdateSessionStatus(ctx, "nonexistent", alertsession.StatusCompleted)
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestSessionService_FindOrphanedSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("finds orphaned sessions", func(t *testing.T) {
		// Create in-progress session with old interaction time
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Set to in-progress with old timestamp
		err = client.AlertSession.UpdateOneID(session.ID).
			SetStatus(alertsession.StatusInProgress).
			SetLastInteractionAt(time.Now().Add(-2 * time.Hour)).
			Exec(ctx)
		require.NoError(t, err)

		// Find orphaned (timeout 1 hour)
		orphaned, err := service.FindOrphanedSessions(ctx, 1*time.Hour)
		require.NoError(t, err)
		assert.Len(t, orphaned, 1)
		assert.Equal(t, session.ID, orphaned[0].ID)
	})

	t.Run("excludes recent sessions", func(t *testing.T) {
		// Create recent in-progress session
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = client.AlertSession.UpdateOneID(session.ID).
			SetStatus(alertsession.StatusInProgress).
			SetLastInteractionAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		// Should not find it
		orphaned, err := service.FindOrphanedSessions(ctx, 1*time.Hour)
		require.NoError(t, err)
		for _, s := range orphaned {
			assert.NotEqual(t, session.ID, s.ID)
		}
	})
}

func TestSessionService_CancelSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("cancels in-progress session", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Set to in_progress
		err = service.UpdateSessionStatus(ctx, session.ID, alertsession.StatusInProgress)
		require.NoError(t, err)

		err = service.CancelSession(ctx, session.ID)
		require.NoError(t, err)

		// Verify status is now cancelling
		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCancelling, updated.Status)
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		err := service.CancelSession(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("returns ErrNotCancellable for non-in-progress session", func(t *testing.T) {
		tests := []struct {
			name   string
			status alertsession.Status
		}{
			{name: "pending", status: alertsession.StatusPending},
			{name: "completed", status: alertsession.StatusCompleted},
			{name: "failed", status: alertsession.StatusFailed},
			{name: "cancelled", status: alertsession.StatusCancelled},
			{name: "timed_out", status: alertsession.StatusTimedOut},
			{name: "cancelling", status: alertsession.StatusCancelling},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				req := models.CreateSessionRequest{
					SessionID: uuid.New().String(),
					AlertData: "test alert",
					AgentType: "kubernetes",
					ChainID:   "k8s-analysis",
				}
				session, err := service.CreateSession(ctx, req)
				require.NoError(t, err)

				// For non-pending states, explicitly set the status
				if tt.status != alertsession.StatusPending {
					err = client.AlertSession.UpdateOneID(session.ID).
						SetStatus(tt.status).
						Exec(ctx)
					require.NoError(t, err)
				}

				err = service.CancelSession(ctx, session.ID)
				require.Error(t, err)
				assert.Equal(t, ErrNotCancellable, err)
			})
		}
	})
}

func TestSessionService_SoftDeleteOldSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("soft deletes old completed sessions", func(t *testing.T) {
		// Create old completed session
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Set completed 100 days ago
		err = client.AlertSession.UpdateOneID(session.ID).
			SetStatus(alertsession.StatusCompleted).
			SetCompletedAt(time.Now().Add(-100 * 24 * time.Hour)).
			Exec(ctx)
		require.NoError(t, err)

		// Soft delete old sessions (90 day retention)
		count, err := service.SoftDeleteOldSessions(ctx, 90)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1)

		// Verify soft deleted
		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.NotNil(t, updated.DeletedAt)
	})

	t.Run("preserves recent sessions", func(t *testing.T) {
		// Create recent completed session
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = client.AlertSession.UpdateOneID(session.ID).
			SetStatus(alertsession.StatusCompleted).
			SetCompletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		// Soft delete old sessions
		_, err = service.SoftDeleteOldSessions(ctx, 90)
		require.NoError(t, err)

		// Should not be deleted
		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Nil(t, updated.DeletedAt)
	})

	t.Run("soft deletes old pending sessions", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test-pending",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Backdate created_at to 100 days ago (session stays pending, never claimed)
		err = client.AlertSession.UpdateOneID(session.ID).
			SetCreatedAt(time.Now().Add(-100 * 24 * time.Hour)).
			Exec(ctx)
		require.NoError(t, err)

		count, err := service.SoftDeleteOldSessions(ctx, 90)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1)

		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.NotNil(t, updated.DeletedAt)
	})
}

func TestSessionService_RestoreSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("restores soft-deleted session", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Soft delete
		err = client.AlertSession.UpdateOneID(session.ID).
			SetDeletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		// Restore
		err = service.RestoreSession(ctx, session.ID)
		require.NoError(t, err)

		// Verify restored
		updated, err := service.GetSession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Nil(t, updated.DeletedAt)
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		err := service.RestoreSession(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestSessionService_SearchSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("searches alert_data", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "critical error in production cluster",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Search for "critical error" (plain text query)
		results, err := service.SearchSessions(ctx, "critical error", 10)
		require.NoError(t, err)

		found := false
		for _, s := range results {
			if s.ID == session.ID {
				found = true
				break
			}
		}
		assert.True(t, found)
	})

	t.Run("searches final_analysis", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		// Add final analysis
		err = client.AlertSession.UpdateOneID(session.ID).
			SetFinalAnalysis("memory leak detected in application").
			Exec(ctx)
		require.NoError(t, err)

		// Search (plain text query)
		results, err := service.SearchSessions(ctx, "memory leak", 10)
		require.NoError(t, err)

		found := false
		for _, s := range results {
			if s.ID == session.ID {
				found = true
				break
			}
		}
		assert.True(t, found)
	})
}

// ────────────────────────────────────────────────────────────
// Dashboard Service Methods
// ────────────────────────────────────────────────────────────

// seedDashboardSession creates a completed session with stages, LLM and MCP
// interactions for dashboard tests. Returns the session ID.
func seedDashboardSession(
	t *testing.T,
	client *ent.Client,
	alertData, alertType, chainID string,
	inputTokens, outputTokens, totalTokens int,
	mcpCount int,
) string {
	t.Helper()
	ctx := context.Background()
	sessionID := uuid.New().String()
	now := time.Now()
	started := now.Add(-5 * time.Second)
	completed := now

	// Create session.
	sess := client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData(alertData).
		SetAlertType(alertType).
		SetChainID(chainID).
		SetAgentType("kubernetes").
		SetStatus(alertsession.StatusCompleted).
		SetStartedAt(started).
		SetCompletedAt(completed).
		SetFinalAnalysis("Investigation result for " + alertData).
		SetExecutiveSummary("Summary for " + alertData).
		SetNillableAuthor(strPtr("test-author")).
		SaveX(ctx)

	// Create a completed stage.
	stg := client.Stage.Create().
		SetID(uuid.New().String()).
		SetSessionID(sess.ID).
		SetStageName("analysis").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		SetStartedAt(started).
		SetCompletedAt(completed).
		SaveX(ctx)

	// Create agent execution.
	exec := client.AgentExecution.Create().
		SetID(uuid.New().String()).
		SetSessionID(sess.ID).
		SetStageID(stg.ID).
		SetAgentName("TestAgent").
		SetAgentIndex(1).
		SetLlmBackend(string(config.LLMBackendLangChain)).
		SetStartedAt(started).
		SetStatus("completed").
		SaveX(ctx)

	// Create LLM interactions with token counts.
	client.LLMInteraction.Create().
		SetID(uuid.New().String()).
		SetSessionID(sess.ID).
		SetStageID(stg.ID).
		SetExecutionID(exec.ID).
		SetInteractionType(llminteraction.InteractionTypeIteration).
		SetModelName("test-model").
		SetLlmRequest(map[string]interface{}{}).
		SetLlmResponse(map[string]interface{}{}).
		SetInputTokens(inputTokens).
		SetOutputTokens(outputTokens).
		SetTotalTokens(totalTokens).
		SaveX(ctx)

	// Create MCP interactions.
	for i := 0; i < mcpCount; i++ {
		client.MCPInteraction.Create().
			SetID(uuid.New().String()).
			SetSessionID(sess.ID).
			SetStageID(stg.ID).
			SetExecutionID(exec.ID).
			SetInteractionType(mcpinteraction.InteractionTypeToolCall).
			SetServerName("test-server").
			SetToolName(fmt.Sprintf("tool_%d", i)).
			SaveX(ctx)
	}

	return sessionID
}

func strPtr(s string) *string { return &s }

func TestSessionService_GetSessionDetail(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("returns enriched detail for completed session", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"CPU alert data", "pod-crash", "k8s-analysis",
			100, 50, 150, 2)

		detail, err := service.GetSessionDetail(ctx, sessionID)
		require.NoError(t, err)

		// Core fields.
		assert.Equal(t, sessionID, detail.ID)
		assert.Equal(t, "CPU alert data", detail.AlertData)
		require.NotNil(t, detail.AlertType)
		assert.Equal(t, "pod-crash", *detail.AlertType)
		assert.Equal(t, "completed", detail.Status)
		assert.Equal(t, "k8s-analysis", detail.ChainID)
		require.NotNil(t, detail.Author)
		assert.Equal(t, "test-author", *detail.Author)

		// Analysis results.
		require.NotNil(t, detail.FinalAnalysis)
		assert.Contains(t, *detail.FinalAnalysis, "CPU alert data")
		require.NotNil(t, detail.ExecutiveSummary)
		assert.Contains(t, *detail.ExecutiveSummary, "CPU alert data")

		// Computed stats.
		assert.Equal(t, 1, detail.LLMInteractionCount)
		assert.Equal(t, 2, detail.MCPInteractionCount)
		assert.Equal(t, int64(100), detail.InputTokens)
		assert.Equal(t, int64(50), detail.OutputTokens)
		assert.Equal(t, int64(150), detail.TotalTokens)
		assert.Equal(t, 1, detail.TotalStages)
		assert.Equal(t, 1, detail.CompletedStages)
		assert.Equal(t, 0, detail.FailedStages)
		assert.Equal(t, false, detail.HasParallelStages)
		assert.Equal(t, true, detail.ChatEnabled, "chat should be enabled by default (no Chat config on chain)")
		assert.Nil(t, detail.ChatID, "no chat created yet")
		assert.Equal(t, 0, detail.ChatMessageCount)

		// Duration.
		require.NotNil(t, detail.DurationMs)
		assert.Greater(t, *detail.DurationMs, int64(0))

		// Stages.
		require.Len(t, detail.Stages, 1)
		assert.Equal(t, "analysis", detail.Stages[0].StageName)
		assert.Equal(t, 1, detail.Stages[0].StageIndex)
		assert.Equal(t, "completed", detail.Stages[0].Status)
		assert.Equal(t, 1, detail.Stages[0].ExpectedAgentCount)

		// Execution overviews on single-agent stage.
		require.Len(t, detail.Stages[0].Executions, 1)
		eo := detail.Stages[0].Executions[0]
		assert.Equal(t, "TestAgent", eo.AgentName)
		assert.Equal(t, 1, eo.AgentIndex)
		assert.Equal(t, "completed", eo.Status)
		assert.Equal(t, string(config.LLMBackendLangChain), eo.LLMBackend)
		assert.Equal(t, int64(100), eo.InputTokens)
		assert.Equal(t, int64(50), eo.OutputTokens)
		assert.Equal(t, int64(150), eo.TotalTokens)
	})

	t.Run("returns execution overviews with parallel agents and per-execution tokens", func(t *testing.T) {
		now := time.Now()
		started := now.Add(-10 * time.Second)
		completed := now
		sessionID := uuid.New().String()

		sess := client.AlertSession.Create().
			SetID(sessionID).
			SetAlertData("parallel alert").
			SetAlertType("test").
			SetChainID("parallel-chain").
			SetAgentType("kubernetes").
			SetStatus(alertsession.StatusCompleted).
			SetStartedAt(started).
			SetCompletedAt(completed).
			SaveX(ctx)

		stg := client.Stage.Create().
			SetID(uuid.New().String()).
			SetSessionID(sess.ID).
			SetStageName("Investigation").
			SetStageIndex(0).
			SetExpectedAgentCount(2).
			SetParallelType(stage.ParallelTypeMultiAgent).
			SetStatus(stage.StatusCompleted).
			SetStartedAt(started).
			SetCompletedAt(completed).
			SaveX(ctx)

		exec1 := client.AgentExecution.Create().
			SetID(uuid.New().String()).
			SetSessionID(sess.ID).
			SetStageID(stg.ID).
			SetAgentName("KubernetesAgent").
			SetAgentIndex(1).
			SetLlmBackend(string(config.LLMBackendNativeGemini)).
			SetLlmProvider("gemini-2.5-pro").
			SetStatus("completed").
			SetStartedAt(started).
			SetCompletedAt(completed).
			SaveX(ctx)

		exec2 := client.AgentExecution.Create().
			SetID(uuid.New().String()).
			SetSessionID(sess.ID).
			SetStageID(stg.ID).
			SetAgentName("ArgoCDAgent").
			SetAgentIndex(2).
			SetLlmBackend(string(config.LLMBackendLangChain)).
			SetStatus("completed").
			SetStartedAt(started).
			SetCompletedAt(completed).
			SaveX(ctx)

		// Two LLM interactions for exec1 (tokens should be summed).
		for _, tokens := range [][3]int{{200, 30, 230}, {100, 20, 120}} {
			client.LLMInteraction.Create().
				SetID(uuid.New().String()).
				SetSessionID(sess.ID).
				SetStageID(stg.ID).
				SetExecutionID(exec1.ID).
				SetInteractionType(llminteraction.InteractionTypeIteration).
				SetModelName("gemini-2.5-pro").
				SetLlmRequest(map[string]interface{}{}).
				SetLlmResponse(map[string]interface{}{}).
				SetInputTokens(tokens[0]).
				SetOutputTokens(tokens[1]).
				SetTotalTokens(tokens[2]).
				SaveX(ctx)
		}

		// One LLM interaction for exec2.
		client.LLMInteraction.Create().
			SetID(uuid.New().String()).
			SetSessionID(sess.ID).
			SetStageID(stg.ID).
			SetExecutionID(exec2.ID).
			SetInteractionType(llminteraction.InteractionTypeIteration).
			SetModelName("gemini-2.5-flash").
			SetLlmRequest(map[string]interface{}{}).
			SetLlmResponse(map[string]interface{}{}).
			SetInputTokens(50).
			SetOutputTokens(10).
			SetTotalTokens(60).
			SaveX(ctx)

		detail, err := service.GetSessionDetail(ctx, sessionID)
		require.NoError(t, err)

		// Stage should be parallel.
		require.Len(t, detail.Stages, 1)
		assert.True(t, detail.HasParallelStages)
		require.NotNil(t, detail.Stages[0].ParallelType)
		assert.Equal(t, "multi_agent", *detail.Stages[0].ParallelType)

		// Two execution overviews, ordered by agent_index.
		require.Len(t, detail.Stages[0].Executions, 2)

		eo1 := detail.Stages[0].Executions[0]
		assert.Equal(t, exec1.ID, eo1.ExecutionID)
		assert.Equal(t, "KubernetesAgent", eo1.AgentName)
		assert.Equal(t, 1, eo1.AgentIndex)
		assert.Equal(t, "completed", eo1.Status)
		assert.Equal(t, string(config.LLMBackendNativeGemini), eo1.LLMBackend)
		require.NotNil(t, eo1.LLMProvider)
		assert.Equal(t, "gemini-2.5-pro", *eo1.LLMProvider)
		// Tokens summed across two interactions: 200+100=300, 30+20=50, 230+120=350.
		assert.Equal(t, int64(300), eo1.InputTokens)
		assert.Equal(t, int64(50), eo1.OutputTokens)
		assert.Equal(t, int64(350), eo1.TotalTokens)

		eo2 := detail.Stages[0].Executions[1]
		assert.Equal(t, exec2.ID, eo2.ExecutionID)
		assert.Equal(t, "ArgoCDAgent", eo2.AgentName)
		assert.Equal(t, 2, eo2.AgentIndex)
		assert.Equal(t, string(config.LLMBackendLangChain), eo2.LLMBackend)
		assert.Nil(t, eo2.LLMProvider)
		assert.Equal(t, int64(50), eo2.InputTokens)
		assert.Equal(t, int64(10), eo2.OutputTokens)
		assert.Equal(t, int64(60), eo2.TotalTokens)
	})

	t.Run("chat_enabled false when chain explicitly disables chat", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"chat disabled test", "test-no-chat", "chat-disabled-chain",
			10, 5, 15, 0)

		detail, err := service.GetSessionDetail(ctx, sessionID)
		require.NoError(t, err)
		assert.Equal(t, false, detail.ChatEnabled, "chat should be disabled when chain sets Chat.Enabled=false")
	})

	t.Run("returns ErrNotFound for nonexistent session", func(t *testing.T) {
		_, err := service.GetSessionDetail(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("excludes soft-deleted session", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"soft-deleted detail", "pod-crash", "k8s-analysis",
			10, 5, 15, 0)

		err := client.AlertSession.UpdateOneID(sessionID).
			SetDeletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		_, err = service.GetSessionDetail(ctx, sessionID)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestSessionService_GetSessionSummary(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("returns correct token aggregation", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"summary test", "pod-crash", "k8s-analysis",
			200, 100, 300, 3)

		summary, err := service.GetSessionSummary(ctx, sessionID)
		require.NoError(t, err)

		assert.Equal(t, sessionID, summary.SessionID)
		assert.Equal(t, 4, summary.TotalInteractions) // 1 LLM + 3 MCP
		assert.Equal(t, 1, summary.LLMInteractions)
		assert.Equal(t, 3, summary.MCPInteractions)
		assert.Equal(t, int64(200), summary.InputTokens)
		assert.Equal(t, int64(100), summary.OutputTokens)
		assert.Equal(t, int64(300), summary.TotalTokens)
		require.NotNil(t, summary.TotalDurationMs)
		assert.Greater(t, *summary.TotalDurationMs, int64(0))

		assert.Equal(t, 1, summary.ChainStatistics.TotalStages)
		assert.Equal(t, 1, summary.ChainStatistics.CompletedStages)
		assert.Equal(t, 0, summary.ChainStatistics.FailedStages)
	})

	t.Run("returns ErrNotFound for nonexistent session", func(t *testing.T) {
		_, err := service.GetSessionSummary(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("excludes soft-deleted session", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"soft-deleted summary", "pod-crash", "k8s-analysis",
			10, 5, 15, 0)

		err := client.AlertSession.UpdateOneID(sessionID).
			SetDeletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		_, err = service.GetSessionSummary(ctx, sessionID)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestSessionService_GetActiveSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Create sessions in various states.
	mkSession := func(status alertsession.Status) string {
		id := uuid.New().String()
		builder := client.AlertSession.Create().
			SetID(id).
			SetAlertData("data").
			SetAlertType("test").
			SetChainID("k8s-analysis").
			SetAgentType("kubernetes").
			SetStatus(status)
		if status == alertsession.StatusInProgress {
			builder = builder.SetStartedAt(time.Now())
		}
		builder.SaveX(ctx)
		return id
	}

	pendingID := mkSession(alertsession.StatusPending)
	activeID := mkSession(alertsession.StatusInProgress)
	mkSession(alertsession.StatusCompleted) // should not appear

	result, err := service.GetActiveSessions(ctx)
	require.NoError(t, err)

	// Active list should contain the in_progress session.
	require.Len(t, result.Active, 1)
	assert.Equal(t, activeID, result.Active[0].ID)
	assert.Equal(t, "in_progress", result.Active[0].Status)

	// Queued list should contain the pending session.
	require.Len(t, result.Queued, 1)
	assert.Equal(t, pendingID, result.Queued[0].ID)
	assert.Equal(t, "pending", result.Queued[0].Status)
	assert.Equal(t, 1, result.Queued[0].QueuePosition)
}

func TestSessionService_ListSessionsForDashboard(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Seed 3 sessions with different alert types and token profiles.
	idA := seedDashboardSession(t, client.Client, "Alpha data", "pod-crash", "k8s-analysis", 100, 50, 150, 0)
	time.Sleep(10 * time.Millisecond) // ensure distinct created_at
	idB := seedDashboardSession(t, client.Client, "Beta data", "oom-kill", "k8s-analysis", 200, 100, 300, 1)
	time.Sleep(10 * time.Millisecond)
	idC := seedDashboardSession(t, client.Client, "Charlie data", "pod-crash", "test-chain", 150, 75, 225, 2)

	t.Run("returns all sessions with default params", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
		})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Pagination.TotalItems)
		assert.Equal(t, 1, result.Pagination.TotalPages)
		require.Len(t, result.Sessions, 3)

		// Default sort is created_at desc: C first, A last.
		assert.Equal(t, idC, result.Sessions[0].ID)
		assert.Equal(t, idA, result.Sessions[2].ID)
	})

	t.Run("pagination", func(t *testing.T) {
		p1, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 2, SortBy: "created_at", SortOrder: "desc",
		})
		require.NoError(t, err)
		assert.Len(t, p1.Sessions, 2)
		assert.Equal(t, 3, p1.Pagination.TotalItems)
		assert.Equal(t, 2, p1.Pagination.TotalPages)

		p2, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 2, PageSize: 2, SortBy: "created_at", SortOrder: "desc",
		})
		require.NoError(t, err)
		assert.Len(t, p2.Sessions, 1)

		// All 3 IDs across both pages.
		ids := map[string]bool{}
		for _, s := range p1.Sessions {
			ids[s.ID] = true
		}
		for _, s := range p2.Sessions {
			ids[s.ID] = true
		}
		assert.True(t, ids[idA] && ids[idB] && ids[idC])
	})

	t.Run("status filter", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
			Status: "completed",
		})
		require.NoError(t, err)
		assert.Equal(t, 3, result.Pagination.TotalItems)

		result, err = service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
			Status: "pending",
		})
		require.NoError(t, err)
		assert.Empty(t, result.Sessions)
	})

	t.Run("alert type filter", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
			AlertType: "pod-crash",
		})
		require.NoError(t, err)
		assert.Equal(t, 2, result.Pagination.TotalItems) // A and C
	})

	t.Run("chain ID filter", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
			ChainID: "test-chain",
		})
		require.NoError(t, err)
		require.Len(t, result.Sessions, 1)
		assert.Equal(t, idC, result.Sessions[0].ID)
	})

	t.Run("search filter", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
			Search: "Alpha",
		})
		require.NoError(t, err)
		require.Len(t, result.Sessions, 1)
		assert.Equal(t, idA, result.Sessions[0].ID)
	})

	t.Run("sorting asc", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "asc",
		})
		require.NoError(t, err)
		require.Len(t, result.Sessions, 3)
		assert.Equal(t, idA, result.Sessions[0].ID)
		assert.Equal(t, idC, result.Sessions[2].ID)
	})

	t.Run("duration sort", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "duration", SortOrder: "desc",
		})
		require.NoError(t, err)
		assert.Len(t, result.Sessions, 3)
	})

	t.Run("aggregate stats are correct", func(t *testing.T) {
		result, err := service.ListSessionsForDashboard(ctx, models.DashboardListParams{
			Page: 1, PageSize: 25, SortBy: "created_at", SortOrder: "desc",
		})
		require.NoError(t, err)

		// Find session B by ID and verify its stats.
		for _, s := range result.Sessions {
			if s.ID == idB {
				assert.Equal(t, 1, s.LLMInteractionCount)
				assert.Equal(t, 1, s.MCPInteractionCount)
				assert.Equal(t, int64(200), s.InputTokens)
				assert.Equal(t, int64(100), s.OutputTokens)
				assert.Equal(t, int64(300), s.TotalTokens)
				assert.Equal(t, 1, s.TotalStages)
				assert.Equal(t, 1, s.CompletedStages)
				assert.Equal(t, false, s.HasParallelStages)
				return
			}
		}
		t.Fatal("session B not found in list")
	})
}

func TestSessionService_GetDistinctAlertTypes(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Seed sessions with different alert types.
	seedDashboardSession(t, client.Client, "data1", "type-a", "k8s-analysis", 10, 5, 15, 0)
	seedDashboardSession(t, client.Client, "data2", "type-b", "k8s-analysis", 10, 5, 15, 0)
	seedDashboardSession(t, client.Client, "data3", "type-a", "k8s-analysis", 10, 5, 15, 0) // duplicate

	types, err := service.GetDistinctAlertTypes(ctx)
	require.NoError(t, err)
	assert.Len(t, types, 2)
	assert.Contains(t, types, "type-a")
	assert.Contains(t, types, "type-b")
}

func TestSessionService_GetSessionStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("returns status for in-progress session with nil optional fields", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "alert under investigation",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = service.UpdateSessionStatus(ctx, session.ID, alertsession.StatusInProgress)
		require.NoError(t, err)

		status, err := service.GetSessionStatus(ctx, session.ID)
		require.NoError(t, err)

		assert.Equal(t, session.ID, status.ID)
		assert.Equal(t, "in_progress", status.Status)
		assert.Nil(t, status.FinalAnalysis)
		assert.Nil(t, status.ExecutiveSummary)
		assert.Nil(t, status.ErrorMessage)
	})

	t.Run("returns status for completed session", func(t *testing.T) {
		sessionID := seedDashboardSession(t, client.Client,
			"status poll data", "pod-crash", "k8s-analysis",
			100, 50, 150, 1)

		status, err := service.GetSessionStatus(ctx, sessionID)
		require.NoError(t, err)

		assert.Equal(t, sessionID, status.ID)
		assert.Equal(t, "completed", status.Status)
		require.NotNil(t, status.FinalAnalysis)
		assert.Contains(t, *status.FinalAnalysis, "status poll data")
		require.NotNil(t, status.ExecutiveSummary)
		assert.Contains(t, *status.ExecutiveSummary, "status poll data")
		assert.Nil(t, status.ErrorMessage)
	})

	t.Run("returns ErrNotFound for nonexistent session", func(t *testing.T) {
		_, err := service.GetSessionStatus(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("excludes soft-deleted session", func(t *testing.T) {
		req := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "soft-deleted session",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session, err := service.CreateSession(ctx, req)
		require.NoError(t, err)

		err = client.AlertSession.UpdateOneID(session.ID).
			SetDeletedAt(time.Now()).
			Exec(ctx)
		require.NoError(t, err)

		_, err = service.GetSessionStatus(ctx, session.ID)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestSessionService_GetDistinctChainIDs(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	seedDashboardSession(t, client.Client, "data1", "test", "k8s-analysis", 10, 5, 15, 0)
	seedDashboardSession(t, client.Client, "data2", "test", "test-chain", 10, 5, 15, 0)

	chains, err := service.GetDistinctChainIDs(ctx)
	require.NoError(t, err)
	assert.Len(t, chains, 2)
	assert.Contains(t, chains, "k8s-analysis")
	assert.Contains(t, chains, "test-chain")
}
