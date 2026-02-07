package config

import (
	"fmt"
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

	if err := v.validateMCPServers(); err != nil {
		return fmt.Errorf("MCP server validation failed: %w", err)
	}

	if err := v.validateLLMProviders(); err != nil {
		return fmt.Errorf("LLM provider validation failed: %w", err)
	}

	if err := v.validateChains(); err != nil {
		return fmt.Errorf("chain validation failed: %w", err)
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

func (v *Validator) validateAgents() error {
	for name, agent := range v.cfg.AgentRegistry.GetAll() {
		// Validate MCP servers exist
		if len(agent.MCPServers) == 0 {
			return NewValidationError("agent", name, "mcp_servers", fmt.Errorf("at least one MCP server required"))
		}

		for _, serverID := range agent.MCPServers {
			if !v.cfg.MCPServerRegistry.Has(serverID) {
				return NewValidationError("agent", name, "mcp_servers", fmt.Errorf("MCP server '%s' not found", serverID))
			}
		}

		// Validate iteration strategy if specified
		if agent.IterationStrategy != "" && !agent.IterationStrategy.IsValid() {
			return NewValidationError("agent", name, "iteration_strategy", fmt.Errorf("invalid strategy: %s", agent.IterationStrategy))
		}

		// Validate max iterations if specified
		if agent.MaxIterations != nil && *agent.MaxIterations < 1 {
			return NewValidationError("agent", name, "max_iterations", fmt.Errorf("must be at least 1"))
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

			// Validate chat iteration strategy if specified
			if chain.Chat.IterationStrategy != "" && !chain.Chat.IterationStrategy.IsValid() {
				return NewValidationError("chain", chainID, "chat.iteration_strategy", fmt.Errorf("invalid strategy: %s", chain.Chat.IterationStrategy))
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

		// Validate chain-level LLM provider if specified
		if chain.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(chain.LLMProvider) {
			return NewValidationError("chain", chainID, "llm_provider", fmt.Errorf("LLM provider '%s' not found", chain.LLMProvider))
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

		// Validate agent-level iteration strategy if specified
		if agentConfig.IterationStrategy != "" && !agentConfig.IterationStrategy.IsValid() {
			return fmt.Errorf("%s: agent '%s' has invalid iteration_strategy: %s", stageRef, agentConfig.Name, agentConfig.IterationStrategy)
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

		// Validate synthesis iteration strategy if specified
		if stage.Synthesis.IterationStrategy != "" && !stage.Synthesis.IterationStrategy.IsValid() {
			return fmt.Errorf("%s: synthesis has invalid iteration_strategy: %s", stageRef, stage.Synthesis.IterationStrategy)
		}

		// Validate synthesis LLM provider if specified
		if stage.Synthesis.LLMProvider != "" && !v.cfg.LLMProviderRegistry.Has(stage.Synthesis.LLMProvider) {
			return fmt.Errorf("%s: synthesis specifies LLM provider '%s' which is not found", stageRef, stage.Synthesis.LLMProvider)
		}
	}

	return nil
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
		if server.Summarization != nil && server.Summarization.Enabled {
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

		// Only validate API key environment variable for providers that are actually referenced
		if referencedProviders[name] {
			if provider.APIKeyEnv != "" {
				if value := os.Getenv(provider.APIKeyEnv); value == "" {
					return NewValidationError("llm_provider", name, "api_key_env", fmt.Errorf("environment variable %s is not set", provider.APIKeyEnv))
				}
			}
		}

		// Validate VertexAI-specific fields (only for referenced providers)
		if referencedProviders[name] && provider.Type == LLMProviderTypeVertexAI {
			if provider.ProjectEnv != "" {
				if value := os.Getenv(provider.ProjectEnv); value == "" {
					return NewValidationError("llm_provider", name, "project_env", fmt.Errorf("environment variable %s is not set", provider.ProjectEnv))
				}
			}
			if provider.LocationEnv != "" {
				if value := os.Getenv(provider.LocationEnv); value == "" {
					return NewValidationError("llm_provider", name, "location_env", fmt.Errorf("environment variable %s is not set", provider.LocationEnv))
				}
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

	// If no chain registry exists, no providers are referenced
	if v.cfg.ChainRegistry == nil {
		return referenced
	}

	for _, chain := range v.cfg.ChainRegistry.GetAll() {
		// Chain-level LLM provider
		if chain.LLMProvider != "" {
			referenced[chain.LLMProvider] = true
		}

		// Chat-level LLM provider
		if chain.Chat != nil && chain.Chat.LLMProvider != "" {
			referenced[chain.Chat.LLMProvider] = true
		}

		// Stage-level LLM providers
		for _, stage := range chain.Stages {
			// Stage agent-level LLM providers
			for _, agent := range stage.Agents {
				if agent.LLMProvider != "" {
					referenced[agent.LLMProvider] = true
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
