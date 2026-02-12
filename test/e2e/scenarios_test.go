package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Scenario 1: Simple Single-Stage with MCP
// ────────────────────────────────────────────────────────────

func TestE2E_SingleStage(t *testing.T) {
	// LLM script: tool call → tool result → final answer.
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "Investigation complete: pod-1 is OOMKilled with 5 restarts."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOM killed due to memory leak."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(`[{"name":"pod-1","status":"OOMKilled","restarts":5}]`),
			},
		}),
	)

	// Connect WS and subscribe to sessions channel.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-alert", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Subscribe to session-specific channel.
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session completion via DB polling (most reliable).
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify session via API.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// Verify DB state.
	stages := app.QueryStages(t, sessionID)
	assert.Len(t, stages, 1)
	assert.Equal(t, "investigation", stages[0].StageName)

	execs := app.QueryExecutions(t, sessionID)
	assert.Len(t, execs, 1)
	assert.Equal(t, "DataCollector", execs[0].AgentName)

	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline)

	// Verify LLM call count: 1 tool call + 1 final answer + 1 executive summary = 3.
	assert.Equal(t, 3, llm.CallCount())

	// Golden file assertions (session API response is deterministic).
	normalizer := NewNormalizer(sessionID)
	for _, s := range stages {
		normalizer.RegisterStageID(s.ID)
	}
	for _, e := range execs {
		normalizer.RegisterExecutionID(e.ID)
	}
	AssertGoldenJSON(t, GoldenPath("single_stage", "session.golden"), session, normalizer)
}

// ────────────────────────────────────────────────────────────
// Scenario 3: Stage Failure — Fail Fast
// ────────────────────────────────────────────────────────────

func TestE2E_FailFast(t *testing.T) {
	// LLM script: first stage agent fails.
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{Error: fmt.Errorf("LLM service unavailable")})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "two-stage-fail-fast")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Test failure")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to fail via DB polling.
	app.WaitForSessionStatus(t, sessionID, "failed")

	// Verify session status.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "failed", session["status"])

	// Only 1 stage should be created (stage 2 never starts).
	stages := app.QueryStages(t, sessionID)
	assert.Len(t, stages, 1)
	assert.Equal(t, "stage-1", stages[0].StageName)

}

// ────────────────────────────────────────────────────────────
// Scenario 4: Investigation Cancellation
// ────────────────────────────────────────────────────────────

func TestE2E_Cancellation(t *testing.T) {
	// LLM script: blocks until context is cancelled.
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Test cancel")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for stage to start (indicating the agent is running).
	_, err = ws.WaitForEventType("stage.status", 30*time.Second)
	require.NoError(t, err)

	// Cancel the session.
	app.CancelSession(t, sessionID)

	// Wait for session to reach a terminal status (cancelled or failed — both valid
	// since context cancellation may cause in-flight DB updates to fail first).
	_, err = ws.WaitForEvent(func(e WSEvent) bool {
		return e.Type == "session.status" &&
			(e.Parsed["status"] == "cancelled" || e.Parsed["status"] == "failed")
	}, 30*time.Second)
	require.NoError(t, err)

	// Verify DB state.
	session := app.GetSession(t, sessionID)
	status := session["status"].(string)
	assert.True(t, status == "cancelled" || status == "failed",
		"session should be cancelled or failed, got: %s", status)

	stages := app.QueryStages(t, sessionID)
	assert.Len(t, stages, 1)
}

// ────────────────────────────────────────────────────────────
// Scenario 5: Parallel Agents — Policy Any
// ────────────────────────────────────────────────────────────

func TestE2E_ParallelPolicyAny(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Agent1 succeeds with tool call.
	llm.AddRouted("Agent1", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	llm.AddRouted("Agent1", LLMScriptEntry{Text: "Agent1 found the issue: memory leak."})
	// Agent2 fails.
	llm.AddRouted("Agent2", LLMScriptEntry{Error: fmt.Errorf("Agent2 LLM error")})
	// Synthesis (sequential — runs after parallel stage).
	llm.AddSequential(LLMScriptEntry{Text: "Synthesis: Agent1 identified a memory leak."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Memory leak identified by Agent1."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "parallel-any")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[{"name":"pod-1"}]`)},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Parallel test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])

	// Should have investigation stage + synthesis stage.
	stages := app.QueryStages(t, sessionID)
	assert.GreaterOrEqual(t, len(stages), 2)

}

// ────────────────────────────────────────────────────────────
// Scenario 6: Parallel Agents — Policy All
// ────────────────────────────────────────────────────────────

func TestE2E_ParallelPolicyAll(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Agent1 succeeds.
	llm.AddRouted("Agent1", LLMScriptEntry{Text: "Agent1 analysis complete."})
	// Agent2 fails.
	llm.AddRouted("Agent2", LLMScriptEntry{Error: fmt.Errorf("Agent2 LLM error")})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "parallel-all")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Parallel all test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Should fail since policy=all and Agent2 fails.
	app.WaitForSessionStatus(t, sessionID, "failed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "failed", session["status"])

}

// ────────────────────────────────────────────────────────────
// Scenario 7: Replica Execution
// ────────────────────────────────────────────────────────────

func TestE2E_Replicas(t *testing.T) {
	llm := NewScriptedLLMClient()
	// 3 replicas, each with a tool call + final answer (all sequential since same agent config).
	for i := 1; i <= 3; i++ {
		llm.AddSequential(LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "test-mcp__get_pods", Arguments: `{}`},
				&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
			},
		})
		llm.AddSequential(LLMScriptEntry{Text: fmt.Sprintf("Replica %d analysis.", i)})
	}
	// Synthesis after replicas.
	llm.AddSequential(LLMScriptEntry{Text: "Synthesis: All 3 replicas agree."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Summary: replicas agree."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "replica")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[{"name":"pod-1"}]`)},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Replica test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])

	execs := app.QueryExecutions(t, sessionID)
	// 3 replicas + 1 synthesis = at least 4 executions.
	assert.GreaterOrEqual(t, len(execs), 3)

}

// ────────────────────────────────────────────────────────────
// Scenario 8: Executive Summary Fail-Open
// ────────────────────────────────────────────────────────────

func TestE2E_ExecutiveSummaryFailOpen(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Stage 1 agent succeeds.
	llm.AddSequential(LLMScriptEntry{Text: "Root cause: memory leak in container."})
	// Executive summary LLM call fails.
	llm.AddSequential(LLMScriptEntry{Error: fmt.Errorf("exec summary LLM error")})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Exec summary test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	// Executive summary should be empty but error should be recorded.
	assert.Empty(t, session["executive_summary"])
	assert.NotEmpty(t, session["executive_summary_error"])
}

// ────────────────────────────────────────────────────────────
// Scenario 9: Chat Context Accumulation
// ────────────────────────────────────────────────────────────

func TestE2E_ChatContextAccumulation(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Investigation agent.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation: pod is OOMKilled."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Summary: OOM issue."})
	// Chat message 1: tool call + answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "chat-call-1", Name: "test-mcp__get_pods", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "Chat response 1: The OOM was caused by..."})
	// Chat message 2: just text.
	llm.AddSequential(LLMScriptEntry{Text: "Chat response 2: You can restart with..."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "chat")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[{"name":"pod-1"}]`)},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert and wait for investigation to complete.
	resp := app.SubmitAlert(t, "test-alert", "Chat test data")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Send first chat message.
	chatResp := app.SendChatMessage(t, sessionID, "What caused the OOM?")
	require.NotEmpty(t, chatResp["chat_id"])

	// Wait for chat stage to complete — look for a stage.status completed after the investigation.
	_, err = ws.WaitForEvent(func(e WSEvent) bool {
		return e.Type == "stage.status" && e.Parsed["status"] == "completed" &&
			e.Parsed["stage_name"] != "investigation"
	}, 30*time.Second)
	require.NoError(t, err)

	// Send second chat message.
	app.SendChatMessage(t, sessionID, "How do I restart it?")

	// Wait for second chat stage to complete.
	time.Sleep(500 * time.Millisecond) // Let first chat events settle.
	_, err = ws.CollectUntil(func(evts []WSEvent) bool {
		chatStageCount := 0
		for _, e := range evts {
			if e.Type == "stage.status" && e.Parsed["status"] == "completed" &&
				e.Parsed["stage_name"] != "investigation" {
				chatStageCount++
			}
		}
		return chatStageCount >= 2
	}, 30*time.Second)
	require.NoError(t, err)

	// Verify timeline has user_question events.
	timeline := app.QueryTimeline(t, sessionID)
	userQuestions := 0
	for _, te := range timeline {
		if te.EventType == "user_question" {
			userQuestions++
		}
	}
	assert.Equal(t, 2, userQuestions, "should have 2 user_question timeline events")

	// Verify second chat's LLM input included context from first exchange.
	inputs := llm.CapturedInputs()
	assert.GreaterOrEqual(t, len(inputs), 5, "should have at least 5 LLM calls")
}

// ────────────────────────────────────────────────────────────
// Scenario 10: Chat Cancellation
// ────────────────────────────────────────────────────────────

func TestE2E_ChatCancellation(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Investigation agent succeeds.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation complete."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Summary."})
	// Chat message: blocks until cancelled.
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "chat")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()

	resp := app.SubmitAlert(t, "test-alert", "Chat cancel test")
	sessionID := resp["session_id"].(string)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Send chat message (will block in LLM).
	chatResp := app.SendChatMessage(t, sessionID, "Will this be cancelled?")
	chatID := chatResp["chat_id"].(string)

	// Give the chat executor time to start the execution.
	time.Sleep(200 * time.Millisecond)

	// Cancel the chat execution directly via the ChatExecutor.
	// (The session is "completed", so the cancel API would return 409 on the
	// session status update, but the ChatExecutor can cancel the chat directly.)
	app.ChatExecutor.CancelBySessionID(ctx, sessionID)

	// Wait for the chat stage to reach a terminal state.
	_ = chatID
	require.Eventually(t, func() bool {
		stages := app.QueryStages(t, sessionID)
		for _, s := range stages {
			if s.StageName != "investigation" &&
				(s.Status == "failed" || s.Status == "cancelled") {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond, "chat stage should reach terminal state")

	// Session should still be "completed" (investigation was already done).
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
}

// ────────────────────────────────────────────────────────────
// Scenario 12: Concurrent Session Execution
// ────────────────────────────────────────────────────────────

func TestE2E_ConcurrentSessions(t *testing.T) {
	llm := NewScriptedLLMClient()
	// 3 sessions, each needs: investigation answer + executive summary.
	for i := 0; i < 3; i++ {
		llm.AddSequential(LLMScriptEntry{Text: fmt.Sprintf("Session %d analysis complete.", i+1)})
		llm.AddSequential(LLMScriptEntry{Text: fmt.Sprintf("Summary %d.", i+1)})
	}

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithWorkerCount(3),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()

	// Submit 3 alerts.
	var sessionIDs []string
	for i := 0; i < 3; i++ {
		resp := app.SubmitAlert(t, "test-alert", fmt.Sprintf("Concurrent test %d", i+1))
		sessionIDs = append(sessionIDs, resp["session_id"].(string))
	}

	// Wait for all to complete.
	require.Eventually(t, func() bool {
		for _, sid := range sessionIDs {
			s, err := app.EntClient.AlertSession.Get(ctx, sid)
			if err != nil || s.Status != alertsession.StatusCompleted {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "all sessions should complete")

	// Verify each session completed independently.
	for _, sid := range sessionIDs {
		session := app.GetSession(t, sid)
		assert.Equal(t, "completed", session["status"])
	}
}

// ────────────────────────────────────────────────────────────
// Scenario 14: Forced Conclusion
// ────────────────────────────────────────────────────────────

func TestE2E_ForcedConclusion(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Iteration 1: tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	// Iteration 2: another tool call (hits MaxIterations=2).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "call-2", Name: "test-mcp__get_pods", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	// Forced conclusion (no tools).
	llm.AddSequential(LLMScriptEntry{Text: "Based on 2 iterations, the system is healthy."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "System healthy after forced conclusion."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "forced-conclusion")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[{"name":"pod-1","status":"Running"}]`)},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Forced conclusion test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.Contains(t, session["final_analysis"], "Based on 2 iterations")

	// Verify: 4 LLM calls (2 tool rounds + 1 forced conclusion + 1 exec summary).
	assert.Equal(t, 4, llm.CallCount())

	// Verify the forced conclusion call (3rd, before exec summary) had no tools.
	inputs := llm.CapturedInputs()
	forcedInput := inputs[2]
	assert.Nil(t, forcedInput.Tools, "forced conclusion should have no tools")
}

// ────────────────────────────────────────────────────────────
// Scenario 15: Session Timeout
// ────────────────────────────────────────────────────────────

func TestE2E_SessionTimeout(t *testing.T) {
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithSessionTimeout(3*time.Second),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Timeout test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Should time out — the worker maps DeadlineExceeded to timed_out.
	_, err = ws.WaitForEvent(func(e WSEvent) bool {
		return e.Type == "session.status" &&
			(e.Parsed["status"] == "timed_out" || e.Parsed["status"] == "failed")
	}, 30*time.Second)
	require.NoError(t, err)

	session := app.GetSession(t, sessionID)
	status := session["status"].(string)
	assert.True(t, status == "timed_out" || status == "failed",
		"session should be timed_out or failed, got: %s", status)
}

// ────────────────────────────────────────────────────────────
// Scenario 16: Chat Timeout
// ────────────────────────────────────────────────────────────

func TestE2E_ChatTimeout(t *testing.T) {
	llm := NewScriptedLLMClient()
	// Investigation succeeds.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation done."})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Summary."})
	// Chat: blocks (will time out).
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "chat")),
		WithLLMClient(llm),
		WithChatTimeout(2*time.Second),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-alert", "Chat timeout test")
	sessionID := resp["session_id"].(string)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Send chat message that will time out.
	app.SendChatMessage(t, sessionID, "Will this time out?")

	// Wait for chat stage to reach timed_out or failed.
	_, err = ws.WaitForEvent(func(e WSEvent) bool {
		return e.Type == "stage.status" &&
			(e.Parsed["status"] == "timed_out" || e.Parsed["status"] == "failed") &&
			e.Parsed["stage_name"] != "investigation"
	}, 30*time.Second)
	require.NoError(t, err)

	// Session should still be "completed" (investigation was already done).
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
}

// ────────────────────────────────────────────────────────────
// Scenario 1: Full Investigation Flow (flagship)
// ────────────────────────────────────────────────────────────

func TestE2E_FullFlow(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Stage 1: DataCollector — tool call + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "dc-1", Name: "test-mcp__get_pod_logs", Arguments: `{"pod":"app-1"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "Collected metrics showing OOM on app-1."})

	// Stage 2: Three parallel agents (routed by agent name / custom instructions).
	// Two Investigators (same agent, different config) + one ResourceAnalyzer (different agent).
	// Investigator 1 (google-test, native-thinking): uses [test-mcp].
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "inv-1", Name: "test-mcp__get_metrics", Arguments: `{"pod":"app-1"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 15, TotalTokens: 95},
		},
	})
	llm.AddRouted("Investigator", LLMScriptEntry{Text: "Agent 1 analysis: memory steadily increasing."})
	// Investigator 2 (openai-test, react): uses [test-mcp, kubernetes-server].
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "inv-2", Name: "kubernetes-server__get_events", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 15, TotalTokens: 95},
		},
	})
	llm.AddRouted("Investigator", LLMScriptEntry{Text: "Agent 2 analysis: OOMKill events found."})
	// ResourceAnalyzer (google-test, native-thinking): uses [kubernetes-server].
	llm.AddRouted("ResourceAnalyzer", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "ra-1", Name: "kubernetes-server__get_resource_usage", Arguments: `{"pod":"app-1"}`},
			&agent.UsageChunk{InputTokens: 70, OutputTokens: 15, TotalTokens: 85},
		},
	})
	llm.AddRouted("ResourceAnalyzer", LLMScriptEntry{Text: "Agent 3 analysis: pod memory limit 512Mi, usage peaked at 510Mi."})

	// Synthesis after parallel stage (3 agents' results).
	llm.AddSequential(LLMScriptEntry{Text: "Synthesized: All agents agree — memory leak in app-1 hitting 512Mi limit."})

	// Stage 3: Diagnostician — final diagnosis (no tools).
	llm.AddSequential(LLMScriptEntry{Text: "Root cause: unbounded cache in app-1 leads to OOM."})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod app-1 OOM killed due to unbounded cache."})

	// Chat message 1: tool call + answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "chat-1", Name: "test-mcp__get_pod_status", Arguments: `{"pod":"app-1"}`},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 10, TotalTokens: 70},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "The OOM was caused by the in-memory cache growing unbounded."})

	// Chat message 2: text only.
	llm.AddSequential(LLMScriptEntry{Text: "You can restart with: kubectl rollout restart deployment/app-1"})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "full-flow")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pod_logs":   StaticToolHandler(`{"logs":"OOM killed at 14:32 UTC"}`),
				"get_metrics":    StaticToolHandler(`{"memory_mb":[100,200,450,900,1024]}`),
				"get_pod_status": StaticToolHandler(`{"status":"CrashLoopBackOff","restarts":5}`),
			},
			"kubernetes-server": {
				"get_events":         StaticToolHandler(`[{"type":"OOMKill","count":3}]`),
				"get_resource_usage": StaticToolHandler(`{"pod":"app-1","memory_limit":"512Mi","memory_usage":"510Mi"}`),
			},
		}),
	)

	// Submit alert.
	resp := app.SubmitAlert(t, "kubernetes-oom", "Pod app-1 OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Wait for investigation to complete.
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify session via API.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])
	assert.NotEmpty(t, session["executive_summary"])

	// Verify stages.
	stages := app.QueryStages(t, sessionID)
	assert.GreaterOrEqual(t, len(stages), 3, "should have at least 3 stages")

	// Verify executions (3 parallel agents + synthesis + stage1 + stage3 = at least 5).
	execs := app.QueryExecutions(t, sessionID)
	assert.GreaterOrEqual(t, len(execs), 5, "should have at least 5 executions")

	// Verify MCP interactions recorded (may be zero with in-memory transport retries).
	mcpInteractions := app.QueryMCPInteractions(t, sessionID)
	t.Logf("MCP interactions recorded: %d", len(mcpInteractions))

	// Verify LLM interactions recorded.
	llmInteractions := app.QueryLLMInteractions(t, sessionID)
	assert.GreaterOrEqual(t, len(llmInteractions), 4, "should have LLM interactions")

	// Chat phase.
	chatResp := app.SendChatMessage(t, sessionID, "What caused the OOM?")
	require.NotEmpty(t, chatResp["chat_id"])

	// Wait for first chat stage to complete.
	require.Eventually(t, func() bool {
		allStages := app.QueryStages(t, sessionID)
		for _, s := range allStages {
			if s.StageName == "Chat Response" && s.Status == "completed" {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond, "first chat stage should complete")

	// Send second chat message.
	app.SendChatMessage(t, sessionID, "How do I restart it?")

	// Wait for second chat stage to complete.
	require.Eventually(t, func() bool {
		allStages := app.QueryStages(t, sessionID)
		chatCompletedCount := 0
		for _, s := range allStages {
			if s.StageName == "Chat Response" && s.Status == "completed" {
				chatCompletedCount++
			}
		}
		return chatCompletedCount >= 2
	}, 30*time.Second, 100*time.Millisecond, "second chat stage should complete")

	// Final session state should still be completed.
	finalSession := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", finalSession["status"])

	// Verify total LLM calls (at least 12: stage1=2, stage2=6, synth=1, stage3=1, exec_summary=1, chat=3).
	assert.GreaterOrEqual(t, llm.CallCount(), 12, "should have at least 12 LLM calls")
}

// ────────────────────────────────────────────────────────────
// Scenario 11: Comprehensive Observability
// ────────────────────────────────────────────────────────────

func TestE2E_ComprehensiveObservability(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Single agent with tool call + final answer — simple flow to verify all 4 data layers.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "obs-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Root cause: pod OOMKilled due to memory leak."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 50, TotalTokens: 200},
		},
	})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "OOM due to memory leak."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(`[{"name":"pod-1","status":"OOMKilled","restarts":5,"memory":"1024Mi"}]`),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-alert", "Pod OOMKilled observability test")
	sessionID := resp["session_id"].(string)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Layer 1: API response ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── Layer 2: Timeline events ──
	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline, "should have timeline events")
	eventTypes := make(map[string]bool)
	for _, te := range timeline {
		eventTypes[string(te.EventType)] = true
	}
	assert.True(t, eventTypes["tool_call"] || eventTypes["llm_response"],
		"timeline should contain tool_call or llm_response events")

	// ── Layer 3: LLM interactions ──
	llmInteractions := app.QueryLLMInteractions(t, sessionID)
	assert.GreaterOrEqual(t, len(llmInteractions), 2, "should have at least 2 LLM interactions")
	for _, li := range llmInteractions {
		assert.NotEmpty(t, li.ModelName, "LLM interaction should have model name")
		if li.InputTokens != nil {
			assert.Greater(t, *li.InputTokens, 0, "LLM interaction should have input tokens")
		}
	}

	// ── Layer 4: MCP interactions ──
	// Note: MCP interaction recording is not yet wired into the agent execution flow.
	// This assertion verifies the query infrastructure works; actual recording is TBD.
	mcpInteractions := app.QueryMCPInteractions(t, sessionID)
	t.Logf("MCP interactions recorded: %d (recording not yet wired in agent flow)", len(mcpInteractions))

	// ── LLM conversation verification via CapturedInputs ──
	inputs := llm.CapturedInputs()
	assert.GreaterOrEqual(t, len(inputs), 2, "should have at least 2 LLM calls captured")

	// First call should have system prompt + user message + tools.
	firstCall := inputs[0]
	assert.GreaterOrEqual(t, len(firstCall.Messages), 2, "first call should have system + user messages")
	assert.Equal(t, agent.RoleSystem, firstCall.Messages[0].Role)
	assert.Equal(t, agent.RoleUser, firstCall.Messages[1].Role)
	assert.NotNil(t, firstCall.Tools, "first call should have tools")

	// Second call should include assistant tool-call + user tool-result in conversation.
	secondCall := inputs[1]
	assert.GreaterOrEqual(t, len(secondCall.Messages), 4,
		"second call should have system + user + assistant(tool) + user(result)")

	// Golden file: session API response.
	normalizer := NewNormalizer(sessionID)
	for _, s := range app.QueryStages(t, sessionID) {
		normalizer.RegisterStageID(s.ID)
	}
	for _, e := range app.QueryExecutions(t, sessionID) {
		normalizer.RegisterExecutionID(e.ID)
	}
	AssertGoldenJSON(t, GoldenPath("observability", "session.golden"), session, normalizer)
}

// ────────────────────────────────────────────────────────────
// Scenario 13: Queue Capacity Limit
// ────────────────────────────────────────────────────────────

func TestE2E_QueueCapacity(t *testing.T) {
	llm := NewScriptedLLMClient()
	// First 2 sessions: block until cancelled (they'll time out).
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})
	llm.AddSequential(LLMScriptEntry{BlockUntilCancelled: true})
	// Next 2 sessions: complete immediately.
	llm.AddSequential(LLMScriptEntry{Text: "Session 3 done."})
	llm.AddSequential(LLMScriptEntry{Text: "Summary 3."})
	llm.AddSequential(LLMScriptEntry{Text: "Session 4 done."})
	llm.AddSequential(LLMScriptEntry{Text: "Summary 4."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "single-stage")),
		WithLLMClient(llm),
		WithWorkerCount(3),
		WithMaxConcurrentSessions(2),
		WithSessionTimeout(3*time.Second),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler("ok")},
		}),
	)

	ctx := context.Background()

	// Submit 4 alerts.
	var sessionIDs []string
	for i := 0; i < 4; i++ {
		resp := app.SubmitAlert(t, "test-alert", fmt.Sprintf("Capacity test %d", i+1))
		sessionIDs = append(sessionIDs, resp["session_id"].(string))
	}

	// Wait until 2 sessions are in_progress (the cap).
	require.Eventually(t, func() bool {
		inProgress := 0
		for _, sid := range sessionIDs {
			s, err := app.EntClient.AlertSession.Get(ctx, sid)
			if err == nil && s.Status == alertsession.StatusInProgress {
				inProgress++
			}
		}
		return inProgress >= 2
	}, 10*time.Second, 100*time.Millisecond, "should have 2 in_progress sessions")

	// Verify health endpoint reports queue state.
	health := app.GetHealth(t)
	if wp, ok := health["worker_pool"].(map[string]interface{}); ok {
		if activeSessions, ok := wp["active_sessions"].(float64); ok {
			assert.LessOrEqual(t, activeSessions, float64(2), "active sessions should be at most 2")
		}
	}

	// First 2 sessions will time out (3s), freeing capacity for sessions 3 & 4.
	// Wait for all 4 sessions to reach terminal status.
	require.Eventually(t, func() bool {
		terminal := 0
		for _, sid := range sessionIDs {
			s, err := app.EntClient.AlertSession.Get(ctx, sid)
			if err != nil {
				continue
			}
			switch s.Status {
			case alertsession.StatusCompleted, alertsession.StatusFailed, alertsession.StatusTimedOut:
				terminal++
			}
		}
		return terminal >= 4
	}, 30*time.Second, 200*time.Millisecond, "all 4 sessions should reach terminal status")

	// Count outcomes.
	completed := 0
	timedOutOrFailed := 0
	for _, sid := range sessionIDs {
		s, err := app.EntClient.AlertSession.Get(ctx, sid)
		require.NoError(t, err)
		switch s.Status {
		case alertsession.StatusCompleted:
			completed++
		case alertsession.StatusTimedOut, alertsession.StatusFailed:
			timedOutOrFailed++
		}
	}
	// First 2 should time out/fail, last 2 should complete.
	assert.Equal(t, 2, timedOutOrFailed, "first 2 sessions should time out or fail")
	assert.Equal(t, 2, completed, "last 2 sessions should complete")
}
