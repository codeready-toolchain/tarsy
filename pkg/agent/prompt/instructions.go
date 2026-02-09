package prompt

import (
	"log/slog"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// generalInstructions is Tier 1 for investigation agents.
const generalInstructions = `## General SRE Agent Instructions

You are an expert Site Reliability Engineer (SRE) with deep knowledge of:
- Kubernetes and container orchestration
- Cloud infrastructure and services
- Incident response and troubleshooting
- System monitoring and alerting
- GitOps and deployment practices

Analyze alerts thoroughly and provide actionable insights based on:
1. Alert information and context
2. Associated runbook procedures
3. Real-time system data from available tools

Always be specific, reference actual data, and provide clear next steps.
Focus on root cause analysis and sustainable solutions.`

// synthesisGeneralInstructions is Tier 1 for synthesis agents.
// Unlike generalInstructions, this does not mention tools since synthesis
// is a tool-less stage that analyzes results from prior investigations.
const synthesisGeneralInstructions = `## General SRE Analysis Instructions

You are an expert Site Reliability Engineer (SRE) with deep knowledge of:
- Kubernetes and container orchestration
- Cloud infrastructure and services
- Incident response and troubleshooting
- System monitoring and alerting
- GitOps and deployment practices

Analyze investigation results thoroughly and provide actionable insights based on:
1. The original alert information and context
2. Findings from parallel investigations
3. Associated runbook procedures

Always be specific, reference actual data from the investigations, and provide clear next steps.
Focus on root cause analysis and sustainable solutions.`

// chatGeneralInstructions is Tier 1 for chat follow-up sessions.
const chatGeneralInstructions = `## Chat Assistant Instructions

You are an expert Site Reliability Engineer (SRE) assistant helping with follow-up questions about a completed alert investigation.

The user has reviewed the investigation results and has follow-up questions. Your role is to:
- Provide clear, actionable answers based on the investigation history
- Use available tools to gather fresh, real-time data when needed
- Reference specific findings from the original investigation when relevant
- Maintain the same professional SRE communication style
- Be concise but thorough in your responses

You have access to the same tools and systems that were used in the original investigation.`

// chatResponseGuidelines is appended after Tier 2+3 for chat sessions.
const chatResponseGuidelines = `## Response Guidelines

1. **Context Awareness**: Reference the investigation history when it provides relevant context
2. **Fresh Data**: Use tools to gather current system state if the question requires up-to-date information
3. **Clarity**: If the question is ambiguous or unclear, ask for clarification in your Final Answer
4. **Specificity**: Always reference actual data and observations, not assumptions
5. **Brevity**: Be concise but complete - users have already read the full investigation`

// ComposeInstructions builds the three-tier instruction set for an investigation agent.
func (b *PromptBuilder) ComposeInstructions(execCtx *agent.ExecutionContext) string {
	var sections []string

	// Tier 1: General SRE instructions
	sections = append(sections, generalInstructions)

	// Tier 2: MCP server instructions (from registry, keyed by server IDs in config)
	sections = b.appendMCPInstructions(sections, execCtx)

	// Tier 3: Custom agent instructions
	if execCtx.Config.CustomInstructions != "" {
		sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
	}

	return strings.Join(sections, "\n\n")
}

// ComposeChatInstructions builds the instruction set for chat sessions.
func (b *PromptBuilder) ComposeChatInstructions(execCtx *agent.ExecutionContext) string {
	var sections []string

	// Tier 1: Chat-specific general instructions
	sections = append(sections, chatGeneralInstructions)

	// Tier 2: MCP server instructions (same logic as investigation)
	sections = b.appendMCPInstructions(sections, execCtx)

	// Tier 3: Custom agent instructions
	if execCtx.Config.CustomInstructions != "" {
		sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
	}

	// Chat-specific guidelines
	sections = append(sections, chatResponseGuidelines)

	return strings.Join(sections, "\n\n")
}

// composeSynthesisInstructions builds the system prompt for synthesis agents.
// Uses synthesisGeneralInstructions (Tier 1, no tool references) + custom instructions (Tier 3).
// Skips MCP instructions (Tier 2) since synthesis is a tool-less stage.
func (b *PromptBuilder) composeSynthesisInstructions(execCtx *agent.ExecutionContext) string {
	sections := []string{synthesisGeneralInstructions}

	// Tier 3: Agent-specific custom instructions
	if execCtx.Config.CustomInstructions != "" {
		sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
	}

	return strings.Join(sections, "\n\n")
}

// appendMCPInstructions adds Tier 2 MCP server instructions to a sections slice.
func (b *PromptBuilder) appendMCPInstructions(sections []string, execCtx *agent.ExecutionContext) []string {
	for _, serverID := range execCtx.Config.MCPServers {
		serverConfig, err := b.mcpRegistry.Get(serverID)
		if err != nil {
			slog.Debug("MCP server not found in registry, skipping instructions",
				"serverID", serverID, "error", err)
			continue
		}
		if serverConfig.Instructions != "" {
			sections = append(sections, "## "+serverID+" Instructions\n\n"+serverConfig.Instructions)
		}
	}
	return sections
}
