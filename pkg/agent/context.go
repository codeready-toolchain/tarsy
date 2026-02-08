package agent

import (
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
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

	// Configuration (resolved from hierarchy)
	Config *ResolvedAgentConfig

	// Dependencies (injected by executor)
	LLMClient    LLMClient
	ToolExecutor ToolExecutor // Phase 3.2: stub, Phase 4: MCP client
	Services     *ServiceBundle
	// EventPublisher EventPublisher  // Phase 3.4

	// Chat context (nil for non-chat sessions)
	ChatContext *ChatContext
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
	IterationStrategy  config.IterationStrategy
	LLMProvider        *config.LLMProviderConfig
	MaxIterations      int
	IterationTimeout   time.Duration // Per-iteration timeout (default: 120s)
	MCPServers         []string
	CustomInstructions string
	// MCPSelection *models.MCPSelectionConfig  // Phase 4
}

// ChatContext carries chat-specific data for controllers.
// Phase 3.3 prompt builder will use this to compose chat-aware prompts.
type ChatContext struct {
	UserQuestion        string
	InvestigationContext string
	ChatHistory         []ConversationMessage
}
