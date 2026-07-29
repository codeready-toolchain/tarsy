package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

	transport, err := createTransport(cfg, map[string]string{"SESSION_ID": "exec-abc123"})
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
	client, err := buildHTTPClient(cfg, map[string]string{"SESSION_ID": "exec-abc123"})
	require.NoError(t, err)

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, "exec-abc123", gotSessionID)
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

	t.Run("empty headers map", func(t *testing.T) {
		got, err := resolveHeaderTemplates(nil, map[string]string{"SESSION_ID": "x"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("omits empty static values", func(t *testing.T) {
		got, err := resolveHeaderTemplates(map[string]string{
			"X-Empty": "",
			"X-Ok":    "v",
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"X-Ok": "v"}, got)
	})

	t.Run("malformed template", func(t *testing.T) {
		_, err := resolveHeaderTemplates(map[string]string{
			"X-Bad": "{{.SESSION_ID}",
		}, map[string]string{"SESSION_ID": "x"})
		require.Error(t, err)
	})

	t.Run("execute error", func(t *testing.T) {
		_, err := resolveHeaderTemplates(map[string]string{
			"X-Bad": "{{call .SESSION_ID}}",
		}, map[string]string{"SESSION_ID": "not-a-func"})
		require.Error(t, err)
	})
}

func TestBuildHTTPClient_ResolveHeaderError(t *testing.T) {
	badHeaders := map[string]string{"X-Bad": "{{.SESSION_ID}}{{"}

	_, err := buildHTTPClient(config.TransportConfig{
		Type:          config.TransportTypeHTTP,
		URL:           "https://mcp.example.com/mcp",
		CustomHeaders: badHeaders,
	}, map[string]string{"SESSION_ID": "x"})
	require.Error(t, err)

	_, err = createHTTPTransport(config.TransportConfig{
		Type:          config.TransportTypeHTTP,
		URL:           "https://mcp.example.com/mcp",
		CustomHeaders: badHeaders,
	}, map[string]string{"SESSION_ID": "x"})
	require.Error(t, err)

	_, err = createSSETransport(config.TransportConfig{
		Type:          config.TransportTypeSSE,
		URL:           "https://mcp.example.com/sse",
		CustomHeaders: badHeaders,
	}, map[string]string{"SESSION_ID": "x"})
	require.Error(t, err)
}

func TestBuildHTTPClient_SameOriginRedirectKeepsHeaders(t *testing.T) {
	var hops atomic.Int32
	var secondAuth, secondSession string

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, server.URL+"/end", http.StatusFound)
	})
	mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		secondAuth = r.Header.Get("Authorization")
		secondSession = r.Header.Get("X-Session-ID")
		w.WriteHeader(http.StatusOK)
	})

	cfg := config.TransportConfig{
		Type:        config.TransportTypeHTTP,
		URL:         server.URL,
		BearerToken: "secret-token",
		CustomHeaders: map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
		},
	}
	client, err := buildHTTPClient(cfg, map[string]string{"SESSION_ID": "exec-same"})
	require.NoError(t, err)

	resp, err := client.Get(server.URL + "/start")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, int32(2), hops.Load())
	assert.Equal(t, "Bearer secret-token", secondAuth)
	assert.Equal(t, "exec-same", secondSession)
}

func TestBuildHTTPClient_SecretHeadersNotSentOnCrossOriginRedirect(t *testing.T) {
	var redirectAuth, redirectSession string
	var originAuth, originSession string

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectAuth = r.Header.Get("Authorization")
		redirectSession = r.Header.Get("X-Session-ID")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(redirectTarget.Close)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuth = r.Header.Get("Authorization")
		originSession = r.Header.Get("X-Session-ID")
		http.Redirect(w, r, redirectTarget.URL+"/elsewhere", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	cfg := config.TransportConfig{
		Type:        config.TransportTypeHTTP,
		URL:         origin.URL,
		BearerToken: "secret-token",
		CustomHeaders: map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
		},
	}
	client, err := buildHTTPClient(cfg, map[string]string{"SESSION_ID": "exec-abc123"})
	require.NoError(t, err)

	resp, err := client.Get(origin.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, "Bearer secret-token", originAuth)
	assert.Equal(t, "exec-abc123", originSession)
	assert.Empty(t, redirectAuth, "Authorization must not follow cross-origin redirect")
	assert.Empty(t, redirectSession, "X-Session-ID must not follow cross-origin redirect")
}
