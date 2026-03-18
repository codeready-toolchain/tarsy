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
//   → PATCH claim → in_progress → PATCH resolve → resolved.
//   Verifies: DB state, API responses, WS events, review-activity, triage.
//
// Test 2 — Cancelled session auto-resolve:
//   Single-stage chain with BlockUntilCancelled agent → cancel → review_status
//   auto-inits to resolved/dismissed.
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

	// ── PATCH resolve ──
	resolveResp := app.PatchReview(t, sessionID, map[string]interface{}{
		"action":            "resolve",
		"resolution_reason": "actioned",
		"note":              "Verified and closed.",
	})
	resolveResults := resolveResp["results"].([]interface{})
	require.Len(t, resolveResults, 1)
	assert.Equal(t, true, resolveResults[0].(map[string]interface{})["success"])

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" && e.Parsed["review_status"] == "resolved"
	}, 5*time.Second, "expected review.status resolved WS event after resolve")

	// ── Review activity ──
	activityResp := app.GetReviewActivity(t, sessionID)
	activities, ok := activityResp["activities"].([]interface{})
	require.True(t, ok, "expected activities array")
	require.Len(t, activities, 2)

	act0 := activities[0].(map[string]interface{})
	assert.Equal(t, "claim", act0["action"])
	assert.Equal(t, "in_progress", act0["to_status"])

	act1 := activities[1].(map[string]interface{})
	assert.Equal(t, "resolve", act1["action"])
	assert.Equal(t, "resolved", act1["to_status"])
	assert.Equal(t, "actioned", act1["resolution_reason"])

	// ── Triage ──
	resolvedGroup := app.GetTriageGroup(t, "resolved", "")
	resolvedSessions, ok := resolvedGroup["sessions"].([]interface{})
	require.True(t, ok, "expected sessions array in resolved group")

	found := false
	for _, item := range resolvedSessions {
		m := item.(map[string]interface{})
		if m["id"] == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "session should appear in triage resolved group")
}

func TestE2E_ReviewWorkflow_CancelledAutoResolved(t *testing.T) {
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

	// Wait for review.status WS event (auto-resolved as dismissed).
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "review.status" &&
			e.Parsed["review_status"] == "resolved" &&
			e.Parsed["actor"] == "system"
	}, 5*time.Second, "expected review.status resolved WS event after cancellation")

	// DB assertion: review_status=resolved, resolution_reason=dismissed.
	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, session.ReviewStatus)
	assert.Equal(t, alertsession.ReviewStatusResolved, *session.ReviewStatus)
	require.NotNil(t, session.ResolutionReason)
	assert.Equal(t, alertsession.ResolutionReasonDismissed, *session.ResolutionReason)

	// ── Triage ──
	resolvedGroup := app.GetTriageGroup(t, "resolved", "")
	resolvedSessions, ok := resolvedGroup["sessions"].([]interface{})
	require.True(t, ok, "expected sessions array in resolved group")

	found := false
	for _, item := range resolvedSessions {
		m := item.(map[string]interface{})
		if m["id"] == sessionID {
			found = true
			break
		}
	}
	assert.True(t, found, "cancelled session should appear in triage resolved group")
}
