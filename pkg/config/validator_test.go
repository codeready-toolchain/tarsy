package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAgents(t *testing.T) {
	tests := []struct {
		name    string
		agents  map[string]*AgentConfig
		servers map[string]*MCPServerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid agent",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{"test-server"},
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"},
				},
			},
			wantErr: false,
		},
		{
			name: "agent with no MCP servers is valid",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "agent with nil MCP servers is valid",
			agents: map[string]*AgentConfig{
				"toolless-agent": {
					MCPServers: nil,
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "synthesis agent without MCP servers is valid",
			agents: map[string]*AgentConfig{
				"synth": {
					Type:       AgentTypeSynthesis,
					MCPServers: nil,
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "agent with invalid MCP server reference",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{"nonexistent-server"},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "MCP server 'nonexistent-server' not found",
		},
		{
			name: "agent with invalid type",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{"test-server"},
					Type:       "invalid-type",
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"},
				},
			},
			wantErr: true,
			errMsg:  "invalid agent type",
		},
		{
			name: "agent with invalid LLM backend",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{"test-server"},
					LLMBackend: "invalid-backend",
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"},
				},
			},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "agent with valid native tools",
			agents: map[string]*AgentConfig{
				"test-agent": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch:  true,
						GoogleNativeToolCodeExecution: false,
					},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "agent with invalid native tool key",
			agents: map[string]*AgentConfig{
				"test-agent": {
					NativeTools: map[GoogleNativeTool]bool{
						"invalid_tool": true,
					},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "invalid native tool",
		},
		{
			name: "orchestrator agent with orchestrator config is valid",
			agents: map[string]*AgentConfig{
				"my-orch": {
					Type:         AgentTypeOrchestrator,
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(3)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "non-orchestrator agent with orchestrator config is invalid",
			agents: map[string]*AgentConfig{
				"regular": {
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(3)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "orchestrator config only valid on orchestrator agents",
		},
		{
			name: "orchestrator config with zero max_concurrent_agents",
			agents: map[string]*AgentConfig{
				"orch": {
					Type:         AgentTypeOrchestrator,
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(0)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "must be at least 1",
		},
		{
			name: "orchestrator config with negative agent_timeout",
			agents: map[string]*AgentConfig{
				"orch": {
					Type:         AgentTypeOrchestrator,
					Orchestrator: &OrchestratorConfig{AgentTimeout: durPtr(-1 * time.Second)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "must be positive",
		},
		{
			name: "orchestrator config with zero max_budget",
			agents: map[string]*AgentConfig{
				"orch": {
					Type:         AgentTypeOrchestrator,
					Orchestrator: &OrchestratorConfig{MaxBudget: durPtr(0)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "must be positive",
		},
		{
			name: "orchestrator agent without orchestrator config is valid",
			agents: map[string]*AgentConfig{
				"orch": {Type: AgentTypeOrchestrator},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "synthesis agent with orchestrator config is invalid",
			agents: map[string]*AgentConfig{
				"synth": {
					Type:         AgentTypeSynthesis,
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(3)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "orchestrator config only valid on orchestrator agents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AgentRegistry:     NewAgentRegistry(tt.agents),
				MCPServerRegistry: NewMCPServerRegistry(tt.servers),
			}

			validator := NewValidator(cfg)
			err := validator.validateAgents()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateChains(t *testing.T) {
	tests := []struct {
		name      string
		chains    map[string]*ChainConfig
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid chain",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name: "stage1",
							Agents: []StageAgentConfig{
								{Name: "test-agent"},
							},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   false,
		},
		{
			name: "chain with no alert types",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "at least one alert type required",
		},
		{
			name: "chain with no stages",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{},
				},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "at least one stage required",
		},
		{
			name: "chain with invalid agent reference",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name: "stage1",
							Agents: []StageAgentConfig{
								{Name: "nonexistent-agent"},
							},
						},
					},
				},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "agent 'nonexistent-agent' not found",
		},
		{
			name: "chain with invalid LLM provider",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "invalid-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "LLM provider 'invalid-provider' not found",
		},
		{
			name: "multiple chains with duplicate alert type",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"critical", "warning"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
				"chain2": {
					AlertTypes: []string{"info", "critical"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "alert type 'critical' is already mapped to chain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ChainRegistry:       NewChainRegistry(tt.chains),
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
			}

			validator := NewValidator(cfg)
			err := validator.validateChains()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateMCPServers(t *testing.T) {
	tests := []struct {
		name    string
		servers map[string]*MCPServerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid stdio server",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test-command",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid http server",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type: TransportTypeHTTP,
						URL:  "http://example.com",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid transport type",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type: "invalid",
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid transport type",
		},
		{
			name: "stdio server missing command",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type: TransportTypeStdio,
					},
				},
			},
			wantErr: true,
			errMsg:  "command required for stdio transport",
		},
		{
			name: "http server missing url",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type: TransportTypeHTTP,
					},
				},
			},
			wantErr: true,
			errMsg:  "url required for http transport",
		},
		{
			name: "invalid pattern group",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					DataMasking: &MaskingConfig{
						Enabled:       true,
						PatternGroups: []string{"nonexistent-group"},
					},
				},
			},
			wantErr: true,
			errMsg:  "pattern group 'nonexistent-group' not found",
		},
		{
			name: "invalid individual pattern",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					DataMasking: &MaskingConfig{
						Enabled:  true,
						Patterns: []string{"nonexistent-pattern"},
					},
				},
			},
			wantErr: true,
			errMsg:  "pattern 'nonexistent-pattern' not found",
		},
		{
			name: "valid summarization with explicit threshold",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						Enabled:             true,
						SizeThresholdTokens: 5000,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid summarization threshold too low",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						Enabled:             true,
						SizeThresholdTokens: 50,
					},
				},
			},
			wantErr: true,
			errMsg:  "must be at least 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				MCPServerRegistry: NewMCPServerRegistry(tt.servers),
			}

			validator := NewValidator(cfg)
			err := validator.validateMCPServers()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateLLMProviders(t *testing.T) {
	tests := []struct {
		name      string
		providers map[string]*LLMProviderConfig
		env       map[string]string
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid provider with API key set",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					APIKeyEnv:           "TEST_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{"TEST_API_KEY": "test-key"},
			wantErr: false,
		},
		{
			name: "unreferenced provider with missing API key does not error",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					APIKeyEnv:           "MISSING_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{},
			wantErr: false, // No error because provider is not referenced by any chain
		},
		{
			name: "provider with invalid type",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                "invalid",
					Model:               "test-model",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "invalid provider type",
		},
		{
			name: "provider with empty model",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "model required",
		},
		{
			name: "provider with low max tokens",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					MaxToolResultTokens: 500, // Less than 1000
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "must be at least 1000",
		},
		{
			name: "VertexAI provider with both environment variables set",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "TEST_GCP_PROJECT",
					LocationEnv:         "TEST_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_PROJECT":  "my-project",
				"TEST_GCP_LOCATION": "us-central1",
			},
			wantErr: false,
		},
		{
			name: "VertexAI provider with missing ProjectEnv",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "MISSING_GCP_PROJECT",
					LocationEnv:         "TEST_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_LOCATION": "us-central1",
			},
			wantErr: false, // No error because provider is not referenced
		},
		{
			name: "VertexAI provider with missing LocationEnv",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "TEST_GCP_PROJECT",
					LocationEnv:         "MISSING_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_PROJECT": "my-project",
			},
			wantErr: false, // No error because provider is not referenced
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg := &Config{
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
			}

			validator := NewValidator(cfg)
			err := validator.validateLLMProviders()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateLLMProvidersOnlyReferencedProviders(t *testing.T) {
	tests := []struct {
		name      string
		chains    map[string]*ChainConfig
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		env       map[string]string
		wantErr   bool
		errMsg    string
	}{
		{
			name: "unreferenced providers do not require env vars",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"used-provider": {
					Type:                LLMProviderTypeOpenAI,
					Model:               "o4-mini",
					APIKeyEnv:           "USED_API_KEY",
					MaxToolResultTokens: 100000,
				},
				"unused-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "gemini-pro",
					APIKeyEnv:           "UNUSED_API_KEY", // This env var is NOT set, but should not cause error
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{}, // No env vars set
			wantErr: false,               // Should NOT error because no provider is referenced
		},
		{
			name: "chain-level referenced provider requires env var",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "used-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"used-provider": {
					Type:                LLMProviderTypeOpenAI,
					Model:               "o4-mini",
					APIKeyEnv:           "USED_API_KEY",
					MaxToolResultTokens: 100000,
				},
				"unused-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "gemini-pro",
					APIKeyEnv:           "UNUSED_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{}, // USED_API_KEY is not set
			wantErr: true,
			errMsg:  "environment variable USED_API_KEY is not set",
		},
		{
			name: "chat-level referenced provider requires env var",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
					Chat: &ChatConfig{
						Enabled:     true,
						Agent:       "test-agent",
						LLMProvider: "chat-provider",
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"chat-provider": {
					Type:                LLMProviderTypeAnthropic,
					Model:               "claude-sonnet-4-5-20250929",
					APIKeyEnv:           "CHAT_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{}, // CHAT_API_KEY is not set
			wantErr: true,
			errMsg:  "environment variable CHAT_API_KEY is not set",
		},
		{
			name: "agent-level referenced provider requires env var",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name: "stage1",
							Agents: []StageAgentConfig{
								{
									Name:        "test-agent",
									LLMProvider: "agent-provider",
								},
							},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"agent-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "gemini-pro",
					APIKeyEnv:           "AGENT_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{}, // AGENT_API_KEY is not set
			wantErr: true,
			errMsg:  "environment variable AGENT_API_KEY is not set",
		},
		{
			name: "synthesis-level referenced provider requires env var",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
							Synthesis: &SynthesisConfig{
								Agent:       "test-agent",
								LLMProvider: "synthesis-provider",
							},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"synthesis-provider": {
					Type:                LLMProviderTypeXAI,
					Model:               "grok-1",
					APIKeyEnv:           "SYNTHESIS_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{}, // SYNTHESIS_API_KEY is not set
			wantErr: true,
			errMsg:  "environment variable SYNTHESIS_API_KEY is not set",
		},
		{
			name: "only one referenced provider needs env var, others don't",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "used-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"used-provider": {
					Type:                LLMProviderTypeOpenAI,
					Model:               "o4-mini",
					APIKeyEnv:           "USED_API_KEY",
					MaxToolResultTokens: 100000,
				},
				"unused-provider-1": {
					Type:                LLMProviderTypeGoogle,
					Model:               "gemini-pro",
					APIKeyEnv:           "UNUSED_API_KEY_1",
					MaxToolResultTokens: 100000,
				},
				"unused-provider-2": {
					Type:                LLMProviderTypeAnthropic,
					Model:               "claude-sonnet-4-5-20250929",
					APIKeyEnv:           "UNUSED_API_KEY_2",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"USED_API_KEY": "valid-key",
				// UNUSED_API_KEY_1 and UNUSED_API_KEY_2 are not set, but should not cause error
			},
			wantErr: false,
		},
		{
			name: "referenced VertexAI provider with missing ProjectEnv",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "vertexai-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"vertexai-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "MISSING_GCP_PROJECT",
					LocationEnv:         "TEST_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_LOCATION": "us-central1",
				// MISSING_GCP_PROJECT is not set
			},
			wantErr: true,
			errMsg:  "environment variable MISSING_GCP_PROJECT is not set",
		},
		{
			name: "referenced VertexAI provider with missing LocationEnv",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "vertexai-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"vertexai-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "TEST_GCP_PROJECT",
					LocationEnv:         "MISSING_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_PROJECT": "my-project",
				// MISSING_GCP_LOCATION is not set
			},
			wantErr: true,
			errMsg:  "environment variable MISSING_GCP_LOCATION is not set",
		},
		{
			name: "referenced VertexAI provider with all env vars set",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "vertexai-provider",
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"vertexai-provider": {
					Type:                LLMProviderTypeVertexAI,
					Model:               "gemini-pro",
					ProjectEnv:          "TEST_GCP_PROJECT",
					LocationEnv:         "TEST_GCP_LOCATION",
					MaxToolResultTokens: 100000,
				},
			},
			env: map[string]string{
				"TEST_GCP_PROJECT":  "my-project",
				"TEST_GCP_LOCATION": "us-central1",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg := &Config{
				ChainRegistry:       NewChainRegistry(tt.chains),
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
				MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{"test": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}}}),
			}

			validator := NewValidator(cfg)
			err := validator.validateLLMProviders()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	err := NewValidationError("agent", "test-agent", "mcp_servers", assert.AnError)

	assert.Equal(t, "agent", err.Component)
	assert.Equal(t, "test-agent", err.ID)
	assert.Equal(t, "mcp_servers", err.Field)
	assert.Contains(t, err.Error(), "agent 'test-agent'")
	assert.Contains(t, err.Error(), "mcp_servers")
	assert.Same(t, assert.AnError, err.Unwrap())
}

// TestValidateStageComprehensive tests validateStage with all edge cases
func TestValidateStageComprehensive(t *testing.T) {
	maxIter15 := 15
	maxIter0 := 0
	negativeReplicas := -1

	tests := []struct {
		name      string
		stage     StageConfig
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		servers   map[string]*MCPServerConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid stage with all fields",
			stage: StageConfig{
				Name:          "stage1",
				Agents:        []StageAgentConfig{{Name: "test-agent"}},
				Replicas:      2,
				SuccessPolicy: SuccessPolicyAll,
				MaxIterations: &maxIter15,
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		{
			name: "stage with empty name",
			stage: StageConfig{
				Name:   "",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			servers:   map[string]*MCPServerConfig{},
			wantErr:   true,
			errMsg:    "stage name required",
		},
		{
			name: "stage with no agents",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			servers:   map[string]*MCPServerConfig{},
			wantErr:   true,
			errMsg:    "must specify at least one agent",
		},
		{
			name: "stage with invalid agent LLM backend",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:       "test-agent",
						LLMBackend: "invalid-backend",
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "invalid llm_backend",
		},
		{
			name: "stage with agent-level invalid LLM provider",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:        "test-agent",
						LLMProvider: "nonexistent-provider",
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "LLM provider 'nonexistent-provider' which is not found",
		},
		{
			name: "stage with agent-level invalid max iterations",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:          "test-agent",
						MaxIterations: &maxIter0,
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "max_iterations must be at least 1",
		},
		{
			name: "stage with agent-level invalid MCP server",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:       "test-agent",
						MCPServers: []string{"nonexistent-server"},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "MCP server 'nonexistent-server' which is not found",
		},
		{
			name: "stage with negative replicas",
			stage: StageConfig{
				Name:     "stage1",
				Agents:   []StageAgentConfig{{Name: "test-agent"}},
				Replicas: negativeReplicas,
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "replicas must be positive",
		},
		{
			name: "stage with invalid success policy",
			stage: StageConfig{
				Name:          "stage1",
				Agents:        []StageAgentConfig{{Name: "test-agent"}},
				SuccessPolicy: "invalid-policy",
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "invalid success_policy",
		},
		{
			name: "stage with invalid stage-level max iterations",
			stage: StageConfig{
				Name:          "stage1",
				Agents:        []StageAgentConfig{{Name: "test-agent"}},
				MaxIterations: &maxIter0,
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "max_iterations must be at least 1",
		},
		{
			name: "stage with synthesis agent not found",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					Agent: "nonexistent-synthesis-agent",
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "synthesis agent 'nonexistent-synthesis-agent' not found",
		},
		{
			name: "stage with synthesis invalid LLM backend",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					Agent:      "synthesis-agent",
					LLMBackend: "invalid-backend",
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent":      {MCPServers: []string{"test-server"}},
				"synthesis-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "synthesis has invalid llm_backend",
		},
		{
			name: "stage with synthesis invalid LLM provider",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					Agent:       "synthesis-agent",
					LLMProvider: "nonexistent-provider",
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent":      {MCPServers: []string{"test-server"}},
				"synthesis-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "LLM provider 'nonexistent-provider' which is not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
				MCPServerRegistry:   NewMCPServerRegistry(tt.servers),
			}

			validator := NewValidator(cfg)
			err := validator.validateStage("test-chain", 1, &tt.stage)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateChainsEdgeCases tests additional chain validation scenarios
func TestValidateChainsEdgeCases(t *testing.T) {
	maxIter0 := 0
	maxIter15 := 15

	tests := []struct {
		name      string
		chains    map[string]*ChainConfig
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		servers   map[string]*MCPServerConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "chain with invalid max iterations",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:    []string{"test"},
					MaxIterations: &maxIter0,
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "must be at least 1",
		},
		{
			name: "chain with invalid MCP server",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					MCPServers: []string{"nonexistent-server"},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "MCP server 'nonexistent-server' not found",
		},
		{
			name: "chain with chat enabled but no chat agent",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Enabled: true,
						// Agent not specified
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "chat.agent required when chat is enabled",
		},
		{
			name: "chain with chat agent not found",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Enabled: true,
						Agent:   "nonexistent-chat-agent",
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "agent 'nonexistent-chat-agent' not found",
		},
		{
			name: "valid chain with all optional fields",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:    []string{"test"},
					LLMProvider:   "test-provider",
					MaxIterations: &maxIter15,
					MCPServers:    []string{"test-server"},
					Chat: &ChatConfig{
						Enabled: true,
						Agent:   "chat-agent",
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
				"chat-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					MaxToolResultTokens: 100000,
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		// Scoring validation tests
		{
			name: "scoring enabled but no agent",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring:    &ScoringConfig{Enabled: true},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "scoring.agent required when scoring is enabled",
		},
		{
			name: "scoring with invalid agent",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled: true,
						Agent:   "nonexistent-scoring-agent",
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "agent 'nonexistent-scoring-agent' not found",
		},
		{
			name: "scoring with invalid LLM backend",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:    true,
						Agent:      "test-agent",
						LLMBackend: "invalid-backend",
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "scoring with invalid LLM provider",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:     true,
						Agent:       "test-agent",
						LLMProvider: "nonexistent-provider",
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "LLM provider 'nonexistent-provider' not found",
		},
		{
			name: "scoring with invalid max iterations",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:       true,
						Agent:         "test-agent",
						MaxIterations: &maxIter0,
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "must be at least 1",
		},
		{
			name: "scoring with invalid MCP server",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:    true,
						Agent:      "test-agent",
						MCPServers: []string{"nonexistent-server"},
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "MCP server 'nonexistent-server' not found",
		},
		{
			name: "scoring disabled with invalid fields passes",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:       false,
						Agent:         "nonexistent-agent",
						LLMBackend:    "invalid",
						LLMProvider:   "nonexistent",
						MaxIterations: &maxIter0,
						MCPServers:    []string{"nonexistent-server"},
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		{
			name: "valid scoring config passes",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Scoring: &ScoringConfig{
						Enabled:       true,
						Agent:         "scoring-agent",
						LLMBackend:    LLMBackendLangChain,
						LLMProvider:   "test-provider",
						MaxIterations: &maxIter15,
						MCPServers:    []string{"test-server"},
					},
					Stages: []StageConfig{
						{
							Name:   "stage1",
							Agents: []StageAgentConfig{{Name: "test-agent"}},
						},
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent":    {MCPServers: []string{"test-server"}},
				"scoring-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					MaxToolResultTokens: 100000,
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ChainRegistry:       NewChainRegistry(tt.chains),
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
				MCPServerRegistry:   NewMCPServerRegistry(tt.servers),
			}

			validator := NewValidator(cfg)
			err := validator.validateChains()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateAllFailFast tests that ValidateAll fails fast on first error
func TestValidateAllFailFast(t *testing.T) {
	// Create config with multiple validation errors:
	// - Agent references nonexistent MCP server (fails in agent validation)
	// - Chain has no alert types (would fail in chain validation)
	// ValidateAll should stop at the first error.
	cfg := &Config{
		Queue: DefaultQueueConfig(),
		AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
			"bad-agent": {MCPServers: []string{"nonexistent"}},
		}),
		ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
			"bad-chain": {
				AlertTypes: []string{}, // Error: no alert types (never reached)
				Stages:     []StageConfig{},
			},
		}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
	}

	validator := NewValidator(cfg)
	err := validator.ValidateAll()

	// Should fail fast at agent validation (before reaching chain validation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent validation failed")
	assert.Contains(t, err.Error(), "MCP server 'nonexistent' not found")
}

// TestValidateMCPServersSSETransport tests SSE transport validation
func TestValidateMCPServersSSETransport(t *testing.T) {
	tests := []struct {
		name    string
		server  *MCPServerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid SSE server",
			server: &MCPServerConfig{
				Transport: TransportConfig{
					Type: TransportTypeSSE,
					URL:  "http://example.com/sse",
				},
			},
			wantErr: false,
		},
		{
			name: "SSE server missing URL",
			server: &MCPServerConfig{
				Transport: TransportConfig{
					Type: TransportTypeSSE,
				},
			},
			wantErr: true,
			errMsg:  "url required for sse transport",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{
					"test-server": tt.server,
				}),
			}

			validator := NewValidator(cfg)
			err := validator.validateMCPServers()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDefaults(t *testing.T) {
	tests := []struct {
		name     string
		defaults *Defaults
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "nil defaults passes",
			defaults: nil,
			wantErr:  false,
		},
		{
			name:     "nil alert masking passes",
			defaults: &Defaults{AlertMasking: nil},
			wantErr:  false,
		},
		{
			name: "valid pattern group passes",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      true,
					PatternGroup: "security",
				},
			},
			wantErr: false,
		},
		{
			name: "all built-in groups pass",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      true,
					PatternGroup: "basic",
				},
			},
			wantErr: false,
		},
		{
			name: "unknown pattern group fails",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      true,
					PatternGroup: "nonexistent-group",
				},
			},
			wantErr: true,
			errMsg:  "pattern group 'nonexistent-group' not found",
		},
		{
			name: "disabled masking with invalid group passes",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      false,
					PatternGroup: "nonexistent-group",
				},
			},
			wantErr: false,
		},
		{
			name: "empty pattern group fails when enabled",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      true,
					PatternGroup: "",
				},
			},
			wantErr: true,
			errMsg:  "pattern_group is required when alert masking is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Defaults: tt.defaults,
			}

			validator := NewValidator(cfg)
			err := validator.validateDefaults()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDefaultsScoringAgent(t *testing.T) {
	tests := []struct {
		name     string
		defaults *Defaults
		agents   map[string]*AgentConfig
		wantErr  bool
		errMsg   string
	}{
		{
			name: "valid scoring agent passes",
			defaults: &Defaults{
				ScoringAgent: "scoring-agent",
			},
			agents: map[string]*AgentConfig{
				"scoring-agent": {},
			},
			wantErr: false,
		},
		{
			name: "invalid scoring agent fails",
			defaults: &Defaults{
				ScoringAgent: "nonexistent-agent",
			},
			agents:  map[string]*AgentConfig{},
			wantErr: true,
			errMsg:  "agent 'nonexistent-agent' not found",
		},
		{
			name:     "empty scoring agent passes",
			defaults: &Defaults{},
			agents:   map[string]*AgentConfig{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Defaults:      tt.defaults,
				AgentRegistry: NewAgentRegistry(tt.agents),
			}

			validator := NewValidator(cfg)
			err := validator.validateDefaults()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRunbooks(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *RunbookConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil config passes",
			cfg:     nil,
			wantErr: false,
		},
		{
			name: "valid config with repo URL",
			cfg: &RunbookConfig{
				RepoURL:        "https://github.com/org/repo/tree/main/runbooks",
				CacheTTL:       1 * time.Minute,
				AllowedDomains: []string{"github.com", "raw.githubusercontent.com"},
			},
			wantErr: false,
		},
		{
			name: "valid config without repo URL",
			cfg: &RunbookConfig{
				CacheTTL:       5 * time.Minute,
				AllowedDomains: []string{"github.com"},
			},
			wantErr: false,
		},
		{
			name: "zero cache TTL fails",
			cfg: &RunbookConfig{
				CacheTTL:       0,
				AllowedDomains: []string{"github.com"},
			},
			wantErr: true,
			errMsg:  "cache_ttl must be positive",
		},
		{
			name: "negative cache TTL fails",
			cfg: &RunbookConfig{
				CacheTTL:       -1 * time.Minute,
				AllowedDomains: []string{"github.com"},
			},
			wantErr: true,
			errMsg:  "cache_ttl must be positive",
		},
		{
			name: "empty allowed domain entry fails",
			cfg: &RunbookConfig{
				CacheTTL:       1 * time.Minute,
				AllowedDomains: []string{"github.com", ""},
			},
			wantErr: true,
			errMsg:  "allowed_domains[1] is empty",
		},
		{
			name: "invalid repo URL fails",
			cfg: &RunbookConfig{
				RepoURL:        "://broken",
				CacheTTL:       1 * time.Minute,
				AllowedDomains: []string{"github.com"},
			},
			wantErr: true,
			errMsg:  "repo_url is not a valid URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Runbooks: tt.cfg,
			}

			validator := NewValidator(cfg)
			err := validator.validateRunbooks()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRunbooks_IntegrationWithValidateAll(t *testing.T) {
	cfg := &Config{
		Queue:               DefaultQueueConfig(),
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		ChainRegistry:       NewChainRegistry(map[string]*ChainConfig{}),
		Runbooks: &RunbookConfig{
			CacheTTL:       0, // Invalid
			AllowedDomains: []string{"github.com"},
		},
	}

	validator := NewValidator(cfg)
	err := validator.ValidateAll()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "runbooks validation failed")
	assert.Contains(t, err.Error(), "cache_ttl must be positive")
}

func TestValidateDefaults_IntegrationWithValidateAll(t *testing.T) {
	// Verify validateDefaults is called as part of ValidateAll
	cfg := &Config{
		Queue:               DefaultQueueConfig(),
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		ChainRegistry:       NewChainRegistry(map[string]*ChainConfig{}),
		Defaults: &Defaults{
			AlertMasking: &AlertMaskingDefaults{
				Enabled:      true,
				PatternGroup: "nonexistent-group",
			},
		},
	}

	validator := NewValidator(cfg)
	err := validator.ValidateAll()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults validation failed")
	assert.Contains(t, err.Error(), "pattern group 'nonexistent-group' not found")
}

func TestValidateSlack(t *testing.T) {
	tests := []struct {
		name    string
		slack   *SlackConfig
		env     map[string]string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil slack config passes",
			slack:   nil,
			wantErr: false,
		},
		{
			name:    "disabled slack passes",
			slack:   &SlackConfig{Enabled: false},
			wantErr: false,
		},
		{
			name: "enabled with channel and token passes",
			slack: &SlackConfig{
				Enabled:  true,
				TokenEnv: "TEST_SLACK_TOKEN",
				Channel:  "C12345678",
			},
			env:     map[string]string{"TEST_SLACK_TOKEN": "xoxb-test"},
			wantErr: false,
		},
		{
			name: "enabled without channel fails",
			slack: &SlackConfig{
				Enabled:  true,
				TokenEnv: "TEST_SLACK_TOKEN",
				Channel:  "",
			},
			env:     map[string]string{"TEST_SLACK_TOKEN": "xoxb-test"},
			wantErr: true,
			errMsg:  "system.slack.channel is required when Slack is enabled",
		},
		{
			name: "enabled with empty token_env fails",
			slack: &SlackConfig{
				Enabled:  true,
				TokenEnv: "",
				Channel:  "C12345678",
			},
			wantErr: true,
			errMsg:  "system.slack.token_env is required when Slack is enabled",
		},
		{
			name: "enabled with missing token env var fails",
			slack: &SlackConfig{
				Enabled:  true,
				TokenEnv: "MISSING_SLACK_TOKEN",
				Channel:  "C12345678",
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "environment variable MISSING_SLACK_TOKEN is not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg := &Config{Slack: tt.slack}
			validator := NewValidator(cfg)
			err := validator.validateSlack()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSlack_IntegrationWithValidateAll(t *testing.T) {
	cfg := &Config{
		Queue:               DefaultQueueConfig(),
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		ChainRegistry:       NewChainRegistry(map[string]*ChainConfig{}),
		Slack: &SlackConfig{
			Enabled:  true,
			TokenEnv: "SLACK_BOT_TOKEN",
			Channel:  "",
		},
	}

	validator := NewValidator(cfg)
	err := validator.ValidateAll()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "slack validation failed")
	assert.Contains(t, err.Error(), "system.slack.channel is required")
}

func TestValidateOrchestratorDefaults(t *testing.T) {
	tests := []struct {
		name    string
		orch    *OrchestratorConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil orchestrator defaults is valid",
			orch:    nil,
			wantErr: false,
		},
		{
			name:    "valid orchestrator defaults",
			orch:    &OrchestratorConfig{MaxConcurrentAgents: intPtr(5), AgentTimeout: durPtr(300 * time.Second)},
			wantErr: false,
		},
		{
			name:    "zero max_concurrent_agents",
			orch:    &OrchestratorConfig{MaxConcurrentAgents: intPtr(0)},
			wantErr: true,
			errMsg:  "must be at least 1",
		},
		{
			name:    "negative agent_timeout",
			orch:    &OrchestratorConfig{AgentTimeout: durPtr(-5 * time.Second)},
			wantErr: true,
			errMsg:  "must be positive",
		},
		{
			name:    "zero max_budget",
			orch:    &OrchestratorConfig{MaxBudget: durPtr(0)},
			wantErr: true,
			errMsg:  "must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Defaults:            &Defaults{Orchestrator: tt.orch},
				AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{}),
				MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
				LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			}

			validator := NewValidator(cfg)
			err := validator.validateDefaults()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSubAgents(t *testing.T) {
	baseAgents := map[string]*AgentConfig{
		"LogAnalyzer":    {Description: "Analyzes logs"},
		"MetricChecker":  {Description: "Checks metrics"},
		"MyOrchestrator": {Type: AgentTypeOrchestrator, Description: "Orchestrator"},
	}

	t.Run("valid chain-level sub_agents", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents:  SubAgentRefs{{Name: "LogAnalyzer"}, {Name: "MetricChecker"}},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "LogAnalyzer"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		assert.NoError(t, err)
	})

	t.Run("chain-level sub_agents references unknown agent", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents:  SubAgentRefs{{Name: "NonExistent"}},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "LogAnalyzer"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agent 'NonExistent' not found")
	})

	t.Run("sub_agents cannot reference orchestrator", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents:  SubAgentRefs{{Name: "MyOrchestrator"}},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "LogAnalyzer"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is an orchestrator and cannot be a sub-agent")
	})

	t.Run("valid stage-level sub_agents", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name:      "s1",
							SubAgents: SubAgentRefs{{Name: "LogAnalyzer"}},
							Agents:    []StageAgentConfig{{Name: "MetricChecker"}},
						},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		assert.NoError(t, err)
	})

	t.Run("valid stage-agent-level sub_agents", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name: "s1",
							Agents: []StageAgentConfig{
								{Name: "MyOrchestrator", SubAgents: SubAgentRefs{{Name: "MetricChecker"}}},
							},
						},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		assert.NoError(t, err)
	})

	t.Run("stage-level sub_agents cannot reference orchestrator", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name:      "s1",
							SubAgents: SubAgentRefs{{Name: "MyOrchestrator"}},
							Agents:    []StageAgentConfig{{Name: "LogAnalyzer"}},
						},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is an orchestrator and cannot be a sub-agent")
	})

	t.Run("stage-agent-level sub_agents references unknown agent", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{
						{
							Name: "s1",
							Agents: []StageAgentConfig{
								{Name: "MyOrchestrator", SubAgents: SubAgentRefs{{Name: "Ghost"}}},
							},
						},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agent 'Ghost' not found")
	})

	t.Run("sub_agent ref with valid overrides", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{"grafana": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "grafana"}}}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{"fast": {}}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", LLMProvider: "fast", MaxIterations: intPtr(5), MCPServers: []string{"grafana"}},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "MetricChecker"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		assert.NoError(t, err)
	})

	t.Run("sub_agent ref with invalid llm_backend", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", LLMBackend: "invalid-backend"},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "MetricChecker"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid llm_backend")
	})

	t.Run("sub_agent ref with unknown llm_provider", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", LLMProvider: "nonexistent"},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "MetricChecker"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LLM provider 'nonexistent' which is not found")
	})

	t.Run("sub_agent ref with invalid max_iterations", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", MaxIterations: intPtr(0)},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "MetricChecker"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_iterations must be at least 1")
	})

	t.Run("sub_agent ref with unknown mcp_server", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(baseAgents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", MCPServers: []string{"missing-server"}},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "MetricChecker"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MCP server 'missing-server' which is not found")
	})
}

func intPtr(i int) *int {
	return &i
}

func durPtr(d time.Duration) *time.Duration {
	return &d
}
