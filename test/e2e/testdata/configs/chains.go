// Package configs provides programmatic chain configurations for e2e tests.
// Configs are built in code (not YAML) for type safety and to avoid file path issues.
package configs

import (
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// intPtr is a helper to create a pointer to an int.
func intPtr(n int) *int { return &n }

// SingleStageConfig creates a minimal 1-stage, 1-agent config with MCP tools.
func SingleStageConfig() *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"DataCollector": {
				IterationStrategy: config.IterationStrategyNativeThinking,
				MaxIterations:     intPtr(3),
				MCPServers:        []string{"test-mcp"},
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test-alert"},
				Stages: []config.StageConfig{
					{Name: "investigation", Agents: []config.StageAgentConfig{
						{Name: "DataCollector"},
					}},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}

// FullFlowConfig creates a multi-stage chain with parallel agents, mixed strategies,
// executive summary, and chat follow-up.
func FullFlowConfig() *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "google-test",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"DataCollector": {
				IterationStrategy:  config.IterationStrategyNativeThinking,
				MaxIterations:      intPtr(3),
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are DataCollector, gathering system metrics and logs.",
			},
			"Investigator": {
				MaxIterations:      intPtr(3),
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are Investigator, analyzing incidents in depth.",
			},
			"Diagnostician": {
				IterationStrategy:  config.IterationStrategyNativeThinking,
				MaxIterations:      intPtr(3),
				CustomInstructions: "You are Diagnostician, providing final root cause analysis.",
			},
			"SynthesisAgent": {
				IterationStrategy: config.IterationStrategySynthesis,
				MCPServers:        []string{"test-mcp"},
			},
			"ChatAgent": {
				IterationStrategy:  config.IterationStrategyNativeThinking,
				MaxIterations:      intPtr(3),
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are ChatAgent, answering follow-up questions.",
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"google-test": {Type: config.LLMProviderTypeGoogle, Model: "gemini-test"},
			"openai-test": {Type: config.LLMProviderTypeOpenAI, Model: "gpt-test"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"kubernetes-oom": {
				AlertTypes: []string{"kubernetes-oom"},
				Stages: []config.StageConfig{
					{Name: "data-collection", Agents: []config.StageAgentConfig{
						{Name: "DataCollector"},
					}},
					{Name: "parallel-investigation", Agents: []config.StageAgentConfig{
						{Name: "Investigator", LLMProvider: "google-test", IterationStrategy: config.IterationStrategyNativeThinking},
						{Name: "Investigator", LLMProvider: "openai-test", IterationStrategy: config.IterationStrategyReact},
					}, SuccessPolicy: config.SuccessPolicyAny},
					{Name: "final-diagnosis", Agents: []config.StageAgentConfig{
						{Name: "Diagnostician"},
					}},
				},
				Chat: &config.ChatConfig{Enabled: true},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}

// TwoStageFailFastConfig creates a 2-stage chain where stage 1 failure prevents stage 2.
func TwoStageFailFastConfig() *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"Investigator": {
				IterationStrategy: config.IterationStrategyNativeThinking,
				MaxIterations:     intPtr(3),
				MCPServers:        []string{"test-mcp"},
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test-alert"},
				Stages: []config.StageConfig{
					{Name: "stage-1", Agents: []config.StageAgentConfig{{Name: "Investigator"}}},
					{Name: "stage-2", Agents: []config.StageAgentConfig{{Name: "Investigator"}}},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}

// ParallelConfig creates a single-stage chain with 2 parallel agents.
func ParallelConfig(policy config.SuccessPolicy) *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"Agent1": {
				IterationStrategy:  config.IterationStrategyNativeThinking,
				MaxIterations:      intPtr(3),
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are Agent1, specializing in infrastructure analysis.",
			},
			"Agent2": {
				IterationStrategy:  config.IterationStrategyNativeThinking,
				MaxIterations:      intPtr(3),
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are Agent2, specializing in application analysis.",
			},
			"SynthesisAgent": {
				IterationStrategy:  config.IterationStrategySynthesis,
				MCPServers:         []string{"test-mcp"},
				CustomInstructions: "You are SynthesisAgent, synthesizing parallel investigation results.",
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test-alert"},
				Stages: []config.StageConfig{
					{Name: "parallel-stage", Agents: []config.StageAgentConfig{
						{Name: "Agent1"},
						{Name: "Agent2"},
					}, SuccessPolicy: policy},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}

// ReplicaConfig creates a single-stage chain with replicas.
func ReplicaConfig(replicaCount int) *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"Investigator": {
				IterationStrategy: config.IterationStrategyNativeThinking,
				MaxIterations:     intPtr(3),
				MCPServers:        []string{"test-mcp"},
			},
			"SynthesisAgent": {
				IterationStrategy: config.IterationStrategySynthesis,
				MCPServers:        []string{"test-mcp"},
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test-alert"},
				Stages: []config.StageConfig{
					{Name: "replicated-stage", Agents: []config.StageAgentConfig{
						{Name: "Investigator"},
					}, Replicas: replicaCount},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}

// ChatConfig creates a single-stage chain with chat enabled.
func ChatConfig() *config.Config {
	cfg := SingleStageConfig()
	// Add ChatAgent to the agent registry.
	cfg.AgentRegistry = config.NewAgentRegistry(map[string]*config.AgentConfig{
		"DataCollector": {
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
			MCPServers:        []string{"test-mcp"},
		},
		"ChatAgent": {
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(3),
			MCPServers:        []string{"test-mcp"},
		},
	})
	chain, _ := cfg.ChainRegistry.Get("test-chain")
	if chain != nil {
		chain.Chat = &config.ChatConfig{Enabled: true}
	}
	return cfg
}

// ForcedConclusionConfig creates a chain with MaxIterations=2 for forced conclusion testing.
func ForcedConclusionConfig() *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyNativeThinking,
			MaxIterations:     intPtr(2),
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"Investigator": {
				IterationStrategy: config.IterationStrategyNativeThinking,
				MaxIterations:     intPtr(2),
				MCPServers:        []string{"test-mcp"},
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test-alert"},
				Stages: []config.StageConfig{
					{Name: "investigation", Agents: []config.StageAgentConfig{
						{Name: "Investigator"},
					}},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-mcp": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "mock"}},
		}),
	}
}
