package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
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

	transport, err := createTransport(cfg, nil)
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

	_, err := createTransport(cfg, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires command")
}

func TestCreateTransport_HTTP(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "https://mcp.example.com/v1",
	}

	transport, err := createTransport(cfg, nil)
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

	transport, err := createTransport(cfg, nil)
	require.NoError(t, err)

	httpTransport, ok := transport.(*mcpsdk.StreamableClientTransport)
	require.True(t, ok)
	assert.NotNil(t, httpTransport.HTTPClient)
}

func TestCreateTransport_HTTP_MissingURL(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeHTTP,
	}

	_, err := createTransport(cfg, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires url")
}

func TestCreateTransport_SSE(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeSSE,
		URL:  "https://mcp.example.com/sse",
	}

	transport, err := createTransport(cfg, nil)
	require.NoError(t, err)

	sseTransport, ok := transport.(*mcpsdk.SSEClientTransport)
	require.True(t, ok)
	assert.Equal(t, "https://mcp.example.com/sse", sseTransport.Endpoint)
}

func TestCreateTransport_SSE_MissingURL(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeSSE,
	}

	_, err := createTransport(cfg, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires url")
}

func TestCreateTransport_UnknownType(t *testing.T) {
	cfg := config.TransportConfig{
		Type: "grpc",
	}

	_, err := createTransport(cfg, nil)
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

	transport, err := createTransport(cfg, nil)
	require.NoError(t, err)

	sseTransport, ok := transport.(*mcpsdk.SSEClientTransport)
	require.True(t, ok)
	assert.NotNil(t, sseTransport.HTTPClient, "expected custom HTTP client for VerifySSL=false")
}

func TestCreateTransport_HTTP_WithCustomHeaders(t *testing.T) {
	cfg := config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "https://mcp.example.com/v1",
		CustomHeaders: map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
		},
	}

	transport, err := createTransport(cfg, map[string]string{"SESSION_ID": "inv-abc123"})
	require.NoError(t, err)

	httpTransport, ok := transport.(*mcpsdk.StreamableClientTransport)
	require.True(t, ok)
	require.NotNil(t, httpTransport.HTTPClient)

	var gotSessionID, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSessionID = r.Header.Get("X-Session-ID")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	cfg.URL = server.URL
	cfg.BearerToken = "secret-token"
	client, err := buildHTTPClient(cfg, map[string]string{"SESSION_ID": "inv-abc123"})
	require.NoError(t, err)

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, "inv-abc123", gotSessionID)
	assert.Equal(t, "Bearer secret-token", gotAuth)
}

func TestResolveHeaderTemplates(t *testing.T) {
	t.Run("resolves SESSION_ID", func(t *testing.T) {
		got, err := resolveHeaderTemplates(map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
			"X-Static":     "fixed",
		}, map[string]string{"SESSION_ID": "sess-1"})
		require.NoError(t, err)
		assert.Equal(t, "sess-1", got["X-Session-ID"])
		assert.Equal(t, "fixed", got["X-Static"])
	})

	t.Run("omits empty after resolution", func(t *testing.T) {
		got, err := resolveHeaderTemplates(map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
		}, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
