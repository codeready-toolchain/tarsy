package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

	// Start server in background with cancellable context to avoid leaked goroutines
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	return &testMCPServer{
		server:          server,
		clientTransport: clientTransport,
		serverTransport: serverTransport,
	}
}

func TestClient_SessionVars(t *testing.T) {
	empty := newClient(config.NewMCPServerRegistry(nil), "")
	assert.Nil(t, empty.sessionVars())

	withSession := newClient(config.NewMCPServerRegistry(nil), "exec-99")
	assert.Equal(t, map[string]string{"SESSION_ID": "exec-99"}, withSession.sessionVars())

	require.NoError(t, withSession.Close())
	assert.Nil(t, withSession.sessionVars(), "Close clears mcpSessionID")
}

func TestClient_SessionVars_ConcurrentWithClose(t *testing.T) {
	client := newClient(config.NewMCPServerRegistry(nil), "exec-race")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			vars := client.sessionVars()
			if vars != nil {
				assert.Equal(t, "exec-race", vars["SESSION_ID"])
			}
		}
	}()
	go func() {
		defer wg.Done()
		require.NoError(t, client.Close())
	}()
	wg.Wait()

	assert.Nil(t, client.sessionVars(), "sessionVars must be nil after Close")
}

func TestClient_InitializeServer_AfterClose(t *testing.T) {
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"srv": {
			Transport: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "true",
			},
		},
	})
	client := newClient(registry, "exec-closed")
	require.NoError(t, client.Close())

	err := client.InitializeServer(context.Background(), "srv")
	require.EqualError(t, err, "client is closed")
	assert.False(t, client.HasSession("srv"))
}

// TestClient_Close_DropsInFlightInitialize ensures a session that finishes
// Connect after Close is discarded instead of being installed.
func TestClient_Close_DropsInFlightInitialize(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "gate-mcp", Version: "test"}, nil)
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return server
	}, &mcpsdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true})

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	verifySSL := false
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"srv": {
			Transport: config.TransportConfig{
				Type:      config.TransportTypeHTTP,
				URL:       httpServer.URL,
				VerifySSL: &verifySSL,
			},
		},
	})
	client := newClient(registry, "exec-inflight")

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.InitializeServer(context.Background(), "srv")
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("InitializeServer never reached the MCP server")
	}

	require.NoError(t, client.Close())
	close(release)

	initErr := <-errCh
	require.EqualError(t, initErr, "client is closed")
	assert.False(t, client.HasSession("srv"), "late Connect must not install a session after Close")
}

// connectClientDirect creates an Client with a pre-wired in-memory transport.
// Bypasses the registry/createTransport path for unit testing the client itself.
func connectClientDirect(t *testing.T, serverID string, transport *mcpsdk.InMemoryTransport) *Client {
	t.Helper()
	ctx := context.Background()

	client := newClient(config.NewMCPServerRegistry(nil), "")

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
	client := newClient(config.NewMCPServerRegistry(nil), "")

	_, err := client.ListTools(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no session")
}

func TestClient_CallTool_NoSession(t *testing.T) {
	client := newClient(config.NewMCPServerRegistry(nil), "")

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
	client := newClient(config.NewMCPServerRegistry(nil), "")

	// Initialize with a non-existent server (failures recorded, not returned)
	client.Initialize(context.Background(), []string{"nonexistent-server"})

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
