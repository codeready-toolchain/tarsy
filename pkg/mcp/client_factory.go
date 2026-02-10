package mcp

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
)

// ClientFactory creates Client instances for sessions.
type ClientFactory struct {
	registry       *config.MCPServerRegistry
	maskingService *masking.MaskingService
}

// NewClientFactory creates a new factory.
// maskingService may be nil (masking disabled).
func NewClientFactory(registry *config.MCPServerRegistry, maskingService *masking.MaskingService) *ClientFactory {
	return &ClientFactory{registry: registry, maskingService: maskingService}
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
	return NewToolExecutor(client, f.registry, serverIDs, toolFilter, f.maskingService), client, nil
}
