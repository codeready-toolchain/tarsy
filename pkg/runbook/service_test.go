package runbook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunbookService_Resolve(t *testing.T) {
	t.Run("URL provided fetches content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("# Fetched Runbook"))
		}))
		defer server.Close()

		svc := newTestService(t, server, "default content")
		content, err := svc.Resolve(context.Background(), server.URL+"/runbook.md")
		require.NoError(t, err)
		assert.Equal(t, "# Fetched Runbook", content)
	})

	t.Run("empty URL returns default content", func(t *testing.T) {
		svc := NewRunbookService(nil, "", "# Default Runbook")
		content, err := svc.Resolve(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, "# Default Runbook", content)
	})

	t.Run("fetch error returns error for caller to handle", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		svc := newTestService(t, server, "default content")
		_, err := svc.Resolve(context.Background(), server.URL+"/runbook.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch alert runbook")
	})

	t.Run("invalid URL domain returns error", func(t *testing.T) {
		cfg := &config.RunbookConfig{
			AllowedDomains: []string{"github.com"},
		}
		svc := NewRunbookService(cfg, "", "default")

		_, err := svc.Resolve(context.Background(), "https://evil.com/runbook.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not in allowed list")
	})

	t.Run("caches fetched content", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			_, _ = w.Write([]byte("# Cached Content"))
		}))
		defer server.Close()

		svc := newTestService(t, server, "default")

		// First call — fetches
		content1, err := svc.Resolve(context.Background(), server.URL+"/runbook.md")
		require.NoError(t, err)
		assert.Equal(t, "# Cached Content", content1)
		assert.Equal(t, 1, callCount)

		// Second call — cache hit
		content2, err := svc.Resolve(context.Background(), server.URL+"/runbook.md")
		require.NoError(t, err)
		assert.Equal(t, "# Cached Content", content2)
		assert.Equal(t, 1, callCount) // Not incremented
	})
}

func TestRunbookService_ListRunbooks(t *testing.T) {
	t.Run("returns files from configured repo", func(t *testing.T) {
		items := []githubContentItem{
			{Name: "k8s.md", Path: "runbooks/k8s.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/k8s.md"},
			{Name: "net.md", Path: "runbooks/net.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/net.md"},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		}))
		defer server.Close()

		cfg := &config.RunbookConfig{
			RepoURL: "https://github.com/org/repo/tree/main/runbooks",
		}
		svc := newTestServiceWithConfig(t, server, cfg, "default")

		files, err := svc.ListRunbooks(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{
			"https://github.com/org/repo/blob/main/runbooks/k8s.md",
			"https://github.com/org/repo/blob/main/runbooks/net.md",
		}, files)
	})

	t.Run("no repo URL returns empty slice", func(t *testing.T) {
		svc := NewRunbookService(nil, "", "default")
		files, err := svc.ListRunbooks(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{}, files)
	})

	t.Run("empty repo URL returns empty slice", func(t *testing.T) {
		cfg := &config.RunbookConfig{RepoURL: ""}
		svc := NewRunbookService(cfg, "", "default")
		files, err := svc.ListRunbooks(context.Background())
		require.NoError(t, err)
		assert.Equal(t, []string{}, files)
	})

	t.Run("API failure returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		cfg := &config.RunbookConfig{
			RepoURL: "https://github.com/org/repo/tree/main/runbooks",
		}
		svc := newTestServiceWithConfig(t, server, cfg, "default")

		_, err := svc.ListRunbooks(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list runbooks")
	})

	t.Run("caches listing results", func(t *testing.T) {
		callCount := 0
		items := []githubContentItem{
			{Name: "k8s.md", Path: "runbooks/k8s.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/k8s.md"},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		}))
		defer server.Close()

		cfg := &config.RunbookConfig{
			RepoURL: "https://github.com/org/repo/tree/main/runbooks",
		}
		svc := newTestServiceWithConfig(t, server, cfg, "default")

		// First call — fetches
		files1, err := svc.ListRunbooks(context.Background())
		require.NoError(t, err)
		assert.Len(t, files1, 1)
		assert.Equal(t, 1, callCount)

		// Second call — cache hit
		files2, err := svc.ListRunbooks(context.Background())
		require.NoError(t, err)
		assert.Len(t, files2, 1)
		assert.Equal(t, 1, callCount) // Not incremented
	})
}

// newTestService creates a RunbookService with no domain restrictions, using the test server for HTTP.
func newTestService(t *testing.T, server *httptest.Server, defaultRunbook string) *RunbookService {
	t.Helper()
	cfg := &config.RunbookConfig{
		CacheTTL:       1 * time.Minute,
		AllowedDomains: nil, // No domain restrictions for tests
	}
	return newTestServiceWithConfig(t, server, cfg, defaultRunbook)
}

// newTestServiceWithConfig creates a RunbookService with custom config, routing API calls through the test server.
func newTestServiceWithConfig(t *testing.T, server *httptest.Server, cfg *config.RunbookConfig, defaultRunbook string) *RunbookService {
	t.Helper()
	svc := NewRunbookService(cfg, "", defaultRunbook)
	// Override the GitHub client to use test server
	svc.github.httpClient = &http.Client{
		Transport: &testTransport{
			server:   server,
			delegate: http.DefaultTransport,
		},
	}
	return svc
}
