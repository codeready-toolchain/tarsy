package mcp

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
)

// ClientFactory creates Client instances for agent executions.
type ClientFactory struct {
	registry       *config.MCPServerRegistry
	maskingService *masking.Service

	// createClientFn overrides the default client creation logic.
	// When non-nil, it is called instead of newClient + Initialize.
	// Used by test infrastructure (see testing.go).
	createClientFn func(ctx context.Context, serverIDs []string, sessionID string) (*Client, error)
}

// NewClientFactory creates a new factory.
// maskingService may be nil (masking disabled).
func NewClientFactory(registry *config.MCPServerRegistry, maskingService *masking.Service) *ClientFactory {
	return &ClientFactory{registry: registry, maskingService: maskingService}
}

// CreateClient creates a new Client connected to the specified servers.
// sessionID is the agent execution ID used to resolve per-execution custom_headers
// (e.g. X-Session-ID). Pass an empty string for health checks and startup validation.
// The caller is responsible for calling Close() when done (also triggers sandbox cleanup).
func (f *ClientFactory) CreateClient(ctx context.Context, serverIDs []string, sessionID string) (*Client, error) {
	if f.createClientFn != nil {
		return f.createClientFn(ctx, serverIDs, sessionID)
	}
	client := newClient(f.registry, sessionID)
	client.Initialize(ctx, serverIDs)
	return client, nil
}

// CreateToolExecutor creates a fully-wired ToolExecutor for an agent execution.
// This is the primary entry point used by the session executor.
// sessionID is the agent execution ID forwarded for custom_headers resolution.
func (f *ClientFactory) CreateToolExecutor(
	ctx context.Context,
	serverIDs []string,
	toolFilter map[string][]string,
	sessionID string,
) (*ToolExecutor, *Client, error) {
	client, err := f.CreateClient(ctx, serverIDs, sessionID)
	if err != nil {
		return nil, nil, err
	}
	return NewToolExecutor(client, f.registry, serverIDs, toolFilter, f.maskingService), client, nil
}
