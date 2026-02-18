package runbook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// GitHubClient provides HTTP access to GitHub for downloading runbook content
// and listing markdown files in a repository.
type GitHubClient struct {
	httpClient *http.Client
	token      string
	logger     *slog.Logger
}

// NewGitHubClient creates an HTTP client for GitHub operations.
// token may be empty (public repos only, lower rate limits).
func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		token:      token,
		logger:     slog.Default(),
	}
}

// DownloadContent fetches raw content from a GitHub URL.
// Converts blob URLs to raw.githubusercontent.com URLs.
// Handles authentication via bearer token.
func (c *GitHubClient) DownloadContent(ctx context.Context, rawURL string) (string, error) {
	downloadURL := ConvertToRawURL(rawURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch runbook from %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned HTTP %d for %s", resp.StatusCode, downloadURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	return string(body), nil
}

// githubContentItem represents a single item from the GitHub Contents API response.
type githubContentItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"` // "file" or "dir"
	HTMLURL string `json:"html_url"`
}

// ListMarkdownFiles returns all .md file URLs from a GitHub directory.
// Uses the GitHub Contents API recursively.
func (c *GitHubClient) ListMarkdownFiles(ctx context.Context, repoURL string) ([]string, error) {
	parts, err := ParseRepoURL(repoURL)
	if err != nil {
		return nil, fmt.Errorf("parse repo URL: %w", err)
	}

	return c.listMarkdownFilesRecursive(ctx, parts.Owner, parts.Repo, parts.Ref, parts.Path)
}

func (c *GitHubClient) listMarkdownFilesRecursive(ctx context.Context, owner, repo, ref, path string) ([]string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list contents at %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d for path %q", resp.StatusCode, path)
	}

	var items []githubContentItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode contents response: %w", err)
	}

	var mdFiles []string
	for _, item := range items {
		switch item.Type {
		case "file":
			if strings.HasSuffix(strings.ToLower(item.Name), ".md") {
				// Use the HTML URL (blob URL) as the canonical reference
				mdFiles = append(mdFiles, item.HTMLURL)
			}
		case "dir":
			subFiles, err := c.listMarkdownFilesRecursive(ctx, owner, repo, ref, item.Path)
			if err != nil {
				c.logger.Warn("Failed to list subdirectory", "path", item.Path, "error", err)
				continue
			}
			mdFiles = append(mdFiles, subFiles...)
		}
	}

	return mdFiles, nil
}

func (c *GitHubClient) setAuthHeader(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
