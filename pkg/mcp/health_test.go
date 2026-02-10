package mcp

import (
	"context"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

func TestHealthMonitor_HealthyServer(t *testing.T) {
	// Setup in-memory server
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	// Create health monitor with pre-wired client
	registry := config.NewMCPServerRegistry(nil)
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)

	monitor := NewHealthMonitor(factory, registry, warningsSvc)
	monitor.checkInterval = 50 * time.Millisecond // Fast for tests
	monitor.pingTimeout = 5 * time.Second

	// Wire client directly for test
	client := newClient(registry)
	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := sdkClient.Connect(context.Background(), ts.clientTransport, nil)
	require.NoError(t, err)
	client.sessions["test-server"] = session
	client.clients["test-server"] = sdkClient
	t.Cleanup(func() { _ = client.Close() })
	monitor.client = client

	// Manually run a check
	monitor.checkServer(context.Background(), "test-server")

	// Verify healthy
	statuses := monitor.GetStatuses()
	require.Contains(t, statuses, "test-server")
	assert.True(t, statuses["test-server"].Healthy)
	assert.Equal(t, 1, statuses["test-server"].ToolCount)

	// No warnings should be set
	assert.Empty(t, warningsSvc.GetWarnings())

	// IsHealthy should return true
	assert.True(t, monitor.IsHealthy())

	// Cached tools should be populated
	cached := monitor.GetCachedTools()
	assert.Contains(t, cached, "test-server")
	assert.Len(t, cached["test-server"], 1)
}

func TestHealthMonitor_UnhealthyServer(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)

	monitor := NewHealthMonitor(factory, registry, warningsSvc)
	monitor.pingTimeout = 1 * time.Second

	// Create client with no sessions (simulating connection failure)
	client := newClient(registry)
	monitor.client = client

	// Check a non-existent server session
	monitor.checkServer(context.Background(), "broken-server")

	// Verify unhealthy
	statuses := monitor.GetStatuses()
	require.Contains(t, statuses, "broken-server")
	assert.False(t, statuses["broken-server"].Healthy)
	assert.NotEmpty(t, statuses["broken-server"].Error)

	// Warning should be set
	warnings := warningsSvc.GetWarnings()
	assert.Len(t, warnings, 1)
	assert.Equal(t, services.WarningCategoryMCPHealth, warnings[0].Category)
	assert.Equal(t, "broken-server", warnings[0].ServerID)

	assert.False(t, monitor.IsHealthy())
}

func TestHealthMonitor_WarningClearedOnRecovery(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"get_pods": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil
		},
	})

	registry := config.NewMCPServerRegistry(nil)
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)

	// Pre-add a warning
	warningsSvc.AddWarning(services.WarningCategoryMCPHealth, "unhealthy", "", "test-server")
	assert.Len(t, warningsSvc.GetWarnings(), 1)

	// Create healthy client
	monitor := NewHealthMonitor(factory, registry, warningsSvc)
	client := newClient(registry)
	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := sdkClient.Connect(context.Background(), ts.clientTransport, nil)
	require.NoError(t, err)
	client.sessions["test-server"] = session
	client.clients["test-server"] = sdkClient
	t.Cleanup(func() { _ = client.Close() })
	monitor.client = client

	// Check should pass and clear the warning
	monitor.checkServer(context.Background(), "test-server")

	assert.Empty(t, warningsSvc.GetWarnings())
	assert.True(t, monitor.IsHealthy())
}

func TestHealthMonitor_StartStop(t *testing.T) {
	ts := startTestServer(t, "test-server", map[string]mcpsdk.ToolHandler{
		"ping": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}}}, nil
		},
	})

	serverCfg := &config.MCPServerConfig{
		Transport: config.TransportConfig{
			Type:    config.TransportTypeStdio,
			Command: "echo", // Won't actually connect, but we wire manually
		},
	}
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"test-server": serverCfg,
	})
	warningsSvc := services.NewSystemWarningsService()
	factory := NewClientFactory(registry, nil)

	monitor := NewHealthMonitor(factory, registry, warningsSvc)
	monitor.checkInterval = 50 * time.Millisecond

	// Pre-wire a client so Start doesn't fail
	client := newClient(registry)
	sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := sdkClient.Connect(context.Background(), ts.clientTransport, nil)
	require.NoError(t, err)
	client.sessions["test-server"] = session
	client.clients["test-server"] = sdkClient
	t.Cleanup(func() { _ = client.Close() })

	monitor.clientMu.Lock()
	monitor.client = client
	monitor.clientMu.Unlock()

	ctx := context.Background()
	monitor.Start(ctx)

	// Poll until at least one check has run (avoids timing-dependent flakes on slow CI)
	require.Eventually(t, func() bool {
		statuses := monitor.GetStatuses()
		_, ok := statuses["test-server"]
		return ok
	}, 2*time.Second, 25*time.Millisecond, "health check should have run at least once")

	monitor.Stop()
}
