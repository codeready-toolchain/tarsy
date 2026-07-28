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

func TestSessionCleanupURL(t *testing.T) {
	got, err := sessionCleanupURL("https://cli-mcp-server:8443/mcp", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/sessions/sess-1", got)

	got, err = sessionCleanupURL("https://cli-mcp-server:8443/mcp/", "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/sessions/sess-1", got)

	got, err = sessionCleanupURL("https://cli-mcp-server:8443/api/mcp?x=1#frag", "sess-2")
	require.NoError(t, err)
	assert.Equal(t, "https://cli-mcp-server:8443/api/sessions/sess-2", got)

	_, err = sessionCleanupURL("://bad", "sess-1")
	require.Error(t, err)
}

func TestNeedsSessionCleanup(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.TransportConfig
		want bool
	}{
		{
			name: "http with SESSION_ID header",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  "https://cli-mcp-server:8443/mcp",
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
			},
			want: true,
		},
		{
			name: "sse with SESSION_ID header",
			cfg: config.TransportConfig{
				Type: config.TransportTypeSSE,
				URL:  "https://cli-mcp-server:8443/sse",
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
			},
			want: true,
		},
		{
			name: "http without custom headers",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  "https://kubernetes-mcp-server:8443/mcp",
			},
			want: false,
		},
		{
			name: "http empty url",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
			},
			want: false,
		},
		{
			name: "http headers without SESSION_ID template",
			cfg: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  "https://cli-mcp-server:8443/mcp",
				CustomHeaders: map[string]string{
					"X-Tenant": "static",
				},
			},
			want: false,
		},
		{
			name: "stdio",
			cfg: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "npx",
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
			},
		},
		"other": {
			Transport: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  server.URL + "/mcp",
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
					Type:      config.TransportTypeHTTP,
					URL:       server.URL + "/mcp",
					VerifySSL: &verifySSL,
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}",
					},
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
					Type:      config.TransportTypeHTTP,
					URL:       server.URL + "/mcp",
					VerifySSL: &verifySSL,
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}",
					},
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
					Type:      config.TransportTypeHTTP,
					URL:       server.URL + "/mcp",
					VerifySSL: &verifySSL,
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}",
					},
				},
			},
		})
		CleanupSessions(context.Background(), registry, "ok-sess", logger)
	})

	t.Run("transport do error is logged and ignored", func(_ *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		url := server.URL + "/mcp"
		server.Close() // force client.Do failure

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type:      config.TransportTypeHTTP,
					URL:       url,
					VerifySSL: &verifySSL,
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}",
					},
				},
			},
		})
		CleanupSessions(context.Background(), registry, "down", logger)
	})

	t.Run("invalid mcp url is logged and ignored", func(_ *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"cli-mcp-server": {
				Transport: config.TransportConfig{
					Type: config.TransportTypeHTTP,
					URL:  "://not-a-url",
					CustomHeaders: map[string]string{
						"X-Session-ID": "{{.SESSION_ID}}",
					},
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
						// Still matches needsSessionCleanup (contains {{.SESSION_ID}})
						// but fails template parse (trailing unclosed action).
						"X-Session-ID": "{{.SESSION_ID}}{{",
					},
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
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	err := deleteSession(context.Background(), config.TransportConfig{
		Type:      config.TransportTypeHTTP,
		URL:       server.URL + "/mcp",
		VerifySSL: &verifySSL,
	}, "direct-1")
	require.NoError(t, err)

	err = deleteSession(context.Background(), config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "://bad",
	}, "x")
	require.Error(t, err)
}
