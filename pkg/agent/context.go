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

	// Sub-agent context (nil for non-sub-agents). Same pattern as ChatContext.
	// Set when the agent was dispatched by an orchestrator.
	SubAgent *SubAgentContext

	// SubAgentCollector provides push-based delivery of completed sub-agent
	// results. nil for non-orchestrator agents — all drain/wait code is skipped.
	// Implemented by orchestrator.ResultCollector; interface avoids agent↔orchestrator cycle.
	SubAgentCollector SubAgentResultCollector

	// SubAgentCatalog lists agents available for orchestrator dispatch.
	// Used by the prompt builder to include the catalog in the system prompt.
	SubAgentCatalog []config.SubAgentEntry

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

// ResolvedAgentConfig is the fully-resolved configuration for an agent execution.
// All hierarchy levels (defaults → chain → stage → agent) have been applied.
type ResolvedAgentConfig struct {
	AgentName          string
	Type               config.AgentType  // Determines controller + wrapper selection
	LLMBackend         config.LLMBackend // Determines SDK path (sent as-is to LLM service)
	LLMProvider        *config.LLMProviderConfig
	LLMProviderName    string // The resolved provider key (for observability / DB records)
	MaxIterations      int
	IterationTimeout   time.Duration // Per-iteration timeout (default: 120s)
	MCPServers         []string
	CustomInstructions string

	// NativeToolsOverride is the per-alert native tools override (nil = use provider defaults).
	// Set by the session executor when the alert provides an MCP selection with native_tools.
	NativeToolsOverride *models.NativeToolsConfig
}

// PromptBuilder builds all prompt text for agent controllers.
// Implemented by prompt.PromptBuilder; defined as interface here to
// avoid a circular import between pkg/agent and pkg/agent/prompt.
type PromptBuilder interface {
	BuildFunctionCallingMessages(execCtx *ExecutionContext, prevStageContext string) []ConversationMessage
	BuildSynthesisMessages(execCtx *ExecutionContext, prevStageContext string) []ConversationMessage
	BuildForcedConclusionPrompt(iteration int) string
	BuildMCPSummarizationSystemPrompt(serverName, toolName string, maxSummaryTokens int) string
	BuildMCPSummarizationUserPrompt(conversationContext, serverName, toolName, resultText string) string
	BuildExecutiveSummarySystemPrompt() string
	BuildExecutiveSummaryUserPrompt(finalAnalysis string) string
	BuildScoringSystemPrompt() string
	BuildScoringInitialPrompt(sessionInvestigationContext, outputSchema string) string
	BuildScoringOutputSchemaReminderPrompt(outputSchema string) string
	BuildScoringMissingToolsReportPrompt() string
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
	PublishInteractionCreated(ctx context.Context, sessionID string, payload events.InteractionCreatedPayload) error
	PublishSessionProgress(ctx context.Context, payload events.SessionProgressPayload) error
	PublishExecutionProgress(ctx context.Context, sessionID string, payload events.ExecutionProgressPayload) error
	PublishExecutionStatus(ctx context.Context, sessionID string, payload events.ExecutionStatusPayload) error
}

// SubAgentResultCollector provides push-based delivery of completed sub-agent
// results to the controller. Implemented by orchestrator.ResultCollector;
// defined as interface here to avoid a circular import between pkg/agent
// and pkg/agent/orchestrator.
type SubAgentResultCollector interface {
	// TryDrainResult returns a formatted sub-agent result as a conversation
	// message without blocking. Returns (msg, true) if a result was available,
	// (zero, false) otherwise.
	TryDrainResult() (ConversationMessage, bool)

	// WaitForResult blocks until a sub-agent result is available or the
	// context is cancelled.
	WaitForResult(ctx context.Context) (ConversationMessage, error)

	// HasPending returns true if any dispatched sub-agents haven't delivered
	// results yet.
	HasPending() bool
}

// ChatContext carries chat-specific data for controllers.
type ChatContext struct {
	UserQuestion         string
	InvestigationContext string
}

// SubAgentContext carries sub-agent-specific data for controllers and prompt
// builders. Same pattern as ChatContext — nil for non-sub-agents.
type SubAgentContext struct {
	Task         string // Task assigned by the orchestrator
	ParentExecID string // Parent orchestrator's execution ID
}
