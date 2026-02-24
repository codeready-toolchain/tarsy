package agent

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const DefaultMaxIterations = 20

// DefaultIterationTimeout is the default per-iteration timeout.
// Each iteration (LLM call + tool execution) gets its own context.WithTimeout
// derived from the parent session context. This prevents a single stuck
// iteration from consuming the entire session budget.
const DefaultIterationTimeout = 120 * time.Second

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

	// Resolve LLM backend (defaults → agentDef → chain → agentConfig)
	backend := resolveLLMBackend(
		defaults.LLMBackend, agentDef.LLMBackend,
		chain.LLMBackend, agentConfig.LLMBackend,
	)

	// Resolve LLM provider (defaults → chain → agentConfig)
	provider, providerName, err := resolveLLMProvider(cfg,
		defaults.LLMProvider, chain.LLMProvider, agentConfig.LLMProvider,
	)
	if err != nil {
		return nil, err
	}

	// Resolve max iterations (defaults → agentDef → chain → stage → agentConfig)
	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations,
		chain.MaxIterations, stageConfig.MaxIterations, agentConfig.MaxIterations,
	)

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
		Type:               agentDef.Type,
		LLMBackend:         backend,
		LLMProvider:        provider,
		LLMProviderName:    providerName,
		MaxIterations:      maxIter,
		IterationTimeout:   DefaultIterationTimeout,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
	}, nil
}

// ResolveChatProviderName resolves the LLM provider name for a chat execution
// using the hierarchy: defaults → chain → chatCfg.
// This is extracted so the same logic can be used in error paths before full
// config resolution (e.g., for audit-trail records when ResolveChatAgentConfig fails).
func ResolveChatProviderName(defaults *config.Defaults, chain *config.ChainConfig, chatCfg *config.ChatConfig) string {
	var providerName string
	if defaults != nil {
		providerName = defaults.LLMProvider
	}
	if chain != nil && chain.LLMProvider != "" {
		providerName = chain.LLMProvider
	}
	if chatCfg != nil && chatCfg.LLMProvider != "" {
		providerName = chatCfg.LLMProvider
	}
	return providerName
}

// ResolveChatAgentConfig builds the agent configuration for a chat execution.
// Hierarchy: defaults → agent definition → chain → chat config.
// Similar to ResolveAgentConfig but without stage-level overrides.
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

	// Extract optional overrides from chatCfg (may be nil)
	var chatBackend config.LLMBackend
	var chatProvider string
	var chatMaxIter *int
	if chatCfg != nil {
		chatBackend = chatCfg.LLMBackend
		chatProvider = chatCfg.LLMProvider
		chatMaxIter = chatCfg.MaxIterations
	}

	// Resolve LLM backend (defaults → agentDef → chain → chatCfg)
	backend := resolveLLMBackend(
		defaults.LLMBackend, agentDef.LLMBackend,
		chain.LLMBackend, chatBackend,
	)

	// Resolve LLM provider (defaults → chain → chatCfg)
	provider, providerName, err := resolveLLMProvider(cfg,
		defaults.LLMProvider, chain.LLMProvider, chatProvider,
	)
	if err != nil {
		return nil, err
	}

	// Resolve max iterations (defaults → agentDef → chain → chatCfg)
	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations,
		chain.MaxIterations, chatMaxIter,
	)

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
		stageServers := AggregateChainMCPServers(cfg, chain)
		if len(stageServers) > 0 {
			mcpServers = stageServers
		}
	}
	if chatCfg != nil && len(chatCfg.MCPServers) > 0 {
		mcpServers = chatCfg.MCPServers
	}

	return &ResolvedAgentConfig{
		AgentName:          agentName,
		Type:               config.AgentTypeDefault,
		LLMBackend:         backend,
		LLMProvider:        provider,
		LLMProviderName:    providerName,
		MaxIterations:      maxIter,
		IterationTimeout:   DefaultIterationTimeout,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
	}, nil
}

// ResolveScoringConfig builds the agent configuration for a scoring execution.
// Hierarchy: defaults → agent definition → chain → scoring config.
// Similar to ResolveChatAgentConfig but without stage aggregation for MCP servers
// (scoring isn't part of investigation stages).
func ResolveScoringConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
	scoringCfg *config.ScoringConfig,
) (*ResolvedAgentConfig, error) {
	if chain == nil {
		return nil, fmt.Errorf("chain configuration cannot be nil")
	}

	defaults := cfg.Defaults

	// Agent name: "ScoringAgent" → defaults.ScoringAgent → scoringCfg.Agent
	agentName := "ScoringAgent"
	if defaults.ScoringAgent != "" {
		agentName = defaults.ScoringAgent
	}
	if scoringCfg != nil && scoringCfg.Agent != "" {
		agentName = scoringCfg.Agent
	}

	// Get agent definition (built-in or user-defined)
	agentDef, err := cfg.GetAgent(agentName)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentName, err)
	}

	// Extract optional overrides from scoringCfg (may be nil)
	var scoringBackend config.LLMBackend
	var scoringProvider string
	var scoringMaxIter *int
	if scoringCfg != nil {
		scoringBackend = scoringCfg.LLMBackend
		scoringProvider = scoringCfg.LLMProvider
		scoringMaxIter = scoringCfg.MaxIterations
	}

	// Resolve LLM backend (defaults → agentDef → scoringCfg).
	// chain.LLMBackend is intentionally excluded: the chain-level
	// backend targets investigation agents.
	backend := resolveLLMBackend(
		defaults.LLMBackend, agentDef.LLMBackend, scoringBackend,
	)

	// Resolve LLM provider (defaults → chain → scoringCfg)
	provider, providerName, err := resolveLLMProvider(cfg,
		defaults.LLMProvider, chain.LLMProvider, scoringProvider,
	)
	if err != nil {
		return nil, err
	}

	// Resolve max iterations (defaults → agentDef → chain → scoringCfg)
	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations,
		chain.MaxIterations, scoringMaxIter,
	)

	// Resolve MCP servers: agentDef → chain → scoringCfg
	// No stage aggregation — scoring isn't part of investigation stages.
	var mcpServers []string
	if len(agentDef.MCPServers) > 0 {
		mcpServers = agentDef.MCPServers
	}
	if len(chain.MCPServers) > 0 {
		mcpServers = chain.MCPServers
	}
	if scoringCfg != nil && len(scoringCfg.MCPServers) > 0 {
		mcpServers = scoringCfg.MCPServers
	}

	return &ResolvedAgentConfig{
		AgentName:          agentName,
		Type:               config.AgentTypeScoring,
		LLMBackend:         backend,
		LLMProvider:        provider,
		LLMProviderName:    providerName,
		MaxIterations:      maxIter,
		IterationTimeout:   DefaultIterationTimeout,
		MCPServers:         mcpServers,
		CustomInstructions: agentDef.CustomInstructions,
	}, nil
}

// resolveLLMBackend returns the last non-empty backend from the
// given overrides, listed in lowest-to-highest precedence order.
func resolveLLMBackend(overrides ...config.LLMBackend) config.LLMBackend {
	var backend config.LLMBackend
	for _, o := range overrides {
		if o != "" {
			backend = o
		}
	}
	return backend
}

// resolveLLMProvider picks the last non-empty provider name from the given
// overrides and looks it up in the config registry.
func resolveLLMProvider(cfg *config.Config, providerNames ...string) (*config.LLMProviderConfig, string, error) {
	var name string
	for _, n := range providerNames {
		if n != "" {
			name = n
		}
	}
	provider, err := cfg.GetLLMProvider(name)
	if err != nil {
		return nil, "", fmt.Errorf("LLM provider %q not found: %w", name, err)
	}
	return provider, name, nil
}

// resolveMaxIterations returns the last non-nil value from the given
// overrides, falling back to DefaultMaxIterations.
func resolveMaxIterations(overrides ...*int) int {
	maxIter := DefaultMaxIterations
	for _, o := range overrides {
		if o != nil {
			maxIter = *o
		}
	}
	return maxIter
}

// AggregateChainMCPServers collects the union of all MCP servers used by the
// chain's investigation stages. It checks stage-level overrides, stage-agent
// overrides, and the agent definitions from the registry. This ensures the
// chat agent inherits all tools that investigation agents had access to.
//
// Also used by the dashboard default-tools endpoint to report which MCP servers
// are configured for a given alert type's chain.
func AggregateChainMCPServers(cfg *config.Config, chain *config.ChainConfig) []string {
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
			agentDef, err := cfg.GetAgent(ag.Name)
			if err != nil {
				slog.Warn("AggregateChainMCPServers: failed to resolve agent definition",
					"agent", ag.Name, "error", err)
				continue
			}
			add(agentDef.MCPServers)
		}
	}
	return servers
}
