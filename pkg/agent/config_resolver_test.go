package agent

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int { return &i }

func TestResolveAgentConfig(t *testing.T) {
	// Setup: build a Config with registries
	maxIter20 := 20
	defaults := &config.Defaults{
		LLMProvider:       "google-default",
		MaxIterations:     &maxIter20,
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
		MCPServers:        []string{"kubernetes-server"},
		IterationStrategy: config.IterationStrategyNativeThinking,
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
		assert.Equal(t, 20, resolved.MaxIterations)
		assert.Equal(t, []string{"kubernetes-server"}, resolved.MCPServers)
		assert.Equal(t, "You are a K8s agent", resolved.CustomInstructions)
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

		// Need custom-server in agent registry is not needed for this test
		// The resolver doesn't validate MCP servers exist - that's the validator's job

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.IterationStrategyReact, resolved.IterationStrategy)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, 5, resolved.MaxIterations)
		assert.Equal(t, []string{"custom-server"}, resolved.MCPServers)
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
}
