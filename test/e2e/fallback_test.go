package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// TestE2E_FallbackOnMaxRetries — Primary provider fails with max_retries,
// system falls back to fallback-1, agent completes investigation normally.
//
// Verifies:
//   - Session completes successfully after fallback
//   - Execution record: original_llm_provider/backend preserved
//   - provider_fallback timeline event with correct metadata
//   - ClearCache flag set on the post-fallback LLM call
//   - Executive summary generated successfully
// ────────────────────────────────────────────────────────────

func TestE2E_FallbackOnMaxRetries(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Iteration 1: primary provider fails with max_retries → immediate fallback
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{
				Message:   "rate limit exceeded after 3 retries",
				Code:      "max_retries",
				Retryable: true,
			},
		},
	})
	// Iteration 1 (retried with fallback-1): tool call
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the system status."},
			&agent.TextChunk{Content: "Checking system health after fallback."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__check_status", Arguments: `{"component":"api"}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
		},
	})
	// Iteration 2: final answer
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete: API server is healthy. The initial provider failure was transient."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 30, TotalTokens: 110},
		},
	})
	// Executive summary
	llm.AddSequential(LLMScriptEntry{Text: "Alert investigated successfully. API server confirmed healthy after provider fallback."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "fallback")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"check_status": StaticToolHandler(`{"status":"healthy","uptime":"72h"}`),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-fallback", "API server alert triggered")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["executive_summary"], "executive summary should be populated")

	// ── Execution record: original provider preserved ──
	execs := app.QueryExecutions(t, sessionID)
	investigator := findExecution(execs, "Investigator")
	require.NotNil(t, investigator, "Investigator execution should exist")
	assert.Equal(t, "completed", string(investigator.Status))
	require.NotNil(t, investigator.OriginalLlmProvider, "original_llm_provider should be set after fallback")
	assert.Equal(t, "primary-provider", *investigator.OriginalLlmProvider)
	require.NotNil(t, investigator.OriginalLlmBackend)
	assert.Equal(t, "google-native", *investigator.OriginalLlmBackend)
	require.NotNil(t, investigator.LlmProvider)
	assert.Equal(t, "fallback-1", *investigator.LlmProvider)

	// ── Timeline: provider_fallback event with metadata ──
	timeline := app.QueryTimeline(t, sessionID)
	fallbackEvents := filterTimelineByType(timeline, timelineevent.EventTypeProviderFallback)
	require.Len(t, fallbackEvents, 1, "should have exactly one provider_fallback event")
	assert.Equal(t, "primary-provider", fallbackEvents[0].Metadata["original_provider"])
	assert.Equal(t, "fallback-1", fallbackEvents[0].Metadata["fallback_provider"])
	assert.Equal(t, "google-native", fallbackEvents[0].Metadata["original_backend"])
	assert.Equal(t, "google-native", fallbackEvents[0].Metadata["fallback_backend"])

	// ── ClearCache: set on the first call after fallback ──
	inputs := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(inputs), 2)
	assert.False(t, inputs[0].ClearCache, "first call (primary) should not have ClearCache")
	assert.True(t, inputs[1].ClearCache, "call after fallback should have ClearCache=true")
	if len(inputs) > 2 {
		assert.False(t, inputs[2].ClearCache, "subsequent calls should not have ClearCache")
	}

	// ── LLM call count: 1 error + 1 tool call + 1 final + 1 exec summary = 4 ──
	assert.Equal(t, 4, llm.CallCount())

	// ── WS: provider_fallback event delivered ──
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 5*time.Second, "waiting for session.status=completed")

	var hasFallbackWSEvent bool
	for _, e := range ws.Events() {
		if e.Type == "timeline_event.created" {
			if et, _ := e.Parsed["event_type"].(string); et == "provider_fallback" {
				hasFallbackWSEvent = true
				break
			}
		}
	}
	assert.True(t, hasFallbackWSEvent, "WS should include provider_fallback timeline event")
}

// ────────────────────────────────────────────────────────────
// TestE2E_FallbackCascade — Primary provider and first fallback both fail
// with credentials errors; second fallback succeeds.
//
// Verifies:
//   - Two provider_fallback timeline events
//   - Execution record tracks the original (primary) provider
//   - Session completes via the second fallback provider
// ────────────────────────────────────────────────────────────

func TestE2E_FallbackCascade(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Primary: credentials error → immediate fallback to fallback-1
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "invalid API key", Code: "credentials"},
		},
	})
	// fallback-1: credentials error → immediate fallback to fallback-2
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "invalid API key for fallback", Code: "credentials"},
		},
	})
	// fallback-2: succeeds with final answer (no tool calls)
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Third provider working, analyzing alert."},
			&agent.TextChunk{Content: "Investigation complete: alert was a false positive triggered by a monitoring misconfiguration."},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 25, TotalTokens: 85},
		},
	})
	// Executive summary
	llm.AddSequential(LLMScriptEntry{Text: "False positive alert caused by monitoring misconfiguration."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "fallback")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"check_status": StaticToolHandler(`{"status":"healthy"}`),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-cascade", "Monitoring alert fired")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session completes ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── Two provider_fallback timeline events ──
	timeline := app.QueryTimeline(t, sessionID)
	fallbackEvents := filterTimelineByType(timeline, timelineevent.EventTypeProviderFallback)
	require.Len(t, fallbackEvents, 2, "should have two provider_fallback events (primary→fb-1, fb-1→fb-2)")

	assert.Equal(t, "primary-provider", fallbackEvents[0].Metadata["original_provider"])
	assert.Equal(t, "fallback-1", fallbackEvents[0].Metadata["fallback_provider"])
	assert.Equal(t, "fallback-1", fallbackEvents[1].Metadata["original_provider"])
	assert.Equal(t, "fallback-2", fallbackEvents[1].Metadata["fallback_provider"])

	// ── Execution record: original provider preserved, current is fallback-2 ──
	execs := app.QueryExecutions(t, sessionID)
	investigator := findExecution(execs, "Investigator")
	require.NotNil(t, investigator)
	require.NotNil(t, investigator.OriginalLlmProvider)
	assert.Equal(t, "primary-provider", *investigator.OriginalLlmProvider, "original provider should be the primary")
	require.NotNil(t, investigator.LlmProvider)
	assert.Equal(t, "fallback-2", *investigator.LlmProvider, "current provider should be the last fallback")

	// ── LLM call count: 2 errors + 1 success + 1 exec summary = 4 ──
	assert.Equal(t, 4, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_FallbackAllExhausted — All providers (primary + 2 fallbacks) fail
// with max_retries. Session fails gracefully after exhausting every option.
//
// Uses LimitedAgent (max_iterations=3) so fallback providers are attempted
// across iterations: primary (iter 0), fallback-1 (iter 1), fallback-2 (iter 2),
// then force conclusion with fallback-2 also fails.
//
// Verifies:
//   - Session/stage/execution all marked as failed
//   - Two provider_fallback events recorded
//   - Error timeline events present
// ────────────────────────────────────────────────────────────

func TestE2E_FallbackAllExhausted(t *testing.T) {
	llm := NewScriptedLLMClient()

	// iter 0: primary fails → fallback to fb-1
	llm.AddRouted("LimitedAgent", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "service unavailable", Code: "max_retries", Retryable: true},
		},
	})
	// iter 1: fb-1 fails → fallback to fb-2
	llm.AddRouted("LimitedAgent", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "service unavailable", Code: "max_retries", Retryable: true},
		},
	})
	// iter 2: fb-2 fails → all exhausted, treated as recoverable partial
	llm.AddRouted("LimitedAgent", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "service unavailable", Code: "max_retries", Retryable: true},
		},
	})
	// force conclusion: fb-2 fails again → exhausted → execution fails
	llm.AddRouted("LimitedAgent", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "service unavailable", Code: "max_retries", Retryable: true},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "fallback")),
		WithLLMClient(llm),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-exhausted", "Critical outage — all providers down")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "failed")

	// ── Session failed ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "failed", session["status"])

	// ── Execution failed with error ──
	execs := app.QueryExecutions(t, sessionID)
	limited := findExecution(execs, "LimitedAgent")
	require.NotNil(t, limited)
	assert.Equal(t, "failed", string(limited.Status))
	require.NotNil(t, limited.ErrorMessage, "failed execution should have error_message")

	// ── Two provider_fallback events (primary→fb-1, fb-1→fb-2) ──
	timeline := app.QueryTimeline(t, sessionID)
	fallbackEvents := filterTimelineByType(timeline, timelineevent.EventTypeProviderFallback)
	assert.Len(t, fallbackEvents, 2, "should have two fallback events before exhaustion")

	// ── Error events present ──
	errorEvents := filterTimelineByType(timeline, timelineevent.EventTypeError)
	assert.NotEmpty(t, errorEvents, "should have error timeline events")

	// ── LLM calls: 3 main loop + 1 force conclusion = 4 ──
	assert.Equal(t, 4, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_FallbackParallelAgents — Parallel stage with two agents:
// AlphaChecker's primary provider fails → falls back to fallback-1.
// BetaChecker succeeds normally on the primary provider.
// Stage completes with synthesis.
//
// Verifies:
//   - Stage completes despite one agent needing fallback
//   - AlphaChecker execution has original_llm_provider set
//   - BetaChecker execution does NOT have original_llm_provider set
//   - Exactly one provider_fallback timeline event (from AlphaChecker)
// ────────────────────────────────────────────────────────────

func TestE2E_FallbackParallelAgents(t *testing.T) {
	llm := NewScriptedLLMClient()

	// AlphaChecker: primary fails, then succeeds on fallback-1
	llm.AddRouted("AlphaChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ErrorChunk{Message: "quota exceeded", Code: "max_retries", Retryable: true},
		},
	})
	llm.AddRouted("AlphaChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "System health check passed: all services are responding normally."},
			&agent.UsageChunk{InputTokens: 40, OutputTokens: 15, TotalTokens: 55},
		},
	})

	// BetaChecker: succeeds normally with tool call + final answer
	llm.AddRouted("BetaChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Checking logs for anomalies."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__check_logs", Arguments: `{"service":"api"}`},
			&agent.UsageChunk{InputTokens: 45, OutputTokens: 18, TotalTokens: 63},
		},
	})
	llm.AddRouted("BetaChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Log analysis complete: no anomalies detected in the last 24 hours."},
			&agent.UsageChunk{InputTokens: 70, OutputTokens: 25, TotalTokens: 95},
		},
	})

	// Synthesis (sequential dispatch)
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Combined analysis: both health check and log analysis confirm system stability."},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 20, TotalTokens: 80},
		},
	})
	// Executive summary (sequential dispatch, generated for all chains)
	llm.AddSequential(LLMScriptEntry{Text: "Parallel investigation confirms system stability."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "fallback")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"check_logs": StaticToolHandler(`{"anomalies":0,"last_error":null}`),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-parallel-fallback", "Parallel investigation triggered")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Stage completes ──
	stages := app.QueryStages(t, sessionID)
	require.NotEmpty(t, stages)
	assert.Equal(t, "completed", string(stages[0].Status))

	// ── Execution assertions: selective fallback ──
	execs := app.QueryExecutions(t, sessionID)

	alpha := findExecution(execs, "AlphaChecker")
	require.NotNil(t, alpha, "AlphaChecker execution should exist")
	assert.Equal(t, "completed", string(alpha.Status))
	require.NotNil(t, alpha.OriginalLlmProvider, "AlphaChecker should have original_llm_provider (fallback occurred)")
	assert.Equal(t, "primary-provider", *alpha.OriginalLlmProvider)

	beta := findExecution(execs, "BetaChecker")
	require.NotNil(t, beta, "BetaChecker execution should exist")
	assert.Equal(t, "completed", string(beta.Status))
	assert.Nil(t, beta.OriginalLlmProvider, "BetaChecker should NOT have original_llm_provider (no fallback)")

	// ── Exactly one provider_fallback event ──
	timeline := app.QueryTimeline(t, sessionID)
	fallbackEvents := filterTimelineByType(timeline, timelineevent.EventTypeProviderFallback)
	assert.Len(t, fallbackEvents, 1, "only AlphaChecker should trigger fallback")

	// ── LLM calls: Alpha (1 err + 1 ok) + Beta (2 ok) + Synthesis (1) + Exec summary (1) = 6 ──
	assert.Equal(t, 6, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_FallbackExecutiveSummary — Agent chain completes normally, but
// the executive summary's primary provider fails. The system falls back
// to a secondary provider and produces the executive summary.
//
// Verifies:
//   - Session completes with executive_summary populated
//   - Agent execution has NO original_llm_provider (no agent-level fallback)
//   - Executive summary was generated despite primary failure
// ────────────────────────────────────────────────────────────

func TestE2E_FallbackExecutiveSummary(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Investigator: normal tool call + final answer
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Checking system metrics."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_metrics", Arguments: `{"service":"api"}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
		},
	})
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Metrics show normal operation. CPU at 45%, memory at 62%. No anomalies detected."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 30, TotalTokens: 110},
		},
	})

	// Executive summary: primary fails, fallback succeeds
	llm.AddSequential(LLMScriptEntry{
		Error: fmt.Errorf("executive summary provider overloaded"),
	})
	llm.AddSequential(LLMScriptEntry{
		Text: "System operating normally. CPU 45%, memory 62%. No action required.",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "fallback")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_metrics": StaticToolHandler(`{"cpu":45,"memory":62,"errors":0}`),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-exec-fallback", "Routine health check")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Session completes with executive summary ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["executive_summary"], "executive summary should be populated via fallback")
	assert.Empty(t, session["executive_summary_error"], "no error since fallback succeeded")

	// ── Agent execution: no fallback at the agent level ──
	execs := app.QueryExecutions(t, sessionID)
	investigator := findExecution(execs, "Investigator")
	require.NotNil(t, investigator)
	assert.Equal(t, "completed", string(investigator.Status))
	assert.Nil(t, investigator.OriginalLlmProvider, "Investigator should not have fallback (succeeded on primary)")

	// ── LLM calls: Investigator (2) + exec summary (1 error + 1 success) = 4 ──
	assert.Equal(t, 4, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────

// findExecution returns the first execution matching agentName, or nil.
func findExecution(execs []*ent.AgentExecution, agentName string) *ent.AgentExecution {
	for _, e := range execs {
		if e.AgentName == agentName {
			return e
		}
	}
	return nil
}

// filterTimelineByType returns timeline events matching the given event type.
func filterTimelineByType(events []*ent.TimelineEvent, eventType timelineevent.EventType) []*ent.TimelineEvent {
	var result []*ent.TimelineEvent
	for _, e := range events {
		if e.EventType == eventType {
			result = append(result, e)
		}
	}
	return result
}
