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
			config.AgentNameKubernetes: agentDef,
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
			"openai-default": openaiProvider,
		}),
	}

	t.Run("uses defaults when no overrides", func(t *testing.T) {
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: config.AgentNameKubernetes}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.AgentNameKubernetes, resolved.AgentName)
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
			Name:          config.AgentNameKubernetes,
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
		agentConfig := config.StageAgentConfig{Name: config.AgentNameKubernetes}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		// Chain-level langchain overrides agent-def's google-native
		assert.Equal(t, config.LLMBackendLangChain, resolved.LLMBackend)
	})

	t.Run("Type propagates from agent definition", func(t *testing.T) {
		synthCfg := &config.Config{
			Defaults: defaults,
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameSynthesis: {
					Type:               config.AgentTypeSynthesis,
					CustomInstructions: "You synthesize.",
				},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: config.AgentNameSynthesis}

		resolved, err := ResolveAgentConfig(synthCfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.AgentTypeSynthesis, resolved.Type)
	})

	t.Run("stage-agent type overrides agent definition type", func(t *testing.T) {
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{
			Name: config.AgentNameKubernetes,
			Type: config.AgentTypeOrchestrator,
		}

		resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.NoError(t, err)

		assert.Equal(t, config.AgentTypeOrchestrator, resolved.Type)
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

	t.Run("nil Defaults does not panic", func(t *testing.T) {
		nilDefaultsCfg := &config.Config{
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"PlainAgent": {},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: "PlainAgent"}

		// No panic — returns a proper error because no LLM provider is configured
		_, err := ResolveAgentConfig(nilDefaultsCfg, chain, stageConfig, agentConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
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
			Name:        config.AgentNameKubernetes,
			LLMProvider: "nonexistent-provider",
		}

		_, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on nil chain", func(t *testing.T) {
		stageConfig := config.StageConfig{}
		agentConfig := config.StageAgentConfig{Name: config.AgentNameKubernetes}

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
			agentConfig := config.StageAgentConfig{Name: config.AgentNameKubernetes}

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
			agentConfig := config.StageAgentConfig{Name: config.AgentNameKubernetes}

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
				Name:       config.AgentNameKubernetes,
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
				Name:       config.AgentNameKubernetes,
				MCPServers: []string{}, // empty, should not override
			}

			resolved, err := ResolveAgentConfig(cfg, chain, stageConfig, agentConfig)
			require.NoError(t, err)
			assert.Equal(t, []string{"chain-server"}, resolved.MCPServers)
		})
	})

	t.Run("NativeTools resolution", func(t *testing.T) {
		providerWithNative := &config.LLMProviderConfig{
			Type:                config.LLMProviderTypeGoogle,
			Model:               "gemini-2.5-pro",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000,
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch:  true,
				config.GoogleNativeToolCodeExecution: false,
				config.GoogleNativeToolURLContext:    true,
			},
		}

		t.Run("agent native_tools override provider defaults per-key", func(t *testing.T) {
			ntCfg := &config.Config{
				Defaults: &config.Defaults{LLMProvider: "google-native"},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"TestAgent": {
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolCodeExecution: true, // override false → true
						},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-native": providerWithNative,
				}),
			}

			resolved, err := ResolveAgentConfig(ntCfg, &config.ChainConfig{}, config.StageConfig{}, config.StageAgentConfig{Name: "TestAgent"})
			require.NoError(t, err)

			assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
			assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution])
			assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolURLContext])
			// Must be a clone, not the shared registry pointer
			assert.NotSame(t, providerWithNative, resolved.LLMProvider)
		})

		t.Run("agent native_tools merge not replace", func(t *testing.T) {
			ntCfg := &config.Config{
				Defaults: &config.Defaults{LLMProvider: "google-native"},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"TestAgent": {
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolGoogleSearch: false, // override true → false
						},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-native": providerWithNative,
				}),
			}

			resolved, err := ResolveAgentConfig(ntCfg, &config.ChainConfig{}, config.StageConfig{}, config.StageAgentConfig{Name: "TestAgent"})
			require.NoError(t, err)

			assert.False(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
			assert.False(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution]) // unchanged
			assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolURLContext])     // unchanged
		})

		t.Run("no agent native_tools returns same provider pointer", func(t *testing.T) {
			ntCfg := &config.Config{
				Defaults: &config.Defaults{LLMProvider: "google-native"},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"TestAgent": {}, // no NativeTools
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-native": providerWithNative,
				}),
			}

			resolved, err := ResolveAgentConfig(ntCfg, &config.ChainConfig{}, config.StageConfig{}, config.StageAgentConfig{Name: "TestAgent"})
			require.NoError(t, err)
			assert.Same(t, providerWithNative, resolved.LLMProvider)
		})

		t.Run("agent adds native tools to provider with none", func(t *testing.T) {
			providerNoNative := &config.LLMProviderConfig{
				Type:                config.LLMProviderTypeGoogle,
				Model:               "gemini-2.5-pro",
				APIKeyEnv:           "GOOGLE_API_KEY",
				MaxToolResultTokens: 950000,
			}
			ntCfg := &config.Config{
				Defaults: &config.Defaults{LLMProvider: "google-bare"},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"TestAgent": {
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolCodeExecution: true,
						},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-bare": providerNoNative,
				}),
			}

			resolved, err := ResolveAgentConfig(ntCfg, &config.ChainConfig{}, config.StageConfig{}, config.StageAgentConfig{Name: "TestAgent"})
			require.NoError(t, err)
			assert.NotSame(t, providerNoNative, resolved.LLMProvider)
			assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution])
			assert.Len(t, resolved.LLMProvider.NativeTools, 1)
		})

		t.Run("clone does not mutate original provider", func(t *testing.T) {
			ntCfg := &config.Config{
				Defaults: &config.Defaults{LLMProvider: "google-native"},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"TestAgent": {
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolCodeExecution: true,
						},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-native": providerWithNative,
				}),
			}

			_, err := ResolveAgentConfig(ntCfg, &config.ChainConfig{}, config.StageConfig{}, config.StageAgentConfig{Name: "TestAgent"})
			require.NoError(t, err)

			// Original provider must be unchanged
			assert.False(t, providerWithNative.NativeTools[config.GoogleNativeToolCodeExecution])
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
			config.AgentNameChat:       chatAgentDef,
			config.AgentNameKubernetes: {MCPServers: []string{"k8s-mcp"}},
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
		assert.Equal(t, config.AgentNameChat, resolved.AgentName)
		assert.Equal(t, config.AgentTypeDefault, resolved.Type)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, 25, resolved.MaxIterations)
		assert.Equal(t, "You are a chat agent", resolved.CustomInstructions)
	})

	t.Run("chatCfg agent overrides default", func(t *testing.T) {
		chain := &config.ChainConfig{}
		chatCfg := &config.ChatConfig{
			Agent: config.AgentNameKubernetes,
		}

		resolved, err := ResolveChatAgentConfig(cfg, chain, chatCfg)
		require.NoError(t, err)
		assert.Equal(t, config.AgentNameKubernetes, resolved.AgentName)
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
						{Name: config.AgentNameKubernetes}, // has "k8s-mcp" in registry
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
						{Name: config.AgentNameKubernetes}, // "k8s-mcp" from registry
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
				config.AgentNameChat: {},                                         // no MCP servers
				"DataCollector":      {MCPServers: []string{"monitoring-tools"}}, // investigation agent
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
		assert.Equal(t, config.AgentNameChat, resolved.AgentName)
		assert.Equal(t, []string{"monitoring-tools"}, resolved.MCPServers)
	})

	t.Run("errors on nil chain", func(t *testing.T) {
		_, err := ResolveChatAgentConfig(cfg, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "chain configuration cannot be nil")
	})

	t.Run("agent native_tools override provider defaults", func(t *testing.T) {
		providerWithNative := &config.LLMProviderConfig{
			Type:      config.LLMProviderTypeGoogle,
			Model:     "gemini-2.5-pro",
			APIKeyEnv: "GOOGLE_API_KEY",
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch:  true,
				config.GoogleNativeToolCodeExecution: false,
			},
		}
		chatCfg := &config.Config{
			Defaults: &config.Defaults{LLMProvider: "google-nt"},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameChat: {
					NativeTools: map[config.GoogleNativeTool]bool{
						config.GoogleNativeToolCodeExecution: true,
					},
				},
			}),
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"google-nt": providerWithNative,
			}),
		}

		resolved, err := ResolveChatAgentConfig(chatCfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution])
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
		assert.NotSame(t, providerWithNative, resolved.LLMProvider)
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
			config.AgentNameScoring:    scoringAgentDef,
			"CustomScorer":             {MCPServers: []string{"custom-mcp"}, Type: config.AgentTypeScoring, LLMBackend: config.LLMBackendLangChain},
			config.AgentNameKubernetes: {MCPServers: []string{"k8s-mcp"}},
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
		assert.Equal(t, config.AgentNameScoring, resolved.AgentName)
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

	t.Run("defaults.Scoring.Agent used as fallback", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:   "google-default",
				MaxIterations: &maxIter25,
				Scoring:       &config.ScoringConfig{Agent: "CustomScorer"},
			},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfgWithDefault, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, "CustomScorer", resolved.AgentName)
	})

	t.Run("scoringCfg agent overrides defaults.Scoring.Agent", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:   "google-default",
				MaxIterations: &maxIter25,
				Scoring:       &config.ScoringConfig{Agent: config.AgentNameKubernetes},
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

	t.Run("defaults.Scoring LLM provider overrides defaults.LLMProvider", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
				Scoring:     &config.ScoringConfig{LLMProvider: "openai-default"},
			},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfgWithDefault, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, "openai-default", resolved.LLMProviderName)
	})

	t.Run("defaults.Scoring LLM backend overrides defaults.LLMBackend", func(t *testing.T) {
		cfgWithDefault := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
				LLMBackend:  config.LLMBackendLangChain,
				Scoring:     &config.ScoringConfig{LLMBackend: config.LLMBackendNativeGemini},
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameScoring: {Type: config.AgentTypeScoring},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		chain := &config.ChainConfig{}

		resolved, err := ResolveScoringConfig(cfgWithDefault, chain, nil)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)
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

	t.Run("agent native_tools override provider defaults", func(t *testing.T) {
		providerWithNative := &config.LLMProviderConfig{
			Type:      config.LLMProviderTypeGoogle,
			Model:     "gemini-2.5-pro",
			APIKeyEnv: "GOOGLE_API_KEY",
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch: true,
				config.GoogleNativeToolURLContext:   true,
			},
		}
		scorCfg := &config.Config{
			Defaults: &config.Defaults{LLMProvider: "google-nt"},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameScoring: {
					Type:       config.AgentTypeScoring,
					LLMBackend: config.LLMBackendLangChain,
					NativeTools: map[config.GoogleNativeTool]bool{
						config.GoogleNativeToolURLContext: false,
					},
				},
			}),
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"google-nt": providerWithNative,
			}),
		}

		resolved, err := ResolveScoringConfig(scorCfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
		assert.False(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolURLContext])
		assert.NotSame(t, providerWithNative, resolved.LLMProvider)
	})

	t.Run("LLM provider full hierarchy: scoringCfg overrides chain overrides defaults.Scoring overrides defaults", func(t *testing.T) {
		cfgFull := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
				Scoring:     &config.ScoringConfig{LLMProvider: "openai-default"},
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameScoring: {Type: config.AgentTypeScoring},
			}),
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"google-default": googleProvider,
				"openai-default": openaiProvider,
			}),
		}

		// defaults.Scoring overrides defaults
		resolved, err := ResolveScoringConfig(cfgFull, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "openai-default", resolved.LLMProviderName)

		// chain overrides defaults.Scoring
		resolved, err = ResolveScoringConfig(cfgFull, &config.ChainConfig{LLMProvider: "google-default"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "google-default", resolved.LLMProviderName)

		// scoringCfg overrides chain
		resolved, err = ResolveScoringConfig(cfgFull,
			&config.ChainConfig{LLMProvider: "google-default"},
			&config.ScoringConfig{LLMProvider: "openai-default"},
		)
		require.NoError(t, err)
		assert.Equal(t, "openai-default", resolved.LLMProviderName)
	})

	t.Run("LLM backend full hierarchy: scoringCfg overrides defaults.Scoring overrides agentDef overrides defaults", func(t *testing.T) {
		cfgFull := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
				LLMBackend:  config.LLMBackendLangChain,
				Scoring:     &config.ScoringConfig{LLMBackend: config.LLMBackendNativeGemini},
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				config.AgentNameScoring: {Type: config.AgentTypeScoring},
			}),
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}

		// defaults.Scoring overrides defaults + agentDef (both LangChain)
		resolved, err := ResolveScoringConfig(cfgFull, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)

		// scoringCfg overrides defaults.Scoring
		resolved, err = ResolveScoringConfig(cfgFull, &config.ChainConfig{},
			&config.ScoringConfig{LLMBackend: config.LLMBackendLangChain},
		)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendLangChain, resolved.LLMBackend)
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

func TestResolveFallbackProviders(t *testing.T) {
	defaultsFallback := []config.FallbackProviderEntry{
		{Provider: "defaults-fb", Backend: config.LLMBackendLangChain},
	}
	chainFallback := []config.FallbackProviderEntry{
		{Provider: "chain-fb", Backend: config.LLMBackendNativeGemini},
	}
	stageFallback := []config.FallbackProviderEntry{
		{Provider: "stage-fb", Backend: config.LLMBackendLangChain},
	}
	agentFallback := []config.FallbackProviderEntry{
		{Provider: "agent-fb", Backend: config.LLMBackendNativeGemini},
	}

	googleProvider := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeGoogle,
		Model:               "gemini-2.5-pro",
		APIKeyEnv:           "GOOGLE_API_KEY",
		MaxToolResultTokens: 950000,
	}

	baseCfg := &config.Config{
		Defaults: &config.Defaults{
			LLMProvider: "google-default",
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent":             {},
			config.AgentNameScoring: {Type: config.AgentTypeScoring},
			config.AgentNameChat:    {},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
		}),
	}

	t.Run("no fallback at any level returns nil", func(t *testing.T) {
		resolved, err := ResolveAgentConfig(baseCfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Nil(t, resolved.FallbackProviders)
	})

	t.Run("defaults fallback inherited", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Equal(t, defaultsFallback, resolved.FallbackProviders)
	})

	t.Run("chain overrides defaults", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{FallbackProviders: chainFallback},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Equal(t, chainFallback, resolved.FallbackProviders)
	})

	t.Run("stage overrides chain", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{FallbackProviders: chainFallback},
			config.StageConfig{FallbackProviders: stageFallback},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Equal(t, stageFallback, resolved.FallbackProviders)
	})

	t.Run("agent overrides stage", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{FallbackProviders: chainFallback},
			config.StageConfig{FallbackProviders: stageFallback},
			config.StageAgentConfig{Name: "TestAgent", FallbackProviders: agentFallback},
		)
		require.NoError(t, err)
		assert.Equal(t, agentFallback, resolved.FallbackProviders)
	})

	t.Run("empty list explicitly clears inherited", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{FallbackProviders: []config.FallbackProviderEntry{}},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.NotNil(t, resolved.FallbackProviders, "explicit empty should be non-nil")
		assert.Empty(t, resolved.FallbackProviders)
	})

	t.Run("nil slice does not override", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Equal(t, defaultsFallback, resolved.FallbackProviders)
	})

	t.Run("chat inherits from defaults and chain", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveChatAgentConfig(cfg,
			&config.ChainConfig{FallbackProviders: chainFallback},
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, chainFallback, resolved.FallbackProviders)
	})

	t.Run("chat inherits defaults when chain has none", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveChatAgentConfig(cfg,
			&config.ChainConfig{},
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, defaultsFallback, resolved.FallbackProviders)
	})

	t.Run("scoring inherits from defaults and chain", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: defaultsFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveScoringConfig(cfg,
			&config.ChainConfig{FallbackProviders: chainFallback},
			nil,
		)
		require.NoError(t, err)
		assert.Equal(t, chainFallback, resolved.FallbackProviders)
	})

	t.Run("multi-entry list preserves order", func(t *testing.T) {
		multiFallback := []config.FallbackProviderEntry{
			{Provider: "first", Backend: config.LLMBackendLangChain},
			{Provider: "second", Backend: config.LLMBackendNativeGemini},
			{Provider: "third", Backend: config.LLMBackendLangChain},
		}
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider:       "google-default",
				FallbackProviders: multiFallback,
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		require.Len(t, resolved.FallbackProviders, 3)
		assert.Equal(t, "first", resolved.FallbackProviders[0].Provider)
		assert.Equal(t, "second", resolved.FallbackProviders[1].Provider)
		assert.Equal(t, "third", resolved.FallbackProviders[2].Provider)
	})

	t.Run("stage fallback does not leak into chat", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveChatAgentConfig(cfg,
			&config.ChainConfig{},
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, resolved.FallbackProviders,
			"chat should not inherit stage-level fallback — only defaults and chain")
	})

	t.Run("stage fallback does not leak into scoring", func(t *testing.T) {
		cfg := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
			},
			AgentRegistry:       baseCfg.AgentRegistry,
			LLMProviderRegistry: baseCfg.LLMProviderRegistry,
		}

		resolved, err := ResolveScoringConfig(cfg,
			&config.ChainConfig{},
			nil,
		)
		require.NoError(t, err)
		assert.Nil(t, resolved.FallbackProviders,
			"scoring should not inherit stage-level fallback — only defaults and chain")
	})
}

func TestResolvedFallbackProviders(t *testing.T) {
	primaryProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-primary",
		APIKeyEnv: "GOOGLE_API_KEY",
	}
	fallbackProvider1 := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-fallback-1",
		APIKeyEnv: "GOOGLE_API_KEY",
	}
	fallbackProvider2 := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeOpenAI,
		Model:     "gpt-fallback-2",
		APIKeyEnv: "OPENAI_API_KEY",
	}

	cfg := &config.Config{
		Defaults: &config.Defaults{
			LLMProvider: "primary",
			FallbackProviders: []config.FallbackProviderEntry{
				{Provider: "fb-1", Backend: config.LLMBackendNativeGemini},
				{Provider: "fb-2", Backend: config.LLMBackendLangChain},
			},
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent":             {},
			config.AgentNameScoring: {Type: config.AgentTypeScoring},
			config.AgentNameChat:    {},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"primary": primaryProvider,
			"fb-1":    fallbackProvider1,
			"fb-2":    fallbackProvider2,
		}),
	}

	t.Run("ResolveAgentConfig populates ResolvedFallbackProviders", func(t *testing.T) {
		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		require.Len(t, resolved.ResolvedFallbackProviders, 2)

		assert.Equal(t, "fb-1", resolved.ResolvedFallbackProviders[0].ProviderName)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.ResolvedFallbackProviders[0].Backend)
		assert.Equal(t, "gemini-fallback-1", resolved.ResolvedFallbackProviders[0].Config.Model)

		assert.Equal(t, "fb-2", resolved.ResolvedFallbackProviders[1].ProviderName)
		assert.Equal(t, config.LLMBackendLangChain, resolved.ResolvedFallbackProviders[1].Backend)
		assert.Equal(t, "gpt-fallback-2", resolved.ResolvedFallbackProviders[1].Config.Model)
	})

	t.Run("ResolveChatAgentConfig populates ResolvedFallbackProviders", func(t *testing.T) {
		resolved, err := ResolveChatAgentConfig(cfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		require.Len(t, resolved.ResolvedFallbackProviders, 2)
		assert.Equal(t, "fb-1", resolved.ResolvedFallbackProviders[0].ProviderName)
		assert.Equal(t, "fb-2", resolved.ResolvedFallbackProviders[1].ProviderName)
	})

	t.Run("ResolveScoringConfig populates ResolvedFallbackProviders", func(t *testing.T) {
		resolved, err := ResolveScoringConfig(cfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		require.Len(t, resolved.ResolvedFallbackProviders, 2)
		assert.Equal(t, "fb-1", resolved.ResolvedFallbackProviders[0].ProviderName)
	})

	t.Run("nil FallbackProviders yields nil ResolvedFallbackProviders", func(t *testing.T) {
		cfgNoFallback := &config.Config{
			Defaults:            &config.Defaults{LLMProvider: "primary"},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		resolved, err := ResolveAgentConfig(cfgNoFallback,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Nil(t, resolved.ResolvedFallbackProviders)
	})

	t.Run("agent native tool overrides applied to fallback entries", func(t *testing.T) {
		// Provider fb-1 has google_search enabled in its registry config
		fbWithNativeTools := &config.LLMProviderConfig{
			Type:      config.LLMProviderTypeGoogle,
			Model:     "gemini-fb-native",
			APIKeyEnv: "GOOGLE_API_KEY",
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch: true,
			},
		}
		cfgNative := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "primary",
				FallbackProviders: []config.FallbackProviderEntry{
					{Provider: "fb-native", Backend: config.LLMBackendNativeGemini},
				},
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"NativeAgent": {
					NativeTools: map[config.GoogleNativeTool]bool{
						config.GoogleNativeToolCodeExecution: true,
						config.GoogleNativeToolGoogleSearch:  false, // override: disable search
					},
				},
			}),
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"primary":   primaryProvider,
				"fb-native": fbWithNativeTools,
			}),
		}

		resolved, err := ResolveAgentConfig(cfgNative,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "NativeAgent"},
		)
		require.NoError(t, err)
		require.Len(t, resolved.ResolvedFallbackProviders, 1)

		fbConfig := resolved.ResolvedFallbackProviders[0].Config
		assert.False(t, fbConfig.NativeTools[config.GoogleNativeToolGoogleSearch],
			"agent override should disable google_search on fallback")
		assert.True(t, fbConfig.NativeTools[config.GoogleNativeToolCodeExecution],
			"agent override should enable code_execution on fallback")

		// Primary provider should also have the same overrides
		assert.False(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
		assert.True(t, resolved.LLMProvider.NativeTools[config.GoogleNativeToolCodeExecution])
	})

	t.Run("missing provider in registry is skipped", func(t *testing.T) {
		cfgMissing := &config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "primary",
				FallbackProviders: []config.FallbackProviderEntry{
					{Provider: "nonexistent", Backend: config.LLMBackendLangChain},
					{Provider: "fb-1", Backend: config.LLMBackendNativeGemini},
				},
			},
			AgentRegistry:       cfg.AgentRegistry,
			LLMProviderRegistry: cfg.LLMProviderRegistry,
		}
		resolved, err := ResolveAgentConfig(cfgMissing,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		require.Len(t, resolved.ResolvedFallbackProviders, 1, "nonexistent provider should be skipped")
		assert.Equal(t, "fb-1", resolved.ResolvedFallbackProviders[0].ProviderName)
	})
}

func TestResolveAdaptiveTimeoutDefaults(t *testing.T) {
	googleProvider := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeGoogle,
		Model:               "gemini-2.5-pro",
		APIKeyEnv:           "GOOGLE_API_KEY",
		MaxToolResultTokens: 950000,
	}

	cfg := &config.Config{
		Defaults: &config.Defaults{LLMProvider: "google-default"},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent":                 {},
			config.AgentNameScoring:     {Type: config.AgentTypeScoring},
			config.AgentNameChat:        {},
			config.AgentNameExecSummary: {Type: config.AgentTypeExecSummary},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default": googleProvider,
		}),
	}

	t.Run("ResolveAgentConfig sets timeout defaults", func(t *testing.T) {
		resolved, err := ResolveAgentConfig(cfg,
			&config.ChainConfig{},
			config.StageConfig{},
			config.StageAgentConfig{Name: "TestAgent"},
		)
		require.NoError(t, err)
		assert.Equal(t, DefaultInitialResponseTimeout, resolved.InitialResponseTimeout)
		assert.Equal(t, DefaultStallTimeout, resolved.StallTimeout)
	})

	t.Run("ResolveChatAgentConfig sets timeout defaults", func(t *testing.T) {
		resolved, err := ResolveChatAgentConfig(cfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.Equal(t, DefaultInitialResponseTimeout, resolved.InitialResponseTimeout)
		assert.Equal(t, DefaultStallTimeout, resolved.StallTimeout)
	})

	t.Run("ResolveScoringConfig sets timeout defaults", func(t *testing.T) {
		resolved, err := ResolveScoringConfig(cfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.Equal(t, DefaultInitialResponseTimeout, resolved.InitialResponseTimeout)
		assert.Equal(t, DefaultStallTimeout, resolved.StallTimeout)
	})

	t.Run("ResolveExecSummaryConfig sets timeout defaults", func(t *testing.T) {
		resolved, err := ResolveExecSummaryConfig(cfg, &config.ChainConfig{})
		require.NoError(t, err)
		assert.Equal(t, DefaultInitialResponseTimeout, resolved.InitialResponseTimeout)
		assert.Equal(t, DefaultStallTimeout, resolved.StallTimeout)
	})
}

func TestResolveExecSummaryConfig(t *testing.T) {
	defaults := &config.Defaults{
		LLMProvider: "google-default",
		LLMBackend:  config.LLMBackendLangChain,
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
	anthropicProvider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeAnthropic,
		Model:     "claude-sonnet",
		APIKeyEnv: "ANTHROPIC_API_KEY",
	}

	cfg := &config.Config{
		Defaults: defaults,
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			// ExecSummaryAgent is a built-in; we register it here for the test.
			config.AgentNameExecSummary: {Type: config.AgentTypeExecSummary, LLMBackend: config.LLMBackendLangChain},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-default":    googleProvider,
			"openai-default":    openaiProvider,
			"anthropic-default": anthropicProvider,
		}),
	}

	t.Run("uses ExecSummaryAgent type", func(t *testing.T) {
		resolved, err := ResolveExecSummaryConfig(cfg, &config.ChainConfig{})
		require.NoError(t, err)
		assert.Equal(t, config.AgentNameExecSummary, resolved.AgentName)
		assert.Equal(t, config.AgentTypeExecSummary, resolved.Type)
	})

	t.Run("defaults to defaults.LLMProvider when no chain override", func(t *testing.T) {
		resolved, err := ResolveExecSummaryConfig(cfg, &config.ChainConfig{})
		require.NoError(t, err)
		assert.Equal(t, googleProvider, resolved.LLMProvider)
		assert.Equal(t, "google-default", resolved.LLMProviderName)
	})

	t.Run("chain.LLMProvider overrides defaults", func(t *testing.T) {
		chain := &config.ChainConfig{LLMProvider: "openai-default"}
		resolved, err := ResolveExecSummaryConfig(cfg, chain)
		require.NoError(t, err)
		assert.Equal(t, openaiProvider, resolved.LLMProvider)
		assert.Equal(t, "openai-default", resolved.LLMProviderName)
	})

	t.Run("chain.ExecutiveSummaryProvider overrides chain.LLMProvider", func(t *testing.T) {
		chain := &config.ChainConfig{
			LLMProvider:              "openai-default",
			ExecutiveSummaryProvider: "anthropic-default",
		}
		resolved, err := ResolveExecSummaryConfig(cfg, chain)
		require.NoError(t, err)
		assert.Equal(t, anthropicProvider, resolved.LLMProvider)
		assert.Equal(t, "anthropic-default", resolved.LLMProviderName)
	})

	t.Run("chain.LLMBackend is included in backend resolution", func(t *testing.T) {
		chain := &config.ChainConfig{LLMBackend: config.LLMBackendNativeGemini}
		resolved, err := ResolveExecSummaryConfig(cfg, chain)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendNativeGemini, resolved.LLMBackend)
	})

	t.Run("fallback providers resolved from chain", func(t *testing.T) {
		chain := &config.ChainConfig{
			FallbackProviders: []config.FallbackProviderEntry{
				{Provider: "openai-default", Backend: config.LLMBackendLangChain},
			},
		}
		resolved, err := ResolveExecSummaryConfig(cfg, chain)
		require.NoError(t, err)
		require.Len(t, resolved.FallbackProviders, 1)
		assert.Equal(t, "openai-default", resolved.FallbackProviders[0].Provider)
	})

	t.Run("nil chain returns error", func(t *testing.T) {
		_, err := ResolveExecSummaryConfig(cfg, nil)
		assert.Error(t, err)
	})

	t.Run("unknown provider returns error", func(t *testing.T) {
		chain := &config.ChainConfig{ExecutiveSummaryProvider: "nonexistent-provider"}
		_, err := ResolveExecSummaryConfig(cfg, chain)
		assert.Error(t, err)
	})
}

func TestResolveSkills(t *testing.T) {
	registry := config.NewSkillRegistry(map[string]*config.SkillConfig{
		"kubernetes-basics": {
			Name:        "kubernetes-basics",
			Description: "Core K8s troubleshooting",
			Body:        "Check pod status first.",
		},
		"networking": {
			Name:        "networking",
			Description: "Network debugging patterns",
			Body:        "Use dig and traceroute.",
		},
		"storage": {
			Name:        "storage",
			Description: "PV/PVC troubleshooting",
			Body:        "Check storage class.",
		},
	})

	t.Run("nil allowlist gives all skills", func(t *testing.T) {
		agentDef := &config.AgentConfig{Skills: nil}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Empty(t, required)
		assert.Len(t, onDemand, 3)
		names := make([]string, len(onDemand))
		for i, s := range onDemand {
			names[i] = s.Name
		}
		assert.Contains(t, names, "kubernetes-basics")
		assert.Contains(t, names, "networking")
		assert.Contains(t, names, "storage")
	})

	t.Run("empty allowlist gives no skills", func(t *testing.T) {
		empty := []string{}
		agentDef := &config.AgentConfig{Skills: &empty}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Empty(t, required)
		assert.Empty(t, onDemand)
	})

	t.Run("explicit allowlist filters skills", func(t *testing.T) {
		allowed := []string{"kubernetes-basics", "networking"}
		agentDef := &config.AgentConfig{Skills: &allowed}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Empty(t, required)
		assert.Len(t, onDemand, 2)
		names := make([]string, len(onDemand))
		for i, s := range onDemand {
			names[i] = s.Name
		}
		assert.Contains(t, names, "kubernetes-basics")
		assert.Contains(t, names, "networking")
	})

	t.Run("required skills split from on-demand", func(t *testing.T) {
		agentDef := &config.AgentConfig{
			Skills:         nil,
			RequiredSkills: []string{"kubernetes-basics"},
		}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		require.Len(t, required, 1)
		assert.Equal(t, "kubernetes-basics", required[0].Name)
		assert.Equal(t, "Check pod status first.", required[0].Body)

		assert.Len(t, onDemand, 2)
		for _, s := range onDemand {
			assert.NotEqual(t, "kubernetes-basics", s.Name, "required skill should not appear in on-demand")
		}
	})

	t.Run("required skills with allowlist", func(t *testing.T) {
		allowed := []string{"kubernetes-basics", "networking"}
		agentDef := &config.AgentConfig{
			Skills:         &allowed,
			RequiredSkills: []string{"kubernetes-basics"},
		}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		require.Len(t, required, 1)
		assert.Equal(t, "kubernetes-basics", required[0].Name)
		require.Len(t, onDemand, 1)
		assert.Equal(t, "networking", onDemand[0].Name)
	})

	t.Run("nil registry returns nil", func(t *testing.T) {
		agentDef := &config.AgentConfig{Skills: nil}
		cfg := &config.Config{SkillRegistry: nil}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Nil(t, required)
		assert.Nil(t, onDemand)
	})

	t.Run("empty registry returns nil", func(t *testing.T) {
		agentDef := &config.AgentConfig{Skills: nil}
		cfg := &config.Config{SkillRegistry: config.NewSkillRegistry(nil)}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Nil(t, required)
		assert.Nil(t, onDemand)
	})

	t.Run("unknown skill in allowlist is skipped", func(t *testing.T) {
		allowed := []string{"kubernetes-basics", "nonexistent"}
		agentDef := &config.AgentConfig{Skills: &allowed}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Empty(t, required)
		require.Len(t, onDemand, 1)
		assert.Equal(t, "kubernetes-basics", onDemand[0].Name)
	})

	t.Run("all effective skills required leaves on-demand empty", func(t *testing.T) {
		allowed := []string{"kubernetes-basics", "networking"}
		agentDef := &config.AgentConfig{
			Skills:         &allowed,
			RequiredSkills: []string{"kubernetes-basics", "networking"},
		}
		cfg := &config.Config{SkillRegistry: registry}

		required, onDemand := resolveSkills(cfg, agentDef)

		assert.Len(t, required, 2)
		assert.Empty(t, onDemand)
	})

	t.Run("on-demand entries have description populated", func(t *testing.T) {
		agentDef := &config.AgentConfig{Skills: nil}
		cfg := &config.Config{SkillRegistry: registry}

		_, onDemand := resolveSkills(cfg, agentDef)

		for _, entry := range onDemand {
			assert.NotEmpty(t, entry.Description, "skill %q should have description", entry.Name)
		}
		descriptions := make(map[string]string, len(onDemand))
		for _, entry := range onDemand {
			descriptions[entry.Name] = entry.Description
		}
		assert.Equal(t, "Core K8s troubleshooting", descriptions["kubernetes-basics"])
		assert.Equal(t, "Network debugging patterns", descriptions["networking"])
		assert.Equal(t, "PV/PVC troubleshooting", descriptions["storage"])
	})
}

func TestResolveAgentConfig_PopulatesSkills(t *testing.T) {
	registry := config.NewSkillRegistry(map[string]*config.SkillConfig{
		"kubernetes-basics": {
			Name:        "kubernetes-basics",
			Description: "Core K8s troubleshooting",
			Body:        "Check pod status first.",
		},
		"networking": {
			Name:        "networking",
			Description: "Network debugging",
			Body:        "Use dig.",
		},
	})

	provider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-2.5-pro",
		APIKeyEnv: "GOOGLE_API_KEY",
	}

	cfg := &config.Config{
		Defaults: &config.Defaults{LLMProvider: "default-provider"},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"test-agent": {
				RequiredSkills: []string{"kubernetes-basics"},
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"default-provider": provider,
		}),
		SkillRegistry: registry,
	}

	resolved, err := ResolveAgentConfig(
		cfg,
		&config.ChainConfig{},
		config.StageConfig{},
		config.StageAgentConfig{Name: "test-agent"},
	)
	require.NoError(t, err)

	require.Len(t, resolved.RequiredSkillContent, 1)
	assert.Equal(t, "kubernetes-basics", resolved.RequiredSkillContent[0].Name)
	assert.Equal(t, "Check pod status first.", resolved.RequiredSkillContent[0].Body)

	require.Len(t, resolved.OnDemandSkills, 1)
	assert.Equal(t, "networking", resolved.OnDemandSkills[0].Name)
}

func TestResolveChatAgentConfig_PopulatesSkills(t *testing.T) {
	registry := config.NewSkillRegistry(map[string]*config.SkillConfig{
		"kubernetes-basics": {
			Name:        "kubernetes-basics",
			Description: "Core K8s troubleshooting",
			Body:        "Check pod status first.",
		},
	})

	provider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-2.5-pro",
		APIKeyEnv: "GOOGLE_API_KEY",
	}

	cfg := &config.Config{
		Defaults: &config.Defaults{LLMProvider: "default-provider"},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			config.AgentNameChat: {},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"default-provider": provider,
		}),
		SkillRegistry: registry,
	}

	resolved, err := ResolveChatAgentConfig(cfg, &config.ChainConfig{}, nil)
	require.NoError(t, err)

	assert.Len(t, resolved.OnDemandSkills, 1)
	assert.Equal(t, "kubernetes-basics", resolved.OnDemandSkills[0].Name)
}

func TestMetaAgents_DoNotGetSkills(t *testing.T) {
	provider := &config.LLMProviderConfig{
		Type:      config.LLMProviderTypeGoogle,
		Model:     "gemini-2.5-pro",
		APIKeyEnv: "GOOGLE_API_KEY",
	}
	registry := config.NewSkillRegistry(map[string]*config.SkillConfig{
		"kubernetes-basics": {
			Name:        "kubernetes-basics",
			Description: "Core K8s troubleshooting",
			Body:        "Check pod status first.",
		},
	})

	cfg := &config.Config{
		Defaults: &config.Defaults{LLMProvider: "default-provider"},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			config.AgentNameScoring:     {Type: config.AgentTypeScoring},
			config.AgentNameExecSummary: {Type: config.AgentTypeExecSummary},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"default-provider": provider,
		}),
		SkillRegistry: registry,
	}

	t.Run("scoring agent excluded from skills", func(t *testing.T) {
		resolved, err := ResolveScoringConfig(cfg, &config.ChainConfig{}, nil)
		require.NoError(t, err)
		assert.Empty(t, resolved.RequiredSkillContent)
		assert.Empty(t, resolved.OnDemandSkills)
	})

	t.Run("exec summary agent excluded from skills", func(t *testing.T) {
		resolved, err := ResolveExecSummaryConfig(cfg, &config.ChainConfig{})
		require.NoError(t, err)
		assert.Empty(t, resolved.RequiredSkillContent)
		assert.Empty(t, resolved.OnDemandSkills)
	})
}

func TestResolvedAgentConfig_OnDemandSkillNameSet(t *testing.T) {
	tests := []struct {
		name     string
		skills   []SkillCatalogEntry
		expected map[string]struct{}
	}{
		{
			name:     "nil skills",
			skills:   nil,
			expected: map[string]struct{}{},
		},
		{
			name:     "empty skills",
			skills:   []SkillCatalogEntry{},
			expected: map[string]struct{}{},
		},
		{
			name: "multiple skills",
			skills: []SkillCatalogEntry{
				{Name: "kubernetes-debugging", Description: "K8s debugging"},
				{Name: "aws-rds", Description: "RDS troubleshooting"},
			},
			expected: map[string]struct{}{
				"kubernetes-debugging": {},
				"aws-rds":              {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ResolvedAgentConfig{OnDemandSkills: tt.skills}
			assert.Equal(t, tt.expected, cfg.OnDemandSkillNameSet())
		})
	}
}
