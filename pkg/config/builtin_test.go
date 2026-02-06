package config

import (
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
	// Reset for test (use separate test to avoid affecting other tests)
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
		name     string
		agentID  string
		wantDesc string
		wantStrat IterationStrategy
	}{
		{
			name:      "KubernetesAgent",
			agentID:   "KubernetesAgent",
			wantDesc:  "Kubernetes-specialized agent using ReAct pattern",
			wantStrat: IterationStrategyReact,
		},
		{
			name:      "ChatAgent",
			agentID:   "ChatAgent",
			wantDesc:  "Built-in agent for follow-up conversations",
			wantStrat: IterationStrategyReact,
		},
		{
			name:      "SynthesisAgent",
			agentID:   "SynthesisAgent",
			wantDesc:  "Synthesizes parallel investigation results",
			wantStrat: IterationStrategySynthesis,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, exists := cfg.Agents[tt.agentID]
			require.True(t, exists, "Agent %s should exist", tt.agentID)
			assert.Equal(t, tt.wantDesc, agent.Description)
			assert.Equal(t, tt.wantStrat, agent.IterationStrategy)
		})
	}
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
			wantMinTokens: 100000,
			checkAPIKey:   true,
		},
		{
			name:          "anthropic-default",
			providerID:    "anthropic-default",
			wantType:      LLMProviderTypeAnthropic,
			wantMinTokens: 150000,
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
			wantMinTokens: 150000,
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
				_, existsInCodeMaskers := cfg.CodeMaskers[patternName]
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
		masker, exists := cfg.CodeMaskers["kubernetes_secret"]
		require.True(t, exists, "kubernetes_secret masker should exist")
		assert.NotEmpty(t, masker)
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
