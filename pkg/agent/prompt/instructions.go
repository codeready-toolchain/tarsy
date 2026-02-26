package prompt

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
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
Focus on root cause analysis and sustainable solutions.

## Evidence Transparency

Your conclusions MUST be grounded in evidence you actually gathered, not assumptions:

- **Distinguish data sources**: Clearly separate what you learned from tool results vs. what was already in the alert data. Never present alert metadata as if it were independently verified.
- **Report tool failures honestly**: If a tool call fails, returns empty results, or returns errors — say so explicitly. Do not silently proceed as if you have the data.
- **Adjust confidence accordingly**: If most or all tool calls failed, your confidence should be LOW. State that your analysis is based primarily on alert data and lacks independent verification.
- **Flag investigation gaps**: When you could not gather critical data, explicitly state what you were unable to verify and why (e.g., "Tool X returned an error, so I could not confirm Y").
- **Never fabricate evidence**: Do not invent details, metrics, or observations that did not appear in tool results or the alert data.`

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
Focus on root cause analysis and sustainable solutions.

## Evaluating Investigation Quality

When reviewing parallel investigation results, assess whether each agent actually gathered evidence:

- **Check for real tool data**: Did the agent successfully execute tool calls and get meaningful results, or did it mostly restate information from the alert?
- **Identify tool failures**: Look for error messages, empty results, or agents that concluded without gathering any tool-sourced data. These investigations provide little beyond the original alert.
- **Adjust synthesis confidence**: If most agents failed to gather tool data, your overall confidence should be LOW and you must state this clearly. Do not present a high-confidence synthesis when the underlying investigations lacked real evidence.
- **Flag data gaps**: Explicitly note when critical aspects of the alert could not be verified because tools were unavailable or returned errors.`

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

	// Unavailable server warnings (from per-session MCP initialization failures)
	sections = b.appendUnavailableServerWarnings(sections, execCtx.FailedServers)

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

	// Unavailable server warnings
	sections = b.appendUnavailableServerWarnings(sections, execCtx.FailedServers)

	// Tier 3: Custom agent instructions
	if execCtx.Config.CustomInstructions != "" {
		sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
	}

	// Chat-specific guidelines
	sections = append(sections, chatResponseGuidelines)

	return strings.Join(sections, "\n\n")
}

// synthesisNativeToolsGuidance is appended to the synthesis system prompt when
// native Gemini tools (Google Search, URL Context) are available
// (synthesis with google-native backend). Since synthesis has no MCP
// tools, native tools are not suppressed by the Gemini API's mutual-exclusivity
// constraint.
const synthesisNativeToolsGuidance = `## Web Search and URL Context Capabilities

You have access to Google Search and URL Context. Use them to look up anything from the investigations that you are not fully certain about — such as unfamiliar processes, tools, software, container images, domains, error messages, or configurations. If the investigations reference URLs, documentation links, or external resources, use URL Context to fetch and review their content. Up-to-date information from the web can help you make a more accurate and confident assessment rather than relying solely on your internal knowledge.

Your primary focus remains critically evaluating and integrating the parallel investigation results.`

// composeSynthesisInstructions builds the system prompt for synthesis agents.
// Uses synthesisGeneralInstructions (Tier 1, no tool references) + optional
// native tools guidance (when Google Search / URL Context are available) +
// custom instructions (Tier 3).
// Skips MCP instructions (Tier 2) since synthesis has no MCP servers.
func (b *PromptBuilder) composeSynthesisInstructions(execCtx *agent.ExecutionContext) string {
	sections := []string{synthesisGeneralInstructions}

	// Add native tools guidance when Google Search or URL Context is available
	// (synthesis with google-native backend). Synthesis has no MCP tools, so native tools
	// are not suppressed by the Gemini API's mutual-exclusivity constraint.
	if hasNativeWebTools(execCtx) {
		sections = append(sections, synthesisNativeToolsGuidance)
	}

	// Tier 3: Agent-specific custom instructions
	if execCtx.Config.CustomInstructions != "" {
		sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
	}

	return strings.Join(sections, "\n\n")
}

// hasNativeWebTools checks whether the execution context has native
// Google Search or URL Context enabled in the LLM provider configuration.
func hasNativeWebTools(execCtx *agent.ExecutionContext) bool {
	if execCtx.Config == nil || execCtx.Config.LLMProvider == nil {
		return false
	}
	nt := execCtx.Config.LLMProvider.NativeTools
	return nt[config.GoogleNativeToolGoogleSearch] || nt[config.GoogleNativeToolURLContext]
}

// appendUnavailableServerWarnings adds a warning section when MCP servers failed to initialize.
func (b *PromptBuilder) appendUnavailableServerWarnings(sections []string, failedServers map[string]string) []string {
	if len(failedServers) == 0 {
		return sections
	}
	var sb strings.Builder
	sb.WriteString("## Unavailable MCP Servers\n\n")
	sb.WriteString("The following servers failed to initialize and their tools are NOT available:\n")
	keys := make([]string, 0, len(failedServers))
	for k := range failedServers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, serverID := range keys {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", serverID, failedServers[serverID]))
	}
	sb.WriteString("\nDo not attempt to use tools from these servers.")
	return append(sections, sb.String())
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
