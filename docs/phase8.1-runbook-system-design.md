# Phase 8.1: Runbook System — Detailed Design

## Overview

The runbook system enables TARSy to fetch, cache, and inject runbook content into LLM prompts, giving agents organization-specific troubleshooting procedures. Runbooks are markdown documents (typically stored in GitHub) that provide step-by-step investigation and remediation guidance.

### Current State

The new TARSy has scaffolding in place but the runbook system is not functional:

- **`runbook_url`** field exists on `AlertSession` Ent schema (stored on submission, never used)
- **`RunbookContent`** field exists on `ExecutionContext` (always set to builtin default)
- **`FormatRunbookSection()`** in prompt builder works correctly (just receives default content)
- **`defaults.runbook`** YAML config is loaded but executor ignores it (uses `GetBuiltinConfig().DefaultRunbook`)
- **Dashboard** has a plain text input for runbook URL (no dropdown/browsing)
- **No GitHub integration** — no HTTP client, no API endpoints for listing runbooks

### Old TARSy Behavior

- **Two services**: `RunbookService` (download content from URL) and `RunbooksService` (list .md files from GitHub repo)
- **Per-alert only**: Runbook URL submitted with alert; no per-chain configuration
- **No caching**: Content fetched fresh every session
- **GitHub auth**: Single token, falls back to default runbook without it
- **Dashboard**: Autocomplete dropdown loading available runbooks + custom URL support
- **SSRF protection**: Client-side GitHub-only domain restriction; server-side http/https scheme check

---

## Architecture

### New Package: `pkg/runbook/`

```
pkg/runbook/
├── service.go          # RunbookService — orchestrates resolution and delivery
├── github.go           # GitHubClient — GitHub API interactions (list + download)
├── cache.go            # RunbookCache — in-memory TTL cache
└── url.go              # URL parsing, conversion, validation
```

Config types (`RunbookConfig`, `GitHubConfig`) live in `pkg/config/` following the existing pattern (all other config types — `MCPServerConfig`, `ChainConfig`, `LLMProviderConfig` — are defined there). `pkg/runbook/` receives resolved config values at construction time.

### Component Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│ Config (tarsy.yaml)                                                  │
│  system.github.token_env → env var name (default: GITHUB_TOKEN)    │
│  system.runbooks.repo_url → GitHub repo for listing                 │
│  system.runbooks.cache_ttl → cache duration                         │
│  defaults.runbook → default inline content                          │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │
                        ┌──────────▼──────────┐
                        │   RunbookService     │
                        │   (pkg/runbook/)     │
                        │                      │
                        │  Resolve() → content │
                        │  ListRunbooks()      │
                        └──┬───────────┬───────┘
                           │           │
              ┌────────────▼──┐  ┌─────▼────────┐
              │  GitHubClient  │  │ RunbookCache  │
              │  (HTTP fetch)  │  │ (in-memory)   │
              └────────────────┘  └───────────────┘
```

---

## Configuration

### YAML Structure

A new `system:` top-level section in `tarsy.yaml` groups system-wide infrastructure settings, keeping them distinct from operational config (`defaults:`, `mcp_servers:`, `agents:`, `agent_chains:`):

```yaml
# System-wide infrastructure configuration
system:
  github:
    token_env: "GITHUB_TOKEN"  # Env var name containing GitHub PAT (optional)
                               # Defaults to GITHUB_TOKEN if omitted
  runbooks:
    repo_url: "https://github.com/org/repo/tree/main/runbooks"  # Optional
    cache_ttl: "1m"            # Default: 1 minute
    allowed_domains:           # URL domain allowlist (default: github.com only)
      - "github.com"
      - "raw.githubusercontent.com"

# Default runbook content (inline, existing field)
defaults:
  runbook: |
    # Company Troubleshooting Guide
    ...
```

**Note on per-chain runbooks**: There is no per-chain `runbook` field. Chain-specific investigation guidance is handled via the existing `custom_instructions` on agents, which is already part of the prompt composition. Runbooks are a separate concern — general troubleshooting procedures selected per-alert (or defaulted globally).

### Config Types

All config types live in `pkg/config/` following the existing pattern. A new `pkg/config/system.go` defines the resolved types, and `pkg/config/loader.go` gains the YAML parse types.

```go
// pkg/config/system.go — new file

// GitHubConfig holds resolved GitHub integration configuration.
type GitHubConfig struct {
    TokenEnv string // Env var name containing GitHub PAT (default: "GITHUB_TOKEN")
}

// RunbookConfig holds resolved runbook system configuration.
type RunbookConfig struct {
    RepoURL        string        // GitHub repo URL for listing runbooks (empty = disabled)
    CacheTTL       time.Duration // Cache duration (default: 1m)
    AllowedDomains []string      // Allowed URL domains (default: ["github.com", "raw.githubusercontent.com"])
}
```

```go
// pkg/config/loader.go — additions to TarsyYAMLConfig

type TarsyYAMLConfig struct {
    // ... existing fields ...
    System *SystemYAMLConfig `yaml:"system"`
}

// SystemYAMLConfig groups system-wide infrastructure settings.
type SystemYAMLConfig struct {
    GitHub   *GitHubYAMLConfig   `yaml:"github"`
    Runbooks *RunbooksYAMLConfig `yaml:"runbooks"`
}

type GitHubYAMLConfig struct {
    TokenEnv string `yaml:"token_env,omitempty"` // Defaults to "GITHUB_TOKEN" if omitted
}

type RunbooksYAMLConfig struct {
    RepoURL        string   `yaml:"repo_url,omitempty"`
    CacheTTL       string   `yaml:"cache_ttl,omitempty"` // Parsed to time.Duration
    AllowedDomains []string `yaml:"allowed_domains,omitempty"`
}
```

### Config on `Config` Struct

```go
// pkg/config/config.go — additions

type Config struct {
    // ... existing fields ...

    // GitHub integration configuration (resolved from system.github)
    GitHub *GitHubConfig

    // Runbook system configuration (resolved from system.runbooks)
    Runbooks *RunbookConfig
}
```

`GitHubConfig` and `RunbookConfig` are simple value types (not registries), stored directly on `Config`. Resolved during `load()` with sensible defaults applied.

---

## Runbook Resolution Hierarchy

When building `ExecutionContext.RunbookContent`, the executor resolves runbook content using this priority chain:

```
1. Per-alert runbook URL  (session.RunbookURL — submitted via API)
   ↓ (if empty)
2. Defaults runbook       (cfg.Defaults.Runbook — inline content from YAML or builtin)
```

Step 1 is a URL that requires fetching. Step 2 is inline content (no fetch).

**Note**: There is no per-chain runbook level. Chain-specific investigation guidance is provided via agent `custom_instructions`, which is a separate prompt section from runbook content. Runbooks provide general troubleshooting procedures; custom instructions provide agent-specific behavioral guidance.

### Resolution Flow in Executor

```go
// In executeAgent() — replaces the current hardcoded line

runbookContent, err := e.runbookService.Resolve(ctx, session.RunbookURL)
if err != nil {
    // Fail-open: log warning, use default content
    logger.Warn("Failed to resolve runbook, using default", "error", err)
    runbookContent = e.cfg.Defaults.Runbook
}

execCtx := &agent.ExecutionContext{
    // ...
    RunbookContent: runbookContent,
}
```

The same resolution applies in `ChatMessageExecutor.execute()`.

---

## RunbookService

### Interface

```go
// pkg/runbook/service.go

type RunbookService struct {
    github   *GitHubClient
    cache    *RunbookCache
    cfg      *config.RunbookConfig
    defaults string // Fallback default content (cfg.Defaults.Runbook, resolved at startup)
}

// NewRunbookService creates a new RunbookService.
// githubToken is the resolved token value (empty string = no auth, public repos only).
// defaultRunbook is the fallback content used when no URL is provided or a fetch fails.
func NewRunbookService(cfg *config.RunbookConfig, githubToken string, defaultRunbook string) *RunbookService

// Resolve returns runbook content using the resolution hierarchy:
//   1. alertRunbookURL (per-alert, from API submission)
//   2. default content (inline from config)
//
// URL-based runbooks are fetched via GitHubClient with caching.
// On fetch failure: returns error (caller applies fail-open policy).
func (s *RunbookService) Resolve(ctx context.Context, alertRunbookURL string) (string, error)

// ListRunbooks returns available runbook URLs from the configured repository.
// Returns empty slice if repo_url is not configured.
func (s *RunbookService) ListRunbooks(ctx context.Context) ([]string, error)
```

### Resolve Implementation

```go
func (s *RunbookService) Resolve(ctx context.Context, alertRunbookURL string) (string, error) {
    // 1. Per-alert URL takes highest priority
    if alertRunbookURL != "" {
        content, err := s.fetchWithCache(ctx, alertRunbookURL)
        if err != nil {
            return "", fmt.Errorf("fetch alert runbook %s: %w", alertRunbookURL, err)
        }
        return content, nil
    }

    // 2. Default content (inline, no fetch)
    return s.defaults, nil
}
```

### Fail-Open Policy

Consistent with TARSy's investigation-availability-first philosophy:

- Runbook fetch failure → use default content, log warning
- Missing GitHub token → system warning at startup, URL-based runbooks degrade to default
- Invalid/unreachable URL → use default content, error in logs
- The session should never fail because of a runbook issue

---

## GitHubClient

### Interface

```go
// pkg/runbook/github.go

type GitHubClient struct {
    httpClient *http.Client
    token      string
    logger     *slog.Logger
}

// NewGitHubClient creates an HTTP client for GitHub operations.
// token may be empty (public repos only, lower rate limits).
func NewGitHubClient(token string) *GitHubClient

// DownloadContent fetches raw content from a GitHub URL.
// Converts blob URLs to raw.githubusercontent.com URLs.
// Handles authentication via bearer token.
func (c *GitHubClient) DownloadContent(ctx context.Context, url string) (string, error)

// ListMarkdownFiles returns all .md file URLs from a GitHub directory.
// Uses the GitHub Contents API (recursive).
func (c *GitHubClient) ListMarkdownFiles(ctx context.Context, repoURL string) ([]string, error)
```

### URL Conversion

GitHub blob URLs must be converted to raw content URLs for direct download:

```
Input:  https://github.com/org/repo/blob/main/runbooks/k8s.md
Output: https://raw.githubusercontent.com/org/repo/refs/heads/main/runbooks/k8s.md
```

Already-raw URLs (`raw.githubusercontent.com`) are passed through unchanged.

```go
// pkg/runbook/url.go

// ConvertToRawURL converts a GitHub blob URL to a raw content URL.
// Returns the URL unchanged if already raw or not a recognized GitHub URL.
func ConvertToRawURL(githubURL string) string

// ParseRepoURL parses a GitHub tree/blob URL into components.
// Supports: https://github.com/org/repo/tree/branch/path
type RepoURLParts struct {
    Owner  string
    Repo   string
    Ref    string
    Path   string
}
func ParseRepoURL(url string) (*RepoURLParts, error)

// ValidateRunbookURL checks that the URL uses an allowed scheme and domain.
func ValidateRunbookURL(rawURL string, allowedDomains []string) error
```

### GitHub Contents API for Listing

Use the GitHub REST API directly (no `go-github` dependency — we only need two simple endpoints):

```
GET https://api.github.com/repos/{owner}/{repo}/contents/{path}?ref={ref}
Accept: application/vnd.github.v3+json
Authorization: Bearer {token}  (if available)
```

Response is a JSON array of content items. Recursively follow `type: "dir"` entries. Collect `type: "file"` entries ending in `.md`, constructing full GitHub blob URLs.

### Authentication

```go
func NewGitHubClient(token string) *GitHubClient {
    client := &http.Client{Timeout: 30 * time.Second}
    return &GitHubClient{
        httpClient: client,
        token:      token,
    }
}
```

Token is resolved at startup from the env var specified in `system.github.token_env` (defaulting to `GITHUB_TOKEN`). If the env var is empty or unset, the client operates without auth (public repos only, 60 req/hour rate limit). A system warning is added via `SystemWarningsService`.

### Rate Limiting

GitHub API rate limits: 60/hour unauthenticated, 5000/hour with token. The listing endpoint is the primary consumer. With caching (1min TTL), this is well within limits for any reasonable usage.

No explicit rate limiter needed in Phase 8.1. If rate limiting becomes an issue, add `golang.org/x/time/rate` later.

---

## RunbookCache

### Interface

```go
// pkg/runbook/cache.go

type RunbookCache struct {
    mu      sync.RWMutex
    entries map[string]*cacheEntry
    ttl     time.Duration
}

type cacheEntry struct {
    content   string
    fetchedAt time.Time
}

func NewRunbookCache(ttl time.Duration) *RunbookCache

// Get returns cached content if present and not expired.
func (c *RunbookCache) Get(url string) (string, bool)

// Set stores content with the current timestamp.
func (c *RunbookCache) Set(url string, content string)
```

### Design Decisions

- **Key**: Normalized URL string (after `ConvertToRawURL`)
- **TTL**: Configurable, default 1 minute. Runbooks don't change frequently, but 1min keeps content fresh while preventing duplicate fetches from concurrent sessions.
- **Eviction**: Lazy — expired entries are not proactively cleaned. Checked on `Get()`. Given the small number of runbook URLs (typically <100), memory is not a concern.
- **No persistence**: Cache is in-memory, resets on restart. Acceptable because runbook fetches are fast.
- **Thread-safe**: `sync.RWMutex` for concurrent access from multiple agent executions.

### Cache Usage in Service

```go
func (s *RunbookService) fetchWithCache(ctx context.Context, url string) (string, error) {
    // Validate URL
    if err := ValidateRunbookURL(url, s.cfg.AllowedDomains); err != nil {
        return "", err
    }

    // Check cache
    normalizedURL := ConvertToRawURL(url)
    if content, ok := s.cache.Get(normalizedURL); ok {
        return content, nil
    }

    // Fetch from GitHub
    content, err := s.github.DownloadContent(ctx, url)
    if err != nil {
        return "", err
    }

    // Cache the result
    s.cache.Set(normalizedURL, content)
    return content, nil
}
```

---

## URL Validation

### Server-Side Validation

Two validation points:

1. **API submission** (`handler_alert.go`): Validate `runbook` URL in the alert request
2. **Execution time** (`RunbookService.fetchWithCache`): Validate before fetching

```go
// pkg/runbook/url.go

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
```

### API Handler Validation

Add URL validation to `handleSubmitAlert` in `handler_alert.go`:

```go
if req.Runbook != "" {
    if err := runbook.ValidateRunbookURL(req.Runbook, s.cfg.Runbooks.AllowedDomains); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
            "error":   "Invalid runbook URL",
            "message": err.Error(),
            "field":   "runbook",
        })
    }
}
```

`s.cfg.Runbooks.AllowedDomains` is the resolved domain list from config (always non-nil after `resolveRunbooksConfig` applies defaults). The handler holds a reference to `*config.Config` already (consistent with other handlers).

---

## API Endpoints

### List Runbooks

```
GET /api/v1/runbooks
```

Returns a JSON array of runbook URLs from the configured GitHub repository.

**Response (200)**:
```json
["https://github.com/org/repo/blob/main/runbooks/k8s.md",
 "https://github.com/org/repo/blob/main/runbooks/network.md"]
```

**Response (200, no repo configured)**:
```json
[]
```

The listing result is cached using the same `RunbookCache` (key: repo URL, same TTL).

### Handler

```go
// pkg/api/handler_runbook.go

func (s *Server) handleListRunbooks(c echo.Context) error {
    runbooks, err := s.runbookService.ListRunbooks(c.Request().Context())
    if err != nil {
        slog.Warn("Failed to list runbooks", "error", err)
        return c.JSON(http.StatusOK, []string{}) // Fail-open
    }
    return c.JSON(http.StatusOK, runbooks)
}
```

Registered in `server.go`:
```go
api.GET("/runbooks", s.handleListRunbooks)
```

---

## Integration Points

### 1. Startup Wiring (`cmd/tarsy/main.go`)

```go
// Resolve GitHub token: use configured env var name, default to "GITHUB_TOKEN"
tokenEnv := "GITHUB_TOKEN"
if cfg.GitHub != nil && cfg.GitHub.TokenEnv != "" {
    tokenEnv = cfg.GitHub.TokenEnv
}
githubToken := os.Getenv(tokenEnv)

// Create RunbookService (token already resolved — service receives value, not env var name)
runbookService := runbook.NewRunbookService(cfg.Runbooks, githubToken, cfg.Defaults.Runbook)

// Wire into executor and API server
executor := queue.NewRealSessionExecutor(..., runbookService)
chatExecutor := queue.NewChatMessageExecutor(..., runbookService)
server.SetRunbookService(runbookService)
```

System warning if no GitHub token and `system.runbooks.repo_url` is configured:
```go
if githubToken == "" && cfg.Runbooks != nil && cfg.Runbooks.RepoURL != "" {
    warningsService.AddWarning("runbook", "GitHub token not configured",
        "Set GITHUB_TOKEN (or configure system.github.token_env). URL-based runbooks will fall back to default.", "")
}
```

### 2. Session Executor (`pkg/queue/executor.go`)

Replace the hardcoded default in `executeAgent()`:

```go
// Current (Phase 7):
RunbookContent: config.GetBuiltinConfig().DefaultRunbook,

// New (Phase 8.1):
runbookContent := e.resolveRunbook(ctx, input.session)
// ...
RunbookContent: runbookContent,
```

Add method to `RealSessionExecutor`:

```go
func (e *RealSessionExecutor) resolveRunbook(ctx context.Context, session *ent.AlertSession) string {
    alertURL := ""
    if session.RunbookURL != nil {
        alertURL = *session.RunbookURL
    }
    
    content, err := e.runbookService.Resolve(ctx, alertURL)
    if err != nil {
        slog.Warn("Runbook resolution failed, using default",
            "session_id", session.ID,
            "error", err)
        return e.cfg.Defaults.Runbook
    }
    return content
}
```

### 3. Chat Executor (`pkg/queue/chat_executor.go`)

Same pattern — replace hardcoded default with resolution call.

### 4. API Server (`pkg/api/server.go`)

Add `runbookService` field and setter:

```go
type Server struct {
    // ... existing fields ...
    runbookService *runbook.RunbookService
}

func (s *Server) SetRunbookService(rs *runbook.RunbookService) {
    s.runbookService = rs
}
```

Add to `ValidateWiring()` if runbook listing is required (optional — the endpoint returns `[]` gracefully without it).

### 5. Config Loading (`pkg/config/loader.go`)

Parse the new YAML sections and store on `Config`:

```go
// In load():
var sysYAML *SystemYAMLConfig
if tarsyConfig.System != nil {
    sysYAML = tarsyConfig.System
}

// resolveGitHubConfig and resolveRunbooksConfig handle nil input (all-defaults case)
githubCfg := resolveGitHubConfig(sysYAML)    // applies defaults: TokenEnv = "GITHUB_TOKEN"
runbooksCfg := resolveRunbooksConfig(sysYAML) // applies defaults: CacheTTL = 1m, AllowedDomains = github.com

return &Config{
    // ... existing fields ...
    GitHub:   githubCfg,
    Runbooks: runbooksCfg,
}
```

---

## Dashboard Changes

### ManualAlertForm Updates

Replace the plain text field with an Autocomplete dropdown (matching old TARSy UX):

```tsx
// State
const [availableRunbooks, setAvailableRunbooks] = useState<string[]>([]);
const [runbookUrl, setRunbookUrl] = useState<string>('');
const DEFAULT_RUNBOOK = 'Default Runbook';

// Load available runbooks on mount
useEffect(() => {
  apiClient.getRunbooks()
    .then(urls => setAvailableRunbooks([DEFAULT_RUNBOOK, ...urls]))
    .catch(() => setAvailableRunbooks([DEFAULT_RUNBOOK]));
}, []);

// UI: Autocomplete with freeSolo for custom URLs
<Autocomplete
  freeSolo
  value={runbookUrl}
  onChange={(_, newValue) => setRunbookUrl(newValue || '')}
  options={availableRunbooks}
  renderInput={(params) => (
    <TextField
      {...params}
      label="Runbook"
      helperText="Select from list or enter a custom GitHub URL"
    />
  )}
/>
```

On submit, omit the `runbook` field when the value is `DEFAULT_RUNBOOK` or empty.

### API Client Addition

```typescript
// services/api.ts
async getRunbooks(): Promise<string[]> {
  try {
    const response = await this.client.get<string[]>('/api/v1/runbooks');
    return response.data;
  } catch {
    return [];
  }
}
```

### SessionHeader

The existing runbook URL display in `SessionHeader.tsx` is already functional. No changes needed — it correctly renders `runbook_url` as a link when present.

---

## Error Handling

### Error Categories

| Scenario | Behavior | User Impact |
|----------|----------|-------------|
| GitHub token missing | System warning, URL runbooks degrade to default | Investigation uses generic runbook |
| URL fetch HTTP error (404, 500) | Log error, use default content | Investigation uses generic runbook |
| URL fetch timeout | Log error, use default content | Investigation uses generic runbook |
| Invalid URL format | API returns 400 on submission | Alert rejected |
| Disallowed domain | API returns 400 on submission | Alert rejected |
| Cache miss + fetch success | Transparent, content cached | No impact |
| List runbooks API failure | Return empty array | Dashboard shows no dropdown options |

### System Warnings

| Condition | Warning |
|-----------|---------|
| `GITHUB_TOKEN` env var empty/unset and `system.runbooks.repo_url` is set | "GitHub token not configured. Set GITHUB_TOKEN (or system.github.token_env). URL-based runbooks will fall back to default." |
| `system.runbooks.repo_url` configured but unreachable on first listing attempt | "Cannot reach runbook repository: {error}" |

Warnings use the existing `SystemWarningsService` with category `"runbook"`.

---

## Testing Strategy

### Unit Tests (`pkg/runbook/`)

| Test File | Coverage |
|-----------|----------|
| `service_test.go` | Resolution hierarchy (URL vs default), fail-open on fetch error, caching behavior |
| `github_test.go` | URL conversion, content download (HTTP mock), listing (HTTP mock), auth headers, error handling |
| `cache_test.go` | Set/Get, TTL expiry, concurrent access |
| `url_test.go` | URL validation (schemes, domains), GitHub URL parsing, raw URL conversion |

### Integration Points

- Update `handler_alert_test.go` to test runbook URL validation
- Update executor tests to verify runbook resolution flow
- Add `handler_runbook_test.go` for the listing endpoint

### E2E Test Update

Add runbook resolution to the Pipeline e2e test:
- Submit an alert with a `runbook` URL
- Mock the HTTP response (or use a test file server)
- Verify `RunbookContent` in the prompt contains the fetched content

---

## Implementation Plan

### Step 1: Core Package (`pkg/runbook/`)

1. Create `pkg/runbook/url.go` — URL parsing, conversion, validation
2. Create `pkg/runbook/cache.go` — In-memory TTL cache
3. Create `pkg/runbook/github.go` — GitHub HTTP client (download + list)
4. Create `pkg/runbook/service.go` — RunbookService with Resolve() and ListRunbooks()
5. Unit tests for all of the above

### Step 2: Configuration

1. Create `pkg/config/system.go` with `GitHubConfig` and `RunbookConfig` resolved types
2. Add `SystemYAMLConfig` (containing `GitHubYAMLConfig`, `RunbooksYAMLConfig`) to `TarsyYAMLConfig`
3. Add `GitHub *GitHubConfig` and `Runbooks *RunbookConfig` fields to `Config`
4. Implement `resolveGitHubConfig()` and `resolveRunbooksConfig()` in `load()` with defaults
5. Update config validation

### Step 3: Executor Integration

1. Add `runbookService` field to `RealSessionExecutor`
2. Add `resolveRunbook()` method
3. Replace hardcoded default in `executeAgent()`
4. Same changes in `ChatMessageExecutor`
5. Update constructor signatures

### Step 4: API & Wiring

1. Add `handleListRunbooks` handler
2. Add `SetRunbookService()` on Server
3. Add runbook URL validation to `handleSubmitAlert`
4. Wire everything in `cmd/tarsy/main.go`
5. System warning for missing token

### Step 5: Dashboard

1. Add `getRunbooks()` to API client
2. Update `ManualAlertForm` with Autocomplete dropdown
3. Add "Default Runbook" sentinel value handling

### Step 6: Testing

1. Unit tests for `pkg/runbook/` (already part of Step 1)
2. Integration tests for handlers
3. Update e2e Pipeline test with runbook resolution

---

## Configuration Example

Complete example showing all runbook-related configuration:

```yaml
# deploy/config/tarsy.yaml

# System-wide infrastructure settings
system:
  github:
    token_env: "GITHUB_TOKEN"  # Optional — reads GITHUB_TOKEN by default if omitted
  runbooks:
    repo_url: "https://github.com/myorg/runbooks/tree/main/sre"
    cache_ttl: "1m"
    allowed_domains:
      - "github.com"
      - "raw.githubusercontent.com"

# Default runbook (used when no URL-based runbook is provided with the alert)
defaults:
  runbook: |
    # Company SRE Troubleshooting Guide
    
    ## Investigation Steps
    1. Check monitoring dashboards (Grafana, Prometheus)
    2. Review recent deployments (ArgoCD, Flux)
    3. Analyze logs and metrics
    4. Check for recent infrastructure changes
    
    ## Escalation
    - Slack: #sre-oncall
    - PagerDuty: SRE team

# Chain-specific investigation guidance uses custom_instructions, not runbooks.
# Runbooks are selected per-alert (via API) or default to defaults.runbook.
agent_chains:
  kubernetes-alerts:
    alert_types: ["kubernetes"]
    stages:
      - name: "investigate"
        agents:
          - name: "KubernetesAgent"

# Agent-level custom instructions for chain-specific guidance
agents:
  KubernetesAgent:
    mcp_servers: ["kubernetes-server"]
    custom_instructions: |
      Focus on pod health, resource limits, and recent deployments.
      Check namespace events and node conditions.
```

---

## Dependencies

| Package | Purpose | Notes |
|---------|---------|-------|
| `net/http` (stdlib) | GitHub API requests | No external HTTP library needed |
| `encoding/json` (stdlib) | Parse GitHub API responses | Standard library |
| `net/url` (stdlib) | URL parsing and validation | Standard library |
| `sync` (stdlib) | Cache thread safety | Standard library |

No new external dependencies. The GitHub API interaction is simple enough (2 endpoints: Contents API for listing, raw download for content) that `net/http` is sufficient. This avoids adding `go-github` as a dependency for minimal usage.
