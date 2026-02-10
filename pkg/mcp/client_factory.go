package mcp

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// ClientFactory creates Client instances for sessions.
type ClientFactory struct {
	registry *config.MCPServerRegistry
}

// NewClientFactory creates a new factory.
func NewClientFactory(registry *config.MCPServerRegistry) *ClientFactory {
	return &ClientFactory{registry: registry}
}

// CreateClient creates a new Client connected to the specified servers.
// The caller is responsible for calling Close() when done.
func (f *ClientFactory) CreateClient(ctx context.Context, serverIDs []string) (*Client, error) {
	client := newClient(f.registry)
	if err := client.Initialize(ctx, serverIDs); err != nil {
		_ = client.Close() // Clean up partial initialization
		return nil, err
	}
	return client, nil
}

// CreateToolExecutor creates a fully-wired ToolExecutor for a session.
// This is the primary entry point used by the session executor.
func (f *ClientFactory) CreateToolExecutor(
	ctx context.Context,
	serverIDs []string,
	toolFilter map[string][]string,
) (*ToolExecutor, *Client, error) {
	client, err := f.CreateClient(ctx, serverIDs)
	if err != nil {
		return nil, nil, err
	}
	return NewToolExecutor(client, f.registry, serverIDs, toolFilter), client, nil
}
