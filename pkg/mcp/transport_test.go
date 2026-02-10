package mcp

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

func TestCreateTransport_Stdio(t *testing.T) {
	cfg := config.TransportConfig{
		Type:    config.TransportTypeStdio,
		Command: "npx",
		Args:    []string{"-y", "kubernetes-mcp-server@0.0.54"},
		Env:     map[string]string{"KUBECONFIG": "/home/test/.kube/config"},
	}

	transport, err := createTransport(cfg)
	require.NoError(t, err)

	cmdTransport, ok := transport.(*mcpsdk.CommandTransport)
	require.True(t, ok)
	// exec.Command resolves the full path, so check Args[0] for the basename
	assert.Contains(t, cmdTransport.Command.Path, "npx")
	assert.Contains(t, cmdTransport.Command.Args, "-y")
	assert.Contains(t, cmdTransport.Command.Args, "kubernetes-mcp-server@0.0.54")

	// Check env override is present
	found := false
	for _, e := range cmdTransport.Command.Env {
		if e == "KUBECONFIG=/home/test/.kube/config" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected KUBECONFIG env override in command environment")
}

func TestCreateTransport_Stdio_MissingCommand(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeStdio,
	}

	_, err := createTransport(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires command")
}

func TestCreateTransport_HTTP(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "https://mcp.example.com/v1",
	}

	transport, err := createTransport(cfg)
	require.NoError(t, err)

	httpTransport, ok := transport.(*mcpsdk.StreamableClientTransport)
	require.True(t, ok)
	assert.Equal(t, "https://mcp.example.com/v1", httpTransport.Endpoint)
	assert.Nil(t, httpTransport.HTTPClient) // No custom client needed
}

func TestCreateTransport_HTTP_WithAuth(t *testing.T) {
	cfg := config.TransportConfig{
		Type:        config.TransportTypeHTTP,
		URL:         "https://mcp.example.com/v1",
		BearerToken: "my-token",
		Timeout:     30,
	}

	transport, err := createTransport(cfg)
	require.NoError(t, err)

	httpTransport, ok := transport.(*mcpsdk.StreamableClientTransport)
	require.True(t, ok)
	assert.NotNil(t, httpTransport.HTTPClient)
}

func TestCreateTransport_HTTP_MissingURL(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeHTTP,
	}

	_, err := createTransport(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires url")
}

func TestCreateTransport_SSE(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeSSE,
		URL:  "https://mcp.example.com/sse",
	}

	transport, err := createTransport(cfg)
	require.NoError(t, err)

	sseTransport, ok := transport.(*mcpsdk.SSEClientTransport)
	require.True(t, ok)
	assert.Equal(t, "https://mcp.example.com/sse", sseTransport.Endpoint)
}

func TestCreateTransport_SSE_MissingURL(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeSSE,
	}

	_, err := createTransport(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires url")
}

func TestCreateTransport_UnknownType(t *testing.T) {
	cfg := config.TransportConfig{
		Type: "grpc",
	}

	_, err := createTransport(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported transport type")
}

func TestCreateTransport_SSE_WithVerifySSLFalse(t *testing.T) {
	verifySSL := false
	cfg := config.TransportConfig{
		Type:      config.TransportTypeSSE,
		URL:       "https://mcp.example.com/sse",
		VerifySSL: &verifySSL,
	}

	transport, err := createTransport(cfg)
	require.NoError(t, err)

	sseTransport, ok := transport.(*mcpsdk.SSEClientTransport)
	require.True(t, ok)
	assert.NotNil(t, sseTransport.HTTPClient, "expected custom HTTP client for VerifySSL=false")
}
