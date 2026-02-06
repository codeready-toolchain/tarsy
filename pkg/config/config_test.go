package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigConvenienceMethods tests all convenience methods on Config
func TestConfigConvenienceMethods(t *testing.T) {
	agents := map[string]*AgentConfig{
		"test-agent": {MCPServers: []string{"test-server"}},
	}
	chains := map[string]*ChainConfig{
		"test-chain": {
			AlertTypes: []string{"test-alert"},
			Stages: []StageConfig{
				{Name: "stage1", Agents: []StageAgentConfig{{Name: "test-agent"}}},
			},
		},
	}
	mcpServers := map[string]*MCPServerConfig{
		"test-server": {
			Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"},
		},
	}
	llmProviders := map[string]*LLMProviderConfig{
		"test-provider": {
			Type:                LLMProviderTypeGoogle,
			Model:               "test-model",
			MaxToolResultTokens: 100000,
		},
	}

	cfg := &Config{
		configDir:           "/test/config",
		AgentRegistry:       NewAgentRegistry(agents),
		ChainRegistry:       NewChainRegistry(chains),
		MCPServerRegistry:   NewMCPServerRegistry(mcpServers),
		LLMProviderRegistry: NewLLMProviderRegistry(llmProviders),
	}

	t.Run("ConfigDir", func(t *testing.T) {
		assert.Equal(t, "/test/config", cfg.ConfigDir())
	})

	t.Run("GetAgent success", func(t *testing.T) {
		agent, err := cfg.GetAgent("test-agent")
		require.NoError(t, err)
		assert.NotNil(t, agent)
		assert.Equal(t, []string{"test-server"}, agent.MCPServers)
	})

	t.Run("GetAgent not found", func(t *testing.T) {
		_, err := cfg.GetAgent("nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("GetChain success", func(t *testing.T) {
		chain, err := cfg.GetChain("test-chain")
		require.NoError(t, err)
		assert.NotNil(t, chain)
		assert.Equal(t, []string{"test-alert"}, chain.AlertTypes)
	})

	t.Run("GetChain not found", func(t *testing.T) {
		_, err := cfg.GetChain("nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("GetChainByAlertType success", func(t *testing.T) {
		chain, err := cfg.GetChainByAlertType("test-alert")
		require.NoError(t, err)
		assert.NotNil(t, chain)
		assert.Contains(t, chain.AlertTypes, "test-alert")
	})

	t.Run("GetChainByAlertType not found", func(t *testing.T) {
		_, err := cfg.GetChainByAlertType("nonexistent-alert")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "for alert type")
	})

	t.Run("GetMCPServer success", func(t *testing.T) {
		server, err := cfg.GetMCPServer("test-server")
		require.NoError(t, err)
		assert.NotNil(t, server)
		assert.Equal(t, TransportTypeStdio, server.Transport.Type)
	})

	t.Run("GetMCPServer not found", func(t *testing.T) {
		_, err := cfg.GetMCPServer("nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("GetLLMProvider success", func(t *testing.T) {
		provider, err := cfg.GetLLMProvider("test-provider")
		require.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, "test-model", provider.Model)
	})

	t.Run("GetLLMProvider not found", func(t *testing.T) {
		_, err := cfg.GetLLMProvider("nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestConfigStats(t *testing.T) {
	cfg := &Config{
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"a1": {}, "a2": {}}),
		ChainRegistry:       NewChainRegistry(map[string]*ChainConfig{"c1": {}}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{"m1": {}, "m2": {}, "m3": {}}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{"l1": {}, "l2": {}, "l3": {}, "l4": {}}),
	}

	stats := cfg.Stats()
	assert.Equal(t, 2, stats.Agents)
	assert.Equal(t, 1, stats.Chains)
	assert.Equal(t, 3, stats.MCPServers)
	assert.Equal(t, 4, stats.LLMProviders)
}
