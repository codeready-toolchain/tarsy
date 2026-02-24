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
	maxIter25 := 25
	defaults := &config.Defaults{
		LLMProvider:   "google-default",
		MaxIterations: &maxIter25,
		LLMBackend:    config.LLMBackendLangChain,
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
		LLMBackend:         config.LLMBackendNativeGemini,
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
		assert.Equal(t, config.AgentTypeDefault, resolved.Type)
		// Agent def overrides defaults for LLM backend
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, 25, resolved.MaxIterations)
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
			Name:          "KubernetesAgent",
			LLMBackend:    config.LLMBackendLangChain,
			LLMProvider:   "openai-default",
			MaxIterations: intPtr(5),
			MCPServers:    []string{"custom-server"},
		}

		// Note: custom-server is not in the agent registry, but that's fine.
		// The resolver doesn't validate MCP servers exist - that's the validator's job.

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.LLMBackendLangChain, resolved.LLMBackend)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, 5, resolved.MaxIterations)
		assert.Equal(t, []string{"custom-server"}, resolved.MCPServers)
	})

	t.Run("chain-level LLM backend overrides agent-def", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMBackend: config.LLMBackendLangChain,
		}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "KubernetesAgent"}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		// Chain-level langchain overrides agent-def's google-native
		assert.Equal(t, config.LLMBackendLangChain, resolved.LLMBackend)
	})

	t.Run("Type propagates from agent definition", func(t *testing.T) {
		synthCfg := &config.Config{
			Defaults: defaults,
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"SynthesisAgent": {
					Type:               config.AgentTypeSynthesis,
					CustomInstructions: "You synthesize.",
				},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "SynthesisAgent"}

		resolved, err := ResolveAgentConfig(synthCfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.AgentTypeSynthesis, resolved.Type)
	})

	t.Run("falls back to DefaultLLMBackend when no level sets backend", func(t *testing.T) {
		noBackendCfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:   "google-default",
				MaxIterations: &maxIter25,
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"PlainAgent": {},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "PlainAgent"}

		resolved, err := ResolveAgentConfig(noBackendCfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)
		assert.Equal(t, DefaultLLMBackend, resolved.LLMBackend)
		assert.True(t, resolved.LLMBackend.IsValid())
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

func TestResolveChatAgentConfig(t *testing.T) {
	maxIter25 := 25
	defaults := &config.Defaults{
		LLMProvider:   "google-default",
		MaxIterations: &maxIter25,
		LLMBackend:    config.LLMBackendLangChain,
	}

	googleProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-2.5-pro",
		APIKeyEnv: "GOOGLE_API_KEY",
	}
	openaiProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeOpenAI,
		Model:     "gpt-5",
		APIKeyEnv: "OPENAI_API_KEY",
	}

	chatAgentDef := &config.AgentConfig{
		MCPServers:         []string{"kubernetes-server"},
		CustomInstructions: "You are a chat agent",
	}

	cfg := &config.Config{
		Defaults: defaults,
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"ChatAgent":       chatAgentDef,
			"KubernetesAgent": {MCPServers: []string{"k8s-mcp"}},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
			"openai-default": openaiProvider,
		}),
	}

	t.Run("defaults to ChatAgent when chatCfg is nil", func(t *testing.T) {
		chain := &config.ChainConfig{}

		resolved, err := ResolveChatAgentConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, "ChatAgent", resolved.AgentName)
		assert.Equal(t, config.AgentTypeDefault, resolved.Type)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, 25, resolved.MaxIterations)
		assert.Equal(t, "You are a chat agent", resolved.CustomInstructions)
	})

	t.Run("chatCfg agent overrides default", func(t *testing.T) {
		chain := &config.ChainConfig{}
		chatCfg := &config.ChatConfig{
			Agent: "KubernetesAgent",
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, chatCfg)
		require.NoError(t, err)
		assert.Equal(t, "KubernetesAgent", resolved.AgentName)
	})

	t.Run("chatCfg overrides chain for LLM backend and provider", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMBackend:    config.LLMBackendLangChain,
			LLMProvider:   "google-default",
			MaxIterations: intPtr(10),
		}
		chatCfg := &config.ChatConfig{
			LLMBackend:    config.LLMBackendNativeGemini,
			LLMProvider:   "openai-default",
			MaxIterations: intPtr(3),
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, chatCfg)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, 3, resolved.MaxIterations)
	})

	t.Run("chatCfg MCP servers override chain aggregate", func(t *testing.T) {
		chain := &config.ChainConfig{
			Stages: []config.StageConfig{
				{Agents: []config.StageAgentConfig{{MCPServers: []string{"stage-server"}}}},
			},
		}
		chatCfg := &config.ChatConfig{
			MCPServers: []string{"chat-specific-server"},
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, chatCfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"chat-specific-server"}, resolved.MCPServers)
	})

	t.Run("aggregates MCP servers from inline stage-agent overrides", func(t *testing.T) {
		chain := &config.ChainConfig{
			Stages: []config.StageConfig{
				{
					MCPServers: []string{"stage-mcp-1"},
					Agents: []config.StageAgentConfig{
						{MCPServers: []string{"agent-mcp-1", "agent-mcp-2"}},
					},
				},
				{
					Agents: []config.StageAgentConfig{
						{MCPServers: []string{"agent-mcp-2", "agent-mcp-3"}},
					},
				},
			},
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, nil)
		require.NoError(t, err)
		// Should have unique union
		assert.Equal(t, []string{"stage-mcp-1", "agent-mcp-1", "agent-mcp-2", "agent-mcp-3"}, resolved.MCPServers)
	})

	t.Run("aggregates MCP servers from agent definitions in registry", func(t *testing.T) {
		// When agents are referenced by name (no inline MCP override), the
		// aggregation should look up their definitions to collect MCP servers.
		chain := &config.ChainConfig{
			Stages: []config.StageConfig{
				{
					Agents: []config.StageAgentConfig{
						{Name: "KubernetesAgent"}, // has "k8s-mcp" in registry
					},
				},
			},
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"k8s-mcp"}, resolved.MCPServers)
	})

	t.Run("aggregates MCP servers from both inline and agent definitions", func(t *testing.T) {
		chain := &config.ChainConfig{
			Stages: []config.StageConfig{
				{
					MCPServers: []string{"stage-level"},
					Agents: []config.StageAgentConfig{
						{Name: "KubernetesAgent"}, // "k8s-mcp" from registry
						{MCPServers: []string{"inline-mcp"}},
					},
				},
			},
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"stage-level", "k8s-mcp", "inline-mcp"}, resolved.MCPServers)
	})

	t.Run("chat agent with no MCP servers inherits from chain aggregation", func(t *testing.T) {
		// Mirrors real-world scenario: ChatAgent has no MCPServers in its
		// definition, gets them from the chain's investigation agents.
		chatlessCfg := &config.Config{
			Defaults: defaults,
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"ChatAgent":     {},                                         // no MCP servers
				"DataCollector": {MCPServers: []string{"monitoring-tools"}}, // investigation agent
			}),
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"google-default": googleProvider,
			}),
		}
		chain := &config.ChainConfig{
			Stages: []config.StageConfig{
				{Agents: []config.StageAgentConfig{{Name: "DataCollector"}}},
			},
		}

		resolved, err := ResolveChatAgentConfig(chatlessCfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, "ChatAgent", resolved.AgentName)
		assert.Equal(t, []string{"monitoring-tools"}, resolved.MCPServers)
	})

	t.Run("errors on nil chain", func(t *testing.T) {
		_, err := ResolveChatAgentConfig(cfg, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chain configuration cannot be nil")
	})

	t.Run("errors on unknown agent", func(t *testing.T) {
		chain := &config.ChainConfig{}
		chatCfg := &config.ChatConfig{
			Agent: "NonexistentAgent",
		}

		_, err := ResolveChatAgentConfig(cfg, chain, chatCfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestResolveScoringConfig(t *testing.T) {
	maxIter25 := 25
	defaults := &config.Defaults{
		LLMProvider:   "google-default",
		MaxIterations: &maxIter25,
		LLMBackend:    config.LLMBackendLangChain,
	}

	googleProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-2.5-pro",
		APIKeyEnv: "GOOGLE_API_KEY",
	}
	openaiProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeOpenAI,
		Model:     "gpt-5",
		APIKeyEnv: "OPENAI_API_KEY",
	}

	scoringAgentDef := &config.AgentConfig{
		MCPServers:         []string{"scoring-server"},
		Type:               config.AgentTypeScoring,
		LLMBackend:         config.LLMBackendLangChain,
		CustomInstructions: "You are a scoring agent",
	}

	cfg := &config.Config{
		Defaults: defaults,
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"ScoringAgent":    scoringAgentDef,
			"CustomScorer":    {MCPServers: []string{"custom-mcp"}, Type: config.AgentTypeScoring, LLMBackend: config.LLMBackendLangChain},
			"KubernetesAgent": {MCPServers: []string{"k8s-mcp"}},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
			"openai-default": openaiProvider,
		}),
	}

	t.Run("defaults to ScoringAgent when no config provided", func(t *testing.T) {
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, "ScoringAgent", resolved.AgentName)
		assert.Equal(t, config.AgentTypeScoring, resolved.Type)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, 25, resolved.MaxIterations)
		assert.Equal(t, "You are a scoring agent", resolved.CustomInstructions)
	})

	t.Run("scoringCfg agent overrides default", func(t *testing.T) {
		chain := &config.ChainConfig{}
		scoringCfg := &config.ScoringConfig{
			Agent: "CustomScorer",
		}

		resolved, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, "CustomScorer", resolved.AgentName)
		assert.Equal(t, config.AgentTypeScoring, resolved.Type)
	})

	t.Run("defaults.ScoringAgent used as fallback", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:   "google-default",
				MaxIterations: &maxIter25,
				ScoringAgent:  "CustomScorer",
			},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfgWithDefault, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, "CustomScorer", resolved.AgentName)
	})

	t.Run("scoringCfg agent overrides defaults.ScoringAgent", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:   "google-default",
				MaxIterations: &maxIter25,
				ScoringAgent:  "KubernetesAgent",
			},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}
		scoringCfg := &config.ScoringConfig{
			Agent: "CustomScorer",
		}

		resolved, err := ResolveScoringConfig(cfgWithDefault, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, "CustomScorer", resolved.AgentName)
	})

	t.Run("LLM backend resolution: scoringCfg overrides agentDef", func(t *testing.T) {
		chain := &config.ChainConfig{}
		scoringCfg := &config.ScoringConfig{
			Agent:      "CustomScorer",
			LLMBackend: config.LLMBackendNativeGemini,
		}

		resolved, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)
	})

	t.Run("chain LLM backend does not affect scoring", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMBackend: config.LLMBackendNativeGemini,
		}

		resolved, err := ResolveScoringConfig(cfg, chain, nil)
		require.NoError(t, err)
		// Scoring uses agentDef.LLMBackend (ScoringAgent has LLMBackendLangChain)
		assert.Equal(t, config.LLMBackendLangChain, resolved.LLMBackend)
	})

	t.Run("LLM provider resolution: scoringCfg overrides chain overrides defaults", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMProvider: "google-default",
		}
		scoringCfg := &config.ScoringConfig{
			LLMProvider: "openai-default",
		}

		resolved, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, "openai-default", resolved.LLMProviderName)
	})

	t.Run("max iterations resolution: scoringCfg overrides chain overrides agentDef overrides defaults", func(t *testing.T) {
		chain := &config.ChainConfig{
			MaxIterations: intPtr(10),
		}
		scoringCfg := &config.ScoringConfig{
			MaxIterations: intPtr(3),
		}

		resolved, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, 3, resolved.MaxIterations)
	})

	t.Run("MCP servers resolution: scoringCfg overrides chain overrides agentDef", func(t *testing.T) {
		chain := &config.ChainConfig{
			MCPServers: []string{"chain-server"},
		}
		scoringCfg := &config.ScoringConfig{
			MCPServers: []string{"scoring-specific-server"},
		}

		resolved, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"scoring-specific-server"}, resolved.MCPServers)
	})

	t.Run("MCP servers from chain override agentDef", func(t *testing.T) {
		chain := &config.ChainConfig{
			MCPServers: []string{"chain-server"},
		}

		resolved, err := ResolveScoringConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"chain-server"}, resolved.MCPServers)
	})

	t.Run("MCP servers from agentDef when no overrides", func(t *testing.T) {
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfg, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"scoring-server"}, resolved.MCPServers)
	})

	t.Run("errors on unknown agent", func(t *testing.T) {
		chain := &config.ChainConfig{}
		scoringCfg := &config.ScoringConfig{
			Agent: "NonexistentAgent",
		}

		_, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on unknown LLM provider", func(t *testing.T) {
		chain := &config.ChainConfig{}
		scoringCfg := &config.ScoringConfig{
			LLMProvider: "nonexistent-provider",
		}

		_, err := ResolveScoringConfig(cfg, chain, scoringCfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on nil chain", func(t *testing.T) {
		_, err := ResolveScoringConfig(cfg, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chain configuration cannot be nil")
	})
}
