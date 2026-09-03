package config

import (
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"slices"
	"time"
)

// Validator validates configuration comprehensively with clear error messages
type Validator struct {
	cfg *Config
}

// NewValidator creates a validator for the given configuration
func NewValidator(cfg *Config) *Validator {
	return &Validator{cfg: cfg}
}

// ValidateAll performs comprehensive validation (fail-fast - stops at first error)
func (v *Validator) ValidateAll() error {
	// Validate in order: queue → agents → MCP servers → fallback lists →
	// LLM providers → chains. Dependencies are validated before dependents.

	if err := v.validateQueue(); err != nil {
		return fmt.Errorf("queue validation failed: %w", err)
	}

	if err := v.validateAgents(); err != nil {
		return fmt.Errorf("agent validation failed: %w", err)
	}

	if err := v.validateSkills(); err != nil {
		return fmt.Errorf("skill validation failed: %w", err)
	}

	if err := v.validateMCPServers(); err != nil {
		return fmt.Errorf("MCP server validation failed: %w", err)
	}

	if err := v.validateNamedFallbackLists(); err != nil {
		return fmt.Errorf("fallback list validation failed: %w", err)
	}

	if err := v.validateLLMProviders(); err != nil {
		return fmt.Errorf("LLM provider validation failed: %w", err)
	}

	if err := v.validateChains(); err != nil {
		return fmt.Errorf("chain validation failed: %w", err)
	}

	v.warnNativeToolAgentsWithoutCompatibleFallback()
	v.warnDeprecatedFallbackProviders()

	if err := v.validateDefaults(); err != nil {
		return fmt.Errorf("defaults validation failed: %w", err)
	}

	if err := v.validateRunbooks(); err != nil {
		return fmt.Errorf("runbooks validation failed: %w", err)
	}

	if err := v.validateSlack(); err != nil {
		return fmt.Errorf("slack validation failed: %w", err)
	}

	if err := v.validateCostEstimation(); err != nil {
		return fmt.Errorf("cost estimation validation failed: %w", err)
	}

	return nil
}

func (v *Validator) validateQueue() error {
	q := v.cfg.Queue
	if q == nil {
		return fmt.Errorf("queue configuration is nil")
	}

	if q.WorkerCount < 1 || q.WorkerCount > 50 {
		return fmt.Errorf("worker_count must be between 1 and 50, got %d", q.WorkerCount)
	}
	if q.MaxConcurrentSessions < 1 {
		return fmt.Errorf("max_concurrent_sessions must be at least 1, got %d", q.MaxConcurrentSessions)
	}
	if q.PollInterval <= 0 {
		return fmt.Errorf("poll_interval must be positive, got %v", q.PollInterval)
	}
	if q.PollIntervalJitter < 0 {
		return fmt.Errorf("poll_interval_jitter must be non-negative, got %v", q.PollIntervalJitter)
	}
	if q.PollIntervalJitter >= q.PollInterval {
		return fmt.Errorf("poll_interval_jitter must be less than poll_interval, got jitter=%v interval=%v", q.PollIntervalJitter, q.PollInterval)
	}
	if q.SessionTimeout <= 0 {
		return fmt.Errorf("session_timeout must be positive, got %v", q.SessionTimeout)
	}
	if q.GracefulShutdownTimeout <= 0 {
		return fmt.Errorf("graceful_shutdown_timeout must be positive, got %v", q.GracefulShutdownTimeout)
	}
	if q.ScoringShutdownTimeout < 0 {
		return fmt.Errorf("scoring_shutdown_timeout must be non-negative, got %v", q.ScoringShutdownTimeout)
	}
	if q.OrphanDetectionInterval <= 0 {
		return fmt.Errorf("orphan_detection_interval must be positive, got %v", q.OrphanDetectionInterval)
	}
	if q.OrphanThreshold <= 0 {
		return fmt.Errorf("orphan_threshold must be positive, got %v", q.OrphanThreshold)
	}
	if q.HeartbeatInterval <= 0 {
		return fmt.Errorf("heartbeat_interval must be positive, got %v", q.HeartbeatInterval)
	}
	if q.HeartbeatInterval >= q.OrphanThreshold {
		return fmt.Errorf("heartbeat_interval must be less than orphan_threshold to prevent false orphan detection, got heartbeat=%v threshold=%v", q.HeartbeatInterval, q.OrphanThreshold)
	}

	return nil
}

func (v *Validator) validateDefaults() error {
	defaults := v.cfg.Defaults
	if defaults == nil {
		return nil
	}

	// Validate defaults.scoring block if specified
	if defaults.Scoring != nil {
		if defaults.Scoring.Agent != "" && !v.cfg.AgentRegistry.Has(defaults.Scoring.Agent) {
			if _, isBuiltin := GetBuiltinConfig().Agents[defaults.Scoring.Agent]; !isBuiltin {
				return NewValidationError("defaults", "", "scoring.agent",
					fmt.Errorf("agent '%s' not found", defaults.Scoring.Agent))
			}
		}
		if defaults.Scoring.LLMBackend != "" && !defaults.Scoring.LLMBackend.IsValid() {
			return NewValidationError("defaults", "", "scoring.llm_backend",
				fmt.Errorf("invalid LLM backend: %s", defaults.Scoring.LLMBackend))
		}
		if defaults.Scoring.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(defaults.Scoring.LLMProvider) {
			return NewValidationError("defaults", "", "scoring.llm_provider",
				fmt.Errorf("LLM provider '%s' not found", defaults.Scoring.LLMProvider))
		}
		if err := v.googleNativeRequiresGoogleProvider(defaults.Scoring.LLMProvider, defaults.Scoring.LLMBackend); err != nil {
			return NewValidationError("defaults", "", "scoring.llm_backend", err)
		}
		if defaults.Scoring.MaxIterations != nil && *defaults.Scoring.MaxIterations < 1 {
			return NewValidationError("defaults", "", "scoring.max_iterations",
				fmt.Errorf("must be at least 1"))
		}
	}

	if err := v.validateDefaultsSummarization(defaults.Summarization); err != nil {
		return err
	}

	if err := v.googleNativeRequiresGoogleProvider(defaults.LLMProvider, defaults.LLMBackend); err != nil {
		return NewValidationError("defaults", "", "llm_backend", err)
	}

	if err := v.validateJobProviderBackend(defaults.ComposeProvider, defaults.ComposeBackend,
		"defaults", "", "compose_provider", "compose_backend"); err != nil {
		return err
	}
	if err := v.validateJobProviderBackend(defaults.ExecutiveSummaryProvider, defaults.ExecutiveSummaryBackend,
		"defaults", "", "executive_summary_provider", "executive_summary_backend"); err != nil {
		return err
	}

	if err := v.validateNamedAgentPairings(defaults.Agents); err != nil {
		return err
	}

	// Validate fallback providers if specified
	if err := v.validateFallbackProviders(defaults.FallbackProviders, "defaults", "", "fallback_providers"); err != nil {
		return err
	}

	// Validate alert masking configuration
	if defaults.AlertMasking != nil && defaults.AlertMasking.Enabled {
		builtin := GetBuiltinConfig()
		groupName := defaults.AlertMasking.PatternGroup
		if groupName == "" {
			return NewValidationError("defaults", "", "alert_masking.pattern_group",
				fmt.Errorf("pattern_group is required when alert masking is enabled"))
		}
		if _, exists := builtin.PatternGroups[groupName]; !exists {
			return NewValidationError("defaults", "", "alert_masking.pattern_group",
				fmt.Errorf("pattern group '%s' not found in built-in groups", groupName))
		}
	}

	if defaults.Orchestrator != nil {
		if err := v.validateOrchestratorConfig(defaults.Orchestrator, "defaults", ""); err != nil {
			return err
		}
	}

	if defaults.Memory != nil && defaults.Memory.Enabled {
		if err := v.validateMemoryConfig(defaults.Memory); err != nil {
			return err
		}
		v.warnMemoryWithoutScoring(defaults)
	}

	return nil
}

func (v *Validator) validateDefaultsSummarization(sum *SummarizationConfig) error {
	if sum == nil {
		return nil
	}
	if sum.Enabled != nil {
		return NewValidationError("defaults", "", "summarization.enabled",
			fmt.Errorf("enabled is per-MCP-server only; do not set it on defaults.summarization"))
	}
	if sum.SizeThresholdTokens != 0 {
		return NewValidationError("defaults", "", "summarization.size_threshold_tokens",
			fmt.Errorf("size_threshold_tokens is per-MCP-server only; do not set it on defaults.summarization"))
	}
	if sum.SummaryMaxTokenLimit != 0 {
		return NewValidationError("defaults", "", "summarization.summary_max_token_limit",
			fmt.Errorf("summary_max_token_limit is per-MCP-server only; do not set it on defaults.summarization"))
	}
	return v.validateSummarizationLLM(sum, "defaults", "", "summarization")
}

func (v *Validator) validateNamedAgentPairings(agents map[string]NamedAgentPairing) error {
	for name, pairing := range agents {
		if v.cfg.AgentRegistry == nil || !v.cfg.AgentRegistry.Has(name) {
			return NewValidationError("defaults", name, "agents",
				fmt.Errorf("unknown agent %q", name))
		}
		if pairing.LLMBackend != "" && !pairing.LLMBackend.IsValid() {
			return NewValidationError("defaults", name, "agents.llm_backend",
				fmt.Errorf("invalid LLM backend: %s", pairing.LLMBackend))
		}
		if pairing.LLMProvider != "" && (v.cfg.LLMProviderRegistry == nil || !v.cfg.LLMProviderRegistry.Has(pairing.LLMProvider)) {
			return NewValidationError("defaults", name, "agents.llm_provider",
				fmt.Errorf("LLM provider '%s' not found", pairing.LLMProvider))
		}
		if err := v.googleNativeRequiresGoogleProvider(pairing.LLMProvider, pairing.LLMBackend); err != nil {
			return NewValidationError("defaults", name, "agents.llm_backend", err)
		}
	}
	return nil
}

// validateJobProviderBackend checks compose/exec-summary sibling backend rules.
// Backend without a sibling provider is an error (same as summarization).
func (v *Validator) validateJobProviderBackend(provider string, backend LLMBackend, section, name, providerField, backendField string) error {
	if backend != "" && provider == "" {
		return NewValidationError(section, name, backendField,
			fmt.Errorf("%s requires %s at the same level", backendField, providerField))
	}
	if backend != "" && !backend.IsValid() {
		return NewValidationError(section, name, backendField,
			fmt.Errorf("invalid LLM backend: %s", backend))
	}
	if provider != "" && (v.cfg.LLMProviderRegistry == nil || !v.cfg.LLMProviderRegistry.Has(provider)) {
		return NewValidationError(section, name, providerField,
			fmt.Errorf("LLM provider '%s' not found", provider))
	}
	if err := v.googleNativeRequiresGoogleProvider(provider, backend); err != nil {
		return NewValidationError(section, name, backendField, err)
	}
	return nil
}

// validateSummarizationLLM checks optional llm_provider / llm_backend on a
// SummarizationConfig. Backend without provider at the same level is an error.
func (v *Validator) validateSummarizationLLM(sum *SummarizationConfig, component, id, fieldPrefix string) error {
	if sum == nil {
		return nil
	}
	if sum.LLMBackend != "" && sum.LLMProvider == "" {
		return NewValidationError(component, id, fieldPrefix+".llm_backend",
			fmt.Errorf("llm_backend requires llm_provider at the same level"))
	}
	if sum.LLMBackend != "" && !sum.LLMBackend.IsValid() {
		return NewValidationError(component, id, fieldPrefix+".llm_backend",
			fmt.Errorf("invalid LLM backend: %s", sum.LLMBackend))
	}
	if sum.LLMProvider != "" && (v.cfg.LLMProviderRegistry == nil || !v.cfg.LLMProviderRegistry.Has(sum.LLMProvider)) {
		return NewValidationError(component, id, fieldPrefix+".llm_provider",
			fmt.Errorf("LLM provider '%s' not found", sum.LLMProvider))
	}
	if err := v.googleNativeRequiresGoogleProvider(sum.LLMProvider, sum.LLMBackend); err != nil {
		return NewValidationError(component, id, fieldPrefix+".llm_backend", err)
	}
	return nil
}

// googleNativeRequiresGoogleProvider rejects an explicit google-native backend
// paired with a non-google provider at the same YAML node. Unknown providers
// are reported elsewhere. Backend-only overrides (no sibling provider) pass.
func (v *Validator) googleNativeRequiresGoogleProvider(providerName string, backend LLMBackend) error {
	if backend != LLMBackendNativeGemini || providerName == "" {
		return nil
	}
	if v.cfg.LLMProviderRegistry == nil {
		return nil
	}
	provider, err := v.cfg.LLMProviderRegistry.Get(providerName)
	if err != nil {
		return nil
	}
	if provider.Type != LLMProviderTypeGoogle {
		return fmt.Errorf("llm_backend %q requires a google provider, got type %q for %q", backend, provider.Type, providerName)
	}
	return nil
}

func (v *Validator) validateMemoryConfig(mc *MemoryConfig) error {
	resolved := ResolvedMemoryConfig(&Defaults{Memory: mc})
	if resolved == nil {
		return nil
	}

	if !resolved.Embedding.Provider.IsValid() {
		return NewValidationError("defaults", "", "memory.embedding.provider",
			fmt.Errorf("invalid embedding provider: %s", resolved.Embedding.Provider))
	}
	if resolved.Embedding.Dimensions <= 0 {
		return NewValidationError("defaults", "", "memory.embedding.dimensions",
			fmt.Errorf("must be positive, got %d", resolved.Embedding.Dimensions))
	}
	if resolved.Embedding.Model == "" {
		return NewValidationError("defaults", "", "memory.embedding.model",
			fmt.Errorf("embedding model is required"))
	}
	if resolved.Embedding.APIKeyEnv == "" {
		return NewValidationError("defaults", "", "memory.embedding.api_key_env",
			fmt.Errorf("api_key_env is required"))
	}
	if os.Getenv(resolved.Embedding.APIKeyEnv) == "" {
		slog.Warn("Memory embedding API key env var is not set — embedding calls will fail at runtime",
			"env_var", resolved.Embedding.APIKeyEnv)
	}
	return nil
}

// warnMemoryWithoutScoring logs a warning if memory is enabled but no chain
// will effectively have scoring enabled. Memory extraction (Reflector) runs
// inside the scoring stage, so without scoring the memory pool will never grow
// from new investigations — only injection of existing memories will work.
//
// A chain's effective scoring state is: chain.Scoring.Enabled if chain.Scoring
// is set, otherwise defaults.Scoring.Enabled. We must check per-chain because
// a chain can explicitly disable scoring even when defaults enable it.
func (v *Validator) warnMemoryWithoutScoring(defaults *Defaults) {
	defaultScoringEnabled := defaults.Scoring != nil && defaults.Scoring.Enabled

	for _, chain := range v.cfg.ChainRegistry.GetAll() {
		if chain.Scoring != nil {
			if chain.Scoring.Enabled {
				return
			}
		} else if defaultScoringEnabled {
			return
		}
	}
	slog.Warn("Memory is enabled but no chain has scoring enabled — memory extraction (Reflector) " +
		"runs inside the scoring stage, so new memories will never be created from investigations. " +
		"Enable scoring on at least one chain, or via defaults.scoring.enabled, for memory extraction to work.")
}

func (v *Validator) validateAgents() error {
	for name, agent := range v.cfg.AgentRegistry.GetAll() {
		// MCP servers are optional — an agent may operate without tools.
		// When specified, validate that each referenced server exists.
		for _, serverID := range agent.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return NewValidationError("agent", name, "mcp_servers", fmt.Errorf("MCP server '%s' not found", serverID))
			}
		}

		// Validate agent type if specified
		if agent.Type != "" && !agent.Type.IsValid() {
			return NewValidationError("agent", name, "type", fmt.Errorf("invalid agent type: %s", agent.Type))
		}
		// type: compose is executor-only; YAML may not declare it except on the builtin.
		if agent.Type == AgentTypeCompose && name != AgentNameCompose {
			return NewValidationError("agent", name, "type", fmt.Errorf("type %q is executor-only and cannot be used in chain YAML", agent.Type))
		}

		// Validate LLM backend if specified
		if agent.LLMBackend != "" && !agent.LLMBackend.IsValid() {
			return NewValidationError("agent", name, "llm_backend", fmt.Errorf("invalid LLM backend: %s", agent.LLMBackend))
		}

		// Validate max iterations if specified
		if agent.MaxIterations != nil && *agent.MaxIterations < 1 {
			return NewValidationError("agent", name, "max_iterations", fmt.Errorf("must be at least 1"))
		}

		// Validate native tool keys if specified
		for tool := range agent.NativeTools {
			if !tool.IsValid() {
				return NewValidationError("agent", name, "native_tools", fmt.Errorf("invalid native tool: %s", tool))
			}
		}

		if agent.Orchestrator != nil {
			if err := v.validateOrchestratorConfig(agent.Orchestrator, "agent", name); err != nil {
				return err
			}
		}
	}

	return nil
}

func (v *Validator) validateChains() error {
	// Build map to ensure each alert type maps to only one chain
	alertTypeToChain := make(map[string]string)

	for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
		// Validate alert_types is not empty
		if len(chain.AlertTypes) == 0 {
			return NewValidationError("chain", chainID, "alert_types", fmt.Errorf("at least one alert type required"))
		}

		// Validate each alert type is unique across all chains
		for _, alertType := range chain.AlertTypes {
			if existingChainID, exists := alertTypeToChain[alertType]; exists {
				return NewValidationError("chain", chainID, "alert_types", fmt.Errorf("alert type '%s' is already mapped to chain '%s' (each alert type must map to exactly one chain)", alertType, existingChainID))
			}
			alertTypeToChain[alertType] = chainID
		}

		// Validate stages
		if len(chain.Stages) == 0 {
			return NewValidationError("chain", chainID, "stages", fmt.Errorf("at least one stage required"))
		}

		for i, stage := range chain.Stages {
			if err := v.validateStage(chainID, i, &stage); err != nil {
				return err
			}
		}

		// Validate chat agent if enabled
		if chain.Chat != nil && chain.Chat.Enabled {
			// Chat agent is required when chat is enabled
			if chain.Chat.Agent == "" {
				return NewValidationError("chain", chainID, "chat.agent", fmt.Errorf("chat.agent required when chat is enabled"))
			}

			if !v.cfg.AgentRegistry.Has(chain.Chat.Agent) {
				return NewValidationError("chain", chainID, "chat.agent", fmt.Errorf("agent '%s' not found", chain.Chat.Agent))
			}

			// Validate chat LLM backend if specified
			if chain.Chat.LLMBackend != "" && !chain.Chat.LLMBackend.IsValid() {
				return NewValidationError("chain", chainID, "chat.llm_backend", fmt.Errorf("invalid LLM backend: %s", chain.Chat.LLMBackend))
			}

			// Validate chat LLM provider if specified
			if chain.Chat.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(chain.Chat.LLMProvider) {
				return NewValidationError("chain", chainID, "chat.llm_provider", fmt.Errorf("LLM provider '%s' not found", chain.Chat.LLMProvider))
			}
			if err := v.googleNativeRequiresGoogleProvider(chain.Chat.LLMProvider, chain.Chat.LLMBackend); err != nil {
				return NewValidationError("chain", chainID, "chat.llm_backend", err)
			}

			// Validate chat max iterations if specified
			if chain.Chat.MaxIterations != nil && *chain.Chat.MaxIterations < 1 {
				return NewValidationError("chain", chainID, "chat.max_iterations", fmt.Errorf("must be at least 1"))
			}

			if err := v.validateSubAgentRefs(chain.Chat.SubAgents, "chain", chainID, "chat.sub_agents"); err != nil {
				return err
			}
		}

		// Validate scoring agent if enabled
		if chain.Scoring != nil && chain.Scoring.Enabled {
			scoringAgent := chain.Scoring.Agent
			if scoringAgent == "" {
				scoringAgent = AgentNameScoring
			}

			if !v.cfg.AgentRegistry.Has(scoringAgent) {
				if _, isBuiltin := GetBuiltinConfig().Agents[scoringAgent]; !isBuiltin {
					return NewValidationError("chain", chainID, "scoring.agent", fmt.Errorf("agent '%s' not found", scoringAgent))
				}
			}

			// Validate scoring LLM backend if specified
			if chain.Scoring.LLMBackend != "" && !chain.Scoring.LLMBackend.IsValid() {
				return NewValidationError("chain", chainID, "scoring.llm_backend", fmt.Errorf("invalid LLM backend: %s", chain.Scoring.LLMBackend))
			}

			// Validate scoring LLM provider if specified
			if chain.Scoring.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(chain.Scoring.LLMProvider) {
				return NewValidationError("chain", chainID, "scoring.llm_provider", fmt.Errorf("LLM provider '%s' not found", chain.Scoring.LLMProvider))
			}
			if err := v.googleNativeRequiresGoogleProvider(chain.Scoring.LLMProvider, chain.Scoring.LLMBackend); err != nil {
				return NewValidationError("chain", chainID, "scoring.llm_backend", err)
			}

			// Validate scoring max iterations if specified
			if chain.Scoring.MaxIterations != nil && *chain.Scoring.MaxIterations < 1 {
				return NewValidationError("chain", chainID, "scoring.max_iterations", fmt.Errorf("must be at least 1"))
			}

			// Validate scoring MCP servers if specified
			for _, serverID := range chain.Scoring.MCPServers {
				if !v.cfg.MCPServerRegistry.Has(serverID) {
					return NewValidationError("chain", chainID, "scoring.mcp_servers", fmt.Errorf("MCP server '%s' not found", serverID))
				}
			}
		}

		// Validate chain-level LLM provider if specified
		if chain.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(chain.LLMProvider) {
			return NewValidationError("chain", chainID, "llm_provider", fmt.Errorf("LLM provider '%s' not found", chain.LLMProvider))
		}
		if err := v.googleNativeRequiresGoogleProvider(chain.LLMProvider, chain.LLMBackend); err != nil {
			return NewValidationError("chain", chainID, "llm_backend", err)
		}

		if err := v.validateJobProviderBackend(chain.ComposeProvider, chain.ComposeBackend,
			"chain", chainID, "compose_provider", "compose_backend"); err != nil {
			return err
		}
		if err := v.validateJobProviderBackend(chain.ExecutiveSummaryProvider, chain.ExecutiveSummaryBackend,
			"chain", chainID, "executive_summary_provider", "executive_summary_backend"); err != nil {
			return err
		}

		// Validate chain-level fallback providers if specified
		if err := v.validateFallbackProviders(chain.FallbackProviders, "chain", chainID, "fallback_providers"); err != nil {
			return err
		}

		// Validate chain-level max iterations if specified
		if chain.MaxIterations != nil && *chain.MaxIterations < 1 {
			return NewValidationError("chain", chainID, "max_iterations", fmt.Errorf("must be at least 1"))
		}

		// Validate chain-level MCP servers if specified
		for _, serverID := range chain.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return NewValidationError("chain", chainID, "mcp_servers", fmt.Errorf("MCP server '%s' not found", serverID))
			}
		}

		// Validate chain-level sub_agents if specified
		if err := v.validateSubAgentRefs(chain.SubAgents, "chain", chainID, "sub_agents"); err != nil {
			return err
		}
	}

	return nil
}

func (v *Validator) validateStage(chainID string, stageIndex int, stage *StageConfig) error {
	stageRef := fmt.Sprintf("chain '%s' stage %d", chainID, stageIndex)

	// Validate stage name
	if stage.Name == "" {
		return fmt.Errorf("%s: stage name required", stageRef)
	}

	// Validate agents field (must have at least 1 agent)
	if len(stage.Agents) == 0 {
		return fmt.Errorf("%s: must specify at least one agent in 'agents' array", stageRef)
	}

	// Validate all agent references
	for _, agentConfig := range stage.Agents {
		if !v.cfg.AgentRegistry.Has(agentConfig.Name) {
			return fmt.Errorf("%s: agent '%s' not found", stageRef, agentConfig.Name)
		}

		// Validate agent-level type if specified
		if agentConfig.Type != "" && !agentConfig.Type.IsValid() {
			return fmt.Errorf("%s: agent '%s' has invalid type: %s", stageRef, agentConfig.Name, agentConfig.Type)
		}
		if agentConfig.Type == AgentTypeCompose {
			return fmt.Errorf("%s: agent '%s' has type %q which is executor-only and cannot be used in chain YAML", stageRef, agentConfig.Name, agentConfig.Type)
		}

		// Validate agent-level LLM backend if specified
		if agentConfig.LLMBackend != "" && !agentConfig.LLMBackend.IsValid() {
			return fmt.Errorf("%s: agent '%s' has invalid llm_backend: %s", stageRef, agentConfig.Name, agentConfig.LLMBackend)
		}

		// Validate agent-level LLM provider if specified
		if agentConfig.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(agentConfig.LLMProvider) {
			return fmt.Errorf("%s: agent '%s' specifies LLM provider '%s' which is not found", stageRef, agentConfig.Name, agentConfig.LLMProvider)
		}
		if err := v.googleNativeRequiresGoogleProvider(agentConfig.LLMProvider, agentConfig.LLMBackend); err != nil {
			return fmt.Errorf("%s: agent '%s': %w", stageRef, agentConfig.Name, err)
		}

		// Validate agent-level max iterations if specified
		if agentConfig.MaxIterations != nil && *agentConfig.MaxIterations < 1 {
			return fmt.Errorf("%s: agent '%s' max_iterations must be at least 1", stageRef, agentConfig.Name)
		}

		// Validate agent-level MCP servers if specified
		for _, serverID := range agentConfig.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return fmt.Errorf("%s: agent '%s' specifies MCP server '%s' which is not found", stageRef, agentConfig.Name, serverID)
			}
		}

		// Validate agent-level sub_agents if specified
		if err := v.validateSubAgentRefs(agentConfig.SubAgents, stageRef, agentConfig.Name, "sub_agents"); err != nil {
			return err
		}

		// Validate agent-level fallback providers if specified
		if err := v.validateFallbackProviders(agentConfig.FallbackProviders, stageRef, agentConfig.Name, "fallback_providers"); err != nil {
			return err
		}

		reqSkillField := fmt.Sprintf("stages[%d].agents.%s.required_skills", stageIndex, agentConfig.Name)
		if err := v.validateSkillNameList(agentConfig.RequiredSkills, "chain", chainID, reqSkillField); err != nil {
			return err
		}
		onDemandSkillField := fmt.Sprintf("stages[%d].agents.%s.skills", stageIndex, agentConfig.Name)
		if err := v.validateSkillNameList(agentConfig.Skills, "chain", chainID, onDemandSkillField); err != nil {
			return err
		}
	}

	// Warn if a stage mixes action and non-action agents
	v.warnMixedActionStage(stage, stageRef)

	// Validate stage-level fallback providers if specified
	if err := v.validateFallbackProviders(stage.FallbackProviders, stageRef, "", "fallback_providers"); err != nil {
		return err
	}

	// Validate stage-level sub_agents if specified
	if err := v.validateSubAgentRefs(stage.SubAgents, stageRef, "", "sub_agents"); err != nil {
		return err
	}

	// Validate replicas if specified
	// Note: 0 is allowed and means "use default of 1" (struct tag min=1 is documentation-only)
	if stage.Replicas < 0 {
		return fmt.Errorf("%s: replicas must be positive", stageRef)
	}

	// Validate success policy if specified
	if stage.SuccessPolicy != "" && !stage.SuccessPolicy.IsValid() {
		return fmt.Errorf("%s: invalid success_policy: %s", stageRef, stage.SuccessPolicy)
	}

	// Validate stage-level max iterations if specified
	if stage.MaxIterations != nil && *stage.MaxIterations < 1 {
		return fmt.Errorf("%s: max_iterations must be at least 1", stageRef)
	}

	// Validate synthesis agent if specified
	if stage.Synthesis != nil {
		if stage.Synthesis.Agent != "" && !v.cfg.AgentRegistry.Has(stage.Synthesis.Agent) {
			return fmt.Errorf("%s: synthesis agent '%s' not found", stageRef, stage.Synthesis.Agent)
		}

		// Validate synthesis LLM backend if specified
		if stage.Synthesis.LLMBackend != "" && !stage.Synthesis.LLMBackend.IsValid() {
			return fmt.Errorf("%s: synthesis has invalid llm_backend: %s", stageRef, stage.Synthesis.LLMBackend)
		}

		// Validate synthesis LLM provider if specified
		if stage.Synthesis.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(stage.Synthesis.LLMProvider) {
			return fmt.Errorf("%s: synthesis specifies LLM provider '%s' which is not found", stageRef, stage.Synthesis.LLMProvider)
		}
		if err := v.googleNativeRequiresGoogleProvider(stage.Synthesis.LLMProvider, stage.Synthesis.LLMBackend); err != nil {
			return fmt.Errorf("%s: synthesis: %w", stageRef, err)
		}
	}

	return nil
}

// warnMixedActionStage logs a warning when a stage has both action and non-action
// agents. The stage type will fall back to "investigation", losing action-stage
// benefits (dashboard rendering, DB queryability).
func (v *Validator) warnMixedActionStage(stg *StageConfig, stageRef string) {
	if len(stg.Agents) < 2 {
		return
	}

	hasAction, hasNonAction := false, false
	for _, ac := range stg.Agents {
		effectiveType := ac.Type
		if effectiveType == "" {
			if agentDef, err := v.cfg.AgentRegistry.Get(ac.Name); err == nil {
				effectiveType = agentDef.Type
			}
		}
		if effectiveType == AgentTypeAction {
			hasAction = true
		} else {
			hasNonAction = true
		}
	}

	if hasAction && hasNonAction {
		slog.Warn("Stage has mixed action and non-action agents — stage type will be 'investigation', action-stage benefits (dashboard, audit) will not apply",
			"stage", stageRef, "stage_name", stg.Name)
	}
}

func (v *Validator) validateMCPServers() error {
	builtin := GetBuiltinConfig()

	for serverID, server := range v.cfg.MCPServerRegistry.GetAll() {
		// Validate transport type
		if !server.Transport.Type.IsValid() {
			return NewValidationError("mcp_server", serverID, "transport.type", fmt.Errorf("invalid transport type: %s", server.Transport.Type))
		}

		// Validate transport-specific fields
		switch server.Transport.Type {
		case TransportTypeStdio:
			if server.Transport.Command == "" {
				return NewValidationError("mcp_server", serverID, "transport.command", fmt.Errorf("command required for stdio transport"))
			}

		case TransportTypeHTTP, TransportTypeSSE:
			if server.Transport.URL == "" {
				return NewValidationError("mcp_server", serverID, "transport.url", fmt.Errorf("url required for %s transport", server.Transport.Type))
			}
		}

		// Validate data masking configuration
		if server.DataMasking != nil && server.DataMasking.Enabled {
			// Validate pattern groups reference built-in patterns
			for _, groupName := range server.DataMasking.PatternGroups {
				if _, exists := builtin.PatternGroups[groupName]; !exists {
					return NewValidationError("mcp_server", serverID, "data_masking.pattern_groups", fmt.Errorf("pattern group '%s' not found", groupName))
				}
			}

			// Validate individual patterns reference built-in patterns
			for _, patternName := range server.DataMasking.Patterns {
				if _, exists := builtin.MaskingPatterns[patternName]; !exists {
					return NewValidationError("mcp_server", serverID, "data_masking.patterns", fmt.Errorf("pattern '%s' not found", patternName))
				}
			}

			// Validate custom patterns have required fields
			for i, pattern := range server.DataMasking.CustomPatterns {
				if pattern.Pattern == "" {
					return NewValidationError("mcp_server", serverID, fmt.Sprintf("data_masking.custom_patterns[%d].pattern", i), fmt.Errorf("pattern required"))
				}
				if pattern.Replacement == "" {
					return NewValidationError("mcp_server", serverID, fmt.Sprintf("data_masking.custom_patterns[%d].replacement", i), fmt.Errorf("replacement required"))
				}
			}
		}

		// Validate summarization configuration.
		// Provider/backend checks run even when summarization is disabled so
		// llm_provider on an enabled:false server is rejected as dead config.
		if server.Summarization != nil {
			if server.Summarization.SummarizationDisabled() && server.Summarization.LLMProvider != "" {
				return NewValidationError("mcp_server", serverID, "summarization.llm_provider",
					fmt.Errorf("llm_provider is unused when summarization is disabled"))
			}
			if err := v.validateSummarizationLLM(server.Summarization, "mcp_server", serverID, "summarization"); err != nil {
				return err
			}
			if server.Summarization.FallbackList != "" {
				return NewValidationError("mcp_server", serverID, "summarization.fallback_list",
					fmt.Errorf("fallback_list is only valid on defaults.summarization"))
			}
			if !server.Summarization.SummarizationDisabled() {
				if server.Summarization.SizeThresholdTokens < 100 {
					return NewValidationError("mcp_server", serverID, "summarization.size_threshold_tokens", fmt.Errorf("must be at least 100"))
				}
				if server.Summarization.SummaryMaxTokenLimit > 0 && server.Summarization.SummaryMaxTokenLimit < 50 {
					return NewValidationError("mcp_server", serverID, "summarization.summary_max_token_limit", fmt.Errorf("must be at least 50 if specified"))
				}
			}
		}
	}

	return nil
}

func (v *Validator) validateLLMProviders() error {
	// Collect all referenced LLM providers from chains
	referencedProviders := v.collectReferencedLLMProviders()

	for name, provider := range v.cfg.LLMProviderRegistry.GetAll() {
		// Validate provider type
		if !provider.Type.IsValid() {
			return NewValidationError("llm_provider", name, "type", fmt.Errorf("invalid provider type: %s", provider.Type))
		}

		// Validate model is not empty
		if provider.Model == "" {
			return NewValidationError("llm_provider", name, "model", fmt.Errorf("model required"))
		}

		// Only validate credentials for providers that are actually referenced
		if referencedProviders[name] {
			if missing := missingProviderEnvVar(provider); missing != "" {
				return NewValidationError("llm_provider", name, "credentials",
					fmt.Errorf("environment variable %s is not set", missing))
			}
		}

		// Validate native tools (Google-specific)
		if provider.Type == LLMProviderTypeGoogle && provider.NativeTools != nil {
			for tool := range provider.NativeTools {
				if !tool.IsValid() {
					return NewValidationError("llm_provider", name, "native_tools", fmt.Errorf("invalid native tool: %s", tool))
				}
			}
		}
	}

	return nil
}

// collectReferencedLLMProviders returns a set of LLM provider names referenced
// by defaults, MCP summarization overlays, and chains.
func (v *Validator) collectReferencedLLMProviders() map[string]bool {
	referenced := make(map[string]bool)

	// Default-level providers
	if v.cfg.Defaults != nil {
		if v.cfg.Defaults.LLMProvider != "" {
			referenced[v.cfg.Defaults.LLMProvider] = true
		}
		for _, fb := range v.cfg.Defaults.FallbackProviders {
			referenced[fb.Provider] = true
		}
		if v.cfg.Defaults.Scoring != nil && v.cfg.Defaults.Scoring.LLMProvider != "" {
			referenced[v.cfg.Defaults.Scoring.LLMProvider] = true
		}
		if v.cfg.Defaults.Summarization != nil && v.cfg.Defaults.Summarization.LLMProvider != "" {
			referenced[v.cfg.Defaults.Summarization.LLMProvider] = true
		}
		if v.cfg.Defaults.ComposeProvider != "" {
			referenced[v.cfg.Defaults.ComposeProvider] = true
		}
		if v.cfg.Defaults.ExecutiveSummaryProvider != "" {
			referenced[v.cfg.Defaults.ExecutiveSummaryProvider] = true
		}
		for _, pairing := range v.cfg.Defaults.Agents {
			if pairing.LLMProvider != "" {
				referenced[pairing.LLMProvider] = true
			}
		}
	}

	v.addReferencedFallbackListProviders(referenced)

	if v.cfg.MCPServerRegistry != nil {
		for _, server := range v.cfg.MCPServerRegistry.GetAll() {
			if server.Summarization != nil && server.Summarization.LLMProvider != "" {
				referenced[server.Summarization.LLMProvider] = true
			}
		}
	}

	// If no chain registry exists, no chain-level providers are referenced
	if v.cfg.ChainRegistry == nil {
		return referenced
	}

	for _, chain := range v.cfg.ChainRegistry.GetAll() {
		// Chain-level LLM provider
		if chain.LLMProvider != "" {
			referenced[chain.LLMProvider] = true
		}
		if chain.ComposeProvider != "" {
			referenced[chain.ComposeProvider] = true
		}
		if chain.ExecutiveSummaryProvider != "" {
			referenced[chain.ExecutiveSummaryProvider] = true
		}

		// Chain-level fallback providers
		for _, fb := range chain.FallbackProviders {
			referenced[fb.Provider] = true
		}

		// Chain-level sub-agent providers
		for _, ref := range chain.SubAgents {
			if ref.LLMProvider != "" {
				referenced[ref.LLMProvider] = true
			}
		}

		// Chat-level LLM provider and chat sub-agent overrides (same refs as validateSubAgentRefs(..., "chat.sub_agents"))
		if chain.Chat != nil {
			if chain.Chat.LLMProvider != "" {
				referenced[chain.Chat.LLMProvider] = true
			}
			for _, ref := range chain.Chat.SubAgents {
				if ref.LLMProvider != "" {
					referenced[ref.LLMProvider] = true
				}
			}
		}

		// Scoring-level LLM provider
		if chain.Scoring != nil && chain.Scoring.LLMProvider != "" {
			referenced[chain.Scoring.LLMProvider] = true
		}

		// Stage-level LLM providers
		for _, stage := range chain.Stages {
			// Stage-level fallback providers
			for _, fb := range stage.FallbackProviders {
				referenced[fb.Provider] = true
			}

			// Stage-level sub-agent providers
			for _, ref := range stage.SubAgents {
				if ref.LLMProvider != "" {
					referenced[ref.LLMProvider] = true
				}
			}

			// Stage agent-level LLM providers
			for _, agent := range stage.Agents {
				if agent.LLMProvider != "" {
					referenced[agent.LLMProvider] = true
				}
				// Agent-level fallback providers
				for _, fb := range agent.FallbackProviders {
					referenced[fb.Provider] = true
				}
				// Agent-level sub-agent providers
				for _, ref := range agent.SubAgents {
					if ref.LLMProvider != "" {
						referenced[ref.LLMProvider] = true
					}
				}
			}

			// Stage synthesis-level LLM provider
			if stage.Synthesis != nil && stage.Synthesis.LLMProvider != "" {
				referenced[stage.Synthesis.LLMProvider] = true
			}
		}
	}

	v.addReachableBuiltinLLMProviders(referenced)

	return referenced
}

func (v *Validator) addReachableBuiltinLLMProviders(referenced map[string]bool) {
	if v.cfg.AgentRegistry == nil || v.cfg.ChainRegistry == nil {
		return
	}
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		def, err := v.cfg.AgentRegistry.Get(name)
		if err != nil || def.LLMProvider == "" {
			return
		}
		referenced[def.LLMProvider] = true
	}
	for _, chain := range v.cfg.ChainRegistry.GetAll() {
		for _, ref := range chain.SubAgents {
			add(ref.Name)
		}
		if chain.Chat != nil {
			for _, ref := range chain.Chat.SubAgents {
				add(ref.Name)
			}
		}
		for _, stage := range chain.Stages {
			for _, ref := range stage.SubAgents {
				add(ref.Name)
			}
			for _, ag := range stage.Agents {
				add(ag.Name)
				for _, ref := range ag.SubAgents {
					add(ref.Name)
				}
			}
		}
	}
}

func (v *Validator) validateOrchestratorConfig(oc *OrchestratorConfig, section, name string) error {
	if oc.MaxConcurrentAgents != nil && *oc.MaxConcurrentAgents < 1 {
		return NewValidationError(section, name, "orchestrator.max_concurrent_agents", fmt.Errorf("must be at least 1"))
	}
	if oc.AgentTimeout != nil && *oc.AgentTimeout <= 0 {
		return NewValidationError(section, name, "orchestrator.agent_timeout", fmt.Errorf("must be positive"))
	}
	return nil
}

func (v *Validator) validateSubAgentRefs(subAgents SubAgentRefs, section, name, field string) error {
	for _, ref := range subAgents {
		if !v.cfg.AgentRegistry.Has(ref.Name) {
			return NewValidationError(section, name, field, fmt.Errorf("agent '%s' not found", ref.Name))
		}
		agentDef, _ := v.cfg.AgentRegistry.Get(ref.Name)
		if agentDef != nil && agentDef.Description == "" {
			return NewValidationError(section, name, field,
				fmt.Errorf("agent '%s' has no description (required for sub-agent catalog)", ref.Name))
		}
		if ref.LLMBackend != "" && !ref.LLMBackend.IsValid() {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' has invalid llm_backend: %s", ref.Name, ref.LLMBackend))
		}
		if ref.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(ref.LLMProvider) {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' specifies LLM provider '%s' which is not found", ref.Name, ref.LLMProvider))
		}
		if err := v.googleNativeRequiresGoogleProvider(ref.LLMProvider, ref.LLMBackend); err != nil {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s': %w", ref.Name, err))
		}
		if err := v.validateFallbackSelector(ref.FallbackList, nil, section, name,
			fmt.Sprintf("%s[%s].fallback_list", field, ref.Name)); err != nil {
			return err
		}
		if ref.MaxIterations != nil && *ref.MaxIterations < 1 {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' max_iterations must be at least 1", ref.Name))
		}
		for _, serverID := range ref.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' specifies MCP server '%s' which is not found", ref.Name, serverID))
			}
		}
		reqField := fmt.Sprintf("%s[%s].required_skills", field, ref.Name)
		if err := v.validateSkillNameList(ref.RequiredSkills, section, name, reqField); err != nil {
			return err
		}
		skillField := fmt.Sprintf("%s[%s].skills", field, ref.Name)
		if err := v.validateSkillNameList(ref.Skills, section, name, skillField); err != nil {
			return err
		}
	}
	return nil
}

// missingProviderEnvVar returns the name of the first required-but-unset
// environment variable for the given provider, or "" if all are set.
func missingProviderEnvVar(provider *LLMProviderConfig) string {
	if provider.APIKeyEnv != "" {
		if os.Getenv(provider.APIKeyEnv) == "" {
			return provider.APIKeyEnv
		}
	}
	if provider.Type == LLMProviderTypeVertexAI {
		if provider.CredentialsEnv != "" && os.Getenv(provider.CredentialsEnv) == "" {
			return provider.CredentialsEnv
		}
		if provider.ProjectEnv != "" && os.Getenv(provider.ProjectEnv) == "" {
			return provider.ProjectEnv
		}
		if provider.LocationEnv != "" && os.Getenv(provider.LocationEnv) == "" {
			return provider.LocationEnv
		}
	}
	return ""
}

func (v *Validator) validateFallbackProviders(entries []FallbackProviderEntry, section, name, field string) error {
	for i, entry := range entries {
		entryRef := fmt.Sprintf("%s[%d]", field, i)
		if err := v.validateFallbackEntryStructure(entry, section, name, entryRef); err != nil {
			return err
		}
		if err := v.validateFallbackEntryCredentials(entry, section, name, entryRef); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) validateFallbackEntryStructure(entry FallbackProviderEntry, section, name, field string) error {
	if v.cfg.LLMProviderRegistry == nil || !v.cfg.LLMProviderRegistry.Has(entry.Provider) {
		return NewValidationError(section, name, field,
			fmt.Errorf("LLM provider '%s' not found", entry.Provider))
	}
	if !entry.ResolvedBackend().IsValid() {
		return NewValidationError(section, name, field,
			fmt.Errorf("invalid LLM backend: %s", entry.Backend))
	}
	if err := v.googleNativeRequiresGoogleProvider(entry.Provider, entry.Backend); err != nil {
		return NewValidationError(section, name, field, err)
	}
	return nil
}

func (v *Validator) validateFallbackEntryCredentials(entry FallbackProviderEntry, section, name, field string) error {
	if v.cfg.LLMProviderRegistry == nil {
		return nil
	}
	provider, err := v.cfg.LLMProviderRegistry.Get(entry.Provider)
	if err != nil {
		return nil
	}
	if missing := missingProviderEnvVar(provider); missing != "" {
		return NewValidationError(section, name, field,
			fmt.Errorf("environment variable %s is not set (required by fallback provider '%s')",
				missing, entry.Provider))
	}
	return nil
}

// validateNamedFallbackLists structure-validates every catalog entry, credential-
// checks referenced lists, and rejects unknown names and mixed selector+inline
// on every selector site (defaults/chain/stage/stage-agent, job knobs,
// defaults.agents, and sub-agent refs).
func (v *Validator) validateNamedFallbackLists() error {
	referenced := v.referencedFallbackListNames()

	for listName, entries := range v.cfg.FallbackLists {
		if listName == "" {
			return NewValidationError("fallback_lists", "", "",
				fmt.Errorf("fallback list name must be non-empty"))
		}
		for i, entry := range entries {
			entryRef := fmt.Sprintf("[%d]", i)
			if err := v.validateFallbackEntryStructure(entry, "fallback_lists", listName, entryRef); err != nil {
				return err
			}
			if referenced[listName] {
				if err := v.validateFallbackEntryCredentials(entry, "fallback_lists", listName, entryRef); err != nil {
					return err
				}
			}
		}
	}

	if v.cfg.Defaults != nil {
		if err := v.validateFallbackSelector(v.cfg.Defaults.FallbackList, v.cfg.Defaults.FallbackProviders,
			"defaults", "", "fallback_list"); err != nil {
			return err
		}
		if err := v.validateFallbackSelector(v.cfg.Defaults.ComposeFallbackList, nil,
			"defaults", "", "compose_fallback_list"); err != nil {
			return err
		}
		if err := v.validateFallbackSelector(v.cfg.Defaults.ExecutiveSummaryFallbackList, nil,
			"defaults", "", "executive_summary_fallback_list"); err != nil {
			return err
		}
		if v.cfg.Defaults.Scoring != nil {
			if err := v.validateFallbackSelector(v.cfg.Defaults.Scoring.FallbackList, nil,
				"defaults", "", "scoring.fallback_list"); err != nil {
				return err
			}
		}
		if v.cfg.Defaults.Summarization != nil {
			if err := v.validateFallbackSelector(v.cfg.Defaults.Summarization.FallbackList, nil,
				"defaults", "", "summarization.fallback_list"); err != nil {
				return err
			}
		}
		for agentName, pairing := range v.cfg.Defaults.Agents {
			if err := v.validateFallbackSelector(pairing.FallbackList, nil,
				"defaults", agentName, "agents.fallback_list"); err != nil {
				return err
			}
		}
	}

	if v.cfg.ChainRegistry == nil {
		return nil
	}
	validateRefSelectors := func(refs SubAgentRefs, section, id, field string) error {
		for _, ref := range refs {
			if err := v.validateFallbackSelector(ref.FallbackList, nil, section, id,
				fmt.Sprintf("%s[%s].fallback_list", field, ref.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
		if err := v.validateFallbackSelector(chain.FallbackList, chain.FallbackProviders,
			"chain", chainID, "fallback_list"); err != nil {
			return err
		}
		if err := v.validateFallbackSelector(chain.ComposeFallbackList, nil,
			"chain", chainID, "compose_fallback_list"); err != nil {
			return err
		}
		if err := v.validateFallbackSelector(chain.ExecutiveSummaryFallbackList, nil,
			"chain", chainID, "executive_summary_fallback_list"); err != nil {
			return err
		}
		if err := validateRefSelectors(chain.SubAgents, "chain", chainID, "sub_agents"); err != nil {
			return err
		}
		if chain.Chat != nil {
			if err := v.validateFallbackSelector(chain.Chat.FallbackList, nil,
				"chain", chainID, "chat.fallback_list"); err != nil {
				return err
			}
			if err := validateRefSelectors(chain.Chat.SubAgents, "chain", chainID, "chat.sub_agents"); err != nil {
				return err
			}
		}
		if chain.Scoring != nil {
			if err := v.validateFallbackSelector(chain.Scoring.FallbackList, nil,
				"chain", chainID, "scoring.fallback_list"); err != nil {
				return err
			}
		}
		for i, stage := range chain.Stages {
			stageRef := fmt.Sprintf("chain '%s' stage %d", chainID, i)
			if err := v.validateFallbackSelector(stage.FallbackList, stage.FallbackProviders,
				stageRef, "", "fallback_list"); err != nil {
				return err
			}
			if stage.Synthesis != nil {
				if err := v.validateFallbackSelector(stage.Synthesis.FallbackList, nil,
					stageRef, "", "synthesis.fallback_list"); err != nil {
					return err
				}
			}
			if err := validateRefSelectors(stage.SubAgents, stageRef, "", "sub_agents"); err != nil {
				return err
			}
			for _, agent := range stage.Agents {
				if err := v.validateFallbackSelector(agent.FallbackList, agent.FallbackProviders,
					stageRef, agent.Name, "fallback_list"); err != nil {
					return err
				}
				if err := validateRefSelectors(agent.SubAgents, stageRef, agent.Name, "sub_agents"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (v *Validator) validateFallbackSelector(listName string, inline []FallbackProviderEntry, section, name, field string) error {
	if listName != "" && inline != nil {
		return NewValidationError(section, name, field,
			fmt.Errorf("cannot set both fallback_list and fallback_providers"))
	}
	if listName == "" {
		return nil
	}
	if _, ok := v.cfg.FallbackLists[listName]; !ok {
		return NewValidationError(section, name, field,
			fmt.Errorf("unknown fallback list %q", listName))
	}
	return nil
}

// referencedFallbackListNames returns every non-empty fallback_list /
// *_fallback_list selector. A default list named in YAML is referenced even if
// every chain overrides it.
func (v *Validator) referencedFallbackListNames() map[string]bool {
	referenced := make(map[string]bool)
	add := func(name string) {
		if name != "" {
			referenced[name] = true
		}
	}
	if v.cfg.Defaults != nil {
		add(v.cfg.Defaults.FallbackList)
		add(v.cfg.Defaults.ComposeFallbackList)
		add(v.cfg.Defaults.ExecutiveSummaryFallbackList)
		if v.cfg.Defaults.Scoring != nil {
			add(v.cfg.Defaults.Scoring.FallbackList)
		}
		if v.cfg.Defaults.Summarization != nil {
			add(v.cfg.Defaults.Summarization.FallbackList)
		}
		for _, pairing := range v.cfg.Defaults.Agents {
			add(pairing.FallbackList)
		}
	}
	if v.cfg.ChainRegistry == nil {
		return referenced
	}
	for _, chain := range v.cfg.ChainRegistry.GetAll() {
		add(chain.FallbackList)
		add(chain.ComposeFallbackList)
		add(chain.ExecutiveSummaryFallbackList)
		if chain.Chat != nil {
			add(chain.Chat.FallbackList)
			for _, ref := range chain.Chat.SubAgents {
				add(ref.FallbackList)
			}
		}
		if chain.Scoring != nil {
			add(chain.Scoring.FallbackList)
		}
		for _, ref := range chain.SubAgents {
			add(ref.FallbackList)
		}
		for _, stage := range chain.Stages {
			add(stage.FallbackList)
			if stage.Synthesis != nil {
				add(stage.Synthesis.FallbackList)
			}
			for _, ref := range stage.SubAgents {
				add(ref.FallbackList)
			}
			for _, agent := range stage.Agents {
				add(agent.FallbackList)
				for _, ref := range agent.SubAgents {
					add(ref.FallbackList)
				}
			}
		}
	}
	return referenced
}

func (v *Validator) addReferencedFallbackListProviders(referenced map[string]bool) {
	if v.cfg.FallbackLists == nil {
		return
	}
	for listName := range v.referencedFallbackListNames() {
		for _, entry := range v.cfg.FallbackLists[listName] {
			if entry.Provider != "" {
				referenced[entry.Provider] = true
			}
		}
	}
}

func (v *Validator) warnDeprecatedFallbackProviders() {
	const msg = "fallback_providers is deprecated; use fallback_lists and fallback_list"
	if v.cfg.Defaults != nil && v.cfg.Defaults.FallbackProviders != nil {
		slog.Warn(msg, "location", "defaults")
	}
	if v.cfg.ChainRegistry == nil {
		return
	}
	for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
		if chain.FallbackProviders != nil {
			slog.Warn(msg, "location", "chain", "chain", chainID)
		}
		for _, stage := range chain.Stages {
			if stage.FallbackProviders != nil {
				slog.Warn(msg, "location", "stage", "chain", chainID, "stage", stage.Name)
			}
			for _, agent := range stage.Agents {
				if agent.FallbackProviders != nil {
					slog.Warn(msg, "location", "stage-agent", "chain", chainID, "stage", stage.Name, "agent", agent.Name)
				}
			}
		}
	}
}

func (v *Validator) validateRunbooks() error {
	rb := v.cfg.Runbooks
	if rb == nil {
		return nil
	}

	if rb.CacheTTL <= 0 {
		return fmt.Errorf("system.runbooks.cache_ttl must be positive, got %v", rb.CacheTTL)
	}

	if rb.RepoURL != "" {
		if _, err := url.Parse(rb.RepoURL); err != nil {
			return fmt.Errorf("system.runbooks.repo_url is not a valid URL: %w", err)
		}
	}

	for i, domain := range rb.AllowedDomains {
		if domain == "" {
			return fmt.Errorf("system.runbooks.allowed_domains[%d] is empty", i)
		}
	}

	return nil
}

func (v *Validator) validateSlack() error {
	s := v.cfg.Slack
	if s == nil || !s.Enabled {
		return nil
	}

	if s.Channel == "" {
		return fmt.Errorf("system.slack.channel is required when Slack is enabled")
	}

	if s.TokenEnv == "" {
		return fmt.Errorf("system.slack.token_env is required when Slack is enabled")
	}

	if token := os.Getenv(s.TokenEnv); token == "" {
		return fmt.Errorf("system.slack.token_env: environment variable %s is not set", s.TokenEnv)
	}

	return nil
}

func (v *Validator) validateCostEstimation() error {
	ce := v.cfg.CostEstimation
	if ce == nil {
		return nil
	}

	for name, rate := range ce.ModelRates {
		if name == "" {
			return fmt.Errorf("system.cost_estimation.model_rates: model name must not be empty")
		}
		if err := validatePerMillionRate(rate.InputPerMillion, fmt.Sprintf("system.cost_estimation.model_rates.%s.input_per_million", name)); err != nil {
			return err
		}
		if err := validatePerMillionRate(rate.OutputPerMillion, fmt.Sprintf("system.cost_estimation.model_rates.%s.output_per_million", name)); err != nil {
			return err
		}
	}

	if err := validatePromotions(ce.Promotions); err != nil {
		return err
	}

	return nil
}

// validatePerMillionRate rejects NaN, ±Inf, and negative per-million USD rates.
func validatePerMillionRate(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be a finite number >= 0", field)
	}
	if value < 0 {
		return fmt.Errorf("%s must be >= 0", field)
	}
	return nil
}

// promoWindow is a parsed promotion used for overlap checks.
type promoWindow struct {
	index int
	model string
	start time.Time // effective start; zero = -∞
	end   time.Time
	open  bool // true when start was omitted (-∞)
}

func validatePromotions(promos []PromotionConfig) error {
	seenIDs := make(map[string]int, len(promos))
	byModel := map[string][]promoWindow{}

	for i, p := range promos {
		prefix := fmt.Sprintf("system.cost_estimation.promotions[%d]", i)
		if p.Model == "" {
			return fmt.Errorf("%s.model must not be empty", prefix)
		}
		if p.End == "" {
			return fmt.Errorf("%s.end is required", prefix)
		}
		if err := validatePerMillionRate(p.InputPerMillion, prefix+".input_per_million"); err != nil {
			return err
		}
		if err := validatePerMillionRate(p.OutputPerMillion, prefix+".output_per_million"); err != nil {
			return err
		}
		if p.ID != "" {
			if prev, dup := seenIDs[p.ID]; dup {
				return fmt.Errorf("%s.id %q duplicates promotions[%d].id", prefix, p.ID, prev)
			}
			seenIDs[p.ID] = i
		}

		start, end, err := ParsePromotionWindow(p.Start, p.End)
		if err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
		w := promoWindow{index: i, model: p.Model, end: end, open: start == nil}
		if start != nil {
			w.start = *start
			if !end.After(*start) {
				return fmt.Errorf("%s: end must be after start", prefix)
			}
		}
		byModel[p.Model] = append(byModel[p.Model], w)
	}

	for model, windows := range byModel {
		if err := checkPromoOverlaps(model, windows); err != nil {
			return err
		}
	}
	return nil
}

func checkPromoOverlaps(model string, windows []promoWindow) error {
	if len(windows) < 2 {
		return nil
	}
	// Sort by effective start: open-ended (-∞) first, then by start ascending.
	slices.SortFunc(windows, func(a, b promoWindow) int {
		if a.open != b.open {
			if a.open {
				return -1
			}
			return 1
		}
		if c := a.start.Compare(b.start); c != 0 {
			return c
		}
		return a.end.Compare(b.end)
	})

	for i := 1; i < len(windows); i++ {
		prev, cur := windows[i-1], windows[i]
		// After sorting, intervals overlap iff cur's effective start is before prev.end.
		// Two omitted starts (-∞) always overlap.
		if cur.open || cur.start.Before(prev.end) {
			return fmt.Errorf("system.cost_estimation.promotions: overlapping windows for model %q (index %d and %d)",
				model, prev.index, cur.index)
		}
	}
	return nil
}

// validateSkillNameList checks that each name exists in the skill registry and is unique within names.
func (v *Validator) validateSkillNameList(names []string, section, resourceName, field string) error {
	if len(names) == 0 {
		return nil
	}
	registry := v.cfg.SkillRegistry
	if registry == nil {
		registry = NewSkillRegistry(nil)
	}
	seen := make(map[string]struct{}, len(names))
	for _, skillName := range names {
		if _, dup := seen[skillName]; dup {
			return NewValidationError(section, resourceName, field,
				fmt.Errorf("duplicate skill %q", skillName))
		}
		seen[skillName] = struct{}{}
		if !registry.Has(skillName) {
			return NewValidationError(section, resourceName, field,
				fmt.Errorf("%w: %s", ErrSkillNotFound, skillName))
		}
	}
	return nil
}

func (v *Validator) validateSkills() error {
	agents := v.cfg.AgentRegistry.GetAll()
	for name, agent := range agents {
		if agent.Skills != nil {
			if err := v.validateSkillNameList(*agent.Skills, "agent", name, "skills"); err != nil {
				return err
			}
		}
		if err := v.validateSkillNameList(agent.RequiredSkills, "agent", name, "required_skills"); err != nil {
			return err
		}
	}

	return nil
}

// warnNativeToolAgentsWithoutCompatibleFallback checks chains for agents that
// require native tools but have no google-native fallback entry in their
// effective fallback list. Logs a warning for each such case. Non-blocking.
func (v *Validator) warnNativeToolAgentsWithoutCompatibleFallback() {
	if v.cfg.ChainRegistry == nil {
		return
	}

	var defaultsLayer FallbackLayer
	if v.cfg.Defaults != nil {
		defaultsLayer = FallbackLayer{
			ListName: v.cfg.Defaults.FallbackList,
			Inline:   v.cfg.Defaults.FallbackProviders,
		}
	}

	for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
		chainLayer := FallbackLayer{ListName: chain.FallbackList, Inline: chain.FallbackProviders}
		for _, stage := range chain.Stages {
			stageLayer := FallbackLayer{ListName: stage.FallbackList, Inline: stage.FallbackProviders}
			for _, agentCfg := range stage.Agents {
				effective, err := ResolveFallbackLayers(v.cfg.FallbackLists, defaultsLayer, chainLayer,
					namedAgentFallbackLayer(v.cfg.Defaults, agentCfg.Name),
					stageLayer,
					FallbackLayer{ListName: agentCfg.FallbackList, Inline: agentCfg.FallbackProviders})
				if err != nil {
					continue
				}
				v.warnIfNativeAgentLacksFallback(chainID, agentCfg.Name, effective)
				for _, ref := range agentCfg.SubAgents {
					v.warnNativeToolSubAgentRef(chainID, defaultsLayer, chainLayer, ref)
				}
			}
			for _, ref := range stage.SubAgents {
				v.warnNativeToolSubAgentRef(chainID, defaultsLayer, chainLayer, ref)
			}
		}

		for _, ref := range chain.SubAgents {
			v.warnNativeToolSubAgentRef(chainID, defaultsLayer, chainLayer, ref)
		}
		if chain.Chat != nil {
			for _, ref := range chain.Chat.SubAgents {
				v.warnNativeToolSubAgentRef(chainID, defaultsLayer, chainLayer, ref)
			}
		}
	}
}

func namedAgentFallbackLayer(defaults *Defaults, name string) FallbackLayer {
	return FallbackLayer{ListName: defaults.AgentPairing(name).FallbackList}
}

func (v *Validator) warnNativeToolSubAgentRef(chainID string, defaultsLayer, chainLayer FallbackLayer, ref SubAgentRef) {
	effective, err := ResolveFallbackLayers(v.cfg.FallbackLists, defaultsLayer, chainLayer,
		namedAgentFallbackLayer(v.cfg.Defaults, ref.Name),
		FallbackLayer{ListName: ref.FallbackList})
	if err != nil {
		return
	}
	v.warnIfNativeAgentLacksFallback(chainID, ref.Name, effective)
}

func (v *Validator) warnIfNativeAgentLacksFallback(chainID, agentName string, effective []FallbackProviderEntry) {
	if v.cfg.AgentRegistry == nil {
		return
	}
	agentDef, err := v.cfg.AgentRegistry.Get(agentName)
	if err != nil {
		return
	}

	if !agentHasEnabledNativeTools(agentDef.NativeTools) {
		return
	}

	if len(effective) == 0 {
		return
	}

	for _, entry := range effective {
		if entry.ResolvedBackend() == LLMBackendNativeGemini {
			return
		}
	}

	slog.Warn("Chain has native-tool agent with no compatible fallback entry",
		"chain", chainID,
		"agent", agentName,
		"hint", "all fallback entries use non-google-native backends; runtime will skip them",
	)
}

func agentHasEnabledNativeTools(nativeTools map[GoogleNativeTool]bool) bool {
	for _, enabled := range nativeTools {
		if enabled {
			return true
		}
	}
	return false
}
