package services

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// setupTestSessionService creates a SessionService with test configuration for testing
func setupTestSessionService(_ *testing.T, client *ent.Client) *SessionService {
	// Create test registries with minimal valid configuration
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{
		"k8s-analysis": {
			AlertTypes: []string{"kubernetes"},
			Stages: []config.StageConfig{
				{
					Name: "analysis",
					Agents: []config.StageAgentConfig{
						{Name: "KubernetesAgent"},
					},
				},
			},
		},
		"k8s-deep-analysis": {
			AlertTypes: []string{"kubernetes"},
			Stages: []config.StageConfig{
				{
					Name: "deep-analysis",
					Agents: []config.StageAgentConfig{
						{Name: "KubernetesAgent"},
					},
				},
			},
		},
		"test-chain": {
			AlertTypes: []string{"test"},
			Stages: []config.StageConfig{
				{
					Name: "stage1",
					Agents: []config.StageAgentConfig{
						{Name: "test-agent"},
					},
				},
			},
		},
		"chat-disabled-chain": {
			AlertTypes: []string{"test-no-chat"},
			Chat:       &config.ChatConfig{Enabled: false},
			Stages: []config.StageConfig{
				{
					Name: "stage1",
					Agents: []config.StageAgentConfig{
						{Name: "test-agent"},
					},
				},
			},
		},
	})

	mcpServerRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {
			Transport: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "test-command",
			},
		},
		"test-server": {
			Transport: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "test-command",
			},
		},
	})

	return NewSessionService(client, chainRegistry, mcpServerRegistry)
}
