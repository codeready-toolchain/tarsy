package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// buildChatUserMessage builds the user message for a chat follow-up session.
func (b *PromptBuilder) buildChatUserMessage(
	execCtx *agent.ExecutionContext,
) string {
	chat := execCtx.ChatContext
	if chat == nil {
		return ""
	}

	var sb strings.Builder

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
