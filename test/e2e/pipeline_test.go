package e2e

import (
	"context"
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
// Pipeline test — grows incrementally into the full pipeline test.
// Two stages: investigation (NativeThinking) → remediation (ReAct).
// Two MCP servers (test-mcp, prometheus-mcp), tool call summarization,
// and executive summary.
// ────────────────────────────────────────────────────────────

func TestE2E_Pipeline(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Stage 1: investigation (DataCollector, native-thinking) ──

	// Iteration 1: thinking + text + two tool calls from test-mcp.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the cluster nodes and pod status."},
			&agent.TextChunk{Content: "I'll look up the nodes and pods."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_nodes", Arguments: `{}`},
			&agent.ToolCallChunk{CallID: "call-2", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	// Tool result summarization for get_pods (triggered by size_threshold_tokens=100).
	llm.AddSequential(LLMScriptEntry{Text: "Pod pod-1 is OOMKilled with 5 restarts."})
	// Iteration 2: thinking + tool call from prometheus-mcp.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the memory metrics for pod-1."},
			&agent.TextChunk{Content: "Querying Prometheus for memory usage."},
			&agent.ToolCallChunk{CallID: "call-3", Name: "prometheus-mcp__query_metrics", Arguments: `{"query":"container_memory_usage_bytes{pod=\"pod-1\"}"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	// Iteration 3: thinking + final answer (no tools).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The pod is clearly OOMKilled."},
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 50, TotalTokens: 200},
		},
	})

	// ── Stage 2: remediation (Remediator, react) ──
	// ReAct uses text-based tool calling (Action/Action Input with dot notation).
	// Mirrors stage 1: tool call (no summary) → tool call (with summary) → final answer.

	// Iteration 1: tool call to test-mcp (small result, no summarization).
	llm.AddSequential(LLMScriptEntry{
		Text: "Thought: I should check the pod logs to understand the OOM pattern.\n" +
			"Action: test-mcp.get_pod_logs\n" +
			`Action Input: {"pod":"pod-1","namespace":"default"}`,
	})
	// Iteration 2: tool call to prometheus-mcp (large result, triggers summarization).
	llm.AddSequential(LLMScriptEntry{
		Text: "Thought: Let me check the Prometheus alert history for memory-related alerts.\n" +
			"Action: prometheus-mcp.query_alerts\n" +
			`Action Input: {"query":"ALERTS{alertname=\"OOMKilled\",pod=\"pod-1\"}"}`,
	})
	// Summarization for query_alerts result (triggered by size_threshold_tokens=100).
	llm.AddSequential(LLMScriptEntry{Text: "OOMKilled alert fired 3 times in the last hour for pod-1."})
	// Iteration 3: final answer.
	llm.AddSequential(LLMScriptEntry{
		Text: "Thought: The logs and alerts confirm repeated OOM kills due to memory pressure.\n" +
			"Final Answer: Recommend increasing memory limit to 1Gi and adding a HPA for pod-1.",
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOM killed due to memory leak. Recommend increasing memory limit."})

	// ── Tool results ──
	nodesResult := `[{"name":"worker-1","status":"Ready","cpu":"4","memory":"16Gi"}]`
	podsResult := `[` +
		`{"name":"pod-1","namespace":"default","status":"OOMKilled","restarts":5,"cpu":"250m","memory":"512Mi","node":"worker-1","image":"app:v1.2.3","started":"2026-01-15T10:00:00Z","lastRestart":"2026-01-15T14:30:00Z"},` +
		`{"name":"pod-2","namespace":"default","status":"Running","restarts":0,"cpu":"100m","memory":"256Mi","node":"worker-2","image":"app:v1.2.3","started":"2026-01-10T08:00:00Z","lastRestart":""},` +
		`{"name":"pod-3","namespace":"default","status":"CrashLoopBackOff","restarts":12,"cpu":"500m","memory":"1Gi","node":"worker-1","image":"app:v1.2.3","started":"2026-01-14T12:00:00Z","lastRestart":"2026-01-15T15:00:00Z"}` +
		`]`
	metricsResult := `[{"metric":"container_memory_usage_bytes","pod":"pod-1","value":"524288000","timestamp":"2026-01-15T14:29:00Z"}]`
	podLogsResult := `{"pod":"pod-1","logs":"OOMKilled at 14:30:00 - memory usage exceeded 512Mi limit"}`
	// Large alert result — triggers summarization (>100 tokens ≈ 400 chars).
	alertsResult := `[` +
		`{"alertname":"OOMKilled","pod":"pod-1","namespace":"default","severity":"critical","state":"firing","startsAt":"2026-01-15T14:30:00Z","summary":"Container killed due to OOM","description":"Pod pod-1 exceeded memory limit of 512Mi"},` +
		`{"alertname":"OOMKilled","pod":"pod-1","namespace":"default","severity":"critical","state":"resolved","startsAt":"2026-01-15T13:15:00Z","endsAt":"2026-01-15T13:20:00Z","summary":"Container killed due to OOM","description":"Pod pod-1 exceeded memory limit of 512Mi"},` +
		`{"alertname":"OOMKilled","pod":"pod-1","namespace":"default","severity":"critical","state":"resolved","startsAt":"2026-01-15T12:00:00Z","endsAt":"2026-01-15T12:05:00Z","summary":"Container killed due to OOM","description":"Pod pod-1 exceeded memory limit of 512Mi"}` +
		`]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "pipeline")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_nodes":    StaticToolHandler(nodesResult),
				"get_pods":     StaticToolHandler(podsResult),
				"get_pod_logs": StaticToolHandler(podLogsResult),
			},
			"prometheus-mcp": {
				"query_metrics": StaticToolHandler(metricsResult),
				"query_alerts":  StaticToolHandler(alertsResult),
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

	// Allow trailing WS events to arrive after session.status:completed.
	time.Sleep(200 * time.Millisecond)

	// Verify session via API.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// Verify DB state.
	stages := app.QueryStages(t, sessionID)
	assert.Len(t, stages, 2)
	assert.Equal(t, "investigation", stages[0].StageName)
	assert.Equal(t, "remediation", stages[1].StageName)

	execs := app.QueryExecutions(t, sessionID)
	assert.Len(t, execs, 2)
	assert.Equal(t, "DataCollector", execs[0].AgentName)
	assert.Equal(t, "Remediator", execs[1].AgentName)

	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline)

	// Verify LLM call count:
	// Stage 1: iteration 1 + summarization + iteration 2 + iteration 3 = 4
	// Stage 2: iteration 1 + iteration 2 + summarization + iteration 3 = 4
	// Executive summary: 1
	// Total: 9
	assert.Equal(t, 9, llm.CallCount())

	// Build normalizer with all known IDs for golden comparison.
	normalizer := NewNormalizer(sessionID)
	for _, s := range stages {
		normalizer.RegisterStageID(s.ID)
	}
	for _, e := range execs {
		normalizer.RegisterExecutionID(e.ID)
	}

	// Golden file assertions.
	AssertGoldenJSON(t, GoldenPath("pipeline", "session.golden"), session, normalizer)

	// WS event structural assertions (not golden — event ordering is non-deterministic
	// due to catchup/NOTIFY race, so we verify expected events in relative order).
	AssertEventsInOrder(t, ws.Events(), testdata.PipelineExpectedEvents)

	// Stages golden.
	projectedStages := make([]map[string]interface{}, len(stages))
	for i, s := range stages {
		projectedStages[i] = ProjectStageForGolden(s)
	}
	AssertGoldenJSON(t, GoldenPath("pipeline", "stages.golden"), projectedStages, normalizer)

	// Timeline golden.
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AssertGoldenJSON(t, GoldenPath("pipeline", "timeline.golden"), projectedTimeline, normalizer)
}
