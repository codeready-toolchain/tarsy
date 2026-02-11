package context

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

const investigationSeparator = "═══════════════════════════════════════════════════════════════════════════════"

// FormatInvestigationContext formats timeline events from the original
// investigation into a readable context for chat sessions.
// This is called by the executor/service layer, not by the prompt builder.
func FormatInvestigationContext(events []*ent.TimelineEvent) string {
	var sb strings.Builder
	sb.WriteString(investigationSeparator + "\n")
	sb.WriteString("📋 INVESTIGATION HISTORY\n")
	sb.WriteString(investigationSeparator + "\n\n")
	sb.WriteString("# Original Investigation\n\n")

	formatTimelineEvents(&sb, events)

	return sb.String()
}

// ParallelAgentInvestigation holds one agent's investigation data for synthesis formatting.
type ParallelAgentInvestigation struct {
	AgentName    string
	AgentIndex   int
	Strategy     string               // e.g., "native-thinking", "react"
	LLMProvider  string               // e.g., "gemini-2.5-pro"
	Status       alertsession.Status   // terminal status (completed, failed, etc.)
	Events       []*ent.TimelineEvent // full investigation (from GetAgentTimeline)
	ErrorMessage string               // for failed agents
}

// FormatInvestigationForSynthesis formats multi-agent full investigation
// histories for the synthesis agent. Uses timeline events (which include thinking,
// tool calls, tool results, and responses) rather than raw messages.
// Each agent's investigation is wrapped with identifying metadata.
func FormatInvestigationForSynthesis(agents []ParallelAgentInvestigation, stageName string) string {
	// Count successes
	succeeded := 0
	for _, a := range agents {
		if a.Status == alertsession.StatusCompleted {
			succeeded++
		}
	}

	var sb strings.Builder
	sb.WriteString("<!-- PARALLEL_RESULTS_START -->\n\n")
	fmt.Fprintf(&sb, "### Parallel Investigation: %q — %d/%d agents succeeded\n\n", stageName, succeeded, len(agents))

	for _, a := range agents {
		fmt.Fprintf(&sb, "#### Agent %d: %s (%s, %s)\n", a.AgentIndex, a.AgentName, a.Strategy, a.LLMProvider)
		fmt.Fprintf(&sb, "**Status**: %s\n\n", a.Status)

		if a.Status != alertsession.StatusCompleted && a.ErrorMessage != "" {
			fmt.Fprintf(&sb, "**Error**: %s\n\n", a.ErrorMessage)
		}

		if len(a.Events) == 0 && a.Status != alertsession.StatusCompleted {
			sb.WriteString("(No investigation history available)\n\n")
		} else {
			formatTimelineEvents(&sb, a.Events)
		}
	}

	sb.WriteString("<!-- PARALLEL_RESULTS_END -->")
	return sb.String()
}

// formatTimelineEvents writes formatted timeline events to the builder.
// Handles tool call / summary deduplication: when an mcp_tool_summary event
// follows an llm_tool_call for the same tool invocation, the formatter shows
// the tool name and arguments from the call but uses the summary content.
func formatTimelineEvents(sb *strings.Builder, events []*ent.TimelineEvent) {
	for i := 0; i < len(events); i++ {
		event := events[i]
		if event == nil {
			continue
		}

		switch event.EventType {
		case timelineevent.EventTypeLlmThinking:
			sb.WriteString("**Internal Reasoning:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeLlmResponse:
			sb.WriteString("**Agent Response:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeLlmToolCall:
			// Check if next event is an mcp_tool_summary for deduplication
			toolHeader := formatToolCallHeader(event)
			if i+1 < len(events) && events[i+1] != nil && events[i+1].EventType == timelineevent.EventTypeMcpToolSummary {
				// Use summary content instead of raw result
				sb.WriteString(toolHeader)
				sb.WriteString("**Result (summarized):**\n\n")
				sb.WriteString(events[i+1].Content)
				sb.WriteString("\n\n")
				i++ // skip the summary event
			} else {
				sb.WriteString(toolHeader)
				if event.Content != "" {
					sb.WriteString("**Result:**\n\n")
					sb.WriteString(event.Content)
					sb.WriteString("\n\n")
				}
			}

		case timelineevent.EventTypeMcpToolSummary:
			// Standalone summary (not preceded by tool call — shouldn't happen, but handle gracefully)
			sb.WriteString("**Tool Result Summary:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeFinalAnalysis:
			sb.WriteString("**Final Analysis:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		default:
			sb.WriteString("**" + strings.ReplaceAll(string(event.EventType), "_", " ") + ":**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		}
	}
}

// formatToolCallHeader extracts tool name and arguments from metadata to build
// a tool call header line.
func formatToolCallHeader(event *ent.TimelineEvent) string {
	serverName, _ := event.Metadata["server_name"].(string)
	toolName, _ := event.Metadata["tool_name"].(string)
	arguments, _ := event.Metadata["arguments"].(string)

	if serverName != "" && toolName != "" {
		return fmt.Sprintf("**Tool Call:** %s.%s(%s)\n", serverName, toolName, arguments)
	}
	// Fallback: use content as-is (old format)
	return fmt.Sprintf("**Tool Call:** %s\n", event.Content)
}
