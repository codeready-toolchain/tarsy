package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// newTestExecutor creates an ToolExecutor with in-memory MCP servers.
func newTestExecutor(t *testing.T, servers map[string]map[string]mcpsdk.ToolHandler) *ToolExecutor {
	t.Helper()

	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)
	var serverIDs []string

	for serverID, tools := range servers {
		ts := startTestServer(t, serverID, tools)
		serverIDs = append(serverIDs, serverID)

		// Directly wire up the client session
		sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{
			Name: "tarsy-test", Version: "test",
		}, nil)
		session, err := sdkClient.Connect(context.Background(), ts.clientTransport, nil)
		require.NoError(t, err)

		client.mu.Lock()
		client.sessions[serverID] = session
		client.clients[serverID] = sdkClient
		client.mu.Unlock()
	}

	executor := NewToolExecutor(client, registry, serverIDs, nil)
	t.Cleanup(func() { _ = executor.Close() })
	return executor
}

func TestToolExecutor_Execute_JSON(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pod-1, pod-2"}},
				}, nil
			},
		},
	})

	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-1",
		Name:      "kubernetes.get_pods",
		Arguments: `{"namespace": "default"}`,
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "pod-1, pod-2", result.Content)
	assert.Equal(t, "call-1", result.CallID)
}

func TestToolExecutor_Execute_KeyValue(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}},
				}, nil
			},
		},
	})

	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-2",
		Name:      "kubernetes.get_pods",
		Arguments: "namespace: default",
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "ok", result.Content)
}

func TestToolExecutor_Execute_NativeThinkingName(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}},
				}, nil
			},
		},
	})

	// NativeThinking uses __ instead of .
	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-3",
		Name:      "kubernetes__get_pods",
		Arguments: `{"namespace": "default"}`,
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "ok", result.Content)
}

func TestToolExecutor_Execute_UnknownServer(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
	})

	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-4",
		Name:      "nonexistent.get_pods",
		Arguments: "{}",
	})

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "not available")
}

func TestToolExecutor_Execute_InvalidToolName(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
	})

	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-5",
		Name:      "just_a_tool",
		Arguments: "{}",
	})

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "invalid tool name")
}

func TestToolExecutor_Execute_MCPError(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"bad_tool": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "something went wrong"}},
					IsError: true,
				}, nil
			},
		},
	})

	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-6",
		Name:      "kubernetes.bad_tool",
		Arguments: "{}",
	})

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "something went wrong")
}

func TestToolExecutor_ListTools(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
			"get_logs": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
	})

	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 2)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	assert.Contains(t, names, "kubernetes.get_pods")
	assert.Contains(t, names, "kubernetes.get_logs")
}

func TestToolExecutor_ListTools_MultiServer(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
		"github": {
			"list_repos": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
	})

	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 2)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	assert.Contains(t, names, "kubernetes.get_pods")
	assert.Contains(t, names, "github.list_repos")
}

func TestToolExecutor_ListTools_WithFilter(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)

	ts := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
		"get_logs": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
		"delete_pod": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := sdkClient.Connect(context.Background(), ts.clientTransport, nil)
	require.NoError(t, err)
	client.mu.Lock()
	client.sessions["kubernetes"] = session
	client.clients["kubernetes"] = sdkClient
	client.mu.Unlock()

	// Only allow get_pods and get_logs
	filter := map[string][]string{
		"kubernetes": {"get_pods", "get_logs"},
	}
	executor := NewToolExecutor(client, registry, []string{"kubernetes"}, filter)
	t.Cleanup(func() { _ = executor.Close() })

	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 2)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	assert.Contains(t, names, "kubernetes.get_pods")
	assert.Contains(t, names, "kubernetes.get_logs")
	assert.NotContains(t, names, "kubernetes.delete_pod")
}

func TestToolExecutor_Close(t *testing.T) {
	executor := newTestExecutor(t, map[string]map[string]mcpsdk.ToolHandler{
		"kubernetes": {
			"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
				return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
			},
		},
	})

	// Close should not error
	err := executor.Close()
	assert.NoError(t, err)
}
