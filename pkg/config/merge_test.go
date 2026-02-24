package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeAgents(t *testing.T) {
	builtin := map[string]BuiltinAgentConfig{
		"builtin-agent": {
			Description: "A built-in agent",
			MCPServers:  []string{"builtin-server"},
		},
		"builtin-with-instructions": {
			Description:        "Synthesis agent",
			Type:               AgentTypeSynthesis,
			MCPServers:         []string{"builtin-server"},
			CustomInstructions: "Built-in custom instructions for synthesis.",
		},
		"override-me": {
			Description: "Override target",
			MCPServers:  []string{"old-server"},
		},
	}

	user := map[string]AgentConfig{
		"user-agent": {
			MCPServers:         []string{"user-server"},
			CustomInstructions: "User instructions",
		},
		"override-me": {
			MCPServers:         []string{"new-server"},
			LLMBackend:         LLMBackendNativeGemini,
			CustomInstructions: "Overridden instructions",
		},
	}

	result := mergeAgents(builtin, user)

	// Should have 4 agents total
	assert.Len(t, result, 4)

	// Built-in agent should exist
	assert.Contains(t, result, "builtin-agent")
	assert.Equal(t, []string{"builtin-server"}, result["builtin-agent"].MCPServers)
	assert.Equal(t, "A built-in agent", result["builtin-agent"].Description)

	// Built-in agent with custom instructions should preserve them
	assert.Contains(t, result, "builtin-with-instructions")
	assert.Equal(t, AgentTypeSynthesis, result["builtin-with-instructions"].Type)
	assert.Equal(t, "Built-in custom instructions for synthesis.", result["builtin-with-instructions"].CustomInstructions)

	// User agent should exist
	assert.Contains(t, result, "user-agent")
	assert.Equal(t, []string{"user-server"}, result["user-agent"].MCPServers)
	assert.Equal(t, "User instructions", result["user-agent"].CustomInstructions)

	// Overridden agent should have user values
	assert.Contains(t, result, "override-me")
	assert.Equal(t, []string{"new-server"}, result["override-me"].MCPServers)
	assert.Equal(t, LLMBackendNativeGemini, result["override-me"].LLMBackend)
	assert.Equal(t, "Overridden instructions", result["override-me"].CustomInstructions)
}

func TestMergeMCPServers(t *testing.T) {
	builtin := map[string]MCPServerConfig{
		"builtin-server": {
			Transport: TransportConfig{
				Type:    TransportTypeStdio,
				Command: "builtin-cmd",
			},
			Instructions: "Built-in instructions",
		},
		"override-me": {
			Transport: TransportConfig{
				Type:    TransportTypeStdio,
				Command: "old-cmd",
			},
		},
	}

	user := map[string]MCPServerConfig{
		"user-server": {
			Transport: TransportConfig{
				Type: TransportTypeHTTP,
				URL:  "http://user.example.com",
			},
			Instructions: "User instructions",
		},
		"override-me": {
			Transport: TransportConfig{
				Type:    TransportTypeStdio,
				Command: "new-cmd",
			},
			Instructions: "Overridden instructions",
		},
	}

	result := mergeMCPServers(builtin, user)

	// Should have 3 servers total
	assert.Len(t, result, 3)

	// Built-in server should exist
	assert.Contains(t, result, "builtin-server")
	assert.Equal(t, TransportTypeStdio, result["builtin-server"].Transport.Type)
	assert.Equal(t, "builtin-cmd", result["builtin-server"].Transport.Command)

	// User server should exist
	assert.Contains(t, result, "user-server")
	assert.Equal(t, TransportTypeHTTP, result["user-server"].Transport.Type)
	assert.Equal(t, "http://user.example.com", result["user-server"].Transport.URL)

	// Overridden server should have user values
	assert.Contains(t, result, "override-me")
	assert.Equal(t, "new-cmd", result["override-me"].Transport.Command)
	assert.Equal(t, "Overridden instructions", result["override-me"].Instructions)
}

func TestMergeChains(t *testing.T) {
	builtin := map[string]ChainConfig{
		"builtin-chain": {
			AlertTypes:  []string{"builtin-alert"},
			Description: "Built-in chain",
			Stages: []StageConfig{
				{Name: "builtin-stage", Agents: []StageAgentConfig{{Name: "builtin-agent"}}},
			},
		},
		"override-me": {
			AlertTypes: []string{"old-alert"},
			Stages: []StageConfig{
				{Name: "old-stage", Agents: []StageAgentConfig{{Name: "old-agent"}}},
			},
		},
	}

	user := map[string]ChainConfig{
		"user-chain": {
			AlertTypes:  []string{"user-alert"},
			Description: "User chain",
			Stages: []StageConfig{
				{Name: "user-stage", Agents: []StageAgentConfig{{Name: "user-agent"}}},
			},
		},
		"override-me": {
			AlertTypes:  []string{"new-alert"},
			Description: "Overridden chain",
			Stages: []StageConfig{
				{Name: "new-stage", Agents: []StageAgentConfig{{Name: "new-agent"}}},
			},
		},
	}

	result := mergeChains(builtin, user)

	// Should have 3 chains total
	assert.Len(t, result, 3)

	// Built-in chain should exist
	assert.Contains(t, result, "builtin-chain")
	assert.Equal(t, []string{"builtin-alert"}, result["builtin-chain"].AlertTypes)
	assert.Equal(t, "Built-in chain", result["builtin-chain"].Description)

	// User chain should exist
	assert.Contains(t, result, "user-chain")
	assert.Equal(t, []string{"user-alert"}, result["user-chain"].AlertTypes)
	assert.Equal(t, "User chain", result["user-chain"].Description)

	// Overridden chain should have user values
	assert.Contains(t, result, "override-me")
	assert.Equal(t, []string{"new-alert"}, result["override-me"].AlertTypes)
	assert.Equal(t, "Overridden chain", result["override-me"].Description)
	assert.Len(t, result["override-me"].Stages, 1)
	assert.Equal(t, "new-stage", result["override-me"].Stages[0].Name)
}

func TestMergeLLMProviders(t *testing.T) {
	builtin := map[string]LLMProviderConfig{
		"builtin-provider": {
			Type:                LLMProviderTypeGoogle,
			Model:               "builtin-model",
			APIKeyEnv:           "BUILTIN_KEY",
			MaxToolResultTokens: 100000,
		},
		"override-me": {
			Type:                LLMProviderTypeOpenAI,
			Model:               "old-model",
			MaxToolResultTokens: 50000,
		},
	}

	user := map[string]LLMProviderConfig{
		"user-provider": {
			Type:                LLMProviderTypeAnthropic,
			Model:               "user-model",
			APIKeyEnv:           "USER_KEY",
			MaxToolResultTokens: 150000,
		},
		"override-me": {
			Type:                LLMProviderTypeOpenAI,
			Model:               "new-model",
			APIKeyEnv:           "NEW_KEY",
			MaxToolResultTokens: 200000,
		},
	}

	result := mergeLLMProviders(builtin, user)

	// Should have 3 providers total
	assert.Len(t, result, 3)

	// Built-in provider should exist
	assert.Contains(t, result, "builtin-provider")
	assert.Equal(t, LLMProviderTypeGoogle, result["builtin-provider"].Type)
	assert.Equal(t, "builtin-model", result["builtin-provider"].Model)
	assert.Equal(t, 100000, result["builtin-provider"].MaxToolResultTokens)

	// User provider should exist
	assert.Contains(t, result, "user-provider")
	assert.Equal(t, LLMProviderTypeAnthropic, result["user-provider"].Type)
	assert.Equal(t, "user-model", result["user-provider"].Model)
	assert.Equal(t, 150000, result["user-provider"].MaxToolResultTokens)

	// Overridden provider should have user values
	assert.Contains(t, result, "override-me")
	assert.Equal(t, "new-model", result["override-me"].Model)
	assert.Equal(t, "NEW_KEY", result["override-me"].APIKeyEnv)
	assert.Equal(t, 200000, result["override-me"].MaxToolResultTokens)
}

// TestMergeEmptyMaps tests merging with empty built-in or user configs
func TestMergeEmptyMaps(t *testing.T) {
	t.Run("empty user agents", func(t *testing.T) {
		builtin := map[string]BuiltinAgentConfig{
			"agent1": {MCPServers: []string{"server1"}},
		}
		result := mergeAgents(builtin, map[string]AgentConfig{})
		assert.Len(t, result, 1)
		assert.Contains(t, result, "agent1")
	})

	t.Run("empty builtin agents", func(t *testing.T) {
		user := map[string]AgentConfig{
			"agent1": {MCPServers: []string{"server1"}},
		}
		result := mergeAgents(map[string]BuiltinAgentConfig{}, user)
		assert.Len(t, result, 1)
		assert.Contains(t, result, "agent1")
	})

	t.Run("both empty", func(t *testing.T) {
		result := mergeAgents(map[string]BuiltinAgentConfig{}, map[string]AgentConfig{})
		assert.Len(t, result, 0)
	})

	t.Run("nil builtin MCP servers", func(t *testing.T) {
		result := mergeMCPServers(nil, map[string]MCPServerConfig{
			"server1": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "cmd"}},
		})
		assert.Len(t, result, 1)
	})

	t.Run("nil builtin chains", func(t *testing.T) {
		result := mergeChains(nil, map[string]ChainConfig{
			"chain1": {AlertTypes: []string{"alert1"}},
		})
		assert.Len(t, result, 1)
	})

	t.Run("nil builtin LLM providers", func(t *testing.T) {
		result := mergeLLMProviders(nil, map[string]LLMProviderConfig{
			"provider1": {Type: LLMProviderTypeGoogle, Model: "model1", MaxToolResultTokens: 100000},
		})
		assert.Len(t, result, 1)
	})
}
