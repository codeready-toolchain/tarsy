package context

import (
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
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

	for _, event := range events {
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
		case timelineevent.EventTypeLlmToolCall, timelineevent.EventTypeMcpToolCall:
			sb.WriteString("**Tool Call:** ")
			sb.WriteString(event.Content)
			sb.WriteString("\n\n")
		case timelineevent.EventTypeToolResult:
			sb.WriteString("**Observation:**\n\n")
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

	return sb.String()
}
