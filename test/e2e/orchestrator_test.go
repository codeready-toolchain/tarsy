package e2e

import (
	"context"
	"encoding/json"
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

	// Synchronization: orchIter2Ready signals that the orchestrator has
	// entered Generate for iteration 2. We then release orchIter2Gate
	// (let iteration 2 proceed) with a small delay before opening
	// subAgentGate to ensure iteration 2 finishes and enters WaitForResult.
	subAgentGate := make(chan struct{})
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

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
	// WaitCh+OnBlock ensure we release the sub-agent gate only after the
	// orchestrator has reached this Generate call, avoiding a timing race.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
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

	// Release gates: wait for orchestrator iteration 2 to enter Generate,
	// let it proceed, then open the sub-agent gate after iteration 2
	// has had time to process the response and enter WaitForResult.
	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		<-orchIter2Ready
		close(orchIter2Gate)
		time.Sleep(50 * time.Millisecond)
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
	var laParentID *string
	for _, e := range execs {
		switch e.AgentName {
		case "SREOrchestrator":
			orchExec = e.ID
			assert.Nil(t, e.ParentExecutionID, "orchestrator should have no parent")
			assert.Equal(t, "completed", string(e.Status))
		case "LogAnalyzer":
			laExec = e.ID
			laParentID = e.ParentExecutionID
			assert.Equal(t, "completed", string(e.Status))
			require.NotNil(t, e.Task)
			assert.Equal(t, "Find all error patterns in the last 30 minutes", *e.Task)
		}
	}
	assert.NotEmpty(t, orchExec, "orchestrator execution should exist")
	assert.NotEmpty(t, laExec, "LogAnalyzer execution should exist")
	require.NotNil(t, laParentID, "sub-agent should have parent_execution_id")
	assert.Equal(t, orchExec, *laParentID, "sub-agent parent should be orchestrator")

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
			if entries[i].CreatedAt != entries[j].CreatedAt {
				return entries[i].CreatedAt < entries[j].CreatedAt
			}
			return entries[i].ID < entries[j].ID
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
			if entries[i].CreatedAt != entries[j].CreatedAt {
				return entries[i].CreatedAt < entries[j].CreatedAt
			}
			return entries[i].ID < entries[j].ID
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
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

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
	// Iteration 2: text → WaitForResult. OnBlock signals readiness.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Waiting for sub-agent results (cycle 1)."},
			&agent.UsageChunk{InputTokens: 250, OutputTokens: 15, TotalTokens: 265},
		},
	})
	// Iterations 3-4: buffer for timing variance (may or may not be consumed).
	for i := range 2 {
		llm.AddRouted("SREOrchestrator", LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: fmt.Sprintf("Waiting for sub-agent results (cycle %d).", i+2)},
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
		<-orchIter2Ready
		close(orchIter2Gate)
		time.Sleep(50 * time.Millisecond)
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
// Multi-phase dispatch — reactive orchestration pattern.
//
// Phase 1: orchestrator dispatches LogAnalyzer (MCP tools) and GeneralWorker
//   (severity assessment) in parallel.
// Phase 2: after receiving phase 1 results, orchestrator dispatches
//   GeneralWorker again with a follow-up remediation task.
//
// Tests: same agent dispatched twice, reactive dispatch based on earlier
// results, 4 total executions (orchestrator + 3 sub-agents).
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorMultiPhase(t *testing.T) {
	llm := NewScriptedLLMClient()

	phase1Gate := make(chan struct{})
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

	// ── SREOrchestrator LLM entries ──

	// Iteration 1: dispatch LogAnalyzer + GeneralWorker (phase 1, parallel).
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Check error logs for payment-service in the last 30 minutes"}`},
			&agent.ToolCallChunk{CallID: "orch-d2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Assess alert severity and blast radius"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})
	// Iteration 2: text → WaitForResult (phase 1 agents still running).
	// OnBlock signals when orchestrator reaches this Generate call.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Dispatched LogAnalyzer and GeneralWorker. Waiting for phase 1 results."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 20, TotalTokens: 320},
		},
	})
	// Iteration 3: reactive dispatch — GeneralWorker again with follow-up task.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d3", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Analyze the OOM pattern from LogAnalyzer findings and recommend remediation steps"}`},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 30, TotalTokens: 530},
		},
	})
	// Iteration 4: text → WaitForResult (phase 2 agent running).
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Waiting for remediation analysis."},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 15, TotalTokens: 615},
		},
	})
	// Buffer entries for timing variance (may or may not be consumed).
	for i := range 2 {
		llm.AddRouted("SREOrchestrator", LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: fmt.Sprintf("Processing results (cycle %d).", i+1)},
				&agent.UsageChunk{InputTokens: 650, OutputTokens: 15, TotalTokens: 665},
			},
		})
	}
	// Final answer combining all 3 sub-agent results.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete. LogAnalyzer found 1,203 5xx errors (OOM pattern) from payment-service. " +
				"Severity assessed as P2. Remediation: rollback deploy-1234 and increase memory limits to 2Gi."},
			&agent.UsageChunk{InputTokens: 800, OutputTokens: 80, TotalTokens: 880},
		},
	})

	// ── LogAnalyzer: MCP tool call + answer (phase 1, gated) ──
	searchLogsResult := `[{"timestamp":"2026-02-25T14:30:00Z","level":"error","service":"payment","message":"OOM killed"}]`
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		WaitCh: phase1Gate,
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "la-call-1", Name: "test-mcp__search_logs",
				Arguments: `{"query":"error OR 5xx","timerange":"30m"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Found 1,203 5xx errors from payment-service. OOM pattern detected: pods restarting with memory pressure."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

	// ── GeneralWorker: 2 entries consumed FIFO ──
	// Entry 1 (phase 1, gated): severity assessment.
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		WaitCh: phase1Gate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Severity assessment: P2. Payment service errors spiking post-deployment. Blast radius: checkout flow affected."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 30, TotalTokens: 180},
		},
	})
	// Entry 2 (phase 2, no gate): remediation analysis.
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Remediation: rollback deploy-1234 to previous version, increase memory limits from 1Gi to 2Gi."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 35, TotalTokens: 235},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{
		Text: "Payment service OOM after deploy-1234. P2 severity. Rollback and increase memory limits.",
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
		<-orchIter2Ready
		close(orchIter2Gate)
		time.Sleep(50 * time.Millisecond)
		close(phase1Gate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session: completed with analysis and summary ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── DB: 4 executions (orchestrator + 3 sub-agents) ──
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 4, "expected orchestrator + 3 sub-agents")

	var orchExecID string
	var gwTasks []string
	agentCounts := make(map[string]int)
	for _, e := range execs {
		agentCounts[e.AgentName]++
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
			gwTasks = append(gwTasks, *e.Task)
		}
	}

	assert.Equal(t, 1, agentCounts["SREOrchestrator"])
	assert.Equal(t, 1, agentCounts["LogAnalyzer"])
	assert.Equal(t, 2, agentCounts["GeneralWorker"], "GeneralWorker should be dispatched twice")
	assert.NotEmpty(t, orchExecID)

	// Verify the two GeneralWorker tasks are different (phase 1 vs phase 2).
	require.Len(t, gwTasks, 2)
	assert.NotEqual(t, gwTasks[0], gwTasks[1], "GeneralWorker tasks should differ across phases")

	// Sub-agents point to orchestrator.
	for _, e := range execs {
		if e.ParentExecutionID != nil {
			assert.Equal(t, orchExecID, *e.ParentExecutionID)
		}
	}

	// ── Timeline: 3 dispatch_agent tool calls ──
	timeline := app.QueryTimeline(t, sessionID)
	var dispatchCount int
	for _, te := range timeline {
		if string(te.EventType) == "llm_tool_call" && te.Metadata != nil {
			if tn, ok := te.Metadata["tool_name"]; ok && tn == "dispatch_agent" {
				dispatchCount++
			}
		}
	}
	assert.Equal(t, 3, dispatchCount, "should have 3 dispatch_agent tool calls (2 phase-1 + 1 phase-2)")

	// ── Trace API: orchestrator with 3 nested sub-agents ──
	traceList := app.GetTraceList(t, sessionID)
	traceStages := traceList["stages"].([]interface{})
	require.Len(t, traceStages, 1)

	stg := traceStages[0].(map[string]interface{})
	traceExecs := stg["executions"].([]interface{})
	require.Len(t, traceExecs, 1, "only orchestrator at top level")

	orchTrace := traceExecs[0].(map[string]interface{})
	assert.Equal(t, "SREOrchestrator", orchTrace["agent_name"])

	subAgents := orchTrace["sub_agents"].([]interface{})
	require.Len(t, subAgents, 3, "orchestrator should have 3 nested sub-agents")

	subNameCounts := make(map[string]int)
	for _, raw := range subAgents {
		sub := raw.(map[string]interface{})
		subNameCounts[sub["agent_name"].(string)]++
	}
	assert.Equal(t, 1, subNameCounts["LogAnalyzer"])
	assert.Equal(t, 2, subNameCounts["GeneralWorker"])

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
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

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
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
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
		<-orchIter2Ready
		close(orchIter2Gate)
		time.Sleep(50 * time.Millisecond)
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
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

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
	// OnBlock signals when orchestrator reaches this Generate call.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
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
		<-orchIter2Ready
		close(orchIter2Gate)
		time.Sleep(50 * time.Millisecond)
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

// ────────────────────────────────────────────────────────────
// cancel_agent tool test.
//
// Orchestrator dispatches LogAnalyzer and GeneralWorker. GeneralWorker
// completes quickly. Orchestrator then calls cancel_agent to cancel the
// slow LogAnalyzer, and produces a final answer from GeneralWorker's
// result alone.
//
// Uses RewriteChunks on the cancel_agent entry to dynamically inject the
// real execution_id (extracted from the dispatch_agent tool result in the
// conversation history).
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorCancelSpecific(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})
	laBlocked := make(chan struct{}, 1)

	// Orchestrator iteration 1: dispatch both sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Deep log analysis of payment-service errors"}`},
			&agent.ToolCallChunk{CallID: "orch-d2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Quick severity assessment"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})
	// Iteration 2: text → wait for first result.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Waiting for sub-agent results."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 15, TotalTokens: 315},
		},
	})
	// Iteration 3: cancel LogAnalyzer (too slow). RewriteChunks patches the
	// execution_id from the dispatch_agent result in conversation history.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-cancel", Name: "cancel_agent",
				Arguments: `{"execution_id":"PLACEHOLDER"}`},
			&agent.UsageChunk{InputTokens: 400, OutputTokens: 20, TotalTokens: 420},
		},
		RewriteChunks: cancelAgentRewriter("LogAnalyzer"),
	})
	// Buffer entries for timing after cancel result + cancelled sub-agent result.
	for i := range 2 {
		llm.AddRouted("SREOrchestrator", LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: fmt.Sprintf("Processing cancellation (cycle %d).", i+1)},
				&agent.UsageChunk{InputTokens: 450, OutputTokens: 15, TotalTokens: 465},
			},
		})
	}
	// Final answer using GeneralWorker result only.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "LogAnalyzer was too slow and has been cancelled. Based on GeneralWorker's assessment: P3 severity, no immediate action required."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 50, TotalTokens: 550},
		},
	})

	// LogAnalyzer: blocks until cancelled. OnBlock signals when it's blocked.
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		BlockUntilCancelled: true,
		OnBlock:             laBlocked,
	})

	// GeneralWorker: quick result (gated so it doesn't run before dispatch).
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Quick assessment: P3 severity. Payment errors elevated but within SLO budget."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 25, TotalTokens: 125},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{
		Text: "P3 severity. Payment errors elevated but within SLO budget. LogAnalyzer cancelled.",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator")),
		WithLLMClient(llm),
	)

	resp := app.SubmitAlert(t, "test-orchestrator", "Payment service elevated error rate")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "in_progress")

	// Wait for LogAnalyzer to be blocked, then release GeneralWorker.
	<-laBlocked
	close(subAgentGate)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session: completed ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// ── DB: 3 executions — orchestrator+GW completed, LA cancelled ──
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3, "expected orchestrator + 2 sub-agents")

	for _, e := range execs {
		switch e.AgentName {
		case "SREOrchestrator":
			assert.Equal(t, "completed", string(e.Status))
		case "LogAnalyzer":
			assert.Equal(t, "cancelled", string(e.Status),
				"LogAnalyzer should be cancelled via cancel_agent tool")
		case "GeneralWorker":
			assert.Equal(t, "completed", string(e.Status))
		}
	}

	// ── Timeline: cancel_agent tool call present ──
	timeline := app.QueryTimeline(t, sessionID)
	var cancelAgentFound bool
	for _, te := range timeline {
		if string(te.EventType) == "llm_tool_call" && te.Metadata != nil {
			if tn, ok := te.Metadata["tool_name"]; ok && tn == "cancel_agent" {
				cancelAgentFound = true
				break
			}
		}
	}
	assert.True(t, cancelAgentFound, "should have a cancel_agent tool call in timeline")

	// ── Trace API: 2 sub-agents nested under orchestrator ──
	traceList := app.GetTraceList(t, sessionID)
	traceStages := traceList["stages"].([]interface{})
	require.Len(t, traceStages, 1)

	stg := traceStages[0].(map[string]interface{})
	traceExecs := stg["executions"].([]interface{})
	require.Len(t, traceExecs, 1, "only orchestrator at top level")

	subAgents := traceExecs[0].(map[string]interface{})["sub_agents"].([]interface{})
	require.Len(t, subAgents, 2, "orchestrator should have 2 nested sub-agents")
}

// cancelAgentRewriter returns a RewriteChunks function that patches
// cancel_agent ToolCallChunk arguments with the real execution_id of the
// named agent, extracted from dispatch_agent results in the conversation.
func cancelAgentRewriter(targetAgent string) func([]agent.ConversationMessage, []agent.Chunk) []agent.Chunk {
	return func(messages []agent.ConversationMessage, chunks []agent.Chunk) []agent.Chunk {
		execID := findDispatchedExecID(messages, targetAgent)
		if execID == "" {
			return chunks
		}

		rewritten := make([]agent.Chunk, len(chunks))
		copy(rewritten, chunks)
		for i, ch := range rewritten {
			if tc, ok := ch.(*agent.ToolCallChunk); ok && tc.Name == "cancel_agent" {
				clone := *tc
				clone.Arguments = fmt.Sprintf(`{"execution_id":"%s"}`, execID)
				rewritten[i] = &clone
			}
		}
		return rewritten
	}
}

// findDispatchedExecID scans the conversation for a dispatch_agent tool call
// targeting the named agent and returns the execution_id from its tool result.
func findDispatchedExecID(messages []agent.ConversationMessage, agentName string) string {
	var targetCallID string
	for _, msg := range messages {
		if msg.Role != agent.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.Name != "dispatch_agent" {
				continue
			}
			var args struct{ Name string }
			if json.Unmarshal([]byte(tc.Arguments), &args) == nil && args.Name == agentName {
				targetCallID = tc.ID
				break
			}
		}
		if targetCallID != "" {
			break
		}
	}
	if targetCallID == "" {
		return ""
	}

	for _, msg := range messages {
		if msg.Role == agent.RoleTool && msg.ToolCallID == targetCallID {
			var result struct{ ExecutionID string `json:"execution_id"` }
			if json.Unmarshal([]byte(msg.Content), &result) == nil {
				return result.ExecutionID
			}
		}
	}
	return ""
}
