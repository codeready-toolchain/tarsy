package config

import (
	"fmt"
	"os"
)

// ConfigValidator validates configuration comprehensively with clear error messages
type ConfigValidator struct {
	cfg *Config
}

// NewValidator creates a validator for the given configuration
func NewValidator(cfg *Config) *ConfigValidator {
	return &ConfigValidator{cfg: cfg}
}

// ValidateAll performs comprehensive validation (fail-fast - stops at first error)
func (v *ConfigValidator) ValidateAll() error {
	// Validate in order: agents → chains → MCP servers → LLM providers
	// This ensures dependencies are validated before dependents

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

func (v *ConfigValidator) validateAgents() error {
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

func (v *ConfigValidator) validateChains() error {
	for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
		// Validate alert_types is not empty
		if len(chain.AlertTypes) == 0 {
			return NewValidationError("chain", chainID, "alert_types", fmt.Errorf("at least one alert type required"))
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
			if chain.Chat.Agent != "" && !v.cfg.AgentRegistry.Has(chain.Chat.Agent) {
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
	}

	return nil
}

func (v *ConfigValidator) validateStage(chainID string, stageIndex int, stage *StageConfig) error {
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

func (v *ConfigValidator) validateMCPServers() error {
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

func (v *ConfigValidator) validateLLMProviders() error {
	for name, provider := range v.cfg.LLMProviderRegistry.GetAll() {
		// Validate provider type
		if !provider.Type.IsValid() {
			return NewValidationError("llm_provider", name, "type", fmt.Errorf("invalid provider type: %s", provider.Type))
		}

		// Validate model is not empty
		if provider.Model == "" {
			return NewValidationError("llm_provider", name, "model", fmt.Errorf("model required"))
		}

		// Validate API key environment variable is set (if specified)
		if provider.APIKeyEnv != "" {
			if value := os.Getenv(provider.APIKeyEnv); value == "" {
				return NewValidationError("llm_provider", name, "api_key_env", fmt.Errorf("environment variable %s is not set", provider.APIKeyEnv))
			}
		}

		// Validate VertexAI-specific fields
		if provider.Type == LLMProviderTypeVertexAI {
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
