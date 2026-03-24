package context

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

const investigationSeparator = "═══════════════════════════════════════════════════════════════════════════════"

// ────────────────────────────────────────────────────────────
// Types
// ────────────────────────────────────────────────────────────

// StageInvestigation holds one stage's investigation data for structured context.
type StageInvestigation struct {
	StageName       string
	StageIndex      int
	Agents          []AgentInvestigation
	SynthesisResult string // final_analysis from the synthesis stage, if any
}

// AgentInvestigation holds one agent's investigation data.
// Used by both synthesis and chat context formatting.
type AgentInvestigation struct {
	AgentName    string
	AgentIndex   int
	LLMBackend   string               // e.g., "google-native", "langchain"
	LLMProvider  string               // e.g., "gemini-2.5-pro"
	Status       alertsession.Status  // terminal status (completed, failed, etc.)
	Events       []*ent.TimelineEvent // full investigation (from GetAgentTimeline)
	ErrorMessage string               // for failed agents
}

// ────────────────────────────────────────────────────────────
// Public formatters
// ────────────────────────────────────────────────────────────

// FormatStructuredInvestigation formats the full investigation across all stages
// for the chat agent context. Each stage is clearly delineated, and parallel
// stages use the same per-agent format as synthesis.
func FormatStructuredInvestigation(stages []StageInvestigation, executiveSummary string) string {
	var sb strings.Builder
	sb.WriteString(investigationSeparator + "\n")
	sb.WriteString("📋 INVESTIGATION HISTORY\n")
	sb.WriteString(investigationSeparator + "\n\n")

	for i, stg := range stages {
		fmt.Fprintf(&sb, "## Stage %d: %s\n\n", i+1, stg.StageName)

		if len(stg.Agents) == 1 {
			// Single agent — show timeline directly under the stage header.
			a := stg.Agents[0]
			if a.LLMProvider != "" {
				fmt.Fprintf(&sb, "**Agent:** %s (%s, %s)\n", a.AgentName, a.LLMBackend, a.LLMProvider)
			} else {
				fmt.Fprintf(&sb, "**Agent:** %s (%s)\n", a.AgentName, a.LLMBackend)
			}
			fmt.Fprintf(&sb, "**Status**: %s\n\n", a.Status)
			formatAgentBody(&sb, a)
		} else if len(stg.Agents) > 1 {
			formatParallelAgents(&sb, stg.Agents, stg.StageName)
		}

		if stg.SynthesisResult != "" {
			sb.WriteString("### Synthesis Result\n\n")
			sb.WriteString(stg.SynthesisResult)
			sb.WriteString("\n\n")
		}
	}

	if executiveSummary != "" {
		sb.WriteString("## Executive Summary\n\n")
		sb.WriteString(executiveSummary)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// FormatInvestigationForSynthesis formats multi-agent investigation histories
// for the synthesis agent. Uses the same per-agent format as the chat context.
func FormatInvestigationForSynthesis(agents []AgentInvestigation, stageName string) string {
	var sb strings.Builder
	formatParallelAgents(&sb, agents, stageName)
	return sb.String()
}

// ────────────────────────────────────────────────────────────
// Shared formatting helpers
// ────────────────────────────────────────────────────────────

// formatParallelAgents renders a parallel agent section with HTML markers,
// a header showing the success count, and each agent's investigation.
func formatParallelAgents(sb *strings.Builder, agents []AgentInvestigation, stageName string) {
	succeeded := 0
	for _, a := range agents {
		if a.Status == alertsession.StatusCompleted {
			succeeded++
		}
	}

	sb.WriteString("<!-- PARALLEL_RESULTS_START -->\n\n")
	fmt.Fprintf(sb, "### Parallel Investigation: %q — %d/%d agents succeeded\n\n", stageName, succeeded, len(agents))

	for _, a := range agents {
		if a.LLMProvider != "" {
			fmt.Fprintf(sb, "#### Agent %d: %s (%s, %s)\n", a.AgentIndex, a.AgentName, a.LLMBackend, a.LLMProvider)
		} else {
			fmt.Fprintf(sb, "#### Agent %d: %s (%s)\n", a.AgentIndex, a.AgentName, a.LLMBackend)
		}
		fmt.Fprintf(sb, "**Status**: %s\n\n", a.Status)
		formatAgentBody(sb, a)
	}

	sb.WriteString("<!-- PARALLEL_RESULTS_END -->\n")
}

// formatAgentBody renders error message (if any) and timeline events for one agent.
func formatAgentBody(sb *strings.Builder, a AgentInvestigation) {
	if a.Status != alertsession.StatusCompleted && a.ErrorMessage != "" {
		fmt.Fprintf(sb, "**Error**: %s\n\n", a.ErrorMessage)
	}

	if len(a.Events) == 0 && a.Status != alertsession.StatusCompleted {
		sb.WriteString("(No investigation history available)\n\n")
	} else {
		formatTimelineEvents(sb, a.Events)
	}
}

// formatTimelineEvents writes formatted timeline events to the builder.
//
// Deduplication rules:
//   - tool call / summary: when an mcp_tool_summary follows an llm_tool_call,
//     the formatter uses the summary content instead of the raw result.
//   - response / final analysis: when a final_analysis has the same content as
//     the immediately preceding llm_response, the duplicate is skipped. This
//     avoids repeating the agent's last response verbatim.
func formatTimelineEvents(sb *strings.Builder, events []*ent.TimelineEvent) {
	prevWasLlmResponse := false
	var lastResponseContent string

	for i := 0; i < len(events); i++ {
		event := events[i]
		if event == nil {
			continue
		}

		switch event.EventType {
		case timelineevent.EventTypeLlmThinking:
			prevWasLlmResponse = false
			sb.WriteString("**Internal Reasoning:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeLlmResponse:
			prevWasLlmResponse = true
			lastResponseContent = event.Content
			sb.WriteString("**Agent Response:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeLlmToolCall:
			prevWasLlmResponse = false
			lastResponseContent = ""
			toolHeader := formatToolCallHeader(event)
			if i+1 < len(events) && events[i+1] != nil &&
				events[i+1].EventType == timelineevent.EventTypeMcpToolSummary {
				sb.WriteString(toolHeader)
				summary := events[i+1].Content
				if strings.TrimSpace(summary) != "" {
					sb.WriteString("**Result (summarized):**\n\n")
					sb.WriteString(summary)
					sb.WriteString("\n\n")
				}
				i++
			} else {
				sb.WriteString(toolHeader)
				if event.Content != "" {
					sb.WriteString("**Result:**\n\n")
					sb.WriteString(event.Content)
					sb.WriteString("\n\n")
				}
			}

		case timelineevent.EventTypeMcpToolSummary:
			prevWasLlmResponse = false
			sb.WriteString("**Tool Result Summary:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		case timelineevent.EventTypeFinalAnalysis:
			if !(prevWasLlmResponse && event.Content == lastResponseContent) {
				sb.WriteString("**Final Analysis:**\n\n")
				sb.WriteString(event.Content)
				sb.WriteString("\n\n")
			}
			prevWasLlmResponse = false

		case timelineevent.EventTypeSkillLoaded:
			prevWasLlmResponse = false
			skillName, _ := event.Metadata["skill_name"].(string)
			if skillName != "" {
				fmt.Fprintf(sb, "**Pre-loaded Skill: %s**\n\n", skillName)
			} else {
				sb.WriteString("**Pre-loaded Skill:**\n\n")
			}
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")

		default:
			prevWasLlmResponse = false
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
