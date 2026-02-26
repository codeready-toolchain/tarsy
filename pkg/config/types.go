package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Shared types used across configuration structs

// TransportConfig defines MCP server transport configuration
type TransportConfig struct {
	Type TransportType `yaml:"type" validate:"required"`

	// For stdio transport
	Command string            `yaml:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"` // Environment overrides for stdio subprocess

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

// DefaultSizeThresholdTokens is the default token count above which MCP
// responses are summarized (when summarization is enabled).
const DefaultSizeThresholdTokens = 10000

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
	Name          string       `yaml:"name" validate:"required"`
	LLMProvider   string       `yaml:"llm_provider,omitempty"`
	LLMBackend    LLMBackend   `yaml:"llm_backend,omitempty"`
	MaxIterations *int         `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	MCPServers    []string     `yaml:"mcp_servers,omitempty"`
	SubAgents     SubAgentRefs `yaml:"sub_agents,omitempty"`
}

// SubAgentRef is a reference to a sub-agent with optional per-reference overrides.
// Same override fields as StageAgentConfig, minus SubAgents (nesting forbidden).
type SubAgentRef struct {
	Name          string     `yaml:"name" validate:"required"`
	LLMProvider   string     `yaml:"llm_provider,omitempty"`
	LLMBackend    LLMBackend `yaml:"llm_backend,omitempty"`
	MaxIterations *int       `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	MCPServers    []string   `yaml:"mcp_servers,omitempty"`
}

// SubAgentRefs is a list of sub-agent references that supports both short-form
// (list of strings) and long-form (list of objects with overrides) in YAML.
type SubAgentRefs []SubAgentRef

// UnmarshalYAML implements custom unmarshaling to support both:
//   - Short-form:  [LogAnalyzer, GeneralWorker]
//   - Long-form:   [{name: LogAnalyzer, max_iterations: 5}, ...]
//   - Mixed:       [LogAnalyzer, {name: GeneralWorker, llm_provider: fast}]
func (r *SubAgentRefs) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("sub_agents must be a sequence, got %v", value.Tag)
	}
	refs := make(SubAgentRefs, 0, len(value.Content))
	for i, node := range value.Content {
		switch node.Kind {
		case yaml.ScalarNode:
			refs = append(refs, SubAgentRef{Name: node.Value})
		case yaml.MappingNode:
			var ref SubAgentRef
			if err := node.Decode(&ref); err != nil {
				return fmt.Errorf("sub_agents[%d]: %w", i, err)
			}
			refs = append(refs, ref)
		default:
			return fmt.Errorf("sub_agents[%d]: expected string or mapping, got %v", i, node.Tag)
		}
	}
	*r = refs
	return nil
}

// Names returns the agent names from all refs. Returns nil when the receiver is nil,
// preserving the "nil = use full registry" semantic in SubAgentRegistry.Filter.
func (r SubAgentRefs) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r))
	for i, ref := range r {
		names[i] = ref.Name
	}
	return names
}

// SynthesisConfig defines synthesis agent configuration
type SynthesisConfig struct {
	Agent       string     `yaml:"agent,omitempty"`
	LLMBackend  LLMBackend `yaml:"llm_backend,omitempty"`
	LLMProvider string     `yaml:"llm_provider,omitempty"`
}

// ChatConfig defines chat agent configuration
type ChatConfig struct {
	Enabled       bool       `yaml:"enabled"`
	Agent         string     `yaml:"agent,omitempty"`
	LLMBackend    LLMBackend `yaml:"llm_backend,omitempty"`
	LLMProvider   string     `yaml:"llm_provider,omitempty"`
	MCPServers    []string   `yaml:"mcp_servers,omitempty"`
	MaxIterations *int       `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}

// ScoringConfig defines scoring agent configuration for session quality evaluation
type ScoringConfig struct {
	Enabled       bool       `yaml:"enabled"`
	Agent         string     `yaml:"agent,omitempty"`
	LLMBackend    LLMBackend `yaml:"llm_backend,omitempty"`
	LLMProvider   string     `yaml:"llm_provider,omitempty"`
	MCPServers    []string   `yaml:"mcp_servers,omitempty"`
	MaxIterations *int       `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}
