package prompt

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// PromptBuilder builds all prompt text for agent controllers.
// It composes system messages, user messages, instruction hierarchies,
// and strategy-specific formatting. Stateless — all state comes from
// parameters. Thread-safe — no mutable state.
type PromptBuilder struct {
	mcpRegistry *config.MCPServerRegistry
}

// NewPromptBuilder creates a PromptBuilder with access to MCP server configs.
// Panics if mcpRegistry is nil — callers must provide a valid registry.
func NewPromptBuilder(mcpRegistry *config.MCPServerRegistry) *PromptBuilder {
	if mcpRegistry == nil {
		panic("prompt.NewPromptBuilder: mcpRegistry must not be nil")
	}
	return &PromptBuilder{
		mcpRegistry: mcpRegistry,
	}
}

// MCPServerRegistry returns the MCP server registry for per-server config lookup.
// Used by the summarization logic to check SummarizationConfig per server.
func (b *PromptBuilder) MCPServerRegistry() *config.MCPServerRegistry {
	return b.mcpRegistry
}

const taskFocus = "Focus on investigation and providing recommendations for human operators to execute."
const chatTaskFocus = "Focus on answering follow-up questions about a completed investigation for human operators to execute."

// BuildReActMessages builds the initial conversation for a ReAct investigation.
func (b *PromptBuilder) BuildReActMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
	tools []agent.ToolDefinition,
) []agent.ConversationMessage {
	isChat := execCtx.ChatContext != nil

	// System message: use chat-specific variants when in chat mode
	var composed, formatInstr, focus string
	if isChat {
		composed = b.ComposeChatInstructions(execCtx)
		formatInstr = chatReActFormatInstructions
		focus = chatTaskFocus
	} else {
		composed = b.ComposeInstructions(execCtx)
		formatInstr = reactFormatInstructions
		focus = taskFocus
	}
	systemContent := composed + "\n\n" + formatInstr + "\n\n" + focus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
	}

	// User message
	var userContent string
	if isChat {
		userContent = b.buildChatUserMessage(execCtx, tools)
	} else {
		userContent = b.buildInvestigationUserMessage(execCtx, prevStageContext, tools)
	}

	messages = append(messages, agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: userContent,
	})

	return messages
}

// BuildNativeThinkingMessages builds the initial conversation for a native thinking investigation.
func (b *PromptBuilder) BuildNativeThinkingMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	isChat := execCtx.ChatContext != nil

	// System message (no ReAct format instructions, no tool descriptions in text)
	var composed, focus string
	if isChat {
		composed = b.ComposeChatInstructions(execCtx)
		focus = chatTaskFocus
	} else {
		composed = b.ComposeInstructions(execCtx)
		focus = taskFocus
	}
	systemContent := composed + "\n\n" + focus

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
	}

	// User message (no tool descriptions — tools are native function declarations)
	var userContent string
	if isChat {
		userContent = b.buildChatUserMessage(execCtx, nil)
	} else {
		userContent = b.buildInvestigationUserMessage(execCtx, prevStageContext, nil)
	}

	messages = append(messages, agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: userContent,
	})

	return messages
}

// BuildSynthesisMessages builds the conversation for a synthesis stage.
// Synthesis is a tool-less, single-shot stage that combines parallel results.
// It uses synthesisGeneralInstructions (no tool references) instead of the
// standard generalInstructions. No taskFocus — the synthesis agent's own
// CustomInstructions already define its focus.
// Synthesis is never used in chat sessions, so no ChatContext handling.
func (b *PromptBuilder) BuildSynthesisMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	systemContent := b.composeSynthesisInstructions(execCtx)

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemContent},
	}

	// User message with synthesis-specific structure
	userContent := b.buildSynthesisUserMessage(execCtx, prevStageContext)

	messages = append(messages, agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: userContent,
	})

	return messages
}

// BuildForcedConclusionPrompt returns a prompt to force an LLM conclusion
// at the iteration limit. The format depends on the iteration strategy.
func (b *PromptBuilder) BuildForcedConclusionPrompt(iteration int, strategy config.IterationStrategy) string {
	var formatInstructions string
	switch strategy {
	case config.IterationStrategyReact:
		formatInstructions = reactForcedConclusionFormat
	case config.IterationStrategyNativeThinking:
		formatInstructions = nativeThinkingForcedConclusionFormat
	default:
		slog.Warn("unknown iteration strategy for forced conclusion, using native-thinking format",
			"strategy", strategy)
		formatInstructions = nativeThinkingForcedConclusionFormat
	}
	return fmt.Sprintf(forcedConclusionTemplate, iteration, formatInstructions)
}

// BuildMCPSummarizationSystemPrompt builds the system prompt for MCP result summarization.
func (b *PromptBuilder) BuildMCPSummarizationSystemPrompt(serverName, toolName string, maxSummaryTokens int) string {
	return fmt.Sprintf(mcpSummarizationSystemTemplate, serverName, toolName, maxSummaryTokens)
}

// BuildMCPSummarizationUserPrompt builds the user prompt for MCP result summarization.
func (b *PromptBuilder) BuildMCPSummarizationUserPrompt(conversationContext, serverName, toolName, resultText string) string {
	return fmt.Sprintf(mcpSummarizationUserTemplate, conversationContext, serverName, toolName, resultText)
}

// BuildExecutiveSummarySystemPrompt returns the system prompt for executive summary generation.
func (b *PromptBuilder) BuildExecutiveSummarySystemPrompt() string {
	return executiveSummarySystemPrompt
}

// BuildExecutiveSummaryUserPrompt builds the user prompt for generating an executive summary.
func (b *PromptBuilder) BuildExecutiveSummaryUserPrompt(finalAnalysis string) string {
	return fmt.Sprintf(executiveSummaryUserTemplate, finalAnalysis)
}

// buildInvestigationUserMessage builds the user message for an investigation.
func (b *PromptBuilder) buildInvestigationUserMessage(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
	tools []agent.ToolDefinition,
) string {
	var sb strings.Builder

	// Available tools (ReAct only)
	if len(tools) > 0 {
		sb.WriteString("Answer the following question using the available tools.\n\n")
		sb.WriteString("Available tools:\n\n")
		sb.WriteString(FormatToolDescriptions(tools))
		sb.WriteString("\n\n")
	}

	// Alert section
	sb.WriteString(FormatAlertSection(execCtx.AlertType, execCtx.AlertData))
	sb.WriteString("\n")

	// Runbook section
	sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
	sb.WriteString("\n")

	// Chain context
	sb.WriteString(FormatChainContext(prevStageContext))
	sb.WriteString("\n")

	// Analysis task
	sb.WriteString(analysisTask)

	return sb.String()
}

// buildSynthesisUserMessage builds the user message for synthesis.
func (b *PromptBuilder) buildSynthesisUserMessage(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) string {
	var sb strings.Builder

	sb.WriteString("Synthesize the investigation results and provide recommendations.\n\n")

	// Alert section — alertType intentionally omitted for synthesis; the synthesizer
	// focuses on combining parallel results, not re-analyzing alert metadata.
	sb.WriteString(FormatAlertSection("", execCtx.AlertData))
	sb.WriteString("\n")

	// Runbook section
	sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
	sb.WriteString("\n")

	// Previous stage results (the main content for synthesis)
	sb.WriteString(FormatChainContext(prevStageContext))
	sb.WriteString("\n")

	// Synthesis instructions
	sb.WriteString(synthesisTask)

	return sb.String()
}
