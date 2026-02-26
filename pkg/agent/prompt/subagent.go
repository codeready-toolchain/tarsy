package prompt

import "github.com/codeready-toolchain/tarsy/pkg/agent"

// buildSubAgentMessages builds the initial conversation for a sub-agent
// dispatched by an orchestrator. System prompt: Tier 1-3 instructions
// (same as investigation). User message: task-only — no alert data, runbook,
// or chain context.
func (b *PromptBuilder) buildSubAgentMessages(
	execCtx *agent.ExecutionContext,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	systemContent := composed + "\n\n" + taskFocus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
		{Role: agent.RoleUser, Content: "## Task\n\n" + execCtx.SubAgent.Task},
	}

	return messages
}
