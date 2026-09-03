package config

import (
	"bytes"
	"log/slog"
	"math"
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
			name: "YAML type compose is rejected",
			agents: map[string]*AgentConfig{
				"my-agent": {
					Type: AgentTypeCompose,
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "executor-only",
		},
		{
			name: "builtin ComposeAgent with type compose is allowed",
			agents: map[string]*AgentConfig{
				AgentNameCompose: {
					Type: AgentTypeCompose,
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
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
			name: "any agent with orchestrator config is valid",
			agents: map[string]*AgentConfig{
				"my-agent": {
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(3)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "orchestrator config with zero max_concurrent_agents",
			agents: map[string]*AgentConfig{
				"orch": {
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
					Orchestrator: &OrchestratorConfig{AgentTimeout: durPtr(-1 * time.Second)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: true,
			errMsg:  "must be positive",
		},
		{
			name: "action agent type is valid",
			agents: map[string]*AgentConfig{
				"remediation": {Type: AgentTypeAction},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
		},
		{
			name: "synthesis agent with orchestrator config is valid (inert if no sub-agents)",
			agents: map[string]*AgentConfig{
				"synth": {
					Type:         AgentTypeSynthesis,
					Orchestrator: &OrchestratorConfig{MaxConcurrentAgents: intPtr(3)},
				},
			},
			servers: map[string]*MCPServerConfig{},
			wantErr: false,
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
			name: "chain with invalid compose_provider",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:      []string{"test"},
					ComposeProvider: "invalid-compose",
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
			errMsg:    "compose_provider",
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
						SizeThresholdTokens: 5000,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid summarization with threshold-only config (no enabled field)",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 3000,
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
						SizeThresholdTokens: 50,
					},
				},
			},
			wantErr: true,
			errMsg:  "must be at least 100",
		},
		{
			name: "disabled summarization with llm_provider fails",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						Enabled:     BoolPtr(false),
						LLMProvider: "test-provider",
					},
				},
			},
			wantErr: true,
			errMsg:  "llm_provider is unused when summarization is disabled",
		},
		{
			name: "backend without provider fails",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMBackend:          LLMBackendLangChain,
					},
				},
			},
			wantErr: true,
			errMsg:  "llm_backend requires llm_provider at the same level",
		},
		{
			name: "unknown summarization provider fails",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMProvider:         "nonexistent-provider",
					},
				},
			},
			wantErr: true,
			errMsg:  "LLM provider 'nonexistent-provider' not found",
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

func TestValidateMCPServersSummarizationProvider(t *testing.T) {
	tests := []struct {
		name      string
		servers   map[string]*MCPServerConfig
		providers map[string]*LLMProviderConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid overlay provider omitted backend passes",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMProvider:         "test-provider",
					},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: false,
		},
		{
			name: "valid overlay provider with backend passes",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMProvider:         "test-provider",
						LLMBackend:          LLMBackendNativeGemini,
					},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: false,
		},
		{
			name: "google-native overlay with openai provider fails",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMProvider:         "openai-default",
						LLMBackend:          LLMBackendNativeGemini,
					},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "invalid backend fails",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						LLMProvider:         "test-provider",
						LLMBackend:          "invalid-backend",
					},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "per-server fallback_list is rejected",
			servers: map[string]*MCPServerConfig{
				"test-server": {
					Transport: TransportConfig{
						Type:    TransportTypeStdio,
						Command: "test",
					},
					Summarization: &SummarizationConfig{
						SizeThresholdTokens: 5000,
						FallbackList:        "google-native",
					},
				},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "fallback_list is only valid on defaults.summarization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				MCPServerRegistry:   NewMCPServerRegistry(tt.servers),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
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
					Type:      LLMProviderTypeGoogle,
					Model:     "test-model",
					APIKeyEnv: "TEST_API_KEY",
				},
			},
			env:     map[string]string{"TEST_API_KEY": "test-key"},
			wantErr: false,
		},
		{
			name: "unreferenced provider with missing API key does not error",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:      LLMProviderTypeGoogle,
					Model:     "test-model",
					APIKeyEnv: "MISSING_API_KEY",
				},
			},
			env:     map[string]string{},
			wantErr: false, // No error because provider is not referenced by any chain
		},
		{
			name: "provider with invalid type",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:  "invalid",
					Model: "test-model",
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
					Type:  LLMProviderTypeGoogle,
					Model: "",
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "model required",
		},
		{
			name: "VertexAI provider with both environment variables set",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "TEST_GCP_PROJECT",
					LocationEnv: "TEST_GCP_LOCATION",
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
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "MISSING_GCP_PROJECT",
					LocationEnv: "TEST_GCP_LOCATION",
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
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "TEST_GCP_PROJECT",
					LocationEnv: "MISSING_GCP_LOCATION",
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
					Type:      LLMProviderTypeOpenAI,
					Model:     "o4-mini",
					APIKeyEnv: "USED_API_KEY",
				},
				"unused-provider": {
					Type:      LLMProviderTypeGoogle,
					Model:     "gemini-pro",
					APIKeyEnv: "UNUSED_API_KEY", // This env var is NOT set, but should not cause error
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
					Type:      LLMProviderTypeOpenAI,
					Model:     "o4-mini",
					APIKeyEnv: "USED_API_KEY",
				},
				"unused-provider": {
					Type:      LLMProviderTypeGoogle,
					Model:     "gemini-pro",
					APIKeyEnv: "UNUSED_API_KEY",
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
					Type:      LLMProviderTypeAnthropic,
					Model:     "claude-sonnet-4-5-20250929",
					APIKeyEnv: "CHAT_API_KEY",
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
					Type:      LLMProviderTypeGoogle,
					Model:     "gemini-pro",
					APIKeyEnv: "AGENT_API_KEY",
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
					Type:      LLMProviderTypeXAI,
					Model:     "grok-1",
					APIKeyEnv: "SYNTHESIS_API_KEY",
				},
			},
			env:     map[string]string{}, // SYNTHESIS_API_KEY is not set
			wantErr: true,
			errMsg:  "environment variable SYNTHESIS_API_KEY is not set",
		},
		{
			name: "chat.sub_agents referenced provider requires env var",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Agent: "ChatAgent",
						SubAgents: SubAgentRefs{
							{Name: "Worker", LLMProvider: "chat-sub-provider"},
						},
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
				"test-agent": {MCPServers: []string{"test"}},
				"ChatAgent":  {MCPServers: []string{"test"}},
				"Worker":     {MCPServers: []string{"test"}},
			},
			providers: map[string]*LLMProviderConfig{
				"chat-sub-provider": {
					Type:      LLMProviderTypeGoogle,
					Model:     "gemini-pro",
					APIKeyEnv: "CHAT_SUB_AGENT_API_KEY",
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "environment variable CHAT_SUB_AGENT_API_KEY is not set",
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
					Type:      LLMProviderTypeOpenAI,
					Model:     "o4-mini",
					APIKeyEnv: "USED_API_KEY",
				},
				"unused-provider-1": {
					Type:      LLMProviderTypeGoogle,
					Model:     "gemini-pro",
					APIKeyEnv: "UNUSED_API_KEY_1",
				},
				"unused-provider-2": {
					Type:      LLMProviderTypeAnthropic,
					Model:     "claude-sonnet-4-5-20250929",
					APIKeyEnv: "UNUSED_API_KEY_2",
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
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "MISSING_GCP_PROJECT",
					LocationEnv: "TEST_GCP_LOCATION",
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
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "TEST_GCP_PROJECT",
					LocationEnv: "MISSING_GCP_LOCATION",
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
					Type:        LLMProviderTypeVertexAI,
					Model:       "gemini-pro",
					ProjectEnv:  "TEST_GCP_PROJECT",
					LocationEnv: "TEST_GCP_LOCATION",
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
			name: "stage with invalid agent type",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name: "test-agent",
						Type: "invalid-type",
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
			errMsg:  "invalid type",
		},
		{
			name: "stage with valid agent type override",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name: "test-agent",
						Type: AgentTypeAction,
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
			name: "stage with type compose is rejected",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name: "test-agent",
						Type: AgentTypeCompose,
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
			errMsg:  "executor-only",
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
			name: "stage agent google-native with openai provider fails",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:        "test-agent",
						LLMProvider: "openai-default",
						LLMBackend:  LLMBackendNativeGemini,
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "stage agent google-native with google provider passes",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:        "test-agent",
						LLMProvider: "google-default",
						LLMBackend:  LLMBackendNativeGemini,
					},
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"google-default": {Type: LLMProviderTypeGoogle, Model: "gemini"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		{
			name: "stage agent backend-only google-native passes",
			stage: StageConfig{
				Name: "stage1",
				Agents: []StageAgentConfig{
					{
						Name:       "test-agent",
						LLMBackend: LLMBackendNativeGemini,
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
		{
			name: "synthesis google-native with openai provider fails",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					LLMProvider: "openai-default",
					LLMBackend:  LLMBackendNativeGemini,
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "synthesis google-native with anthropic provider fails",
			stage: StageConfig{
				Name:   "stage1",
				Agents: []StageAgentConfig{{Name: "test-agent"}},
				Synthesis: &SynthesisConfig{
					LLMProvider: "anthropic-default",
					LLMBackend:  LLMBackendNativeGemini,
				},
			},
			agents: map[string]*AgentConfig{
				"test-agent": {MCPServers: []string{"test-server"}},
			},
			providers: map[string]*LLMProviderConfig{
				"anthropic-default": {Type: LLMProviderTypeAnthropic, Model: "claude-sonnet"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "stage with action agent type is valid",
			stage: StageConfig{
				Name:   "take-action",
				Agents: []StageAgentConfig{{Name: "remediation-agent"}},
			},
			agents: map[string]*AgentConfig{
				"remediation-agent": {Type: AgentTypeAction},
			},
			providers: map[string]*LLMProviderConfig{},
			servers:   map[string]*MCPServerConfig{},
			wantErr:   false,
		},
		{
			name: "stage with mixed action and non-action agents is valid (warning only)",
			stage: StageConfig{
				Name: "mixed-stage",
				Agents: []StageAgentConfig{
					{Name: "investigation-agent"},
					{Name: "remediation-agent"},
				},
			},
			agents: map[string]*AgentConfig{
				"investigation-agent": {},
				"remediation-agent":   {Type: AgentTypeAction},
			},
			providers: map[string]*LLMProviderConfig{},
			servers:   map[string]*MCPServerConfig{},
			wantErr:   false,
		},
		{
			name: "stage with action type override is valid",
			stage: StageConfig{
				Name: "action-override",
				Agents: []StageAgentConfig{
					{Name: "generic-agent", Type: AgentTypeAction},
				},
			},
			agents: map[string]*AgentConfig{
				"generic-agent": {},
			},
			providers: map[string]*LLMProviderConfig{},
			servers:   map[string]*MCPServerConfig{},
			wantErr:   false,
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

func TestWarnMixedActionStage(t *testing.T) {
	t.Run("logs warning for mixed action and non-action agents", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		oldLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		t.Cleanup(func() { slog.SetDefault(oldLogger) })

		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"investigator": {},
				"remediator":   {Type: AgentTypeAction},
			}),
		}
		v := NewValidator(cfg)
		stg := &StageConfig{
			Name: "mixed",
			Agents: []StageAgentConfig{
				{Name: "investigator"},
				{Name: "remediator"},
			},
		}
		v.warnMixedActionStage(stg, "chain 'test' stage 0")

		assert.Contains(t, buf.String(), "mixed action and non-action agents")
	})

	t.Run("no warning for pure action stage", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		oldLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		t.Cleanup(func() { slog.SetDefault(oldLogger) })

		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"remediator1": {Type: AgentTypeAction},
				"remediator2": {Type: AgentTypeAction},
			}),
		}
		v := NewValidator(cfg)
		stg := &StageConfig{
			Name: "pure-action",
			Agents: []StageAgentConfig{
				{Name: "remediator1"},
				{Name: "remediator2"},
			},
		}
		v.warnMixedActionStage(stg, "chain 'test' stage 0")

		assert.Empty(t, buf.String())
	})

	t.Run("no warning for single agent stage", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		oldLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		t.Cleanup(func() { slog.SetDefault(oldLogger) })

		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"remediator": {Type: AgentTypeAction},
			}),
		}
		v := NewValidator(cfg)
		stg := &StageConfig{
			Name:   "single",
			Agents: []StageAgentConfig{{Name: "remediator"}},
		}
		v.warnMixedActionStage(stg, "chain 'test' stage 0")

		assert.Empty(t, buf.String())
	})

	t.Run("stage override resolves correctly", func(t *testing.T) {
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		oldLogger := slog.Default()
		slog.SetDefault(slog.New(handler))
		t.Cleanup(func() { slog.SetDefault(oldLogger) })

		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"generic1": {},
				"generic2": {},
			}),
		}
		v := NewValidator(cfg)
		stg := &StageConfig{
			Name: "all-overridden",
			Agents: []StageAgentConfig{
				{Name: "generic1", Type: AgentTypeAction},
				{Name: "generic2", Type: AgentTypeAction},
			},
		}
		v.warnMixedActionStage(stg, "chain 'test' stage 0")

		assert.Empty(t, buf.String())
	})
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
			name: "compose_backend without compose_provider fails",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:     []string{"test"},
					ComposeBackend: LLMBackendLangChain,
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
			errMsg:  "compose_backend requires compose_provider at the same level",
		},
		{
			name: "executive_summary_backend without provider fails",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:              []string{"test"},
					ExecutiveSummaryBackend: LLMBackendLangChain,
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
			errMsg:  "executive_summary_backend requires executive_summary_provider at the same level",
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
			name: "chain with chat enabled and omitted agent defaults to ChatAgent",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Enabled: BoolPtr(true),
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
			name: "chain with chat overrides omitted enabled and agent",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						LLMProvider: "test-provider",
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
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		{
			name: "chain with chat agent not found",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Agent: "nonexistent-chat-agent",
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
			name: "chat disabled skips agent validation",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Enabled: BoolPtr(false),
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
			wantErr: false,
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
						Agent: "chat-agent",
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
					Type:  LLMProviderTypeGoogle,
					Model: "test-model",
				},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: false,
		},
		{
			name: "chain google-native with openai provider fails",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes:  []string{"test"},
					LLMProvider: "openai-default",
					LLMBackend:  LLMBackendNativeGemini,
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
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "chat google-native with openai provider fails",
			chains: map[string]*ChainConfig{
				"test-chain": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Agent:       "test-agent",
						LLMProvider: "openai-default",
						LLMBackend:  LLMBackendNativeGemini,
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
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			servers: map[string]*MCPServerConfig{
				"test-server": {Transport: TransportConfig{Type: TransportTypeStdio, Command: "test"}},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "scoring enabled uses built-in agent by default",
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
			wantErr: false,
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
					Type:  LLMProviderTypeGoogle,
					Model: "test-model",
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
		name      string
		defaults  *Defaults
		providers map[string]*LLMProviderConfig
		wantErr   bool
		errMsg    string
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
		{
			name: "unknown compose_provider fails",
			defaults: &Defaults{
				ComposeProvider: "no-such-provider",
			},
			wantErr: true,
			errMsg:  "compose_provider",
		},
		{
			name: "compose_backend without compose_provider fails",
			defaults: &Defaults{
				ComposeBackend: LLMBackendLangChain,
			},
			wantErr: true,
			errMsg:  "compose_backend requires compose_provider at the same level",
		},
		{
			name: "executive_summary_backend without provider fails",
			defaults: &Defaults{
				ExecutiveSummaryBackend: LLMBackendLangChain,
			},
			wantErr: true,
			errMsg:  "executive_summary_backend requires executive_summary_provider at the same level",
		},
		{
			name: "google-native with openai defaults provider fails",
			defaults: &Defaults{
				LLMProvider: "openai-default",
				LLMBackend:  LLMBackendNativeGemini,
			},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Defaults:            tt.defaults,
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
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

func TestValidateNamedAgentPairings(t *testing.T) {
	google := &LLMProviderConfig{Type: LLMProviderTypeGoogle, Model: "gemini"}
	openai := &LLMProviderConfig{Type: LLMProviderTypeOpenAI, Model: "gpt-5"}

	tests := []struct {
		name      string
		defaults  *Defaults
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		errMsg    string
	}{
		{
			name: "known agent pairing passes",
			defaults: &Defaults{
				Agents: map[string]NamedAgentPairing{
					"TestAgent": {LLMProvider: "google-default", LLMBackend: LLMBackendNativeGemini},
				},
			},
			agents:    map[string]*AgentConfig{"TestAgent": {}},
			providers: map[string]*LLMProviderConfig{"google-default": google},
		},
		{
			name: "unknown agent name fails",
			defaults: &Defaults{
				Agents: map[string]NamedAgentPairing{
					"GhostAgent": {FallbackList: "mid"},
				},
			},
			agents: map[string]*AgentConfig{"TestAgent": {}},
			errMsg: `unknown agent "GhostAgent"`,
		},
		{
			name: "google-native with openai pairing fails",
			defaults: &Defaults{
				Agents: map[string]NamedAgentPairing{
					"TestAgent": {LLMProvider: "openai-default", LLMBackend: LLMBackendNativeGemini},
				},
			},
			agents:    map[string]*AgentConfig{"TestAgent": {}},
			providers: map[string]*LLMProviderConfig{"openai-default": openai},
			errMsg:    "requires a google provider",
		},
		{
			name: "unknown pairing provider fails",
			defaults: &Defaults{
				Agents: map[string]NamedAgentPairing{
					"TestAgent": {LLMProvider: "missing"},
				},
			},
			agents:    map[string]*AgentConfig{"TestAgent": {}},
			providers: map[string]*LLMProviderConfig{},
			errMsg:    "LLM provider 'missing' not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Defaults:            tt.defaults,
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
			}
			err := NewValidator(cfg).validateDefaults()
			if tt.errMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateDefaultsScoring(t *testing.T) {
	tests := []struct {
		name      string
		defaults  *Defaults
		agents    map[string]*AgentConfig
		providers map[string]*LLMProviderConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid scoring agent passes",
			defaults: &Defaults{
				Scoring: &ScoringConfig{Agent: "scoring-agent"},
			},
			agents: map[string]*AgentConfig{
				"scoring-agent": {},
			},
			wantErr: false,
		},
		{
			name: "built-in scoring agent passes without registry entry",
			defaults: &Defaults{
				Scoring: &ScoringConfig{Agent: AgentNameScoring},
			},
			agents:  map[string]*AgentConfig{},
			wantErr: false,
		},
		{
			name: "invalid scoring agent fails",
			defaults: &Defaults{
				Scoring: &ScoringConfig{Agent: "nonexistent-agent"},
			},
			agents:  map[string]*AgentConfig{},
			wantErr: true,
			errMsg:  "agent 'nonexistent-agent' not found",
		},
		{
			name:     "nil scoring passes",
			defaults: &Defaults{},
			agents:   map[string]*AgentConfig{},
			wantErr:  false,
		},
		{
			name: "invalid scoring llm_backend fails",
			defaults: &Defaults{
				Scoring: &ScoringConfig{LLMBackend: "invalid-backend"},
			},
			agents:  map[string]*AgentConfig{},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "valid scoring llm_backend passes",
			defaults: &Defaults{
				Scoring: &ScoringConfig{LLMBackend: LLMBackendNativeGemini},
			},
			agents:  map[string]*AgentConfig{},
			wantErr: false,
		},
		{
			name: "invalid scoring llm_provider fails",
			defaults: &Defaults{
				Scoring: &ScoringConfig{LLMProvider: "nonexistent-provider"},
			},
			agents:    map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "LLM provider 'nonexistent-provider' not found",
		},
		{
			name: "valid scoring llm_provider passes",
			defaults: &Defaults{
				Scoring: &ScoringConfig{LLMProvider: "test-provider"},
			},
			agents: map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: false,
		},
		{
			name: "google-native with openai scoring provider fails",
			defaults: &Defaults{
				Scoring: &ScoringConfig{
					LLMProvider: "openai-default",
					LLMBackend:  LLMBackendNativeGemini,
				},
			},
			agents: map[string]*AgentConfig{},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "invalid scoring max_iterations fails",
			defaults: &Defaults{
				Scoring: &ScoringConfig{MaxIterations: intPtr(0)},
			},
			agents:  map[string]*AgentConfig{},
			wantErr: true,
			errMsg:  "must be at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providers := tt.providers
			if providers == nil {
				providers = map[string]*LLMProviderConfig{}
			}
			cfg := &Config{
				Defaults:            tt.defaults,
				AgentRegistry:       NewAgentRegistry(tt.agents),
				LLMProviderRegistry: NewLLMProviderRegistry(providers),
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

func TestValidateDefaultsSummarization(t *testing.T) {
	tests := []struct {
		name      string
		defaults  *Defaults
		providers map[string]*LLMProviderConfig
		wantErr   bool
		errMsg    string
	}{
		{
			name:     "nil summarization passes",
			defaults: &Defaults{},
			wantErr:  false,
		},
		{
			name: "valid provider and backend passes",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMProvider: "test-provider",
					LLMBackend:  LLMBackendLangChain,
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: false,
		},
		{
			name: "valid provider with omitted backend passes",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMProvider: "test-provider",
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: false,
		},
		{
			name: "google-native with openai summarization provider fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMProvider: "openai-default",
					LLMBackend:  LLMBackendNativeGemini,
				},
			},
			providers: map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "unknown provider fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMProvider: "nonexistent-provider",
				},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "LLM provider 'nonexistent-provider' not found",
		},
		{
			name: "invalid backend fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMProvider: "test-provider",
					LLMBackend:  "invalid-backend",
				},
			},
			providers: map[string]*LLMProviderConfig{
				"test-provider": {Type: LLMProviderTypeGoogle, Model: "test"},
			},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "backend without provider fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					LLMBackend: LLMBackendLangChain,
				},
			},
			wantErr: true,
			errMsg:  "llm_backend requires llm_provider at the same level",
		},
		{
			name: "enabled on defaults fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					Enabled: BoolPtr(true),
				},
			},
			wantErr: true,
			errMsg:  "enabled is per-MCP-server only",
		},
		{
			name: "size_threshold_tokens on defaults fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					SizeThresholdTokens: 5000,
				},
			},
			wantErr: true,
			errMsg:  "size_threshold_tokens is per-MCP-server only",
		},
		{
			name: "summary_max_token_limit on defaults fails",
			defaults: &Defaults{
				Summarization: &SummarizationConfig{
					SummaryMaxTokenLimit: 1000,
				},
			},
			wantErr: true,
			errMsg:  "summary_max_token_limit is per-MCP-server only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providers := tt.providers
			if providers == nil {
				providers = map[string]*LLMProviderConfig{}
			}
			cfg := &Config{
				Defaults:            tt.defaults,
				LLMProviderRegistry: NewLLMProviderRegistry(providers),
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

func TestValidateCostEstimation(t *testing.T) {
	tests := []struct {
		name    string
		ce      *CostEstimationConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil passes",
			ce:      nil,
			wantErr: false,
		},
		{
			name: "valid rates pass",
			ce: &CostEstimationConfig{
				Enabled: true,
				ModelRates: map[string]ModelRateConfig{
					"gemini-3.1-pro-preview": {InputPerMillion: 2.0, OutputPerMillion: 12.0},
				},
			},
			wantErr: false,
		},
		{
			name: "zero rates pass",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"free-model": {InputPerMillion: 0, OutputPerMillion: 0},
				},
			},
			wantErr: false,
		},
		{
			name: "empty model name fails",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"": {InputPerMillion: 1.0, OutputPerMillion: 1.0},
				},
			},
			wantErr: true,
			errMsg:  "model name must not be empty",
		},
		{
			name: "negative input rate fails",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"bad": {InputPerMillion: -1.0, OutputPerMillion: 1.0},
				},
			},
			wantErr: true,
			errMsg:  "input_per_million must be >= 0",
		},
		{
			name: "negative output rate fails",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"bad": {InputPerMillion: 1.0, OutputPerMillion: -0.5},
				},
			},
			wantErr: true,
			errMsg:  "output_per_million must be >= 0",
		},
		{
			name: "valid promotion passes",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					ID:               "intro",
					Model:            "gemini-3.7-flash",
					Start:            "2026-08-01",
					End:              "2026-10-01",
					InputPerMillion:  0.75,
					OutputPerMillion: 3.75,
				}},
			},
			wantErr: false,
		},
		{
			name: "expired promotion still loads",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "old-model",
					Start:            "2020-01-01",
					End:              "2020-06-01",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: false,
		},
		{
			name: "omitted start passes",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "open-start",
					End:              "2026-10-01",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: false,
		},
		{
			name: "missing end fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "no-end",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: true,
			errMsg:  "end is required",
		},
		{
			name: "empty model fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					End:              "2026-10-01",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: true,
			errMsg:  "model must not be empty",
		},
		{
			name: "bad time fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "bad-time",
					End:              "not-a-date",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: true,
			errMsg:  "end:",
		},
		{
			name: "end before start fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "backwards",
					Start:            "2026-10-01",
					End:              "2026-08-01",
					InputPerMillion:  1.0,
					OutputPerMillion: 1.0,
				}},
			},
			wantErr: true,
			errMsg:  "end must be after start",
		},
		{
			name: "duplicate id fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{ID: "same", Model: "m1", End: "2026-06-01", InputPerMillion: 1, OutputPerMillion: 1},
					{ID: "same", Model: "m2", End: "2026-07-01", InputPerMillion: 1, OutputPerMillion: 1},
				},
			},
			wantErr: true,
			errMsg:  "duplicates",
		},
		{
			name: "overlapping windows fail",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", Start: "2026-08-01", End: "2026-10-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m1", Start: "2026-09-01", End: "2026-11-01", InputPerMillion: 1, OutputPerMillion: 1},
				},
			},
			wantErr: true,
			errMsg:  "overlapping windows",
		},
		{
			name: "sequential non-overlapping windows pass",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", Start: "2026-08-01", End: "2026-10-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m1", Start: "2026-10-01", End: "2026-12-01", InputPerMillion: 2, OutputPerMillion: 2},
				},
			},
			wantErr: false,
		},
		{
			name: "negative promo rate fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "neg",
					End:              "2026-10-01",
					InputPerMillion:  -1,
					OutputPerMillion: 1,
				}},
			},
			wantErr: true,
			errMsg:  "input_per_million must be >= 0",
		},
		{
			name: "NaN promo rate fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "nan",
					End:              "2026-10-01",
					InputPerMillion:  math.NaN(),
					OutputPerMillion: 1,
				}},
			},
			wantErr: true,
			errMsg:  "must be a finite number >= 0",
		},
		{
			name: "positive infinity promo rate fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "inf",
					End:              "2026-10-01",
					InputPerMillion:  1,
					OutputPerMillion: math.Inf(1),
				}},
			},
			wantErr: true,
			errMsg:  "must be a finite number >= 0",
		},
		{
			name: "NaN model_rates fails",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"bad": {InputPerMillion: math.NaN(), OutputPerMillion: 1},
				},
			},
			wantErr: true,
			errMsg:  "must be a finite number >= 0",
		},
		{
			name: "positive infinity model_rates fails",
			ce: &CostEstimationConfig{
				ModelRates: map[string]ModelRateConfig{
					"bad": {InputPerMillion: 1, OutputPerMillion: math.Inf(1)},
				},
			},
			wantErr: true,
			errMsg:  "must be a finite number >= 0",
		},
		{
			name: "end equals start fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "equal",
					Start:            "2026-08-01",
					End:              "2026-08-01",
					InputPerMillion:  1,
					OutputPerMillion: 1,
				}},
			},
			wantErr: true,
			errMsg:  "end must be after start",
		},
		{
			name: "RFC3339 window passes",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{{
					Model:            "rfc",
					Start:            "2026-08-01T00:00:00Z",
					End:              "2026-10-01T15:04:05-07:00",
					InputPerMillion:  1,
					OutputPerMillion: 1,
				}},
			},
			wantErr: false,
		},
		{
			name: "overlapping open start with later window fails",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", End: "2026-10-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m1", Start: "2026-09-01", End: "2026-11-01", InputPerMillion: 1, OutputPerMillion: 1},
				},
			},
			wantErr: true,
			errMsg:  "overlapping windows",
		},
		{
			name: "two open starts for same model fail",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", End: "2026-06-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m1", End: "2026-12-01", InputPerMillion: 1, OutputPerMillion: 1},
				},
			},
			wantErr: true,
			errMsg:  "overlapping windows",
		},
		{
			name: "same window different models pass",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", Start: "2026-08-01", End: "2026-10-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m2", Start: "2026-08-01", End: "2026-10-01", InputPerMillion: 2, OutputPerMillion: 2},
				},
			},
			wantErr: false,
		},
		{
			name: "open start abutting next window passes",
			ce: &CostEstimationConfig{
				Promotions: []PromotionConfig{
					{Model: "m1", End: "2026-10-01", InputPerMillion: 1, OutputPerMillion: 1},
					{Model: "m1", Start: "2026-10-01", End: "2026-12-01", InputPerMillion: 2, OutputPerMillion: 2},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{CostEstimation: tt.ce}
			err := NewValidator(cfg).validateCostEstimation()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
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
			name:    "omitted agent_timeout is valid",
			orch:    &OrchestratorConfig{MaxConcurrentAgents: intPtr(5)},
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
		"MyOrchestrator": {Description: "Orchestrator agent"},
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

	t.Run("any agent can be a sub-agent", func(t *testing.T) {
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
		assert.NoError(t, err)
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

	t.Run("sub_agent ref to agent with empty description", func(t *testing.T) {
		agents := map[string]*AgentConfig{
			"LogAnalyzer": {Description: "Analyzes logs"},
			"NoDescAgent": {MCPServers: []string{"some-server"}},
		}
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(agents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents:  SubAgentRefs{{Name: "NoDescAgent"}},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "LogAnalyzer"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NoDescAgent")
		assert.Contains(t, err.Error(), "no description")
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

	t.Run("sub_agent google-native with openai provider fails", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry:     NewAgentRegistry(baseAgents),
			MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
				"openai-default": {Type: LLMProviderTypeOpenAI, Model: "gpt-5"},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					SubAgents: SubAgentRefs{
						{Name: "LogAnalyzer", LLMProvider: "openai-default", LLMBackend: LLMBackendNativeGemini},
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
		assert.Contains(t, err.Error(), "requires a google provider")
	})

	t.Run("sub_agent ref with unknown required_skill fails", func(t *testing.T) {
		reg := NewSkillRegistry(map[string]*SkillConfig{
			"known-skill": {Name: "known-skill", Description: "d", Body: "b"},
		})
		cfg := &Config{
			SkillRegistry:       reg,
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
								{
									Name: "MyOrchestrator",
									SubAgents: SubAgentRefs{
										{Name: "LogAnalyzer", RequiredSkills: []string{"no-such-skill"}},
									},
								},
							},
						},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no-such-skill")
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

	t.Run("valid chat.sub_agents when chat enabled", func(t *testing.T) {
		agents := map[string]*AgentConfig{
			"LogAnalyzer":    {Description: "Analyzes logs"},
			"MetricChecker":  {Description: "Checks metrics"},
			"MyOrchestrator": {Description: "Orchestrator agent"},
			"ChatAgent":      {Description: "Chat"},
		}
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(agents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Agent:     "ChatAgent",
						SubAgents: SubAgentRefs{{Name: "LogAnalyzer"}},
					},
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

	t.Run("chat.sub_agents references unknown agent", func(t *testing.T) {
		agents := map[string]*AgentConfig{
			"LogAnalyzer": {Description: "Analyzes logs"},
			"ChatAgent":   {Description: "Chat"},
		}
		cfg := &Config{
			AgentRegistry:       NewAgentRegistry(agents),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Chat: &ChatConfig{
						Agent:     "ChatAgent",
						SubAgents: SubAgentRefs{{Name: "MissingSub"}},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "LogAnalyzer"}}},
					},
				},
			}),
		}
		validator := NewValidator(cfg)
		err := validator.validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "agent 'MissingSub' not found")
	})
}

func TestValidateStageAgentSkills(t *testing.T) {
	skillReg := NewSkillRegistry(map[string]*SkillConfig{
		"stage-skill": {Name: "stage-skill", Description: "d", Body: "b"},
		"on-extra":    {Name: "on-extra", Description: "d2", Body: "b2"},
	})
	baseChain := func(agents []StageAgentConfig) map[string]*ChainConfig {
		return map[string]*ChainConfig{
			"c1": {
				AlertTypes: []string{"t"},
				Stages: []StageConfig{{
					Name:   "s1",
					Agents: agents,
				}},
			},
		}
	}

	t.Run("valid stage-agent required_skills and skills", func(t *testing.T) {
		cfg := &Config{
			SkillRegistry:       skillReg,
			AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"worker": {Description: "w"}}),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(baseChain([]StageAgentConfig{{
				Name:           "worker",
				RequiredSkills: []string{"stage-skill"},
				Skills:         []string{"on-extra"},
			}})),
		}
		err := NewValidator(cfg).validateChains()
		assert.NoError(t, err)
	})

	t.Run("unknown stage-agent required_skill", func(t *testing.T) {
		cfg := &Config{
			SkillRegistry:       skillReg,
			AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"worker": {Description: "w"}}),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(baseChain([]StageAgentConfig{{
				Name:           "worker",
				RequiredSkills: []string{"unknown-skill"},
			}})),
		}
		err := NewValidator(cfg).validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown-skill")
	})

	t.Run("duplicate stage-agent required_skill", func(t *testing.T) {
		cfg := &Config{
			SkillRegistry:       skillReg,
			AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"worker": {Description: "w"}}),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(baseChain([]StageAgentConfig{{
				Name:           "worker",
				RequiredSkills: []string{"stage-skill", "stage-skill"},
			}})),
		}
		err := NewValidator(cfg).validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate skill")
	})

	t.Run("unknown stage-agent skill in skills", func(t *testing.T) {
		cfg := &Config{
			SkillRegistry:       skillReg,
			AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"worker": {Description: "w"}}),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(baseChain([]StageAgentConfig{{
				Name:   "worker",
				Skills: []string{"no-such-skill"},
			}})),
		}
		err := NewValidator(cfg).validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no-such-skill")
	})

	t.Run("duplicate stage-agent skill in skills", func(t *testing.T) {
		cfg := &Config{
			SkillRegistry:       skillReg,
			AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"worker": {Description: "w"}}),
			MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
			ChainRegistry: NewChainRegistry(baseChain([]StageAgentConfig{{
				Name:   "worker",
				Skills: []string{"on-extra", "on-extra"},
			}})),
		}
		err := NewValidator(cfg).validateChains()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate skill")
	})
}

func TestValidateFallbackProviders(t *testing.T) {
	baseAgents := map[string]*AgentConfig{
		"TestAgent": {},
	}

	tests := []struct {
		name      string
		defaults  *Defaults
		chains    map[string]*ChainConfig
		providers map[string]*LLMProviderConfig
		env       map[string]string
		wantErr   bool
		errMsg    string
	}{
		{
			name: "valid defaults-level fallback",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "fallback-1", Backend: LLMBackendNativeGemini},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini-2.5-pro", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: false,
		},
		{
			name: "defaults-level fallback with omitted backend",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "fallback-1"},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini-2.5-pro", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: false,
		},
		{
			name: "defaults-level fallback with missing provider",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "nonexistent", Backend: LLMBackendLangChain},
				},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "LLM provider 'nonexistent' not found",
		},
		{
			name: "defaults-level fallback with invalid backend",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "fallback-1", Backend: "invalid-backend"},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "defaults-level fallback with missing credentials",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "fallback-1", Backend: LLMBackendNativeGemini},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{}, // FB_KEY not set
			wantErr: true,
			errMsg:  "environment variable FB_KEY is not set",
		},
		{
			name: "chain-level fallback valid",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					FallbackProviders: []FallbackProviderEntry{
						{Provider: "fallback-1", Backend: LLMBackendLangChain},
					},
					Stages: []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeOpenAI, Model: "gpt-5", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: false,
		},
		{
			name: "chain-level fallback with missing provider",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					FallbackProviders: []FallbackProviderEntry{
						{Provider: "ghost", Backend: LLMBackendLangChain},
					},
					Stages: []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   true,
			errMsg:    "LLM provider 'ghost' not found",
		},
		{
			name: "stage-level fallback valid",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{{
						Name:              "s1",
						Agents:            []StageAgentConfig{{Name: "TestAgent"}},
						FallbackProviders: []FallbackProviderEntry{{Provider: "fallback-1", Backend: LLMBackendNativeGemini}},
					}},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: false,
		},
		{
			name: "agent-level fallback valid",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{{
						Name: "s1",
						Agents: []StageAgentConfig{{
							Name:              "TestAgent",
							FallbackProviders: []FallbackProviderEntry{{Provider: "fallback-1", Backend: LLMBackendLangChain}},
						}},
					}},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeOpenAI, Model: "gpt-5", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: false,
		},
		{
			name: "agent-level fallback with invalid backend",
			chains: map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{{
						Name: "s1",
						Agents: []StageAgentConfig{{
							Name:              "TestAgent",
							FallbackProviders: []FallbackProviderEntry{{Provider: "fallback-1", Backend: "bad"}},
						}},
					}},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"fallback-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: true,
			errMsg:  "invalid LLM backend",
		},
		{
			name: "vertexai fallback requires credentials_env",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "vertex-fallback", Backend: LLMBackendLangChain},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"vertex-fallback": {Type: LLMProviderTypeVertexAI, Model: "claude", CredentialsEnv: "VERTEX_CREDS"},
			},
			env:     map[string]string{}, // VERTEX_CREDS not set
			wantErr: true,
			errMsg:  "environment variable VERTEX_CREDS is not set",
		},
		{
			name: "vertexai fallback requires project_env",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "vertex-fallback", Backend: LLMBackendLangChain},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"vertex-fallback": {Type: LLMProviderTypeVertexAI, Model: "claude", ProjectEnv: "VERTEX_PROJECT"},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "environment variable VERTEX_PROJECT is not set",
		},
		{
			name: "vertexai fallback requires location_env",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "vertex-fallback", Backend: LLMBackendLangChain},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"vertex-fallback": {Type: LLMProviderTypeVertexAI, Model: "claude", LocationEnv: "VERTEX_LOCATION"},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "environment variable VERTEX_LOCATION is not set",
		},
		{
			name: "multi-entry error on second entry",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "good-provider", Backend: LLMBackendLangChain},
					{Provider: "bad-provider", Backend: LLMBackendNativeGemini},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"good-provider": {Type: LLMProviderTypeOpenAI, Model: "gpt-5", APIKeyEnv: "GOOD_KEY"},
			},
			env:     map[string]string{"GOOD_KEY": "secret"},
			wantErr: true,
			errMsg:  "LLM provider 'bad-provider' not found",
		},
		{
			name: "fallback google-native with openai provider fails",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb", Backend: LLMBackendNativeGemini},
				},
			},
			providers: map[string]*LLMProviderConfig{
				"openai-fb": {Type: LLMProviderTypeOpenAI, Model: "gpt-5", APIKeyEnv: "FB_KEY"},
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: true,
			errMsg:  "requires a google provider",
		},
		{
			name: "empty fallback list is valid",
			defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{},
			},
			providers: map[string]*LLMProviderConfig{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			chains := tt.chains
			if chains == nil {
				chains = map[string]*ChainConfig{
					"chain1": {
						AlertTypes: []string{"test"},
						Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
					},
				}
			}

			cfg := &Config{
				Defaults:            tt.defaults,
				AgentRegistry:       NewAgentRegistry(baseAgents),
				LLMProviderRegistry: NewLLMProviderRegistry(tt.providers),
				MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
				ChainRegistry:       NewChainRegistry(chains),
			}

			validator := NewValidator(cfg)

			// Test defaults-level validation
			if tt.defaults != nil {
				err := validator.validateDefaults()
				if tt.wantErr {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.errMsg)
				} else {
					assert.NoError(t, err)
				}
				return
			}

			// Test chain/stage/agent-level validation
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

func TestCollectReferencedLLMProviders_IncludesFallbackAndSubAgents(t *testing.T) {
	cfg := &Config{
		Defaults: &Defaults{
			LLMProvider:              "defaults-primary",
			ComposeProvider:          "defaults-compose",
			ExecutiveSummaryProvider: "defaults-exec-summary",
			FallbackProviders: []FallbackProviderEntry{
				{Provider: "defaults-fallback", Backend: LLMBackendNativeGemini},
			},
			Summarization: &SummarizationConfig{
				LLMProvider: "defaults-summarization",
			},
			Agents: map[string]NamedAgentPairing{
				"Worker": {LLMProvider: "defaults-agents-worker"},
			},
		},
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}, "Worker": {}}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{
			"k8s": {
				Transport: TransportConfig{Type: TransportTypeStdio, Command: "npx"},
				Summarization: &SummarizationConfig{
					SizeThresholdTokens: 5000,
					LLMProvider:         "mcp-summarization",
				},
			},
		}),
		ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
			"chain1": {
				AlertTypes:               []string{"test"},
				ComposeProvider:          "chain-compose",
				ExecutiveSummaryProvider: "chain-exec-summary",
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "chain-fallback", Backend: LLMBackendLangChain},
				},
				SubAgents: SubAgentRefs{
					{Name: "Worker", LLMProvider: "chain-subagent"},
				},
				Chat: &ChatConfig{
					Agent: "TestAgent",
					SubAgents: SubAgentRefs{
						{Name: "Worker", LLMProvider: "chat-subagent"},
					},
				},
				Stages: []StageConfig{{
					Name: "s1",
					FallbackProviders: []FallbackProviderEntry{
						{Provider: "stage-fallback", Backend: LLMBackendNativeGemini},
					},
					SubAgents: SubAgentRefs{
						{Name: "Worker", LLMProvider: "stage-subagent"},
					},
					Agents: []StageAgentConfig{{
						Name: "TestAgent",
						FallbackProviders: []FallbackProviderEntry{
							{Provider: "agent-fallback", Backend: LLMBackendLangChain},
						},
						SubAgents: SubAgentRefs{
							{Name: "Worker", LLMProvider: "agent-subagent"},
						},
					}},
				}},
			},
		}),
	}

	validator := NewValidator(cfg)
	referenced := validator.collectReferencedLLMProviders()

	assert.True(t, referenced["defaults-primary"], "defaults primary provider should be referenced")
	assert.True(t, referenced["defaults-compose"], "defaults compose provider should be referenced")
	assert.True(t, referenced["defaults-exec-summary"], "defaults executive_summary_provider should be referenced")
	assert.True(t, referenced["defaults-agents-worker"], "defaults.agents pairing provider should be referenced")
	assert.True(t, referenced["defaults-fallback"], "defaults fallback provider should be referenced")
	assert.True(t, referenced["defaults-summarization"], "defaults summarization provider should be referenced")
	assert.True(t, referenced["mcp-summarization"], "MCP server summarization provider should be referenced")
	assert.True(t, referenced["chain-compose"], "chain compose provider should be referenced")
	assert.True(t, referenced["chain-exec-summary"], "chain executive_summary_provider should be referenced")
	assert.True(t, referenced["chain-fallback"], "chain fallback provider should be referenced")
	assert.True(t, referenced["chain-subagent"], "chain sub-agent provider should be referenced")
	assert.True(t, referenced["chat-subagent"], "chat.sub_agents provider should be referenced")
	assert.True(t, referenced["stage-fallback"], "stage fallback provider should be referenced")
	assert.True(t, referenced["stage-subagent"], "stage sub-agent provider should be referenced")
	assert.True(t, referenced["agent-fallback"], "agent fallback provider should be referenced")
	assert.True(t, referenced["agent-subagent"], "agent sub-agent provider should be referenced")
}

func TestCollectReferencedLLMProviders_NamedFallbackLists(t *testing.T) {
	cfg := &Config{
		Defaults: &Defaults{
			FallbackList:                 "premium",
			ComposeFallbackList:          "compose-list",
			ExecutiveSummaryFallbackList: "exec-list",
			Scoring:                      &ScoringConfig{FallbackList: "scoring-list"},
			Summarization:                &SummarizationConfig{FallbackList: "sum-list"},
			Agents: map[string]NamedAgentPairing{
				"TestAgent": {FallbackList: "agent-pairing-list"},
			},
		},
		FallbackLists: map[string][]FallbackProviderEntry{
			"premium":            {{Provider: "catalog-referenced"}},
			"spare":              {{Provider: "catalog-unused"}},
			"compose-list":       {{Provider: "compose-catalog"}},
			"exec-list":          {{Provider: "exec-catalog"}},
			"scoring-list":       {{Provider: "scoring-catalog"}},
			"sum-list":           {{Provider: "sum-catalog"}},
			"agent-pairing-list": {{Provider: "pairing-catalog"}},
		},
		AgentRegistry:       NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{}),
		MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
		ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
			"chain1": {
				AlertTypes:   []string{"test"},
				FallbackList: "mid",
				Stages: []StageConfig{{
					Name:         "s1",
					FallbackList: "stage-list",
					Agents: []StageAgentConfig{{
						Name:         "TestAgent",
						FallbackList: "agent-list",
					}},
				}},
			},
		}),
	}
	cfg.FallbackLists["mid"] = []FallbackProviderEntry{{Provider: "chain-catalog"}}
	cfg.FallbackLists["stage-list"] = []FallbackProviderEntry{{Provider: "stage-catalog"}}
	cfg.FallbackLists["agent-list"] = []FallbackProviderEntry{{Provider: "agent-catalog"}}

	referenced := NewValidator(cfg).collectReferencedLLMProviders()

	assert.True(t, referenced["catalog-referenced"])
	assert.True(t, referenced["chain-catalog"])
	assert.True(t, referenced["stage-catalog"])
	assert.True(t, referenced["agent-catalog"])
	assert.True(t, referenced["compose-catalog"])
	assert.True(t, referenced["exec-catalog"])
	assert.True(t, referenced["scoring-catalog"])
	assert.True(t, referenced["sum-catalog"])
	assert.True(t, referenced["pairing-catalog"])
	assert.False(t, referenced["catalog-unused"])
}

func TestCollectReferencedLLMProviders_ReachableBuiltinPair(t *testing.T) {
	googleDefault := "google-default"
	web := &AgentConfig{LLMProvider: googleDefault}

	t.Run("reachable WebResearcher includes builtin pair", func(t *testing.T) {
		cfg := &Config{
			Defaults:      &Defaults{LLMProvider: "opus"},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}, "WebResearcher": web}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages: []StageConfig{{
						Name: "s1",
						Agents: []StageAgentConfig{{
							Name:      "TestAgent",
							SubAgents: SubAgentRefs{{Name: "WebResearcher"}},
						}},
					}},
				},
			}),
		}
		referenced := NewValidator(cfg).collectReferencedLLMProviders()
		assert.True(t, referenced[googleDefault])
		assert.True(t, referenced["opus"])
	})

	t.Run("unused builtin in registry is not referenced", func(t *testing.T) {
		cfg := &Config{
			Defaults:      &Defaults{LLMProvider: "opus"},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}, "WebResearcher": web}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			}),
		}
		referenced := NewValidator(cfg).collectReferencedLLMProviders()
		assert.False(t, referenced[googleDefault])
		assert.True(t, referenced["opus"])
	})
}

func TestValidateLLMProviders_ExecutiveSummaryAndBuiltinPair(t *testing.T) {
	t.Run("executive_summary_provider missing key fails", func(t *testing.T) {
		cfg := &Config{
			Defaults: &Defaults{
				LLMProvider:              "primary",
				ExecutiveSummaryProvider: "exec-only",
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
				"primary":   {Type: LLMProviderTypeGoogle, Model: "g", APIKeyEnv: "PRIMARY_KEY"},
				"exec-only": {Type: LLMProviderTypeOpenAI, Model: "gpt", APIKeyEnv: "EXEC_SUMMARY_KEY"},
			}),
			MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			}),
		}
		t.Setenv("PRIMARY_KEY", "secret")
		err := NewValidator(cfg).validateLLMProviders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "EXEC_SUMMARY_KEY")
	})

	t.Run("reachable builtin google-default missing key fails", func(t *testing.T) {
		cfg := &Config{
			Defaults: &Defaults{LLMProvider: "primary"},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"TestAgent":     {},
				"WebResearcher": {LLMProvider: "google-default"},
			}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
				"primary":        {Type: LLMProviderTypeOpenAI, Model: "gpt", APIKeyEnv: "PRIMARY_KEY"},
				"google-default": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "UNUSED_BUILTIN_KEY"},
			}),
			MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}}},
				},
			}),
		}
		t.Setenv("PRIMARY_KEY", "secret")
		err := NewValidator(cfg).validateLLMProviders()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "UNUSED_BUILTIN_KEY")
	})

	t.Run("unused builtin google-default is not credential-checked", func(t *testing.T) {
		cfg := &Config{
			Defaults: &Defaults{LLMProvider: "primary"},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"TestAgent":     {},
				"WebResearcher": {LLMProvider: "google-default"},
			}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
				"primary":        {Type: LLMProviderTypeOpenAI, Model: "gpt", APIKeyEnv: "PRIMARY_KEY"},
				"google-default": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "UNUSED_BUILTIN_KEY"},
			}),
			MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			}),
		}
		t.Setenv("PRIMARY_KEY", "secret")
		require.NoError(t, NewValidator(cfg).validateLLMProviders())
	})
}

func TestValidateLLMProviders_SummarizationOverlayMissingAPIKey(t *testing.T) {
	cfg := &Config{
		Queue:    DefaultQueueConfig(),
		Defaults: &Defaults{},
		AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
			"TestAgent": {MCPServers: []string{"k8s"}},
		}),
		LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
			"flash-only": {
				Type:      LLMProviderTypeGoogle,
				Model:     "gemini-flash",
				APIKeyEnv: "SUMMARIZATION_TEST_API_KEY",
			},
		}),
		MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{
			"k8s": {
				Transport: TransportConfig{Type: TransportTypeStdio, Command: "npx"},
				Summarization: &SummarizationConfig{
					SizeThresholdTokens: 5000,
					LLMProvider:         "flash-only",
				},
			},
		}),
		ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
			"chain1": {
				AlertTypes: []string{"test"},
				Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
			},
		}),
	}

	err := NewValidator(cfg).ValidateAll()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM provider validation failed")
	assert.Contains(t, err.Error(), "environment variable SUMMARIZATION_TEST_API_KEY is not set")
}

func TestValidateSkills(t *testing.T) {
	baseSkills := map[string]*SkillConfig{
		"k8s-basics":   {Name: "k8s-basics", Description: "Kubernetes basics"},
		"networking":   {Name: "networking", Description: "Network skills"},
		"log-analysis": {Name: "log-analysis", Description: "Log analysis"},
	}

	tests := []struct {
		name    string
		agents  map[string]*AgentConfig
		skills  map[string]*SkillConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "nil skills allowlist passes (all skills available)",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: nil},
			},
			skills: baseSkills,
		},
		{
			name: "valid skills allowlist passes",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"k8s-basics", "networking"}},
			},
			skills: baseSkills,
		},
		{
			name: "empty skills allowlist passes (opt-out)",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: &[]string{}},
			},
			skills: baseSkills,
		},
		{
			name: "skills allowlist references nonexistent skill",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"k8s-basics", "nonexistent"}},
			},
			skills:  baseSkills,
			wantErr: true,
			errMsg:  "nonexistent",
		},
		{
			name: "valid required_skills passes",
			agents: map[string]*AgentConfig{
				"agent1": {RequiredSkills: []string{"k8s-basics"}},
			},
			skills: baseSkills,
		},
		{
			name: "required_skills references nonexistent skill",
			agents: map[string]*AgentConfig{
				"agent1": {RequiredSkills: []string{"nonexistent"}},
			},
			skills:  baseSkills,
			wantErr: true,
			errMsg:  "nonexistent",
		},
		{
			name: "required skill within allowlist passes",
			agents: map[string]*AgentConfig{
				"agent1": {
					Skills:         &[]string{"k8s-basics", "networking"},
					RequiredSkills: []string{"k8s-basics"},
				},
			},
			skills: baseSkills,
		},
		{
			name: "required skill outside on-demand allowlist passes (independent validation)",
			agents: map[string]*AgentConfig{
				"agent1": {
					Skills:         &[]string{"k8s-basics"},
					RequiredSkills: []string{"networking"},
				},
			},
			skills: baseSkills,
		},
		{
			name: "required skill with empty on-demand allowlist passes",
			agents: map[string]*AgentConfig{
				"agent1": {
					Skills:         &[]string{},
					RequiredSkills: []string{"k8s-basics"},
				},
			},
			skills: baseSkills,
		},
		{
			name: "nil skill registry with no refs passes",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: nil},
			},
			skills: nil,
		},
		{
			name: "nil skill registry with allowlist ref fails",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"nonexistent"}},
			},
			skills:  nil,
			wantErr: true,
			errMsg:  "nonexistent",
		},
		{
			name: "nil skill registry with required_skills ref fails",
			agents: map[string]*AgentConfig{
				"agent1": {RequiredSkills: []string{"ghost"}},
			},
			skills:  nil,
			wantErr: true,
			errMsg:  "ghost",
		},
		{
			name: "empty skill registry with no agent skill refs passes",
			agents: map[string]*AgentConfig{
				"agent1": {},
			},
			skills: map[string]*SkillConfig{},
		},
		{
			name: "duplicate in skills allowlist fails",
			agents: map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"k8s-basics", "networking", "k8s-basics"}},
			},
			skills:  baseSkills,
			wantErr: true,
			errMsg:  `duplicate skill "k8s-basics"`,
		},
		{
			name: "duplicate in required_skills fails",
			agents: map[string]*AgentConfig{
				"agent1": {RequiredSkills: []string{"k8s-basics", "k8s-basics"}},
			},
			skills:  baseSkills,
			wantErr: true,
			errMsg:  `duplicate skill "k8s-basics"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var skillRegistry *SkillRegistry
			if tt.skills != nil {
				skillRegistry = NewSkillRegistry(tt.skills)
			}

			cfg := &Config{
				AgentRegistry:       NewAgentRegistry(tt.agents),
				ChainRegistry:       NewChainRegistry(nil),
				MCPServerRegistry:   NewMCPServerRegistry(nil),
				LLMProviderRegistry: NewLLMProviderRegistry(nil),
				SkillRegistry:       skillRegistry,
			}

			v := NewValidator(cfg)
			err := v.validateSkills()

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestValidateSkills_ErrorTypes(t *testing.T) {
	skills := map[string]*SkillConfig{
		"k8s-basics": {Name: "k8s-basics", Description: "Kubernetes basics"},
	}

	t.Run("allowlist miss wraps ErrSkillNotFound in ValidationError", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"nonexistent"}},
			}),
			ChainRegistry:       NewChainRegistry(nil),
			MCPServerRegistry:   NewMCPServerRegistry(nil),
			LLMProviderRegistry: NewLLMProviderRegistry(nil),
			SkillRegistry:       NewSkillRegistry(skills),
		}

		err := NewValidator(cfg).validateSkills()
		require.Error(t, err)

		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "agent", ve.Component)
		assert.Equal(t, "agent1", ve.ID)
		assert.Equal(t, "skills", ve.Field)
		assert.ErrorIs(t, ve.Err, ErrSkillNotFound)
	})

	t.Run("required skill miss wraps ErrSkillNotFound in ValidationError", func(t *testing.T) {
		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"agent1": {RequiredSkills: []string{"ghost"}},
			}),
			ChainRegistry:       NewChainRegistry(nil),
			MCPServerRegistry:   NewMCPServerRegistry(nil),
			LLMProviderRegistry: NewLLMProviderRegistry(nil),
			SkillRegistry:       NewSkillRegistry(skills),
		}

		err := NewValidator(cfg).validateSkills()
		require.Error(t, err)

		var ve *ValidationError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "required_skills", ve.Field)
		assert.ErrorIs(t, ve.Err, ErrSkillNotFound)
	})
}

func TestValidateSkills_IntegrationWithValidateAll(t *testing.T) {
	t.Run("invalid skill ref fails ValidateAll", func(t *testing.T) {
		cfg := &Config{
			Queue: DefaultQueueConfig(),
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"agent1": {Skills: &[]string{"nonexistent"}},
			}),
			ChainRegistry:       NewChainRegistry(nil),
			MCPServerRegistry:   NewMCPServerRegistry(nil),
			LLMProviderRegistry: NewLLMProviderRegistry(nil),
			SkillRegistry:       NewSkillRegistry(map[string]*SkillConfig{}),
		}

		err := NewValidator(cfg).ValidateAll()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skill validation failed")
	})

	t.Run("valid skills pass ValidateAll", func(t *testing.T) {
		cfg := &Config{
			Queue: DefaultQueueConfig(),
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"agent1": {
					Skills:         &[]string{"k8s"},
					RequiredSkills: []string{"k8s"},
				},
			}),
			ChainRegistry:       NewChainRegistry(nil),
			MCPServerRegistry:   NewMCPServerRegistry(nil),
			LLMProviderRegistry: NewLLMProviderRegistry(nil),
			SkillRegistry: NewSkillRegistry(map[string]*SkillConfig{
				"k8s": {Name: "k8s", Description: "Kubernetes"},
			}),
		}

		err := NewValidator(cfg).ValidateAll()
		require.NoError(t, err)
	})
}

func intPtr(i int) *int {
	return &i
}

func durPtr(d time.Duration) *time.Duration {
	return &d
}

func TestWarnMemoryWithoutScoring(t *testing.T) {
	captureLogs := func(t *testing.T) (*bytes.Buffer, func()) {
		t.Helper()
		var buf bytes.Buffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		return &buf, func() { slog.SetDefault(old) }
	}

	t.Run("warns when memory enabled and no scoring anywhere", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain-a": {},
			}),
		}
		v := NewValidator(cfg)
		v.warnMemoryWithoutScoring(&Defaults{
			Memory: &MemoryConfig{Enabled: true},
		})

		assert.Contains(t, buf.String(), "Memory is enabled but no chain has scoring enabled")
	})

	t.Run("no warning when defaults.scoring.enabled is true", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain-a": {},
			}),
		}
		v := NewValidator(cfg)
		v.warnMemoryWithoutScoring(&Defaults{
			Memory:  &MemoryConfig{Enabled: true},
			Scoring: &ScoringConfig{Enabled: true},
		})

		assert.Empty(t, buf.String())
	})

	t.Run("no warning when at least one chain has scoring enabled", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"no-scoring":  {},
				"has-scoring": {Scoring: &ScoringConfig{Enabled: true}},
			}),
		}
		v := NewValidator(cfg)
		v.warnMemoryWithoutScoring(&Defaults{
			Memory: &MemoryConfig{Enabled: true},
		})

		assert.Empty(t, buf.String())
	})

	t.Run("warns when scoring exists but is disabled", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain-a": {Scoring: &ScoringConfig{Enabled: false}},
			}),
		}
		v := NewValidator(cfg)
		v.warnMemoryWithoutScoring(&Defaults{
			Memory:  &MemoryConfig{Enabled: true},
			Scoring: &ScoringConfig{Enabled: false},
		})

		assert.Contains(t, buf.String(), "Memory is enabled but no chain has scoring enabled")
	})

	t.Run("warns when defaults enabled but all chains explicitly disable scoring", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain-a": {Scoring: &ScoringConfig{Enabled: false}},
				"chain-b": {Scoring: &ScoringConfig{Enabled: false}},
			}),
		}
		v := NewValidator(cfg)
		v.warnMemoryWithoutScoring(&Defaults{
			Memory:  &MemoryConfig{Enabled: true},
			Scoring: &ScoringConfig{Enabled: true},
		})

		assert.Contains(t, buf.String(), "Memory is enabled but no chain has scoring enabled")
	})
}

func TestWarnNativeToolAgentsWithoutCompatibleFallback(t *testing.T) {
	captureLogs := func(t *testing.T) (*bytes.Buffer, func()) {
		t.Helper()
		var buf bytes.Buffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		return &buf, func() { slog.SetDefault(old) }
	}

	t.Run("warns when native-tool agent has only langchain fallback", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb", Backend: LLMBackendLangChain},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
						GoogleNativeToolURLContext:   true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
		assert.Contains(t, buf.String(), "WebResearcher")
	})

	t.Run("warns when native-tool agent has fallback with omitted backend", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb"},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
	})

	t.Run("no warning when fallback includes google-native entry", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb", Backend: LLMBackendLangChain},
					{Provider: "gemini-fb", Backend: LLMBackendNativeGemini},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Empty(t, buf.String())
	})

	t.Run("no warning when agent has no native tools", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb", Backend: LLMBackendLangChain},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"KubernetesAgent": {},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "KubernetesAgent"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Empty(t, buf.String())
	})

	t.Run("no warning when no fallback providers configured", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Empty(t, buf.String())
	})

	t.Run("agent-level fallback overrides chain-level", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "gemini-fb", Backend: LLMBackendNativeGemini},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{
							Name: "s1",
							Agents: []StageAgentConfig{{
								Name: "WebResearcher",
								FallbackProviders: []FallbackProviderEntry{
									{Provider: "openai-only", Backend: LLMBackendLangChain},
								},
							}},
						},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
	})

	t.Run("warns for native-tool sub-agent refs", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{
					{Provider: "openai-fb", Backend: LLMBackendLangChain},
				},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"Orchestrator": {},
				"CodeExecutor": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolCodeExecution: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					SubAgents: SubAgentRefs{{Name: "CodeExecutor"}},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "Orchestrator"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
		assert.Contains(t, buf.String(), "CodeExecutor")
	})

	t.Run("warns when named list has only langchain entries", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{FallbackList: "langchain-only"},
			FallbackLists: map[string][]FallbackProviderEntry{
				"langchain-only": {{Provider: "openai-fb", Backend: LLMBackendLangChain}},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
		assert.Contains(t, buf.String(), "WebResearcher")
	})

	t.Run("no warning when named list includes google-native entry", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{FallbackList: "google-native"},
			FallbackLists: map[string][]FallbackProviderEntry{
				"google-native": {{Provider: "google-fb", Backend: LLMBackendNativeGemini}},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		v := NewValidator(cfg)
		v.warnNativeToolAgentsWithoutCompatibleFallback()

		assert.NotContains(t, buf.String(), "native-tool agent with no compatible fallback")
	})

	t.Run("warns for named list on chat sub-agent ref", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{FallbackList: "langchain-only"},
			FallbackLists: map[string][]FallbackProviderEntry{
				"langchain-only": {{Provider: "openai-fb", Backend: LLMBackendLangChain}},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"Orchestrator": {},
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Chat: &ChatConfig{
						SubAgents: SubAgentRefs{{Name: "WebResearcher"}},
					},
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "Orchestrator"}}},
					},
				},
			}),
		}
		NewValidator(cfg).warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
		assert.Contains(t, buf.String(), "WebResearcher")
	})

	t.Run("warns for named list on stage sub-agent ref", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{FallbackList: "langchain-only"},
			FallbackLists: map[string][]FallbackProviderEntry{
				"langchain-only": {{Provider: "openai-fb", Backend: LLMBackendLangChain}},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"Orchestrator": {},
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{{
						Name:      "s1",
						SubAgents: SubAgentRefs{{Name: "WebResearcher"}},
						Agents:    []StageAgentConfig{{Name: "Orchestrator"}},
					}},
				},
			}),
		}
		NewValidator(cfg).warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Contains(t, buf.String(), "native-tool agent with no compatible fallback")
		assert.Contains(t, buf.String(), "WebResearcher")
	})

	t.Run("defaults.agents named list suppresses native-tool warning", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackList: "langchain-only",
				Agents: map[string]NamedAgentPairing{
					"WebResearcher": {FallbackList: "google-native"},
				},
			},
			FallbackLists: map[string][]FallbackProviderEntry{
				"langchain-only": {{Provider: "openai-fb", Backend: LLMBackendLangChain}},
				"google-native":  {{Provider: "google-fb", Backend: LLMBackendNativeGemini}},
			},
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{
				"WebResearcher": {
					NativeTools: map[GoogleNativeTool]bool{
						GoogleNativeToolGoogleSearch: true,
					},
				},
			}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"test-chain": {
					Stages: []StageConfig{
						{Name: "s1", Agents: []StageAgentConfig{{Name: "WebResearcher"}}},
					},
				},
			}),
		}
		NewValidator(cfg).warnNativeToolAgentsWithoutCompatibleFallback()

		assert.Empty(t, buf.String())
	})
}

func TestValidateNamedFallbackLists(t *testing.T) {
	baseAgents := map[string]*AgentConfig{"TestAgent": {}}
	baseChains := map[string]*ChainConfig{
		"chain1": {
			AlertTypes: []string{"test"},
			Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
		},
	}
	providers := map[string]*LLMProviderConfig{
		"fb-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
		"fb-2": {Type: LLMProviderTypeOpenAI, Model: "gpt-5", APIKeyEnv: "UNUSED_KEY"},
	}

	tests := []struct {
		name    string
		cfg     func() *Config
		env     map[string]string
		wantErr string
		alsoLLM bool
	}{
		{
			name: "unknown list name",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{FallbackList: "ghost"},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults '': field 'fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "typo provider in unused list still fails",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"spare": {{Provider: "ghost"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: "fallback_lists 'spare': field '[0]': LLM provider 'ghost' not found",
		},
		{
			name: "missing creds fail on referenced list",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{FallbackList: "premium"},
					FallbackLists: map[string][]FallbackProviderEntry{
						"premium": {{Provider: "fb-1"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: "fallback_lists 'premium': field '[0]': environment variable FB_KEY is not set (required by fallback provider 'fb-1')",
		},
		{
			name: "missing creds on unused list pass",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"spare": {{Provider: "fb-2"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			alsoLLM: true,
		},
		{
			name: "both fields on defaults including empty inline",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{
						FallbackList:      "premium",
						FallbackProviders: []FallbackProviderEntry{},
					},
					FallbackLists: map[string][]FallbackProviderEntry{
						"premium": {{Provider: "fb-1"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: "defaults '': field 'fallback_list': cannot set both fallback_list and fallback_providers",
		},
		{
			name: "both fields on chain",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"premium": {{Provider: "fb-1"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
						"chain1": {
							AlertTypes:        []string{"test"},
							FallbackList:      "premium",
							FallbackProviders: []FallbackProviderEntry{{Provider: "fb-1"}},
							Stages:            []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
						},
					}),
				}
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: "chain 'chain1': field 'fallback_list': cannot set both fallback_list and fallback_providers",
		},
		{
			name: "empty named list is valid",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{FallbackList: "empty"},
					FallbackLists: map[string][]FallbackProviderEntry{
						"empty": {},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
		},
		{
			name: "empty catalog key is rejected",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"": {{Provider: "fb-1"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			env:     map[string]string{"FB_KEY": "secret"},
			wantErr: "fallback_lists '': fallback list name must be non-empty",
		},
		{
			name: "valid named default list",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{FallbackList: "premium"},
					FallbackLists: map[string][]FallbackProviderEntry{
						"premium": {{Provider: "fb-1", Backend: LLMBackendNativeGemini}},
						"spare":   {{Provider: "fb-2"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					MCPServerRegistry:   NewMCPServerRegistry(map[string]*MCPServerConfig{}),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			env:     map[string]string{"FB_KEY": "secret"},
			alsoLLM: true,
		},
		{
			name: "defaults list still needs creds when every chain overrides",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{FallbackList: "premium"},
					FallbackLists: map[string][]FallbackProviderEntry{
						"premium": {{Provider: "fb-1"}},
						"mid":     {{Provider: "fb-2"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
						"chain1": {
							AlertTypes:   []string{"test"},
							FallbackList: "mid",
							Stages:       []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
						},
					}),
				}
			},
			env:     map[string]string{"UNUSED_KEY": "secret"},
			wantErr: "fallback_lists 'premium': field '[0]': environment variable FB_KEY is not set (required by fallback provider 'fb-1')",
		},
		{
			name: "google-native on unused non-google list fails structure",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"spare": {{Provider: "fb-2", Backend: LLMBackendNativeGemini}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `fallback_lists 'spare': field '[0]': llm_backend "google-native" requires a google provider, got type "openai" for "fb-2"`,
		},
		{
			name: "invalid backend in unused list fails structure",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{},
					FallbackLists: map[string][]FallbackProviderEntry{
						"spare": {{Provider: "fb-2", Backend: "bad"}},
					},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: "fallback_lists 'spare': field '[0]': invalid LLM backend: bad",
		},
		{
			name: "unknown defaults.agents fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults: &Defaults{
						Agents: map[string]NamedAgentPairing{
							"TestAgent": {FallbackList: "ghost"},
						},
					},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults 'TestAgent': field 'agents.fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown compose_fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{ComposeFallbackList: "ghost"},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults '': field 'compose_fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown executive_summary_fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{ExecutiveSummaryFallbackList: "ghost"},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults '': field 'executive_summary_fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown scoring.fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{Scoring: &ScoringConfig{FallbackList: "ghost"}},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults '': field 'scoring.fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown summarization.fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{Summarization: &SummarizationConfig{FallbackList: "ghost"}},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry:       NewChainRegistry(baseChains),
				}
			},
			wantErr: `defaults '': field 'summarization.fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown chat.fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
						"chain1": {
							AlertTypes: []string{"test"},
							Chat:       &ChatConfig{FallbackList: "ghost"},
							Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
						},
					}),
				}
			},
			wantErr: `chain 'chain1': field 'chat.fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown sub-agent ref fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
						"chain1": {
							AlertTypes: []string{"test"},
							SubAgents:  SubAgentRefs{{Name: "TestAgent", FallbackList: "ghost"}},
							Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
						},
					}),
				}
			},
			wantErr: `chain 'chain1': field 'sub_agents[TestAgent].fallback_list': unknown fallback list "ghost"`,
		},
		{
			name: "unknown chat.sub_agents fallback_list",
			cfg: func() *Config {
				return &Config{
					Defaults:            &Defaults{},
					FallbackLists:       map[string][]FallbackProviderEntry{},
					AgentRegistry:       NewAgentRegistry(baseAgents),
					LLMProviderRegistry: NewLLMProviderRegistry(providers),
					ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
						"chain1": {
							AlertTypes: []string{"test"},
							Chat: &ChatConfig{
								SubAgents: SubAgentRefs{{Name: "TestAgent", FallbackList: "ghost"}},
							},
							Stages: []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
						},
					}),
				}
			},
			wantErr: `chain 'chain1': field 'chat.sub_agents[TestAgent].fallback_list': unknown fallback list "ghost"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			v := NewValidator(tt.cfg())
			err := v.validateNamedFallbackLists()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			if tt.alsoLLM {
				require.NoError(t, v.validateLLMProviders())
			}
		})
	}
}

func TestWarnDeprecatedFallbackProviders(t *testing.T) {
	captureLogs := func(t *testing.T) (*bytes.Buffer, func()) {
		t.Helper()
		var buf bytes.Buffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		return &buf, func() { slog.SetDefault(old) }
	}

	t.Run("warns for defaults including empty slice", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults: &Defaults{
				FallbackProviders: []FallbackProviderEntry{},
			},
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{}),
		}
		NewValidator(cfg).warnDeprecatedFallbackProviders()

		assert.Contains(t, buf.String(), "fallback_providers is deprecated")
		assert.Contains(t, buf.String(), "defaults")
	})

	t.Run("warns for chain stage and stage-agent", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"c1": {
					FallbackProviders: []FallbackProviderEntry{{Provider: "x"}},
					Stages: []StageConfig{{
						Name:              "s1",
						FallbackProviders: []FallbackProviderEntry{{Provider: "y"}},
						Agents: []StageAgentConfig{{
							Name:              "A",
							FallbackProviders: []FallbackProviderEntry{{Provider: "z"}},
						}},
					}},
				},
			}),
		}
		NewValidator(cfg).warnDeprecatedFallbackProviders()

		logs := buf.String()
		assert.Contains(t, logs, "fallback_providers is deprecated")
		assert.Contains(t, logs, "chain=c1")
		assert.Contains(t, logs, "stage=s1")
		assert.Contains(t, logs, "agent=A")
	})

	t.Run("no warning when only fallback_list is set", func(t *testing.T) {
		buf, restore := captureLogs(t)
		t.Cleanup(restore)

		cfg := &Config{
			Defaults:      &Defaults{FallbackList: "premium"},
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{}),
		}
		NewValidator(cfg).warnDeprecatedFallbackProviders()

		assert.NotContains(t, buf.String(), "fallback_providers is deprecated")
	})
}

func TestValidateAll_NamedFallbackLists(t *testing.T) {
	minimal := func(fallbackList string, lists map[string][]FallbackProviderEntry) *Config {
		return &Config{
			Queue:         DefaultQueueConfig(),
			Defaults:      &Defaults{FallbackList: fallbackList},
			FallbackLists: lists,
			AgentRegistry: NewAgentRegistry(map[string]*AgentConfig{"TestAgent": {}}),
			LLMProviderRegistry: NewLLMProviderRegistry(map[string]*LLMProviderConfig{
				"fb-1": {Type: LLMProviderTypeGoogle, Model: "gemini", APIKeyEnv: "FB_KEY"},
			}),
			MCPServerRegistry: NewMCPServerRegistry(map[string]*MCPServerConfig{}),
			ChainRegistry: NewChainRegistry(map[string]*ChainConfig{
				"chain1": {
					AlertTypes: []string{"test"},
					Stages:     []StageConfig{{Name: "s1", Agents: []StageAgentConfig{{Name: "TestAgent"}}}},
				},
			}),
		}
	}

	t.Run("unknown list name fails ValidateAll", func(t *testing.T) {
		err := NewValidator(minimal("ghost", map[string][]FallbackProviderEntry{})).ValidateAll()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fallback list validation failed")
		assert.Contains(t, err.Error(), `unknown fallback list "ghost"`)
	})

	t.Run("valid named list passes ValidateAll", func(t *testing.T) {
		t.Setenv("FB_KEY", "secret")
		err := NewValidator(minimal("premium", map[string][]FallbackProviderEntry{
			"premium": {{Provider: "fb-1"}},
		})).ValidateAll()
		require.NoError(t, err)
	})
}
