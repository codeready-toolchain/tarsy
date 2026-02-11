package queue

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMCPSelection(t *testing.T) {
	// Build a test registry with known servers
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {},
		"argocd-server":     {},
		"prometheus-server": {},
	})
	cfg := &config.Config{
		MCPServerRegistry: registry,
	}
	executor := &RealSessionExecutor{cfg: cfg}

	t.Run("no override returns chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: nil,
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server", "argocd-server"},
		}

		serverIDs, toolFilter, err := executor.resolveMCPSelection(session, resolved)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server", "argocd-server"}, serverIDs)
		assert.Nil(t, toolFilter)
	})

	t.Run("empty map returns chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		serverIDs, toolFilter, err := executor.resolveMCPSelection(session, resolved)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server"}, serverIDs)
		assert.Nil(t, toolFilter)
	})

	t.Run("override replaces chain config", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "prometheus-server"},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server", "argocd-server"},
		}

		serverIDs, toolFilter, err := executor.resolveMCPSelection(session, resolved)
		require.NoError(t, err)
		assert.Equal(t, []string{"prometheus-server"}, serverIDs)
		assert.Nil(t, toolFilter) // No tool filter specified
	})

	t.Run("override with tool filter", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{
						"name":  "kubernetes-server",
						"tools": []interface{}{"get_pods", "describe_pod"},
					},
					map[string]interface{}{
						"name": "argocd-server",
					},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"prometheus-server"},
		}

		serverIDs, toolFilter, err := executor.resolveMCPSelection(session, resolved)
		require.NoError(t, err)
		assert.Equal(t, []string{"kubernetes-server", "argocd-server"}, serverIDs)
		require.NotNil(t, toolFilter)
		assert.Equal(t, []string{"get_pods", "describe_pod"}, toolFilter["kubernetes-server"])
		_, hasArgoFilter := toolFilter["argocd-server"]
		assert.False(t, hasArgoFilter, "argocd-server should not have a filter")
	})

	t.Run("unknown server in override returns error", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "nonexistent-server"},
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		_, _, err := executor.resolveMCPSelection(session, resolved)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent-server")
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("override with native tools sets NativeToolsOverride", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
				},
				"native_tools": map[string]interface{}{
					"google_search": false,
				},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"argocd-server"},
		}

		_, _, err := executor.resolveMCPSelection(session, resolved)
		require.NoError(t, err)
		require.NotNil(t, resolved.NativeToolsOverride)
		require.NotNil(t, resolved.NativeToolsOverride.GoogleSearch)
		assert.False(t, *resolved.NativeToolsOverride.GoogleSearch)
	})

	t.Run("empty servers in override returns error", func(t *testing.T) {
		session := &ent.AlertSession{
			McpSelection: map[string]interface{}{
				"servers": []interface{}{},
			},
		}
		resolved := &agent.ResolvedAgentConfig{
			MCPServers: []string{"kubernetes-server"},
		}

		_, _, err := executor.resolveMCPSelection(session, resolved)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one server")
	})
}
