package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

func TestClientFactory_CreateToolExecutor_PropagatesSessionID(t *testing.T) {
	registry := config.NewMCPServerRegistry(nil)
	var gotSessionID string
	factory := &ClientFactory{
		registry: registry,
		createClientFn: func(_ context.Context, _ []string, mcpSessionID string) (*Client, error) {
			gotSessionID = mcpSessionID
			return newClient(registry, mcpSessionID), nil
		},
	}

	exec, client, err := factory.CreateToolExecutor(context.Background(), nil, nil, "exec-factory")
	require.NoError(t, err)
	require.NotNil(t, exec)
	require.NotNil(t, client)
	assert.Equal(t, "exec-factory", gotSessionID)
	assert.Equal(t, map[string]string{"SESSION_ID": "exec-factory"}, client.sessionVars())
}

func TestClientFactory_CreateToolExecutor_Error(t *testing.T) {
	factory := &ClientFactory{
		registry: config.NewMCPServerRegistry(nil),
		createClientFn: func(_ context.Context, _ []string, _ string) (*Client, error) {
			return nil, errors.New("boom")
		},
	}

	exec, client, err := factory.CreateToolExecutor(context.Background(), nil, nil, "exec-x")
	require.Error(t, err)
	assert.Nil(t, exec)
	assert.Nil(t, client)
}
