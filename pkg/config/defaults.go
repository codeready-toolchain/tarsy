package config

// Defaults contains system-wide default configurations
// These values are used when specific components don't specify their own values
type Defaults struct {
	// LLM provider default for all agents/chains
	LLMProvider string `yaml:"llm_provider,omitempty"`

	// Max iterations default (forces conclusion when reached, no pause/resume)
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// Iteration strategy default
	IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`

	// Success policy default for parallel stages
	SuccessPolicy SuccessPolicy `yaml:"success_policy,omitempty"`

	// Default alert type for new sessions (application state default)
	AlertType string `yaml:"alert_type,omitempty"`

	// Default runbook content for new sessions (application state default)
	Runbook string `yaml:"runbook,omitempty"`

	// Alert data masking configuration
	AlertMasking *AlertMaskingDefaults `yaml:"alert_masking,omitempty"`
}

// AlertMaskingDefaults holds alert payload masking settings.
// Applied system-wide to all alert data before DB storage.
type AlertMaskingDefaults struct {
	Enabled      bool   `yaml:"enabled"`
	PatternGroup string `yaml:"pattern_group"`
}
