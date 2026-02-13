package e2e

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Concurrency test — Scenarios 12 (Concurrent Sessions) + 13 (Queue Capacity Limit).
//
// Single TestApp with WorkerCount=3, MaxConcurrentSessions=2.
// Submits 4 alerts simultaneously. The first 2 sessions block in
// Generate() via WaitCh while the remaining 2 stay pending (capacity
// limit). After releasing the blocked sessions, all 4 complete and
// per-session assertions verify no cross-session data leakage.
//
// No WS event assertions — event ordering across 4 concurrent
// sessions is non-deterministic.
// ────────────────────────────────────────────────────────────

func TestE2E_Concurrency(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ═══════════════════════════════════════════════════════
	// LLM entries for 4 sessions (routed to SimpleAgent)
	// ═══════════════════════════════════════════════════════

	// releaseCh blocks the first 2 sessions in Generate() so we can
	// observe the MaxConcurrentSessions limit before they complete.
	releaseCh := make(chan struct{})

	// blockedCh receives a signal each time a session enters the WaitCh
	// select in Generate(). We wait for 2 signals to confirm both
	// sessions are genuinely blocked before checking capacity.
	blockedCh := make(chan struct{}, 2)

	// Entries 0-1: blocked until releaseCh is closed, then return response.
	// OnBlock notifies the test when Generate() starts blocking.
	llm.AddRouted("SimpleAgent", LLMScriptEntry{
		Text:    "Analysis complete: system is healthy.",
		WaitCh:  releaseCh,
		OnBlock: blockedCh,
	})
	llm.AddRouted("SimpleAgent", LLMScriptEntry{
		Text:    "Analysis complete: system is healthy.",
		WaitCh:  releaseCh,
		OnBlock: blockedCh,
	})

	// Entries 2-3: instant response (no blocking).
	llm.AddRouted("SimpleAgent", LLMScriptEntry{
		Text: "Analysis complete: system is healthy.",
	})
	llm.AddRouted("SimpleAgent", LLMScriptEntry{
		Text: "Analysis complete: system is healthy.",
	})

	// Executive summary entries (fail-open, but providing them avoids
	// warning logs). One per session, consumed sequentially.
	for i := 0; i < 4; i++ {
		llm.AddSequential(LLMScriptEntry{
			Text: "Executive summary: all clear.",
		})
	}

	// ═══════════════════════════════════════════════════════
	// Boot test app
	// ═══════════════════════════════════════════════════════

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "concurrency")),
		WithLLMClient(llm),
		WithWorkerCount(3),
		WithMaxConcurrentSessions(2),
	)

	// ═══════════════════════════════════════════════════════
	// Submit 4 alerts simultaneously
	// ═══════════════════════════════════════════════════════

	sessionIDs := make([]string, 4)
	for i := 0; i < 4; i++ {
		resp := app.SubmitAlert(t, "test-concurrency", fmt.Sprintf("Concurrency test session %d", i+1))
		sessionIDs[i] = resp["session_id"].(string)
		require.NotEmpty(t, sessionIDs[i])
	}

	// ═══════════════════════════════════════════════════════
	// Phase 1: Verify capacity limit (Scenario 13)
	// ═══════════════════════════════════════════════════════

	// Wait until both blocked sessions have entered Generate() and are
	// sitting in the WaitCh select. This is stronger than just checking
	// DB status — it confirms the sessions are genuinely held.
	<-blockedCh
	<-blockedCh

	// Verify: exactly 2 sessions in_progress, exactly 2 pending.
	inProgressIDs := app.QuerySessionsByStatus(t, "in_progress")
	assert.Len(t, inProgressIDs, 2,
		"exactly 2 sessions should be in_progress")

	pendingIDs := app.QuerySessionsByStatus(t, "pending")
	assert.Len(t, pendingIDs, 2,
		"exactly 2 sessions should be pending (capacity limit = 2)")

	// ═══════════════════════════════════════════════════════
	// Phase 2: Release and verify all complete (Scenario 12)
	// ═══════════════════════════════════════════════════════

	// Release the blocked sessions — they will complete, freeing capacity.
	close(releaseCh)

	// Wait for all 4 sessions to complete.
	app.WaitForNSessionsInStatus(t, 4, "completed")

	// ═══════════════════════════════════════════════════════
	// Per-session assertions
	// ═══════════════════════════════════════════════════════

	var timelineEventCounts []int

	for i, sessionID := range sessionIDs {
		label := fmt.Sprintf("session %d (%s)", i+1, sessionID[:8])

		// Session status.
		session := app.GetSession(t, sessionID)
		assert.Equal(t, "completed", session["status"],
			"%s: should be completed", label)

		// Stage assertions: exactly 1 stage (analysis), completed.
		stages := app.QueryStages(t, sessionID)
		require.Len(t, stages, 1, "%s: should have exactly 1 stage", label)
		assert.Equal(t, "analysis", stages[0].StageName,
			"%s: stage name", label)
		assert.Equal(t, "completed", string(stages[0].Status),
			"%s: stage status", label)

		// Execution assertions: exactly 1 execution (SimpleAgent), completed.
		execs := app.QueryExecutions(t, sessionID)
		require.Len(t, execs, 1, "%s: should have exactly 1 execution", label)
		assert.Equal(t, "SimpleAgent", execs[0].AgentName,
			"%s: agent name", label)
		assert.Equal(t, "completed", string(execs[0].Status),
			"%s: execution status", label)

		// Timeline API: no events stuck as "streaming".
		apiTimeline := app.GetTimeline(t, sessionID)
		for j, raw := range apiTimeline {
			event, ok := raw.(map[string]interface{})
			require.True(t, ok)
			status, _ := event["status"].(string)
			assert.NotEqual(t, "streaming", status,
				"%s: timeline event %d should not be stuck as streaming", label, j)
		}

		timelineEventCounts = append(timelineEventCounts, len(apiTimeline))
	}

	// All sessions should have the same timeline event count (no cross-session leakage).
	if len(timelineEventCounts) > 1 {
		for i := 1; i < len(timelineEventCounts); i++ {
			assert.Equal(t, timelineEventCounts[0], timelineEventCounts[i],
				"session %d and session 1 should have the same timeline event count (no cross-session leakage)",
				i+1)
		}
	}

	// ── Total LLM call count ──
	// 4 sessions × (1 SimpleAgent + 1 executive summary) = 8
	assert.Equal(t, 8, llm.CallCount())
}
