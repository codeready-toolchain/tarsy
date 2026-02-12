package agent

import (
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const DefaultMaxIterations = 20

// DefaultIterationTimeout is the default per-iteration timeout.
// Each iteration (LLM call + tool execution) gets its own context.WithTimeout
// derived from the parent session context. This prevents a single stuck
// iteration from consuming the entire session budget.
const DefaultIterationTimeout = 120 * time.Second

// ResolveBackend maps an iteration strategy to its Python backend.
// Native thinking strategies use the Google SDK directly; everything
// else goes through LangChain.
func ResolveBackend(strategy config.IterationStrategy) string {
	switch strategy {
	case config.IterationStrategyNativeThinking,
		config.IterationStrategySynthesisNativeThinking:
		return BackendGoogleNative
	default:
		return BackendLangChain
	}
}

// ResolveAgentConfig builds the final agent configuration by applying
// the hierarchy: defaults → agent definition → chain → stage → stage-agent.
func ResolveAgentConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
	stageConfig config.StageConfig,
	agentConfig config.StageAgentConfig,
) (*ResolvedAgentConfig, error) {
	// Guard against nil chain to prevent nil pointer dereference
	// when accessing chain.LLMProvider and chain.MaxIterations
	if chain == nil {
		return nil, fmt.Errorf("chain configuration cannot be nil")
	}

	defaults := cfg.Defaults

	// Get agent definition (built-in or user-defined)
	agentDef, err := cfg.GetAgent(agentConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentConfig.Name, err)
	}

	// Resolve iteration strategy: defaults.IterationStrategy → agentDef.IterationStrategy
	// → chain.IterationStrategy → agentConfig.IterationStrategy (later values override earlier ones).
	strategy := defaults.IterationStrategy
	if agentDef.IterationStrategy != "" {
		strategy = agentDef.IterationStrategy
	}
	if chain.IterationStrategy != "" {
		strategy = chain.IterationStrategy
	}
	if agentConfig.IterationStrategy != "" {
		strategy = agentConfig.IterationStrategy
	}

	// Resolve LLM provider (stage-agent > chain > defaults)
	providerName := defaults.LLMProvider
	if chain.LLMProvider != "" {
		providerName = chain.LLMProvider
	}
	if agentConfig.LLMProvider != "" {
		providerName = agentConfig.LLMProvider
	}
	provider, err := cfg.GetLLMProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("LLM provider %q not found: %w", providerName, err)
	}

	// Resolve max iterations (stage-agent > stage > chain > agent-def > defaults)
	maxIter := DefaultMaxIterations
	if defaults.MaxIterations != nil {
		maxIter = *defaults.MaxIterations
	}
	if agentDef.MaxIterations != nil {
		maxIter = *agentDef.MaxIterations
	}
	if chain.MaxIterations != nil {
		maxIter = *chain.MaxIterations
	}
	if stageConfig.MaxIterations != nil {
		maxIter = *stageConfig.MaxIterations
	}
	if agentConfig.MaxIterations != nil {
		maxIter = *agentConfig.MaxIterations
	}

	// Resolve MCP servers (stage-agent > stage > chain > agent-def > defaults)
	var mcpServers []string
	if len(agentDef.MCPServers) > 0 {
		mcpServers = agentDef.MCPServers
	}
	if len(chain.MCPServers) > 0 {
		mcpServers = chain.MCPServers
	}
	if len(stageConfig.MCPServers) > 0 {
		mcpServers = stageConfig.MCPServers
	}
	if len(agentConfig.MCPServers) > 0 {
		mcpServers = agentConfig.MCPServers
	}

	return &ResolvedAgentConfig{
		AgentName:          agentConfig.Name,
		IterationStrategy:  strategy,
		LLMProvider:        provider,
		MaxIterations:      maxIter,
		IterationTimeout:   DefaultIterationTimeout,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
		Backend:            ResolveBackend(strategy),
	}, nil
}

// ResolveChatAgentConfig builds the agent configuration for a chat execution.
// Hierarchy: defaults → agent definition → chain → chat config.
// Similar to ResolveAgentConfig but without stage-level overrides.
// NOTE: The iteration strategy, LLM provider, and max iterations resolution
// blocks parallel ResolveAgentConfig. If a third resolver variant is needed,
// consider extracting common resolution helpers to reduce duplication.
func ResolveChatAgentConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
	chatCfg *config.ChatConfig,
) (*ResolvedAgentConfig, error) {
	if chain == nil {
		return nil, fmt.Errorf("chain configuration cannot be nil")
	}

	defaults := cfg.Defaults

	// Agent name: chatCfg.Agent → "ChatAgent"
	agentName := "ChatAgent"
	if chatCfg != nil && chatCfg.Agent != "" {
		agentName = chatCfg.Agent
	}

	// Get agent definition (built-in or user-defined)
	agentDef, err := cfg.GetAgent(agentName)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentName, err)
	}

	// Resolve iteration strategy: defaults → agentDef → chain → chatCfg
	strategy := defaults.IterationStrategy
	if agentDef.IterationStrategy != "" {
		strategy = agentDef.IterationStrategy
	}
	if chain.IterationStrategy != "" {
		strategy = chain.IterationStrategy
	}
	if chatCfg != nil && chatCfg.IterationStrategy != "" {
		strategy = chatCfg.IterationStrategy
	}

	// Resolve LLM provider: defaults → chain → chatCfg
	providerName := defaults.LLMProvider
	if chain.LLMProvider != "" {
		providerName = chain.LLMProvider
	}
	if chatCfg != nil && chatCfg.LLMProvider != "" {
		providerName = chatCfg.LLMProvider
	}
	provider, err := cfg.GetLLMProvider(providerName)
	if err != nil {
		return nil, fmt.Errorf("LLM provider %q not found: %w", providerName, err)
	}

	// Resolve max iterations: defaults → agentDef → chain → chatCfg
	maxIter := DefaultMaxIterations
	if defaults.MaxIterations != nil {
		maxIter = *defaults.MaxIterations
	}
	if agentDef.MaxIterations != nil {
		maxIter = *agentDef.MaxIterations
	}
	if chain.MaxIterations != nil {
		maxIter = *chain.MaxIterations
	}
	if chatCfg != nil && chatCfg.MaxIterations != nil {
		maxIter = *chatCfg.MaxIterations
	}

	// Resolve MCP servers for chat (lowest-to-highest precedence):
	// agentDef → chain (or aggregated chain stages) → chatCfg
	var mcpServers []string
	if len(agentDef.MCPServers) > 0 {
		mcpServers = agentDef.MCPServers
	}
	// Aggregate from chain stages (union of all stage MCP servers)
	if len(chain.MCPServers) > 0 {
		mcpServers = chain.MCPServers
	} else {
		stageServers := aggregateChainMCPServers(cfg, chain)
		if len(stageServers) > 0 {
			mcpServers = stageServers
		}
	}
	if chatCfg != nil && len(chatCfg.MCPServers) > 0 {
		mcpServers = chatCfg.MCPServers
	}

	return &ResolvedAgentConfig{
		AgentName:          agentName,
		IterationStrategy:  strategy,
		LLMProvider:        provider,
		MaxIterations:      maxIter,
		IterationTimeout:   DefaultIterationTimeout,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
		Backend:            ResolveBackend(strategy),
	}, nil
}

// aggregateChainMCPServers collects the union of all MCP servers used by the
// chain's investigation stages. It checks stage-level overrides, stage-agent
// overrides, and the agent definitions from the registry. This ensures the
// chat agent inherits all tools that investigation agents had access to.
func aggregateChainMCPServers(cfg *config.Config, chain *config.ChainConfig) []string {
	seen := make(map[string]struct{})
	var servers []string
	add := func(ids []string) {
		for _, s := range ids {
			if _, ok := seen[s]; !ok {
				seen[s] = struct{}{}
				servers = append(servers, s)
			}
		}
	}
	for _, stage := range chain.Stages {
		add(stage.MCPServers)
		for _, ag := range stage.Agents {
			add(ag.MCPServers)
			// Also resolve the agent definition to pick up its MCP servers.
			if agentDef, err := cfg.GetAgent(ag.Name); err == nil {
				add(agentDef.MCPServers)
			}
		}
	}
	return servers
}
