package config

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test Agent Registry

func TestAgentRegistry(t *testing.T) {
	agents := map[string]*AgentConfig{
		"agent1": {MCPServers: []string{"server1"}},
		"agent2": {MCPServers: []string{"server2"}},
	}

	registry := NewAgentRegistry(agents)

	t.Run("Get existing agent", func(t *testing.T) {
		agent, err := registry.Get("agent1")
		require.NoError(t, err)
		assert.Equal(t, []string{"server1"}, agent.MCPServers)
	})

	t.Run("Get nonexistent agent", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAgentNotFound)
	})

	t.Run("Has agent", func(t *testing.T) {
		assert.True(t, registry.Has("agent1"))
		assert.False(t, registry.Has("nonexistent"))
	})

	t.Run("GetAll returns copy", func(t *testing.T) {
		all := registry.GetAll()
		assert.Len(t, all, 2)

		// Modify the returned map
		all["agent3"] = &AgentConfig{MCPServers: []string{"server3"}}

		// Original registry should be unchanged
		assert.False(t, registry.Has("agent3"))
	})
}

func TestAgentRegistryThreadSafety(_ *testing.T) {
	agents := map[string]*AgentConfig{
		"agent1": {MCPServers: []string{"server1"}},
		"agent2": {MCPServers: []string{"server2"}},
	}

	registry := NewAgentRegistry(agents)

	const goroutines = 100
	var wg sync.WaitGroup

	// Launch multiple goroutines reading concurrently
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = registry.Get("agent1")
			_ = registry.Has("agent2")
			_ = registry.GetAll()
		}()
	}

	wg.Wait()
	// If no panic, thread safety is good
}

// Test Chain Registry

func TestChainRegistry(t *testing.T) {
	chains := map[string]*ChainConfig{
		"chain1": {
			AlertTypes: []string{"alert1", "alert2"},
			Stages: []StageConfig{
				{Name: "stage1", Agents: []StageAgentConfig{{Name: "agent1"}}},
			},
		},
		"chain2": {
			AlertTypes: []string{"alert3"},
			Stages: []StageConfig{
				{Name: "stage1", Agents: []StageAgentConfig{{Name: "agent2"}}},
			},
		},
	}

	registry := NewChainRegistry(chains)

	t.Run("Get existing chain", func(t *testing.T) {
		chain, err := registry.Get("chain1")
		require.NoError(t, err)
		assert.Contains(t, chain.AlertTypes, "alert1")
	})

	t.Run("Get nonexistent chain", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrChainNotFound)
	})

	t.Run("GetByAlertType", func(t *testing.T) {
		chain, err := registry.GetByAlertType("alert1")
		require.NoError(t, err)
		assert.Contains(t, chain.AlertTypes, "alert1")

		chain, err = registry.GetByAlertType("alert3")
		require.NoError(t, err)
		assert.Contains(t, chain.AlertTypes, "alert3")
	})

	t.Run("GetByAlertType nonexistent", func(t *testing.T) {
		_, err := registry.GetByAlertType("nonexistent-alert")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "for alert type")
	})

	t.Run("Has chain", func(t *testing.T) {
		assert.True(t, registry.Has("chain1"))
		assert.False(t, registry.Has("nonexistent"))
	})

	t.Run("GetAll returns copy", func(t *testing.T) {
		all := registry.GetAll()
		assert.Len(t, all, 2)

		// Modify the returned map
		all["chain3"] = &ChainConfig{AlertTypes: []string{"alert4"}}

		// Original registry should be unchanged
		assert.False(t, registry.Has("chain3"))
	})
}

func TestChainRegistryThreadSafety(_ *testing.T) {
	chains := map[string]*ChainConfig{
		"chain1": {
			AlertTypes: []string{"alert1"},
			Stages: []StageConfig{
				{Name: "stage1", Agents: []StageAgentConfig{{Name: "agent1"}}},
			},
		},
	}

	registry := NewChainRegistry(chains)

	const goroutines = 100
	var wg sync.WaitGroup

	// Launch multiple goroutines reading concurrently
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = registry.Get("chain1")
			_, _ = registry.GetByAlertType("alert1")
			_ = registry.Has("chain1")
			_ = registry.GetAll()
		}()
	}

	wg.Wait()
	// If no panic, thread safety is good
}

// Test MCP Server Registry

func TestMCPServerRegistry(t *testing.T) {
	servers := map[string]*MCPServerConfig{
		"server1": {
			Transport: TransportConfig{Type: TransportTypeStdio, Command: "cmd1"},
		},
		"server2": {
			Transport: TransportConfig{Type: TransportTypeHTTP, URL: "http://example.com"},
		},
	}

	registry := NewMCPServerRegistry(servers)

	t.Run("Get existing server", func(t *testing.T) {
		server, err := registry.Get("server1")
		require.NoError(t, err)
		assert.Equal(t, "cmd1", server.Transport.Command)
	})

	t.Run("Get nonexistent server", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMCPServerNotFound)
	})

	t.Run("Has server", func(t *testing.T) {
		assert.True(t, registry.Has("server1"))
		assert.False(t, registry.Has("nonexistent"))
	})

	t.Run("GetAll returns copy", func(t *testing.T) {
		all := registry.GetAll()
		assert.Len(t, all, 2)

		// Modify the returned map
		all["server3"] = &MCPServerConfig{
			Transport: TransportConfig{Type: TransportTypeStdio, Command: "cmd3"},
		}

		// Original registry should be unchanged
		assert.False(t, registry.Has("server3"))
	})
}

func TestMCPServerRegistryThreadSafety(_ *testing.T) {
	servers := map[string]*MCPServerConfig{
		"server1": {
			Transport: TransportConfig{Type: TransportTypeStdio, Command: "cmd1"},
		},
	}

	registry := NewMCPServerRegistry(servers)

	const goroutines = 100
	var wg sync.WaitGroup

	// Launch multiple goroutines reading concurrently
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = registry.Get("server1")
			_ = registry.Has("server1")
			_ = registry.GetAll()
		}()
	}

	wg.Wait()
	// If no panic, thread safety is good
}

// Test LLM Provider Registry

func TestLLMProviderRegistry(t *testing.T) {
	providers := map[string]*LLMProviderConfig{
		"provider1": {
			Type:                LLMProviderTypeGoogle,
			Model:               "model1",
			MaxToolResultTokens: 100000,
		},
		"provider2": {
			Type:                LLMProviderTypeOpenAI,
			Model:               "model2",
			MaxToolResultTokens: 50000,
		},
	}

	registry := NewLLMProviderRegistry(providers)

	t.Run("Get existing provider", func(t *testing.T) {
		provider, err := registry.Get("provider1")
		require.NoError(t, err)
		assert.Equal(t, "model1", provider.Model)
	})

	t.Run("Get nonexistent provider", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrLLMProviderNotFound)
	})

	t.Run("Has provider", func(t *testing.T) {
		assert.True(t, registry.Has("provider1"))
		assert.False(t, registry.Has("nonexistent"))
	})

	t.Run("GetAll returns copy", func(t *testing.T) {
		all := registry.GetAll()
		assert.Len(t, all, 2)

		// Modify the returned map
		all["provider3"] = &LLMProviderConfig{
			Type:                LLMProviderTypeAnthropic,
			Model:               "model3",
			MaxToolResultTokens: 75000,
		}

		// Original registry should be unchanged
		assert.False(t, registry.Has("provider3"))
	})
}

func TestLLMProviderRegistryThreadSafety(_ *testing.T) {
	providers := map[string]*LLMProviderConfig{
		"provider1": {
			Type:                LLMProviderTypeGoogle,
			Model:               "model1",
			MaxToolResultTokens: 100000,
		},
	}

	registry := NewLLMProviderRegistry(providers)

	const goroutines = 100
	var wg sync.WaitGroup

	// Launch multiple goroutines reading concurrently
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = registry.Get("provider1")
			_ = registry.Has("provider1")
			_ = registry.GetAll()
		}()
	}

	wg.Wait()
	// If no panic, thread safety is good
}
