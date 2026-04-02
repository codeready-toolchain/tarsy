package prompt

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// orchestratorBehavioralInstructions is auto-injected for every agent that
// resolves a non-empty sub-agent catalog at runtime. Provides the orchestration
// strategy and principles so that any agent with sub-agents gets the same
// behavioral guidance without duplicating it in their CustomInstructions.
const orchestratorBehavioralInstructions = `## Orchestrator Strategy

You can dispatch specialized sub-agents; you are not required to.
They are there to use: parallel tracks, different tools, and focused work are good reasons to dispatch. For complex tasks, delegation also keeps your context clearer — multi-step investigation and tool output can stay in sub-agent runs instead of cluttering your thread. Lean toward delegation when it fits; working directly is fine when one straightforward path is enough.

Strategy:
1. Understand what is being asked and what evidence you need
2. Prefer dispatching sub-agents in parallel for independent tracks when you have them; use your own tools for narrow, single-step checks or when delegation would not add meaningful leverage
3. As sub-agent results arrive, decide whether follow-up investigation is needed
4. When you have enough verified information, give a clear conclusion with actionable recommendations

Principles:
- Reach for sub-agents when work splits across tracks, tool boundaries, or roles, or when isolating deep tool work keeps your context focused for synthesis
- When you dispatch multiple independent tasks, parallelize rather than serializing unnecessarily
- Cancel agents whose work is no longer needed based on earlier findings
- Be specific in task descriptions — include relevant context from the alert
- In your final response, ground the analysis in evidence you actually gathered either directly or from sub-agents`

func orchestratorResultDelivery() string {
	return fmt.Sprintf(`## Result Delivery

Sub-agent results are delivered to you automatically as follow-up messages. Do NOT call %s to poll for status — the system pushes results to you.
After dispatching sub-agents, if you have no other tool calls to make, respond with a brief status (1-2 sentences only) and stop. The system will pause and deliver each sub-agent result as it becomes available. You do not need to loop, poll, or take any action to stay alive.
You will receive results one at a time. React to each delivered result as needed: dispatch follow-ups, cancel unnecessary agents, or produce your final analysis once all relevant results are collected.

CRITICAL — result integrity rules:
- NEVER predict, fabricate, or speculate about what a sub-agent might find. You do not know the results until they are delivered.
- NEVER dispatch follow-up sub-agents based on anticipated outcomes. Only act on results you have actually received in a prior message.
- If you have not yet received a sub-agent's result, do NOT reference its findings — wait for delivery.

Tracking: keep a mental checklist of every agent you dispatch. When a result arrives, match it against your list. Only produce your final analysis once every dispatched agent has reported back (completed, failed, or cancelled by you).`, builtintools.ListAgents)
}

const orchestratorTaskFocus = "Give clear, actionable guidance. Prefer sub-agents when parallel or specialized work fits the problem; you may work directly when it stays simpler."

// InjectOrchestratorSections appends orchestrator behavioral instructions,
// agent catalog, and result delivery rules to the given system prompt content.
// Called when an agent's SubAgentCatalog is non-empty, regardless of agent type.
func InjectOrchestratorSections(systemContent string, catalog []config.SubAgentEntry) string {
	catalogSection := formatAgentCatalog(catalog)
	return systemContent + "\n\n" + orchestratorBehavioralInstructions + "\n\n" + catalogSection + "\n\n" + orchestratorResultDelivery()
}

// OrchestratorTaskFocus returns the task focus string for orchestrator agents.
// Used by the prompt builder to replace the default task focus when the agent
// has a non-empty sub-agent catalog.
func OrchestratorTaskFocus() string {
	return orchestratorTaskFocus
}

// formatAgentCatalog renders the available sub-agents section for the
// orchestrator's system prompt.
func formatAgentCatalog(entries []config.SubAgentEntry) string {
	var sb strings.Builder
	sb.WriteString("## Available Sub-Agents\n\n")
	sb.WriteString(fmt.Sprintf("You can dispatch these agents using the %s tool.\n", builtintools.DispatchAgent))
	sb.WriteString(fmt.Sprintf("Use %s to stop unnecessary work.\n", builtintools.CancelAgent))

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
