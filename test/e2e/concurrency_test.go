package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Concurrency test — Scenarios 12 (Concurrent Sessions) + 13 (Queue Capacity Limit).
//
// Single TestApp with WorkerCount=3, MaxConcurrentSessions=2.
// Submits 4 alerts simultaneously. All sessions block in Generate()
// via WaitCh so we get a stable snapshot: at least 2 sessions are
// in_progress (the capacity floor), and at least 1 remains pending
// (bounded by 3 workers for 4 sessions). The best-effort capacity
// check may allow up to 3 sessions to race in on the first poll
// cycle (documented behaviour). After releasing, all 4 complete and
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

	// releaseCh blocks sessions in Generate() so we can observe the
	// MaxConcurrentSessions limit before they complete.
	releaseCh := make(chan struct{})

	// blockedCh receives a signal each time a session enters the WaitCh
	// select in Generate(). The capacity check is best-effort (racy), so
	// up to WorkerCount (3) sessions may be claimed on the first poll
	// cycle. All entries block to keep sessions in a deterministic state.
	blockedCh := make(chan struct{}, 4)

	// All 4 entries block until releaseCh is closed. This ensures that
	// even if the best-effort capacity check allows a 3rd worker to race
	// past the limit, the session stays blocked and in_progress — giving
	// us a stable snapshot to assert against.
	for i := 0; i < 4; i++ {
		llm.AddRouted("SimpleAgent", LLMScriptEntry{
			Text:    "Analysis complete: system is healthy.",
			WaitCh:  releaseCh,
			OnBlock: blockedCh,
		})
	}

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

	// Wait until at least MaxConcurrentSessions (2) workers have entered
	// Generate() and are sitting in the WaitCh select. This confirms
	// sessions are genuinely held before we inspect DB state.
	<-blockedCh
	<-blockedCh

	// The capacity check is best-effort: multiple workers can race past
	// the limit on the first poll cycle (see pollAndProcess comment).
	// Allow the 3rd worker time to either claim+block or back off.
	drainDone := make(chan struct{})
	extraBlocked := 0
	go func() {
		defer close(drainDone)
		for {
			select {
			case <-blockedCh:
				extraBlocked++
			case <-time.After(200 * time.Millisecond):
				return
			}
		}
	}()
	<-drainDone
	totalBlocked := 2 + extraBlocked

	// Verify: totalBlocked sessions are in_progress (all held by WaitCh),
	// the rest are pending. The capacity check bounds this to at most
	// WorkerCount (3), so at least 1 session must remain pending.
	inProgressIDs := app.QuerySessionsByStatus(t, "in_progress")
	assert.Equal(t, totalBlocked, len(inProgressIDs),
		"in_progress count should match blocked workers")

	pendingIDs := app.QuerySessionsByStatus(t, "pending")
	assert.Equal(t, 4-totalBlocked, len(pendingIDs),
		"remaining sessions should be pending")

	assert.GreaterOrEqual(t, len(inProgressIDs), 2,
		"at least MaxConcurrentSessions (2) should be in_progress")
	assert.GreaterOrEqual(t, len(pendingIDs), 1,
		"at least 1 session should be pending (bounded by 3 workers, 4 sessions)")

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
