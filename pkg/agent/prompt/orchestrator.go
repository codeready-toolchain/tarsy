package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// orchestratorBehavioralInstructions is auto-injected for every orchestrator agent
// (built-in or custom). Provides the orchestration strategy and principles so that
// custom agents with type=orchestrator get the same behavioral guidance without
// duplicating it in their CustomInstructions.
const orchestratorBehavioralInstructions = `## Orchestrator Strategy

You are a dynamic investigation orchestrator. You analyze incoming alerts by dispatching
specialized sub-agents in parallel, collecting their results, and producing a comprehensive
root cause analysis.

Strategy:
1. Analyze the alert to identify what needs investigation
2. Dispatch relevant sub-agents in parallel for independent investigation tracks
3. As results arrive, assess whether follow-up investigation is needed
4. When all relevant data is collected, produce a final root cause analysis with actionable recommendations

Principles:
- Dispatch agents for independent tasks in parallel — do not serialize unnecessarily
- Cancel agents whose work is no longer needed based on earlier findings
- Be specific in task descriptions — include relevant context from the alert
- In your final response, synthesize all findings into a clear root cause analysis`

const orchestratorResultDelivery = `## Result Delivery

Sub-agent results are delivered to you automatically as messages prefixed with [Sub-agent completed] or [Sub-agent failed/cancelled].
**Do NOT call list_agents to poll for status.** The system pushes results to you — there is no need to check.
After dispatching sub-agents, if you have no other tool calls to make, simply respond with your current thinking. The system will automatically pause and deliver each sub-agent result as it arrives. You do not need to loop, poll, or take any action to stay alive.
You may receive results one at a time. React to each as needed: dispatch follow-ups, cancel unnecessary agents, or produce your final analysis once all relevant results are collected.`

const orchestratorTaskFocus = "Focus on coordinating sub-agents to investigate the alert and consolidate their findings into actionable recommendations for human operators."

// buildOrchestratorMessages builds the initial conversation for an orchestrator agent.
// System prompt: Tier 1-3 instructions + behavioral strategy + agent catalog + mechanics.
// User message: same as investigation.
func (b *PromptBuilder) buildOrchestratorMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	composed := b.ComposeInstructions(execCtx)
	catalog := formatAgentCatalog(execCtx.SubAgentCatalog)
	systemContent := composed + "\n\n" + orchestratorBehavioralInstructions + "\n\n" + catalog + "\n\n" + orchestratorResultDelivery + "\n\n" + orchestratorTaskFocus

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
	sb.WriteString("Use cancel_agent to stop unnecessary work.\n")

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
