package context

import (
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
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

	for _, event := range events {
		switch string(event.EventType) {
		case "llm_thinking":
			sb.WriteString("**Internal Reasoning:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		case "llm_response":
			sb.WriteString("**Agent Response:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		case "llm_tool_call", "mcp_tool_call":
			sb.WriteString("**Tool Call:** ")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		case "tool_result":
			sb.WriteString("**Observation:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		case "final_analysis":
			sb.WriteString("**Final Analysis:**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		default:
			sb.WriteString("**" + strings.ReplaceAll(string(event.EventType), "_", " ") + ":**\n\n")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}
