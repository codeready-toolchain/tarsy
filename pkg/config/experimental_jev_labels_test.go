package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestExperimentalJEVLabelsYAMLLoading(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	tests := []struct {
		name string
		yaml string
		want *ExperimentalJEVLabelsConfig
	}{
		{name: "omitted", yaml: "{}"},
		{name: "empty block", yaml: "system:\n  experimental_jev_labels: {}"},
		{name: "disabled", yaml: "system:\n  experimental_jev_labels:\n    enabled: false\n    timeout: -1s\n    min_confidence: .nan"},
		{
			name: "enabled defaults without API key",
			yaml: "system:\n  experimental_jev_labels:\n    enabled: true",
			want: &ExperimentalJEVLabelsConfig{
				Enabled: true, Model: "jev-1.13.0", APIKeyEnv: "TYPESAFE_API_KEY",
				Timeout: 5 * time.Second, MinConfidence: 0.9,
			},
		},
		{
			name: "overrides with explicit zero confidence",
			yaml: "system:\n  experimental_jev_labels:\n    enabled: true\n    model: ' custom-model '\n    api_key_env: ' CUSTOM_JEV_KEY '\n    timeout: 2s\n    min_confidence: 0",
			want: &ExperimentalJEVLabelsConfig{
				Enabled: true, Model: "custom-model", APIKeyEnv: "CUSTOM_JEV_KEY",
				Timeout: 2 * time.Second, MinConfidence: 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "tarsy.yaml"), []byte(tt.yaml), 0644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "llm-providers.yaml"), []byte("llm_providers: {}\n"), 0644))
			cfg, err := load(t.Context(), dir)
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.ExperimentalJEVLabels)
			assert.NoError(t, NewValidator(cfg).validateExperimentalJEVLabels())
		})
	}
}

func TestExperimentalJEVLabelsYAMLValidation(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		err  string
	}{
		{"explicit zero timeout", "timeout: 0s", "system.experimental_jev_labels.timeout must be positive and at most 30s"},
		{"whitespace model", "model: ' '", "system.experimental_jev_labels.model must be nonempty and trimmed"},
		{"whitespace key env", "api_key_env: ' '", "system.experimental_jev_labels.api_key_env must be nonempty and trimmed"},
		{"nan confidence", "min_confidence: .nan", "system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw TarsyYAMLConfig
			require.NoError(t, yaml.Unmarshal([]byte("system:\n  experimental_jev_labels:\n    enabled: true\n    "+tt.yaml), &raw))
			cfg := &Config{ExperimentalJEVLabels: resolveExperimentalJEVLabelsConfig(raw.System)}
			require.EqualError(t, NewValidator(cfg).validateExperimentalJEVLabels(), tt.err)
		})
	}
	t.Run("inline key rejected without echoing secret", func(t *testing.T) {
		var raw TarsyYAMLConfig
		err := yaml.Unmarshal([]byte("system:\n  experimental_jev_labels:\n    enabled: true\n    api_key: test-secret"), &raw)
		require.EqualError(t, err, `unknown field "api_key"`)
	})
}

func TestValidateExperimentalJEVLabels(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ExperimentalJEVLabelsConfig)
		err    string
	}{
		{name: "valid defaults"},
		{name: "disabled ignores values", change: func(c *ExperimentalJEVLabelsConfig) { *c = ExperimentalJEVLabelsConfig{} }},
		{name: "confidence lower bound", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = 0 }},
		{name: "confidence upper bound", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = 1 }},
		{name: "maximum timeout", change: func(c *ExperimentalJEVLabelsConfig) { c.Timeout = 30 * time.Second }},
		{
			name: "negative timeout", change: func(c *ExperimentalJEVLabelsConfig) { c.Timeout = -time.Second },
			err: "system.experimental_jev_labels.timeout must be positive and at most 30s",
		},
		{
			name: "excessive timeout", change: func(c *ExperimentalJEVLabelsConfig) { c.Timeout = 30*time.Second + time.Nanosecond },
			err: "system.experimental_jev_labels.timeout must be positive and at most 30s",
		},
		{
			name: "negative confidence", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = -0.01 },
			err: "system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1",
		},
		{
			name: "confidence above one", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = 1.01 },
			err: "system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1",
		},
		{
			name: "infinite confidence", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = math.Inf(1) },
			err: "system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1",
		},
		{
			name: "negative infinite confidence", change: func(c *ExperimentalJEVLabelsConfig) { c.MinConfidence = math.Inf(-1) },
			err: "system.experimental_jev_labels.min_confidence must be a finite number between 0 and 1",
		},
		{
			name: "empty model", change: func(c *ExperimentalJEVLabelsConfig) { c.Model = "" },
			err: "system.experimental_jev_labels.model must be nonempty and trimmed",
		},
		{
			name: "empty key env", change: func(c *ExperimentalJEVLabelsConfig) { c.APIKeyEnv = "" },
			err: "system.experimental_jev_labels.api_key_env must be nonempty and trimmed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := resolveExperimentalJEVLabelsConfig(&SystemYAMLConfig{ExperimentalJEVLabels: &ExperimentalJEVLabelsYAMLConfig{Enabled: true}})
			if tt.change != nil {
				tt.change(cfg)
			}
			err := NewValidator(&Config{ExperimentalJEVLabels: cfg}).validateExperimentalJEVLabels()
			if tt.err == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.err)
			}
		})
	}
}

func TestExperimentalJEVLabelsValidateAll(t *testing.T) {
	cfg := &Config{
		Queue:                 DefaultQueueConfig(),
		AgentRegistry:         NewAgentRegistry(map[string]*AgentConfig{}),
		MCPServerRegistry:     NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry:   NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		ChainRegistry:         NewChainRegistry(map[string]*ChainConfig{}),
		ExperimentalJEVLabels: &ExperimentalJEVLabelsConfig{Enabled: true},
	}
	require.EqualError(t, NewValidator(cfg).ValidateAll(), "experimental Jev labels validation failed: system.experimental_jev_labels.model must be nonempty and trimmed")
}
