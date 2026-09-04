package config

import "gopkg.in/yaml.v3"

// Defaults contains system-wide default configurations
// These values are used when specific components don't specify their own values
type Defaults struct {
	// LLM provider default for all agents/chains
	LLMProvider string `yaml:"llm_provider,omitempty"`

	// Compose (amended-report) pairing. Beats chain.llm_provider.
	// Deprecated aliases: compose_provider / compose_backend / compose_fallback_list.
	Compose *JobPairing `yaml:"compose,omitempty"`

	// Executive summary pairing. Beats chain.llm_provider.
	// Deprecated aliases: executive_summary_provider / executive_summary_backend /
	// executive_summary_fallback_list.
	ExecutiveSummary *JobPairing `yaml:"executive_summary,omitempty"`

	// Max iterations default (forces conclusion when reached, no pause/resume)
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// LLM backend default
	LLMBackend LLMBackend `yaml:"llm_backend,omitempty"`

	// Named catalog list to use as the default fallback walk. Expanded at
	// resolve time. Empty / omitted inherits nothing (this is the root layer).
	FallbackList string `yaml:"fallback_list,omitempty"`

	// Ordered list of fallback providers to try when the primary provider fails.
	// Deprecated: use fallback_lists + fallback_list. Still honored when
	// fallback_list is unset. Mixing both on this node is a load-time error.
	FallbackProviders []FallbackProviderEntry `yaml:"fallback_providers,omitempty"`

	// Default scoring configuration for all chains.
	// Chains with an explicit scoring: block are not affected.
	// Provides defaults for enabled, agent, llm_provider, llm_backend, etc.
	Scoring *ScoringConfig `yaml:"scoring,omitempty"`

	// Default tool-result summarization provider/backend. Only llm_provider
	// and llm_backend are inherited; enablement and size thresholds stay
	// per-MCP-server. Unset means the calling agent's model.
	Summarization *SummarizationConfig `yaml:"summarization,omitempty"`

	// Success policy default for parallel stages
	SuccessPolicy SuccessPolicy `yaml:"success_policy,omitempty"`

	// Default alert type for new sessions (application state default)
	AlertType string `yaml:"alert_type,omitempty"`

	// Default runbook content for new sessions (application state default)
	Runbook string `yaml:"runbook,omitempty"`

	// Alert data masking configuration
	AlertMasking *AlertMaskingDefaults `yaml:"alert_masking,omitempty"`

	// Global orchestrator defaults (applied to all orchestrator agents unless overridden)
	Orchestrator *OrchestratorConfig `yaml:"orchestrator,omitempty"`

	// Investigation memory configuration
	Memory *MemoryConfig `yaml:"memory,omitempty"`

	// Named-agent pairing (pair + list only). Keyed by registry name.
	// Does not replace agent identity (tools, instructions). Unknown names fail load.
	Agents map[string]NamedAgentPairing `yaml:"agents,omitempty"`
}

// UnmarshalYAML accepts nested compose / executive_summary blocks and the
// deprecated compose_* / executive_summary_* keys (migrated into the blocks).
func (d *Defaults) UnmarshalYAML(value *yaml.Node) error {
	type raw Defaults
	aux := struct {
		*raw              `yaml:",inline"`
		deprecatedJobKeys `yaml:",inline"`
	}{raw: (*raw)(d)}
	if err := value.Decode(&aux); err != nil {
		return err
	}
	return applyDeprecatedJobPairings(&d.Compose, &d.ExecutiveSummary, aux.deprecatedJobKeys, value)
}

// NamedAgentPairing is provider/backend/list for defaults.agents.<name>.
// Identity stays in agents: / Go builtins. Chain/stage/ref that names the agent wins when set.
type NamedAgentPairing struct {
	LLMProvider  string     `yaml:"llm_provider,omitempty"`
	LLMBackend   LLMBackend `yaml:"llm_backend,omitempty"`
	FallbackList string     `yaml:"fallback_list,omitempty"`
}

var llmPairingAllowedKeys = map[string]bool{
	"llm_provider":  true,
	"llm_backend":   true,
	"fallback_list": true,
}

// UnmarshalYAML rejects unknown keys (e.g. catalog `backend` instead of `llm_backend`).
func (p *NamedAgentPairing) UnmarshalYAML(value *yaml.Node) error {
	type raw NamedAgentPairing
	return decodeMapping(value, llmPairingAllowedKeys, pairingKeyHints, (*raw)(p))
}

// AgentPairing returns defaults.agents[name]. The zero value means inherit.
func (d *Defaults) AgentPairing(name string) NamedAgentPairing {
	if d == nil || d.Agents == nil {
		return NamedAgentPairing{}
	}
	return d.Agents[name]
}

// AlertMaskingDefaults holds alert payload masking settings.
// Applied system-wide to all alert data before DB storage.
type AlertMaskingDefaults struct {
	Enabled      bool   `yaml:"enabled"`
	PatternGroup string `yaml:"pattern_group"`
}

// DefaultEmbeddingConfig returns the built-in embedding configuration.
func DefaultEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		Provider:   EmbeddingProviderGoogle,
		Model:      "gemini-embedding-2-preview",
		APIKeyEnv:  "GOOGLE_API_KEY",
		Dimensions: 768,
	}
}

// ResolvedMemoryConfig returns the memory config with defaults applied.
// Returns nil if memory is not configured or disabled.
func ResolvedMemoryConfig(defaults *Defaults) *MemoryConfig {
	if defaults == nil || defaults.Memory == nil || !defaults.Memory.Enabled {
		return nil
	}
	mc := *defaults.Memory

	if mc.MaxInject == 0 {
		mc.MaxInject = 5
	}
	if mc.ReflectorMemoryLimit == 0 {
		mc.ReflectorMemoryLimit = 20
	}

	defaultEmb := DefaultEmbeddingConfig()
	if mc.Embedding.Provider == "" {
		mc.Embedding.Provider = defaultEmb.Provider
	}
	if mc.Embedding.Model == "" {
		mc.Embedding.Model = defaultEmb.Model
	}
	if mc.Embedding.APIKeyEnv == "" {
		mc.Embedding.APIKeyEnv = defaultEmb.APIKeyEnv
	}
	if mc.Embedding.Dimensions == 0 {
		mc.Embedding.Dimensions = defaultEmb.Dimensions
	}

	return &mc
}
