package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const orchestratorTaskFocus = "Focus on coordinating sub-agents to investigate the alert and consolidate their findings into actionable recommendations for human operators."

// buildOrchestratorMessages builds the initial conversation for an orchestrator agent.
// System prompt: Tier 1-3 instructions + agent catalog. User message: same as investigation.
func (b *PromptBuilder) buildOrchestratorMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	catalog := formatAgentCatalog(execCtx.SubAgentCatalog)
	systemContent := composed + "\n\n" + catalog + "\n\n" + orchestratorTaskFocus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
	}

	userContent := b.buildInvestigationUserMessage(execCtx, prevStageContext)
	messages = append(messages, agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: userContent,
	})

	return messages
}

// formatAgentCatalog renders the available sub-agents section for the
// orchestrator's system prompt.
func formatAgentCatalog(entries []config.SubAgentEntry) string {
	var sb strings.Builder
	sb.WriteString("## Available Sub-Agents\n\n")
	sb.WriteString("You can dispatch these agents using the dispatch_agent tool.\n")
	sb.WriteString("Results are delivered automatically when each sub-agent finishes — do not poll.\n")
	sb.WriteString("Use cancel_agent to stop unnecessary work. Use list_agents to check status.\n")

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("\n- **%s**: %s\n", e.Name, e.Description))
		hasMCP := len(e.MCPServers) > 0
		hasNative := len(e.NativeTools) > 0
		if hasMCP {
			sb.WriteString(fmt.Sprintf("  MCP tools: %s\n", strings.Join(e.MCPServers, ", ")))
		}
		if hasNative {
			sb.WriteString(fmt.Sprintf("  Native tools: %s\n", strings.Join(e.NativeTools, ", ")))
		}
		if !hasMCP && !hasNative {
			sb.WriteString("  Tools: none (pure reasoning)\n")
		}
	}

	return sb.String()
}
