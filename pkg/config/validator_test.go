package config

import (
	"testing"

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
			name: "agent with no MCP servers",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers: []string{},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "at least one MCP server required",
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
			name: "agent with invalid iteration strategy",
			agents: map[string]*AgentConfig{
				"test-agent": {
					MCPServers:        []string{"test-server"},
					IterationStrategy: "invalid-strategy",
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"},
				},
			},
			wantErr: true,
			errMsg:  "invalid strategy",
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
					Model:               "gpt-4",
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
					Model:               "gpt-4",
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
					Model:               "claude-3",
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
					Model:               "gpt-4",
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
					Model:               "claude-3",
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
			name: "stage with invalid agent iteration strategy",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:              "test-agent",
						IterationStrategy: "invalid-strategy",
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
			errMsg:  "invalid iteration_strategy",
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
			name: "stage with synthesis invalid iteration strategy",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					Agent:             "synthesis-agent",
					IterationStrategy: "invalid-strategy",
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
			errMsg:  "synthesis has invalid iteration_strategy",
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
	// Create config with multiple validation errors
	// Agent has no MCP servers (will fail after queue passes)
	cfg := &Config{
		Queue: DefaultQueueConfig(),
		AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
			"bad-agent": {MCPServers: []string{}}, // Error: no MCP servers
		}),
		ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
			"bad-chain": {
				AlertTypes: []string{}, // Error: no alert types
				Stages:     []StageConfig{},
			},
		}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
	}

	validator := NewValidator(cfg)
	err := validator.ValidateAll()

	// Should fail fast and return only the first error (agent validation, after queue passes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent validation failed")
	assert.Contains(t, err.Error(), "at least one MCP server required")
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
			name: "empty pattern group passes when enabled",
			defaults: &Defaults{
				AlertMasking: &AlertMaskingDefaults{
					Enabled:      true,
					PatternGroup: "",
				},
			},
			wantErr: false,
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
