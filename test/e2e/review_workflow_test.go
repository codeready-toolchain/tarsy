package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Review Workflow e2e tests
//
// Test 1 — Completed session lifecycle:
//   Single-stage chain completes → review_status auto-inits to needs_review
//   → PATCH claim → in_progress → PATCH complete → reviewed.
//   Verifies: DB state, API responses, WS events, review-activity, triage.
//
// Test 2 — Cancelled session auto-reviewed:
//   Single-stage chain with BlockUntilCancelled agent → cancel → review_status
//   auto-inits to reviewed (no quality_rating).
//   Verifies: DB state, WS event, triage.
// ────────────────────────────────────────────────────────────

func TestE2E_ReviewWorkflow_CompletedSession(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Single investigation agent: thinking + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the alert data."},
			&agent.TextChunk{Content: "Investigation complete: no issues found."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 15, TotalTokens: 45},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "review-workflow")),
		WithLLMClient(llm),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert and subscribe to session channel.
	resp := app.SubmitAlert(t, "test-review", "Review workflow test")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to complete.
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for review.status WS event from worker (auto-init).
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "needs_review"
	}, 5*time.Second, "expected review.status needs_review WS event")

	// DB assertion: review_status should be needs_review.
	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, session.ReviewStatus)
	assert.Equal(t, alertsession.ReviewStatusNeedsReview, *session.ReviewStatus)

	// ── PATCH claim ──
	claimResp := app.PatchReview(t, sessionID, map[string]interface{}{
		"action": "claim",
	})
	claimResults := claimResp["results"].([]interface{})
	require.Len(t, claimResults, 1)
	assert.Equal(t, true, claimResults[0].(map[string]interface{})["success"])

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "in_progress"
	}, 5*time.Second, "expected review.status in_progress WS event after claim")

	// ── PATCH complete ──
	completeResp := app.PatchReview(t, sessionID, map[string]interface{}{
		"action":         "complete",
		"quality_rating": "accurate",
		"action_taken":   "Verified and closed.",
	})
	completeResults := completeResp["results"].([]interface{})
	require.Len(t, completeResults, 1)
	assert.Equal(t, true, completeResults[0].(map[string]interface{})["success"])

	var completeEvent WSEvent
	ws.WaitForEvent(t, func(e WSEvent) bool {
		if e.Type == "review.status" && e.Parsed["review_status"] == "reviewed" {
			completeEvent = e
			return true
		}
		return false
	}, 5*time.Second, "expected review.status reviewed WS event after complete")

	assert.Equal(t, "accurate", completeEvent.Parsed["quality_rating"])
	assert.Equal(t, "Verified and closed.", completeEvent.Parsed["action_taken"])

	// ── DB assertion after complete ──
	session, err = app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.ReviewStatusReviewed, *session.ReviewStatus)
	require.NotNil(t, session.QualityRating)
	assert.Equal(t, alertsession.QualityRatingAccurate, *session.QualityRating)
	require.NotNil(t, session.ActionTaken)
	assert.Equal(t, "Verified and closed.", *session.ActionTaken)
	assert.NotNil(t, session.ReviewedAt)

	// ── Review activity ──
	activityResp := app.GetReviewActivity(t, sessionID)
	activities, ok := activityResp["activities"].([]interface{})
	require.True(t, ok, "expected activities array")
	require.Len(t, activities, 2)

	act0 := activities[0].(map[string]interface{})
	assert.Equal(t, "claim", act0["action"])
	assert.Equal(t, "in_progress", act0["to_status"])

	act1 := activities[1].(map[string]interface{})
	assert.Equal(t, "complete", act1["action"])
	assert.Equal(t, "reviewed", act1["to_status"])
	assert.Equal(t, "accurate", act1["quality_rating"])
	assert.Equal(t, "Verified and closed.", act1["note"])

	// ── Triage ──
	reviewedGroup := app.GetTriageGroup(t, "reviewed", "")
	reviewedSessions, ok := reviewedGroup["sessions"].([]interface{})
	require.True(t, ok, "expected sessions array in reviewed group")

	found := false
	for _, item := range reviewedSessions {
		m := item.(map[string]interface{})
		if m["id"] == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "session should appear in triage reviewed group")
}

func TestE2E_ReviewWorkflow_DirectComplete(t *testing.T) {
	llm := NewScriptedLLMClient()

	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the alert data."},
			&agent.TextChunk{Content: "Investigation complete: direct-complete test."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 15, TotalTokens: 45},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "review-workflow")),
		WithLLMClient(llm),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-review", "Direct complete test")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "needs_review"
	}, 5*time.Second, "expected review.status needs_review WS event")

	// ── PATCH complete directly (no prior claim) ──
	completeResp := app.PatchReview(t, sessionID, map[string]interface{}{
		"action":                  "complete",
		"quality_rating":          "partially_accurate",
		"action_taken":            "Acknowledged but needs follow-up.",
		"investigation_feedback":  "Good initial response, missed edge case.",
	})
	completeResults := completeResp["results"].([]interface{})
	require.Len(t, completeResults, 1)
	assert.Equal(t, true, completeResults[0].(map[string]interface{})["success"])

	var completeEvent WSEvent
	ws.WaitForEvent(t, func(e WSEvent) bool {
		if e.Type == "review.status" && e.Parsed["review_status"] == "reviewed" {
			completeEvent = e
			return true
		}
		return false
	}, 5*time.Second, "expected review.status reviewed WS event after direct complete")

	assert.Equal(t, "partially_accurate", completeEvent.Parsed["quality_rating"])
	assert.Equal(t, "Acknowledged but needs follow-up.", completeEvent.Parsed["action_taken"])

	// ── DB assertion ──
	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.ReviewStatusReviewed, *session.ReviewStatus)
	require.NotNil(t, session.QualityRating)
	assert.Equal(t, alertsession.QualityRatingPartiallyAccurate, *session.QualityRating)
	require.NotNil(t, session.ActionTaken)
	assert.Equal(t, "Acknowledged but needs follow-up.", *session.ActionTaken)
	require.NotNil(t, session.InvestigationFeedback)
	assert.Equal(t, "Good initial response, missed edge case.", *session.InvestigationFeedback)
	assert.NotNil(t, session.ReviewedAt)
	assert.NotNil(t, session.Assignee, "direct-complete should auto-assign")

	// ── Review activity: should have implicit claim + complete ──
	activityResp := app.GetReviewActivity(t, sessionID)
	activities, ok := activityResp["activities"].([]interface{})
	require.True(t, ok, "expected activities array")
	require.Len(t, activities, 2, "should have claim + complete activity rows")

	act0 := activities[0].(map[string]interface{})
	assert.Equal(t, "claim", act0["action"])
	assert.Equal(t, "in_progress", act0["to_status"])

	act1 := activities[1].(map[string]interface{})
	assert.Equal(t, "complete", act1["action"])
	assert.Equal(t, "reviewed", act1["to_status"])
	assert.Equal(t, "partially_accurate", act1["quality_rating"])
	assert.Equal(t, "Acknowledged but needs follow-up.", act1["note"])

	// Verify ordering: claim created_at < complete created_at.
	assert.True(t, act0["created_at"].(string) < act1["created_at"].(string),
		"claim timestamp should be strictly before complete timestamp")

	// ── Triage ──
	reviewedGroup := app.GetTriageGroup(t, "reviewed", "")
	reviewedSessions, ok := reviewedGroup["sessions"].([]interface{})
	require.True(t, ok, "expected sessions array in reviewed group")

	found := false
	for _, item := range reviewedSessions {
		m := item.(map[string]interface{})
		if m["id"] == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "session should appear in triage reviewed group")
}

func TestE2E_ReviewWorkflow_UpdateFeedback(t *testing.T) {
	llm := NewScriptedLLMClient()

	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the alert data."},
			&agent.TextChunk{Content: "Investigation complete: update-feedback test."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 15, TotalTokens: 45},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "review-workflow")),
		WithLLMClient(llm),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-review", "Update feedback test")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "needs_review"
	}, 5*time.Second, "expected review.status needs_review WS event")

	// ── Claim + Complete to reach reviewed state ──
	app.PatchReview(t, sessionID, map[string]interface{}{
		"action": "claim",
	})
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "in_progress"
	}, 5*time.Second, "expected in_progress WS event after claim")

	app.PatchReview(t, sessionID, map[string]interface{}{
		"action":         "complete",
		"quality_rating": "accurate",
		"action_taken":   "Initial review complete.",
	})
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "reviewed"
	}, 5*time.Second, "expected reviewed WS event after complete")

	// ── PATCH update_feedback: partial update changes only quality_rating ──
	updateResp := app.PatchReview(t, sessionID, map[string]interface{}{
		"action":                  "update_feedback",
		"quality_rating":          "partially_accurate",
		"investigation_feedback":  "Actually missed a key finding.",
	})
	updateResults := updateResp["results"].([]interface{})
	require.Len(t, updateResults, 1)
	assert.Equal(t, true, updateResults[0].(map[string]interface{})["success"])

	var updateEvent WSEvent
	ws.WaitForEvent(t, func(e WSEvent) bool {
		if e.Type == "review.status" && e.Parsed["quality_rating"] == "partially_accurate" {
			updateEvent = e
			return true
		}
		return false
	}, 5*time.Second, "expected review.status WS event after update_feedback")

	assert.Equal(t, "reviewed", updateEvent.Parsed["review_status"])
	assert.Equal(t, "partially_accurate", updateEvent.Parsed["quality_rating"])

	// ── DB assertion: session reflects updated values ──
	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.ReviewStatusReviewed, *session.ReviewStatus)
	require.NotNil(t, session.QualityRating)
	assert.Equal(t, alertsession.QualityRatingPartiallyAccurate, *session.QualityRating)
	require.NotNil(t, session.ActionTaken)
	assert.Equal(t, "Initial review complete.", *session.ActionTaken, "action_taken unchanged")
	require.NotNil(t, session.InvestigationFeedback)
	assert.Equal(t, "Actually missed a key finding.", *session.InvestigationFeedback)

	// ── Review activity: claim + complete + update_feedback = 3 rows ──
	activityResp := app.GetReviewActivity(t, sessionID)
	activities, ok := activityResp["activities"].([]interface{})
	require.True(t, ok, "expected activities array")
	require.Len(t, activities, 3)

	act2 := activities[2].(map[string]interface{})
	assert.Equal(t, "update_feedback", act2["action"])
	assert.Equal(t, "reviewed", act2["to_status"])
	assert.Equal(t, "partially_accurate", act2["quality_rating"])
	assert.Equal(t, "Initial review complete.", act2["note"], "activity should snapshot existing action_taken")
	assert.Equal(t, "Actually missed a key finding.", act2["investigation_feedback"])
}

func TestE2E_ReviewWorkflow_CancelledAutoReviewed(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Agent blocks until context is cancelled.
	agentBlocked := make(chan struct{}, 1)
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true, OnBlock: agentBlocked})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "review-workflow")),
		WithLLMClient(llm),
		WithSessionTimeout(2*time.Minute),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert and subscribe.
	resp := app.SubmitAlert(t, "test-review-cancel", "Review cancel test")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to become active and agent to block.
	app.WaitForSessionStatus(t, sessionID, "in_progress")
	select {
	case <-agentBlocked:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for agent to block in Generate()")
	}

	// Cancel the session.
	app.CancelSession(t, sessionID)
	app.WaitForSessionStatus(t, sessionID, "cancelled")

	// Wait for review.status WS event (auto-reviewed, no quality_rating).
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" &&
			e.Parsed["review_status"] == "reviewed" &&
			e.Parsed["actor"] == "system"
	}, 5*time.Second, "expected review.status reviewed WS event after cancellation")

	// DB assertion: review_status=reviewed, quality_rating=nil.
	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, session.ReviewStatus)
	assert.Equal(t, alertsession.ReviewStatusReviewed, *session.ReviewStatus)
	assert.Nil(t, session.QualityRating)

	// ── Triage ──
	reviewedGroup := app.GetTriageGroup(t, "reviewed", "")
	reviewedSessions, ok := reviewedGroup["sessions"].([]interface{})
	require.True(t, ok, "expected sessions array in reviewed group")

	found := false
	for _, item := range reviewedSessions {
		m := item.(map[string]interface{})
		if m["id"] == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "cancelled session should appear in triage reviewed group")
}
