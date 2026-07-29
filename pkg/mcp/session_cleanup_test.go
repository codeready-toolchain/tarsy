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
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/exec-42" {
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
	CleanupSessions(context.Background(), registry, "exec-42", []string{"cli-mcp-server", "other", "nil-entry"}, logger)

	assert.True(t, deleted.Load(), "expected DELETE /sessions/exec-42")
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "exec-42", gotSessionHeader)
}

// TestCleanupSessions_AuthHeadersScopedToCleanupHost verifies DELETE auth and
// custom headers follow the cleanup URL host when it differs from the MCP URL.
func TestCleanupSessions_AuthHeadersScopedToCleanupHost(t *testing.T) {
	var mcpDeleted atomic.Bool
	mcpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mcpDeleted.Store(true)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(mcpServer.Close)

	var deleted atomic.Bool
	var gotAuth, gotSessionHeader string
	cleanupServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/exec-host" {
			deleted.Store(true)
			gotAuth = r.Header.Get("Authorization")
			gotSessionHeader = r.Header.Get("X-Session-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(cleanupServer.Close)

	require.NotEqual(t, mcpServer.URL, cleanupServer.URL, "test requires distinct hosts")

	verifySSL := false
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"cli-mcp-server": {
			Transport: config.TransportConfig{
				Type:        config.TransportTypeHTTP,
				URL:         mcpServer.URL + "/mcp",
				BearerToken: "cleanup-tok",
				VerifySSL:   &verifySSL,
				CustomHeaders: map[string]string{
					"X-Session-ID": "{{.SESSION_ID}}",
				},
				SessionCleanupURL: cleanupServer.URL + "/sessions/{{.SESSION_ID}}",
			},
		},
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	CleanupSessions(context.Background(), registry, "exec-host", []string{"cli-mcp-server"}, logger)

	assert.True(t, deleted.Load(), "DELETE must hit cleanup host")
	assert.False(t, mcpDeleted.Load(), "DELETE must not hit MCP host")
	assert.Equal(t, "Bearer cleanup-tok", gotAuth)
	assert.Equal(t, "exec-host", gotSessionHeader)
}

func TestCleanupSessions_OnlyRequestedServers(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Agent only used kubernetes-server — must not DELETE on cli-mcp.
	CleanupSessions(context.Background(), registry, "exec-k8s-only", []string{"kubernetes-server"}, logger)
	assert.False(t, deleted.Load(), "cleanup must not touch unrequested servers")
}

func TestCleanupSessions_SkipPaths(_ *testing.T) {
	CleanupSessions(context.Background(), nil, "sess", []string{"cli"}, nil)
	CleanupSessions(context.Background(), config.NewMCPServerRegistry(nil), "", []string{"cli"}, nil)
	CleanupSessions(context.Background(), config.NewMCPServerRegistry(nil), "sess", nil, nil)
	CleanupSessions(context.Background(), config.NewMCPServerRegistry(nil), "sess", []string{}, nil)
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
		CleanupSessions(context.Background(), registry, "exec-err", []string{"cli-mcp-server"}, logger)
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
		CleanupSessions(context.Background(), registry, "gone", []string{"cli-mcp-server"}, logger)
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
		CleanupSessions(context.Background(), registry, "ok-sess", []string{"cli-mcp-server"}, logger)
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
		CleanupSessions(context.Background(), registry, "down", []string{"cli-mcp-server"}, logger)
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
		CleanupSessions(context.Background(), registry, "bad-url", []string{"cli-mcp-server"}, logger)
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
		CleanupSessions(context.Background(), registry, "bad-header", []string{"cli-mcp-server"}, logger)
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
	var deleteCount atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/exec-close-1" {
			deleteCount.Add(1)
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
		"kubernetes-server": {
			Transport: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  server.URL + "/k8s",
			},
		},
	})

	client := newClient(registry, "exec-close-1")
	client.recordRequestedServers([]string{"cli-mcp-server"})
	require.NoError(t, client.Close())
	assert.Equal(t, int32(1), deleteCount.Load(), "Client.Close must DELETE sandbox for agent execution ID")

	// Second Close must not DELETE again (idempotent).
	require.NoError(t, client.Close())
	assert.Equal(t, int32(1), deleteCount.Load(), "second Close must not re-DELETE")

	execClient := newClient(registry, "exec-close-1")
	execClient.recordRequestedServers([]string{"cli-mcp-server"})
	exec := NewToolExecutor(execClient, registry, []string{"cli-mcp-server"}, nil, nil)
	deleteCount.Store(0)
	require.NoError(t, exec.Close())
	assert.Equal(t, int32(1), deleteCount.Load(), "ToolExecutor.Close must DELETE sandbox for agent execution ID")
}

func TestClient_Close_CleansUpMultipleRequestedServers(t *testing.T) {
	var deleteCount atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/sessions/exec-multi" {
			deleteCount.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	verifySSL := false
	cleanupURL := server.URL + "/sessions/{{.SESSION_ID}}"
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"cli-a": {
			Transport: config.TransportConfig{
				Type:              config.TransportTypeHTTP,
				URL:               server.URL + "/mcp-a",
				VerifySSL:         &verifySSL,
				SessionCleanupURL: cleanupURL,
			},
		},
		"cli-b": {
			Transport: config.TransportConfig{
				Type:              config.TransportTypeHTTP,
				URL:               server.URL + "/mcp-b",
				VerifySSL:         &verifySSL,
				SessionCleanupURL: cleanupURL,
			},
		},
		"no-cleanup": {
			Transport: config.TransportConfig{
				Type: config.TransportTypeHTTP,
				URL:  server.URL + "/mcp-c",
			},
		},
	})

	client := newClient(registry, "exec-multi")
	client.recordRequestedServers([]string{"cli-a", "cli-b", "no-cleanup"})
	require.NoError(t, client.Close())
	assert.Equal(t, int32(2), deleteCount.Load(), "Close must DELETE once per opted-in requested server")
}

func TestClient_Close_SkipsCleanupForUnrequestedServers(t *testing.T) {
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

	client := newClient(registry, "exec-k8s-only")
	client.recordRequestedServers([]string{"kubernetes-server"})
	require.NoError(t, client.Close())
	assert.False(t, deleted.Load(), "must not DELETE cli-mcp when it was not requested")
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
	client.recordRequestedServers([]string{"cli-mcp-server"})
	require.NoError(t, client.Close())
	assert.False(t, deleted.Load(), "empty mcpSessionID must not trigger sandbox DELETE")
}
