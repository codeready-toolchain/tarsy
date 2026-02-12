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
// Currently: single NativeThinking agent, thinking + tool call + final answer + executive summary.
// ────────────────────────────────────────────────────────────

func TestE2E_Pipeline(t *testing.T) {
	// LLM script: thinking + tool call → tool result → thinking + final answer.
	llm := NewScriptedLLMClient()
	// Iteration 1: thinking + text alongside tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the pod status."},
			&agent.TextChunk{Content: "I'll look up the pods."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Iteration 2: thinking + final answer (no tools).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The pod is clearly OOMKilled."},
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 50, TotalTokens: 200},
		},
	})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOM killed due to memory leak."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "pipeline")),
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

	// Allow trailing WS events to arrive after session.status:completed.
	time.Sleep(200 * time.Millisecond)

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
