# Phase 11: Monitoring & Operations — Detailed Design

## Scope

Phase 11 covers four items from the project plan:

1. **Structured logging** — Configurable log level and output format
2. **Retention policies** — YAML configuration for session and event retention
3. **Cleanup service** — Background goroutine that enforces retention policies
4. **Cascade deletes** — Already implemented (no work needed)

---

## What's Already Implemented

Before designing new work, here's the inventory of existing pieces:

### Cascade Deletes — Complete

All Ent schema edges have `ON DELETE CASCADE` annotations, and the SQL migration defines matching FK constraints. Deleting an `AlertSession` cascades to all children: stages, agent_executions, timeline_events, messages, llm_interactions, mcp_interactions, events, chats, chat_user_messages.

**No work needed.**

### Soft Delete Infrastructure — Complete (needs minor update)

- `AlertSession.deleted_at` field (optional, nillable, partial index)
- `SessionService.SoftDeleteOldSessions(ctx, retentionDays)` — sets `deleted_at` on completed sessions older than cutoff
- `SessionService.RestoreSession(ctx, sessionID)` — clears `deleted_at`
- All queries filter on `DeletedAtIsNil()` (dashboard, search, workers, orphan detection)

**Implemented and tested, but never called from a scheduler. Needs update to also soft-delete old `pending` sessions (see Q5).**

### Event Cleanup Infrastructure — Complete (needs minor update)

- `EventService.CleanupOrphanedEvents(ctx, ttlDays int)` — deletes Event rows older than TTL
- `Worker.scheduleEventCleanup(sessionID)` — 60s delayed cleanup after session completes
- `ChatMessageExecutor.scheduleStageEventCleanup(stageID, cutoff)` — 60s delayed cleanup after chat stage

**TTL cleanup implemented and tested, but never called from a scheduler. Per-session cleanup already active. Signature changes from `ttlDays int` to `ttl time.Duration` (event TTL is now 1 hour, not days).**

### Orphan Detection — Complete

- `WorkerPool.runOrphanDetection(ctx)` — ticker-based goroutine (default 5 min)
- `CleanupStartupOrphans(ctx, client, podID)` — one-time cleanup on startup

**Fully operational.**

### Structured Logging — Partially Complete

- All code uses `log/slog` (Go 1.21+ structured logger). Zero `log.Printf` or `fmt.Printf` usage.
- Key-value pairs used consistently (`slog.Info("msg", "key", value)`)

**Missing: configurable log level, JSON output format for containers.**

---

## New Implementation

### 1. Structured Logging Configuration

#### Current State

The codebase uses `slog` with the default handler (text format, info level, stderr). No explicit handler setup exists.

#### Design

Add a `configureLogging()` function in `cmd/tarsy/main.go` called before any other initialization. Two settings:

| Setting | Source | Default |
|---------|--------|---------|
| Log level | `LOG_LEVEL` env var | `info` |
| Log format | `LOG_FORMAT` env var | `text` |

**Why env vars instead of YAML:** Logging must be configured before config loading (config loader itself logs). Env vars are the standard approach for container logging.

#### Implementation

```go
// cmd/tarsy/main.go

func configureLogging() {
    level := parseLogLevel(getEnv("LOG_LEVEL", "info"))

    var handler slog.Handler
    opts := &slog.HandlerOptions{Level: level}

    switch getEnv("LOG_FORMAT", "text") {
    case "json":
        handler = slog.NewJSONHandler(os.Stderr, opts)
    default:
        handler = slog.NewTextHandler(os.Stderr, opts)
    }

    slog.SetDefault(slog.New(handler))
}

func parseLogLevel(s string) slog.Level {
    switch strings.ToLower(s) {
    case "debug":
        return slog.LevelDebug
    case "warn", "warning":
        return slog.LevelWarn
    case "error":
        return slog.LevelError
    default:
        return slog.LevelInfo
    }
}
```

Called as the first line of `main()`, before flag parsing or config loading.

#### Container Defaults

In the Kubernetes Deployment manifest, set `LOG_FORMAT=json` as an env var on the tarsy container. Local development uses the default `text` format.

---

### 2. Retention Policy Configuration

#### YAML Schema

Add a `retention` section under `system` in `tarsy.yaml`:

```yaml
system:
  retention:
    session_retention_days: 365      # Soft-delete completed sessions older than N days
    event_ttl: 1h                    # Delete orphaned Event rows older than this
    cleanup_interval: 12h            # How often the cleanup loop runs
```

All values have defaults (see below) and the entire `retention` section is optional.

#### Config Types

```go
// pkg/config/retention.go

type RetentionConfig struct {
    SessionRetentionDays int           `yaml:"session_retention_days"`
    EventTTL             time.Duration `yaml:"event_ttl"`
    CleanupInterval      time.Duration `yaml:"cleanup_interval"`
}

func DefaultRetentionConfig() *RetentionConfig {
    return &RetentionConfig{
        SessionRetentionDays: 365,
        EventTTL:             1 * time.Hour,
        CleanupInterval:      12 * time.Hour,
    }
}
```

#### YAML Loader Changes

Add `Retention *RetentionYAMLConfig` to `SystemYAMLConfig`. In `load()`, resolve with defaults (same pattern as `QueueConfig`).

Add `Retention *RetentionConfig` to the top-level `Config` struct.

---

### 3. Cleanup Service

#### Architecture

A standalone `CleanupService` in `pkg/cleanup/` with the same lifecycle pattern as `HealthMonitor`: `Start(ctx)` launches a background goroutine, `Stop()` signals it to exit.

The service is intentionally separate from `WorkerPool` — retention cleanup is a system maintenance concern, not a session processing concern. It runs the same way whether the worker pool has 1 worker or 50.

#### Service Design

```go
// pkg/cleanup/service.go

type CleanupService struct {
    config         *config.RetentionConfig
    sessionService *services.SessionService
    eventService   *services.EventService
    stopCh         chan struct{}
    done           chan struct{}
}

func NewCleanupService(
    cfg *config.RetentionConfig,
    sessionService *services.SessionService,
    eventService *services.EventService,
) *CleanupService

func (s *CleanupService) Start(ctx context.Context)
func (s *CleanupService) Stop()
```

#### Cleanup Loop

```go
func (s *CleanupService) run(ctx context.Context) {
    defer close(s.done)

    // Run once at startup (catch up after downtime)
    s.runAll(ctx)

    ticker := time.NewTicker(s.config.CleanupInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-s.stopCh:
            return
        case <-ticker.C:
            s.runAll(ctx)
        }
    }
}

func (s *CleanupService) runAll(ctx context.Context) {
    s.softDeleteOldSessions(ctx)
    s.cleanupOrphanedEvents(ctx)
}
```

#### Two Cleanup Operations

**1. Soft-delete old sessions** — calls existing `SessionService.SoftDeleteOldSessions()`:

```go
func (s *CleanupService) softDeleteOldSessions(ctx context.Context) {
    count, err := s.sessionService.SoftDeleteOldSessions(ctx, s.config.SessionRetentionDays)
    if err != nil {
        slog.Error("Retention: soft-delete sessions failed", "error", err)
        return
    }
    if count > 0 {
        slog.Info("Retention: soft-deleted old sessions", "count", count)
    }
}
```

**Note:** `SoftDeleteOldSessions` currently only targets sessions with `completed_at < cutoff`. Per Q5, update to also soft-delete `pending` sessions where `created_at < cutoff` — a safety net for sessions that were never claimed (e.g., submitted while all workers were down). Orphan detection only covers `in_progress` sessions with stale heartbeats.

**2. Cleanup orphaned events** — calls `EventService.CleanupOrphanedEvents()` (signature updated from `ttlDays int` to `ttl time.Duration`):

```go
func (s *CleanupService) cleanupOrphanedEvents(ctx context.Context) {
    count, err := s.eventService.CleanupOrphanedEvents(ctx, s.config.EventTTL)
    if err != nil {
        slog.Error("Retention: event cleanup failed", "error", err)
        return
    }
    if count > 0 {
        slog.Info("Retention: cleaned up orphaned events", "count", count)
    }
}
```

#### Idempotency

All operations are safe to run from multiple pods simultaneously:

- Soft delete: `WHERE deleted_at IS NULL AND (completed_at < cutoff OR (status = pending AND created_at < cutoff))` — setting `deleted_at` on an already-soft-deleted row is a no-op (filtered out)
- Event cleanup: `WHERE created_at < cutoff` — DELETE is idempotent

No distributed locking needed. Worst case: two pods run cleanup at the same time, one deletes N rows, the other deletes 0.

---

### 4. Wiring in `main.go`

The cleanup service slots into the existing startup sequence after services are created (step 4) and before the HTTP server (step 7). Shutdown happens after the worker pool stops.

```go
// After step 4 (services initialized):

// 4a. Start cleanup service (retention + event TTL)
cleanupService := cleanup.NewCleanupService(cfg.Retention, sessionService, eventService)
cleanupService.Start(ctx)
defer cleanupService.Stop()
slog.Info("Cleanup service started",
    "session_retention_days", cfg.Retention.SessionRetentionDays,
    "event_ttl", cfg.Retention.EventTTL,
    "interval", cfg.Retention.CleanupInterval)
```

---

## Comparison with Old TARSy

| Feature | Old TARSy | New TARSy (after Phase 11) |
|---------|-----------|---------------------------|
| Retention delete type | Hard delete only | Soft delete (hidden from queries, child rows preserved) |
| Restore capability | No | Yes (`RestoreSession` clears `deleted_at`) |
| Session retention default | 365 days | 365 days |
| Event retention | 24 hours | 1 hour (safety net; per-session cleanup handles normal case) |
| Cleanup interval | 12 hours (history) + 6 hours (events) | 12 hours (unified) |
| Orphan cleanup | In same service | Already separate (WorkerPool) |
| Cascade deletes | Added via migration | Built into schema from day 1 |
| Structured logging | Python `logging` | Go `slog` with configurable level + format |
| Log format | Text | Configurable (text/JSON) |

---

## Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `pkg/cleanup/service.go` | CleanupService (background goroutine) |
| `pkg/cleanup/service_test.go` | Unit tests |
| `pkg/config/retention.go` | RetentionConfig type + defaults |

### Modified Files

| File | Change |
|------|--------|
| `cmd/tarsy/main.go` | Add `configureLogging()`, create + start CleanupService |
| `pkg/config/loader.go` | Add `Retention` to SystemYAMLConfig, resolve in `load()` |
| `pkg/config/config.go` | Add `Retention *RetentionConfig` field |
| `pkg/services/session_service.go` | Update `SoftDeleteOldSessions` to also cover old `pending` sessions (Q5) |
| `pkg/services/event_service.go` | Change `CleanupOrphanedEvents` signature from `ttlDays int` to `ttl time.Duration` |
| `deploy/config/tarsy.yaml.example` | Add `retention` config section |

### Config Example Addition

```yaml
system:
  # Data retention and cleanup (all values are defaults — override as needed)
  retention:
    session_retention_days: 365      # Soft-delete completed sessions older than N days
    event_ttl: 1h                    # Delete orphaned Event rows older than this
    cleanup_interval: 12h            # How often the cleanup loop runs
```

### Container Env Vars

```yaml
# In Kubernetes Deployment / podman-compose
env:
  - name: LOG_LEVEL
    value: "info"      # debug | info | warn | error
  - name: LOG_FORMAT
    value: "json"      # json | text
```

---

## Implementation Plan

| Step | Task | Verify |
|------|------|--------|
| 1 | Add `configureLogging()` to `main.go` | `LOG_LEVEL=debug LOG_FORMAT=json go run ./cmd/tarsy` produces JSON output |
| 2 | Add `RetentionConfig` type + defaults in `pkg/config/retention.go` | Unit test: defaults are correct |
| 3 | Wire retention config in loader + Config struct | Config loads with/without `retention` section |
| 4 | Update `SoftDeleteOldSessions` to also cover old `pending` sessions (Q5) | Unit test: old pending sessions are soft-deleted |
| 5 | Update `CleanupOrphanedEvents` signature from `ttlDays int` to `ttl time.Duration` | Existing tests pass after signature change |
| 6 | Create `CleanupService` in `pkg/cleanup/` | Unit test: calls both operations on tick |
| 7 | Wire `CleanupService` in `main.go` | Startup log shows cleanup service started |
| 8 | Update `tarsy.yaml.example` with retention section | Example file is valid YAML |
| 9 | Update Kubernetes Deployment with `LOG_FORMAT=json` | Container logs are JSON |
| 10 | Update `docs/architecture-context.md` and `docs/project-plan.md` | Docs reflect Phase 11 |
