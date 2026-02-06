package config

// Shared types used across configuration structs

// TransportConfig defines MCP server transport configuration
type TransportConfig struct {
	Type TransportType `yaml:"type" validate:"required"`

	// For stdio transport
	Command string   `yaml:"command,omitempty"`
	Args    []string `yaml:"args,omitempty"`

	// For http/sse transport
	URL         string `yaml:"url,omitempty"`
	BearerToken string `yaml:"bearer_token,omitempty"`
	VerifySSL   *bool  `yaml:"verify_ssl,omitempty"`
	Timeout     int    `yaml:"timeout,omitempty"` // In seconds
}

// MaskingConfig defines data masking configuration for MCP servers
type MaskingConfig struct {
	Enabled        bool             `yaml:"enabled"`
	PatternGroups  []string         `yaml:"pattern_groups,omitempty"`
	Patterns       []string         `yaml:"patterns,omitempty"`
	CustomPatterns []MaskingPattern `yaml:"custom_patterns,omitempty"`
}

// MaskingPattern defines a regex-based masking pattern
type MaskingPattern struct {
	Pattern     string `yaml:"pattern" validate:"required"`
	Replacement string `yaml:"replacement" validate:"required"`
	Description string `yaml:"description,omitempty"`
}

// SummarizationConfig defines when and how to summarize large MCP responses
type SummarizationConfig struct {
	Enabled              bool `yaml:"enabled"`
	SizeThresholdTokens  int  `yaml:"size_threshold_tokens,omitempty" validate:"omitempty,min=100"`
	SummaryMaxTokenLimit int  `yaml:"summary_max_token_limit,omitempty" validate:"omitempty,min=50"`
}

// StageAgentConfig represents an agent reference with stage-level overrides
// Used in stage.agents[] array (even for single-agent stages)
// Parallel execution occurs when: len(agents) > 1 OR replicas > 1
type StageAgentConfig struct {
	Name              string            `yaml:"name" validate:"required"`
	LLMProvider       string            `yaml:"llm_provider,omitempty"`
	IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
	MaxIterations     *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	MCPServers        []string          `yaml:"mcp_servers,omitempty"`
}

// SynthesisConfig defines synthesis agent configuration
type SynthesisConfig struct {
	Agent             string            `yaml:"agent,omitempty"`
	IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
	LLMProvider       string            `yaml:"llm_provider,omitempty"`
}

// ChatConfig defines chat agent configuration
type ChatConfig struct {
	Enabled           bool              `yaml:"enabled"`
	Agent             string            `yaml:"agent,omitempty"`
	IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
	LLMProvider       string            `yaml:"llm_provider,omitempty"`
	MCPServers        []string          `yaml:"mcp_servers,omitempty"`
	MaxIterations     *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}
