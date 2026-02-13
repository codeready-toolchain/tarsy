package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// FailurePropagation test — Scenarios 3 (Fail Fast) + 6 (Policy All failure).
// Three-stage chain:
//   1. preparation  (Preparer, NativeThinking) — succeeds
//   2. parallel-check (CheckerA ∥ CheckerB, policy=all)
//      CheckerA succeeds, CheckerB LLM error → stage fails
//   3. final (Finalizer) — NEVER STARTS (fail-fast)
//
// Verifies: policy=all failure propagation, fail-fast (stage 3 absent),
// session/stage/execution error statuses, timeline event statuses via API.
// ────────────────────────────────────────────────────────────

func TestE2E_FailurePropagation(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Stage 1: preparation (Preparer, native-thinking) — succeeds ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the alert data."},
			&agent.TextChunk{Content: "Preparation complete: alert data reviewed and ready for parallel checks."},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
		},
	})

	// ── Stage 2: parallel-check (CheckerA succeeds, CheckerB errors) ──
	// Routed: CheckerA — succeeds with a final answer.
	llm.AddRouted("CheckerA", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "System status looks nominal."},
			&agent.TextChunk{Content: "CheckerA verification passed: all systems operational."},
			&agent.UsageChunk{InputTokens: 40, OutputTokens: 15, TotalTokens: 55},
		},
	})
	// Routed: CheckerB — LLM error.
	llm.AddRouted("CheckerB", LLMScriptEntry{
		Error: fmt.Errorf("LLM service unavailable"),
	})

	// No entries for stage 3 (Finalizer) — it should never start.

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "failure-propagation")),
		WithLLMClient(llm),
	)

	// Connect WS and subscribe to sessions channel.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-failure", "System check required")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Subscribe to session-specific channel.
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to reach terminal status (failed).
	app.WaitForSessionStatus(t, sessionID, "failed")

	// Allow trailing WS events to arrive.
	time.Sleep(200 * time.Millisecond)

	// ── Session assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "failed", session["status"])
	assert.NotEmpty(t, session["error_message"], "failed session should have an error message")

	// ── Stage assertions ──
	// Only 2 stages should exist — stage 3 ("final") never created due to fail-fast.
	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 2, "fail-fast: stage 3 should never be created")

	assert.Equal(t, "preparation", stages[0].StageName)
	assert.Equal(t, "completed", string(stages[0].Status))

	assert.Equal(t, "parallel-check", stages[1].StageName)
	assert.Equal(t, "failed", string(stages[1].Status))
	require.NotNil(t, stages[1].ErrorMessage, "failed stage should have error_message")
	assert.NotEmpty(t, *stages[1].ErrorMessage)

	// ── Execution assertions ──
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3, "Preparer + CheckerA + CheckerB")

	assert.Equal(t, "Preparer", execs[0].AgentName)
	assert.Equal(t, "completed", string(execs[0].Status))

	// Parallel agent order may vary — find by name.
	execByName := make(map[string]string)
	for _, e := range execs[1:] {
		execByName[e.AgentName] = string(e.Status)
	}
	assert.Equal(t, "completed", execByName["CheckerA"], "CheckerA should have completed")
	assert.Equal(t, "failed", execByName["CheckerB"], "CheckerB should have failed")

	// ── LLM call count ──
	// Preparer (1) + CheckerA (1) + CheckerB (1 — error counts as a call) = 3
	assert.Equal(t, 3, llm.CallCount())

	// ── Timeline API assertions ──
	// Verify timeline events through the API (not DB) — this is what the dashboard uses.
	apiTimeline := app.GetTimeline(t, sessionID)

	// Stage 1 (Preparer) should produce timeline events — all completed.
	// Stage 2 (CheckerA) should produce events — all completed.
	// Stage 2 (CheckerB) should produce NO timeline events (error before streaming).
	// No stage 3 events at all.
	require.NotEmpty(t, apiTimeline, "should have timeline events")

	// Verify all timeline events have terminal statuses (no stuck "streaming").
	for i, raw := range apiTimeline {
		event, ok := raw.(map[string]interface{})
		require.True(t, ok, "timeline event %d should be a JSON object", i)
		status, _ := event["status"].(string)
		assert.NotEqual(t, "streaming", status,
			"timeline event %d (%s) should not be stuck as streaming", i, event["event_type"])
	}

	// Verify no timeline events belong to a stage 3 execution.
	// Stage 3 was never created, so there should be no execution for "Finalizer".
	for i, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		eventType, _ := event["event_type"].(string)
		// All events should belong to either Preparer or CheckerA's executions.
		// There's no direct way to check "no Finalizer events" except by verifying
		// stage count (already done above — only 2 stages exist).
		assert.NotEmpty(t, eventType, "timeline event %d should have event_type", i)
	}

	// Count completed events per stage — Preparer events (stage 1) should all be completed.
	var completedCount, failedCount int
	for _, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		status, _ := event["status"].(string)
		switch status {
		case "completed":
			completedCount++
		case "failed":
			failedCount++
		}
	}
	assert.Greater(t, completedCount, 0, "should have completed timeline events")

	// ── WS event structural assertions ──
	AssertEventsInOrder(t, ws.Events(), testdata.FailurePropagationExpectedEvents)
}
