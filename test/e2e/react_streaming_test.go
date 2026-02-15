package e2e

import (
	"context"
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
// ReAct streaming test — verifies that the ReAct-aware streaming
// produces correct timeline events with no duplicates, no llm_response
// events, and proper WS stream.chunk delivery.
//
// Single-stage ReAct scenario:
//   1. investigation (Investigator, ReAct)
//      Iteration 1: Thought + Action (tool call)
//      Iteration 2: Thought + Final Answer
//   + Executive summary
// ────────────────────────────────────────────────────────────

func TestE2E_ReactStreaming(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Investigation: ReAct agent with two iterations ──

	// Iteration 1: Thought → Action (split into chunks for realistic streaming).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: I need to check the pods in the default namespace."},
			&agent.TextChunk{Content: "\nAction: test-mcp.get_pods"},
			&agent.TextChunk{Content: "\nAction Input: {\"namespace\":\"default\"}"},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 30, TotalTokens: 80},
		},
	})

	// Iteration 2: Thought → Final Answer (split into chunks).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: The pod-1 is OOMKilled with 5 restarts."},
			&agent.TextChunk{Content: "\nFinal Answer: Pod-1 is being killed due to OOM. "},
			&agent.TextChunk{Content: "The memory limit of 512Mi is insufficient."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 50, TotalTokens: 130},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Pod-1 OOM killed. Increase memory limit to 1Gi.",
	})

	// ── Tool results ──
	podsResult := `[{"name":"pod-1","namespace":"default","status":"OOMKilled","restarts":5}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "react-streaming")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	// Connect WS.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "react-test", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Subscribe to session events.
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for completion.
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait briefly for trailing WS events.
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 5*time.Second, "expected session.status completed WS event")

	// ── Verify session ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])

	// ── Verify LLM call count ──
	// Iteration 1 + Iteration 2 + Executive Summary = 3
	assert.Equal(t, 3, llm.CallCount(), "expected 3 LLM calls")

	// ── Verify timeline events in DB ──
	timeline := app.QueryTimeline(t, sessionID)
	require.NotEmpty(t, timeline)

	eventCounts := map[timelineevent.EventType]int{}
	for _, ev := range timeline {
		eventCounts[ev.EventType]++
	}

	// CRITICAL: No llm_response events should exist for ReAct agents.
	assert.Equal(t, 0, eventCounts[timelineevent.EventTypeLlmResponse],
		"ReAct streaming should produce 0 llm_response events, got %d", eventCounts[timelineevent.EventTypeLlmResponse])

	// 2 llm_thinking events (one per iteration, source=react).
	assert.Equal(t, 2, eventCounts[timelineevent.EventTypeLlmThinking],
		"expected 2 llm_thinking events, got %d", eventCounts[timelineevent.EventTypeLlmThinking])

	// 1 final_analysis event (from iteration 2).
	assert.Equal(t, 1, eventCounts[timelineevent.EventTypeFinalAnalysis],
		"expected 1 final_analysis event, got %d", eventCounts[timelineevent.EventTypeFinalAnalysis])

	// 1 executive_summary event.
	assert.Equal(t, 1, eventCounts[timelineevent.EventTypeExecutiveSummary],
		"expected 1 executive_summary event, got %d", eventCounts[timelineevent.EventTypeExecutiveSummary])

	// 1 llm_tool_call event (from iteration 1).
	assert.Equal(t, 1, eventCounts[timelineevent.EventTypeLlmToolCall],
		"expected 1 llm_tool_call event, got %d", eventCounts[timelineevent.EventTypeLlmToolCall])

	// Verify llm_thinking content and metadata.
	thinkingEvents := filterEvents(timeline, timelineevent.EventTypeLlmThinking)
	for _, ev := range thinkingEvents {
		assert.Equal(t, timelineevent.StatusCompleted, ev.Status,
			"llm_thinking event should be completed")
		assert.Equal(t, "react", ev.Metadata["source"],
			"llm_thinking events should have source=react")
	}
	assert.Equal(t, "I need to check the pods in the default namespace.", thinkingEvents[0].Content)
	assert.Equal(t, "The pod-1 is OOMKilled with 5 restarts.", thinkingEvents[1].Content)

	// Verify final_analysis content.
	finalEvents := filterEvents(timeline, timelineevent.EventTypeFinalAnalysis)
	require.Len(t, finalEvents, 1)
	assert.Equal(t, "Pod-1 is being killed due to OOM. The memory limit of 512Mi is insufficient.",
		finalEvents[0].Content)

	// ── Verify WS events ──
	wsEvents := ws.Events()

	// Should have timeline_event.created events for llm_thinking and final_analysis.
	createdTypes := map[string]int{}
	for _, e := range wsEvents {
		if e.Type == "timeline_event.created" {
			if et, ok := e.Parsed["event_type"].(string); ok {
				createdTypes[et]++
			}
		}
	}
	assert.GreaterOrEqual(t, createdTypes["llm_thinking"], 2,
		"expected at least 2 timeline_event.created for llm_thinking, got %d", createdTypes["llm_thinking"])
	assert.GreaterOrEqual(t, createdTypes["final_analysis"], 1,
		"expected at least 1 timeline_event.created for final_analysis, got %d", createdTypes["final_analysis"])

	// No llm_response WS events should exist for ReAct agents.
	assert.Equal(t, 0, createdTypes["llm_response"],
		"expected 0 timeline_event.created for llm_response, got %d", createdTypes["llm_response"])
}

// filterEvents returns timeline events matching the given type.
func filterEvents(events []*ent.TimelineEvent, eventType timelineevent.EventType) []*ent.TimelineEvent {
	var result []*ent.TimelineEvent
	for _, ev := range events {
		if ev.EventType == eventType {
			result = append(result, ev)
		}
	}
	return result
}
