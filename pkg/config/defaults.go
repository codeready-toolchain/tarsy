package config

// Defaults contains system-wide default configurations
// These values are used when specific components don't specify their own values
type Defaults struct {
	// LLM provider default for all agents/chains
	LLMProvider string `yaml:"llm_provider,omitempty"`

	// Max iterations default (forces conclusion when reached, no pause/resume)
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// LLM backend default
	LLMBackend LLMBackend `yaml:"llm_backend,omitempty"`

	// Ordered list of fallback providers to try when the primary provider fails
	FallbackProviders []FallbackProviderEntry `yaml:"fallback_providers,omitempty"`

	// Default scoring configuration for all chains.
	// Chains with an explicit scoring: block are not affected.
	// Provides defaults for enabled, agent, llm_provider, llm_backend, etc.
	Scoring *ScoringConfig `yaml:"scoring,omitempty"`

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
