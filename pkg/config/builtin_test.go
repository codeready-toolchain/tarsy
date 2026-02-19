package config

import (
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBuiltinConfig(t *testing.T) {
	// Test singleton pattern - should return same instance
	cfg1 := GetBuiltinConfig()
	cfg2 := GetBuiltinConfig()

	assert.Same(t, cfg1, cfg2, "GetBuiltinConfig should return same instance")
	assert.NotNil(t, cfg1, "Built-in config should not be nil")
}

func TestBuiltinConfigThreadSafety(t *testing.T) {
	const goroutines = 100

	var wg sync.WaitGroup
	configs := make([]*BuiltinConfig, goroutines)

	// Launch multiple goroutines to access config concurrently
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			configs[index] = GetBuiltinConfig()
		}(i)
	}

	wg.Wait()

	// All goroutines should get the same instance
	for i := 1; i < goroutines; i++ {
		assert.Same(t, configs[0], configs[i], "All goroutines should get same instance")
	}
}

func TestBuiltinAgents(t *testing.T) {
	cfg := GetBuiltinConfig()

	tests := []struct {
		name                    string
		agentID                 string
		wantDesc                string
		wantStrat               IterationStrategy
		wantCustomInstructions  bool   // Whether CustomInstructions should be non-empty
		customInstructionsMatch string // Substring to check in CustomInstructions (if wantCustomInstructions)
	}{
		{
			name:      "KubernetesAgent",
			agentID:   "KubernetesAgent",
			wantDesc:  "Kubernetes-specialized agent",
			wantStrat: "", // No explicit strategy — inherits from defaults.
		},
		{
			name:      "ChatAgent",
			agentID:   "ChatAgent",
			wantDesc:  "Built-in agent for follow-up conversations",
			wantStrat: "", // No explicit strategy — inherits from defaults.
		},
		{
			name:                    "SynthesisAgent",
			agentID:                 "SynthesisAgent",
			wantDesc:                "Synthesizes parallel investigation results",
			wantStrat:               IterationStrategySynthesis,
			wantCustomInstructions:  true,
			customInstructionsMatch: "Incident Commander",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, exists := cfg.Agents[tt.agentID]
			require.True(t, exists, "Agent %s should exist", tt.agentID)
			assert.Equal(t, tt.wantDesc, agent.Description)
			assert.Equal(t, tt.wantStrat, agent.IterationStrategy)

			if tt.wantCustomInstructions {
				assert.NotEmpty(t, agent.CustomInstructions, "Agent %s should have custom instructions", tt.agentID)
				assert.Contains(t, agent.CustomInstructions, tt.customInstructionsMatch)
			}
		})
	}
}

func TestBuiltinChatAgentInheritsFromDefaults(t *testing.T) {
	cfg := GetBuiltinConfig()
	agent, exists := cfg.Agents["ChatAgent"]
	require.True(t, exists)

	// ChatAgent should not pin a specific iteration strategy or MCP servers.
	// Both are inherited at resolution time: strategy from defaults, MCP
	// servers from the chain's investigation stages via aggregation.
	assert.Empty(t, agent.IterationStrategy, "ChatAgent should not set IterationStrategy")
	assert.Empty(t, agent.MCPServers, "ChatAgent should not set MCPServers")
	assert.Empty(t, agent.CustomInstructions, "ChatAgent should not set CustomInstructions")
}

func TestBuiltinSynthesisAgentHasNoMCPServers(t *testing.T) {
	cfg := GetBuiltinConfig()
	agent, exists := cfg.Agents["SynthesisAgent"]
	require.True(t, exists)

	assert.Empty(t, agent.MCPServers, "SynthesisAgent should not have MCP servers (it never calls tools)")
}

func TestBuiltinMCPServers(t *testing.T) {
	cfg := GetBuiltinConfig()

	t.Run("kubernetes-server", func(t *testing.T) {
		server, exists := cfg.MCPServers["kubernetes-server"]
		require.True(t, exists, "kubernetes-server should exist")

		assert.Equal(t, TransportTypeStdio, server.Transport.Type)
		assert.Equal(t, "npx", server.Transport.Command)
		assert.NotEmpty(t, server.Transport.Args)
		assert.NotEmpty(t, server.Instructions)
		assert.NotNil(t, server.DataMasking)
		assert.True(t, server.DataMasking.Enabled)
		assert.NotNil(t, server.Summarization)
		assert.True(t, server.Summarization.Enabled)
	})
}

func TestBuiltinLLMProviders(t *testing.T) {
	cfg := GetBuiltinConfig()

	tests := []struct {
		name          string
		providerID    string
		wantType      LLMProviderType
		wantMinTokens int
		checkAPIKey   bool // VertexAI uses ProjectEnv/LocationEnv instead
	}{
		{
			name:          "google-default",
			providerID:    "google-default",
			wantType:      LLMProviderTypeGoogle,
			wantMinTokens: 900000,
			checkAPIKey:   true,
		},
		{
			name:          "openai-default",
			providerID:    "openai-default",
			wantType:      LLMProviderTypeOpenAI,
			wantMinTokens: 350000,
			checkAPIKey:   true,
		},
		{
			name:          "anthropic-default",
			providerID:    "anthropic-default",
			wantType:      LLMProviderTypeAnthropic,
			wantMinTokens: 800000,
			checkAPIKey:   true,
		},
		{
			name:          "gemini-3-flash",
			providerID:    "gemini-3-flash",
			wantType:      LLMProviderTypeGoogle,
			wantMinTokens: 900000,
			checkAPIKey:   true,
		},
		{
			name:          "gemini-3-pro",
			providerID:    "gemini-3-pro",
			wantType:      LLMProviderTypeGoogle,
			wantMinTokens: 900000,
			checkAPIKey:   true,
		},
		{
			name:          "gemini-2.5-flash",
			providerID:    "gemini-2.5-flash",
			wantType:      LLMProviderTypeGoogle,
			wantMinTokens: 900000,
			checkAPIKey:   true,
		},
		{
			name:          "gemini-2.5-pro",
			providerID:    "gemini-2.5-pro",
			wantType:      LLMProviderTypeGoogle,
			wantMinTokens: 900000,
			checkAPIKey:   true,
		},
		{
			name:          "xai-default",
			providerID:    "xai-default",
			wantType:      LLMProviderTypeXAI,
			wantMinTokens: 200000,
			checkAPIKey:   true,
		},
		{
			name:          "vertexai-default",
			providerID:    "vertexai-default",
			wantType:      LLMProviderTypeVertexAI,
			wantMinTokens: 800000,
			checkAPIKey:   false, // VertexAI uses ProjectEnv/LocationEnv
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, exists := cfg.LLMProviders[tt.providerID]
			require.True(t, exists, "Provider %s should exist", tt.providerID)
			assert.Equal(t, tt.wantType, provider.Type)
			assert.NotEmpty(t, provider.Model)
			if tt.checkAPIKey {
				assert.NotEmpty(t, provider.APIKeyEnv)
			}
			assert.GreaterOrEqual(t, provider.MaxToolResultTokens, tt.wantMinTokens)
		})
	}
}

func TestBuiltinChains(t *testing.T) {
	cfg := GetBuiltinConfig()

	t.Run("kubernetes-agent-chain", func(t *testing.T) {
		chain, exists := cfg.ChainDefinitions["kubernetes-agent-chain"]
		require.True(t, exists, "kubernetes-agent-chain should exist")

		assert.Contains(t, chain.AlertTypes, "kubernetes")
		assert.NotEmpty(t, chain.Description)
		assert.Len(t, chain.Stages, 1)
		assert.Equal(t, "analysis", chain.Stages[0].Name)
		assert.Len(t, chain.Stages[0].Agents, 1)
		assert.Equal(t, "KubernetesAgent", chain.Stages[0].Agents[0].Name)
	})
}

func TestBuiltinMaskingPatterns(t *testing.T) {
	cfg := GetBuiltinConfig()

	// Test that key patterns exist
	requiredPatterns := []string{
		"api_key",
		"password",
		"certificate",
		"certificate_authority_data",
		"token",
		"email",
		"ssh_key",
		"base64_secret",
		"base64_short",
	}

	for _, patternName := range requiredPatterns {
		t.Run(patternName, func(t *testing.T) {
			pattern, exists := cfg.MaskingPatterns[patternName]
			require.True(t, exists, "Pattern %s should exist", patternName)
			assert.NotEmpty(t, pattern.Pattern, "Pattern regex should not be empty")
			assert.NotEmpty(t, pattern.Replacement, "Pattern replacement should not be empty")
			assert.NotEmpty(t, pattern.Description, "Pattern description should not be empty")
		})
	}

	// Test that we have at least 15 patterns (as per design)
	assert.GreaterOrEqual(t, len(cfg.MaskingPatterns), 15, "Should have at least 15 masking patterns")
}

func TestBuiltinPatternGroups(t *testing.T) {
	cfg := GetBuiltinConfig()

	tests := []struct {
		name      string
		groupName string
		minSize   int
	}{
		{
			name:      "basic group",
			groupName: "basic",
			minSize:   2,
		},
		{
			name:      "secrets group",
			groupName: "secrets",
			minSize:   3,
		},
		{
			name:      "security group",
			groupName: "security",
			minSize:   5,
		},
		{
			name:      "kubernetes group",
			groupName: "kubernetes",
			minSize:   3,
		},
		{
			name:      "all group",
			groupName: "all",
			minSize:   10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, exists := cfg.PatternGroups[tt.groupName]
			require.True(t, exists, "Pattern group %s should exist", tt.groupName)
			assert.GreaterOrEqual(t, len(group), tt.minSize, "Group should have at least %d patterns", tt.minSize)

			// Verify all patterns in group exist (either as regex patterns or code-based maskers)
			for _, patternName := range group {
				_, existsInPatterns := cfg.MaskingPatterns[patternName]
				existsInCodeMaskers := slices.Contains(cfg.CodeMaskers, patternName)
				assert.True(t, existsInPatterns || existsInCodeMaskers,
					"Pattern %s in group %s should exist in either MaskingPatterns or CodeMaskers",
					patternName, tt.groupName)
			}
		})
	}
}

func TestBuiltinCodeMaskers(t *testing.T) {
	cfg := GetBuiltinConfig()

	t.Run("kubernetes_secret masker", func(t *testing.T) {
		assert.Contains(t, cfg.CodeMaskers, "kubernetes_secret",
			"kubernetes_secret masker should exist")
	})
}

func TestBuiltinDefaults(t *testing.T) {
	cfg := GetBuiltinConfig()

	t.Run("DefaultRunbook", func(t *testing.T) {
		assert.NotEmpty(t, cfg.DefaultRunbook, "Default runbook should not be empty")
		assert.Contains(t, cfg.DefaultRunbook, "Investigation Steps", "Default runbook should contain investigation steps")
	})

	t.Run("DefaultAlertType", func(t *testing.T) {
		assert.Equal(t, "kubernetes", cfg.DefaultAlertType, "Default alert type should be kubernetes")
	})
}

func TestBuiltinConfigCompleteness(t *testing.T) {
	cfg := GetBuiltinConfig()

	t.Run("all required fields populated", func(t *testing.T) {
		assert.NotEmpty(t, cfg.Agents, "Agents should be populated")
		assert.NotEmpty(t, cfg.MCPServers, "MCP servers should be populated")
		assert.NotEmpty(t, cfg.LLMProviders, "LLM providers should be populated")
		assert.NotEmpty(t, cfg.ChainDefinitions, "Chain definitions should be populated")
		assert.NotEmpty(t, cfg.MaskingPatterns, "Masking patterns should be populated")
		assert.NotEmpty(t, cfg.PatternGroups, "Pattern groups should be populated")
		assert.NotEmpty(t, cfg.DefaultRunbook, "Default runbook should be populated")
		assert.NotEmpty(t, cfg.DefaultAlertType, "Default alert type should be populated")
	})
}

func TestMaskingPatternsRegexValidation(t *testing.T) {
	cfg := GetBuiltinConfig()

	tests := []struct {
		name        string
		patternName string
		testInput   string
		shouldMatch bool
		description string
	}{
		// Certificate pattern tests (multi-line PEM blocks)
		{
			name:        "certificate - RSA private key (multi-line)",
			patternName: "certificate",
			testInput: `-----BEGIN RSA PRIVATE KEY-----
FAKE-RSA-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-RSA-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXX
-----END RSA PRIVATE KEY-----`,
			shouldMatch: true,
			description: "Multi-line PEM certificate should match",
		},
		{
			name:        "certificate - certificate (multi-line)",
			patternName: "certificate",
			testInput: `-----BEGIN CERTIFICATE-----
FAKE-CERTIFICATE-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-CERTIFICATE-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-CERTIFICATE-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXX
-----END CERTIFICATE-----`,
			shouldMatch: true,
			description: "Multi-line certificate should match",
		},
		{
			name:        "certificate - EC private key",
			patternName: "certificate",
			testInput: `-----BEGIN EC PRIVATE KEY-----
FAKE-EC-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-EC-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
FAKE-EC-KEY-DATA-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
-----END EC PRIVATE KEY-----`,
			shouldMatch: true,
			description: "Multi-line EC private key should match",
		},
		{
			name:        "certificate - no match for plain text",
			patternName: "certificate",
			testInput:   "This is just plain text without any certificate",
			shouldMatch: false,
			description: "Plain text should not match",
		},

		// API key pattern tests
		{
			name:        "api_key - standard format",
			patternName: "api_key",
			testInput:   `"api_key": "FAKE-API-KEY-NOT-REAL-XXXXXXXXXXXX"`,
			shouldMatch: true,
			description: "Standard API key format should match",
		},
		{
			name:        "api_key - alternative format",
			patternName: "api_key",
			testInput:   `apikey=FAKE-SK-KEY-NOT-REAL-XXXXX`,
			shouldMatch: true,
			description: "Alternative API key format should match",
		},
		{
			name:        "api_key - short key should not match",
			patternName: "api_key",
			testInput:   `api_key: "short"`,
			shouldMatch: false,
			description: "Short API key should not match (less than 20 chars)",
		},

		// Password pattern tests
		{
			name:        "password - standard format",
			patternName: "password",
			testInput:   `password: "FAKE-PASSWORD-NOT-REAL"`,
			shouldMatch: true,
			description: "Standard password format should match",
		},
		{
			name:        "password - short password should not match",
			patternName: "password",
			testInput:   `password: "short"`,
			shouldMatch: false,
			description: "Short password should not match (less than 6 chars)",
		},

		// Token pattern tests
		{
			name:        "token - bearer token",
			patternName: "token",
			testInput:   `bearer: FAKE-JWT-TOKEN-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX`,
			shouldMatch: true,
			description: "Bearer token should match",
		},
		{
			name:        "token - jwt token",
			patternName: "token",
			testInput:   `jwt: "FAKE-JWT-TOKEN-NOT-REAL-XXXXXXXXXXXXX"`,
			shouldMatch: true,
			description: "JWT token should match",
		},
		{
			name:        "token - token with equals",
			patternName: "token",
			testInput:   `token=FAKE-TOKEN-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX`,
			shouldMatch: true,
			description: "Token with equals should match",
		},

		// Email pattern tests
		{
			name:        "email - standard email",
			patternName: "email",
			testInput:   "user@example.com",
			shouldMatch: true,
			description: "Standard email should match",
		},
		{
			name:        "email - email with subdomain",
			patternName: "email",
			testInput:   "admin@mail.company.co.uk",
			shouldMatch: true,
			description: "Email with subdomain should match",
		},
		{
			name:        "email - email with plus",
			patternName: "email",
			testInput:   "user+tag@example.com",
			shouldMatch: true,
			description: "Email with plus should match",
		},
		{
			name:        "email - invalid email",
			patternName: "email",
			testInput:   "not-an-email",
			shouldMatch: false,
			description: "Invalid email should not match",
		},

		// SSH key pattern tests
		{
			name:        "ssh_key - RSA public key",
			patternName: "ssh_key",
			testInput:   `ssh-rsa FAKE-SSH-RSA-KEY-NOT-REAL-XXXXXXXXXXXXXXXXXXXXXXXXXX user@host`,
			shouldMatch: true,
			description: "SSH RSA public key should match",
		},
		{
			name:        "ssh_key - ed25519 key",
			patternName: "ssh_key",
			testInput:   `ssh-ed25519 FAKE-SSH-ED25519-KEY-NOT-REAL-XXXXXXXXXXXXXX user@host`,
			shouldMatch: true,
			description: "SSH ed25519 key should match",
		},

		// Certificate authority data pattern tests
		{
			name:        "certificate_authority_data - k8s format",
			patternName: "certificate_authority_data",
			testInput:   `certificate-authority-data: RkFLRS1DRVJUSUFJQ0FURS1EQVRBLU5PVC1SRUFMLVRYWFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRYUFRY`,
			shouldMatch: true,
			description: "Kubernetes CA data should match",
		},

		// Base64 pattern tests
		{
			name:        "base64_secret - long base64",
			patternName: "base64_secret",
			testInput:   "RkFLRS1CQVNFNTY0LUZBVEFMT05HLU5PVC1SRUFMLURYYFJJU1hYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhY",
			shouldMatch: true,
			description: "Long base64 value should match",
		},
		{
			name:        "base64_short - short base64",
			patternName: "base64_short",
			testInput:   "key: dGVzdA==",
			shouldMatch: true,
			description: "Short base64 value should match",
		},

		// AWS keys pattern tests
		{
			name:        "aws_access_key - AKIA format",
			patternName: "aws_access_key",
			testInput:   `aws_access_key_id: "AKIAFAKENOTREALSECRET"`,
			shouldMatch: true,
			description: "AWS access key should match",
		},
		{
			name:        "aws_secret_key - 40 char format",
			patternName: "aws_secret_key",
			testInput:   `aws_secret_access_key: "FAKESECRETNOTREAL1234567890XXXXXXXXXXXABC"`,
			shouldMatch: true,
			description: "AWS secret key should match",
		},

		// GitHub token pattern tests
		{
			name:        "github_token - ghp format",
			patternName: "github_token",
			testInput:   `github_token: ghp_FAKE_NOT_REAL_GITHUB_TOKEN_XXXXXXXXXXXX`,
			shouldMatch: true,
			description: "GitHub personal access token should match",
		},
		{
			name:        "github_token - ghs format",
			patternName: "github_token",
			testInput:   `GITHUB_TOKEN=ghs_FAKE_NOT_REAL_GITHUB_SERVER_TOKEN_XXXX`,
			shouldMatch: true,
			description: "GitHub server token should match",
		},

		// Slack token pattern tests
		{
			name:        "slack_token - xoxb format",
			patternName: "slack_token",
			testInput:   `SLACK_TOKEN=xoxb-FAKE-NOT-REAL-SLACK-BOT-TOKEN-XXXXXXXXXX`,
			shouldMatch: true,
			description: "Slack bot token should match",
		},
		{
			name:        "slack_token - xoxp format",
			patternName: "slack_token",
			testInput:   `slack_token: xoxp-FAKE-NOT-REAL-SLACK-USER-TOKEN-XXXXXXX`,
			shouldMatch: true,
			description: "Slack user token should match",
		},

		// Private key and secret key patterns
		{
			name:        "private_key - standard format",
			patternName: "private_key",
			testInput:   `private_key: "sk_test_FAKE_NOT_REAL_XXXXX"`,
			shouldMatch: true,
			description: "Private key should match",
		},
		{
			name:        "secret_key - standard format",
			patternName: "secret_key",
			testInput:   `secret_key: "sec_FAKE_NOT_REAL_XXXXXXX"`,
			shouldMatch: true,
			description: "Secret key should match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, exists := cfg.MaskingPatterns[tt.patternName]
			require.True(t, exists, "Pattern %s should exist", tt.patternName)

			// Compile and test the regex
			re, err := regexp.Compile(pattern.Pattern)
			require.NoError(t, err, "Pattern %s should compile: %s", tt.patternName, pattern.Pattern)

			matched := re.MatchString(tt.testInput)
			if tt.shouldMatch {
				assert.True(t, matched, "%s: expected pattern to match input\nPattern: %s\nInput: %s",
					tt.description, pattern.Pattern, tt.testInput)
			} else {
				assert.False(t, matched, "%s: expected pattern NOT to match input\nPattern: %s\nInput: %s",
					tt.description, pattern.Pattern, tt.testInput)
			}
		})
	}
}

func TestMaskingPatternReplacementFormat(t *testing.T) {
	cfg := GetBuiltinConfig()

	// All replacements should use [MASKED_X] format (not __MASKED_X__ or ***MASKED_X***)
	for name, pattern := range cfg.MaskingPatterns {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, pattern.Replacement, "[MASKED_",
				"Pattern %s replacement should use [MASKED_X] format, got: %s", name, pattern.Replacement)
			assert.NotContains(t, pattern.Replacement, "__MASKED_",
				"Pattern %s replacement should not use old __MASKED_X__ format", name)
			assert.NotContains(t, pattern.Replacement, "***MASKED_",
				"Pattern %s replacement should not use ***MASKED_X*** format", name)
		})
	}
}

func TestAllMaskingPatternsCompile(t *testing.T) {
	cfg := GetBuiltinConfig()

	// Ensure all patterns compile as valid regex
	for patternName, pattern := range cfg.MaskingPatterns {
		t.Run(patternName, func(t *testing.T) {
			_, err := regexp.Compile(pattern.Pattern)
			assert.NoError(t, err, "Pattern %s should compile: %s", patternName, pattern.Pattern)
		})
	}
}

func TestPatternGroupMembersResolve(t *testing.T) {
	cfg := GetBuiltinConfig()

	// Test that all pattern group members resolve to either MaskingPatterns or CodeMaskers
	// This is critical for runtime masking to work correctly
	for groupName, patternNames := range cfg.PatternGroups {
		t.Run(groupName, func(t *testing.T) {
			for _, patternName := range patternNames {
				_, existsInPatterns := cfg.MaskingPatterns[patternName]
				existsInCodeMaskers := slices.Contains(cfg.CodeMaskers, patternName)

				assert.True(t, existsInPatterns || existsInCodeMaskers,
					"Pattern '%s' in group '%s' must exist in either MaskingPatterns or CodeMaskers. "+
						"Found in MaskingPatterns: %v, Found in CodeMaskers: %v",
					patternName, groupName, existsInPatterns, existsInCodeMaskers)
			}
		})
	}
}

func TestKubernetesPatternGroupSpecifically(t *testing.T) {
	cfg := GetBuiltinConfig()

	// Explicit test for kubernetes group to ensure kubernetes_secret (code-based masker) is properly resolved
	t.Run("kubernetes group exists", func(t *testing.T) {
		kubernetesGroup, exists := cfg.PatternGroups["kubernetes"]
		require.True(t, exists, "kubernetes pattern group should exist")
		assert.NotEmpty(t, kubernetesGroup, "kubernetes group should have patterns")
	})

	t.Run("kubernetes_secret in CodeMaskers", func(t *testing.T) {
		assert.Contains(t, cfg.CodeMaskers, "kubernetes_secret",
			"kubernetes_secret should exist in CodeMaskers")
	})

	t.Run("kubernetes group references kubernetes_secret", func(t *testing.T) {
		kubernetesGroup := cfg.PatternGroups["kubernetes"]
		assert.Contains(t, kubernetesGroup, "kubernetes_secret",
			"kubernetes group should reference kubernetes_secret from CodeMaskers")
	})

	t.Run("all kubernetes group members resolve", func(t *testing.T) {
		kubernetesGroup := cfg.PatternGroups["kubernetes"]
		for _, patternName := range kubernetesGroup {
			_, existsInPatterns := cfg.MaskingPatterns[patternName]
			existsInCodeMaskers := slices.Contains(cfg.CodeMaskers, patternName)

			assert.True(t, existsInPatterns || existsInCodeMaskers,
				"Pattern '%s' in kubernetes group must exist in either MaskingPatterns or CodeMaskers",
				patternName)

			// Log where each pattern is found for debugging
			if existsInPatterns {
				t.Logf("✓ Pattern '%s' found in MaskingPatterns", patternName)
			}
			if existsInCodeMaskers {
				t.Logf("✓ Pattern '%s' found in CodeMaskers", patternName)
			}
		}
	})
}
