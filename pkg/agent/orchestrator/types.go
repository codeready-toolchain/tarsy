// Package orchestrator provides the sub-agent runtime for orchestrator agents.
// It manages sub-agent goroutine lifecycle, result collection, and tool routing.
package orchestrator

import (
	"errors"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// Sentinel errors for orchestration operations.
var (
	ErrAgentNotFound       = errors.New("agent not found in registry")
	ErrMaxConcurrentAgents = errors.New("max concurrent agents exceeded")
	ErrExecutionNotFound   = errors.New("execution not found")
)

// SubAgentDeps bundles dependencies needed by SubAgentRunner to dispatch sub-agents.
// Services are used instead of raw *ent.Client to follow existing data access patterns.
type SubAgentDeps struct {
	Config       *config.Config
	Chain        *config.ChainConfig
	AgentFactory *agent.AgentFactory
	MCPFactory   *mcp.ClientFactory

	LLMClient      agent.LLMClient
	EventPublisher agent.EventPublisher
	PromptBuilder  agent.PromptBuilder

	StageService       *services.StageService
	TimelineService    *services.TimelineService
	MessageService     *services.MessageService
	InteractionService *services.InteractionService

	// Orchestrator's session context, passed through to sub-agent ExecutionContexts.
	AlertData      string
	AlertType      string
	RunbookContent string
}

// OrchestratorGuardrails holds resolved orchestrator limits
// (defaults.orchestrator merged with per-agent orchestrator config).
type OrchestratorGuardrails struct {
	MaxConcurrentAgents int
	AgentTimeout        time.Duration
	MaxBudget           time.Duration
}

// SubAgentResult is the outcome of a completed sub-agent execution.
// Delivered to the orchestrator via the results channel.
type SubAgentResult struct {
	ExecutionID string
	AgentName   string
	Task        string
	Status      agent.ExecutionStatus
	Result      string // FinalAnalysis text on success
	Error       string // Error message on failure
}

// SubAgentStatus is a snapshot of a dispatched sub-agent's state.
// Returned by SubAgentRunner.List.
type SubAgentStatus struct {
	ExecutionID string
	AgentName   string
	Task        string
	Status      agent.ExecutionStatus
}

// subAgentExecution tracks the state of a single dispatched sub-agent.
type subAgentExecution struct {
	executionID string
	agentName   string
	task        string
	status      agent.ExecutionStatus
	cancel      func()
	done        chan struct{}
}

// Orchestration tool names. Plain names (no dots) — naturally separated
// from MCP tools which use server.tool format.
const (
	ToolDispatchAgent = "dispatch_agent"
	ToolCancelAgent   = "cancel_agent"
	ToolListAgents    = "list_agents"
)

// orchestrationTools defines the tool set exposed to the orchestrator LLM.
var orchestrationTools = []agent.ToolDefinition{
	{
		Name:        ToolDispatchAgent,
		Description: "Dispatch a sub-agent to execute a task. Returns immediately. Results are automatically delivered when the sub-agent finishes — do not poll.",
		ParametersSchema: `{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "Agent name from the available agents list"},
				"task": {"type": "string", "description": "Natural language task description"}
			},
			"required": ["name", "task"]
		}`,
	},
	{
		Name:        ToolCancelAgent,
		Description: "Cancel a running sub-agent.",
		ParametersSchema: `{
			"type": "object",
			"properties": {
				"execution_id": {"type": "string", "description": "Execution ID from dispatch_agent"}
			},
			"required": ["execution_id"]
		}`,
	},
	{
		Name:        ToolListAgents,
		Description: "List all dispatched sub-agents and their current status. Use for status overview before deciding to cancel or dispatch more.",
		ParametersSchema: `{
			"type": "object",
			"properties": {}
		}`,
	},
}

// orchestrationToolNames is used for quick lookup when routing tool calls.
var orchestrationToolNames = map[string]bool{
	ToolDispatchAgent: true,
	ToolCancelAgent:   true,
	ToolListAgents:    true,
}

// FormatSubAgentResult formats a sub-agent result as a conversation message
// for injection into the orchestrator's conversation. Used by the controller
// (PR4) to deliver results between iterations.
func FormatSubAgentResult(result *SubAgentResult) agent.ConversationMessage {
	var content string
	if result.Status == agent.ExecutionStatusCompleted {
		content = fmt.Sprintf(
			"[Sub-agent completed] %s (exec %s):\n%s",
			result.AgentName, result.ExecutionID, result.Result,
		)
	} else {
		content = fmt.Sprintf(
			"[Sub-agent %s] %s (exec %s): %s",
			result.Status, result.AgentName, result.ExecutionID, result.Error,
		)
	}
	return agent.ConversationMessage{Role: agent.RoleUser, Content: content}
}
