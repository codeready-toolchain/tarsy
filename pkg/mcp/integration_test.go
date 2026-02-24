package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// TestIntegration_E2E_ToolExecution tests the full tool execution pipeline:
// ToolExecutor.Execute → ParseActionInput → SplitToolName → Client.CallTool → result.
func TestIntegration_E2E_ToolExecution(t *testing.T) {
	// Create an in-memory MCP server with a tool that echoes its arguments
	ts := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			// Parse the arguments to echo them back
			args := req.Params.Arguments
			var parsed map[string]any
			if err := json.Unmarshal(args, &parsed); err != nil {
				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "parse error: " + err.Error()}},
					IsError: true,
				}, nil
			}

			ns, _ := parsed["namespace"].(string)
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{
					Text: "pods in namespace " + ns + ": pod-1, pod-2",
				}},
			}, nil
		},
	})

	// Wire up executor
	executor := newTestExecutorFromTransport(t, "kubernetes", ts.clientTransport)

	// Execute with JSON arguments
	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-e2e-1",
		Name:      "kubernetes.get_pods",
		Arguments: `{"namespace": "default"}`,
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "pods in namespace default")
	assert.Contains(t, result.Content, "pod-1, pod-2")

	// Execute with key-value arguments (parsing cascade)
	result, err = executor.Execute(context.Background(), agent.ToolCall{
		ID:        "call-e2e-2",
		Name:      "kubernetes.get_pods",
		Arguments: "namespace: production",
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "pods in namespace production")
}

// TestIntegration_MultiServer_Routing tests tool discovery and routing across multiple servers.
func TestIntegration_MultiServer_Routing(t *testing.T) {
	// Create two in-memory MCP servers
	k8sServer := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "k8s: pods"}},
			}, nil
		},
	})

	ghServer := startTestServer(t, "github", map[string]mcpsdk.ToolHandler{
		"list_repos": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "gh: repos"}},
			}, nil
		},
	})

	// Build multi-server executor
	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)
	wireSession(t, client, "kubernetes", k8sServer.clientTransport)
	wireSession(t, client, "github", ghServer.clientTransport)

	executor := NewToolExecutor(client, registry, []string{"kubernetes", "github"}, nil, nil)
	t.Cleanup(func() { _ = executor.Close() })

	// List tools should show both servers' tools
	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 2)

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	assert.Contains(t, names, "kubernetes.get_pods")
	assert.Contains(t, names, "github.list_repos")

	// Route to kubernetes
	r1, err := executor.Execute(context.Background(), agent.ToolCall{
		ID: "r1", Name: "kubernetes.get_pods", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.Equal(t, "k8s: pods", r1.Content)

	// Route to github
	r2, err := executor.Execute(context.Background(), agent.ToolCall{
		ID: "r2", Name: "github.list_repos", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.Equal(t, "gh: repos", r2.Content)
}

// TestIntegration_GoogleNative_Normalization tests the __ → . normalization through the full pipeline.
// The LLM service may return tool call names in "server__tool" format (Gemini convention),
// which the executor normalizes back to "server.tool" for routing.
func TestIntegration_GoogleNative_Normalization(t *testing.T) {
	ts := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "native thinking works"}},
			}, nil
		},
	})

	executor := newTestExecutorFromTransport(t, "kubernetes", ts.clientTransport)

	// LLM service may return tool calls in "server__tool" format from Gemini;
	// executor normalizes them back to "server.tool" for routing.
	result, err := executor.Execute(context.Background(), agent.ToolCall{
		ID:        "nt-1",
		Name:      "kubernetes__get_pods",
		Arguments: `{"namespace": "default"}`,
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "native thinking works", result.Content)
}

// TestIntegration_ListToolsCanonicalFormat verifies tool names stay in canonical "server.tool" format.
// The LLM service handles backend-specific encoding (e.g. "server__tool" for Gemini).
func TestIntegration_ListToolsCanonicalFormat(t *testing.T) {
	ts := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	executor := newTestExecutorFromTransport(t, "kubernetes", ts.clientTransport)

	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "kubernetes.get_pods", tools[0].Name)
}

// TestIntegration_PerSessionIsolation tests that two concurrent executors from the same factory
// operate independently.
func TestIntegration_PerSessionIsolation(t *testing.T) {
	ts1 := startTestServer(t, "server1", map[string]mcpsdk.ToolHandler{
		"tool": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "from session 1"}},
			}, nil
		},
	})

	ts2 := startTestServer(t, "server2", map[string]mcpsdk.ToolHandler{
		"tool": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "from session 2"}},
			}, nil
		},
	})

	// Create two independent executors
	registry := config.NewMCPServerRegistry(nil)

	client1 := newClient(registry)
	wireSession(t, client1, "server1", ts1.clientTransport)
	exec1 := NewToolExecutor(client1, registry, []string{"server1"}, nil, nil)
	t.Cleanup(func() { _ = exec1.Close() })

	client2 := newClient(registry)
	wireSession(t, client2, "server2", ts2.clientTransport)
	exec2 := NewToolExecutor(client2, registry, []string{"server2"}, nil, nil)
	t.Cleanup(func() { _ = exec2.Close() })

	// Execute on each
	r1, err := exec1.Execute(context.Background(), agent.ToolCall{
		ID: "iso-1", Name: "server1.tool", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.Equal(t, "from session 1", r1.Content)

	r2, err := exec2.Execute(context.Background(), agent.ToolCall{
		ID: "iso-2", Name: "server2.tool", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.Equal(t, "from session 2", r2.Content)
}

// TestIntegration_HealthMonitor_Lifecycle tests healthy → failure → recovery lifecycle.
func TestIntegration_HealthMonitor_Lifecycle(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"ping": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}}}, nil
		},
	})

	registry := config.NewMCPServerRegistry(nil)
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)
	monitor := NewHealthMonitor(factory, registry, warningsSvc)

	// Wire healthy client
	client := newClient(registry)
	wireSession(t, client, "test-server", ts.clientTransport)
	t.Cleanup(func() { _ = client.Close() })
	monitor.client = client

	// Phase 1: Healthy
	monitor.checkServer(context.Background(), "test-server")
	assert.True(t, monitor.IsHealthy())
	assert.Empty(t, warningsSvc.GetWarnings())
	status := monitor.GetStatuses()["test-server"]
	require.NotNil(t, status)
	assert.True(t, status.Healthy)
	assert.Empty(t, status.Error)
	assert.Equal(t, 1, status.ToolCount)

	// Phase 2: Simulate failure (close the session)
	client.mu.Lock()
	if session, exists := client.sessions["test-server"]; exists {
		_ = session.Close()
		delete(client.sessions, "test-server")
		delete(client.clients, "test-server")
	}
	client.mu.Unlock()

	monitor.checkServer(context.Background(), "test-server")
	assert.False(t, monitor.IsHealthy())
	warnings := warningsSvc.GetWarnings()
	require.Len(t, warnings, 1)
	assert.Equal(t, services.WarningCategoryMCPHealth, warnings[0].Category)
	assert.Equal(t, "test-server", warnings[0].ServerID)
	assert.NotEmpty(t, warnings[0].Message)
	status = monitor.GetStatuses()["test-server"]
	require.NotNil(t, status)
	assert.False(t, status.Healthy)
	assert.NotEmpty(t, status.Error)

	// Phase 3: Simulate recovery (reconnect with new server)
	ts2 := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"ping": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}}}, nil
		},
	})
	wireSession(t, client, "test-server", ts2.clientTransport)

	monitor.checkServer(context.Background(), "test-server")
	assert.True(t, monitor.IsHealthy())
	assert.Empty(t, warningsSvc.GetWarnings())
	status = monitor.GetStatuses()["test-server"]
	require.NotNil(t, status)
	assert.True(t, status.Healthy)
	assert.Empty(t, status.Error)
}

// --- Test helpers ---

// newTestExecutorFromTransport creates a single-server ToolExecutor for testing.
func newTestExecutorFromTransport(t *testing.T, serverID string, transport *mcpsdk.InMemoryTransport) *ToolExecutor {
	t.Helper()

	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)
	wireSession(t, client, serverID, transport)

	executor := NewToolExecutor(client, registry, []string{serverID}, nil, nil)
	t.Cleanup(func() { _ = executor.Close() })
	return executor
}

// wireSession connects a client to an in-memory transport and registers the session.
func wireSession(t *testing.T, client *Client, serverID string, transport *mcpsdk.InMemoryTransport) {
	t.Helper()

	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name: "tarsy-test", Version: "test",
	}, nil)
	session, err := sdkClient.Connect(context.Background(), transport, nil)
	require.NoError(t, err)

	client.mu.Lock()
	client.sessions[serverID] = session
	client.clients[serverID] = sdkClient
	client.mu.Unlock()
}

// TestIntegration_ToolFilter tests that tool filtering works end-to-end.
func TestIntegration_ToolFilter(t *testing.T) {
	ts := startTestServer(t, "kubernetes", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pods"}}}, nil
		},
		"delete_pod": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "deleted"}}}, nil
		},
	})

	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)
	wireSession(t, client, "kubernetes", ts.clientTransport)

	// Only allow get_pods
	filter := map[string][]string{"kubernetes": {"get_pods"}}
	executor := NewToolExecutor(client, registry, []string{"kubernetes"}, filter, nil)
	t.Cleanup(func() { _ = executor.Close() })

	// ListTools should only return get_pods
	tools, err := executor.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 1)
	assert.Equal(t, "kubernetes.get_pods", tools[0].Name)

	// Execute allowed tool should work
	r1, err := executor.Execute(context.Background(), agent.ToolCall{
		ID: "f1", Name: "kubernetes.get_pods", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.False(t, r1.IsError)
	assert.Equal(t, "pods", r1.Content)

	// Execute filtered tool should fail
	r2, err := executor.Execute(context.Background(), agent.ToolCall{
		ID: "f2", Name: "kubernetes.delete_pod", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.True(t, r2.IsError)
	assert.Contains(t, r2.Content, "not available")
}

// TestIntegration_FailedServers tests failed server tracking through the pipeline.
func TestIntegration_FailedServers(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	client := newClient(registry)

	// Initialize with a non-existent server (failures recorded, not returned)
	client.Initialize(context.Background(), []string{"broken-server"})

	failed := client.FailedServers()
	assert.Contains(t, failed, "broken-server")
	assert.NotEmpty(t, failed["broken-server"])
}

// TestIntegration_HealthMonitor_ToolCaching tests that the health monitor populates tool cache.
func TestIntegration_HealthMonitor_ToolCaching(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"tool_a": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "a"}}}, nil
		},
		"tool_b": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "b"}}}, nil
		},
	})

	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": {Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "echo"}},
	})
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)
	monitor := NewHealthMonitor(factory, registry, warningsSvc)
	monitor.pingTimeout = 5 * time.Second

	// Wire client
	client := newClient(registry)
	wireSession(t, client, "test-server", ts.clientTransport)
	t.Cleanup(func() { _ = client.Close() })
	monitor.client = client

	// Run health check
	monitor.checkServer(context.Background(), "test-server")

	// Tool cache should be populated
	cached := monitor.GetCachedTools()
	require.Contains(t, cached, "test-server")
	assert.Len(t, cached["test-server"], 2)
}
