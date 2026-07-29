package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

func TestResolveSessionCleanupURL(t *testing.T) {
	got, err := resolveSessionCleanupURL("https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/sessions/sess-1", got)

	got, err = resolveSessionCleanupURL("https://cli-mcp-server:8443/api/sessions/{{.SESSION_ID}}", "sess-2")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/api/sessions/sess-2", got)

	got, err = resolveSessionCleanupURL("https://cli-mcp-server:8443/sessions/static", "ignored")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/sessions/static", got)

	_, err = resolveSessionCleanupURL("", "sess-1")
	require.Error(t, err)

	_, err = resolveSessionCleanupURL("https://x/{{.SESSION_ID}}{{", "sess-1")
	require.Error(t, err)
}

func TestNeedsSessionCleanup(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.TransportConfig
		want bool
	}{
		{
			name: "http with session_cleanup_url",
			cfg: config.TransportConfig{
				Type:              config.TransportTypeHTTP,
				URL:               "https://cli-mcp-server:8443/mcp",
				SessionCleanupURL: "https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}",
			},
			want: true,
		},
		{
			name: "sse with session_cleanup_url",
			cfg: config.TransportConfig{
				Type:              config.TransportTypeSSE,
				URL:               "https://cli-mcp-server:8443/sse",
				SessionCleanupURL: "https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}",
			},
			want: true,
		},
		{
			name: "http without session_cleanup_url",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  "https://kubernetes-mcp-server:8443/mcp",
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
			},
			want: false,
		},
		{
			name: "stdio with session_cleanup_url still opts in",
			cfg: config.TransportConfig{
				Type:              config.TransportTypeStdio,
				Command:           "npx",
				SessionCleanupURL: "https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}",
			},
			want: true,
		},
		{
			name: "empty session_cleanup_url",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  "https://cli-mcp-server:8443/mcp",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, needsSessionCleanup(tt.cfg))
		})
	}
}

func TestCleanupSessions(t *testing.T) {
	var deleted atomic.Bool
	var gotAuth, gotSessionHeader string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/inv-42" {
			deleted.Store(true)
			gotAuth = r.Header.Get("Authorization")
			gotSessionHeader = r.Header.Get("X-Session-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	verifySSL := false
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"cli-mcp-server": {
			Transport: config.TransportConfig{
				Type:        config.TransportTypeHTTP,
				URL:         server.URL + "/mcp",
				BearerToken: "tok",
				VerifySSL:   &verifySSL,
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
				SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
			},
		},
		"other": {
			Transport: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  server.URL + "/mcp",
				// custom_headers alone must not trigger cleanup
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
			},
		},
		"nil-entry": nil,
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	CleanupSessions(context.Background(), registry, "inv-42", logger)

	assert.True(t, deleted.Load(), "expected DELETE /sessions/inv-42")
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "inv-42", gotSessionHeader)
}

func TestCleanupSessions_SkipPaths(_ *testing.T) {
	CleanupSessions(context.Background(), nil, "sess", nil)
	CleanupSessions(context.Background(), config.NewMCPServerRegistry(nil), "", nil)

	// nil logger uses slog.Default(); empty registry is a no-op.
	CleanupSessions(context.Background(), config.NewMCPServerRegistry(nil), "sess", nil)
}

func TestCleanupSessions_ErrorAndStatusPaths(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	verifySSL := false

	t.Run("unexpected status is logged and ignored", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(server.Close)

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:              config.TransportTypeHTTP,
					URL:               server.URL + "/mcp",
					VerifySSL:         &verifySSL,
					SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
				},
			},
		})
		CleanupSessions(context.Background(), registry, "inv-err", logger)
	})

	t.Run("404 treated as success", func(t *testing.T) {
		var hit atomic.Bool
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hit.Store(true)
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(server.Close)

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:              config.TransportTypeHTTP,
					URL:               server.URL + "/mcp",
					VerifySSL:         &verifySSL,
					SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
				},
			},
		})
		CleanupSessions(context.Background(), registry, "gone", logger)
		assert.True(t, hit.Load())
	})

	t.Run("200 ok treated as success", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		t.Cleanup(server.Close)

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:              config.TransportTypeHTTP,
					URL:               server.URL + "/mcp",
					VerifySSL:         &verifySSL,
					SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
				},
			},
		})
		CleanupSessions(context.Background(), registry, "ok-sess", logger)
	})

	t.Run("transport do error is logged and ignored", func(_ *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		cleanupURL := server.URL + "/sessions/{{.SESSION_ID}}"
		mcpURL := server.URL + "/mcp"
		server.Close() // force client.Do failure

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:              config.TransportTypeHTTP,
					URL:               mcpURL,
					VerifySSL:         &verifySSL,
					SessionCleanupURL: cleanupURL,
				},
			},
		})
		CleanupSessions(context.Background(), registry, "down", logger)
	})

	t.Run("invalid cleanup url template is logged and ignored", func(_ *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:              config.TransportTypeHTTP,
					URL:               "https://cli-mcp-server:8443/mcp",
					SessionCleanupURL: "https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}{{",
				},
			},
		})
		CleanupSessions(context.Background(), registry, "bad-url", logger)
	})

	t.Run("buildHTTPClient error is logged and ignored", func(_ *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type: config.TransportTypeHTTP,
					URL:  "https://cli-mcp-server:8443/mcp",
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}{{",
					},
					SessionCleanupURL: "https://cli-mcp-server:8443/sessions/{{.SESSION_ID}}",
				},
			},
		})
		CleanupSessions(context.Background(), registry, "bad-header", logger)
	})
}

func TestDeleteSession_Direct(t *testing.T) {
	verifySSL := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/sessions/direct-1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	err := deleteSession(context.Background(), config.TransportConfig{
		Type:              config.TransportTypeHTTP,
		URL:               server.URL + "/mcp",
		VerifySSL:         &verifySSL,
		SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
	}, "direct-1")
	require.NoError(t, err)

	err = deleteSession(context.Background(), config.TransportConfig{
		Type:              config.TransportTypeHTTP,
		URL:               "https://example.com/mcp",
		SessionCleanupURL: "{{.SESSION_ID}}{{",
	}, "x")
	require.Error(t, err)
}

func TestClient_Close_TriggersSessionCleanup(t *testing.T) {
	var deleted atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/exec-close-1" {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	verifySSL := false
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"cli-mcp-server": {
			Transport: config.TransportConfig{
				Type:              config.TransportTypeHTTP,
				URL:               server.URL + "/mcp",
				VerifySSL:         &verifySSL,
				SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
			},
		},
	})

	client := newClient(registry, "exec-close-1")
	require.NoError(t, client.Close())
	assert.True(t, deleted.Load(), "Client.Close must DELETE sandbox for agent execution ID")

	exec := NewToolExecutor(newClient(registry, "exec-close-1"), registry, nil, nil, nil)
	deleted.Store(false)
	require.NoError(t, exec.Close())
	assert.True(t, deleted.Load(), "ToolExecutor.Close must DELETE sandbox for agent execution ID")
}

func TestClient_Close_SkipsCleanupWithoutSessionID(t *testing.T) {
	var deleted atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Store(true)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	verifySSL := false
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"cli-mcp-server": {
			Transport: config.TransportConfig{
				Type:              config.TransportTypeHTTP,
				URL:               server.URL + "/mcp",
				VerifySSL:         &verifySSL,
				SessionCleanupURL: server.URL + "/sessions/{{.SESSION_ID}}",
			},
		},
	})

	client := newClient(registry, "") // health/startup clients
	require.NoError(t, client.Close())
	assert.False(t, deleted.Load(), "empty sessionID must not trigger sandbox DELETE")
}
