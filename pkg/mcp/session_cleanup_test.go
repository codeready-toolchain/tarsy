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
}

func TestNeedsSessionCleanup(t *testing.T) {
	assert.True(t, needsSessionCleanup(config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "https://cli-mcp-server:8443/mcp",
		CustomHeaders: map[string]string{
			"X-Session-ID": "{{.SESSION_ID}}",
		},
	}))
	assert.False(t, needsSessionCleanup(config.TransportConfig{
		Type: config.TransportTypeHTTP,
		URL:  "https://kubernetes-mcp-server:8443/mcp",
	}))
	assert.False(t, needsSessionCleanup(config.TransportConfig{
		Type:    config.TransportTypeStdio,
		Command: "npx",
	}))
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
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	CleanupSessions(context.Background(), registry, "inv-42", logger)

	assert.True(t, deleted.Load(), "expected DELETE /sessions/inv-42")
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "inv-42", gotSessionHeader)
}
