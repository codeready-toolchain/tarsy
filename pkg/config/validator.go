package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
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
	// Validate in order: queue → agents → MCP servers → LLM providers → chains
	// This ensures dependencies are validated before dependents

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

	if err := v.validateLLMProviders(); err != nil {
		return fmt.Errorf("LLM provider validation failed: %w", err)
	}

	if err := v.validateChains(); err != nil {
		return fmt.Errorf("chain validation failed: %w", err)
	}

	if err := v.validateDefaults(); err != nil {
		return fmt.Errorf("defaults validation failed: %w", err)
	}

	if err := v.validateRunbooks(); err != nil {
		return fmt.Errorf("runbooks validation failed: %w", err)
	}

	if err := v.validateSlack(); err != nil {
		return fmt.Errorf("slack validation failed: %w", err)
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
		if defaults.Scoring.MaxIterations != nil && *defaults.Scoring.MaxIterations < 1 {
			return NewValidationError("defaults", "", "scoring.max_iterations",
				fmt.Errorf("must be at least 1"))
		}
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

	return nil
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

		// Orchestrator config only valid on orchestrator agents
		if agent.Orchestrator != nil && agent.Type != AgentTypeOrchestrator {
			return NewValidationError("agent", name, "orchestrator", fmt.Errorf("orchestrator config only valid on orchestrator agents"))
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

			// Validate chat max iterations if specified
			if chain.Chat.MaxIterations != nil && *chain.Chat.MaxIterations < 1 {
				return NewValidationError("chain", chainID, "chat.max_iterations", fmt.Errorf("must be at least 1"))
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

		// Validate agent-level LLM backend if specified
		if agentConfig.LLMBackend != "" && !agentConfig.LLMBackend.IsValid() {
			return fmt.Errorf("%s: agent '%s' has invalid llm_backend: %s", stageRef, agentConfig.Name, agentConfig.LLMBackend)
		}

		// Validate agent-level LLM provider if specified
		if agentConfig.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(agentConfig.LLMProvider) {
			return fmt.Errorf("%s: agent '%s' specifies LLM provider '%s' which is not found", stageRef, agentConfig.Name, agentConfig.LLMProvider)
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

		// Validate summarization configuration
		if server.Summarization != nil && !server.Summarization.SummarizationDisabled() {
			if server.Summarization.SizeThresholdTokens < 100 {
				return NewValidationError("mcp_server", serverID, "summarization.size_threshold_tokens", fmt.Errorf("must be at least 100"))
			}
			if server.Summarization.SummaryMaxTokenLimit > 0 && server.Summarization.SummaryMaxTokenLimit < 50 {
				return NewValidationError("mcp_server", serverID, "summarization.summary_max_token_limit", fmt.Errorf("must be at least 50 if specified"))
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

		// Validate max tool result tokens
		if provider.MaxToolResultTokens < 1000 {
			return NewValidationError("llm_provider", name, "max_tool_result_tokens", fmt.Errorf("must be at least 1000"))
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

// collectReferencedLLMProviders returns a set of LLM provider names that are actually referenced by chains
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

		// Chat-level LLM provider
		if chain.Chat != nil && chain.Chat.LLMProvider != "" {
			referenced[chain.Chat.LLMProvider] = true
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

	return referenced
}

func (v *Validator) validateOrchestratorConfig(oc *OrchestratorConfig, section, name string) error {
	if oc.MaxConcurrentAgents != nil && *oc.MaxConcurrentAgents < 1 {
		return NewValidationError(section, name, "orchestrator.max_concurrent_agents", fmt.Errorf("must be at least 1"))
	}
	if oc.AgentTimeout != nil && *oc.AgentTimeout <= 0 {
		return NewValidationError(section, name, "orchestrator.agent_timeout", fmt.Errorf("must be positive"))
	}
	if oc.MaxBudget != nil && *oc.MaxBudget <= 0 {
		return NewValidationError(section, name, "orchestrator.max_budget", fmt.Errorf("must be positive"))
	}
	return nil
}

func (v *Validator) validateSubAgentRefs(subAgents SubAgentRefs, section, name, field string) error {
	for _, ref := range subAgents {
		if !v.cfg.AgentRegistry.Has(ref.Name) {
			return NewValidationError(section, name, field, fmt.Errorf("agent '%s' not found", ref.Name))
		}
		agentDef, _ := v.cfg.AgentRegistry.Get(ref.Name)
		if agentDef.Type == AgentTypeOrchestrator {
			return NewValidationError(section, name, field, fmt.Errorf("agent '%s' is an orchestrator and cannot be a sub-agent", ref.Name))
		}
		if ref.LLMBackend != "" && !ref.LLMBackend.IsValid() {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' has invalid llm_backend: %s", ref.Name, ref.LLMBackend))
		}
		if ref.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(ref.LLMProvider) {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' specifies LLM provider '%s' which is not found", ref.Name, ref.LLMProvider))
		}
		if ref.MaxIterations != nil && *ref.MaxIterations < 1 {
			return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' max_iterations must be at least 1", ref.Name))
		}
		for _, serverID := range ref.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return NewValidationError(section, name, field, fmt.Errorf("sub-agent '%s' specifies MCP server '%s' which is not found", ref.Name, serverID))
			}
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

		// Provider must exist in the registry
		if !v.cfg.LLMProviderRegistry.Has(entry.Provider) {
			return NewValidationError(section, name, entryRef,
				fmt.Errorf("LLM provider '%s' not found", entry.Provider))
		}

		// Backend must be valid
		if !entry.Backend.IsValid() {
			return NewValidationError(section, name, entryRef,
				fmt.Errorf("invalid LLM backend: %s", entry.Backend))
		}

		// Credentials must be set
		provider, _ := v.cfg.LLMProviderRegistry.Get(entry.Provider)
		if missing := missingProviderEnvVar(provider); missing != "" {
			return NewValidationError(section, name, entryRef,
				fmt.Errorf("environment variable %s is not set (required by fallback provider '%s')",
					missing, entry.Provider))
		}
	}
	return nil
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

func (v *Validator) validateSkills() error {
	registry := v.cfg.SkillRegistry
	if registry == nil {
		registry = NewSkillRegistry(nil)
	}

	agents := v.cfg.AgentRegistry.GetAll()
	for name, agent := range agents {
		// Validate Skills allowlist references
		var allowedSkills map[string]struct{}
		if agent.Skills != nil {
			allowedSkills = make(map[string]struct{}, len(*agent.Skills))
			for _, skillName := range *agent.Skills {
				if _, dup := allowedSkills[skillName]; dup {
					return NewValidationError("agent", name, "skills",
						fmt.Errorf("duplicate skill %q", skillName))
				}
				allowedSkills[skillName] = struct{}{}
				if !registry.Has(skillName) {
					return NewValidationError("agent", name, "skills",
						fmt.Errorf("%w: %s", ErrSkillNotFound, skillName))
				}
			}
		}

		// Validate RequiredSkills references
		seenRequired := make(map[string]struct{}, len(agent.RequiredSkills))
		for _, skillName := range agent.RequiredSkills {
			if _, dup := seenRequired[skillName]; dup {
				return NewValidationError("agent", name, "required_skills",
					fmt.Errorf("duplicate skill %q", skillName))
			}
			seenRequired[skillName] = struct{}{}

			if !registry.Has(skillName) {
				return NewValidationError("agent", name, "required_skills",
					fmt.Errorf("%w: %s", ErrSkillNotFound, skillName))
			}

			if allowedSkills != nil {
				if _, found := allowedSkills[skillName]; !found {
					return NewValidationError("agent", name, "required_skills",
						fmt.Errorf("required skill %q is not in the skills allowlist", skillName))
				}
			}
		}
	}

	return nil
}
