package agent

import (
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// ResolveAgentConfig builds the final agent configuration by applying
// the hierarchy: defaults → agent definition → chain → stage → stage-agent.
func ResolveAgentConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
	stageConfig config.StageConfig,
	agentConfig config.StageAgentConfig,
) (*ResolvedAgentConfig, error) {
	defaults := cfg.Defaults

	// Get agent definition (built-in or user-defined)
	agentDef, err := cfg.GetAgent(agentConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentConfig.Name, err)
	}

	// Resolve iteration strategy (stage-agent > agent-def > defaults)
	strategy := defaults.IterationStrategy
	if agentDef.IterationStrategy != "" {
		strategy = agentDef.IterationStrategy
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
	maxIter := 20 // built-in default
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

	// Resolve MCP servers (stage-agent > agent-def)
	mcpServers := agentDef.MCPServers
	if len(agentConfig.MCPServers) > 0 {
		mcpServers = agentConfig.MCPServers
	}

	return &ResolvedAgentConfig{
		AgentName:          agentConfig.Name,
		IterationStrategy:  strategy,
		LLMProvider:        provider,
		MaxIterations:      maxIter,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
	}, nil
}
