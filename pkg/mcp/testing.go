package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// InjectSession injects a pre-connected MCP SDK session into the Client.
// This is intended for test infrastructure that needs to wire in-memory MCP
// servers without going through the real Initialize() transport creation path.
func (c *Client) InjectSession(serverID string, sdkClient *mcpsdk.Client, session *mcpsdk.ClientSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[serverID] = session
	c.clients[serverID] = sdkClient
}

// NewTestClientFactory creates a ClientFactory that uses injectFn to wire
// sessions into each new Client instead of calling Initialize().
// Each call to CreateClient/CreateToolExecutor invokes injectFn on the
// freshly-created Client, allowing tests to inject in-memory MCP sessions.
func NewTestClientFactory(registry *config.MCPServerRegistry, injectFn func(c *Client)) *ClientFactory {
	return &ClientFactory{
		registry: registry,
		createClientFn: func(_ context.Context, _ []string) (*Client, error) {
			c := newClient(registry)
			injectFn(c)
			return c, nil
		},
	}
}
