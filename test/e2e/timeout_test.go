package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Timeout test — Scenarios 15 (Session timeout) + 16 (Chat timeout).
//
// Session 1 — Investigation timeout:
//   timeout-investigation chain, single stage "investigation" with TimeoutAgent
//   using BlockUntilCancelled. Session timeout (2s) fires → DeadlineExceeded
//   propagates → agent, stage, and session all become timed_out.
//
// Session 2 — Chat timeout:
//   timeout-chat chain, single stage "quick-check" with QuickInvestigator
//   (non-blocking), executive summary, chat enabled. Investigation completes
//   normally. First chat blocks on BlockUntilCancelled → chat timeout (2s) fires
//   → timed_out. Follow-up chat succeeds, verifying chat still works after timeout.
// ────────────────────────────────────────────────────────────

func TestE2E_Timeout(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ═══════════════════════════════════════════════════════
	// Session 1 LLM entries (routed to TimeoutAgent)
	// ═══════════════════════════════════════════════════════

	// TimeoutAgent blocks until context deadline fires.
	llm.AddRouted("TimeoutAgent", LLMScriptEntry{BlockUntilCancelled: true})

	// ═══════════════════════════════════════════════════════
	// Session 2 LLM entries (sequential dispatch)
	// ═══════════════════════════════════════════════════════

	// QuickInvestigator — single iteration: thinking + final answer.
	llm.AddRouted("QuickInvestigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Quick check on the alert."},
			&agent.TextChunk{Content: "Alert verified: system is stable, no action needed."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 15, TotalTokens: 45},
		},
	})

	// Executive summary for Session 2.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Executive summary: quick check confirmed system stability."},
			&agent.UsageChunk{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		},
	})

	// Chat 1: BlockUntilCancelled (will time out).
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})

	// Chat 2 (follow-up): thinking + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Answering the follow-up."},
			&agent.TextChunk{Content: "Here is your follow-up answer: everything looks good."},
			&agent.UsageChunk{InputTokens: 25, OutputTokens: 12, TotalTokens: 37},
		},
	})

	// ═══════════════════════════════════════════════════════
	// Boot test app
	// ═══════════════════════════════════════════════════════

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "timeout")),
		WithLLMClient(llm),
		// Short timeouts so BlockUntilCancelled agents are killed by the deadline.
		WithSessionTimeout(2*time.Second),
		WithChatTimeout(2*time.Second),
	)

	// ═══════════════════════════════════════════════════════
	// Session 1: Investigation timeout
	// ═══════════════════════════════════════════════════════

	// Connect WS for Session 1.
	ctx := context.Background()
	ws1, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws1.Close()

	// Submit alert that routes to timeout-investigation chain.
	resp1 := app.SubmitAlert(t, "test-timeout", "Investigation timeout test")
	session1ID := resp1["session_id"].(string)
	require.NotEmpty(t, session1ID)

	require.NoError(t, ws1.Subscribe("session:"+session1ID))

	// Wait for session to reach timed_out — the 2s deadline fires automatically.
	app.WaitForSessionStatus(t, session1ID, "timed_out")

	// Allow trailing WS events to arrive.
	time.Sleep(200 * time.Millisecond)

	// ── Session 1 assertions ──
	session1 := app.GetSession(t, session1ID)
	assert.Equal(t, "timed_out", session1["status"])

	// Error message should mention the deadline.
	errorMsg, _ := session1["error_message"].(string)
	assert.Contains(t, errorMsg, "deadline exceeded",
		"session 1 error message should mention deadline exceeded")

	// Stage assertions: single "investigation" stage, timed_out.
	stages1 := app.QueryStages(t, session1ID)
	require.Len(t, stages1, 1, "only the investigation stage should exist")
	assert.Equal(t, "investigation", stages1[0].StageName)
	assert.Equal(t, "timed_out", string(stages1[0].Status))

	// Execution assertions: single agent should be timed_out.
	execs1 := app.QueryExecutions(t, session1ID)
	require.Len(t, execs1, 1, "TimeoutAgent only")
	assert.Equal(t, "TimeoutAgent", execs1[0].AgentName)
	assert.Equal(t, "timed_out", string(execs1[0].Status),
		"execution %s (%s) should be timed_out", execs1[0].ID, execs1[0].AgentName)

	// Timeline API: no events stuck as "streaming".
	apiTimeline1 := app.GetTimeline(t, session1ID)
	for i, raw := range apiTimeline1 {
		event, ok := raw.(map[string]interface{})
		require.True(t, ok)
		status, _ := event["status"].(string)
		assert.NotEqual(t, "streaming", status,
			"session 1: timeline event %d should not be stuck as streaming", i)
	}

	// WS event structural assertions for Session 1.
	AssertEventsInOrder(t, ws1.Events(), testdata.TimeoutInvestigationExpectedEvents)

	// ═══════════════════════════════════════════════════════
	// Session 2: Chat timeout
	// ═══════════════════════════════════════════════════════

	// Connect WS for Session 2.
	ws2, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws2.Close()

	// Submit alert that routes to timeout-chat chain.
	resp2 := app.SubmitAlert(t, "test-chat-timeout", "Chat timeout test")
	session2ID := resp2["session_id"].(string)
	require.NotEmpty(t, session2ID)

	require.NoError(t, ws2.Subscribe("session:"+session2ID))

	// Wait for investigation to complete normally.
	app.WaitForSessionStatus(t, session2ID, "completed")

	// ── Send Chat 1 (will time out) ──
	chat1Resp := app.SendChatMessage(t, session2ID, "Ask a question")
	chat1StageID := chat1Resp["stage_id"].(string)
	require.NotEmpty(t, chat1StageID)

	// Wait for the timed-out chat stage — the 2s chat deadline fires automatically.
	app.WaitForStageStatus(t, chat1StageID, "timed_out")

	// Session status should remain "completed" (chat timeout doesn't change it).
	session2 := app.GetSession(t, session2ID)
	assert.Equal(t, "completed", session2["status"],
		"session 2 should remain completed after chat timeout")

	// ── Send Chat 2 (follow-up — should succeed) ──
	chat2Resp := app.SendChatMessage(t, session2ID, "Follow-up question")
	chat2StageID := chat2Resp["stage_id"].(string)
	require.NotEmpty(t, chat2StageID)

	app.WaitForStageStatus(t, chat2StageID, "completed")

	// Allow trailing WS events to arrive.
	time.Sleep(200 * time.Millisecond)

	// ── Session 2 stage assertions ──
	stages2 := app.QueryStages(t, session2ID)
	// Expect: quick-check + Chat Response (timed_out) + Chat Response (completed) = 3 stages
	require.Len(t, stages2, 3, "quick-check + 2 chat stages")

	assert.Equal(t, "quick-check", stages2[0].StageName)
	assert.Equal(t, "completed", string(stages2[0].Status))

	assert.Equal(t, "Chat Response", stages2[1].StageName)
	assert.Equal(t, "timed_out", string(stages2[1].Status))

	assert.Equal(t, "Chat Response", stages2[2].StageName)
	assert.Equal(t, "completed", string(stages2[2].Status))

	// ── Session 2 execution assertions ──
	execs2 := app.QueryExecutions(t, session2ID)
	// QuickInvestigator + ChatAgent (timed_out) + ChatAgent (completed) = 3
	require.Len(t, execs2, 3, "QuickInvestigator + 2 chat executions")

	assert.Equal(t, "QuickInvestigator", execs2[0].AgentName)
	assert.Equal(t, "completed", string(execs2[0].Status))

	// Chat executions — both use the built-in ChatAgent.
	assert.Equal(t, "ChatAgent", execs2[1].AgentName)
	assert.Equal(t, "timed_out", string(execs2[1].Status))

	assert.Equal(t, "ChatAgent", execs2[2].AgentName)
	assert.Equal(t, "completed", string(execs2[2].Status))

	// ── Session 2 Timeline API assertions ──
	apiTimeline2 := app.GetTimeline(t, session2ID)
	require.NotEmpty(t, apiTimeline2, "should have timeline events for session 2")

	// No events stuck as "streaming".
	for i, raw := range apiTimeline2 {
		event, ok := raw.(map[string]interface{})
		require.True(t, ok)
		status, _ := event["status"].(string)
		assert.NotEqual(t, "streaming", status,
			"session 2: timeline event %d should not be stuck as streaming", i)
	}

	// Investigation events should be completed, follow-up chat events should be completed.
	var finalAnalysisCount int
	for _, raw := range apiTimeline2 {
		event, _ := raw.(map[string]interface{})
		if event["event_type"] == "final_analysis" {
			finalAnalysisCount++
		}
	}
	// QuickInvestigator final_analysis + follow-up chat final_analysis = 2
	assert.Equal(t, 2, finalAnalysisCount,
		"should have 2 final_analysis events (QuickInvestigator + follow-up chat)")

	// WS event structural assertions for Session 2.
	AssertEventsInOrder(t, ws2.Events(), testdata.TimeoutChatExpectedEvents)

	// ── Total LLM call count ──
	// Session 1: TimeoutAgent (1) = 1
	// Session 2: QuickInvestigator (1) + exec summary (1) + chat1 BlockUntilCancelled (1) + chat2 (1) = 4
	// Total: 5
	assert.Equal(t, 5, llm.CallCount())
}
