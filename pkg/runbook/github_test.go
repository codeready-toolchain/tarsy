package runbook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubClient_DownloadContent(t *testing.T) {
	t.Run("successful download", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("# Runbook Content\n\nStep 1: Check pods"))
		}))
		defer server.Close()

		client := newTestGitHubClient("", server)

		content, err := client.DownloadContent(context.Background(), server.URL+"/org/repo/blob/main/runbook.md")
		require.NoError(t, err)
		assert.Equal(t, "# Runbook Content\n\nStep 1: Check pods", content)
	})

	t.Run("authentication header sent when token present", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("content"))
		}))
		defer server.Close()

		client := newTestGitHubClient("test-token-123", server)

		_, err := client.DownloadContent(context.Background(), server.URL+"/file.md")
		require.NoError(t, err)
		assert.Equal(t, "Bearer test-token-123", gotAuth)
	})

	t.Run("no auth header when token empty", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("content"))
		}))
		defer server.Close()

		client := newTestGitHubClient("", server)

		_, err := client.DownloadContent(context.Background(), server.URL+"/file.md")
		require.NoError(t, err)
		assert.Empty(t, gotAuth)
	})

	t.Run("HTTP 404 returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := newTestGitHubClient("", server)

		_, err := client.DownloadContent(context.Background(), server.URL+"/missing.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("HTTP 500 returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := newTestGitHubClient("", server)

		_, err := client.DownloadContent(context.Background(), server.URL+"/file.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("content"))
		}))
		defer server.Close()

		client := newTestGitHubClient("", server)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := client.DownloadContent(ctx, server.URL+"/file.md")
		require.Error(t, err)
	})
}

func TestGitHubClient_ListMarkdownFiles(t *testing.T) {
	t.Run("lists md files from flat directory", func(t *testing.T) {
		items := []githubContentItem{
			{Name: "k8s.md", Path: "runbooks/k8s.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/k8s.md"},
			{Name: "network.md", Path: "runbooks/network.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/network.md"},
			{Name: "README.txt", Path: "runbooks/README.txt", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/README.txt"},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		}))
		defer server.Close()

		client := newTestGitHubClientWithAPIBase("", server)
		files, err := client.ListMarkdownFiles(context.Background(), "https://github.com/org/repo/tree/main/runbooks")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"https://github.com/org/repo/blob/main/runbooks/k8s.md",
			"https://github.com/org/repo/blob/main/runbooks/network.md",
		}, files)
	})

	t.Run("recurses into subdirectories", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")

			if callCount == 1 {
				// Root directory
				items := []githubContentItem{
					{Name: "root.md", Path: "runbooks/root.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/root.md"},
					{Name: "subdir", Path: "runbooks/subdir", Type: "dir"},
				}
				_ = json.NewEncoder(w).Encode(items)
			} else {
				// Subdirectory
				items := []githubContentItem{
					{Name: "nested.md", Path: "runbooks/subdir/nested.md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/subdir/nested.md"},
				}
				_ = json.NewEncoder(w).Encode(items)
			}
		}))
		defer server.Close()

		client := newTestGitHubClientWithAPIBase("", server)
		files, err := client.ListMarkdownFiles(context.Background(), "https://github.com/org/repo/tree/main/runbooks")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"https://github.com/org/repo/blob/main/runbooks/root.md",
			"https://github.com/org/repo/blob/main/runbooks/subdir/nested.md",
		}, files)
		assert.Equal(t, 2, callCount)
	})

	t.Run("empty directory returns empty slice", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]githubContentItem{})
		}))
		defer server.Close()

		client := newTestGitHubClientWithAPIBase("", server)
		files, err := client.ListMarkdownFiles(context.Background(), "https://github.com/org/repo/tree/main/runbooks")
		require.NoError(t, err)
		assert.Empty(t, files)
	})

	t.Run("API error returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := newTestGitHubClientWithAPIBase("", server)
		_, err := client.ListMarkdownFiles(context.Background(), "https://github.com/org/repo/tree/main/runbooks")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("invalid repo URL returns error", func(t *testing.T) {
		client := NewGitHubClient("")
		_, err := client.ListMarkdownFiles(context.Background(), "https://not-github.com/repo")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse repo URL")
	})

	t.Run("case insensitive md extension", func(t *testing.T) {
		items := []githubContentItem{
			{Name: "upper.MD", Path: "runbooks/upper.MD", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/upper.MD"},
			{Name: "mixed.Md", Path: "runbooks/mixed.Md", Type: "file", HTMLURL: "https://github.com/org/repo/blob/main/runbooks/mixed.Md"},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(items)
		}))
		defer server.Close()

		client := newTestGitHubClientWithAPIBase("", server)
		files, err := client.ListMarkdownFiles(context.Background(), "https://github.com/org/repo/tree/main/runbooks")
		require.NoError(t, err)
		assert.Len(t, files, 2)
	})
}

// newTestGitHubClient creates a GitHubClient that uses the test server for raw content downloads.
// This is for DownloadContent tests where the URL is used directly.
func newTestGitHubClient(token string, server *httptest.Server) *GitHubClient {
	client := NewGitHubClient(token)
	client.httpClient = server.Client()
	return client
}

// newTestGitHubClientWithAPIBase creates a GitHubClient that routes API calls to the test server.
// It overrides the HTTP client to intercept GitHub API calls.
func newTestGitHubClientWithAPIBase(token string, server *httptest.Server) *GitHubClient {
	client := NewGitHubClient(token)
	// Override the HTTP transport to redirect api.github.com to test server
	client.httpClient = &http.Client{
		Transport: &testTransport{
			server:   server,
			delegate: http.DefaultTransport,
		},
	}
	return client
}

// testTransport redirects GitHub API requests to the test server.
type testTransport struct {
	server   *httptest.Server
	delegate http.RoundTripper
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect GitHub API and raw content requests to test server
	if req.URL.Host == "api.github.com" || req.URL.Host == "raw.githubusercontent.com" {
		parsed, _ := url.Parse(t.server.URL)
		req.URL.Scheme = parsed.Scheme
		req.URL.Host = parsed.Host
	}
	return t.delegate.RoundTrip(req)
}
