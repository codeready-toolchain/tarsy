package e2e

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
)

// emptySchema is a minimal valid JSON Schema for test tools.
var emptySchema = json.RawMessage(`{"type":"object"}`)

// serverSpec holds the blueprint for an in-memory MCP server so that fresh
// transports can be created for each CreateClient call.
type serverSpec struct {
	serverID string
	tools    map[string]mcpsdk.ToolHandler
}

// SetupInMemoryMCP creates in-memory MCP servers with scripted tool handlers
// and returns a real *mcp.ClientFactory backed by those servers.
//
// Each call to ClientFactory.CreateClient creates fresh in-memory transports
// and sessions, so consecutive MCP clients (e.g. investigation → chat) get
// independent, non-shared connections.
//
// servers maps serverID → (toolName → handler).
func SetupInMemoryMCP(t *testing.T, servers map[string]map[string]mcpsdk.ToolHandler) *mcp.ClientFactory {
	t.Helper()

	// Capture server blueprints.
	specs := make([]serverSpec, 0, len(servers))
	for serverID, tools := range servers {
		specs = append(specs, serverSpec{serverID: serverID, tools: tools})
	}

	// Build a registry with stub configs so tool filtering resolves correctly.
	mcpConfigs := make(map[string]*config.MCPServerConfig, len(servers))
	for serverID := range servers {
		mcpConfigs[serverID] = &config.MCPServerConfig{
			Transport: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "mock", // Overridden by in-memory transport.
			},
		}
	}
	registry := config.NewMCPServerRegistry(mcpConfigs)

	// Create a ClientFactory that spins up fresh in-memory transports per call.
	return mcp.NewTestClientFactory(registry, func(c *mcp.Client) {
		for _, spec := range specs {
			server := mcpsdk.NewServer(&mcpsdk.Implementation{
				Name: spec.serverID, Version: "test",
			}, nil)
			for toolName, handler := range spec.tools {
				server.AddTool(&mcpsdk.Tool{
					Name:        toolName,
					Description: "test tool: " + toolName,
					InputSchema: emptySchema,
				}, handler)
			}

			clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go func() { _ = server.Run(ctx, serverTransport) }()

			sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{
				Name: "tarsy-e2e", Version: "test",
			}, nil)
			session, err := sdkClient.Connect(context.Background(), clientTransport, nil)
			require.NoError(t, err)

			c.InjectSession(spec.serverID, sdkClient, session)
		}
	})
}

// StaticToolHandler returns an mcpsdk.ToolHandler that always returns the given text.
func StaticToolHandler(text string) mcpsdk.ToolHandler {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		}, nil
	}
}

// ErrorToolHandler returns an mcpsdk.ToolHandler that returns the given error.
func ErrorToolHandler(err error) mcpsdk.ToolHandler {
	return func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return nil, err
	}
}
