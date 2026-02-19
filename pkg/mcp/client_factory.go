package mcp

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
)

// ClientFactory creates Client instances for sessions.
type ClientFactory struct {
	registry       *config.MCPServerRegistry
	maskingService *masking.Service

	// createClientFn overrides the default client creation logic.
	// When non-nil, it is called instead of newClient + Initialize.
	// Used by test infrastructure (see export_test.go).
	createClientFn func(ctx context.Context, serverIDs []string) (*Client, error)
}

// NewClientFactory creates a new factory.
// maskingService may be nil (masking disabled).
func NewClientFactory(registry *config.MCPServerRegistry, maskingService *masking.Service) *ClientFactory {
	return &ClientFactory{registry: registry, maskingService: maskingService}
}

// CreateClient creates a new Client connected to the specified servers.
// The caller is responsible for calling Close() when done.
func (f *ClientFactory) CreateClient(ctx context.Context, serverIDs []string) (*Client, error) {
	if f.createClientFn != nil {
		return f.createClientFn(ctx, serverIDs)
	}
	client := newClient(f.registry)
	client.Initialize(ctx, serverIDs)
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
