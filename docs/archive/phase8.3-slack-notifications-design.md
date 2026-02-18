# Phase 8.3: Slack Notifications — Detailed Design

## Overview

Add Slack notification support to TARSy so users are informed in Slack when sessions start, complete, fail, or time out. Preserves all features from old TARSy (`/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/slack_service.py`) while improving the architecture: Block Kit instead of legacy attachments, YAML-based configuration, proper Go SDK, and a clean integration point in the worker lifecycle.

### Goals

1. **Notify on terminal session status** — completed, failed, timed_out, cancelled
2. **Notify on session start** — only for Slack-originated alerts (those with a fingerprint)
3. **Thread replies** — when `slack_message_fingerprint` is provided, reply in the original alert's thread
4. **Dashboard link** — every notification includes a link to the session detail page
5. **Fail-open** — Slack API failures are logged but never block session processing
6. **Configurable** — enable/disable via YAML config, secrets via env vars

### Non-Goals

- Interactive Slack elements (buttons, actions) — deferred
- Multi-channel routing (per-chain or per-alert-type channels) — deferred
- Slack-initiated alert submission (slash commands, event subscriptions) — deferred
- Message updates (editing in-place) — each lifecycle event posts a new message

---

## Architecture

### Package Layout

```
pkg/slack/
├── service.go    # SlackService — main notification orchestrator
├── client.go     # SlackClient — thin wrapper around slack-go SDK
├── message.go    # Block Kit message builders (formatting, templates)
└── fingerprint.go # Fingerprint-based thread resolution
```

### Component Diagram

```
Worker (worker.go)
  │
  ├─ updateSessionTerminalStatus()
  ├─ publishSessionStatus()        ← existing WebSocket publish
  └─ notifySlack()                 ← NEW: calls SlackService
       │
       ▼
SlackService (pkg/slack/service.go)
  │
  ├─ resolveThread()  ← fingerprint lookup via conversations.history
  │
  └─ SlackClient.PostMessage()  ← slack-go SDK
       │
       ▼
  Slack Web API (chat.postMessage)
```

### Integration Point

Notifications are triggered from `Worker.pollAndProcess()` in `pkg/queue/worker.go`, immediately after the terminal session status is published via WebSocket (line 228). This is the single chokepoint where all session outcomes converge.

**Start notifications** are triggered right after the session is claimed and status is set to `in_progress` (line 160), but only when a `slack_message_fingerprint` is present.

```
// In pollAndProcess():

// Publish session status "in_progress"
w.publishSessionStatus(ctx, session.ID, alertsession.StatusInProgress)

// NEW: Send start notification (only if fingerprint present, resolves threadTS)
slackThreadTS := w.notifySlackStart(ctx, session)

// ... execution ...

// 10a. Publish terminal session status event
w.publishSessionStatus(context.Background(), session.ID, result.Status)

// NEW: Send terminal notification (reuses cached threadTS)
w.notifySlackTerminal(context.Background(), session, result, slackThreadTS)
```

---

## Interfaces & Types

### SlackService (`pkg/slack/service.go`)

```go
// Service handles Slack notification delivery.
// Thread-safe. Nil-safe: all methods are no-ops when service is nil.
type Service struct {
    client       *Client
    cfg          *Config
    dashboardURL string
    logger       *slog.Logger
}

// NewService creates a new Slack notification service.
// Returns nil if Slack is not configured (enabled=false or missing token/channel).
func NewService(cfg *Config) *Service

// NotifySessionStarted sends a "processing started" notification.
// Only sends if fingerprint is present (Slack-originated alerts).
// Returns resolved threadTS for reuse by terminal notification.
// Fail-open: errors are logged, never returned.
func (s *Service) NotifySessionStarted(ctx context.Context, input SessionStartedInput) string

// NotifySessionCompleted sends a terminal status notification.
// Fail-open: errors are logged, never returned.
func (s *Service) NotifySessionCompleted(ctx context.Context, input SessionCompletedInput)
```

Nil-receiver safety pattern (same as `MaskingService`):
```go
func (s *Service) NotifySessionStarted(ctx context.Context, input SessionStartedInput) string {
    if s == nil {
        return ""
    }
    // ...
}
```

### Input Types

```go
// SessionStartedInput contains data for a session start notification.
type SessionStartedInput struct {
    SessionID              string
    AlertType              string
    SlackMessageFingerprint string
}

// SessionCompletedInput contains data for a terminal session notification.
type SessionCompletedInput struct {
    SessionID              string
    AlertType              string
    Status                 string // completed, failed, timed_out, cancelled
    ExecutiveSummary       string // For completed sessions (preferred content)
    FinalAnalysis          string // Fallback if executive summary is empty
    ErrorMessage           string // For failed/timed_out sessions
    SlackMessageFingerprint string
    ThreadTS               string // Cached from start notification (avoids duplicate lookup)
}
```

### SlackClient (`pkg/slack/client.go`)

```go
// Client is a thin wrapper around the slack-go SDK.
type Client struct {
    api       *slack.Client
    channelID string
    logger    *slog.Logger
}

// NewClient creates a new Slack API client.
func NewClient(token, channelID string) *Client

// PostMessage sends a message to the configured channel.
// If threadTS is non-empty, the message is posted as a threaded reply.
// timeout bounds the Slack API call (5s for start, 10s for terminal).
func (c *Client) PostMessage(ctx context.Context, blocks []slack.Block, threadTS string, timeout time.Duration) error

// FindMessageByFingerprint searches recent channel history for a message
// containing the given fingerprint text. Returns the message timestamp (ts)
// for threading, or empty string if not found.
func (c *Client) FindMessageByFingerprint(ctx context.Context, fingerprint string) (string, error)
```

### Config (`pkg/slack/config.go` — types defined in `pkg/config/`)

```go
// SlackConfig holds resolved Slack notification configuration.
// Defined in pkg/config/system.go alongside GitHubConfig and RunbookConfig.
type SlackConfig struct {
    Enabled  bool
    TokenEnv string // Env var name for Slack bot token (default: "SLACK_BOT_TOKEN")
    Channel  string // Slack channel ID (e.g., "C12345678")
}
```

Dashboard URL lives at `system.dashboard_url` (shared across Slack, CORS, future OAuth redirects):

```go
// In Config struct (pkg/config/config.go)
DashboardURL string // Base URL for dashboard links (default: "http://localhost:8080")
```

---

## Configuration

### YAML Structure

Added under `system.slack` in `tarsy.yaml`, following the existing `system.github` / `system.runbooks` pattern. Dashboard URL is at `system.dashboard_url` (shared, not Slack-specific):

```yaml
system:
  dashboard_url: "https://tarsy.example.com"  # Default: http://localhost:8080
  github:
    token_env: "GITHUB_TOKEN"
  runbooks:
    repo_url: "https://github.com/org/repo/tree/main/runbooks"
  slack:
    enabled: true
    token_env: "SLACK_BOT_TOKEN"      # Env var name (default: "SLACK_BOT_TOKEN")
    channel: "C12345678"               # Channel ID or name
```

### YAML Config Types (`pkg/config/loader.go`)

```go
// SlackYAMLConfig holds Slack notification settings from YAML.
type SlackYAMLConfig struct {
    Enabled  *bool  `yaml:"enabled,omitempty"`   // Default: false
    TokenEnv string `yaml:"token_env,omitempty"` // Default: "SLACK_BOT_TOKEN"
    Channel  string `yaml:"channel,omitempty"`   // Channel ID
}
```

Added to `SystemYAMLConfig`:

```go
type SystemYAMLConfig struct {
    DashboardURL string              `yaml:"dashboard_url"` // NEW: shared
    GitHub       *GitHubYAMLConfig   `yaml:"github"`
    Runbooks     *RunbooksYAMLConfig `yaml:"runbooks"`
    Slack        *SlackYAMLConfig    `yaml:"slack"`         // NEW
}
```

### Resolved Config Type (`pkg/config/system.go`)

```go
// SlackConfig holds resolved Slack notification configuration.
type SlackConfig struct {
    Enabled  bool
    TokenEnv string
    Channel  string
}
```

Added to `Config`:

```go
type Config struct {
    // ... existing fields ...
    DashboardURL string       // NEW: shared dashboard URL (default: "http://localhost:8080")
    Slack        *SlackConfig // NEW: Slack notification configuration
}
```

### Resolution (`pkg/config/loader.go`)

```go
func resolveSlackConfig(sys *SystemYAMLConfig) *SlackConfig {
    cfg := &SlackConfig{
        Enabled:  false,
        TokenEnv: "SLACK_BOT_TOKEN",
    }

    if sys == nil || sys.Slack == nil {
        return cfg
    }

    s := sys.Slack
    if s.Enabled != nil {
        cfg.Enabled = *s.Enabled
    }
    if s.TokenEnv != "" {
        cfg.TokenEnv = s.TokenEnv
    }
    if s.Channel != "" {
        cfg.Channel = s.Channel
    }

    return cfg
}

func resolveDashboardURL(sys *SystemYAMLConfig) string {
    if sys != nil && sys.DashboardURL != "" {
        return sys.DashboardURL
    }
    return "http://localhost:8080"
}
```

### Validation (`pkg/config/validator.go`)

```go
func (v *Validator) validateSlack() error {
    s := v.cfg.Slack
    if s == nil || !s.Enabled {
        return nil
    }

    if s.Channel == "" {
        return fmt.Errorf("system.slack.channel is required when Slack is enabled")
    }

    // Validate token env var exists at startup (eager validation)
    if token := os.Getenv(s.TokenEnv); token == "" {
        return fmt.Errorf("system.slack.token_env: environment variable %s is not set", s.TokenEnv)
    }

    return nil
}
```

### Environment Variables

```bash
# In .env
SLACK_BOT_TOKEN=xoxb-your-slack-bot-token
```

Token is read at service creation time from the env var specified by `token_env`.

---

## Message Formatting

### Block Kit Templates

New TARSy uses Slack Block Kit (structured blocks) instead of old TARSy's legacy attachments. Block Kit is Slack's current recommended approach and provides richer formatting.

#### Session Started (only when fingerprint present)

```json
{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": ":arrows_counterclockwise: *Processing started* — this may take a few minutes.\n<https://tarsy.example.com/sessions/abc-123|View in Dashboard>"
      }
    }
  ]
}
```

Color: No color bar with Block Kit. Status conveyed via emoji.

#### Session Completed

```json
{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": ":white_check_mark: *Analysis Complete*"
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "<executive summary content, truncated to ~2900 chars>"
      }
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "View Full Analysis" },
          "url": "https://tarsy.example.com/sessions/abc-123"
        }
      ]
    }
  ]
}
```

#### Session Failed / Timed Out / Cancelled

```json
{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": ":x: *Analysis Failed*\n\n*Error:*\n<error message>"
      }
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": { "type": "plain_text", "text": "View Details" },
          "url": "https://tarsy.example.com/sessions/abc-123"
        }
      ]
    }
  ]
}
```

Status-to-emoji mapping:
| Status | Emoji | Label |
|--------|-------|-------|
| `completed` | `:white_check_mark:` | Analysis Complete |
| `failed` | `:x:` | Analysis Failed |
| `timed_out` | `:hourglass:` | Analysis Timed Out |
| `cancelled` | `:no_entry_sign:` | Analysis Cancelled |

### Message Builder (`pkg/slack/message.go`)

```go
// BuildStartedMessage creates Block Kit blocks for a session start notification.
func BuildStartedMessage(sessionID, dashboardURL string) []slack.Block

// BuildTerminalMessage creates Block Kit blocks for a terminal session notification.
// For completed sessions: uses ExecutiveSummary; falls back to FinalAnalysis if empty.
func BuildTerminalMessage(input SessionCompletedInput, dashboardURL string) []slack.Block
```

### Content Selection (Q6)

For completed sessions, content priority:
1. `ExecutiveSummary` (preferred — designed for brevity)
2. `FinalAnalysis` (fallback — when executive summary generation failed)
3. Empty → show only status + dashboard link

### Content Truncation

Slack messages have a ~3000 character limit per text block. Executive summaries and error messages are truncated with `"... (truncated)"` suffix if they exceed the limit.

```go
const maxBlockTextLength = 2900 // Leave margin for formatting

func truncateForSlack(text string) string {
    if len(text) <= maxBlockTextLength {
        return text
    }
    return text[:maxBlockTextLength] + "\n\n_... (truncated — view full analysis in dashboard)_"
}
```

---

## Threading / Fingerprinting

### How It Works

Threading is preserved from old TARSy. When a `slack_message_fingerprint` is provided with an alert:

1. **Alert submission**: `POST /api/v1/alerts` with `slack_message_fingerprint` field → stored on `AlertSession`
2. **Session start**: If fingerprint present, `FindMessageByFingerprint()` searches last 24h of channel history (up to 50 messages), finds the message containing the fingerprint, extracts its `ts`
3. **Threaded reply**: `PostMessage()` with `thread_ts` set to the found message's `ts`
4. **All subsequent notifications** (completion, failure) also thread to the same parent

### Fingerprint Lookup

Identical matching logic to old TARSy:
- Case-insensitive comparison
- Whitespace normalization (collapse multiple spaces/newlines/tabs)
- Searches message text AND attachment text/fallback fields
- 24-hour lookback window, 50-message limit

```go
// FindMessageByFingerprint searches channel history for a message containing
// the fingerprint string. Returns the message ts for threading.
func (c *Client) FindMessageByFingerprint(ctx context.Context, fingerprint string) (string, error) {
    oldest := fmt.Sprintf("%d", time.Now().Add(-24*time.Hour).Unix())

    params := &slack.GetConversationHistoryParameters{
        ChannelID: c.channelID,
        Oldest:    oldest,
        Limit:     50,
    }
    history, err := c.api.GetConversationHistoryContext(ctx, params)
    if err != nil {
        return "", fmt.Errorf("conversations.history failed: %w", err)
    }

    normalizedFingerprint := normalizeText(fingerprint)
    for _, msg := range history.Messages {
        text := collectMessageText(msg)
        if strings.Contains(normalizeText(text), normalizedFingerprint) {
            return msg.Timestamp, nil
        }
    }
    return "", nil // Not found — will post to channel directly
}
```

### Thread TS Caching

To avoid redundant `conversations.history` API calls (one for start, one for terminal), the resolved `thread_ts` is cached in-memory during the worker's `pollAndProcess()` lifecycle:

```go
// In pollAndProcess():
var slackThreadTS string

// Start notification (resolves thread_ts if fingerprint present)
slackThreadTS = w.notifySlackStart(ctx, session)

// ... execution ...

// Terminal notification (reuses cached thread_ts)
w.notifySlackTerminal(context.Background(), session, result, slackThreadTS)
```

### Fallback Behavior

If fingerprint is provided but no matching message is found:
- Log a warning
- Post to channel directly (not threaded) — same as old TARSy behavior

If no fingerprint is provided:
- Post to channel directly (not threaded)
- Skip start notification (only terminal notification sent)

---

## API Changes

### Alert Submission Request

Add `slack_message_fingerprint` to `SubmitAlertRequest`:

```go
// pkg/api/requests.go
type SubmitAlertRequest struct {
    AlertType              string                     `json:"alert_type"`
    Runbook                string                     `json:"runbook,omitempty"`
    Data                   string                     `json:"data"`
    MCP                    *models.MCPSelectionConfig `json:"mcp,omitempty"`
    SlackMessageFingerprint string                    `json:"slack_message_fingerprint,omitempty"`
}
```

### Alert Service Input

```go
// pkg/services/alert_service.go
type SubmitAlertInput struct {
    AlertType              string
    Runbook                string
    Data                   string
    MCP                    *models.MCPSelectionConfig
    Author                 string
    SlackMessageFingerprint string  // NEW
}
```

### Handler Change

```go
// In submitAlertHandler, add to input transformation:
input := services.SubmitAlertInput{
    AlertType:              req.AlertType,
    Runbook:                req.Runbook,
    Data:                   req.Data,
    MCP:                    req.MCP,
    Author:                 extractAuthor(c),
    SlackMessageFingerprint: req.SlackMessageFingerprint,  // NEW
}
```

### Alert Service Change

```go
// In SubmitAlert, add to builder:
if input.SlackMessageFingerprint != "" {
    builder.SetSlackMessageFingerprint(input.SlackMessageFingerprint)
}
```

### Dashboard Change

Add `slack_message_fingerprint` field to `ManualAlertForm.tsx` under "Advanced Options" — same pattern as old TARSy dashboard with expandable section and text field.

---

## Startup Wiring (`cmd/tarsy/main.go`)

```go
// After resolving config, before creating worker pool:

// 5d. Create Slack notification service (optional)
var slackService *slack.Service
if cfg.Slack != nil && cfg.Slack.Enabled {
    slackToken := os.Getenv(cfg.Slack.TokenEnv)
    slackService = slack.NewService(slack.ServiceConfig{
        Token:        slackToken,
        Channel:      cfg.Slack.Channel,
        DashboardURL: cfg.DashboardURL,
    })
    slog.Info("Slack notifications enabled", "channel", cfg.Slack.Channel)
} else {
    slog.Info("Slack notifications disabled")
}

// Pass to worker pool
workerPool := queue.NewWorkerPool(podID, dbClient.Client, cfg.Queue, executor, eventPublisher, slackService)
```

### Worker Pool Threading

```go
// pkg/queue/pool.go — add slackService field
type WorkerPool struct {
    // ... existing fields ...
    slackService *slack.Service  // Optional: nil = no Slack notifications
}

// NewWorkerPool — add slackService parameter
func NewWorkerPool(podID string, client *ent.Client, cfg *config.QueueConfig,
    executor SessionExecutor, eventPublisher agent.EventPublisher,
    slackService *slack.Service) *WorkerPool
```

```go
// pkg/queue/worker.go — add slackService field
type Worker struct {
    // ... existing fields ...
    slackService *slack.Service
}
```

### System Warning

If Slack is configured but the token env var is empty at runtime (validation catches this at startup):

```go
if cfg.Slack != nil && cfg.Slack.Enabled {
    if os.Getenv(cfg.Slack.TokenEnv) == "" {
        warningsService.AddWarning("slack", "Slack bot token not configured",
            "Set "+cfg.Slack.TokenEnv+" to enable Slack notifications.", "")
    }
}
```

Note: With eager validation in `validateSlack()`, this warning path is only reachable if validation is relaxed in the future. Included for defense-in-depth, consistent with the runbook pattern.

---

## Error Handling

### Fail-Open Policy

All Slack notification failures are logged and swallowed. Session processing is never blocked by Slack.

```go
func (w *Worker) notifySlackTerminal(ctx context.Context, session *ent.AlertSession,
    result *ExecutionResult, threadTS string) {

    if w.slackService == nil {
        return
    }

    w.slackService.NotifySessionCompleted(ctx, slack.SessionCompletedInput{
        SessionID:              session.ID,
        AlertType:              session.AlertType,
        Status:                 string(result.Status),
        ExecutiveSummary:       result.ExecutiveSummary,
        FinalAnalysis:          result.FinalAnalysis,
        ErrorMessage:           errorString(result.Error),
        SlackMessageFingerprint: derefString(session.SlackMessageFingerprint),
        ThreadTS:               threadTS,
    })
}
```

Inside `SlackService.NotifySessionCompleted()`:

```go
func (s *Service) NotifySessionCompleted(ctx context.Context, input SessionCompletedInput) {
    if s == nil {
        return
    }

    // Reuse cached threadTS from start notification; fall back to lookup if needed
    threadTS := input.ThreadTS
    if threadTS == "" && input.SlackMessageFingerprint != "" {
        var err error
        threadTS, err = s.client.FindMessageByFingerprint(ctx, input.SlackMessageFingerprint)
        if err != nil {
            s.logger.Warn("Failed to find Slack thread for fingerprint",
                "session_id", input.SessionID,
                "fingerprint", input.SlackMessageFingerprint,
                "error", err)
        }
    }

    blocks := BuildTerminalMessage(input, s.dashboardURL)
    if err := s.client.PostMessage(ctx, blocks, threadTS, 10*time.Second); err != nil {
        s.logger.Error("Failed to send Slack notification",
            "session_id", input.SessionID,
            "status", input.Status,
            "error", err)
    }
}
```

### Timeout

Slack API calls use per-request timeouts (separate from the session context):

- **Start notification**: 5-second timeout — happens before execution, must not delay investigation
- **Terminal notification**: 10-second timeout — happens after session is complete, no execution impact

```go
func (c *Client) PostMessage(ctx context.Context, blocks []slack.Block, threadTS string, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    // ...
}
```

---

## Dependencies

### Go SDK

```
github.com/slack-go/slack
```

The `slack-go/slack` package is the standard Go SDK for Slack. It provides:
- `chat.postMessage` via `PostMessageContext()`
- `conversations.history` via `GetConversationHistoryContext()`
- Block Kit types (`SectionBlock`, `ActionsBlock`, `ButtonBlockElement`, etc.)
- Context-aware API methods

### Slack Bot Scopes Required

| Scope | Purpose |
|-------|---------|
| `channels:history` | Read channel messages (fingerprint search) |
| `groups:history` | Read private channel messages |
| `chat:write` | Post messages and replies |
| `channels:read` | View channel info (optional, for validation) |
| `groups:read` | View private channel info (optional) |

Same scopes as old TARSy.

---

## Testing Strategy

### Unit Tests (`pkg/slack/`)

- **`service_test.go`**: Test `NotifySessionStarted` / `NotifySessionCompleted` with mock client
  - Nil receiver is no-op
  - Fingerprint present → thread resolution called
  - Fingerprint missing → no thread resolution
  - Client error → logged, no panic
- **`message_test.go`**: Test Block Kit message building
  - Completed message includes executive summary
  - Failed message includes error
  - Truncation at max length
  - Dashboard URL construction
- **`fingerprint_test.go`**: Test fingerprint matching
  - Case-insensitive
  - Whitespace normalization
  - Match in message text
  - Match in attachment text
  - No match returns empty string
- **`client_test.go`**: Test client wrapper (mock Slack HTTP server or interface mock)

### Testing the Worker

The worker takes `*slack.Service` directly (Q2). For unit testing, define a test-only interface in the test file:

```go
// In worker_test.go — test-only interface for mocking
type slackNotifier interface {
    NotifySessionStarted(ctx context.Context, input slack.SessionStartedInput) string
    NotifySessionCompleted(ctx context.Context, input slack.SessionCompletedInput)
}
```

Alternatively, pass a `*slack.Service` backed by a mock `Client` (the `Client` struct wraps the SDK and is easily mockable via an HTTP test server).

### E2E Tests

Add a `Slack` scenario to `test/e2e/` that:
1. Submits an alert with `slack_message_fingerprint`
2. Verifies the fingerprint is stored on the session
3. Verifies the Slack service is called with correct inputs (mock Slack service injected via the interface)

No real Slack API calls in e2e tests — use a mock that records calls.

### Integration Notes

- Config validation tests: enabled with missing channel → error, enabled with missing token → error
- Builder tests in `alert_service_test.go`: fingerprint passed through to DB

---

## Implementation Plan

### Step 1: Configuration (config changes)

1. Add `DashboardURL` to `SystemYAMLConfig` and `Config` struct (shared, default `http://localhost:8080`)
2. Add `resolveDashboardURL()` in `loader.go`
3. Add `SlackYAMLConfig` to `pkg/config/loader.go`
4. Add `SlackConfig` to `pkg/config/system.go`
5. Add `Slack` field to `Config` struct
6. Add `resolveSlackConfig()` in `loader.go`
7. Add `validateSlack()` in `validator.go`
8. Update `tarsy.yaml.example` with `dashboard_url` and Slack section
9. Update `.env.example` with updated Slack comments

### Step 2: API Changes (fingerprint plumbing)

1. Add `SlackMessageFingerprint` to `SubmitAlertRequest` in `requests.go`
2. Add `SlackMessageFingerprint` to `SubmitAlertInput` in `alert_service.go`
3. Pass through in `submitAlertHandler` (handler_alert.go)
4. Set on builder in `SubmitAlert()` (alert_service.go)
5. Unit test: fingerprint flows from API → DB

### Step 3: Slack Package (`pkg/slack/`)

1. `client.go` — Slack API wrapper (PostMessage, FindMessageByFingerprint)
2. `message.go` — Block Kit message builders
3. `fingerprint.go` — Text normalization and matching helpers
4. `service.go` — Service orchestrator (nil-safe, fail-open)
5. Unit tests for all files

### Step 4: Worker Integration

1. Add `slackService *slack.Service` field to `Worker` and `WorkerPool`
2. Add `notifySlackStart()` (returns `threadTS string`) and `notifySlackTerminal()` methods to `Worker`
3. Wire in `pollAndProcess()`: start after `publishSessionStatus(in_progress)`, terminal after `publishSessionStatus(terminal)`
4. Thread `slackService` from `main.go` → `NewWorkerPool()` → `NewWorker()`

### Step 5: Startup Wiring

1. Create `slack.Service` in `main.go` (conditional on config)
2. Pass to `NewWorkerPool()`
3. Add system warning for misconfiguration

### Step 6: Dashboard Changes

1. Add `slack_message_fingerprint` field to `ManualAlertForm.tsx`
2. Add to submit payload construction

### Step 7: E2E Test

1. Add mock Slack notifier for e2e harness
2. Add Slack notification scenario

---

## Config Example (Complete)

```yaml
# tarsy.yaml
system:
  dashboard_url: "https://tarsy.example.com"  # Default: http://localhost:8080
  github:
    token_env: "GITHUB_TOKEN"
  runbooks:
    repo_url: "https://github.com/org/repo/tree/main/runbooks"
  slack:
    enabled: true
    token_env: "SLACK_BOT_TOKEN"
    channel: "C12345678"
```

```bash
# .env
SLACK_BOT_TOKEN=xoxb-your-slack-bot-token
```

---

## Deferred Items

- **Per-chain channel routing**: Different chains send to different channels
- **Interactive elements**: Buttons that trigger actions (cancel, rerun)
- **Slack-initiated alerts**: Slash commands or event subscriptions
- **Message updates**: Edit messages in-place instead of posting new ones
- **Rich alert context**: Include alert type, chain ID, author in notification
- **Rate limiting**: Explicit Slack API rate limit handling (slack-go handles 429 retries internally)
