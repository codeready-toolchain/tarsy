package runbook

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Service orchestrates runbook resolution and delivery.
type Service struct {
	github   *GitHubClient
	cache    *Cache
	cfg      *config.RunbookConfig
	defaults string // Fallback default content
}

// NewService creates a new Service.
// githubToken is the resolved token value (empty string = no auth, public repos only).
// defaultRunbook is the fallback content used when no URL is provided.
func NewService(cfg *config.RunbookConfig, githubToken string, defaultRunbook string) *Service {
	cacheTTL := 1 * time.Minute
	if cfg != nil && cfg.CacheTTL > 0 {
		cacheTTL = cfg.CacheTTL
	}

	return &Service{
		github:   NewGitHubClient(githubToken),
		cache:    NewCache(cacheTTL),
		cfg:      cfg,
		defaults: defaultRunbook,
	}
}

// Resolve returns runbook content using the resolution hierarchy:
//  1. alertRunbookURL (per-alert, from API submission)
//  2. default content (inline from config)
//
// URL-based runbooks are fetched via GitHubClient with caching.
// On fetch failure: returns error (caller applies fail-open policy).
func (s *Service) Resolve(ctx context.Context, alertRunbookURL string) (string, error) {
	// Per-alert URL takes highest priority
	if alertRunbookURL != "" {
		content, err := s.fetchWithCache(ctx, alertRunbookURL)
		if err != nil {
			return "", fmt.Errorf("fetch alert runbook %s: %w", alertRunbookURL, err)
		}
		return content, nil
	}

	// Default content (inline, no fetch)
	return s.defaults, nil
}

// ListRunbooks returns available runbook URLs from the configured repository.
// Returns empty slice if repo_url is not configured.
func (s *Service) ListRunbooks(ctx context.Context) ([]string, error) {
	if s.cfg == nil || s.cfg.RepoURL == "" {
		return []string{}, nil
	}

	// Check cache (using repo URL as key)
	if cached, ok := s.cache.Get(s.cfg.RepoURL); ok {
		return splitCachedList(cached), nil
	}

	files, err := s.github.ListMarkdownFiles(ctx, s.cfg.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("list runbooks from %s: %w", s.cfg.RepoURL, err)
	}

	if files == nil {
		files = []string{}
	}

	// Cache the result as a joined string
	s.cache.Set(s.cfg.RepoURL, joinForCache(files))
	return files, nil
}

// OverrideHTTPClientForTest replaces the internal GitHub client's HTTP client.
// For testing only.
func (s *Service) OverrideHTTPClientForTest(httpClient *http.Client) {
	s.github.httpClient = httpClient
}

func (s *Service) fetchWithCache(ctx context.Context, rawURL string) (string, error) {
	// Validate URL
	var allowedDomains []string
	if s.cfg != nil {
		allowedDomains = s.cfg.AllowedDomains
	}
	if err := ValidateRunbookURL(rawURL, allowedDomains); err != nil {
		return "", err
	}

	// Check cache (key: normalized URL)
	normalizedURL := ConvertToRawURL(rawURL)
	if content, ok := s.cache.Get(normalizedURL); ok {
		return content, nil
	}

	// Fetch from GitHub
	content, err := s.github.DownloadContent(ctx, rawURL)
	if err != nil {
		return "", err
	}

	// Cache the result
	s.cache.Set(normalizedURL, content)
	return content, nil
}

func joinForCache(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(items[0])
	for _, item := range items[1:] {
		sb.WriteByte('\x00')
		sb.WriteString(item)
	}
	return sb.String()
}

func splitCachedList(cached string) []string {
	if cached == "" {
		return []string{}
	}
	var result []string
	start := 0
	for i := 0; i < len(cached); i++ {
		if cached[i] == '\x00' {
			result = append(result, cached[start:i])
			start = i + 1
		}
	}
	result = append(result, cached[start:])
	return result
}
