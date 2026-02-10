package masking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

func TestCompileBuiltinPatterns(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	// All 15 built-in patterns should compile successfully
	builtin := config.GetBuiltinConfig()
	assert.Equal(t, len(builtin.MaskingPatterns), len(svc.patterns),
		"All built-in patterns should compile (no custom patterns with empty registry)")

	// Each compiled pattern should have a valid regex
	for name, cp := range svc.patterns {
		assert.NotNil(t, cp.Regex, "Pattern %s should have compiled regex", name)
		assert.NotEmpty(t, cp.Replacement, "Pattern %s should have replacement", name)
	}
}

func TestCompileCustomPatterns(t *testing.T) {
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": {
			Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
			DataMasking: &config.MaskingConfig{
				Enabled: true,
				CustomPatterns: []config.MaskingPattern{
					{
						Pattern:     `CUSTOM_SECRET_[A-Za-z0-9]+`,
						Replacement: "[MASKED_CUSTOM]",
						Description: "Custom secret pattern",
					},
				},
			},
		},
	})

	svc := NewMaskingService(registry, AlertMaskingConfig{})

	// Built-in patterns + 1 custom pattern
	builtinCount := len(config.GetBuiltinConfig().MaskingPatterns)
	assert.Equal(t, builtinCount+1, len(svc.patterns))

	// Custom pattern should be keyed as "custom:test-server:0"
	cp, exists := svc.patterns["custom:test-server:0"]
	require.True(t, exists, "Custom pattern should be registered")
	assert.Equal(t, "[MASKED_CUSTOM]", cp.Replacement)
}

func TestCompileCustomPatterns_InvalidRegex(t *testing.T) {
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": {
			Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
			DataMasking: &config.MaskingConfig{
				Enabled: true,
				CustomPatterns: []config.MaskingPattern{
					{
						Pattern:     `[invalid`,
						Replacement: "[MASKED]",
					},
					{
						Pattern:     `valid_pattern`,
						Replacement: "[MASKED_VALID]",
					},
				},
			},
		},
	})

	svc := NewMaskingService(registry, AlertMaskingConfig{})

	// Invalid pattern should be skipped, valid one compiled
	_, invalidExists := svc.patterns["custom:test-server:0"]
	assert.False(t, invalidExists, "Invalid regex pattern should be skipped")

	_, validExists := svc.patterns["custom:test-server:1"]
	assert.True(t, validExists, "Valid pattern should be compiled")
}

func TestCompileCustomPatterns_MaskingDisabled(t *testing.T) {
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": {
			Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
			DataMasking: &config.MaskingConfig{
				Enabled: false, // Disabled
				CustomPatterns: []config.MaskingPattern{
					{Pattern: `secret`, Replacement: "[MASKED]"},
				},
			},
		},
	})

	svc := NewMaskingService(registry, AlertMaskingConfig{})

	// Custom patterns from disabled servers should not be compiled
	_, exists := svc.patterns["custom:test-server:0"]
	assert.False(t, exists, "Custom patterns from disabled servers should not be compiled")
}

func TestResolvePatterns_GroupExpansion(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	tests := []struct {
		name           string
		groups         []string
		minRegex       int
		hasCodeMaskers bool
	}{
		{
			name:     "basic group",
			groups:   []string{"basic"},
			minRegex: 2, // api_key, password
		},
		{
			name:     "secrets group",
			groups:   []string{"secrets"},
			minRegex: 5, // api_key, password, token, private_key, secret_key
		},
		{
			name:     "security group",
			groups:   []string{"security"},
			minRegex: 7,
		},
		{
			name:           "kubernetes group",
			groups:         []string{"kubernetes"},
			minRegex:       3, // api_key, password, certificate_authority_data (kubernetes_secret is a code masker)
			hasCodeMaskers: true,
		},
		{
			name:     "cloud group",
			groups:   []string{"cloud"},
			minRegex: 4,
		},
		{
			name:     "all group",
			groups:   []string{"all"},
			minRegex: 15,
		},
		{
			name:     "multiple groups with dedup",
			groups:   []string{"basic", "secrets"}, // Both have api_key and password
			minRegex: 5,                            // Should be deduplicated
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MaskingConfig{
				Enabled:       true,
				PatternGroups: tt.groups,
			}
			resolved := svc.resolvePatterns(cfg, "")

			assert.GreaterOrEqual(t, len(resolved.regexPatterns), tt.minRegex,
				"Should have at least %d regex patterns", tt.minRegex)

			if tt.hasCodeMaskers {
				assert.NotEmpty(t, resolved.codeMaskerNames, "Should have code maskers")
				assert.Contains(t, resolved.codeMaskerNames, "kubernetes_secret")
			}
		})
	}
}

func TestResolvePatterns_IndividualPatterns(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	cfg := &config.MaskingConfig{
		Enabled:  true,
		Patterns: []string{"api_key", "email"},
	}
	resolved := svc.resolvePatterns(cfg, "")

	assert.Len(t, resolved.regexPatterns, 2)

	names := make([]string, len(resolved.regexPatterns))
	for i, p := range resolved.regexPatterns {
		names[i] = p.Name
	}
	assert.Contains(t, names, "api_key")
	assert.Contains(t, names, "email")
}

func TestResolvePatterns_UnknownGroup(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	cfg := &config.MaskingConfig{
		Enabled:       true,
		PatternGroups: []string{"nonexistent_group"},
	}
	resolved := svc.resolvePatterns(cfg, "")

	assert.Empty(t, resolved.regexPatterns)
	assert.Empty(t, resolved.codeMaskerNames)
}

func TestResolvePatterns_WithCustomPatterns(t *testing.T) {
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": {
			Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
			DataMasking: &config.MaskingConfig{
				Enabled:       true,
				PatternGroups: []string{"basic"},
				CustomPatterns: []config.MaskingPattern{
					{Pattern: `MY_SECRET_[A-Z]+`, Replacement: "[MASKED_MY_SECRET]"},
				},
			},
		},
	})

	svc := NewMaskingService(registry, AlertMaskingConfig{})

	cfg := &config.MaskingConfig{
		Enabled:       true,
		PatternGroups: []string{"basic"},
		CustomPatterns: []config.MaskingPattern{
			{Pattern: `MY_SECRET_[A-Z]+`, Replacement: "[MASKED_MY_SECRET]"},
		},
	}
	resolved := svc.resolvePatterns(cfg, "test-server")

	// Should have basic group patterns + the custom pattern
	assert.GreaterOrEqual(t, len(resolved.regexPatterns), 3) // api_key + password + custom
}

func TestResolvePatternsFromGroup(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	t.Run("valid group", func(t *testing.T) {
		resolved := svc.resolvePatternsFromGroup("security")
		assert.GreaterOrEqual(t, len(resolved.regexPatterns), 7)
	})

	t.Run("unknown group", func(t *testing.T) {
		resolved := svc.resolvePatternsFromGroup("nonexistent")
		assert.Empty(t, resolved.regexPatterns)
		assert.Empty(t, resolved.codeMaskerNames)
	})
}

func TestResolvePatterns_Deduplication(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{})

	// api_key appears in both the group and the individual patterns list
	cfg := &config.MaskingConfig{
		Enabled:       true,
		PatternGroups: []string{"basic"},     // Contains api_key, password
		Patterns:      []string{"api_key"},   // Duplicate
	}
	resolved := svc.resolvePatterns(cfg, "")

	// Count occurrences of api_key
	apiKeyCount := 0
	for _, p := range resolved.regexPatterns {
		if p.Name == "api_key" {
			apiKeyCount++
		}
	}
	assert.Equal(t, 1, apiKeyCount, "api_key should appear only once (deduplicated)")
}
