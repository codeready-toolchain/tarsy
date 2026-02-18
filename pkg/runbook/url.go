package runbook

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// RepoURLParts holds the parsed components of a GitHub repository URL.
type RepoURLParts struct {
	Owner string
	Repo  string
	Ref   string
	Path  string
}

// githubBlobTreePattern matches GitHub blob or tree URLs.
// Format: https://github.com/{owner}/{repo}/{blob|tree}/{ref}/{path...}
var githubBlobTreePattern = regexp.MustCompile(`^/([^/]+)/([^/]+)/(blob|tree)/([^/]+)(?:/(.*))?$`)

// ConvertToRawURL converts a GitHub blob URL to a raw content URL.
// Returns the URL unchanged if already raw or not a recognized GitHub URL.
func ConvertToRawURL(githubURL string) string {
	parsed, err := url.Parse(githubURL)
	if err != nil {
		return githubURL
	}

	// Already a raw URL — pass through
	if parsed.Host == "raw.githubusercontent.com" {
		return githubURL
	}

	// Only convert github.com URLs
	if parsed.Host != "github.com" && parsed.Host != "www.github.com" {
		return githubURL
	}

	matches := githubBlobTreePattern.FindStringSubmatch(parsed.Path)
	if matches == nil {
		return githubURL
	}

	owner := matches[1]
	repo := matches[2]
	// matches[3] is "blob" or "tree"
	ref := matches[4]
	path := matches[5]

	// Build raw URL: https://raw.githubusercontent.com/{owner}/{repo}/refs/heads/{ref}/{path}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/refs/heads/%s/%s", owner, repo, ref, path)
	return rawURL
}

// ParseRepoURL parses a GitHub tree/blob URL into components.
// Supports: https://github.com/{owner}/{repo}/tree/{ref}/{path}
func ParseRepoURL(rawURL string) (*RepoURLParts, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("malformed URL: %w", err)
	}

	if parsed.Host != "github.com" && parsed.Host != "www.github.com" {
		return nil, fmt.Errorf("not a GitHub URL: %s", parsed.Host)
	}

	matches := githubBlobTreePattern.FindStringSubmatch(parsed.Path)
	if matches == nil {
		return nil, fmt.Errorf("URL does not match GitHub blob/tree pattern: %s", parsed.Path)
	}

	return &RepoURLParts{
		Owner: matches[1],
		Repo:  matches[2],
		Ref:   matches[4],
		Path:  matches[5],
	}, nil
}

// ValidateRunbookURL checks that the URL uses an allowed scheme and domain.
func ValidateRunbookURL(rawURL string, allowedDomains []string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}

	// Scheme check
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid scheme %q: only http and https allowed", parsed.Scheme)
	}

	// Domain allowlist check (if configured)
	if len(allowedDomains) > 0 {
		host := strings.ToLower(parsed.Hostname())
		allowed := false
		for _, domain := range allowedDomains {
			if host == domain || host == "www."+domain {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("domain %q not in allowed list", host)
		}
	}

	return nil
}
