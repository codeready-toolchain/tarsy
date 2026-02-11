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
// FormatInvestigationContext — header + formatted events
// ────────────────────────────────────────────────────────────

func TestFormatInvestigationContext(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		result := FormatInvestigationContext(nil)

		expected := investigationSeparator + "\n" +
			"📋 INVESTIGATION HISTORY\n" +
			investigationSeparator + "\n\n" +
			"# Original Investigation\n\n"
		assert.Equal(t, expected, result)
	})

	t.Run("with events", func(t *testing.T) {
		events := []*ent.TimelineEvent{
			{EventType: timelineevent.EventTypeLlmThinking, Content: "Thinking."},
			{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Done."},
		}
		result := FormatInvestigationContext(events)

		assert.True(t, strings.HasPrefix(result, investigationSeparator))
		assert.Contains(t, result, "**Internal Reasoning:**\n\nThinking.\n\n")
		assert.Contains(t, result, "**Final Analysis:**\n\nDone.\n\n")
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
				Strategy:    "react",
				LLMProvider: "gemini-2.5-pro",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding A."},
				},
			},
			{
				AgentName:   "AgentB",
				AgentIndex:  2,
				Strategy:    "native-thinking",
				LLMProvider: "claude-sonnet",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Finding B."},
				},
			},
		}

		result := FormatInvestigationForSynthesis(agents, "investigation")

		assert.True(t, strings.HasPrefix(result, "<!-- PARALLEL_RESULTS_START -->"))
		assert.True(t, strings.HasSuffix(result, "<!-- PARALLEL_RESULTS_END -->"))
		assert.Contains(t, result, `"investigation" — 2/2 agents succeeded`)
		assert.Contains(t, result, "#### Agent 1: AgentA (react, gemini-2.5-pro)\n**Status**: completed")
		assert.Contains(t, result, "#### Agent 2: AgentB (native-thinking, claude-sonnet)\n**Status**: completed")
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
				Strategy:    "react",
				LLMProvider: "gemini",
				Status:      alertsession.StatusCompleted,
				Events: []*ent.TimelineEvent{
					{EventType: timelineevent.EventTypeFinalAnalysis, Content: "OK."},
				},
			},
			{
				AgentName:    "AgentB",
				AgentIndex:   2,
				Strategy:     "react",
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
				Strategy:    "react",
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
				Strategy:    "react",
				LLMProvider: "gemini",
				Status:      alertsession.StatusCompleted,
				// No events — but completed, so no placeholder
			},
		}

		result := FormatInvestigationForSynthesis(agents, "stage-1")

		assert.Contains(t, result, "1/1 agents succeeded")
		assert.NotContains(t, result, "(No investigation history available)")
	})

	t.Run("events are formatted through shared formatter", func(t *testing.T) {
		agents := []AgentInvestigation{
			{
				AgentName:   "Agent",
				AgentIndex:  1,
				Strategy:    "react",
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
