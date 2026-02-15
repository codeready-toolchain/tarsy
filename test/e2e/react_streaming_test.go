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
// TestE2E_ReactStreamingChunkBoundaries — exercises the ReAct-aware
// streaming state machine with tiny, adversarial chunk splits that
// place ReAct markers ("Thought:", "Action:", "Final Answer:") mid-chunk
// and across chunk boundaries.
//
// This complements the pipeline e2e test (which uses clean 2-chunk splits
// per iteration) by stressing:
//   - Marker detection when "Thought:" or "Final Answer:" is split
//     across two chunks (e.g. "Thou" + "ght: content")
//   - Incremental delta accumulation within a single phase
//   - Correct event creation despite fragmented delivery
//
// Scenario (single ReAct agent, 2 iterations):
//
//	Iteration 1: Thought (fragmented) → Action
//	Iteration 2: Thought (fragmented) → Final Answer (fragmented)
//	+ Executive summary
//
// ────────────────────────────────────────────────────────────
func TestE2E_ReactStreamingChunkBoundaries(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Iteration 1: Thought + Action ──
	// "Thought:" marker arrives across two chunks: "Thou" + "ght: ..."
	// The thought content itself is spread across multiple chunks.
	// Then "\nAction:" arrives cleanly to trigger the phase transition.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thou"},                              // partial marker
			&agent.TextChunk{Content: "ght: I need"},                       // marker completes, content starts
			&agent.TextChunk{Content: " to check"},                         // mid-thought delta
			&agent.TextChunk{Content: " the pods."},                        // thought continues
			&agent.TextChunk{Content: "\nAction: test-mcp.get_pods"},       // phase transition
			&agent.TextChunk{Content: "\nAction Input: {\"ns\":\"default\"}"}, // action input
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 30, TotalTokens: 80},
		},
	})

	// ── Iteration 2: Thought + Final Answer ──
	// Both markers are split across chunk boundaries.
	// "Final Answer:" is split as "Final An" + "swer: content..."
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Though"},                   // partial "Thought:"
			&agent.TextChunk{Content: "t: The pod"},               // marker completes
			&agent.TextChunk{Content: " is OOMKilled."},           // thought content
			&agent.TextChunk{Content: "\nFinal An"},               // partial "Final Answer:"
			&agent.TextChunk{Content: "swer: Increase"},           // marker completes, answer starts
			&agent.TextChunk{Content: " memory"},                  // mid-answer delta
			&agent.TextChunk{Content: " to 1Gi."},                 // answer continues
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 50, TotalTokens: 130},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Pod OOMKilled. Increase memory to 1Gi.",
	})

	// ── Tool result ──
	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "react-streaming")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "react-test", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 5*time.Second, "expected session.status completed")

	// ── DB assertions ──
	timeline := app.QueryTimeline(t, sessionID)
	require.NotEmpty(t, timeline)

	counts := map[timelineevent.EventType]int{}
	for _, ev := range timeline {
		counts[ev.EventType]++
	}

	assert.Equal(t, 0, counts[timelineevent.EventTypeLlmResponse],
		"ReAct streaming must produce 0 llm_response events")
	assert.Equal(t, 2, counts[timelineevent.EventTypeLlmThinking],
		"expected 2 llm_thinking events (one per iteration)")
	assert.Equal(t, 1, counts[timelineevent.EventTypeFinalAnalysis],
		"expected 1 final_analysis event")
	assert.Equal(t, 1, counts[timelineevent.EventTypeLlmToolCall],
		"expected 1 llm_tool_call event")

	// Verify content was correctly assembled from fragmented chunks.
	thinking := filterEvents(timeline, timelineevent.EventTypeLlmThinking)
	require.Len(t, thinking, 2)
	assert.Equal(t, "I need to check the pods.", thinking[0].Content,
		"thought 1: fragments should assemble correctly")
	assert.Equal(t, "The pod is OOMKilled.", thinking[1].Content,
		"thought 2: fragments should assemble correctly")

	for _, ev := range thinking {
		assert.Equal(t, timelineevent.StatusCompleted, ev.Status)
		assert.Equal(t, "react", ev.Metadata["source"])
	}

	final := filterEvents(timeline, timelineevent.EventTypeFinalAnalysis)
	require.Len(t, final, 1)
	assert.Equal(t, "Increase memory to 1Gi.", final[0].Content,
		"final answer: fragments should assemble correctly")

	// ── WS assertions ──
	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)

	createdTypes := map[string]int{}
	completedTypes := map[string]int{}
	for _, e := range wsEvents {
		switch e.Type {
		case "timeline_event.created":
			if et, ok := e.Parsed["event_type"].(string); ok {
				createdTypes[et]++
			}
		case "timeline_event.completed":
			if et, ok := e.Parsed["event_type"].(string); ok {
				completedTypes[et]++
			}
		}
	}

	assert.GreaterOrEqual(t, createdTypes["llm_thinking"], 2,
		"WS: at least 2 llm_thinking created events")
	assert.GreaterOrEqual(t, completedTypes["llm_thinking"], 2,
		"WS: at least 2 llm_thinking completed events")
	assert.GreaterOrEqual(t, createdTypes["final_analysis"], 1,
		"WS: at least 1 final_analysis created event")
	assert.Equal(t, 0, createdTypes["llm_response"],
		"WS: 0 llm_response events")
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
