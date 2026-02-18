package runbook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRawURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "blob URL converts to raw",
			input:    "https://github.com/org/repo/blob/main/runbooks/k8s.md",
			expected: "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbooks/k8s.md",
		},
		{
			name:     "tree URL converts to raw",
			input:    "https://github.com/org/repo/tree/main/runbooks/k8s.md",
			expected: "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbooks/k8s.md",
		},
		{
			name:     "nested path converts correctly",
			input:    "https://github.com/myorg/docs/blob/develop/sre/runbooks/network.md",
			expected: "https://raw.githubusercontent.com/myorg/docs/refs/heads/develop/sre/runbooks/network.md",
		},
		{
			name:     "already raw URL passes through",
			input:    "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbooks/k8s.md",
			expected: "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbooks/k8s.md",
		},
		{
			name:     "non-GitHub URL passes through",
			input:    "https://example.com/some/path",
			expected: "https://example.com/some/path",
		},
		{
			name:     "github.com without blob/tree passes through",
			input:    "https://github.com/org/repo",
			expected: "https://github.com/org/repo",
		},
		{
			name:     "www.github.com blob URL converts",
			input:    "https://www.github.com/org/repo/blob/main/runbook.md",
			expected: "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbook.md",
		},
		{
			name:     "invalid URL passes through",
			input:    "://not-a-url",
			expected: "://not-a-url",
		},
		{
			name:     "empty string passes through",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertToRawURL(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *RepoURLParts
		wantErr bool
		errMsg  string
	}{
		{
			name:  "tree URL with path",
			input: "https://github.com/org/repo/tree/main/runbooks",
			want: &RepoURLParts{
				Owner: "org",
				Repo:  "repo",
				Ref:   "main",
				Path:  "runbooks",
			},
		},
		{
			name:  "blob URL with nested path",
			input: "https://github.com/myorg/docs/blob/develop/sre/runbooks/network.md",
			want: &RepoURLParts{
				Owner: "myorg",
				Repo:  "docs",
				Ref:   "develop",
				Path:  "sre/runbooks/network.md",
			},
		},
		{
			name:  "tree URL without trailing path",
			input: "https://github.com/org/repo/tree/main",
			want: &RepoURLParts{
				Owner: "org",
				Repo:  "repo",
				Ref:   "main",
				Path:  "",
			},
		},
		{
			name:    "not a GitHub URL",
			input:   "https://gitlab.com/org/repo/tree/main/runbooks",
			wantErr: true,
			errMsg:  "not a GitHub URL",
		},
		{
			name:    "GitHub URL without blob or tree",
			input:   "https://github.com/org/repo",
			wantErr: true,
			errMsg:  "does not match",
		},
		{
			name:    "malformed URL",
			input:   "://broken",
			wantErr: true,
			errMsg:  "malformed URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRepoURL(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateRunbookURL(t *testing.T) {
	defaultDomains := []string{"github.com", "raw.githubusercontent.com"}

	tests := []struct {
		name           string
		url            string
		allowedDomains []string
		wantErr        bool
		errMsg         string
	}{
		{
			name:           "valid github.com URL",
			url:            "https://github.com/org/repo/blob/main/runbook.md",
			allowedDomains: defaultDomains,
			wantErr:        false,
		},
		{
			name:           "valid raw.githubusercontent.com URL",
			url:            "https://raw.githubusercontent.com/org/repo/refs/heads/main/runbook.md",
			allowedDomains: defaultDomains,
			wantErr:        false,
		},
		{
			name:           "valid http scheme",
			url:            "http://github.com/org/repo/blob/main/runbook.md",
			allowedDomains: defaultDomains,
			wantErr:        false,
		},
		{
			name:           "www prefix accepted",
			url:            "https://www.github.com/org/repo/blob/main/runbook.md",
			allowedDomains: defaultDomains,
			wantErr:        false,
		},
		{
			name:           "invalid scheme ftp",
			url:            "ftp://github.com/org/repo/blob/main/runbook.md",
			allowedDomains: defaultDomains,
			wantErr:        true,
			errMsg:         "invalid scheme",
		},
		{
			name:           "invalid scheme file",
			url:            "file:///etc/passwd",
			allowedDomains: defaultDomains,
			wantErr:        true,
			errMsg:         "invalid scheme",
		},
		{
			name:           "disallowed domain",
			url:            "https://evil.com/malicious",
			allowedDomains: defaultDomains,
			wantErr:        true,
			errMsg:         "not in allowed list",
		},
		{
			name:           "empty allowlist allows any domain",
			url:            "https://any-domain.com/path",
			allowedDomains: []string{},
			wantErr:        false,
		},
		{
			name:           "nil allowlist allows any domain",
			url:            "https://any-domain.com/path",
			allowedDomains: nil,
			wantErr:        false,
		},
		{
			name:           "malformed URL",
			url:            "://broken",
			allowedDomains: defaultDomains,
			wantErr:        true,
			errMsg:         "malformed URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRunbookURL(tt.url, tt.allowedDomains)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			require.NoError(t, err)
		})
	}
}
