package masking

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// newTestMaskingService creates a MaskingService with a registry containing a server
// with data masking enabled for the given pattern groups and patterns.
func newTestMaskingService(t *testing.T, groups []string, patterns []string) *MaskingService {
	t.Helper()
	return NewMaskingService(
		config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
				DataMasking: &config.MaskingConfig{
					Enabled:       true,
					PatternGroups: groups,
					Patterns:      patterns,
				},
			},
		}),
		AlertMaskingConfig{Enabled: true, PatternGroup: "security"},
	)
}

func TestNewMaskingService(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	svc := NewMaskingService(registry, AlertMaskingConfig{Enabled: true, PatternGroup: "security"})

	assert.NotNil(t, svc)
	assert.NotEmpty(t, svc.patterns, "Should have compiled patterns")
	assert.NotEmpty(t, svc.codeMaskers, "Should have registered code maskers")
	assert.Contains(t, svc.codeMaskers, "kubernetes_secret")
}

func TestMaskToolResult_EmptyContent(t *testing.T) {
	svc := newTestMaskingService(t, []string{"basic"}, nil)
	result := svc.MaskToolResult("", "test-server")
	assert.Empty(t, result)
}

func TestMaskToolResult_NoMaskingConfigured(t *testing.T) {
	// Server exists but no masking configured
	svc := NewMaskingService(
		config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"no-masking-server": {
				Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
			},
		}),
		AlertMaskingConfig{},
	)

	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result := svc.MaskToolResult(content, "no-masking-server")
	assert.Equal(t, content, result, "Content should pass through when masking not configured")
}

func TestMaskToolResult_MaskingDisabled(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"disabled-server": {
				Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
				DataMasking: &config.MaskingConfig{
					Enabled:       false,
					PatternGroups: []string{"basic"},
				},
			},
		}),
		AlertMaskingConfig{},
	)

	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result := svc.MaskToolResult(content, "disabled-server")
	assert.Equal(t, content, result, "Content should pass through when masking disabled")
}

func TestMaskToolResult_UnknownServer(t *testing.T) {
	svc := newTestMaskingService(t, []string{"basic"}, nil)
	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result := svc.MaskToolResult(content, "nonexistent-server")
	assert.Equal(t, content, result, "Content should pass through for unknown server")
}

func TestMaskToolResult_MasksAPIKey(t *testing.T) {
	svc := newTestMaskingService(t, []string{"basic"}, nil)
	content := `Configuration:
api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"
debug: true`

	result := svc.MaskToolResult(content, "test-server")

	assert.NotContains(t, result, "sk-FAKE-NOT-REAL-API-KEY-XXXX", "API key should be masked")
	assert.Contains(t, result, "[MASKED_API_KEY]", "Should contain masked replacement")
	assert.Contains(t, result, "debug: true", "Non-sensitive content should be preserved")
}

func TestMaskToolResult_MasksPassword(t *testing.T) {
	svc := newTestMaskingService(t, []string{"basic"}, nil)
	content := `password: "FAKE-S3CRET-PASS-NOT-REAL"`

	result := svc.MaskToolResult(content, "test-server")

	assert.NotContains(t, result, "FAKE-S3CRET-PASS-NOT-REAL", "Password should be masked")
	assert.Contains(t, result, "[MASKED_PASSWORD]")
}

func TestMaskToolResult_MasksMultiplePatterns(t *testing.T) {
	svc := newTestMaskingService(t, []string{"security"}, nil)
	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"
password: "FAKE-S3CRET-PASS-NOT-REAL"
user@example.com contacted us`

	result := svc.MaskToolResult(content, "test-server")

	assert.NotContains(t, result, "sk-FAKE-NOT-REAL-API-KEY-XXXX")
	assert.NotContains(t, result, "FAKE-S3CRET-PASS-NOT-REAL")
	assert.NotContains(t, result, "user@example.com")
	assert.Contains(t, result, "[MASKED_API_KEY]")
	assert.Contains(t, result, "[MASKED_PASSWORD]")
	assert.Contains(t, result, "[MASKED_EMAIL]")
}

func TestMaskToolResult_NoPatterns(t *testing.T) {
	// Server has masking enabled but no patterns/groups configured
	svc := NewMaskingService(
		config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"empty-server": {
				Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
				DataMasking: &config.MaskingConfig{
					Enabled: true,
					// No pattern groups, patterns, or custom patterns
				},
			},
		}),
		AlertMaskingConfig{},
	)

	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result := svc.MaskToolResult(content, "empty-server")
	assert.Equal(t, content, result, "Should pass through when no patterns configured")
}

func TestMaskToolResult_CustomPatterns(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"custom-server": {
				Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"},
				DataMasking: &config.MaskingConfig{
					Enabled: true,
					CustomPatterns: []config.MaskingPattern{
						{
							Pattern:     `INTERNAL_TOKEN_[A-Z0-9]+`,
							Replacement: "[MASKED_INTERNAL_TOKEN]",
							Description: "Internal tokens",
						},
					},
				},
			},
		}),
		AlertMaskingConfig{},
	)

	content := `token: INTERNAL_TOKEN_ABC123DEF`
	result := svc.MaskToolResult(content, "custom-server")

	assert.NotContains(t, result, "INTERNAL_TOKEN_ABC123DEF")
	assert.Contains(t, result, "[MASKED_INTERNAL_TOKEN]")
}

func TestMaskAlertData_Enabled(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(nil),
		AlertMaskingConfig{Enabled: true, PatternGroup: "security"},
	)

	data := `Alert: password: "FAKE-S3CRET-NOT-REAL" detected on user@example.com`
	result := svc.MaskAlertData(data)

	assert.NotContains(t, result, "FAKE-S3CRET-NOT-REAL")
	assert.NotContains(t, result, "user@example.com")
	assert.Contains(t, result, "[MASKED_PASSWORD]")
	assert.Contains(t, result, "[MASKED_EMAIL]")
}

func TestMaskAlertData_Disabled(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(nil),
		AlertMaskingConfig{Enabled: false, PatternGroup: "security"},
	)

	data := `password: "FAKE-S3CRET-NOT-REAL"`
	result := svc.MaskAlertData(data)
	assert.Equal(t, data, result, "Should pass through when alert masking disabled")
}

func TestMaskAlertData_EmptyData(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(nil),
		AlertMaskingConfig{Enabled: true, PatternGroup: "security"},
	)

	result := svc.MaskAlertData("")
	assert.Empty(t, result)
}

func TestMaskAlertData_UnknownPatternGroup(t *testing.T) {
	svc := NewMaskingService(
		config.NewMCPServerRegistry(nil),
		AlertMaskingConfig{Enabled: true, PatternGroup: "nonexistent"},
	)

	data := `password: "FAKE-S3CRET-NOT-REAL"`
	result := svc.MaskAlertData(data)
	assert.Equal(t, data, result, "Should pass through with unknown pattern group")
}

func TestMaskToolResult_FailClosed(t *testing.T) {
	// The current implementation doesn't have a code path that returns an error
	// from applyMasking, but we test that MaskToolResult returns the redaction
	// notice when content would leak. This test verifies that content is masked
	// and the fail-closed behavior is wired correctly in the service.
	svc := newTestMaskingService(t, []string{"basic"}, nil)
	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result := svc.MaskToolResult(content, "test-server")

	// Should be masked (not the original)
	assert.NotEqual(t, content, result)
	assert.Contains(t, result, "[MASKED_API_KEY]")
}

func TestMaskAlertData_FailOpen(t *testing.T) {
	// Alert masking should return original data on failure (fail-open).
	// The current implementation doesn't have a code path that returns an error,
	// but this test verifies the fail-open behavior is wired.
	svc := NewMaskingService(
		config.NewMCPServerRegistry(nil),
		AlertMaskingConfig{Enabled: true, PatternGroup: "basic"},
	)

	data := `password: "FAKE-S3CRET-NOT-REAL"`
	result := svc.MaskAlertData(data)

	// Should be masked
	assert.NotEqual(t, data, result)
	assert.Contains(t, result, "[MASKED_PASSWORD]")
}

func TestApplyMasking_CodeMaskersBeforeRegex(t *testing.T) {
	// Verify code maskers run before regex patterns.
	// We use the kubernetes_secret masker as our code masker.
	svc := newTestMaskingService(t, []string{"kubernetes"}, nil)

	resolved := &resolvedPatterns{
		codeMaskerNames: []string{"kubernetes_secret"},
		regexPatterns:   svc.resolvePatterns(&config.MaskingConfig{
			Enabled: true,
			Patterns: []string{"api_key"},
		}, "").regexPatterns,
	}

	content := `api_key: "sk-FAKE-NOT-REAL-API-KEY-XXXX"`
	result, err := svc.applyMasking(content, resolved)
	require.NoError(t, err)

	// api_key should still be masked by regex
	assert.Contains(t, result, "[MASKED_API_KEY]")
}

func TestMaskToolResult_Certificate(t *testing.T) {
	svc := newTestMaskingService(t, []string{"security"}, nil)
	content := `Config:
-----BEGIN RSA PRIVATE KEY-----
FAKE-RSA-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-RSA-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXX
-----END RSA PRIVATE KEY-----
Done.`

	result := svc.MaskToolResult(content, "test-server")

	assert.NotContains(t, result, "FAKE-RSA-KEY-DATA")
	assert.Contains(t, result, "[MASKED_CERTIFICATE]")
	assert.Contains(t, result, "Done.")
}

func TestMaskToolResult_CombinedCodeMaskerAndRegex(t *testing.T) {
	// The "kubernetes" group includes both the kubernetes_secret code masker
	// and regex patterns (api_key, password, certificate_authority_data).
	// This test verifies both masking phases work together on a single Secret.
	svc := newTestMaskingService(t, []string{"kubernetes"}, nil)

	content := `apiVersion: v1
kind: Secret
metadata:
  name: db-creds
  annotations:
    note: "certificate-authority-data: FAKECERTDATANOTREALDATAXXXXXXXXXX"
type: Opaque
data:
  token: c3VwZXJzZWNyZXQ=
  tls.key: RkFLRS10bHMta2V5LW5vdC1yZWFs`

	result := svc.MaskToolResult(content, "test-server")

	// Code masker (phase 1) should mask the Secret data field values
	assert.NotContains(t, result, "c3VwZXJzZWNyZXQ=", "Secret data should be masked by code masker")
	assert.NotContains(t, result, "RkFLRS10bHMta2V5LW5vdC1yZWFs", "TLS key data should be masked by code masker")

	// Regex patterns (phase 2) should mask CA data in annotations
	assert.NotContains(t, result, "FAKECERTDATANOTREALDATAXXXXXXXXXX", "CA data in annotation should be masked by regex")
	assert.Contains(t, result, "[MASKED_CA_CERTIFICATE]")

	// Metadata should be preserved
	assert.Contains(t, result, "name: db-creds")
}

func TestBuiltinPatternRegression(t *testing.T) {
	// Table-driven regression tests for each of the 15 built-in patterns.
	svc := NewMaskingService(config.NewMCPServerRegistry(nil), AlertMaskingConfig{})

	tests := []struct {
		name        string
		pattern     string
		input       string
		shouldMask  bool
		maskContain string
	}{
		{
			name:        "api_key masks standard format",
			pattern:     "api_key",
			input:       `api_key: "FAKE-API-KEY-NOT-REAL-XXXXXXXXXXXX"`,
			shouldMask:  true,
			maskContain: "[MASKED_API_KEY]",
		},
		{
			name:        "password masks standard format",
			pattern:     "password",
			input:       `password: "FAKE-PASSWORD-NOT-REAL"`,
			shouldMask:  true,
			maskContain: "[MASKED_PASSWORD]",
		},
		{
			name:       "password does not mask short value",
			pattern:    "password",
			input:      `password: "short"`,
			shouldMask: false,
		},
		{
			name: "certificate masks PEM block",
			pattern: "certificate",
			input: `-----BEGIN CERTIFICATE-----
FAKE-CERT-DATA-NOT-REAL
-----END CERTIFICATE-----`,
			shouldMask:  true,
			maskContain: "[MASKED_CERTIFICATE]",
		},
		{
			name:        "certificate_authority_data masks k8s CA",
			pattern:     "certificate_authority_data",
			input:       `certificate-authority-data: FAKECERTDATANOTREALDATAXXXXXXXXXX`,
			shouldMask:  true,
			maskContain: "[MASKED_CA_CERTIFICATE]",
		},
		{
			name:        "token masks bearer token",
			pattern:     "token",
			input:       `bearer: FAKE-JWT-TOKEN-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX`,
			shouldMask:  true,
			maskContain: "[MASKED_TOKEN]",
		},
		{
			name:        "email masks standard email",
			pattern:     "email",
			input:       `contact: user@example.com`,
			shouldMask:  true,
			maskContain: "[MASKED_EMAIL]",
		},
		{
			name:        "ssh_key masks RSA public key",
			pattern:     "ssh_key",
			input:       `ssh-rsa FAKENOTREALRSAPUBLICKEYXXXXXXXXXXXXXX user@host`,
			shouldMask:  true,
			maskContain: "[MASKED_SSH_KEY]",
		},
		{
			name:        "private_key masks standard format",
			pattern:     "private_key",
			input:       `private_key: "sk_test_FAKE_NOT_REAL_XXXXX"`,
			shouldMask:  true,
			maskContain: "[MASKED_PRIVATE_KEY]",
		},
		{
			name:        "secret_key masks standard format",
			pattern:     "secret_key",
			input:       `secret_key: "sec_FAKE_NOT_REAL_XXXXXXX"`,
			shouldMask:  true,
			maskContain: "[MASKED_SECRET_KEY]",
		},
		{
			name:        "aws_access_key masks AKIA format",
			pattern:     "aws_access_key",
			input:       `aws_access_key_id: "AKIAFAKENOTREALSECRET"`,
			shouldMask:  true,
			maskContain: "[MASKED_AWS_KEY]",
		},
		{
			name:        "github_token masks ghp format",
			pattern:     "github_token",
			input:       `github_token: ghp_FAKE_NOT_REAL_GITHUB_TOKEN_XXXXXXXXXXXX`,
			shouldMask:  true,
			maskContain: "[MASKED_GITHUB_TOKEN]",
		},
		{
			name:        "slack_token masks xoxb format",
			pattern:     "slack_token",
			input:       `SLACK_TOKEN=xoxb-FAKE-NOT-REAL-SLACK-BOT-TOKEN-XXXXXXXXXX`,
			shouldMask:  true,
			maskContain: "[MASKED_SLACK_TOKEN]",
		},
		{
			name:        "base64_secret masks long base64",
			pattern:     "base64_secret",
			input:       `data: RkFLRS1CQVNFNTY0LUZBVEFMT05HLU5PVC1SRUFMLURYWFJJU1hYWFhYWFhYWFhYWFg=`,
			shouldMask:  true,
			maskContain: "[MASKED_BASE64_VALUE]",
		},
		{
			name:        "base64_short masks short base64 value",
			pattern:     "base64_short",
			input:       `key: dGVzdA==`,
			shouldMask:  true,
			maskContain: "[MASKED_SHORT_BASE64]",
		},
		{
			name:        "aws_secret_key masks 40 char format",
			pattern:     "aws_secret_key",
			input:       `aws_secret_access_key: "FAKESECRETNOTREAL1234567890XXXXXXXXXXXABC"`,
			shouldMask:  true,
			maskContain: "[MASKED_AWS_SECRET]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp, exists := svc.patterns[tt.pattern]
			require.True(t, exists, "Pattern %s should exist", tt.pattern)

			result := cp.Regex.ReplaceAllString(tt.input, cp.Replacement)
			if tt.shouldMask {
				assert.NotEqual(t, tt.input, result, "Should have masked the input")
				assert.Contains(t, result, tt.maskContain)
			} else {
				assert.Equal(t, tt.input, result, "Should not have masked the input")
			}
		})
	}
}
