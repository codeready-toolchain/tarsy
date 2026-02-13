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
// Pipeline test — grows incrementally into the full pipeline test.
// Four stages + two synthesis stages + two chat messages:
//   1. investigation  (DataCollector, NativeThinking)
//   2. remediation    (Remediator, ReAct)
//   3. validation     (ConfigValidator react ∥ MetricsValidator native-thinking, forced conclusion)
//      → validation - Synthesis (synthesis-native-thinking)
//   4. scaling-review (ScalingReviewer x2 replicas, NativeThinking)
//      → scaling-review - Synthesis (plain synthesis)
//   + Chat 1: native-thinking with test-mcp tool call
//   + Chat 2: native-thinking with prometheus-mcp tool call
// Two MCP servers (test-mcp, prometheus-mcp), tool call summarization,
// parallel agents, replicas, both synthesis strategies, forced conclusion,
// executive summary, and follow-up chat with MCP tools.
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

	// ── Stage 3: validation (parallel: ConfigValidator react + MetricsValidator native-thinking) ──
	// Parallel agents use routed dispatch — LLM calls are matched by agent name.

	// ConfigValidator (react): 2 iterations.
	llm.AddRouted("ConfigValidator", LLMScriptEntry{
		Text: "Thought: I should verify the pod memory limits are properly configured.\n" +
			"Action: test-mcp.get_resource_config\n" +
			`Action Input: {"pod":"pod-1","namespace":"default"}`,
	})
	llm.AddRouted("ConfigValidator", LLMScriptEntry{
		Text: "Thought: The memory limit of 512Mi matches the alert threshold.\n" +
			"Final Answer: Config validated: pod-1 memory limit is 512Mi, matching the OOM threshold.",
	})

	// MetricsValidator (native-thinking): max_iterations=1 → forced conclusion.
	// Iteration 1: tool call consumes the single allowed iteration.
	llm.AddRouted("MetricsValidator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me verify the SLO metrics for pod-1."},
			&agent.TextChunk{Content: "Checking SLO compliance."},
			&agent.ToolCallChunk{CallID: "call-v1", Name: "prometheus-mcp__query_slo", Arguments: `{"pod":"pod-1"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	// Forced conclusion: called WITHOUT tools after max_iterations exhausted.
	llm.AddRouted("MetricsValidator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "SLO is being violated."},
			&agent.TextChunk{Content: "Metrics confirm SLO violation for pod-1 availability."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// ── Validation Synthesis (synthesis-native-thinking — includes thinking + Google Search grounding) ──
	// The test-provider has native_tools.google_search enabled, and synthesis-native-thinking
	// uses the google-native backend with no MCP tools, so native tools (Google Search) activate.
	// The mock returns a GroundingChunk simulating a Google Search result.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Combining ConfigValidator and MetricsValidator results."},
			&agent.TextChunk{Content: "Combined validation confirms pod-1 has correct memory limit of 512Mi but violates 99.9% availability SLO."},
			&agent.GroundingChunk{
				WebSearchQueries: []string{"kubernetes pod OOM memory limit best practices"},
				Sources: []agent.GroundingSource{
					{URI: "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/", Title: "Resource Management for Pods and Containers"},
				},
			},
			&agent.UsageChunk{InputTokens: 120, OutputTokens: 40, TotalTokens: 160},
		},
	})

	// ── Stage 4: scaling-review (ScalingReviewer x2 replicas, native-thinking) ──
	// Replicas run in parallel with the same agent config. Both extract "ScalingReviewer"
	// from custom instructions, so routed dispatch handles them (entries consumed in arrival order).

	// Both replica entries are identical — replicas are interchangeable and goroutine
	// scheduling determines which replica gets which entry. Identical entries ensure
	// golden file stability regardless of dispatch order.
	scalingReviewerEntry := LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Evaluating horizontal scaling needs for pod-1."},
			&agent.TextChunk{Content: "Current replicas=1 is insufficient. Recommend min=2 max=5 with 70% CPU target."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 25, TotalTokens: 105},
		},
	}
	llm.AddRouted("ScalingReviewer", scalingReviewerEntry)
	llm.AddRouted("ScalingReviewer", scalingReviewerEntry)

	// ── Scaling-review Synthesis (plain "synthesis" strategy — no thinking) ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Both replicas confirm: set HPA to 70% CPU with min=2, max=5 replicas for pod-1.",
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOM killed due to memory leak. Recommend increasing memory limit."})

	// ── Chat 1: "What caused the OOM?" — native-thinking with test-mcp tool call ──
	// Iteration 1: thinking + text + tool call to test-mcp/get_pods.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The user wants to know the OOM root cause. Let me check current pod status."},
			&agent.TextChunk{Content: "Let me check the current pod status to explain the OOM kill."},
			&agent.ToolCallChunk{CallID: "chat-call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	// Tool result summarization for get_pods (triggered by size_threshold_tokens=100).
	llm.AddSequential(LLMScriptEntry{Text: "Pod pod-1 is OOMKilled with 5 restarts in default namespace."})
	// Iteration 2: thinking + final answer (no tools).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The pod data confirms the OOM kill pattern."},
			&agent.TextChunk{Content: "Pod-1 was OOM killed because it exceeded the 512Mi memory limit. The pod has restarted 5 times due to this issue."},
			&agent.UsageChunk{InputTokens: 250, OutputTokens: 50, TotalTokens: 300},
		},
	})

	// ── Chat 2: "What are the current SLO metrics?" — native-thinking with prometheus-mcp tool call ──
	// Iteration 1: thinking + text + tool call to prometheus-mcp/query_slo.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The user wants current SLO status. Let me query Prometheus."},
			&agent.TextChunk{Content: "Let me check the current SLO metrics for pod-1."},
			&agent.ToolCallChunk{CallID: "chat-call-2", Name: "prometheus-mcp__query_slo", Arguments: `{"pod":"pod-1"}`},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 25, TotalTokens: 325},
		},
	})
	// Iteration 2: thinking + final answer (no tools).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The SLO data shows the availability target is not being met."},
			&agent.TextChunk{Content: "The current SLO metrics show pod-1 availability at 95%, well below the 99.9% target. This is a critical violation that needs immediate attention."},
			&agent.UsageChunk{InputTokens: 350, OutputTokens: 40, TotalTokens: 390},
		},
	})

	// ── Tool results ──
	nodesResult := `[{"name":"worker-1","status":"Ready","cpu":"4","memory":"16Gi"}]`
	podsResult := `[` +
		`{"name":"pod-1","namespace":"default","status":"OOMKilled","restarts":5,"cpu":"250m","memory":"512Mi","node":"worker-1","image":"app:v1.2.3","started":"2026-01-15T10:00:00Z","lastRestart":"2026-01-15T14:30:00Z"},` +
		`{"name":"pod-2","namespace":"default","status":"Running","restarts":0,"cpu":"100m","memory":"256Mi","node":"worker-2","image":"app:v1.2.3","started":"2026-01-10T08:00:00Z","lastRestart":""},` +
		`{"name":"pod-3","namespace":"default","status":"CrashLoopBackOff","restarts":12,"cpu":"500m","memory":"1Gi","node":"worker-1","image":"app:v1.2.3","started":"2026-01-14T12:00:00Z","lastRestart":"2026-01-15T15:00:00Z"}` +
		`]`
	metricsResult := `[{"metric":"container_memory_usage_bytes","pod":"pod-1","value":"524288000","timestamp":"2026-01-15T14:29:00Z"}]`
	podLogsResult := `{"pod":"pod-1","logs":"OOMKilled at 14:30:00 - memory usage exceeded 512Mi limit"}`
	resourceConfigResult := `{"pod":"pod-1","limits":{"memory":"512Mi","cpu":"250m"},"requests":{"memory":"256Mi","cpu":"100m"}}`
	sloResult := `[{"slo":"availability","target":0.999,"current":0.95,"pod":"pod-1","violation":true}]`
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
				"get_nodes":           StaticToolHandler(nodesResult),
				"get_pods":            StaticToolHandler(podsResult),
				"get_pod_logs":        StaticToolHandler(podLogsResult),
				"get_resource_config": StaticToolHandler(resourceConfigResult),
			},
			"prometheus-mcp": {
				"query_metrics": StaticToolHandler(metricsResult),
				"query_alerts":  StaticToolHandler(alertsResult),
				"query_slo":     StaticToolHandler(sloResult),
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

	// ── Chat messages (sent after session completes — chat requires terminal session) ──

	// Chat 1: "What caused the OOM?"
	chat1Resp := app.SendChatMessage(t, sessionID, "What caused the OOM kill for pod-1?")
	chat1StageID := chat1Resp["stage_id"].(string)
	require.NotEmpty(t, chat1StageID)
	app.WaitForStageStatus(t, chat1StageID, "completed")

	// Chat 2: "What are the current SLO metrics?"
	chat2Resp := app.SendChatMessage(t, sessionID, "What are the current SLO metrics for pod-1?")
	chat2StageID := chat2Resp["stage_id"].(string)
	require.NotEmpty(t, chat2StageID)
	app.WaitForStageStatus(t, chat2StageID, "completed")

	// Wait for the final WS event (chat 2 stage completed) instead of a fixed sleep.
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "stage.status" &&
			e.Parsed["stage_id"] == chat2StageID &&
			e.Parsed["status"] == "completed"
	}, 5*time.Second, "expected Chat Response stage.status completed WS event for chat 2")

	// Verify session via API.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// Verify DB state — 6 pipeline stages + 2 chat stages.
	stages := app.QueryStages(t, sessionID)
	assert.Len(t, stages, 8)
	assert.Equal(t, "investigation", stages[0].StageName)
	assert.Equal(t, "remediation", stages[1].StageName)
	assert.Equal(t, "validation", stages[2].StageName)
	assert.Equal(t, "validation - Synthesis", stages[3].StageName)
	assert.Equal(t, "scaling-review", stages[4].StageName)
	assert.Equal(t, "scaling-review - Synthesis", stages[5].StageName)
	assert.Equal(t, "Chat Response", stages[6].StageName)
	assert.Equal(t, "Chat Response", stages[7].StageName)

	// 8 pipeline execs + 2 chat execs = 10.
	execs := app.QueryExecutions(t, sessionID)
	assert.Len(t, execs, 10)
	assert.Equal(t, "DataCollector", execs[0].AgentName)
	assert.Equal(t, "Remediator", execs[1].AgentName)
	// Validation parallel agents — order may vary, so check by name set.
	validationNames := map[string]bool{execs[2].AgentName: true, execs[3].AgentName: true}
	assert.True(t, validationNames["ConfigValidator"], "expected ConfigValidator execution")
	assert.True(t, validationNames["MetricsValidator"], "expected MetricsValidator execution")
	assert.Equal(t, "SynthesisAgent", execs[4].AgentName)
	// Scaling-review replicas — order may vary, so check by name set.
	replicaNames := map[string]bool{execs[5].AgentName: true, execs[6].AgentName: true}
	assert.True(t, replicaNames["ScalingReviewer-1"], "expected ScalingReviewer-1 execution")
	assert.True(t, replicaNames["ScalingReviewer-2"], "expected ScalingReviewer-2 execution")
	assert.Equal(t, "SynthesisAgent", execs[7].AgentName)
	// Chat executions — both use the built-in ChatAgent.
	assert.Equal(t, "ChatAgent", execs[8].AgentName)
	assert.Equal(t, "ChatAgent", execs[9].AgentName)

	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline)

	// ── Timeline API verification ──────────────────────────────
	// Verify the GET /sessions/:id/timeline endpoint returns correct data.
	apiTimeline := app.GetTimeline(t, sessionID)
	require.Len(t, apiTimeline, len(timeline),
		"API timeline event count must match DB query")

	// Verify each event has required fields and correct values.
	for i, raw := range apiTimeline {
		event, ok := raw.(map[string]interface{})
		require.True(t, ok, "timeline event %d should be a JSON object", i)

		// Required fields must be present.
		assert.NotEmpty(t, event["id"], "event %d: id required", i)
		assert.NotEmpty(t, event["session_id"], "event %d: session_id required", i)
		assert.NotEmpty(t, event["event_type"], "event %d: event_type required", i)
		assert.NotEmpty(t, event["status"], "event %d: status required", i)

		// All events belong to this session.
		assert.Equal(t, sessionID, event["session_id"], "event %d: wrong session_id", i)

		// Sequence numbers are in ascending order (API returns ordered).
		seq := toInt(event["sequence_number"])
		if i > 0 {
			prevEvent, _ := apiTimeline[i-1].(map[string]interface{})
			prevSeq := toInt(prevEvent["sequence_number"])
			assert.GreaterOrEqual(t, seq, prevSeq,
				"event %d: sequence_number %d should be >= previous %d", i, seq, prevSeq)
		}

		// Cross-reference with DB: event IDs must match.
		assert.Equal(t, timeline[i].ID, event["id"],
			"event %d: API id must match DB id", i)
		assert.Equal(t, string(timeline[i].EventType), event["event_type"],
			"event %d: API event_type must match DB", i)
		assert.Equal(t, string(timeline[i].Status), event["status"],
			"event %d: API status must match DB", i)
		assert.Equal(t, timeline[i].Content, event["content"],
			"event %d: API content must match DB", i)
	}

	// Verify LLM call count:
	// Stage 1: iteration 1 + summarization + iteration 2 + iteration 3 = 4
	// Stage 2: iteration 1 + iteration 2 + summarization + iteration 3 = 4
	// Stage 3: ConfigValidator (2) + MetricsValidator (1 iteration + 1 forced conclusion) = 4
	// Validation Synthesis: 1
	// Stage 4: ScalingReviewer-1 (1) + ScalingReviewer-2 (1) = 2
	// Scaling-review Synthesis: 1
	// Executive summary: 1
	// Chat 1: iteration 1 (tool call) + summarization + iteration 2 (answer) = 3
	// Chat 2: iteration 1 (tool call) + iteration 2 (answer) = 2
	// Total: 22
	assert.Equal(t, 22, llm.CallCount())

	// ── Debug API (fetch first — used to register IDs in deterministic order) ──
	//
	// The debug list endpoint returns executions in stage_index + agent_index
	// order, which is deterministic even for parallel agents. We use this order
	// to register execution and interaction IDs BEFORE running golden assertions,
	// so placeholder numbering is stable across runs regardless of parallel
	// agent start times.
	debugList := app.GetDebugList(t, sessionID)
	debugStages, ok := debugList["stages"].([]interface{})
	require.True(t, ok, "stages should be an array")
	require.NotEmpty(t, debugStages, "should have stage groups")

	// Build normalizer with IDs registered in deterministic order.
	// The debug list is ordered by stage_index → agent_index, so placeholder
	// numbering is stable regardless of parallel agent start times.
	normalizer := NewNormalizer(sessionID)
	for si, rawStage := range debugStages {
		stg, ok := rawStage.(map[string]interface{})
		require.True(t, ok, "debug stage %d: expected object", si)
		stageID, ok := stg["stage_id"].(string)
		require.True(t, ok, "debug stage %d: stage_id missing or not a string", si)
		normalizer.RegisterStageID(stageID)

		executions, ok := stg["executions"].([]interface{})
		require.True(t, ok, "debug stage %d: executions missing or not an array", si)
		for ei, rawExec := range executions {
			exec, ok := rawExec.(map[string]interface{})
			require.True(t, ok, "debug stage %d exec %d: expected object", si, ei)
			execID, ok := exec["execution_id"].(string)
			require.True(t, ok, "debug stage %d exec %d: execution_id missing or not a string", si, ei)
			normalizer.RegisterExecutionID(execID)

			llmInteractions, _ := exec["llm_interactions"].([]interface{})
			for _, rawLI := range llmInteractions {
				li, _ := rawLI.(map[string]interface{})
				if id, ok := li["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
			mcpInteractions, _ := exec["mcp_interactions"].([]interface{})
			for _, rawMI := range mcpInteractions {
				mi, _ := rawMI.(map[string]interface{})
				if id, ok := mi["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
		}
	}

	// Register session-level interactions (e.g. executive summary).
	debugSessionInteractions, _ := debugList["session_interactions"].([]interface{})
	for _, rawLI := range debugSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
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

	// Timeline golden — sort deterministically by agent name since parallel agents
	// produce events at overlapping sequence numbers in non-deterministic order.
	// Agent names are stable strings (unlike execution UUIDs), so sort order is deterministic.
	agentIndex := BuildAgentNameIndex(execs)
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AnnotateTimelineWithAgent(projectedTimeline, timeline, agentIndex)
	SortTimelineProjection(projectedTimeline)
	AssertGoldenJSON(t, GoldenPath("pipeline", "timeline.golden"), projectedTimeline, normalizer)

	// Timeline API golden — project the API response the same way as the DB
	// response and verify it produces the identical golden output. This proves
	// the REST endpoint returns the exact same data as the direct DB query.
	apiProjectedTimeline := make([]map[string]interface{}, len(apiTimeline))
	for i, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		apiProjectedTimeline[i] = ProjectAPITimelineForGolden(event)
	}
	AnnotateAPITimelineWithAgent(apiProjectedTimeline, apiTimeline, agentIndex)
	SortTimelineProjection(apiProjectedTimeline)
	AssertGoldenJSON(t, GoldenPath("pipeline", "timeline.golden"), apiProjectedTimeline, normalizer)

	// ── Debug golden assertions ─────────────────────────────────
	AssertGoldenJSON(t, GoldenPath("pipeline", "debug_list.golden"), debugList, normalizer)

	// ── Level 2: Verify ALL interaction details in chronological order ──
	//
	// Collect all LLM and MCP interactions from every execution into a
	// unified slice, sort by created_at, and verify each one against its
	// own human-readable golden file.

	type interactionEntry struct {
		Kind      string // "llm" or "mcp"
		ID        string
		AgentName string
		CreatedAt string
		Label     string // interaction_type or tool_name
	}

	// Collect interactions per-execution in chronological order.
	// Within a single execution, LLM and MCP interactions are merged
	// by created_at (deterministic because one agent runs sequentially).
	// Across executions we use the debug list's structural order
	// (stage_index → agent_index), which is deterministic even for
	// parallel agents where absolute timestamps vary between runs.
	var allInteractions []interactionEntry
	for _, rawStage := range debugStages {
		stg, _ := rawStage.(map[string]interface{})
		for _, rawExec := range stg["executions"].([]interface{}) {
			exec, _ := rawExec.(map[string]interface{})
			agentName, _ := exec["agent_name"].(string)

			// Collect all interactions for this execution.
			var execInteractions []interactionEntry
			for _, rawLI := range exec["llm_interactions"].([]interface{}) {
				li, _ := rawLI.(map[string]interface{})
				execInteractions = append(execInteractions, interactionEntry{
					Kind:      "llm",
					ID:        li["id"].(string),
					AgentName: agentName,
					CreatedAt: li["created_at"].(string),
					Label:     li["interaction_type"].(string),
				})
			}
			for _, rawMI := range exec["mcp_interactions"].([]interface{}) {
				mi, _ := rawMI.(map[string]interface{})
				label := mi["interaction_type"].(string)
				if tn, ok := mi["tool_name"].(string); ok && tn != "" {
					label = tn
				}
				execInteractions = append(execInteractions, interactionEntry{
					Kind:      "mcp",
					ID:        mi["id"].(string),
					AgentName: agentName,
					CreatedAt: mi["created_at"].(string),
					Label:     label,
				})
			}
			// Sort within execution by created_at (deterministic for single agent).
			sort.Slice(execInteractions, func(i, j int) bool {
				return execInteractions[i].CreatedAt < execInteractions[j].CreatedAt
			})
			allInteractions = append(allInteractions, execInteractions...)
		}
	}

	// Append session-level interactions (e.g. executive summary) after stages.
	for _, rawLI := range debugSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		allInteractions = append(allInteractions, interactionEntry{
			Kind:      "llm",
			ID:        li["id"].(string),
			AgentName: "Session",
			CreatedAt: li["created_at"].(string),
			Label:     li["interaction_type"].(string),
		})
	}

	// Track per-agent iteration counters for readable filenames.
	iterationCounters := make(map[string]int) // "AgentName_type" → count

	for idx, entry := range allInteractions {
		// Build a counter key to disambiguate multiple iterations of the same type.
		counterKey := entry.AgentName + "_" + entry.Label
		iterationCounters[counterKey]++
		count := iterationCounters[counterKey]

		// Build filename: 01_DataCollector_llm_iteration_1.golden
		label := strings.ReplaceAll(entry.Label, " ", "_")
		filename := fmt.Sprintf("%02d_%s_%s_%s_%d.golden", idx+1, entry.AgentName, entry.Kind, label, count)
		goldenPath := GoldenPath("pipeline", filepath.Join("debug_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}
}
