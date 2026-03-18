package services

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/sessionreviewactivity"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
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

// doReview is a test helper that calls UpdateReviewStatus for a single session
// and returns the updated ent session (or fails the test on error).
func doReview(t *testing.T, service *SessionService, id string, req models.UpdateReviewRequest) *ent.AlertSession {
	t.Helper()
	req.SessionIDs = []string{id}
	resp, updated := service.UpdateReviewStatus(context.Background(), req)
	require.Len(t, resp.Results, 1)
	require.True(t, resp.Results[0].Success, "expected success, got error: %s", resp.Results[0].Error)
	require.Len(t, updated, 1)
	return updated[0]
}

// doReviewExpectError is a test helper that calls UpdateReviewStatus for a single
// session and asserts that the per-session result reports a failure.
func doReviewExpectError(t *testing.T, service *SessionService, id string, req models.UpdateReviewRequest) string {
	t.Helper()
	req.SessionIDs = []string{id}
	resp, updated := service.UpdateReviewStatus(context.Background(), req)
	require.Len(t, resp.Results, 1)
	require.False(t, resp.Results[0].Success, "expected failure, but got success")
	assert.Empty(t, updated)
	return resp.Results[0].Error
}

func TestSessionService_UpdateReviewStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)

	t.Run("claim from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "john@test.com",
		})
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusInProgress, *sess.ReviewStatus)
		assert.NotNil(t, sess.Assignee)
		assert.Equal(t, "john@test.com", *sess.Assignee)
		assert.NotNil(t, sess.AssignedAt)
	})

	t.Run("claim reassignment from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "john@test.com")

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "bob@test.com",
		})
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusInProgress, *sess.ReviewStatus)
		assert.Equal(t, "bob@test.com", *sess.Assignee)
	})

	t.Run("claim conflict from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "john@test.com")

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "claim",
			Actor:  "bob@test.com",
		})
		assert.Contains(t, errMsg, "conflict")
	})

	t.Run("unclaim from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "john@test.com")

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "unclaim",
			Actor:  "john@test.com",
		})
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusNeedsReview, *sess.ReviewStatus)
		assert.Nil(t, sess.Assignee)
		assert.Nil(t, sess.AssignedAt)
	})

	t.Run("unclaim conflict from needs_review", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "unclaim",
			Actor:  "john@test.com",
		})
		assert.Contains(t, errMsg, "conflict")
	})

	t.Run("resolve from in_progress", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "john@test.com")
		reason := "actioned"
		note := "Applied fix from runbook"

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "john@test.com",
			ResolutionReason: &reason,
			Note:             &note,
		})
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

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "john@test.com",
			ResolutionReason: &reason,
		})
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusResolved, *sess.ReviewStatus)
		assert.Equal(t, "john@test.com", *sess.Assignee, "direct resolve should auto-assign")
		assert.Equal(t, alertsession.ResolutionReasonDismissed, *sess.ResolutionReason)

		// Direct resolve should create 2 activity rows (claim + resolve).
		ctx := context.Background()
		activities, err := service.GetReviewActivity(ctx, id)
		require.NoError(t, err)
		require.Len(t, activities, 2)
		assert.Equal(t, "claim", string(activities[0].Action))
		assert.Equal(t, "resolve", string(activities[1].Action))
	})

	t.Run("resolve without resolution_reason returns error", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "john@test.com")

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "resolve",
			Actor:  "john@test.com",
		})
		assert.Contains(t, errMsg, "resolution_reason")
	})

	t.Run("resolve conflict from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "john@test.com")
		reason := "actioned"

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action:           "resolve",
			Actor:            "bob@test.com",
			ResolutionReason: &reason,
		})
		assert.Contains(t, errMsg, "conflict")
	})

	t.Run("reopen from resolved", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "john@test.com")

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "reopen",
			Actor:  "bob@test.com",
		})
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

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "reopen",
			Actor:  "john@test.com",
		})
		assert.Contains(t, errMsg, "conflict")
	})

	t.Run("update_note on resolved session", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "john@test.com")
		note := "Updated fix details"

		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "update_note",
			Actor:  "john@test.com",
			Note:   &note,
		})
		require.NotNil(t, sess.ResolutionNote)
		assert.Equal(t, "Updated fix details", *sess.ResolutionNote)
		assert.Equal(t, alertsession.ReviewStatusResolved, *sess.ReviewStatus, "status should not change")

		ctx := context.Background()
		activities, err := service.GetReviewActivity(ctx, id)
		require.NoError(t, err)
		last := activities[len(activities)-1]
		assert.Equal(t, sessionreviewactivity.ActionUpdateNote, last.Action)
		assert.Equal(t, "john@test.com", last.Actor)
		require.NotNil(t, last.Note)
		assert.Equal(t, "Updated fix details", *last.Note)
	})

	t.Run("update_note clears note when nil", func(t *testing.T) {
		id := seedReviewSession(t, service, "resolved", "john@test.com")

		// Set a note first.
		note := "Some note"
		doReview(t, service, id, models.UpdateReviewRequest{
			Action: "update_note", Actor: "john@test.com", Note: &note,
		})

		// Clear it.
		sess := doReview(t, service, id, models.UpdateReviewRequest{
			Action: "update_note", Actor: "john@test.com",
		})
		assert.Nil(t, sess.ResolutionNote)
	})

	t.Run("update_note conflict on non-resolved session", func(t *testing.T) {
		id := seedReviewSession(t, service, "in_progress", "john@test.com")
		note := "Should fail"

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "update_note",
			Actor:  "john@test.com",
			Note:   &note,
		})
		assert.Contains(t, errMsg, "conflict")
	})

	t.Run("unknown action returns error", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		errMsg := doReviewExpectError(t, service, id, models.UpdateReviewRequest{
			Action: "bogus",
			Actor:  "john@test.com",
		})
		assert.Contains(t, errMsg, "unknown action")
	})

	t.Run("multiple sessions in one call", func(t *testing.T) {
		id1 := seedReviewSession(t, service, "needs_review", "")
		id2 := seedReviewSession(t, service, "needs_review", "")

		resp, updated := service.UpdateReviewStatus(context.Background(), models.UpdateReviewRequest{
			SessionIDs: []string{id1, id2},
			Action:     "claim",
			Actor:      "john@test.com",
		})
		require.Len(t, resp.Results, 2)
		assert.True(t, resp.Results[0].Success)
		assert.True(t, resp.Results[1].Success)
		assert.Len(t, updated, 2)
	})

	t.Run("partial failure when some sessions conflict", func(t *testing.T) {
		goodID := seedReviewSession(t, service, "needs_review", "")
		conflictID := seedReviewSession(t, service, "resolved", "john@test.com")

		resp, updated := service.UpdateReviewStatus(context.Background(), models.UpdateReviewRequest{
			SessionIDs: []string{goodID, conflictID},
			Action:     "claim",
			Actor:      "bob@test.com",
		})
		require.Len(t, resp.Results, 2)
		assert.True(t, resp.Results[0].Success)
		assert.False(t, resp.Results[1].Success)
		assert.NotEmpty(t, resp.Results[1].Error)
		assert.Len(t, updated, 1)
		assert.Equal(t, goodID, updated[0].ID)
	})

	t.Run("empty session IDs returns empty results", func(t *testing.T) {
		resp, updated := service.UpdateReviewStatus(context.Background(), models.UpdateReviewRequest{
			SessionIDs: []string{},
			Action:     "claim",
			Actor:      "john@test.com",
		})
		assert.Empty(t, resp.Results)
		assert.Empty(t, updated)
	})
}

func TestSessionService_GetReviewActivity(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	t.Run("returns activities in chronological order", func(t *testing.T) {
		id := seedReviewSession(t, service, "needs_review", "")

		// Perform claim then resolve — creates 2 activity rows.
		doReview(t, service, id, models.UpdateReviewRequest{
			Action: "claim", Actor: "john@test.com",
		})

		reason := "actioned"
		doReview(t, service, id, models.UpdateReviewRequest{
			Action: "resolve", Actor: "john@test.com", ResolutionReason: &reason,
		})

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

func TestSessionService_GetTriageGroup(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Seed sessions across all 4 triage groups.
	investigatingID := seedActiveSession(t, service, alertsession.StatusInProgress)
	pendingID := seedActiveSession(t, service, alertsession.StatusPending)
	needsReviewID := seedReviewSession(t, service, "needs_review", "")
	inProgressID := seedReviewSession(t, service, "in_progress", "john@test.com")
	resolvedID1 := seedReviewSession(t, service, "resolved", "john@test.com")
	resolvedID2 := seedReviewSession(t, service, "resolved", "bob@test.com")
	resolvedID3 := seedReviewSession(t, service, "resolved", "john@test.com")

	defaultParams := models.TriageGroupParams{Page: 1, PageSize: 20}

	t.Run("investigating group", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupInvestigating, defaultParams)
		require.NoError(t, err)

		assert.Equal(t, 2, result.Count)
		assert.Equal(t, 1, result.Page)
		assert.Equal(t, 1, result.TotalPages)
		ids := collectIDs(result.Sessions)
		assert.Contains(t, ids, investigatingID)
		assert.Contains(t, ids, pendingID)

		for _, s := range result.Sessions {
			assert.Nil(t, s.ReviewStatus)
		}
	})

	t.Run("needs_review group", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupNeedsReview, defaultParams)
		require.NoError(t, err)

		assert.Equal(t, 1, result.Count)
		assert.Equal(t, needsReviewID, result.Sessions[0].ID)
	})

	t.Run("in_progress group", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupInProgress, defaultParams)
		require.NoError(t, err)

		assert.Equal(t, 1, result.Count)
		assert.Equal(t, inProgressID, result.Sessions[0].ID)
		require.NotNil(t, result.Sessions[0].Assignee)
		assert.Equal(t, "john@test.com", *result.Sessions[0].Assignee)
		require.NotNil(t, result.Sessions[0].ReviewStatus)
		assert.Equal(t, "in_progress", *result.Sessions[0].ReviewStatus)
	})

	t.Run("resolved group", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, defaultParams)
		require.NoError(t, err)

		assert.Equal(t, 3, result.Count)
		assert.Len(t, result.Sessions, 3)
		for _, s := range result.Sessions {
			require.NotNil(t, s.ReviewStatus)
			assert.Equal(t, "resolved", *s.ReviewStatus)
			assert.NotNil(t, s.ResolutionReason)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 2,
		})
		require.NoError(t, err)

		assert.Equal(t, 3, result.Count)
		assert.Equal(t, 1, result.Page)
		assert.Equal(t, 2, result.PageSize)
		assert.Equal(t, 2, result.TotalPages)
		assert.Len(t, result.Sessions, 2)

		// Page 2 should return the remaining session.
		result2, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 2, PageSize: 2,
		})
		require.NoError(t, err)
		assert.Equal(t, 3, result2.Count)
		assert.Equal(t, 2, result2.Page)
		assert.Len(t, result2.Sessions, 1)
	})

	t.Run("assignee filter", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 20, Assignee: strPtr("john@test.com"),
		})
		require.NoError(t, err)

		assert.Equal(t, 2, result.Count)
		ids := collectIDs(result.Sessions)
		assert.Contains(t, ids, resolvedID1)
		assert.Contains(t, ids, resolvedID3)
		assert.NotContains(t, ids, resolvedID2)
	})

	t.Run("unassigned filter", func(t *testing.T) {
		unassignedID := seedReviewSession(t, service, "resolved", "")
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 20, Assignee: strPtr(""),
		})
		require.NoError(t, err)

		assert.Equal(t, 1, result.Count)
		ids := collectIDs(result.Sessions)
		assert.Contains(t, ids, unassignedID)
		assert.NotContains(t, ids, resolvedID1)
		assert.NotContains(t, ids, resolvedID2)
	})

	t.Run("empty group", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 20, Assignee: strPtr("nobody@test.com"),
		})
		require.NoError(t, err)

		assert.Equal(t, 0, result.Count)
		assert.Equal(t, 1, result.TotalPages)
		assert.Empty(t, result.Sessions)
	})

	t.Run("includes resolution_note in resolved group", func(t *testing.T) {
		// Set a resolution note on one resolved session.
		noteText := "Root cause was memory leak"
		service.client.AlertSession.UpdateOneID(resolvedID1).
			SetResolutionNote(noteText).
			ExecX(ctx)

		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 20, Assignee: strPtr("john@test.com"),
		})
		require.NoError(t, err)

		var found bool
		for _, s := range result.Sessions {
			if s.ID == resolvedID1 {
				found = true
				require.NotNil(t, s.ResolutionNote)
				assert.Equal(t, noteText, *s.ResolutionNote)
			}
		}
		assert.True(t, found, "resolvedID1 should be in results")
	})

	t.Run("page beyond total is clamped", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 999, PageSize: 20,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, result.Page, 1)
		expectedTotalPages := (result.Count + result.PageSize - 1) / result.PageSize
		if expectedTotalPages == 0 {
			expectedTotalPages = 1
		}
		assert.LessOrEqual(t, result.Page, expectedTotalPages)
		assert.LessOrEqual(t, len(result.Sessions), result.PageSize)
		assert.LessOrEqual(t, len(result.Sessions), result.Count)
	})

	t.Run("zero pageSize defaults to 20", func(t *testing.T) {
		result, err := service.GetTriageGroup(ctx, models.TriageGroupResolved, models.TriageGroupParams{
			Page: 1, PageSize: 0,
		})
		require.NoError(t, err)
		assert.Equal(t, 20, result.PageSize)
		expected := result.Count
		if expected > result.PageSize {
			expected = result.PageSize
		}
		assert.Equal(t, expected, len(result.Sessions))
	})

	t.Run("unknown group returns validation error", func(t *testing.T) {
		_, err := service.GetTriageGroup(ctx, "bogus", defaultParams)
		assert.True(t, IsValidationError(err))
	})

	t.Run("includes scoring fields", func(t *testing.T) {
		client.SessionScore.Create().
			SetID(uuid.New().String()).
			SetSessionID(needsReviewID).
			SetTotalScore(88).
			SetScoreTriggeredBy("auto").
			SetStatus(sessionscore.StatusCompleted).
			SaveX(ctx)

		result, err := service.GetTriageGroup(ctx, models.TriageGroupNeedsReview, defaultParams)
		require.NoError(t, err)
		require.Equal(t, 1, result.Count)

		s := result.Sessions[0]
		assert.Equal(t, needsReviewID, s.ID)
		require.NotNil(t, s.LatestScore)
		assert.Equal(t, 88, *s.LatestScore)
		require.NotNil(t, s.ScoringStatus)
		assert.Equal(t, "completed", *s.ScoringStatus)
	})
}

func collectIDs(items []models.DashboardSessionItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}
