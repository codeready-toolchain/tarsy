package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// buildChatUserMessage builds the user message for a chat follow-up session.
func (b *PromptBuilder) buildChatUserMessage(
	execCtx *agent.ExecutionContext,
	tools []agent.ToolDefinition,
) string {
	chat := execCtx.ChatContext
	if chat == nil {
		return ""
	}

	var sb strings.Builder

	// Available tools (ReAct only)
	if len(tools) > 0 {
		sb.WriteString("Answer the following question using the available tools.\n\n")
		sb.WriteString("Available tools:\n\n")
		sb.WriteString(FormatToolDescriptions(tools))
		sb.WriteString("\n\n")
	}

	// Alert data + runbook — same components used by investigation agents.
	// The chat agent needs the original alert context to answer follow-up questions.
	sb.WriteString(FormatAlertSection(execCtx.AlertType, execCtx.AlertData))
	sb.WriteString("\n")
	sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
	sb.WriteString("\n")

	// Investigation context (pre-formatted by executor — includes full timeline
	// with investigation history and any previous chat exchanges)
	sb.WriteString(chat.InvestigationContext)

	// Current task
	sb.WriteString(fmt.Sprintf(`
%s
🎯 CURRENT TASK
%s

**Question:** %s

**Your Task:**
Answer the user's question based on the investigation context above.
- Reference investigation history when relevant
- Use tools to get fresh data if needed
- Provide clear, actionable responses

Begin your response:
`, separator, separator, chat.UserQuestion))

	return sb.String()
}
