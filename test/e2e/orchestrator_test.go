package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Orchestrator E2E test — comprehensive happy path.
//
// Single stage with SREOrchestrator (type=orchestrator) dispatching
// LogAnalyzer with MCP tools. Only one sub-agent is dispatched so that
// the orchestrator iteration count is deterministic (3 iterations):
//
//	Iteration 1: dispatch LogAnalyzer
//	Iteration 2: drain empty → text (no tools) → HasPending → WaitForResult blocks
//	  Sub-agent released → LogAnalyzer (MCP tool call + answer) → result injected
//	Iteration 3: drain empty (result consumed by WaitForResult) → final answer
//	Executive summary
//
// The 2-sub-agent dispatch scenario is exercised in the failure, list_agents,
// and cancellation tests which use structural assertions rather than golden files.
//
// Verifies: DB records, API responses, WS events, golden files, executive summary.
// ────────────────────────────────────────────────────────────

func TestE2E_Orchestrator(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Sub-agent release channel: closed after orchestrator's iteration 2
	// enters WaitForResult, ensuring deterministic iteration count.
	subAgentGate := make(chan struct{})

	// ── SREOrchestrator LLM entries ──

	// Iteration 1: thinking + dispatch LogAnalyzer.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "I need to investigate this alert. Let me dispatch LogAnalyzer to check error patterns."},
			&agent.ToolCallChunk{CallID: "orch-call-1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Find all error patterns in the last 30 minutes"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})
	// Iteration 2: thinking + text (no tool calls) → HasPending → WaitForResult blocks.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "I've dispatched LogAnalyzer. Waiting for results."},
			&agent.TextChunk{Content: "Waiting for sub-agent results to complete the investigation."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 30, TotalTokens: 330},
		},
	})
	// Iteration 3: final answer after seeing LogAnalyzer result.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "LogAnalyzer found 5xx errors from the payment service. Memory pressure from recent deployment."},
			&agent.TextChunk{Content: "Investigation complete: payment service has 2,847 5xx errors due to memory pressure from recent deployment. Recommend rollback and memory limit increase."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 60, TotalTokens: 560},
		},
	})

	// ── LogAnalyzer LLM entries (gated behind subAgentGate) ──

	// Iteration 1: thinking + MCP tool call to test-mcp/search_logs.
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me search the logs for error patterns."},
			&agent.ToolCallChunk{CallID: "la-call-1", Name: "test-mcp__search_logs",
				Arguments: `{"query":"error OR 5xx","timerange":"30m"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Iteration 2: final answer after tool result.
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Found significant error patterns in the logs."},
			&agent.TextChunk{Content: "Found 2,847 5xx errors in the last 30 minutes, primarily from the payment service."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Payment service alert: 2,847 5xx errors caused by memory pressure from recent deployment.",
	})

	// ── MCP tool results ──
	searchLogsResult := `[{"timestamp":"2026-02-25T14:30:00Z","level":"error","service":"payment","message":"OOM killed"},` +
		`{"timestamp":"2026-02-25T14:29:55Z","level":"error","service":"payment","message":"connection refused"}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"search_logs": StaticToolHandler(searchLogsResult),
			},
		}),
	)

	// Connect WS and subscribe.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-orchestrator", "Payment service 5xx errors spiking")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to be in_progress, then release sub-agent after a delay.
	// The delay ensures the orchestrator's iteration 2 has entered WaitForResult.
	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(subAgentGate)
	}()

	// Wait for session completion.
	app.WaitForSessionStatus(t, sessionID, "completed")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 10*time.Second, "expected session.status completed WS event")

	// ── Session API assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── DB assertions ──

	// Stages: 1 orchestrate stage.
	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 1)
	assert.Equal(t, "orchestrate", stages[0].StageName)
	assert.Equal(t, "completed", string(stages[0].Status))

	// Executions: 2 (orchestrator + 1 sub-agent).
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 2, "expected orchestrator + 1 sub-agent")

	var orchExec, laExec string
	for _, e := range execs {
		switch e.AgentName {
		case "SREOrchestrator":
			orchExec = e.ID
			assert.Nil(t, e.ParentExecutionID, "orchestrator should have no parent")
			assert.Equal(t, "completed", string(e.Status))
		case "LogAnalyzer":
			laExec = e.ID
			require.NotNil(t, e.ParentExecutionID, "sub-agent should have parent_execution_id")
			assert.Equal(t, orchExec, *e.ParentExecutionID)
			assert.Equal(t, "completed", string(e.Status))
			require.NotNil(t, e.Task)
			assert.Equal(t, "Find all error patterns in the last 30 minutes", *e.Task)
		}
	}
	assert.NotEmpty(t, orchExec, "orchestrator execution should exist")
	assert.NotEmpty(t, laExec, "LogAnalyzer execution should exist")

	// Timeline: task_assigned event for sub-agent.
	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline)

	var taskAssignedCount, finalAnalysisCount int
	for _, te := range timeline {
		if string(te.EventType) == "task_assigned" {
			taskAssignedCount++
		}
		if string(te.EventType) == "final_analysis" {
			finalAnalysisCount++
		}
	}
	assert.Equal(t, 1, taskAssignedCount, "should have 1 task_assigned event")
	assert.GreaterOrEqual(t, finalAnalysisCount, 2, "should have at least 2 final_analysis events (orchestrator + sub-agent)")

	// LLM call count: 3 (orchestrator) + 2 (LogAnalyzer) + 1 (executive summary) = 6
	assert.Equal(t, 6, llm.CallCount())

	// ── Trace API assertions ──
	traceList := app.GetTraceList(t, sessionID)
	traceStages, ok := traceList["stages"].([]interface{})
	require.True(t, ok, "stages should be an array")
	require.Len(t, traceStages, 1, "should have 1 stage")

	// Verify the orchestrator execution has nested sub-agents.
	stg := traceStages[0].(map[string]interface{})
	traceExecs := stg["executions"].([]interface{})
	require.Len(t, traceExecs, 1, "only the orchestrator should be at top level")
	orchTraceExec := traceExecs[0].(map[string]interface{})
	assert.Equal(t, "SREOrchestrator", orchTraceExec["agent_name"])

	subAgents, ok := orchTraceExec["sub_agents"].([]interface{})
	require.True(t, ok, "orchestrator should have sub_agents")
	require.Len(t, subAgents, 1, "should have 1 sub-agent")
	assert.Equal(t, "LogAnalyzer", subAgents[0].(map[string]interface{})["agent_name"])

	// Build normalizer with IDs registered in deterministic order.
	normalizer := NewNormalizer(sessionID)
	normalizer.RegisterStageID(stg["stage_id"].(string))
	normalizer.RegisterExecutionID(orchTraceExec["execution_id"].(string))

	// Register orchestrator interactions.
	for _, rawLI := range orchTraceExec["llm_interactions"].([]interface{}) {
		li := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
	}
	for _, rawMI := range orchTraceExec["mcp_interactions"].([]interface{}) {
		mi := rawMI.(map[string]interface{})
		normalizer.RegisterInteractionID(mi["id"].(string))
	}

	// Register sub-agent IDs and interactions.
	for _, rawSub := range subAgents {
		sub := rawSub.(map[string]interface{})
		normalizer.RegisterExecutionID(sub["execution_id"].(string))
		for _, rawLI := range sub["llm_interactions"].([]interface{}) {
			li := rawLI.(map[string]interface{})
			normalizer.RegisterInteractionID(li["id"].(string))
		}
		for _, rawMI := range sub["mcp_interactions"].([]interface{}) {
			mi := rawMI.(map[string]interface{})
			normalizer.RegisterInteractionID(mi["id"].(string))
		}
	}

	// Register session-level interactions (executive summary).
	traceSessionInteractions, _ := traceList["session_interactions"].([]interface{})
	for _, rawLI := range traceSessionInteractions {
		li := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
	}

	// ── Golden file assertions ──
	AssertGoldenJSON(t, GoldenPath("orchestrator", "session.golden"), session, normalizer)

	projectedStages := make([]map[string]interface{}, len(stages))
	for i, s := range stages {
		projectedStages[i] = ProjectStageForGolden(s)
	}
	AssertGoldenJSON(t, GoldenPath("orchestrator", "stages.golden"), projectedStages, normalizer)

	agentIndex := BuildAgentNameIndex(execs)
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AnnotateTimelineWithAgent(projectedTimeline, timeline, agentIndex)
	SortTimelineProjection(projectedTimeline)
	AssertGoldenJSON(t, GoldenPath("orchestrator", "timeline.golden"), projectedTimeline, normalizer)

	AssertGoldenJSON(t, GoldenPath("orchestrator", "trace_list.golden"), traceList, normalizer)

	// ── Trace interaction detail golden files ──
	type interactionEntry struct {
		Kind       string
		ID         string
		AgentName  string
		CreatedAt  string
		Label      string
		ServerName string
	}

	var allInteractions []interactionEntry

	// Collect interactions from orchestrator and sub-agents.
	for _, group := range []struct {
		name string
		data map[string]interface{}
	}{
		{orchTraceExec["agent_name"].(string), orchTraceExec},
	} {
		var entries []interactionEntry
		for _, rawLI := range group.data["llm_interactions"].([]interface{}) {
			li := rawLI.(map[string]interface{})
			entries = append(entries, interactionEntry{
				Kind: "llm", ID: li["id"].(string), AgentName: group.name,
				CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
			})
		}
		for _, rawMI := range group.data["mcp_interactions"].([]interface{}) {
			mi := rawMI.(map[string]interface{})
			label := mi["interaction_type"].(string)
			if tn, ok := mi["tool_name"].(string); ok && tn != "" {
				label = tn
			}
			serverName, _ := mi["server_name"].(string)
			entries = append(entries, interactionEntry{
				Kind: "mcp", ID: mi["id"].(string), AgentName: group.name,
				CreatedAt: mi["created_at"].(string), Label: label, ServerName: serverName,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt < entries[j].CreatedAt
		})
		allInteractions = append(allInteractions, entries...)
	}

	for _, rawSub := range subAgents {
		sub := rawSub.(map[string]interface{})
		subName := sub["agent_name"].(string)
		var entries []interactionEntry
		for _, rawLI := range sub["llm_interactions"].([]interface{}) {
			li := rawLI.(map[string]interface{})
			entries = append(entries, interactionEntry{
				Kind: "llm", ID: li["id"].(string), AgentName: subName,
				CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
			})
		}
		for _, rawMI := range sub["mcp_interactions"].([]interface{}) {
			mi := rawMI.(map[string]interface{})
			label := mi["interaction_type"].(string)
			if tn, ok := mi["tool_name"].(string); ok && tn != "" {
				label = tn
			}
			serverName, _ := mi["server_name"].(string)
			entries = append(entries, interactionEntry{
				Kind: "mcp", ID: mi["id"].(string), AgentName: subName,
				CreatedAt: mi["created_at"].(string), Label: label, ServerName: serverName,
			})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt < entries[j].CreatedAt
		})
		allInteractions = append(allInteractions, entries...)
	}

	// Session-level interactions (executive summary).
	for _, rawLI := range traceSessionInteractions {
		li := rawLI.(map[string]interface{})
		allInteractions = append(allInteractions, interactionEntry{
			Kind: "llm", ID: li["id"].(string), AgentName: "Session",
			CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
		})
	}

	iterationCounters := make(map[string]int)
	for idx, entry := range allInteractions {
		counterKey := entry.AgentName + "_" + entry.Label
		iterationCounters[counterKey]++
		count := iterationCounters[counterKey]

		label := strings.ReplaceAll(entry.Label, " ", "_")
		filename := fmt.Sprintf("%02d_%s_%s_%s_%d.golden", idx+1, entry.AgentName, entry.Kind, label, count)
		goldenPath := GoldenPath("orchestrator", filepath.Join("trace_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}

	// ── WS event assertions ──
	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)
	AssertEventsInOrder(t, wsEvents, testdata.OrchestratorExpectedEvents)
}

// ────────────────────────────────────────────────────────────
// Multi-agent happy path — the realistic production scenario.
//
// Orchestrator dispatches LogAnalyzer (custom agent with MCP tools) and
// GeneralWorker (built-in, pure reasoning). Both succeed. Asserts on
// end results rather than iteration counts since the exact number of
// orchestrator iterations is non-deterministic with concurrent sub-agents.
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorMultiAgent(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})

	// Orchestrator iteration 1: dispatch both sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Analyze recent error logs for the payment service"}`},
			&agent.ToolCallChunk{CallID: "orch-d2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Summarize the alert context and severity"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})
	// Iterations 2-4: text responses while waiting for sub-agents.
	// Exact count depends on timing; extras won't be consumed.
	for i := range 3 {
		llm.AddRouted("SREOrchestrator", LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: fmt.Sprintf("Waiting for sub-agent results (cycle %d).", i+1)},
				&agent.UsageChunk{InputTokens: 250, OutputTokens: 15, TotalTokens: 265},
			},
		})
	}
	// Final answer after receiving all results.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Both sub-agents completed. LogAnalyzer found 5xx errors from payment-service, " +
				"GeneralWorker assessed severity as P2. Root cause: memory pressure after deploy-1234."},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 80, TotalTokens: 680},
		},
	})

	// ── LogAnalyzer: MCP tool call + answer (gated) ──
	searchLogsResult := `[{"timestamp":"2026-02-25T14:30:00Z","level":"error","service":"payment","message":"OOM killed"}]`
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "la-call-1", Name: "test-mcp__search_logs",
				Arguments: `{"query":"error","timerange":"30m"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Found 1,203 5xx errors from payment-service in the last 30 minutes."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

	// ── GeneralWorker: pure reasoning, no tools (gated) ──
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Alert severity assessment: P2. Payment service errors spiking post-deployment."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 30, TotalTokens: 180},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{
		Text: "Payment service 5xx spike caused by memory pressure after deploy-1234. Severity: P2.",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"search_logs": StaticToolHandler(searchLogsResult),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-orchestrator", "Payment service 5xx errors after deploy-1234")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session: completed with analysis and summary ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── DB: 3 executions (orchestrator + 2 sub-agents), all completed ──
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3, "expected orchestrator + 2 sub-agents")

	agentNames := make(map[string]bool)
	var orchExecID string
	for _, e := range execs {
		agentNames[e.AgentName] = true
		assert.Equal(t, "completed", string(e.Status), "%s should be completed", e.AgentName)

		switch e.AgentName {
		case "SREOrchestrator":
			orchExecID = e.ID
			assert.Nil(t, e.ParentExecutionID)
		case "LogAnalyzer":
			require.NotNil(t, e.ParentExecutionID)
			assert.NotNil(t, e.Task)
			assert.Contains(t, *e.Task, "error logs")
		case "GeneralWorker":
			require.NotNil(t, e.ParentExecutionID)
			assert.NotNil(t, e.Task)
			assert.Contains(t, *e.Task, "alert context")
		}
	}
	assert.True(t, agentNames["SREOrchestrator"], "should have orchestrator")
	assert.True(t, agentNames["LogAnalyzer"], "should have LogAnalyzer")
	assert.True(t, agentNames["GeneralWorker"], "should have GeneralWorker")
	assert.NotEmpty(t, orchExecID)

	// Sub-agents point to orchestrator.
	for _, e := range execs {
		if e.ParentExecutionID != nil {
			assert.Equal(t, orchExecID, *e.ParentExecutionID)
		}
	}

	// ── Trace API: orchestrator with 2 nested sub-agents ──
	traceList := app.GetTraceList(t, sessionID)
	traceStages := traceList["stages"].([]interface{})
	require.Len(t, traceStages, 1)

	stg := traceStages[0].(map[string]interface{})
	traceExecs := stg["executions"].([]interface{})
	require.Len(t, traceExecs, 1, "only orchestrator at top level")

	orchTrace := traceExecs[0].(map[string]interface{})
	assert.Equal(t, "SREOrchestrator", orchTrace["agent_name"])

	subAgents := orchTrace["sub_agents"].([]interface{})
	require.Len(t, subAgents, 2, "orchestrator should have 2 nested sub-agents")

	subNames := make(map[string]bool)
	for _, raw := range subAgents {
		sub := raw.(map[string]interface{})
		subNames[sub["agent_name"].(string)] = true
	}
	assert.True(t, subNames["LogAnalyzer"])
	assert.True(t, subNames["GeneralWorker"])

	// ── WS: session completed event received ──
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 10*time.Second, "expected session.status completed WS event")

	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)
}

// ────────────────────────────────────────────────────────────
// Sub-agent failure test.
// Orchestrator dispatches 2 sub-agents. LogAnalyzer receives an LLM error,
// GeneralWorker succeeds. The orchestrator sees the failure result and
// produces a final answer.
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorSubAgentFailure(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})

	// Orchestrator iteration 1: dispatch both sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-call-1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Analyze logs"}`},
			&agent.ToolCallChunk{CallID: "orch-call-2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Analyze alert"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Orchestrator iteration 2: no tools → wait for sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Dispatched agents, waiting for results."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 15, TotalTokens: 165},
		},
	})
	// Orchestrator iteration 3: text → wait if one sub-agent still pending.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Received partial results."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 15, TotalTokens: 215},
		},
	})
	// Orchestrator iteration 4: sees failure + success → final answer.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "LogAnalyzer failed but GeneralWorker provided useful analysis. Proceeding with partial results."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 40, TotalTokens: 340},
		},
	})

	// LogAnalyzer: LLM errors (gated). Two entries to cover max_iterations=2.
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		WaitCh: subAgentGate,
		Error:  fmt.Errorf("model overloaded"),
	})
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		Error: fmt.Errorf("model overloaded"),
	})

	// GeneralWorker: succeeds (gated).
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Alert analysis: service degradation detected."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Partial investigation with one failed sub-agent."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator")),
		WithLLMClient(llm),
	)

	resp := app.SubmitAlert(t, "test-orchestrator", "Service degradation alert")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Session should complete despite sub-agent failure.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// Execution assertions.
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3)

	for _, e := range execs {
		switch e.AgentName {
		case "SREOrchestrator":
			assert.Equal(t, "completed", string(e.Status))
		case "LogAnalyzer":
			assert.Equal(t, "failed", string(e.Status))
			assert.NotNil(t, e.ErrorMessage)
		case "GeneralWorker":
			assert.Equal(t, "completed", string(e.Status))
		}
	}
}

// ────────────────────────────────────────────────────────────
// list_agents tool test.
// Orchestrator dispatches 2 sub-agents, calls list_agents, then produces
// final answer after sub-agents complete.
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorListAgents(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})

	// Orchestrator iteration 1: dispatch both + call list_agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Check logs"}`},
			&agent.ToolCallChunk{CallID: "orch-d2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Analyze data"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Orchestrator iteration 2: call list_agents to check status.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-list", Name: "list_agents", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 10, TotalTokens: 210},
		},
	})
	// Orchestrator iteration 3: text → wait for sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Checking agent status, waiting for completion."},
			&agent.UsageChunk{InputTokens: 250, OutputTokens: 15, TotalTokens: 265},
		},
	})
	// Orchestrator iteration 4: intermediate text after first result.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Received first result, waiting for second."},
			&agent.UsageChunk{InputTokens: 350, OutputTokens: 15, TotalTokens: 365},
		},
	})
	// Orchestrator iteration 5: final answer.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "All sub-agents completed. Investigation done."},
			&agent.UsageChunk{InputTokens: 400, OutputTokens: 30, TotalTokens: 430},
		},
	})

	// Sub-agents (gated).
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Log analysis complete: no critical errors found."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Data analysis complete: metrics are within normal range."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation complete, no issues found."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator")),
		WithLLMClient(llm),
	)

	resp := app.SubmitAlert(t, "test-orchestrator", "Routine check alert")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])

	// Verify list_agents tool call in timeline.
	timeline := app.QueryTimeline(t, sessionID)
	var listAgentsFound bool
	for _, te := range timeline {
		if string(te.EventType) == "llm_tool_call" && te.Metadata != nil {
			if tn, ok := te.Metadata["tool_name"]; ok && tn == "list_agents" {
				listAgentsFound = true
				break
			}
		}
	}
	assert.True(t, listAgentsFound, "should have a list_agents tool call in timeline")

	// Verify all 3 executions completed.
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3)
	for _, e := range execs {
		assert.Equal(t, "completed", string(e.Status),
			"execution %s (%s) should be completed", e.ID, e.AgentName)
	}
}
