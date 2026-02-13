package agent

import (
	"context"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// ExecutionContext carries all dependencies and state needed by an agent
// during execution. Created by the session executor for each agent run.
type ExecutionContext struct {
	// Identity
	SessionID   string
	StageID     string
	ExecutionID string
	AgentName   string
	AgentIndex  int

	// Alert data (pulled from AlertSession by executor).
	// Arbitrary text — not parsed, not assumed to be JSON.
	AlertData string

	// Alert type (from session/chain config)
	AlertType string

	// Runbook content (fetched by executor, passed as text)
	RunbookContent string

	// Configuration (resolved from hierarchy)
	Config *ResolvedAgentConfig

	// Dependencies (injected by executor)
	LLMClient      LLMClient
	ToolExecutor   ToolExecutor
	EventPublisher EventPublisher // Real-time event delivery to WebSocket clients
	Services       *ServiceBundle

	// Prompt builder (injected by executor, stateless, shared across executions).
	// Implemented by prompt.PromptBuilder; interface avoids agent↔prompt import cycle.
	PromptBuilder PromptBuilder

	// Chat context (nil for non-chat sessions)
	ChatContext *ChatContext

	// FailedServers maps serverID → error message for MCP servers that
	// failed to initialize. Used by the prompt builder to warn the LLM.
	// nil when all servers initialized successfully.
	FailedServers map[string]string
}

// ServiceBundle groups all service dependencies needed during execution.
type ServiceBundle struct {
	Timeline    *services.TimelineService
	Message     *services.MessageService
	Interaction *services.InteractionService
	Stage       *services.StageService
}

// Backend constants — resolved from iteration strategy via ResolveBackend().
const (
	BackendGoogleNative = "google-native"
	BackendLangChain    = "langchain"
)

// ResolvedAgentConfig is the fully-resolved configuration for an agent execution.
// All hierarchy levels (defaults → chain → stage → agent) have been applied.
type ResolvedAgentConfig struct {
	AgentName          string
	IterationStrategy  config.IterationStrategy
	LLMProvider        *config.LLMProviderConfig
	LLMProviderName    string        // The resolved provider key (for observability / DB records)
	MaxIterations      int
	IterationTimeout   time.Duration // Per-iteration timeout (default: 120s)
	MCPServers         []string
	CustomInstructions string
	Backend            string // "google-native" or "langchain" — resolved from iteration strategy

	// NativeToolsOverride is the per-alert native tools override (nil = use provider defaults).
	// Set by the session executor when the alert provides an MCP selection with native_tools.
	NativeToolsOverride *models.NativeToolsConfig
}

// PromptBuilder builds all prompt text for agent controllers.
// Implemented by prompt.PromptBuilder; defined as interface here to
// avoid a circular import between pkg/agent and pkg/agent/prompt.
type PromptBuilder interface {
	BuildReActMessages(execCtx *ExecutionContext, prevStageContext string, tools []ToolDefinition) []ConversationMessage
	BuildNativeThinkingMessages(execCtx *ExecutionContext, prevStageContext string) []ConversationMessage
	BuildSynthesisMessages(execCtx *ExecutionContext, prevStageContext string) []ConversationMessage
	BuildForcedConclusionPrompt(iteration int, strategy config.IterationStrategy) string
	BuildMCPSummarizationSystemPrompt(serverName, toolName string, maxSummaryTokens int) string
	BuildMCPSummarizationUserPrompt(conversationContext, serverName, toolName, resultText string) string
	BuildExecutiveSummarySystemPrompt() string
	BuildExecutiveSummaryUserPrompt(finalAnalysis string) string
	MCPServerRegistry() *config.MCPServerRegistry
}

// EventPublisher publishes events for WebSocket delivery.
// Implemented by events.EventPublisher; defined as interface here to
// avoid a circular import between pkg/agent and pkg/events and to
// enable testing with mocks.
//
// Each method accepts a specific typed payload struct — no untyped maps or any.
type EventPublisher interface {
	PublishTimelineCreated(ctx context.Context, sessionID string, payload events.TimelineCreatedPayload) error
	PublishTimelineCompleted(ctx context.Context, sessionID string, payload events.TimelineCompletedPayload) error
	PublishStreamChunk(ctx context.Context, sessionID string, payload events.StreamChunkPayload) error
	PublishSessionStatus(ctx context.Context, sessionID string, payload events.SessionStatusPayload) error
	PublishStageStatus(ctx context.Context, sessionID string, payload events.StageStatusPayload) error
	PublishChatCreated(ctx context.Context, sessionID string, payload events.ChatCreatedPayload) error
}

// ChatContext carries chat-specific data for controllers.
type ChatContext struct {
	UserQuestion         string
	InvestigationContext string
}
