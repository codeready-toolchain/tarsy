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

	// Investigation context (pre-formatted by executor/service before execution)
	sb.WriteString(chat.InvestigationContext)

	// Chat history (previous exchanges, if any)
	if len(chat.ChatHistory) > 0 {
		sb.WriteString("\n")
		sb.WriteString(FormatChatHistory(chat.ChatHistory))
	}

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

// FormatChatHistory formats previous chat exchanges for inclusion in the prompt.
func FormatChatHistory(exchanges []agent.ChatExchange) string {
	if len(exchanges) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(separator + "\n")
	sb.WriteString(fmt.Sprintf("💬 CHAT HISTORY (%d previous exchange%s)\n",
		len(exchanges), pluralS(len(exchanges))))
	sb.WriteString(separator + "\n\n")

	for i, exchange := range exchanges {
		sb.WriteString(fmt.Sprintf("## Exchange %d\n\n", i+1))
		sb.WriteString("**USER:**\n")
		sb.WriteString(exchange.UserQuestion)
		sb.WriteString("\n\n")

		// Format the conversation messages (assistant responses, tool results, observations)
		for _, msg := range exchange.Messages {
			switch msg.Role {
			case agent.RoleAssistant:
				sb.WriteString("**ASSISTANT:**\n")
				sb.WriteString(msg.Content)
				sb.WriteString("\n\n")
			case agent.RoleTool:
				sb.WriteString("**Observation (tool):**\n\n")
				sb.WriteString(msg.Content)
				sb.WriteString("\n\n")
			case agent.RoleUser:
				sb.WriteString("**Observation:**\n\n")
				sb.WriteString(msg.Content)
				sb.WriteString("\n\n")
			}
		}
	}

	return sb.String()
}

// pluralS returns "s" if count != 1, empty string otherwise.
func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
