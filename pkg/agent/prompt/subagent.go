package prompt

import "github.com/codeready-toolchain/tarsy/pkg/agent"

const subAgentFocus = `You are a sub-agent dispatched by an orchestrator for a specific task.

Rules:
- Focus exclusively on your assigned task — do not investigate unrelated areas.
- Your final response is automatically reported back to the orchestrator. Do not address the user directly.
- Be concise: state what you found, key evidence, and any relevant details the orchestrator should know.
- If you have tools available, use them to complete your task. If not, use reasoning alone.`

// buildSubAgentMessages builds the initial conversation for a sub-agent
// dispatched by an orchestrator. System prompt: Tier 1-3 instructions
// + sub-agent context. User message: task-only — no alert data, runbook,
// or chain context.
func (b *PromptBuilder) buildSubAgentMessages(
	execCtx *agent.ExecutionContext,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	systemContent := composed + "\n\n" + subAgentFocus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
		{Role: agent.RoleUser, Content: "## Task\n\n" + execCtx.SubAgent.Task},
	}

	return messages
}
