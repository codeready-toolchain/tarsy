package services

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedActiveSession creates a session in the given non-terminal status with NULL
// review_status (simulates an actively investigating session for the triage view).
func seedActiveSession(t *testing.T, service *SessionService, status alertsession.Status) string {
	t.Helper()
	ctx := context.Background()

	req := models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test alert",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	}
	sess, err := service.CreateSession(ctx, req)
	require.NoError(t, err)

	if status != alertsession.StatusPending {
		require.NoError(t, service.UpdateSessionStatus(ctx, sess.ID, status))
	}
	return sess.ID
}

// seedReviewSession creates a completed session with the given review_status.
// If reviewStatus is empty, review_status stays NULL (simulates active session).
func seedReviewSession(t *testing.T, service *SessionService, reviewStatus string, assignee string) string {
	t.Helper()
	ctx := context.Background()

	req := models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test alert",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	}
	sess, err := service.CreateSession(ctx, req)
	require.NoError(t, err)

	// Move to completed terminal state.
	require.NoError(t, service.UpdateSessionStatus(ctx, sess.ID, alertsession.StatusCompleted))

	if reviewStatus == "" {
		return sess.ID
	}

	// Set review_status directly via the client.
	update := service.client.AlertSession.UpdateOneID(sess.ID).
		SetReviewStatus(alertsession.ReviewStatus(reviewStatus))
	if assignee != "" {
		update = update.SetAssignee(assignee).SetAssignedAt(time.Now())
	}
	if reviewStatus == "resolved" {
		update = update.SetResolvedAt(time.Now()).
			SetResolutionReason(alertsession.ResolutionReasonActioned)
	}
	require.NoError(t, update.Exec(ctx))
	return sess.ID
}

func TestSessionService_UpdateReviewStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("claim from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "alice@test.com",
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusInProgress, *sess.ReviewStatus)
		assert.NotNil(t, sess.Assignee)
		assert.Equal(t, "alice@test.com", *sess.Assignee)
		assert.NotNil(t, sess.AssignedAt)
	})

	t.Run("claim reassignment from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "alice@test.com")

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "bob@test.com",
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusInProgress, *sess.ReviewStatus)
		assert.Equal(t, "bob@test.com", *sess.Assignee)
	})

	t.Run("claim conflict from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "alice@test.com")

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "bob@test.com",
		})
		assert.ErrorIs(t, err, ErrConflict)
	})

	t.Run("unclaim from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "alice@test.com")

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "unclaim",
			Actor:  "alice@test.com",
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusNeedsReview, *sess.ReviewStatus)
		assert.Nil(t, sess.Assignee)
		assert.Nil(t, sess.AssignedAt)
	})

	t.Run("unclaim conflict from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "unclaim",
			Actor:  "alice@test.com",
		})
		assert.ErrorIs(t, err, ErrConflict)
	})

	t.Run("resolve from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "alice@test.com")
		reason := "actioned"
		note := "Applied fix from runbook"

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "alice@test.com",
			ResolutionReason: &reason,
			Note:             &note,
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusResolved, *sess.ReviewStatus)
		assert.NotNil(t, sess.ResolvedAt)
		assert.NotNil(t, sess.ResolutionReason)
		assert.Equal(t, alertsession.ResolutionReasonActioned, *sess.ResolutionReason)
		assert.NotNil(t, sess.ResolutionNote)
		assert.Equal(t, "Applied fix from runbook", *sess.ResolutionNote)
	})

	t.Run("direct resolve from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")
		reason := "dismissed"

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "alice@test.com",
			ResolutionReason: &reason,
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusResolved, *sess.ReviewStatus)
		assert.Equal(t, "alice@test.com", *sess.Assignee, "direct resolve should auto-assign")
		assert.Equal(t, alertsession.ResolutionReasonDismissed, *sess.ResolutionReason)

		// Direct resolve should create 2 activity rows (claim + resolve).
		activities, err := service.GetReviewActivity(ctx, id)
		require.NoError(t, err)
		require.Len(t, activities, 2)
		assert.Equal(t, "claim", string(activities[0].Action))
		assert.Equal(t, "resolve", string(activities[1].Action))
	})

	t.Run("resolve without resolution_reason returns validation error", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "alice@test.com")

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "resolve",
			Actor:  "alice@test.com",
		})
		assert.True(t, IsValidationError(err))
	})

	t.Run("resolve conflict from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "alice@test.com")
		reason := "actioned"

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "bob@test.com",
			ResolutionReason: &reason,
		})
		assert.ErrorIs(t, err, ErrConflict)
	})

	t.Run("reopen from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "alice@test.com")

		sess, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "reopen",
			Actor:  "bob@test.com",
		})
		require.NoError(t, err)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusNeedsReview, *sess.ReviewStatus)
		assert.Nil(t, sess.Assignee)
		assert.Nil(t, sess.AssignedAt)
		assert.Nil(t, sess.ResolvedAt)
		assert.Nil(t, sess.ResolutionReason)
		assert.Nil(t, sess.ResolutionNote)
	})

	t.Run("reopen conflict from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "reopen",
			Actor:  "alice@test.com",
		})
		assert.ErrorIs(t, err, ErrConflict)
	})

	t.Run("unknown action returns validation error", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "bogus",
			Actor:  "alice@test.com",
		})
		assert.True(t, IsValidationError(err))
	})
}

func TestSessionService_GetReviewActivity(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("returns activities in chronological order", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		// Perform claim then resolve — creates 2 activity rows.
		_, err := service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "claim", Actor: "alice@test.com",
		})
		require.NoError(t, err)

		reason := "actioned"
		_, err = service.UpdateReviewStatus(ctx, id, models.UpdateReviewRequest{
			Action: "resolve", Actor: "alice@test.com", ResolutionReason: &reason,
		})
		require.NoError(t, err)

		activities, err := service.GetReviewActivity(ctx, id)
		require.NoError(t, err)
		require.Len(t, activities, 2)
		assert.Equal(t, "claim", string(activities[0].Action))
		assert.Equal(t, "resolve", string(activities[1].Action))
		assert.True(t, !activities[0].CreatedAt.After(activities[1].CreatedAt))
	})

	t.Run("empty for session with no activity", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		activities, err := service.GetReviewActivity(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, activities)
	})

	t.Run("not found for missing session", func(t *testing.T) {
		_, err := service.GetReviewActivity(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestSessionService_GetTriageSessions(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Seed sessions across all 4 triage groups.
	investigatingID := seedActiveSession(t, service, alertsession.StatusInProgress)
	pendingID := seedActiveSession(t, service, alertsession.StatusPending)
	needsReviewID := seedReviewSession(t, service, "needs_review", "")
	inProgressID := seedReviewSession(t, service, "in_progress", "alice@test.com")
	resolvedID1 := seedReviewSession(t, service, "resolved", "alice@test.com")
	resolvedID2 := seedReviewSession(t, service, "resolved", "bob@test.com")
	resolvedID3 := seedReviewSession(t, service, "resolved", "alice@test.com")

	t.Run("groups sessions correctly", func(t *testing.T) {
		result, err := service.GetTriageSessions(ctx, models.TriageParams{ResolvedLimit: 20})
		require.NoError(t, err)

		assert.Equal(t, 2, result.Investigating.Count)
		investigatingIDs := collectIDs(result.Investigating.Sessions)
		assert.Contains(t, investigatingIDs, investigatingID)
		assert.Contains(t, investigatingIDs, pendingID)

		assert.Equal(t, 1, result.NeedsReview.Count)
		assert.Equal(t, needsReviewID, result.NeedsReview.Sessions[0].ID)

		assert.Equal(t, 1, result.InProgress.Count)
		assert.Equal(t, inProgressID, result.InProgress.Sessions[0].ID)
		assert.NotNil(t, result.InProgress.Sessions[0].Assignee)
		assert.Equal(t, "alice@test.com", *result.InProgress.Sessions[0].Assignee)

		assert.Equal(t, 3, result.Resolved.Count)
		assert.False(t, result.Resolved.HasMore)
	})

	t.Run("resolved_limit caps resolved group", func(t *testing.T) {
		result, err := service.GetTriageSessions(ctx, models.TriageParams{ResolvedLimit: 2})
		require.NoError(t, err)

		assert.Equal(t, 2, result.Resolved.Count)
		assert.True(t, result.Resolved.HasMore)
		assert.Len(t, result.Resolved.Sessions, 2)

		// Other groups unaffected.
		assert.Equal(t, 2, result.Investigating.Count)
		assert.Equal(t, 1, result.NeedsReview.Count)
	})

	t.Run("assignee filter", func(t *testing.T) {
		result, err := service.GetTriageSessions(ctx, models.TriageParams{
			ResolvedLimit: 20,
			Assignee:      "alice@test.com",
		})
		require.NoError(t, err)

		assert.Equal(t, 1, result.InProgress.Count)
		assert.Equal(t, inProgressID, result.InProgress.Sessions[0].ID)

		// Resolved sessions: 2 from alice, 1 from bob (filtered out).
		resolvedIDs := collectIDs(result.Resolved.Sessions)
		assert.Contains(t, resolvedIDs, resolvedID1)
		assert.Contains(t, resolvedIDs, resolvedID3)
		assert.NotContains(t, resolvedIDs, resolvedID2)
	})

	t.Run("defaults to limit 20 when zero", func(t *testing.T) {
		result, err := service.GetTriageSessions(ctx, models.TriageParams{ResolvedLimit: 0})
		require.NoError(t, err)

		assert.Equal(t, 3, result.Resolved.Count)
		assert.False(t, result.Resolved.HasMore)
	})

	t.Run("review fields populated in items", func(t *testing.T) {
		result, err := service.GetTriageSessions(ctx, models.TriageParams{ResolvedLimit: 20})
		require.NoError(t, err)

		// Investigating sessions have nil review_status.
		for _, s := range result.Investigating.Sessions {
			assert.Nil(t, s.ReviewStatus)
		}

		// In-progress session has review_status and assignee.
		require.Len(t, result.InProgress.Sessions, 1)
		require.NotNil(t, result.InProgress.Sessions[0].ReviewStatus)
		assert.Equal(t, "in_progress", *result.InProgress.Sessions[0].ReviewStatus)
		assert.NotNil(t, result.InProgress.Sessions[0].Assignee)

		// Resolved sessions have resolution_reason.
		for _, s := range result.Resolved.Sessions {
			require.NotNil(t, s.ReviewStatus)
			assert.Equal(t, "resolved", *s.ReviewStatus)
			assert.NotNil(t, s.ResolutionReason)
		}
	})
}

func collectIDs(items []models.DashboardSessionItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
