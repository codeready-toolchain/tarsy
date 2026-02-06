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
	builtin := GetBuiltinConfig()

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

			// Need to ensure builtin config is available for pattern validation
			_ = builtin

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
			name: "provider with missing API key",
			providers: map[string]*LLMProviderConfig{
				"test-provider": {
					Type:                LLMProviderTypeGoogle,
					Model:               "test-model",
					APIKeyEnv:           "MISSING_API_KEY",
					MaxToolResultTokens: 100000,
				},
			},
			env:     map[string]string{},
			wantErr: true,
			errMsg:  "environment variable MISSING_API_KEY is not set",
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

func TestValidationError(t *testing.T) {
	err := NewValidationError("agent", "test-agent", "mcp_servers", assert.AnError)

	assert.Equal(t, "agent", err.Component)
	assert.Equal(t, "test-agent", err.ID)
	assert.Equal(t, "mcp_servers", err.Field)
	assert.Contains(t, err.Error(), "agent 'test-agent'")
	assert.Contains(t, err.Error(), "mcp_servers")
	assert.Same(t, assert.AnError, err.Unwrap())
}
