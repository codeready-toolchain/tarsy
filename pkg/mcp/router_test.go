package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "double underscore to dot",
			input:    "kubernetes-server__get_pods",
			expected: "kubernetes-server.get_pods",
		},
		{
			name:     "already dotted passthrough",
			input:    "kubernetes-server.get_pods",
			expected: "kubernetes-server.get_pods",
		},
		{
			name:     "no separator passthrough",
			input:    "get_pods",
			expected: "get_pods",
		},
		{
			name:     "both dot and underscore keeps dot",
			input:    "server.tool__name",
			expected: "server.tool__name",
		},
		{
			name:     "only first double underscore replaced",
			input:    "server__tool__extra",
			expected: "server.tool__extra",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeBuiltinPlainToolName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"dispatch_agent", "dispatch_agent"},
		{"google:dispatch_agent", "dispatch_agent"},
		{"google:load_skill", "load_skill"},
		{"x:recall_past_investigations", "recall_past_investigations"},
		{"kubernetes.get_pods", "kubernetes.get_pods"},
		{"google:read_file", "google:read_file"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeBuiltinPlainToolName(tt.in))
		})
	}
}

func TestSplitToolName_ErrorHints(t *testing.T) {
	t.Run("colon prefixed orchestration tool", func(t *testing.T) {
		_, _, err := SplitToolName("google:dispatch_agent")
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "orchestration")
		assert.Contains(t, msg, "dispatch_agent")
		assert.NotContains(t, strings.ToLower(msg), "must be in 'server.tool'")
	})

	t.Run("colon prefixed skill tool", func(t *testing.T) {
		_, _, err := SplitToolName("google:load_skill")
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "skill")
		assert.Contains(t, msg, "load_skill")
	})

	t.Run("colon prefixed memory tool", func(t *testing.T) {
		_, _, err := SplitToolName("openai:search_past_sessions")
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "memory")
		assert.Contains(t, msg, "search_past_sessions")
	})

	t.Run("plain orchestration name routed to MCP", func(t *testing.T) {
		_, _, err := SplitToolName("list_agents")
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "orchestration")
		assert.Contains(t, msg, "list_agents")
	})

	t.Run("plain load_skill routed to MCP", func(t *testing.T) {
		_, _, err := SplitToolName("load_skill")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skill")
	})

	t.Run("genuinely malformed MCP name", func(t *testing.T) {
		_, _, err := SplitToolName("resources_get")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "one dot")
	})
}

func TestSplitToolName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantServer string
		wantTool   string
		wantErr    bool
	}{
		{
			name:       "valid simple",
			input:      "kubernetes.get_pods",
			wantServer: "kubernetes",
			wantTool:   "get_pods",
		},
		{
			name:       "valid with hyphens",
			input:      "kubernetes-server.get-pods",
			wantServer: "kubernetes-server",
			wantTool:   "get-pods",
		},
		{
			name:       "valid with numbers",
			input:      "server1.tool2",
			wantServer: "server1",
			wantTool:   "tool2",
		},
		{
			name:       "valid with underscores",
			input:      "my_server.my_tool",
			wantServer: "my_server",
			wantTool:   "my_tool",
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no dot",
			input:   "kubernetes_get_pods",
			wantErr: true,
		},
		{
			name:    "multiple dots",
			input:   "server.tool.extra",
			wantErr: true,
		},
		{
			name:    "dot at start",
			input:   ".tool",
			wantErr: true,
		},
		{
			name:    "dot at end",
			input:   "server.",
			wantErr: true,
		},
		{
			name:    "only dot",
			input:   ".",
			wantErr: true,
		},
		{
			name:    "spaces in name",
			input:   "my server.my tool",
			wantErr: true,
		},
		{
			name:    "starts with hyphen",
			input:   "-server.tool",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, tool, err := SplitToolName(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, server)
				assert.Empty(t, tool)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantTool, tool)
		})
	}
}
