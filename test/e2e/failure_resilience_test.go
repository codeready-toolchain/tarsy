package e2e

import (
	"context"
	"fmt"
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
// FailureResilience test — Scenarios 5 (Policy Any) + 8 (Exec Summary Fail-Open).
// Two-stage chain + exec summary failure:
//   1. analysis (Analyzer ∥ Investigator, policy=any)
//      Analyzer: LLM error → fails (max_iterations=1)
//      Investigator: tool call + final answer → succeeds
//      → analysis - Synthesis (synthesis-native-thinking)
//   2. summary (Summarizer) — succeeds
//   Executive summary: LLM error → fail-open, session still completed
//
// Verifies: policy=any resilience (stage succeeds despite one agent failure),
// synthesis with partial results, executive summary fail-open (session completed
// with executive_summary_error populated), timeline event statuses via API.
// ────────────────────────────────────────────────────────────

func TestE2E_FailureResilience(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Stage 1: analysis (parallel, policy=any) ──

	// Routed: Analyzer — LLM error (max_iterations=1 → fails immediately).
	llm.AddRouted("Analyzer", LLMScriptEntry{
		Error: fmt.Errorf("LLM service unavailable"),
	})

	// Routed: Investigator — Iteration 1: thinking + text + tool call.
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the system status."},
			&agent.TextChunk{Content: "Checking system status to investigate the alert."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__check_status", Arguments: `{"component":"api-server"}`},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 25, TotalTokens: 85},
		},
	})
	// Routed: Investigator — Iteration 2: thinking + final answer.
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "System check complete, API server is healthy."},
			&agent.TextChunk{Content: "Investigation complete: API server is healthy, alert was transient."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 35, TotalTokens: 135},
		},
	})

	// ── Synthesis (synthesis-native-thinking) — falls to sequential dispatch ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "One agent succeeded, one failed. Summarizing available results."},
			&agent.TextChunk{Content: "Synthesis: Investigator confirmed API server is healthy. Analyzer failed due to LLM error."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 30, TotalTokens: 110},
		},
	})

	// ── Stage 2: summary (Summarizer) — falls to sequential dispatch ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Creating final summary of the investigation."},
			&agent.TextChunk{Content: "Summary: API server alert was transient. No action required."},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
		},
	})

	// ── Executive summary — LLM error (fail-open) ──
	llm.AddSequential(LLMScriptEntry{
		Error: fmt.Errorf("executive summary model overloaded"),
	})

	// ── MCP tool results ──
	statusResult := `{"component":"api-server","status":"healthy","uptime":"48h","last_error":null}`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "failure-resilience")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"check_status": StaticToolHandler(statusResult),
			},
		}),
	)

	// Connect WS and subscribe to sessions channel.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-resilience", "API server alert triggered")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Subscribe to session-specific channel.
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session to reach terminal status (completed — fail-open).
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Allow trailing WS events to arrive.
	time.Sleep(200 * time.Millisecond)

	// ── Session assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"], "session should be completed despite exec summary failure")

	// Executive summary fail-open: empty summary, populated error.
	assert.Empty(t, session["executive_summary"], "executive_summary should be empty (LLM failed)")
	assert.NotEmpty(t, session["executive_summary_error"], "executive_summary_error should be populated")

	// ── Stage assertions ──
	// 3 stages: analysis, analysis - Synthesis, summary.
	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 3, "analysis + analysis - Synthesis + summary")

	assert.Equal(t, "analysis", stages[0].StageName)
	assert.Equal(t, "completed", string(stages[0].Status))

	assert.Equal(t, "analysis - Synthesis", stages[1].StageName)
	assert.Equal(t, "completed", string(stages[1].Status))

	assert.Equal(t, "summary", stages[2].StageName)
	assert.Equal(t, "completed", string(stages[2].Status))

	// ── Execution assertions ──
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 4, "Analyzer + Investigator + SynthesisAgent + Summarizer")

	execByName := make(map[string]string)
	for _, e := range execs {
		execByName[e.AgentName] = string(e.Status)
	}
	assert.Equal(t, "failed", execByName["Analyzer"], "Analyzer should have failed")
	assert.Equal(t, "completed", execByName["Investigator"], "Investigator should have completed")
	assert.Equal(t, "completed", execByName["SynthesisAgent"], "SynthesisAgent should have completed")
	assert.Equal(t, "completed", execByName["Summarizer"], "Summarizer should have completed")

	// Verify the failed execution has an error message.
	for _, e := range execs {
		if e.AgentName == "Analyzer" {
			require.NotNil(t, e.ErrorMessage, "Analyzer execution should have error_message")
			assert.NotEmpty(t, *e.ErrorMessage)
			break
		}
	}

	// ── LLM call count ──
	// Analyzer (1 error) + Investigator (2) + Synthesis (1) + Summarizer (1) + Exec summary (1 error) = 6
	assert.Equal(t, 6, llm.CallCount())

	// ── Timeline API assertions ──
	apiTimeline := app.GetTimeline(t, sessionID)
	require.NotEmpty(t, apiTimeline, "should have timeline events")

	// Verify all timeline events have terminal statuses (no stuck "streaming").
	for i, raw := range apiTimeline {
		event, ok := raw.(map[string]interface{})
		require.True(t, ok, "timeline event %d should be a JSON object", i)
		status, _ := event["status"].(string)
		assert.NotEqual(t, "streaming", status,
			"timeline event %d (%s) should not be stuck as streaming", i, event["event_type"])
	}

	// Verify at least one "error" timeline event exists (from Analyzer's failed Generate).
	var hasErrorEvent bool
	for _, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		if event["event_type"] == "error" {
			hasErrorEvent = true
			break
		}
	}
	assert.True(t, hasErrorEvent, "should have at least one 'error' timeline event from Analyzer")

	// Verify final_analysis events exist (from Investigator, Synthesis, Summarizer).
	var finalAnalysisCount int
	for _, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		if event["event_type"] == "final_analysis" {
			finalAnalysisCount++
		}
	}
	assert.Equal(t, 3, finalAnalysisCount,
		"should have 3 final_analysis events (Investigator + Synthesis + Summarizer)")

	// Executive summary timeline event is only created on success — should be absent.
	for i, raw := range apiTimeline {
		event, _ := raw.(map[string]interface{})
		assert.NotEqual(t, "executive_summary", event["event_type"],
			"timeline event %d: executive_summary should not exist (LLM failed)", i)
	}

	// ── WS event structural assertions ──
	AssertEventsInOrder(t, ws.Events(), testdata.FailureResilienceExpectedEvents)
}
