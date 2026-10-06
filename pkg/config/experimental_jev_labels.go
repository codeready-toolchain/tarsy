package config

import (
	"fmt"
	"math"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ExperimentalJEVLabelsConfig configures optional shadow label evaluation.
// The API key is read from APIKeyEnv at startup, never stored in configuration.
type ExperimentalJEVLabelsConfig struct {
	Enabled       bool
	Model         string
	APIKeyEnv     string
	Timeout       time.Duration
	MinConfidence float64
}

// ExperimentalJEVLabelsYAMLConfig preserves explicit zero values for validation.
type ExperimentalJEVLabelsYAMLConfig struct {
	Enabled       bool           `yaml:"enabled"`
	Model         string         `yaml:"model,omitempty"`
	APIKeyEnv     string         `yaml:"api_key_env,omitempty"`
	Timeout       *time.Duration `yaml:"timeout,omitempty"`
	MinConfidence *float64       `yaml:"min_confidence,omitempty"`
}

// UnmarshalYAML rejects unknown keys, including inline API keys.
func (c *ExperimentalJEVLabelsYAMLConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw ExperimentalJEVLabelsYAMLConfig
	return decodeMapping(value, map[string]bool{
		"enabled": true, "model": true, "api_key_env": true,
		"timeout": true, "min_confidence": true,
	}, nil, (*raw)(c))
}

func resolveExperimentalJEVLabelsConfig(sys *SystemYAMLConfig) *ExperimentalJEVLabelsConfig {
	if sys == nil || sys.ExperimentalJEVLabels == nil || !sys.ExperimentalJEVLabels.Enabled {
		return nil
	}
	raw := sys.ExperimentalJEVLabels
	cfg := &ExperimentalJEVLabelsConfig{
		Enabled:       true,
		Model:         "jev-1.13.0",
		APIKeyEnv:     "TYPESAFE_API_KEY",
		Timeout:       5 * time.Second,
		MinConfidence: 0.9,
	}
	if raw.Model != "" {
		cfg.Model = strings.TrimSpace(raw.Model)
	}
	if raw.APIKeyEnv != "" {
		cfg.APIKeyEnv = strings.TrimSpace(raw.APIKeyEnv)
	}
	if raw.Timeout != nil {
		cfg.Timeout = *raw.Timeout
	}
	if raw.MinConfidence != nil {
		cfg.MinConfidence = *raw.MinConfidence
	}
	return cfg
}

func (v *Validator) validateExperimentalJEVLabels() error {
	cfg := v.cfg.ExperimentalJEVLabels
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if cfg.Model == "" || cfg.Model != strings.TrimSpace(cfg.Model) {
		return fmt.Errorf("system.experimental_jev_labels.model must be nonempty and trimmed")
	}
	if cfg.APIKeyEnv == "" || cfg.APIKeyEnv != strings.TrimSpace(cfg.APIKeyEnv) {
		return fmt.Errorf("system.experimental_jev_labels.api_key_env must be nonempty and trimmed")
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 30*time.Second {
		return fmt.Errorf("system.experimental_jev_labels.timeout must be positive and at most 30s")
	}
	if math.IsNaN(cfg.MinConfidence) || math.IsInf(cfg.MinConfidence, 0) || cfg.MinConfidence < 0 || cfg.MinConfidence > 1 {
		return fmt.Errorf("system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1")
	}
	return nil
}
