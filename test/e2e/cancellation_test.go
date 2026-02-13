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
// Cancellation test — Scenarios 4 (Session cancellation) + 10 (Chat cancellation).
//
// Session 1 — Investigation cancellation:
//   cancel-investigation chain, single stage "investigation" with 2 parallel
//   agents (policy=any), both BlockUntilCancelled. Test cancels the session
//   while agents are blocked → agents, stage, and session all become cancelled.
//
// Session 2 — Chat cancellation:
//   cancel-chat chain, single stage "quick-check" with QuickInvestigator
//   (non-blocking), executive summary, chat enabled. Investigation completes
//   normally. First chat blocks on BlockUntilCancelled → cancelled. Follow-up
//   chat succeeds, verifying chat functionality is not broken after cancellation.
// ────────────────────────────────────────────────────────────

func TestE2E_Cancellation(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ═══════════════════════════════════════════════════════
	// Session 1 LLM entries (routed to parallel agents)
	// ═══════════════════════════════════════════════════════

	// Both agents block until context is cancelled.
	// investigatorsBlocked receives a signal when each agent enters Generate()'s
	// blocking path, replacing the previous time.Sleep heuristic.
	investigatorsBlocked := make(chan struct{}, 2)
	llm.AddRouted("InvestigatorA", LLMScriptEntry{BlockUntilCancelled: true, OnBlock: investigatorsBlocked})
	llm.AddRouted("InvestigatorB", LLMScriptEntry{BlockUntilCancelled: true, OnBlock: investigatorsBlocked})

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

	// Chat 1: BlockUntilCancelled (will be cancelled).
	// chatBlocked signals when the chat agent's Generate() enters its blocking path.
	chatBlocked := make(chan struct{}, 1)
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true, OnBlock: chatBlocked})

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
		WithConfig(configs.Load(t, "cancellation")),
		WithLLMClient(llm),
		// Long timeout so BlockUntilCancelled agents aren't killed by the session deadline.
		WithSessionTimeout(2*time.Minute),
		WithChatTimeout(2*time.Minute),
	)

	// ═══════════════════════════════════════════════════════
	// Session 1: Investigation cancellation
	// ═══════════════════════════════════════════════════════

	// Connect WS for Session 1.
	ctx := context.Background()
	ws1, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws1.Close()

	// Submit alert that routes to cancel-investigation chain.
	resp1 := app.SubmitAlert(t, "test-cancel", "Investigation cancellation test")
	session1ID := resp1["session_id"].(string)
	require.NotEmpty(t, session1ID)

	require.NoError(t, ws1.Subscribe("session:"+session1ID))

	// Wait until the session is in_progress and agents are executing.
	app.WaitForSessionStatus(t, session1ID, "in_progress")

	// Wait for both agents to enter Generate()'s blocking path.
	// OnBlock fires once each agent is blocking on ctx.Done(), so after
	// receiving both signals we know cancellation will be observed immediately.
	for i := 0; i < 2; i++ {
		select {
		case <-investigatorsBlocked:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for investigator agents to block in Generate()")
		}
	}

	// Cancel the session while both agents are blocked.
	app.CancelSession(t, session1ID)

	// Wait for session to reach terminal status.
	app.WaitForSessionStatus(t, session1ID, "cancelled")

	// Wait for the final WS event (session.status cancelled) instead of a fixed sleep.
	ws1.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "cancelled"
	}, 5*time.Second, "session 1: expected session.status cancelled WS event")

	// ── Session 1 assertions ──
	session1 := app.GetSession(t, session1ID)
	assert.Equal(t, "cancelled", session1["status"])

	// Stage assertions: single "investigation" stage, cancelled.
	stages1 := app.QueryStages(t, session1ID)
	require.Len(t, stages1, 1, "only the investigation stage should exist")
	assert.Equal(t, "investigation", stages1[0].StageName)
	assert.Equal(t, "cancelled", string(stages1[0].Status))

	// Execution assertions: both agents should be cancelled.
	execs1 := app.QueryExecutions(t, session1ID)
	require.Len(t, execs1, 2, "InvestigatorA + InvestigatorB")
	for _, e := range execs1 {
		assert.Equal(t, "cancelled", string(e.Status),
			"execution %s (%s) should be cancelled", e.ID, e.AgentName)
	}

	// LLM call count for Session 1: 2 (one per parallel agent).
	// (Verified at the end after Session 2 completes.)

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
	AssertEventsInOrder(t, ws1.Events(), testdata.CancellationInvestigationExpectedEvents)

	// ═══════════════════════════════════════════════════════
	// Session 2: Chat cancellation
	// ═══════════════════════════════════════════════════════

	// Connect WS for Session 2.
	ws2, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws2.Close()

	// Submit alert that routes to cancel-chat chain.
	resp2 := app.SubmitAlert(t, "test-chat-cancel", "Chat cancellation test")
	session2ID := resp2["session_id"].(string)
	require.NotEmpty(t, session2ID)

	require.NoError(t, ws2.Subscribe("session:"+session2ID))

	// Wait for investigation to complete normally.
	app.WaitForSessionStatus(t, session2ID, "completed")

	// ── Send Chat 1 (will be cancelled) ──
	chat1Resp := app.SendChatMessage(t, session2ID, "Ask a question")
	chat1StageID := chat1Resp["stage_id"].(string)
	require.NotEmpty(t, chat1StageID)

	// Wait for the chat agent to enter Generate()'s blocking path.
	// OnBlock fires once the agent is blocking on ctx.Done().
	select {
	case <-chatBlocked:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for chat agent to block in Generate()")
	}

	// Cancel the session — Bug 1 fix ensures chat cancellation works on completed sessions.
	app.CancelSession(t, session2ID)

	// Wait for the cancelled chat stage to reach terminal status.
	app.WaitForStageStatus(t, chat1StageID, "cancelled")

	// Session status should remain "completed" (chat cancellation doesn't change it).
	session2 := app.GetSession(t, session2ID)
	assert.Equal(t, "completed", session2["status"],
		"session 2 should remain completed after chat cancellation")

	// ── Send Chat 2 (follow-up — should succeed) ──
	chat2Resp := app.SendChatMessage(t, session2ID, "Follow-up question")
	chat2StageID := chat2Resp["stage_id"].(string)
	require.NotEmpty(t, chat2StageID)

	app.WaitForStageStatus(t, chat2StageID, "completed")

	// Wait for the final WS event (chat 2 stage completed) instead of a fixed sleep.
	ws2.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "stage.status" &&
			e.Parsed["stage_id"] == chat2StageID &&
			e.Parsed["status"] == "completed"
	}, 5*time.Second, "session 2: expected Chat Response stage.status completed WS event for chat 2")

	// ── Session 2 stage assertions ──
	stages2 := app.QueryStages(t, session2ID)
	// Expect: quick-check + Chat Response (cancelled) + Chat Response (completed) = 3 stages
	require.Len(t, stages2, 3, "quick-check + 2 chat stages")

	assert.Equal(t, "quick-check", stages2[0].StageName)
	assert.Equal(t, "completed", string(stages2[0].Status))

	assert.Equal(t, "Chat Response", stages2[1].StageName)
	assert.Equal(t, "cancelled", string(stages2[1].Status))

	assert.Equal(t, "Chat Response", stages2[2].StageName)
	assert.Equal(t, "completed", string(stages2[2].Status))

	// ── Session 2 execution assertions ──
	execs2 := app.QueryExecutions(t, session2ID)
	// QuickInvestigator + ChatAgent (cancelled) + ChatAgent (completed) = 3
	require.Len(t, execs2, 3, "QuickInvestigator + 2 chat executions")

	assert.Equal(t, "QuickInvestigator", execs2[0].AgentName)
	assert.Equal(t, "completed", string(execs2[0].Status))

	// Chat executions — both use the built-in ChatAgent.
	assert.Equal(t, "ChatAgent", execs2[1].AgentName)
	assert.Equal(t, "cancelled", string(execs2[1].Status))

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
	AssertEventsInOrder(t, ws2.Events(), testdata.CancellationChatExpectedEvents)

	// ── Total LLM call count ──
	// Session 1: InvestigatorA (1) + InvestigatorB (1) = 2
	// Session 2: QuickInvestigator (1) + exec summary (1) + chat1 BlockUntilCancelled (1) + chat2 (1) = 4
	// Total: 6
	assert.Equal(t, 6, llm.CallCount())
}
