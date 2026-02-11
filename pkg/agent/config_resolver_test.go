package agent

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int { return &i }

func TestResolveBackend(t *testing.T) {
	assert.Equal(t, BackendLangChain, ResolveBackend(config.IterationStrategyReact))
	assert.Equal(t, BackendGoogleNative, ResolveBackend(config.IterationStrategyNativeThinking))
	assert.Equal(t, BackendLangChain, ResolveBackend(config.IterationStrategySynthesis))
	assert.Equal(t, BackendGoogleNative, ResolveBackend(config.IterationStrategySynthesisNativeThinking))
	// Unknown strategy defaults to langchain
	assert.Equal(t, BackendLangChain, ResolveBackend("unknown"))
}

func TestResolveAgentConfig(t *testing.T) {
	// Setup: build a Config with registries
	maxIter25 := 25
	defaults := &config.Defaults{
		LLMProvider:       "google-default",
		MaxIterations:     &maxIter25,
		IterationStrategy: config.IterationStrategyReact,
	}

	googleProvider := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeGoogle,
		Model:               "gemini-2.5-pro",
		APIKeyEnv:           "GOOGLE_API_KEY",
		MaxToolResultTokens: 950000,
	}
	openaiProvider := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeOpenAI,
		Model:               "gpt-5",
		APIKeyEnv:           "OPENAI_API_KEY",
		MaxToolResultTokens: 250000,
	}

	agentDef := &config.AgentConfig{
		MCPServers:         []string{"kubernetes-server"},
		IterationStrategy:  config.IterationStrategyNativeThinking,
		CustomInstructions: "You are a K8s agent",
	}

	cfg := &config.Config{
		Defaults: defaults,
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"KubernetesAgent": agentDef,
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
			"openai-default": openaiProvider,
		}),
	}

	t.Run("uses defaults when no overrides", func(t *testing.T) {
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, "KubernetesAgent", resolved.AgentName)
		// Agent def overrides defaults for iteration strategy
		assert.Equal(t, config.IterationStrategyNativeThinking, resolved.IterationStrategy)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, 25, resolved.MaxIterations)
		assert.Equal(t, []string{"kubernetes-server"}, resolved.MCPServers)
		assert.Equal(t, "You are a K8s agent", resolved.CustomInstructions)
		// Backend resolved from iteration strategy
		assert.Equal(t, BackendGoogleNative, resolved.Backend)
	})

	t.Run("stage-agent overrides chain and agent def", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMProvider:   "google-default",
			MaxIterations: intPtr(15),
		}
		stageConfig := config.StageConfig{
			MaxIterations: intPtr(10),
		}
		agentConfig := config.StageAgentConfig{
			Name:              "KubernetesAgent",
			IterationStrategy: config.IterationStrategyReact,
			LLMProvider:       "openai-default",
			MaxIterations:     intPtr(5),
			MCPServers:        []string{"custom-server"},
		}

		// Note: custom-server is not in the agent registry, but that's fine.
		// The resolver doesn't validate MCP servers exist - that's the validator's job.

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.IterationStrategyReact, resolved.IterationStrategy)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, 5, resolved.MaxIterations)
		assert.Equal(t, []string{"custom-server"}, resolved.MCPServers)
		assert.Equal(t, BackendLangChain, resolved.Backend)
	})

	t.Run("chain-level strategy overrides agent-def", func(t *testing.T) {
		chain := &config.ChainConfig{
			IterationStrategy: config.IterationStrategyReact,
		}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		// Chain-level react overrides agent-def's native-thinking
		assert.Equal(t, config.IterationStrategyReact, resolved.IterationStrategy)
		assert.Equal(t, BackendLangChain, resolved.Backend)
	})

	t.Run("errors on unknown agent", func(t *testing.T) {
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "UnknownAgent"}

		_, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on unknown LLM provider", func(t *testing.T) {
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{
			Name:        "KubernetesAgent",
			LLMProvider: "nonexistent-provider",
		}

		_, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on nil chain", func(t *testing.T) {
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

		_, err := ResolveAgentConfig(cfg, nil, stageConfig, agentConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chain configuration cannot be nil")
	})

	t.Run("MCPServers follows five-level precedence", func(t *testing.T) {
		// Test that chain overrides agent-def
		t.Run("chain overrides agent-def", func(t *testing.T) {
			chain := &config.ChainConfig{
				MCPServers: []string{"chain-server"},
			}
			stageConfig := config.StageConfig{}
			agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

			resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"chain-server"}, resolved.MCPServers)
		})

		// Test that stage overrides chain
		t.Run("stage overrides chain and agent-def", func(t *testing.T) {
			chain := &config.ChainConfig{
				MCPServers: []string{"chain-server"},
			}
			stageConfig := config.StageConfig{
				MCPServers: []string{"stage-server"},
			}
			agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

			resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"stage-server"}, resolved.MCPServers)
		})

		// Test that stage-agent overrides all
		t.Run("stage-agent overrides stage, chain, and agent-def", func(t *testing.T) {
			chain := &config.ChainConfig{
				MCPServers: []string{"chain-server"},
			}
			stageConfig := config.StageConfig{
				MCPServers: []string{"stage-server"},
			}
			agentConfig := config.StageAgentConfig{
				Name:       "KubernetesAgent",
				MCPServers: []string{"stage-agent-server"},
			}

			resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"stage-agent-server"}, resolved.MCPServers)
		})

		// Test that empty lists don't override
		t.Run("empty lists don't override previous levels", func(t *testing.T) {
			chain := &config.ChainConfig{
				MCPServers: []string{"chain-server"},
			}
			stageConfig := config.StageConfig{
				MCPServers: []string{}, // empty, should not override
			}
			agentConfig := config.StageAgentConfig{
				Name:       "KubernetesAgent",
				MCPServers: []string{}, // empty, should not override
			}

			resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"chain-server"}, resolved.MCPServers)
		})
	})
}
