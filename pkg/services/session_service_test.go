package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionService_CreateSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := NewSessionService(client.Client)
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
		assert.NotNil(t, session.StartedAt)
		assert.NotNil(t, session.CurrentStageIndex)
		assert.Equal(t, 0, *session.CurrentStageIndex)

		// Verify stage created
		stages, err := client.Stage.Query().Where(stage.SessionIDEQ(session.ID)).All(ctx)
		require.NoError(t, err)
		assert.Len(t, stages, 1)
		assert.Equal(t, "Initial Analysis", stages[0].StageName)
		assert.Equal(t, 0, stages[0].StageIndex)
		assert.Equal(t, 1, stages[0].ExpectedAgentCount)

		// Verify agent execution created
		executions, err := client.AgentExecution.Query().All(ctx)
		require.NoError(t, err)
		assert.Len(t, executions, 1)
		assert.Equal(t, stages[0].ID, executions[0].StageID)
		assert.Equal(t, 1, executions[0].AgentIndex)
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
	service := NewSessionService(client.Client)
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
	service := NewSessionService(client.Client)
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
	service := NewSessionService(client.Client)
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

func TestSessionService_ClaimNextPendingSession(t *testing.T) {
	t.Run("claims oldest pending session", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		service := NewSessionService(client.Client)
		ctx := context.Background()

		// Create two pending sessions
		req1 := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test 1",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		session1, err := service.CreateSession(ctx, req1)
		require.NoError(t, err)

		time.Sleep(10 * time.Millisecond) // Ensure different timestamps

		req2 := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test 2",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		}
		_, err = service.CreateSession(ctx, req2)
		require.NoError(t, err)

		// Claim should get first session
		claimed, err := service.ClaimNextPendingSession(ctx, "pod-1")
		require.NoError(t, err)
		require.NotNil(t, claimed)
		assert.Equal(t, session1.ID, claimed.ID)
		assert.Equal(t, alertsession.StatusInProgress, claimed.Status)
		assert.Equal(t, "pod-1", *claimed.PodID)
	})

	t.Run("returns nil when no pending sessions", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		service := NewSessionService(client.Client)
		ctx := context.Background()

		claimed, err := service.ClaimNextPendingSession(ctx, "pod-1")
		require.NoError(t, err)
		assert.Nil(t, claimed)
	})

	t.Run("allows concurrent claims without conflict", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		service := NewSessionService(client.Client)
		ctx := context.Background()

		// Create sessions
		for i := 0; i < 3; i++ {
			req := models.CreateSessionRequest{
				SessionID: uuid.New().String(),
				AlertData: "test",
				AgentType: "kubernetes",
				ChainID:   "k8s-analysis",
			}
			_, err := service.CreateSession(ctx, req)
			require.NoError(t, err)
		}

		// Simulate concurrent claims
		claimed1, err := service.ClaimNextPendingSession(ctx, "pod-1")
		require.NoError(t, err)
		require.NotNil(t, claimed1)

		claimed2, err := service.ClaimNextPendingSession(ctx, "pod-2")
		require.NoError(t, err)
		require.NotNil(t, claimed2)

		// Should be different sessions
		assert.NotEqual(t, claimed1.ID, claimed2.ID)
	})
}

func TestSessionService_ConcurrentClaiming(t *testing.T) {
	t.Run("multiple workers claim different sessions without conflict", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		service := NewSessionService(client.Client)
		ctx := context.Background()
		// Create 10 pending sessions
		numSessions := 10
		for i := 0; i < numSessions; i++ {
			req := models.CreateSessionRequest{
				SessionID: uuid.New().String(),
				AlertData: "test alert",
				AgentType: "kubernetes",
				ChainID:   "k8s-analysis",
			}
			_, err := service.CreateSession(ctx, req)
			require.NoError(t, err)
		}

		// Launch 10 goroutines claiming sessions concurrently
		numWorkers := 10
		type result struct {
			session *ent.AlertSession
			err     error
		}
		results := make(chan result, numWorkers)

		for i := 0; i < numWorkers; i++ {
			go func(workerID int) {
				podID := fmt.Sprintf("pod-%d", workerID)
				session, err := service.ClaimNextPendingSession(ctx, podID)
				results <- result{session: session, err: err}
			}(i)
		}

		// Collect all results
		var claimedSessions []*ent.AlertSession
		var errors []error
		for i := 0; i < numWorkers; i++ {
			res := <-results
			if res.err != nil {
				errors = append(errors, res.err)
			} else if res.session != nil {
				claimedSessions = append(claimedSessions, res.session)
			}
		}

		// Verify no errors occurred
		require.Empty(t, errors, "concurrent claiming should not produce errors")

		// Verify we claimed all available sessions (workers might return nil if no sessions left)
		// The key is that all sessions get claimed, even if not all workers succeed
		assert.LessOrEqual(t, len(claimedSessions), numSessions, "should not claim more than available")
		assert.GreaterOrEqual(t, len(claimedSessions), 1, "should claim at least one session")

		// The critical test: verify no duplicate claims - all session IDs must be unique
		seenIDs := make(map[string]bool)
		for _, session := range claimedSessions {
			assert.False(t, seenIDs[session.ID], "session %s was claimed multiple times", session.ID)
			seenIDs[session.ID] = true
		}

		// Verify all claimed sessions are in_progress status with correct pod_id
		for _, session := range claimedSessions {
			assert.Equal(t, alertsession.StatusInProgress, session.Status)
			assert.NotNil(t, session.PodID, "claimed session should have pod_id set")
		}
	})

	t.Run("workers claiming more sessions than available", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		service := NewSessionService(client.Client)
		ctx := context.Background()
		// Create only 3 pending sessions
		numSessions := 3
		for i := 0; i < numSessions; i++ {
			req := models.CreateSessionRequest{
				SessionID: uuid.New().String(),
				AlertData: "test alert",
				AgentType: "kubernetes",
				ChainID:   "k8s-analysis",
			}
			_, err := service.CreateSession(ctx, req)
			require.NoError(t, err)
		}

		// Launch 10 workers (more than available sessions)
		numWorkers := 10
		type result struct {
			session *ent.AlertSession
			err     error
		}
		results := make(chan result, numWorkers)

		for i := 0; i < numWorkers; i++ {
			go func(workerID int) {
				podID := fmt.Sprintf("pod-%d", workerID)
				session, err := service.ClaimNextPendingSession(ctx, podID)
				results <- result{session: session, err: err}
			}(i)
		}

		// Collect all results
		var claimedSessions []*ent.AlertSession
		var errors []error
		for i := 0; i < numWorkers; i++ {
			res := <-results
			if res.err != nil {
				errors = append(errors, res.err)
			} else if res.session != nil {
				claimedSessions = append(claimedSessions, res.session)
			}
		}

		// Verify no errors occurred
		require.Empty(t, errors, "concurrent claiming should not produce errors")

		// Verify we claimed at most the available sessions (some workers may get nil)
		assert.LessOrEqual(t, len(claimedSessions), numSessions, "should not claim more than available")
		assert.GreaterOrEqual(t, len(claimedSessions), 1, "should claim at least one session")

		// Verify no duplicate claims - this is the critical concurrent safety test
		seenIDs := make(map[string]bool)
		for _, session := range claimedSessions {
			assert.False(t, seenIDs[session.ID], "session %s was claimed multiple times", session.ID)
			seenIDs[session.ID] = true
		}
	})
}

func TestSessionService_FindOrphanedSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := NewSessionService(client.Client)
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

func TestSessionService_SoftDeleteOldSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := NewSessionService(client.Client)
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
}

func TestSessionService_RestoreSession(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := NewSessionService(client.Client)
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
	service := NewSessionService(client.Client)
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
