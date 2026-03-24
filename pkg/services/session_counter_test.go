package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionCounter_ReviewCountsByRating(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	counter := NewSessionCounter(client.Client)
	ctx := context.Background()

	t.Run("zero counts when no reviews exist", func(t *testing.T) {
		rc, err := counter.ReviewCountsByRating(ctx)
		require.NoError(t, err)
		assert.Equal(t, metrics.ReviewCounts{}, rc)
	})

	t.Run("counts by rating", func(t *testing.T) {
		// Seed sessions with different quality ratings via the review service.
		accurate1 := seedReviewSession(t, service, "needs_review", "")
		accurate2 := seedReviewSession(t, service, "needs_review", "")
		partial := seedReviewSession(t, service, "needs_review", "")
		inaccurate := seedReviewSession(t, service, "needs_review", "")

		rating := func(v string) *string { return &v }

		doReview(t, service, accurate1, models.UpdateReviewRequest{
			Action: "complete", Actor: "a@test.com", QualityRating: rating("accurate"),
		})
		doReview(t, service, accurate2, models.UpdateReviewRequest{
			Action: "complete", Actor: "a@test.com", QualityRating: rating("accurate"),
		})
		doReview(t, service, partial, models.UpdateReviewRequest{
			Action: "complete", Actor: "a@test.com", QualityRating: rating("partially_accurate"),
		})
		doReview(t, service, inaccurate, models.UpdateReviewRequest{
			Action: "complete", Actor: "a@test.com", QualityRating: rating("inaccurate"),
		})

		rc, err := counter.ReviewCountsByRating(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, rc.Accurate)
		assert.Equal(t, 1, rc.Partial)
		assert.Equal(t, 1, rc.Inaccurate)
	})

	t.Run("excludes soft-deleted sessions", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")
		rating := func(v string) *string { return &v }
		doReview(t, service, id, models.UpdateReviewRequest{
			Action: "complete", Actor: "a@test.com", QualityRating: rating("accurate"),
		})

		before, err := counter.ReviewCountsByRating(ctx)
		require.NoError(t, err)

		// Soft-delete the session.
		require.NoError(t, client.AlertSession.UpdateOneID(id).
			SetDeletedAt(client.AlertSession.Query().
				Where(alertsession.IDEQ(id)).
				FirstX(ctx).CreatedAt).
			Exec(ctx))

		after, err := counter.ReviewCountsByRating(ctx)
		require.NoError(t, err)
		assert.Equal(t, before.Accurate-1, after.Accurate)
	})
}

func TestSessionCounter_PendingAndActiveCounts(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	counter := NewSessionCounter(client.Client)
	ctx := context.Background()

	seedActiveSession(t, service, alertsession.StatusPending)
	seedActiveSession(t, service, alertsession.StatusPending)
	seedActiveSession(t, service, alertsession.StatusInProgress)
	seedActiveSession(t, service, alertsession.StatusCancelling)

	pending, err := counter.PendingCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, pending)

	active, err := counter.ActiveCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, active, "ActiveCount should include both in_progress and cancelling sessions")
}

func TestSessionCounter_ActionOutcomesByAgent(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	counter := NewSessionCounter(client.Client)
	ctx := context.Background()

	t.Run("empty when no action stages exist", func(t *testing.T) {
		rows, err := counter.ActionOutcomesByAgent(ctx)
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("counts by agent and outcome", func(t *testing.T) {
		createActionStage := func(agentName string, executed bool) {
			sess, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
				SessionID: uuid.New().String(),
				AlertData: "test",
				AgentType: "kubernetes",
				ChainID:   "k8s-analysis",
			})
			require.NoError(t, err)

			stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
				SessionID:          sess.ID,
				StageName:          "Remediation",
				StageIndex:         1,
				ExpectedAgentCount: 1,
				StageType:          string(stage.StageTypeAction),
			})
			require.NoError(t, err)

			require.NoError(t, stageService.SetActionsExecuted(ctx, stg.ID, executed))

			_, err = stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
				StageID:    stg.ID,
				SessionID:  sess.ID,
				AgentName:  agentName,
				AgentIndex: 1,
				LLMBackend: config.LLMBackendLangChain,
			})
			require.NoError(t, err)
		}

		createActionStage("RemediationAgent", true)
		createActionStage("RemediationAgent", true)
		createActionStage("RemediationAgent", false)
		createActionStage("SecurityAgent", true)

		rows, err := counter.ActionOutcomesByAgent(ctx)
		require.NoError(t, err)

		byKey := make(map[string]int)
		for _, row := range rows {
			executed := "no"
			if row.ActionsExecuted {
				executed = "yes"
			}
			byKey[row.AgentName+"/"+executed] = row.Count
		}

		assert.Equal(t, 2, byKey["RemediationAgent/yes"])
		assert.Equal(t, 1, byKey["RemediationAgent/no"])
		assert.Equal(t, 1, byKey["SecurityAgent/yes"])
	})
}
