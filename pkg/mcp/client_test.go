package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// emptySchema is a minimal valid JSON Schema for test tools.
var emptySchema = json.RawMessage(`{"type":"object"}`)

// testMCPServer holds an in-memory MCP server and its transport pair.
type testMCPServer struct {
	server          *mcpsdk.Server
	clientTransport *mcpsdk.InMemoryTransport
	serverTransport *mcpsdk.InMemoryTransport
}

// startTestServer creates an in-memory MCP server with given tools and connects it.
func startTestServer(t *testing.T, name string, tools map[string]mcpsdk.ToolHandler) *testMCPServer {
	t.Helper()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name: name, Version: "test",
	}, nil)

	for toolName, handler := range tools {
		server.AddTool(&mcpsdk.Tool{
			Name:        toolName,
			Description: "test tool: " + toolName,
			InputSchema: emptySchema,
		}, handler)
	}

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	// Start server in background
	go func() {
		_ = server.Run(context.Background(), serverTransport)
	}()

	return &testMCPServer{
		server:          server,
		clientTransport: clientTransport,
		serverTransport: serverTransport,
	}
}

// connectClientDirect creates an Client with a pre-wired in-memory transport.
// Bypasses the registry/createTransport path for unit testing the client itself.
func connectClientDirect(t *testing.T, serverID string, transport *mcpsdk.InMemoryTransport) *Client {
	t.Helper()
	ctx := context.Background()

	client := newClient(config.NewMCPServerRegistry(nil))

	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name: "tarsy-test", Version: "test",
	}, nil)

	session, err := sdkClient.Connect(ctx, transport, nil)
	require.NoError(t, err)

	client.mu.Lock()
	client.sessions[serverID] = session
	client.clients[serverID] = sdkClient
	client.mu.Unlock()

	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestClient_ListTools(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
		"get_logs": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)
	ctx := context.Background()

	tools, err := client.ListTools(ctx, "kubernetes")
	require.NoError(t, err)
	assert.Len(t, tools, 2)

	// Verify tool names
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	assert.Contains(t, names, "get_pods")
	assert.Contains(t, names, "get_logs")
}

func TestClient_ListTools_Cached(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)
	ctx := context.Background()

	// First call populates cache
	tools1, err := client.ListTools(ctx, "kubernetes")
	require.NoError(t, err)

	// Second call should return cached results
	tools2, err := client.ListTools(ctx, "kubernetes")
	require.NoError(t, err)

	assert.Equal(t, tools1, tools2)
}

func TestClient_CallTool(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pod-1\npod-2"}},
			}, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)
	ctx := context.Background()

	result, err := client.CallTool(ctx, "kubernetes", "get_pods", map[string]any{})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 1)

	tc, ok := result.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, "pod-1\npod-2", tc.Text)
}

func TestClient_CallTool_ErrorResult(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"bad_tool": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			result := &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "tool error: invalid namespace"}},
				IsError: true,
			}
			return result, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)
	ctx := context.Background()

	result, err := client.CallTool(ctx, "kubernetes", "bad_tool", map[string]any{})
	require.NoError(t, err) // No Go error — error is in result
	assert.True(t, result.IsError)
}

func TestClient_ListTools_NoSession(t *testing.T) {
	client := newClient(config.NewMCPServerRegistry(nil))

	_, err := client.ListTools(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no session")
}

func TestClient_CallTool_NoSession(t *testing.T) {
	client := newClient(config.NewMCPServerRegistry(nil))

	_, err := client.CallTool(context.Background(), "nonexistent", "tool", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no session")
}

func TestClient_HasSession(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"ping": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}}}, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)

	assert.True(t, client.HasSession("kubernetes"))
	assert.False(t, client.HasSession("nonexistent"))
}

func TestClient_FailedServers(t *testing.T) {
	client := newClient(config.NewMCPServerRegistry(nil))

	// Initialize with a non-existent server
	err := client.Initialize(context.Background(), []string{"nonexistent-server"})
	require.NoError(t, err) // Initialize doesn't return error; it records failures

	failed := client.FailedServers()
	assert.Contains(t, failed, "nonexistent-server")
}

func TestClient_Close(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"ping": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}}}, nil
		},
	})

	client := connectClientDirect(t, "kubernetes", ts.clientTransport)

	assert.True(t, client.HasSession("kubernetes"))

	err := client.Close()
	require.NoError(t, err)
	assert.False(t, client.HasSession("kubernetes"))
}
