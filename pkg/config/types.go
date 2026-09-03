package config

import (
	"cmp"
	"errors"
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
	URL               string            `yaml:"url,omitempty"`
	BearerToken       string            `yaml:"bearer_token,omitempty"`
	VerifySSL         *bool             `yaml:"verify_ssl,omitempty"`
	Timeout           int               `yaml:"timeout,omitempty"`             // In seconds
	CustomHeaders     map[string]string `yaml:"custom_headers,omitempty"`      // Per-request headers; {{.SESSION_ID}} = agent execution ID
	SessionCleanupURL string            `yaml:"session_cleanup_url,omitempty"` // Optional DELETE URL; {{.SESSION_ID}} = agent execution ID
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
const DefaultSizeThresholdTokens = 5000

// SummarizationConfig defines when and how to summarize large MCP responses.
// Enabled is a *bool: nil means "use default" (enabled), explicit false disables.
// LLMProvider / LLMBackend are optional; unset means the calling agent's model.
// On defaults.summarization only provider/backend are valid — enablement and
// size thresholds stay per-MCP-server.
type SummarizationConfig struct {
	Enabled              *bool      `yaml:"enabled,omitempty"`
	SizeThresholdTokens  int        `yaml:"size_threshold_tokens,omitempty" validate:"omitempty,min=100"`
	SummaryMaxTokenLimit int        `yaml:"summary_max_token_limit,omitempty" validate:"omitempty,min=50"`
	LLMProvider          string     `yaml:"llm_provider,omitempty"`
	LLMBackend           LLMBackend `yaml:"llm_backend,omitempty"`
	// Named catalog list for the summarization-local walk. Valid on
	// defaults.summarization only; rejected on per-MCP-server blocks.
	FallbackList string `yaml:"fallback_list,omitempty"`
}

// SummarizationDisabled returns true only when Enabled is explicitly set to false.
func (c *SummarizationConfig) SummarizationDisabled() bool {
	return c.Enabled != nil && !*c.Enabled
}

// BoolPtr returns a pointer to b. Convenience for *bool struct fields.
func BoolPtr(b bool) *bool { return &b }

// StageAgentConfig represents an agent reference with stage-level overrides
// Used in stage.agents[] array (even for single-agent stages)
// Parallel execution occurs when: len(agents) > 1 OR replicas > 1
type StageAgentConfig struct {
	Name          string       `yaml:"name" validate:"required"`
	Type          AgentType    `yaml:"type,omitempty"`
	LLMProvider   string       `yaml:"llm_provider,omitempty"`
	LLMBackend    LLMBackend   `yaml:"llm_backend,omitempty"`
	MaxIterations *int         `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	MCPServers    []string     `yaml:"mcp_servers,omitempty"`
	SubAgents     SubAgentRefs `yaml:"sub_agents,omitempty"`
	// Named catalog list for this stage-agent. Expanded at resolve time.
	// Empty / omitted inherits the next less-specific layer.
	FallbackList string `yaml:"fallback_list,omitempty"`
	// Stage-agent fallback providers override.
	// Deprecated: use fallback_lists + fallback_list. Still honored when
	// fallback_list is unset. Mixing both on this node is a load-time error.
	FallbackProviders []FallbackProviderEntry `yaml:"fallback_providers,omitempty"`
	// RequiredSkills and Skills are additive with the agent definition (merged at resolve time, deduplicated).
	RequiredSkills []string `yaml:"required_skills,omitempty"`
	Skills         []string `yaml:"skills,omitempty"`
}

// SubAgentRef is a reference to a sub-agent with optional per-reference overrides.
// Same override fields as StageAgentConfig, minus SubAgents (nesting forbidden).
type SubAgentRef struct {
	Name          string     `yaml:"name" validate:"required"`
	LLMProvider   string     `yaml:"llm_provider,omitempty"`
	LLMBackend    LLMBackend `yaml:"llm_backend,omitempty"`
	MaxIterations *int       `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	MCPServers    []string   `yaml:"mcp_servers,omitempty"`
	// Named catalog list for this dispatch. Empty / omitted inherits
	// defaults → chain → defaults.agents.<name> (empty stage).
	FallbackList string `yaml:"fallback_list,omitempty"`
	// RequiredSkills and Skills merge with the agent definition for this dispatch only
	// (same additive rules as StageAgentConfig). Parent stage required_skills do not apply.
	RequiredSkills []string `yaml:"required_skills,omitempty"`
	Skills         []string `yaml:"skills,omitempty"`
}

// SubAgentRefs is a list of sub-agent references that supports both short-form
// (list of strings) and long-form (list of objects with overrides) in YAML.
type SubAgentRefs []SubAgentRef

// subAgentRefAllowedKeys are the YAML keys accepted in a SubAgentRef mapping.
// Kept in sync with the struct tags on SubAgentRef.
var subAgentRefAllowedKeys = map[string]bool{
	"name":            true,
	"llm_provider":    true,
	"llm_backend":     true,
	"max_iterations":  true,
	"mcp_servers":     true,
	"fallback_list":   true,
	"required_skills": true,
	"skills":          true,
}

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
			if node.Tag != "!!str" {
				return fmt.Errorf("sub_agents[%d]: expected string, got %s", i, node.Tag)
			}
			refs = append(refs, SubAgentRef{Name: node.Value})
		case yaml.MappingNode:
			if err := checkUnknownKeysWithHints(node, subAgentRefAllowedKeys, pairingKeyHints, fmt.Sprintf("sub_agents[%d]", i)); err != nil {
				return err
			}
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

// pairingKeyHints maps catalog/inline short keys onto pairing-site names.
var pairingKeyHints = map[string]string{
	"provider": "llm_provider",
	"backend":  "llm_backend",
}

// checkUnknownKeysWithHints validates that a MappingNode contains only keys in
// the allowed set. MappingNode.Content alternates key, value, key, value, ...
// hints, when set, turn a rejected key into `unknown field "x" (did you mean "y"?)`.
func checkUnknownKeysWithHints(node *yaml.Node, allowed map[string]bool, hints map[string]string, prefix string) error {
	for j := 0; j < len(node.Content)-1; j += 2 {
		key := node.Content[j].Value
		if allowed[key] {
			continue
		}
		msg := fmt.Sprintf("unknown field %q", key)
		if want, ok := hints[key]; ok {
			msg = fmt.Sprintf("unknown field %q (did you mean %q?)", key, want)
		}
		if prefix != "" {
			return fmt.Errorf("%s: %s", prefix, msg)
		}
		return errors.New(msg)
	}
	return nil
}

func decodeMapping(node *yaml.Node, allowed map[string]bool, hints map[string]string, dest any) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("must be a mapping, got %v", node.Tag)
	}
	if err := checkUnknownKeysWithHints(node, allowed, hints, ""); err != nil {
		return err
	}
	return node.Decode(dest)
}

// FallbackProviderEntry is one item in a fallback walk (in-memory).
// Deprecated fallback_providers YAML uses provider/backend. Catalog YAML uses
// llm_provider/llm_backend (catalogFallbackEntry). Omitted backend is langchain
// (see ResolvedBackend).
type FallbackProviderEntry struct {
	Provider string     `yaml:"provider" validate:"required"`
	Backend  LLMBackend `yaml:"backend,omitempty"`
}

var inlineFallbackAllowedKeys = map[string]bool{
	"provider": true,
	"backend":  true,
}

var inlineFallbackKeyHints = map[string]string{
	"llm_provider": "provider",
	"llm_backend":  "backend",
}

// UnmarshalYAML accepts only provider/backend (deprecated inline lists).
func (e *FallbackProviderEntry) UnmarshalYAML(value *yaml.Node) error {
	type raw FallbackProviderEntry
	return decodeMapping(value, inlineFallbackAllowedKeys, inlineFallbackKeyHints, (*raw)(e))
}

var catalogFallbackAllowedKeys = map[string]bool{
	"llm_provider": true,
	"llm_backend":  true,
}

// catalogFallbackEntry is the YAML form of one fallback_lists item.
type catalogFallbackEntry struct {
	LLMProvider string     `yaml:"llm_provider"`
	LLMBackend  LLMBackend `yaml:"llm_backend,omitempty"`
}

func (e *catalogFallbackEntry) UnmarshalYAML(value *yaml.Node) error {
	type raw catalogFallbackEntry
	return decodeMapping(value, catalogFallbackAllowedKeys, pairingKeyHints, (*raw)(e))
}

func (e catalogFallbackEntry) toEntry() FallbackProviderEntry {
	return FallbackProviderEntry{Provider: e.LLMProvider, Backend: e.LLMBackend}
}

// catalogFallbackLists is the YAML form of top-level fallback_lists.
type catalogFallbackLists map[string][]catalogFallbackEntry

func (lists catalogFallbackLists) toEntries() map[string][]FallbackProviderEntry {
	if lists == nil {
		return nil
	}
	out := make(map[string][]FallbackProviderEntry, len(lists))
	for name, entries := range lists {
		converted := make([]FallbackProviderEntry, len(entries))
		for i, e := range entries {
			converted[i] = e.toEntry()
		}
		out[name] = converted
	}
	return out
}

// ResolvedBackend returns the entry's backend, or DefaultLLMBackend when omitted.
func (e FallbackProviderEntry) ResolvedBackend() LLMBackend {
	return cmp.Or(e.Backend, DefaultLLMBackend)
}

// SynthesisConfig defines synthesis agent configuration
type SynthesisConfig struct {
	Agent        string     `yaml:"agent,omitempty"`
	LLMBackend   LLMBackend `yaml:"llm_backend,omitempty"`
	LLMProvider  string     `yaml:"llm_provider,omitempty"`
	FallbackList string     `yaml:"fallback_list,omitempty"`
}

// ChatConfig defines chat agent configuration
type ChatConfig struct {
	Enabled       bool         `yaml:"enabled"`
	Agent         string       `yaml:"agent,omitempty"`
	LLMBackend    LLMBackend   `yaml:"llm_backend,omitempty"`
	LLMProvider   string       `yaml:"llm_provider,omitempty"`
	MCPServers    []string     `yaml:"mcp_servers,omitempty"`
	MaxIterations *int         `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	SubAgents     SubAgentRefs `yaml:"sub_agents,omitempty"`
	FallbackList  string       `yaml:"fallback_list,omitempty"`
}

// ScoringConfig defines scoring agent configuration for session quality evaluation
type ScoringConfig struct {
	Enabled       bool       `yaml:"enabled"`
	Agent         string     `yaml:"agent,omitempty"`
	LLMBackend    LLMBackend `yaml:"llm_backend,omitempty"`
	LLMProvider   string     `yaml:"llm_provider,omitempty"`
	MCPServers    []string   `yaml:"mcp_servers,omitempty"`
	MaxIterations *int       `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
	FallbackList  string     `yaml:"fallback_list,omitempty"`
}

// EmbeddingProviderType identifies the embedding API provider.
type EmbeddingProviderType string

// Known embedding provider types.
const (
	EmbeddingProviderGoogle EmbeddingProviderType = "google"
	EmbeddingProviderOpenAI EmbeddingProviderType = "openai"
)

// IsValid returns true for known embedding provider types.
func (p EmbeddingProviderType) IsValid() bool {
	switch p {
	case EmbeddingProviderGoogle, EmbeddingProviderOpenAI:
		return true
	default:
		return false
	}
}

// MemoryConfig defines investigation memory configuration.
type MemoryConfig struct {
	Enabled              bool            `yaml:"enabled"`
	MaxInject            int             `yaml:"max_inject,omitempty"`
	ReflectorMemoryLimit int             `yaml:"reflector_memory_limit,omitempty"`
	Embedding            EmbeddingConfig `yaml:"embedding,omitempty"`
}

// EmbeddingConfig defines embedding model configuration.
type EmbeddingConfig struct {
	Provider   EmbeddingProviderType `yaml:"provider,omitempty"`
	Model      string                `yaml:"model,omitempty"`
	APIKeyEnv  string                `yaml:"api_key_env,omitempty"`
	Dimensions int                   `yaml:"dimensions,omitempty"`
	BaseURL    string                `yaml:"base_url,omitempty"`
}
