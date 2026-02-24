package context

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────
// formatTimelineEvents — each event type produces a known block
// ────────────────────────────────────────────────────────────

func TestFormatTimelineEvents(t *testing.T) {
	tests := []struct {
		name     string
		events   []*ent.TimelineEvent
		expected string
	}{
		{
			name:     "nil slice",
			events:   nil,
			expected: "",
		},
		{
			name:     "nil event in slice",
			events:   []*ent.TimelineEvent{nil},
			expected: "",
		},
		{
			name: "thinking",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeLlmThinking, Content: "Analyzing pod metrics."},
			},
			expected: "**Internal Reasoning:**\n\nAnalyzing pod metrics.\n\n",
		},
		{
			name: "response",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeLlmResponse, Content: "The pods are healthy."},
			},
			expected: "**Agent Response:**\n\nThe pods are healthy.\n\n",
		},
		{
			name: "final analysis",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Root cause: OOM."},
			},
			expected: "**Final Analysis:**\n\nRoot cause: OOM.\n\n",
		},
		{
			name: "standalone summary (not preceded by tool call)",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeMcpToolSummary, Content: "3 pods running"},
			},
			expected: "**Tool Result Summary:**\n\n3 pods running\n\n",
		},
		{
			name: "unknown event type uses default formatting",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeError, Content: "Something went wrong."},
			},
			expected: "**error:**\n\nSomething went wrong.\n\n",
		},
		{
			name: "tool call without metadata (fallback)",
			events: []*ent.TimelineEvent{
				{EventType: timelineevent.EventTypeLlmToolCall, Content: "k8s.pods_list(ns=default)"},
			},
			expected: "**Tool Call:** k8s.pods_list(ns=default)\n" +
				"**Result:**\n\nk8s.pods_list(ns=default)\n\n",
		},
		{
			name: "tool call with metadata",
			events: []*ent.TimelineEvent{
				{
					EventType: timelineevent.EventTypeLlmToolCall,
					Metadata: map[string]interface{}{
						"server_name": "k8s",
						"tool_name":   "pods_list",
						"arguments":   `{"namespace":"default"}`,
					},
				},
			},
			// No content → header only, no result block
			expected: "**Tool Call:** k8s.pods_list({\"namespace\":\"default\"})\n",
		},
		{
			name: "tool call with metadata and content",
			events: []*ent.TimelineEvent{
				{
					EventType: timelineevent.EventTypeLlmToolCall,
					Content:   "pod-1 Running, pod-2 Running",
					Metadata: map[string]interface{}{
						"server_name": "k8s",
						"tool_name":   "pods_list",
						"arguments":   "ns=default",
					},
				},
			},
			expected: "**Tool Call:** k8s.pods_list(ns=default)\n" +
				"**Result:**\n\npod-1 Running, pod-2 Running\n\n",
		},
		{
			name: "tool call + summary deduplication",
			events: []*ent.TimelineEvent{
				{
					EventType: timelineevent.EventTypeLlmToolCall,
					Content:   "raw output (very long)",
					Metadata: map[string]interface{}{
						"server_name": "k8s",
						"tool_name":   "pods_list",
						"arguments":   "ns=prod",
					},
				},
				{EventType: timelineevent.EventTypeMcpToolSummary, Content: "3 pods running in prod"},
			},
			// Summary replaces raw result; raw content is NOT emitted
			expected: "**Tool Call:** k8s.pods_list(ns=prod)\n" +
				"**Result (summarized):**\n\n3 pods running in prod\n\n",
		},
		{
			name: "tool call followed by non-summary event (no dedup)",
			events: []*ent.TimelineEvent{
				{
					EventType: timelineevent.EventTypeLlmToolCall,
					Content:   "pod-1 Running",
					Metadata: map[string]interface{}{
						"server_name": "k8s",
						"tool_name":   "pods_list",
						"arguments":   "",
					},
				},
				{EventType: timelineevent.EventTypeLlmResponse, Content: "Pods look fine."},
			},
			expected: "**Tool Call:** k8s.pods_list()\n" +
				"**Result:**\n\npod-1 Running\n\n" +
				"**Agent Response:**\n\nPods look fine.\n\n",
		},
		{
			name: "partial metadata (only server_name) falls back to content",
			events: []*ent.TimelineEvent{
				{
					EventType: timelineevent.EventTypeLlmToolCall,
					Content:   "k8s.pods_list()",
					Metadata: map[string]interface{}{
						"server_name": "k8s",
					},
				},
			},
			expected: "**Tool Call:** k8s.pods_list()\n" +
				"**Result:**\n\nk8s.pods_list()\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			formatTimelineEvents(&sb, tc.events)
			assert.Equal(t, tc.expected, sb.String())
		})
	}
}

// ────────────────────────────────────────────────────────────
// FormatStructuredInvestigation (chat context)
// ────────────────────────────────────────────────────────────

func TestFormatStructuredInvestigation(t *testing.T) {
	t.Run("empty stages and no summary", func(t *testing.T) {
		result := FormatStructuredInvestigation(nil, "")

		assert.Contains(t, result, "📋 INVESTIGATION HISTORY")
		// No stage or summary sections
		assert.NotContains(t, result, "## Stage")
		assert.NotContains(t, result, "## Executive Summary")
	})

	t.Run("single-agent stage uses simplified header", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "data-collection",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:  "DataCollector",
						AgentIndex: 1,
						LLMBackend: "google-native",
						Status:     alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Collected data."},
						},
					},
				},
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		assert.Contains(t, result, "## Stage 1: data-collection")
		assert.Contains(t, result, "**Agent:** DataCollector (google-native)")
		assert.Contains(t, result, "**Status**: completed")
		assert.Contains(t, result, "**Final Analysis:**\n\nCollected data.")
		// Single-agent should NOT use the parallel format
		assert.NotContains(t, result, "<!-- PARALLEL_RESULTS_START -->")
		assert.NotContains(t, result, "#### Agent")
		assert.NotContains(t, result, "### Parallel Investigation")
	})

	t.Run("parallel-agent stage uses synthesis format", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "validation",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:   "ConfigValidator",
						AgentIndex:  1,
						LLMBackend:  "langchain",
						LLMProvider: "gemini-2.5-pro",
						Status:      alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Config OK."},
						},
					},
					{
						AgentName:   "MetricsValidator",
						AgentIndex:  2,
						LLMBackend:  "langchain",
						LLMProvider: "claude-sonnet",
						Status:      alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Metrics OK."},
						},
					},
				},
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		// Uses the same format as synthesis
		assert.Contains(t, result, "<!-- PARALLEL_RESULTS_START -->")
		assert.Contains(t, result, "<!-- PARALLEL_RESULTS_END -->")
		assert.Contains(t, result, `"validation" — 2/2 agents succeeded`)
		assert.Contains(t, result, "#### Agent 1: ConfigValidator (langchain, gemini-2.5-pro)")
		assert.Contains(t, result, "#### Agent 2: MetricsValidator (langchain, claude-sonnet)")
		assert.Contains(t, result, "**Final Analysis:**\n\nConfig OK.")
		assert.Contains(t, result, "**Final Analysis:**\n\nMetrics OK.")
	})

	t.Run("single-agent stage with LLMProvider includes provider", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "investigation",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:   "DataCollector",
						AgentIndex:  1,
						LLMBackend:  "google-native",
						LLMProvider: "gemini-2.5-pro",
						Status:      alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Collected data."},
						},
					},
				},
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		assert.Contains(t, result, "**Agent:** DataCollector (google-native, gemini-2.5-pro)")
		assert.NotContains(t, result, "<!-- PARALLEL_RESULTS_START -->")
	})

	t.Run("stage with synthesis result", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "validation",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:   "Agent",
						AgentIndex:  1,
						LLMBackend:  "langchain",
						LLMProvider: "gemini",
						Status:      alertsession.StatusCompleted,
					},
				},
				SynthesisResult: "Both agents agree: no issues found.",
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		assert.Contains(t, result, "**Agent:** Agent (langchain, gemini)")
		assert.Contains(t, result, "### Synthesis Result")
		assert.Contains(t, result, "Both agents agree: no issues found.")
	})

	t.Run("executive summary", func(t *testing.T) {
		result := FormatStructuredInvestigation(nil, "Overall: system is healthy.")

		assert.Contains(t, result, "## Executive Summary")
		assert.Contains(t, result, "Overall: system is healthy.")
	})

	t.Run("multi-stage mixed scenario", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "data-collection",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:  "DataCollector",
						AgentIndex: 1,
						LLMBackend: "google-native",
						Status:     alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Data collected."},
						},
					},
				},
			},
			{
				StageName:  "validation",
				StageIndex: 1,
				Agents: []AgentInvestigation{
					{
						AgentName:   "AgentA",
						AgentIndex:  1,
						LLMBackend:  "langchain",
						LLMProvider: "gemini",
						Status:      alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Valid."},
						},
					},
					{
						AgentName:    "AgentB",
						AgentIndex:   2,
						LLMBackend:   "langchain",
						LLMProvider:  "gemini",
						Status:       alertsession.StatusFailed,
						ErrorMessage: "timeout",
					},
				},
				SynthesisResult: "Partial success.",
			},
		}

		result := FormatStructuredInvestigation(stages, "Everything analyzed.")

		// Stage 1: single-agent
		assert.Contains(t, result, "## Stage 1: data-collection")
		assert.Contains(t, result, "**Agent:** DataCollector (google-native)")

		// Stage 2: parallel with synthesis
		assert.Contains(t, result, "## Stage 2: validation")
		assert.Contains(t, result, `"validation" — 1/2 agents succeeded`)
		assert.Contains(t, result, "#### Agent 2: AgentB (langchain, gemini)")
		assert.Contains(t, result, "**Error**: timeout")
		assert.Contains(t, result, "(No investigation history available)")
		assert.Contains(t, result, "### Synthesis Result")
		assert.Contains(t, result, "Partial success.")

		// Verify newline between PARALLEL_RESULTS_END and Synthesis heading
		assert.Contains(t, result, "<!-- PARALLEL_RESULTS_END -->\n### Synthesis Result")

		// Executive summary
		assert.Contains(t, result, "## Executive Summary")
		assert.Contains(t, result, "Everything analyzed.")
	})

	t.Run("sequential stage numbering ignores StageIndex", func(t *testing.T) {
		stages := []StageInvestigation{
			{StageName: "first", StageIndex: 0, Agents: []AgentInvestigation{
				{AgentName: "A", AgentIndex: 1, LLMBackend: "langchain", Status: alertsession.StatusCompleted},
			}},
			{StageName: "third", StageIndex: 5, Agents: []AgentInvestigation{
				{AgentName: "B", AgentIndex: 1, LLMBackend: "langchain", Status: alertsession.StatusCompleted},
			}},
		}

		result := FormatStructuredInvestigation(stages, "")

		// Sequential numbering: 1, 2 — not 1, 6
		assert.Contains(t, result, "## Stage 1: first")
		assert.Contains(t, result, "## Stage 2: third")
		assert.NotContains(t, result, "## Stage 6")
	})

	t.Run("stage with no agents produces only header", func(t *testing.T) {
		stages := []StageInvestigation{
			{StageName: "empty-stage", StageIndex: 0},
		}

		result := FormatStructuredInvestigation(stages, "")

		assert.Contains(t, result, "## Stage 1: empty-stage")
		assert.NotContains(t, result, "**Agent:")
		assert.NotContains(t, result, "<!-- PARALLEL_RESULTS_START -->")
	})

	t.Run("failed single agent with error shows error and placeholder", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "investigation",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:    "Analyzer",
						AgentIndex:   1,
						LLMBackend:   "langchain",
						Status:       alertsession.StatusFailed,
						ErrorMessage: "LLM provider unreachable",
					},
				},
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		assert.Contains(t, result, "**Agent:** Analyzer (langchain)")
		assert.Contains(t, result, "**Status**: failed")
		assert.Contains(t, result, "**Error**: LLM provider unreachable")
		assert.Contains(t, result, "(No investigation history available)")
	})

	t.Run("parallel-agent stage with empty LLMProvider omits provider", func(t *testing.T) {
		stages := []StageInvestigation{
			{
				StageName:  "validation",
				StageIndex: 0,
				Agents: []AgentInvestigation{
					{
						AgentName:   "AgentA",
						AgentIndex:  1,
						LLMBackend:  "langchain",
						LLMProvider: "gemini-2.5-pro",
						Status:      alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "OK."},
						},
					},
					{
						AgentName:  "AgentB",
						AgentIndex: 2,
						LLMBackend: "google-native",
						// LLMProvider intentionally empty
						Status: alertsession.StatusCompleted,
						Events: []*ent.TimelineEvent{
							{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Also OK."},
						},
					},
				},
			},
		}

		result := FormatStructuredInvestigation(stages, "")

		// Agent with provider shows "(strategy, provider)"
		assert.Contains(t, result, "#### Agent 1: AgentA (langchain, gemini-2.5-pro)")
		// Agent without provider shows "(strategy)" only — no trailing comma/space
		assert.Contains(t, result, "#### Agent 2: AgentB (google-native)")
		assert.NotContains(t, result, "AgentB (google-native, )")
	})

	t.Run("parallel format matches synthesis format exactly", func(t *testing.T) {
		// Same agents used in both formatters should produce identical parallel blocks
		agents := []AgentInvestigation{
			{
				AgentName:   "AgentA",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini-2.5-pro",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding A."},
				},
			},
			{
				AgentName:   "AgentB",
				AgentIndex:  2,
				LLMBackend:  "google-native",
				LLMProvider: "claude-sonnet",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding B."},
				},
			},
		}

		synthesisResult := FormatInvestigationForSynthesis(agents, "investigation")

		stages := []StageInvestigation{
			{StageName: "investigation", StageIndex: 0, Agents: agents},
		}
		chatResult := FormatStructuredInvestigation(stages, "")

		// The parallel block within the chat output should match synthesis exactly
		// Extract the parallel block from chat output (after the stage header)
		parallelStart := strings.Index(chatResult, "<!-- PARALLEL_RESULTS_START -->")
		require.Greater(t, parallelStart, 0, "chat result should contain PARALLEL_RESULTS_START")

		endMarker := "<!-- PARALLEL_RESULTS_END -->\n"
		endIdx := strings.Index(chatResult, endMarker)
		require.GreaterOrEqual(t, endIdx, 0, "chat result should contain PARALLEL_RESULTS_END marker")

		parallelEnd := endIdx + len(endMarker)
		require.Greater(t, parallelEnd, parallelStart,
			"PARALLEL_RESULTS_END must come after PARALLEL_RESULTS_START in chatResult")

		chatParallelBlock := chatResult[parallelStart:parallelEnd]
		assert.Equal(t, synthesisResult, chatParallelBlock, "parallel block in chat must match synthesis output exactly")
	})
}

// ────────────────────────────────────────────────────────────
// FormatInvestigationForSynthesis
// ────────────────────────────────────────────────────────────

func TestFormatInvestigationForSynthesis(t *testing.T) {
	t.Run("two agents both completed", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "AgentA",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini-2.5-pro",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding A."},
				},
			},
			{
				AgentName:   "AgentB",
				AgentIndex:  2,
				LLMBackend:  "google-native",
				LLMProvider: "claude-sonnet",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding B."},
				},
			},
		}

		result := FormatInvestigationForSynthesis(agents, "investigation")

		assert.True(t, strings.HasPrefix(result, "<!-- PARALLEL_RESULTS_START -->"))
		assert.True(t, strings.HasSuffix(result, "<!-- PARALLEL_RESULTS_END -->\n"))
		assert.Contains(t, result, `"investigation" — 2/2 agents succeeded`)
		assert.Contains(t, result, "#### Agent 1: AgentA (langchain, gemini-2.5-pro)\n**Status**: completed")
		assert.Contains(t, result, "#### Agent 2: AgentB (google-native, claude-sonnet)\n**Status**: completed")
		assert.Contains(t, result, "**Final Analysis:**\n\nFinding A.")
		assert.Contains(t, result, "**Final Analysis:**\n\nFinding B.")
		// No error blocks for completed agents
		assert.NotContains(t, result, "**Error**")
	})

	t.Run("one failed with error", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "AgentA",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "OK."},
				},
			},
			{
				AgentName:    "AgentB",
				AgentIndex:   2,
				LLMBackend:   "langchain",
				LLMProvider:  "gemini",
				Status:       alertsession.StatusFailed,
				ErrorMessage: "LLM timeout",
			},
		}

		result := FormatInvestigationForSynthesis(agents, "investigation")

		assert.Contains(t, result, `1/2 agents succeeded`)
		assert.Contains(t, result, "**Status**: failed")
		assert.Contains(t, result, "**Error**: LLM timeout")
		// Failed agent with no events shows placeholder
		assert.Contains(t, result, "(No investigation history available)")
	})

	t.Run("failed agent without error message", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "AgentA",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini",
				Status:      alertsession.StatusFailed,
				// No ErrorMessage, no Events
			},
		}

		result := FormatInvestigationForSynthesis(agents, "stage-1")

		assert.Contains(t, result, "0/1 agents succeeded")
		// No error line when ErrorMessage is empty
		assert.NotContains(t, result, "**Error**")
		assert.Contains(t, result, "(No investigation history available)")
	})

	t.Run("completed agent with no events omits placeholder", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "AgentA",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini",
				Status:      alertsession.StatusCompleted,
				// No events — but completed, so no placeholder
			},
		}

		result := FormatInvestigationForSynthesis(agents, "stage-1")

		assert.Contains(t, result, "1/1 agents succeeded")
		assert.NotContains(t, result, "(No investigation history available)")
	})

	t.Run("agent with empty LLMProvider omits provider from header", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:  "AgentA",
				AgentIndex: 1,
				LLMBackend: "langchain",
				// LLMProvider intentionally empty
				Status: alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Done."},
				},
			},
		}

		result := FormatInvestigationForSynthesis(agents, "stage-1")

		assert.Contains(t, result, "#### Agent 1: AgentA (langchain)\n**Status**: completed")
		assert.NotContains(t, result, "AgentA (langchain, )")
	})

	t.Run("events are formatted through shared formatter", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "Agent",
				AgentIndex:  1,
				LLMBackend:  "langchain",
				LLMProvider: "gemini",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{
						EventType: timelineevent.EventTypeLlmToolCall,
						Metadata: map[string]interface{}{
							"server_name": "k8s",
							"tool_name":   "pods_list",
							"arguments":   "",
						},
					},
					{EventType: timelineevent.EventTypeMcpToolSummary, Content: "3 pods"},
				},
			},
		}

		result := FormatInvestigationForSynthesis(agents, "stage-1")

		// Tool call + summary deduplication works through the shared formatter
		require.Contains(t, result, "**Tool Call:** k8s.pods_list()")
		assert.Contains(t, result, "**Result (summarized):**\n\n3 pods")
		// Standalone summary block should NOT appear (it was consumed by dedup)
		assert.NotContains(t, result, "**Tool Result Summary:**")
	})
}
