package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMCPSelectionConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]interface{}
		wantNil bool
		wantErr string
		check   func(t *testing.T, cfg *MCPSelectionConfig)
	}{
		{
			name:    "nil input returns nil",
			raw:     nil,
			wantNil: true,
		},
		{
			name:    "empty map returns nil",
			raw:     map[string]interface{}{},
			wantNil: true,
		},
		{
			name: "valid single server",
			raw: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
				},
			},
			check: func(t *testing.T, cfg *MCPSelectionConfig) {
				require.Len(t, cfg.Servers, 1)
				assert.Equal(t, "kubernetes-server", cfg.Servers[0].Name)
				assert.Empty(t, cfg.Servers[0].Tools)
				assert.Nil(t, cfg.NativeTools)
			},
		},
		{
			name: "valid server with tool filter",
			raw: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{
						"name":  "kubernetes-server",
						"tools": []interface{}{"get_pods", "describe_pod"},
					},
				},
			},
			check: func(t *testing.T, cfg *MCPSelectionConfig) {
				require.Len(t, cfg.Servers, 1)
				assert.Equal(t, "kubernetes-server", cfg.Servers[0].Name)
				assert.Equal(t, []string{"get_pods", "describe_pod"}, cfg.Servers[0].Tools)
			},
		},
		{
			name: "multiple servers",
			raw: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
					map[string]interface{}{"name": "argocd-server", "tools": []interface{}{"get_application_status"}},
				},
			},
			check: func(t *testing.T, cfg *MCPSelectionConfig) {
				require.Len(t, cfg.Servers, 2)
				assert.Equal(t, "kubernetes-server", cfg.Servers[0].Name)
				assert.Equal(t, "argocd-server", cfg.Servers[1].Name)
				assert.Equal(t, []string{"get_application_status"}, cfg.Servers[1].Tools)
			},
		},
		{
			name: "with native tools override",
			raw: map[string]interface{}{
				"servers": []interface{}{
					map[string]interface{}{"name": "kubernetes-server"},
				},
				"native_tools": map[string]interface{}{
					"google_search":  false,
					"code_execution": true,
				},
			},
			check: func(t *testing.T, cfg *MCPSelectionConfig) {
				require.Len(t, cfg.Servers, 1)
				require.NotNil(t, cfg.NativeTools)
				require.NotNil(t, cfg.NativeTools.GoogleSearch)
				assert.False(t, *cfg.NativeTools.GoogleSearch)
				require.NotNil(t, cfg.NativeTools.CodeExecution)
				assert.True(t, *cfg.NativeTools.CodeExecution)
				assert.Nil(t, cfg.NativeTools.URLContext) // not set = nil
			},
		},
		{
			name: "empty servers list returns error",
			raw: map[string]interface{}{
				"servers": []interface{}{},
			},
			wantErr: "MCP selection must have at least one server",
		},
		{
			name: "servers key missing still fails (no servers)",
			raw: map[string]interface{}{
				"native_tools": map[string]interface{}{"google_search": true},
			},
			wantErr: "MCP selection must have at least one server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseMCPSelectionConfig(tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, cfg)
				return
			}
			require.NotNil(t, cfg)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
