package agent

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const DefaultMaxIterations = 20

// DefaultLLMBackend is the fallback when no level in the config hierarchy
// specifies an LLM backend. LangChain is the general-purpose multi-provider
// backend and matches the typical production default.
const DefaultLLMBackend = config.LLMBackendLangChain

// DefaultIterationTimeout is the overall per-iteration timeout.
// Each iteration (LLM call + tool execution) gets its own context.WithTimeout
// derived from the parent session context. This prevents a single stuck
// iteration from consuming the entire session budget.
const DefaultIterationTimeout = 6 * time.Minute

// DefaultLLMCallTimeout caps a single LLM streaming call within an iteration.
const DefaultLLMCallTimeout = 5 * time.Minute

// DefaultToolCallTimeout caps a single MCP tool call within an iteration.
const DefaultToolCallTimeout = 1 * time.Minute

// DefaultInitialResponseTimeout is the max wait for the first streaming chunk
// before treating the provider as unresponsive.
const DefaultInitialResponseTimeout = 120 * time.Second

// DefaultStallTimeout is the max gap between consecutive streaming chunks
// before treating the stream as stalled.
const DefaultStallTimeout = 60 * time.Second

// ResolveAgentConfig builds the final agent configuration by applying
// the hierarchy: defaults → agent definition → chain → stage → stage-agent.
func ResolveAgentConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
	stageConfig config.StageConfig,
	agentConfig config.StageAgentConfig,
) (*ResolvedAgentConfig, error) {
	if chain == nil {
		return nil, fmt.Errorf("chain configuration cannot be nil")
	}

	var defaults config.Defaults
	if cfg.Defaults != nil {
		defaults = *cfg.Defaults
	}

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

	// Resolve agent type (agentDef → agentConfig)
	agentType := agentDef.Type
	if agentConfig.Type != "" {
		agentType = agentConfig.Type
	}

	// Resolve fallback providers (defaults → chain → stage → agentConfig)
	fallbackProviders := resolveFallbackProviders(
		defaults.FallbackProviders, chain.FallbackProviders,
		stageConfig.FallbackProviders, agentConfig.FallbackProviders,
	)

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return &ResolvedAgentConfig{
		AgentName:                 agentConfig.Name,
		Type:                      agentType,
		LLMBackend:                backend,
		LLMProvider:               resolvedProvider,
		LLMProviderName:           providerName,
		MaxIterations:             maxIter,
		IterationTimeout:          DefaultIterationTimeout,
		LLMCallTimeout:            DefaultLLMCallTimeout,
		ToolCallTimeout:           DefaultToolCallTimeout,
		MCPServers:                mcpServers,
		CustomInstructions:        agentDef.CustomInstructions,
		FallbackProviders:         fallbackProviders,
		ResolvedFallbackProviders: resolvedFallback,
		InitialResponseTimeout:    DefaultInitialResponseTimeout,
		StallTimeout:              DefaultStallTimeout,
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

	var defaults config.Defaults
	if cfg.Defaults != nil {
		defaults = *cfg.Defaults
	}

	// Agent name: chatCfg.Agent → AgentNameChat
	agentName := config.AgentNameChat
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

	// Resolve fallback providers (defaults → chain; chatCfg has no fallback field)
	fallbackProviders := resolveFallbackProviders(
		defaults.FallbackProviders, chain.FallbackProviders,
	)

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return &ResolvedAgentConfig{
		AgentName: agentName,
		// Chat always uses the iterating function-calling controller,
		// regardless of what the agent definition's Type field says.
		Type:                      config.AgentTypeDefault,
		LLMBackend:                backend,
		LLMProvider:               resolvedProvider,
		LLMProviderName:           providerName,
		MaxIterations:             maxIter,
		IterationTimeout:          DefaultIterationTimeout,
		LLMCallTimeout:            DefaultLLMCallTimeout,
		ToolCallTimeout:           DefaultToolCallTimeout,
		MCPServers:                mcpServers,
		CustomInstructions:        agentDef.CustomInstructions,
		FallbackProviders:         fallbackProviders,
		ResolvedFallbackProviders: resolvedFallback,
		InitialResponseTimeout:    DefaultInitialResponseTimeout,
		StallTimeout:              DefaultStallTimeout,
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

	var defaults config.Defaults
	if cfg.Defaults != nil {
		defaults = *cfg.Defaults
	}

	// Agent name: AgentNameScoring → defaults.ScoringAgent → scoringCfg.Agent
	agentName := config.AgentNameScoring
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

	// Resolve fallback providers (defaults → chain; scoringCfg has no fallback field)
	fallbackProviders := resolveFallbackProviders(
		defaults.FallbackProviders, chain.FallbackProviders,
	)

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return &ResolvedAgentConfig{
		AgentName:                 agentName,
		Type:                      config.AgentTypeScoring,
		LLMBackend:                backend,
		LLMProvider:               resolvedProvider,
		LLMProviderName:           providerName,
		MaxIterations:             maxIter,
		IterationTimeout:          DefaultIterationTimeout,
		LLMCallTimeout:            DefaultLLMCallTimeout,
		ToolCallTimeout:           DefaultToolCallTimeout,
		MCPServers:                mcpServers,
		CustomInstructions:        agentDef.CustomInstructions,
		FallbackProviders:         fallbackProviders,
		ResolvedFallbackProviders: resolvedFallback,
		InitialResponseTimeout:    DefaultInitialResponseTimeout,
		StallTimeout:              DefaultStallTimeout,
	}, nil
}

// ResolveExecSummaryConfig builds the agent configuration for an executive summary execution.
// Hierarchy: defaults → agent definition → chain (including executive_summary_provider).
// The executive_summary_provider chain field is the highest-priority provider override,
// allowing a different model for summaries than for investigation agents.
func ResolveExecSummaryConfig(
	cfg *config.Config,
	chain *config.ChainConfig,
) (*ResolvedAgentConfig, error) {
	if chain == nil {
		return nil, fmt.Errorf("chain configuration cannot be nil")
	}

	var defaults config.Defaults
	if cfg.Defaults != nil {
		defaults = *cfg.Defaults
	}

	// Agent name is always AgentNameExecSummary — no configurable override.
	agentDef, err := cfg.GetAgent(config.AgentNameExecSummary)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", config.AgentNameExecSummary, err)
	}

	// Resolve LLM backend (defaults → agentDef → chain).
	backend := resolveLLMBackend(
		defaults.LLMBackend, agentDef.LLMBackend, chain.LLMBackend,
	)

	// Resolve LLM provider (defaults → chain.llm_provider → chain.executive_summary_provider).
	// executive_summary_provider is the highest-priority override, allowing a dedicated
	// model for summaries without affecting investigation agents.
	provider, providerName, err := resolveLLMProvider(cfg,
		defaults.LLMProvider, chain.LLMProvider, chain.ExecutiveSummaryProvider,
	)
	if err != nil {
		return nil, err
	}

	// Resolve max iterations (defaults → agentDef → chain).
	// Exec summary is a single-shot call; max_iterations from the chain is advisory only.
	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations, chain.MaxIterations, nil,
	)

	// Resolve fallback providers (defaults → chain).
	fallbackProviders := resolveFallbackProviders(
		defaults.FallbackProviders, chain.FallbackProviders,
	)

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return &ResolvedAgentConfig{
		AgentName:                 config.AgentNameExecSummary,
		Type:                      config.AgentTypeExecSummary,
		LLMBackend:                backend,
		LLMProvider:               resolvedProvider,
		LLMProviderName:           providerName,
		MaxIterations:             maxIter,
		IterationTimeout:          DefaultIterationTimeout,
		LLMCallTimeout:            DefaultLLMCallTimeout,
		ToolCallTimeout:           DefaultToolCallTimeout,
		CustomInstructions:        agentDef.CustomInstructions,
		FallbackProviders:         fallbackProviders,
		ResolvedFallbackProviders: resolvedFallback,
		InitialResponseTimeout:    DefaultInitialResponseTimeout,
		StallTimeout:              DefaultStallTimeout,
	}, nil
}

// applyAgentNativeTools clones the provider and merges agent-level native tool
// overrides into the clone's NativeTools map. Returns the original provider
// unchanged when the agent has no native tools override.
func applyAgentNativeTools(provider *config.LLMProviderConfig, agentTools map[config.GoogleNativeTool]bool) *config.LLMProviderConfig {
	if len(agentTools) == 0 {
		return provider
	}
	cloned := *provider
	cloned.NativeTools = make(map[config.GoogleNativeTool]bool, len(provider.NativeTools)+len(agentTools))
	for k, v := range provider.NativeTools {
		cloned.NativeTools[k] = v
	}
	for k, v := range agentTools {
		cloned.NativeTools[k] = v
	}
	return &cloned
}

// resolveLLMBackend returns the last non-empty backend from the
// given overrides, listed in lowest-to-highest precedence order.
// Falls back to DefaultLLMBackend when no override provides a value.
func resolveLLMBackend(overrides ...config.LLMBackend) config.LLMBackend {
	backend := DefaultLLMBackend
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

// resolveFallbackProviders returns the last non-nil fallback list from the
// given overrides, listed in lowest-to-highest precedence order.
// A non-nil empty slice is an explicit override that clears inherited values.
// Returns nil when no override provides a value.
func resolveFallbackProviders(overrides ...[]config.FallbackProviderEntry) []config.FallbackProviderEntry {
	var result []config.FallbackProviderEntry
	found := false
	for _, o := range overrides {
		if o != nil {
			result = o
			found = true
		}
	}
	if !found {
		return nil
	}
	if len(result) == 0 {
		return make([]config.FallbackProviderEntry, 0)
	}
	return append([]config.FallbackProviderEntry(nil), result...)
}

// resolveFullFallbackEntries looks up the full LLMProviderConfig for each
// fallback provider entry and applies agent-level native tool overrides so
// that native tool configuration survives provider swaps during fallback.
// Entries whose provider is not found in the registry are logged and skipped
// (startup validation should have caught these).
func resolveFullFallbackEntries(cfg *config.Config, entries []config.FallbackProviderEntry, agentNativeTools map[config.GoogleNativeTool]bool) []ResolvedFallbackEntry {
	if len(entries) == 0 {
		return nil
	}
	resolved := make([]ResolvedFallbackEntry, 0, len(entries))
	for _, entry := range entries {
		provider, err := cfg.GetLLMProvider(entry.Provider)
		if err != nil {
			slog.Warn("Fallback provider not found in registry (skipping)",
				"provider", entry.Provider, "error", err)
			continue
		}
		resolved = append(resolved, ResolvedFallbackEntry{
			ProviderName: entry.Provider,
			Backend:      entry.Backend,
			Config:       applyAgentNativeTools(provider, agentNativeTools),
		})
	}
	return resolved
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
