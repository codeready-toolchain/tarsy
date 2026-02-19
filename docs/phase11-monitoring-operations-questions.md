# Phase 11: Monitoring & Operations — Open Questions

---

## Q1: Two-Phase Retention vs Hard-Delete-Only

**DECIDED: A — Soft delete only.**

Soft-delete sessions at N days (set `deleted_at`), no hard-delete path. Child rows remain in the database but are hidden from all queries. Simplest implementation — no new `PurgeDeletedSessions()` method needed. Zero risk of accidental data loss. If DB growth becomes a concern, a hard-delete purge can be added later without schema changes (the `deleted_at` field and CASCADE constraints already support it).

---

## Q2: Default Retention Values

**DECIDED: A — Conservative defaults.**

`session_retention_days=365`, `event_ttl=1h`, `cleanup_interval=12h`. Matches old TARSy's session retention window. Event TTL at 1 hour is sufficient since per-session cleanup already handles the normal case within 60 seconds — the TTL is just a safety net for missed cleanups.

---

## Q3: Cleanup Service Location

**DECIDED: A — Standalone `pkg/cleanup/` package.**

`CleanupService` struct with `Start(ctx)`/`Stop()` methods, wired in `main.go`. Same lifecycle pattern as `HealthMonitor`. Clean separation of concerns — retention cleanup is a system maintenance task, not a session processing concern. Easy to test in isolation without WorkerPool dependencies.

---

## Q4: Structured Logging — Format Auto-Detection vs Explicit Config

**DECIDED: A — Env vars only.**

`LOG_FORMAT` env var (`json` or `text`, default `text`) and `LOG_LEVEL` env var (`debug`, `info`, `warn`, `error`, default `info`). Set `LOG_FORMAT=json` in Kubernetes Deployment manifests. Industry standard for Go services. Logging must be configured before YAML config loading (config loader itself logs), so env vars are the only option that avoids a bootstrap problem.

---

## Q5: Cleanup of In-Progress Sessions During Retention

**DECIDED: B — Also soft-delete old non-terminal sessions.**

Add a fallback predicate: `pending` sessions where `created_at < cutoff` are also soft-deleted. Safety net for edge cases that orphan detection doesn't cover — orphan detection only handles `in_progress` sessions with stale heartbeats, not `pending` sessions that were never claimed (e.g., submitted while all workers were down).
