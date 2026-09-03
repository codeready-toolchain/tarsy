package agent

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const DefaultMaxIterations = 40

// DefaultLLMBackend is the fallback when no level in the config hierarchy
// specifies an LLM backend. LangChain is the general-purpose multi-provider
// backend and matches the typical production default.
const DefaultLLMBackend = config.DefaultLLMBackend

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
// the hierarchy: defaults → agent definition → chain → defaults.agents.<name>
// → stage → stage-agent.
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

	provider, providerName, backend, err := ResolveLLMPair(cfg,
		LLMLayer{Provider: defaults.LLMProvider, Backend: defaults.LLMBackend},
		LLMLayer{Provider: agentDef.LLMProvider, Backend: agentDef.LLMBackend},
		LLMLayer{Provider: chain.LLMProvider, Backend: chain.LLMBackend},
		namedAgentLLMLayer(&defaults, agentConfig.Name),
		LLMLayer{Provider: agentConfig.LLMProvider, Backend: agentConfig.LLMBackend},
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

	// Resolve fallback providers (defaults → chain → defaults.agents.<name> → stage → agentConfig)
	fallbackProviders, err := config.ResolveFallbackLayers(cfg.FallbackLists,
		defaultsFallbackLayer(&defaults),
		chainFallbackLayer(chain),
		namedAgentFallbackLayer(&defaults, agentConfig.Name),
		config.FallbackLayer{ListName: stageConfig.FallbackList, Inline: stageConfig.FallbackProviders},
		config.FallbackLayer{ListName: agentConfig.FallbackList, Inline: agentConfig.FallbackProviders},
	)
	if err != nil {
		return nil, err
	}

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	skillAgentDef := effectiveAgentDefForSkills(agentDef, agentConfig)
	requiredSkills, onDemandSkills := resolveSkills(cfg, &skillAgentDef)

	return withPromptCaching(&ResolvedAgentConfig{
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
		RequiresNativeTools:       requiresNativeTools(agentDef.NativeTools),
		RequiredSkillContent:      requiredSkills,
		OnDemandSkills:            onDemandSkills,
	}, cfg), nil
}

// ResolveChatProviderName resolves the LLM provider name for a chat execution
// using the hierarchy: defaults → chain → defaults.agents.<chat agent> → chatCfg.
// This is extracted so the same logic can be used in error paths before full
// config resolution (e.g., for audit-trail records when ResolveChatAgentConfig fails).
func ResolveChatProviderName(defaults *config.Defaults, chain *config.ChainConfig, chatCfg *config.ChatConfig) string {
	name, _ := applyLLMLayers(
		LLMLayer{Provider: defaultsLLMProvider(defaults)},
		LLMLayer{Provider: chainLLMProvider(chain)},
		namedAgentLLMLayer(defaults, chatAgentName(chatCfg)),
		chatLLMLayer(chatCfg),
	)
	return name
}

func defaultsLLMProvider(defaults *config.Defaults) string {
	if defaults == nil {
		return ""
	}
	return defaults.LLMProvider
}

func chainLLMProvider(chain *config.ChainConfig) string {
	if chain == nil {
		return ""
	}
	return chain.LLMProvider
}

func chatLLMLayer(chatCfg *config.ChatConfig) LLMLayer {
	if chatCfg == nil {
		return LLMLayer{}
	}
	return LLMLayer{Provider: chatCfg.LLMProvider, Backend: chatCfg.LLMBackend}
}

func chatAgentName(chatCfg *config.ChatConfig) string {
	if chatCfg != nil && chatCfg.Agent != "" {
		return chatCfg.Agent
	}
	return config.AgentNameChat
}

// ResolveChatAgentConfig builds the agent configuration for a chat execution.
// Hierarchy: defaults → agent definition → chain → defaults.agents.<name> → chat config.
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

	agentName := chatAgentName(chatCfg)

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

	provider, providerName, backend, err := ResolveLLMPair(cfg,
		LLMLayer{Provider: defaults.LLMProvider, Backend: defaults.LLMBackend},
		LLMLayer{Provider: agentDef.LLMProvider, Backend: agentDef.LLMBackend},
		LLMLayer{Provider: chain.LLMProvider, Backend: chain.LLMBackend},
		namedAgentLLMLayer(&defaults, agentName),
		LLMLayer{Provider: chatProvider, Backend: chatBackend},
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

	var chatFallbackList string
	if chatCfg != nil {
		chatFallbackList = chatCfg.FallbackList
	}

	// Resolve fallback providers (defaults → chain → defaults.agents.<name> → chain.chat)
	fallbackProviders, err := config.ResolveFallbackLayers(cfg.FallbackLists,
		defaultsFallbackLayer(&defaults),
		chainFallbackLayer(chain),
		namedAgentFallbackLayer(&defaults, agentName),
		config.FallbackLayer{ListName: chatFallbackList},
	)
	if err != nil {
		return nil, err
	}

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	requiredSkills, onDemandSkills := resolveSkills(cfg, agentDef)

	return withPromptCaching(&ResolvedAgentConfig{
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
		RequiresNativeTools:       requiresNativeTools(agentDef.NativeTools),
		RequiredSkillContent:      requiredSkills,
		OnDemandSkills:            onDemandSkills,
	}, cfg), nil
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

	// Agent name: AgentNameScoring → defaults.Scoring.Agent → scoringCfg.Agent
	agentName := config.AgentNameScoring
	if defaults.Scoring != nil && defaults.Scoring.Agent != "" {
		agentName = defaults.Scoring.Agent
	}
	if scoringCfg != nil && scoringCfg.Agent != "" {
		agentName = scoringCfg.Agent
	}

	// Get agent definition (built-in or user-defined)
	agentDef, err := cfg.GetAgent(agentName)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentName, err)
	}

	// Extract optional overrides from defaults.Scoring and scoringCfg
	var defaultsScoringBackend config.LLMBackend
	var defaultsScoringProvider string
	if defaults.Scoring != nil {
		defaultsScoringBackend = defaults.Scoring.LLMBackend
		defaultsScoringProvider = defaults.Scoring.LLMProvider
	}

	var scoringBackend config.LLMBackend
	var scoringProvider string
	var scoringMaxIter *int
	if scoringCfg != nil {
		scoringBackend = scoringCfg.LLMBackend
		scoringProvider = scoringCfg.LLMProvider
		scoringMaxIter = scoringCfg.MaxIterations
	}

	// chain.LLMBackend is excluded: it targets investigation agents.
	// A chain llm_provider without a scoring-level backend therefore
	// pairs with langchain.
	provider, providerName, backend, err := ResolveLLMPair(cfg,
		LLMLayer{Provider: defaults.LLMProvider, Backend: defaults.LLMBackend},
		LLMLayer{Provider: agentDef.LLMProvider, Backend: agentDef.LLMBackend},
		LLMLayer{Provider: defaultsScoringProvider, Backend: defaultsScoringBackend},
		LLMLayer{Provider: chain.LLMProvider},
		LLMLayer{Provider: scoringProvider, Backend: scoringBackend},
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

	var defaultsScoringFallback string
	if defaults.Scoring != nil {
		defaultsScoringFallback = defaults.Scoring.FallbackList
	}
	var scoringFallback string
	if scoringCfg != nil {
		scoringFallback = scoringCfg.FallbackList
	}

	// Resolve fallback providers (defaults → chain → defaults.scoring → chain.scoring)
	fallbackProviders, err := config.ResolveFallbackLayers(cfg.FallbackLists,
		defaultsFallbackLayer(&defaults),
		chainFallbackLayer(chain),
		config.FallbackLayer{ListName: defaultsScoringFallback},
		config.FallbackLayer{ListName: scoringFallback},
	)
	if err != nil {
		return nil, err
	}

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return withPromptCaching(&ResolvedAgentConfig{
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
		RequiresNativeTools:       requiresNativeTools(agentDef.NativeTools),
	}, cfg), nil
}

// ResolveExecSummaryConfig builds the agent configuration for an executive summary execution.
// Hierarchy: defaults → agent definition → chain → defaults.executive_summary_*
// → chain.executive_summary_*.
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

	provider, providerName, backend, err := ResolveLLMPair(cfg,
		LLMLayer{Provider: defaults.LLMProvider, Backend: defaults.LLMBackend},
		LLMLayer{Provider: agentDef.LLMProvider, Backend: agentDef.LLMBackend},
		LLMLayer{Provider: chain.LLMProvider, Backend: chain.LLMBackend},
		LLMLayer{Provider: defaults.ExecutiveSummaryProvider, Backend: defaults.ExecutiveSummaryBackend},
		LLMLayer{Provider: chain.ExecutiveSummaryProvider, Backend: chain.ExecutiveSummaryBackend},
	)
	if err != nil {
		return nil, err
	}

	// Resolve max iterations (defaults → agentDef → chain).
	// Exec summary is a single-shot call; max_iterations from the chain is advisory only.
	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations, chain.MaxIterations, nil,
	)

	fallbackProviders, err := config.ResolveFallbackLayers(cfg.FallbackLists,
		defaultsFallbackLayer(&defaults),
		chainFallbackLayer(chain),
		config.FallbackLayer{ListName: defaults.ExecutiveSummaryFallbackList},
		config.FallbackLayer{ListName: chain.ExecutiveSummaryFallbackList},
	)
	if err != nil {
		return nil, err
	}

	// Apply agent-level native tools override (provider → agent merge)
	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)

	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return withPromptCaching(&ResolvedAgentConfig{
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
		RequiresNativeTools:       requiresNativeTools(agentDef.NativeTools),
	}, cfg), nil
}

// ResolveComposeConfig builds the agent configuration for a compose execution.
// Provider order (last non-empty wins): defaults.llm_provider → chain.llm_provider
// → defaults.compose_provider/backend → chain.compose_provider/backend.
// defaults.compose_provider beats chain.llm_provider so a mid-tier compose default
// is not overridden by a chain's investigation model.
func ResolveComposeConfig(
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

	agentDef, err := cfg.GetAgent(config.AgentNameCompose)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", config.AgentNameCompose, err)
	}

	provider, providerName, backend, err := ResolveLLMPair(cfg,
		LLMLayer{Provider: defaults.LLMProvider, Backend: defaults.LLMBackend},
		LLMLayer{Provider: agentDef.LLMProvider, Backend: agentDef.LLMBackend},
		LLMLayer{Provider: chain.LLMProvider, Backend: chain.LLMBackend},
		LLMLayer{Provider: defaults.ComposeProvider, Backend: defaults.ComposeBackend},
		LLMLayer{Provider: chain.ComposeProvider, Backend: chain.ComposeBackend},
	)
	if err != nil {
		return nil, err
	}

	maxIter := resolveMaxIterations(
		defaults.MaxIterations, agentDef.MaxIterations, chain.MaxIterations, nil,
	)

	fallbackProviders, err := config.ResolveFallbackLayers(cfg.FallbackLists,
		defaultsFallbackLayer(&defaults),
		chainFallbackLayer(chain),
		config.FallbackLayer{ListName: defaults.ComposeFallbackList},
		config.FallbackLayer{ListName: chain.ComposeFallbackList},
	)
	if err != nil {
		return nil, err
	}

	resolvedProvider := applyAgentNativeTools(provider, agentDef.NativeTools)
	resolvedFallback := resolveFullFallbackEntries(cfg, fallbackProviders, agentDef.NativeTools)

	return withPromptCaching(&ResolvedAgentConfig{
		AgentName:                 config.AgentNameCompose,
		Type:                      config.AgentTypeCompose,
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
		RequiresNativeTools:       requiresNativeTools(agentDef.NativeTools),
	}, cfg), nil
}

// requiresNativeTools returns true when the agent definition declares at least
// one enabled native tool. Used to set RequiresNativeTools on ResolvedAgentConfig.
func requiresNativeTools(agentTools map[config.GoogleNativeTool]bool) bool {
	for _, enabled := range agentTools {
		if enabled {
			return true
		}
	}
	return false
}

// withPromptCaching copies cluster-wide system.prompt_caching.enabled onto the
// resolved config. Omitted / nil means enabled (same omit-means-on as YAML).
func withPromptCaching(resolved *ResolvedAgentConfig, cfg *config.Config) *ResolvedAgentConfig {
	resolved.PromptCachingEnabled = promptCachingEnabledFrom(cfg)
	return resolved
}

func promptCachingEnabledFrom(cfg *config.Config) bool {
	if cfg == nil || cfg.PromptCaching == nil {
		return true
	}
	return cfg.PromptCaching.Enabled
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

// LLMLayer is one config level in provider/backend pairing.
// A layer that names a provider without a sibling backend resolves
// backend to langchain and does not inherit a parent backend.
type LLMLayer struct {
	Provider string
	Backend  config.LLMBackend
}

func namedAgentLLMLayer(defaults *config.Defaults, name string) LLMLayer {
	pairing := defaults.AgentPairing(name)
	return LLMLayer{Provider: pairing.LLMProvider, Backend: pairing.LLMBackend}
}

func namedAgentFallbackLayer(defaults *config.Defaults, name string) config.FallbackLayer {
	return config.FallbackLayer{ListName: defaults.AgentPairing(name).FallbackList}
}

func defaultsFallbackLayer(defaults *config.Defaults) config.FallbackLayer {
	if defaults == nil {
		return config.FallbackLayer{}
	}
	return config.FallbackLayer{ListName: defaults.FallbackList, Inline: defaults.FallbackProviders}
}

func chainFallbackLayer(chain *config.ChainConfig) config.FallbackLayer {
	if chain == nil {
		return config.FallbackLayer{}
	}
	return config.FallbackLayer{ListName: chain.FallbackList, Inline: chain.FallbackProviders}
}

// ResolveSummarizationFallback expands defaults.summarization.fallback_list.
// Returns nil when the selector is unset (caller should use the agent's list).
func ResolveSummarizationFallback(cfg *config.Config) []ResolvedFallbackEntry {
	if cfg == nil || cfg.Defaults == nil || cfg.Defaults.Summarization == nil ||
		cfg.Defaults.Summarization.FallbackList == "" {
		return nil
	}
	entries, err := config.ExpandFallbackLayer(cfg.FallbackLists, config.FallbackLayer{
		ListName: cfg.Defaults.Summarization.FallbackList,
	})
	if err != nil {
		slog.Warn("Summarization fallback list could not be expanded",
			"list", cfg.Defaults.Summarization.FallbackList, "error", err)
		return []ResolvedFallbackEntry{}
	}
	return resolveFullFallbackEntries(cfg, entries, nil)
}

// applyLLMLayers walks layers lowest-to-highest and returns the resolved pair.
func applyLLMLayers(layers ...LLMLayer) (providerName string, backend config.LLMBackend) {
	backend = DefaultLLMBackend
	for _, layer := range layers {
		if layer.Provider != "" {
			providerName = layer.Provider
			backend = cmp.Or(layer.Backend, DefaultLLMBackend)
		} else if layer.Backend != "" {
			backend = layer.Backend
		}
	}
	return providerName, backend
}

// ResolveLLMPair walks layers (lowest to highest) and looks up the provider.
// On lookup failure, name and backend from the walk are still returned.
func ResolveLLMPair(cfg *config.Config, layers ...LLMLayer) (*config.LLMProviderConfig, string, config.LLMBackend, error) {
	name, backend := applyLLMLayers(layers...)
	if cfg == nil {
		return nil, name, backend, fmt.Errorf("LLM provider %q not found: config is nil", name)
	}
	provider, err := cfg.GetLLMProvider(name)
	if err != nil {
		return nil, name, backend, fmt.Errorf("LLM provider %q not found: %w", name, err)
	}
	return provider, name, backend, nil
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
			Backend:      entry.ResolvedBackend(),
			Config:       applyAgentNativeTools(provider, agentNativeTools),
		})
	}
	return resolved
}

// effectiveAgentDefForSkills returns a shallow copy of agentDef with RequiredSkills and Skills
// merged from stage-level overrides (additive, deduplicated). When agentDef.Skills is nil
// (all registry skills on-demand), stage skills do not change the allowlist.
func effectiveAgentDefForSkills(agentDef *config.AgentConfig, stageAgent config.StageAgentConfig) config.AgentConfig {
	out := *agentDef
	out.RequiredSkills = dedupeStringsInOrder(append(slices.Clone(agentDef.RequiredSkills), stageAgent.RequiredSkills...))

	if len(stageAgent.Skills) == 0 {
		return out
	}
	if agentDef.Skills == nil {
		return out
	}
	merged := dedupeStringsInOrder(append(slices.Clone(*agentDef.Skills), stageAgent.Skills...))
	out.Skills = &merged
	return out
}

func dedupeStringsInOrder(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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

// resolveSkills determines which skills an agent gets, split into required
// (injected into prompt) and on-demand (available via load_skill tool).
//
// Logic:
//   - RequiredSkills are resolved independently (always from registry, regardless of Skills allowlist)
//   - Skills allowlist nil → all registry skills on-demand (minus required)
//   - Skills allowlist empty → no on-demand skills (required still resolve)
//   - Skills allowlist non-nil → only those skills on-demand (minus required)
func resolveSkills(cfg *config.Config, agentDef *config.AgentConfig) ([]ResolvedSkill, []SkillCatalogEntry) {
	registry := cfg.SkillRegistry
	if registry == nil || registry.Len() == 0 {
		return nil, nil
	}

	// Resolve required skills (independent of on-demand allowlist)
	requiredSet := make(map[string]struct{}, len(agentDef.RequiredSkills))
	var required []ResolvedSkill
	for _, name := range agentDef.RequiredSkills {
		requiredSet[name] = struct{}{}
		skill, err := registry.Get(name)
		if err != nil {
			continue
		}
		required = append(required, ResolvedSkill{
			Name: skill.Name,
			Body: skill.Body,
		})
	}

	// Determine on-demand names from allowlist
	var onDemandNames []string
	if agentDef.Skills == nil {
		onDemandNames = registry.Names()
	} else {
		onDemandNames = *agentDef.Skills
	}

	// Build on-demand catalog (excluding required skills)
	var onDemand []SkillCatalogEntry
	for _, name := range onDemandNames {
		if _, isRequired := requiredSet[name]; isRequired {
			continue
		}
		skill, err := registry.Get(name)
		if err != nil {
			continue
		}
		onDemand = append(onDemand, SkillCatalogEntry{
			Name:        skill.Name,
			Description: skill.Description,
		})
	}

	return required, onDemand
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
