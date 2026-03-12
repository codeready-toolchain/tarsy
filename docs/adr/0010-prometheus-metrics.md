# ADR-0010: Prometheus Metrics

**Status:** Implemented
**Date:** 2026-03-12

## Overview

TARSy has no runtime metrics export. This design adds Prometheus metrics to the Go orchestrator, exposing session lifecycle, LLM call performance, MCP tool reliability, worker pool health, HTTP request patterns, and WebSocket connection counts via a `/metrics` endpoint.

The sketch phase established:

- **Labels:** `provider`+`model` for LLM, `server`+`tool` for MCP
- **HTTP middleware:** Echo middleware with promhttp (custom fallback for v5)
- **Registration:** Single `pkg/metrics` package
- **Gauge strategy:** Hybrid — event-driven for local worker gauges, DB-polled for global queue/session gauges

## Design Principles

1. **Instrument at the boundary** — record metrics at the point where the operation starts and completes, not deep in implementation details.
2. **Labels must be bounded** — every label value must come from a finite, known set (config-driven providers, models, route patterns).
3. **No DB queries for counters** — counters and histograms are incremented inline with the operation. Only gauges use DB polling.
4. **Prometheus client conventions** — use `prometheus.DefaultRegisterer`, standard naming (`_total` suffix for counters, `_seconds` for durations), and `promhttp.Handler()`.

## Architecture

### Package Layout

```
pkg/metrics/
├── metrics.go          # All metric declarations + init() registration
└── collector.go        # GaugeCollector: DB-polling goroutine for global gauges
```

All metrics are declared as package-level vars in `metrics.go`, registered via `init()` against `prometheus.DefaultRegisterer`.

### `/metrics` Endpoint

Served via `promhttp.Handler()` wrapped in an Echo handler, registered on the existing HTTP server at the root level alongside `/health`. Includes standard Go runtime/process metrics (`go_goroutines`, `go_memstats_*`, `process_cpu_seconds_total`, etc.) via the default registry.

Accessible on port 8080 without authentication (same as `/health` — Kubernetes probes and Prometheus ServiceMonitor target the container port directly, bypassing oauth2-proxy/kube-rbac-proxy sidecars).

### Gauge Collector

A `GaugeCollector` struct in `pkg/metrics` with `Start(ctx)` / `Stop()` lifecycle, started from `main.go`. Receives a `SessionCounter` interface and periodically queries the DB (~15s interval) for:

- `tarsy_sessions_active` — `COUNT(*) WHERE status = 'in_progress'`
- `tarsy_sessions_queued` — `COUNT(*) WHERE status = 'pending'`

```go
type SessionCounter interface {
    PendingCount(ctx context.Context) (int, error)
    ActiveCount(ctx context.Context) (int, error)
}
```

### Histogram Buckets

Custom bucket sets per metric type. Default Prometheus buckets (max 10s) are broken for LLM calls (1–180s) and session durations (30s–30min) — all observations would land in `+Inf`.

```go
LLMBuckets     = []float64{1, 2, 5, 10, 20, 30, 60, 90, 120, 180}
MCPBuckets     = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60}
HTTPBuckets    = prometheus.DefBuckets // 0.005 – 10s, fine for HTTP
SessionBuckets = []float64{30, 60, 120, 180, 300, 600, 900, 1200, 1800}
```

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| S-Q1 | `provider`+`model` labels for LLM | ~15 series per metric is safe; model-level granularity needed for cost/latency tracking |
| S-Q2 | `server`+`tool` labels for MCP | ~30 series is modest; per-tool latency/error rates are the primary diagnostic signal |
| S-Q3 | Echo middleware with `promhttp.Handler()` | Standard pattern; use `echo-contrib` if v5-compatible, else custom middleware with `c.RouteInfo().Path()` |
| S-Q4 | Single `pkg/metrics` package | All ~20 metrics discoverable in one place; standard for this scale |
| S-Q5 | Hybrid gauges: event-driven local, DB-polled global | Instant for local worker metrics, consistent for global queue/session state across pods |
| D-Q1 | Custom histogram buckets per metric type | Default buckets produce only `+Inf` for LLM (1–180s) and session (30s–30min) ranges |
| D-Q2 | Instrument LLM at controller layer with shared helper | Full context (provider, model, usage, errors) available; `observeLLMCall()` keeps each site to one line |
| D-Q3 | Separate `duration_seconds` and `wait_seconds` histograms | Processing time and queue wait are different signals; one extra histogram is cheap |
| D-Q4 | Include Go runtime/process metrics via default registry | Zero effort, universally expected, useful for debugging memory/goroutine leaks |
| D-Q5 | `GaugeCollector` in `pkg/metrics` with `SessionCounter` interface | Clean ownership, testable, decoupled from worker pool |
| D-Q6 | Single PR for all metrics | ~300–400 lines of straightforward instrumentation; metrics most useful as a complete set |

## Metric Definitions

### Session Lifecycle

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_sessions_submitted_total` | Counter | `alert_type` | — | Alerts submitted via API |
| `tarsy_sessions_terminal_total` | Counter | `alert_type`, `status` | — | Sessions reaching terminal state |
| `tarsy_session_duration_seconds` | Histogram | `alert_type`, `status` | SessionBuckets | Processing time (claim → complete) |
| `tarsy_session_wait_seconds` | Histogram | `alert_type` | SessionBuckets | Queue wait time (submit → claim) |
| `tarsy_sessions_active` | Gauge | — | — | In-progress sessions (DB-polled) |
| `tarsy_sessions_queued` | Gauge | — | — | Pending sessions (DB-polled) |

### Worker Pool

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_workers_total` | Gauge | — | Configured workers (set once at startup) |
| `tarsy_workers_active` | Gauge | — | Workers currently processing (event-driven) |
| `tarsy_orphans_recovered_total` | Counter | — | Orphaned sessions recovered |

### LLM Calls

Instrumented at the controller layer (`pkg/agent/controller/`) via a shared helper function. Provider name and model are read from `execCtx.Config.LLMProviderName` and `execCtx.Config.LLMProvider.Model`.

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_llm_calls_total` | Counter | `provider`, `model` | — | LLM Generate calls |
| `tarsy_llm_errors_total` | Counter | `provider`, `model`, `error_code` | — | LLM call failures |
| `tarsy_llm_duration_seconds` | Histogram | `provider`, `model` | LLMBuckets | LLM call wall-clock time |
| `tarsy_llm_tokens_total` | Counter | `provider`, `model`, `direction` | — | Tokens consumed (input/output/thinking) |
| `tarsy_llm_fallbacks_total` | Counter | `from_provider`, `to_provider` | — | Provider fallback switches |

**Helper function** (in `pkg/agent/controller/`):

```go
func observeLLMCall(provider, model string, duration time.Duration, usage *agent.TokenUsage, err error)
```

Each of the ~6 LLM call sites becomes a single line after the call completes.

### MCP Tool Calls

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_mcp_calls_total` | Counter | `server`, `tool` | — | Tool calls made |
| `tarsy_mcp_errors_total` | Counter | `server`, `tool` | — | Tool call failures |
| `tarsy_mcp_duration_seconds` | Histogram | `server`, `tool` | MCPBuckets | Tool call latency |
| `tarsy_mcp_health_status` | Gauge | `server` | — | Health probe result (1/0) |

### HTTP API

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_http_requests_total` | Counter | `method`, `path`, `status_code` | — | Requests handled |
| `tarsy_http_duration_seconds` | Histogram | `method`, `path` | HTTPBuckets | Request latency |

### WebSocket

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_ws_connections_active` | Gauge | — | Active WebSocket connections |

## Instrumentation Points

### Session Lifecycle

**Submit** — `pkg/api/handler_alert.go`, `submitAlertHandler`: after `s.alertService.SubmitAlert()` succeeds, increment `tarsy_sessions_submitted_total` with alert type.

**Terminal** — `pkg/queue/worker.go`, `pollAndProcess`: after `updateSessionTerminalStatus` succeeds and `statusUpdated` is true (~line 249):
- Increment `tarsy_sessions_terminal_total` with alert type and status
- Observe `tarsy_session_duration_seconds` with `completedAt - startedAt`
- Observe `tarsy_session_wait_seconds` with `startedAt - createdAt`

### Worker Pool

**Active workers** — `pkg/queue/worker.go`, `setStatus`: `Inc()` on transition to `WorkerStatusWorking`, `Dec()` on transition to `WorkerStatusIdle`.

**Workers total** — set once in `main.go` after `NewWorkerPool`.

**Orphans** — `pkg/queue/orphan.go`, `detectAndRecoverOrphans`: `Add(float64(recovered))` after the recovery loop (~line 83).

### LLM Calls

**All call sites** in `pkg/agent/controller/` — after each `callLLM`/`callLLMWithStreaming` returns, call `observeLLMCall(provider, model, duration, usage, err)`. Call sites:
1. `iterating.go` — main iteration loop (~line 107)
2. `iterating.go` — `forceConclusion` (~line 327)
3. `single_shot.go` — synthesis calls
4. `scoring.go` — scoring calls
5. `summarize.go` — MCP result summarization (~line 182)
6. Any executive summary calls

**Fallbacks** — `pkg/agent/controller/fallback.go`, `tryFallback`: when the provider actually switches, increment `tarsy_llm_fallbacks_total`.

### MCP Tool Calls

**Instrumentation point:** `pkg/agent/controller/tool_execution.go`, `executeToolCall`:
- After `Execute` returns (~line 76): increment `tarsy_mcp_calls_total`, observe `tarsy_mcp_duration_seconds`
- On error path (~line 78–82): also increment `tarsy_mcp_errors_total`
- Labels `server` and `tool` from `serverID` and `toolName` already resolved at line 55

**MCP health** — `pkg/mcp/health.go`, `setStatus`: set `tarsy_mcp_health_status` gauge to 1.0 or 0.0.

### HTTP API

**Middleware** — `pkg/api/server.go`, `setupRoutes`: register Prometheus middleware. Uses `c.RouteInfo().Path()` for the `path` label. Exclude `/metrics` and `/health` to avoid scrape noise.

### WebSocket

**Connect/disconnect** — `pkg/events/manager.go`:
- `registerConnection` (line 408): `Inc()`
- `unregisterConnection` (line 415): `Dec()`

## Wiring in `main.go`

```go
// After worker pool creation (~line 273)
metrics.WorkersTotal.Set(float64(cfg.Queue.WorkerCount))

// Start gauge collector (DB-polled session gauges)
gaugeCollector := metrics.NewGaugeCollector(sessionCounter)
gaugeCollector.Start(ctx)
defer gaugeCollector.Stop()

// Register /metrics endpoint and HTTP middleware on httpServer
```

## Implementation

Single PR covering all metric categories. ~300–400 lines across ~10 files:

| File | Changes |
|------|---------|
| `pkg/metrics/metrics.go` | New — all metric declarations |
| `pkg/metrics/collector.go` | New — `GaugeCollector` for DB-polled gauges |
| `pkg/api/server.go` | `/metrics` endpoint, HTTP middleware |
| `pkg/api/handler_alert.go` | `sessions_submitted_total` |
| `pkg/queue/worker.go` | `sessions_terminal_total`, `session_duration`, `session_wait`, `workers_active` |
| `pkg/queue/orphan.go` | `orphans_recovered_total` |
| `pkg/agent/controller/` | LLM helper + call sites (~6 files) |
| `pkg/agent/controller/tool_execution.go` | MCP call/error/duration |
| `pkg/agent/controller/fallback.go` | `llm_fallbacks_total` |
| `pkg/mcp/health.go` | `mcp_health_status` |
| `pkg/events/manager.go` | `ws_connections_active` |
| `cmd/tarsy/main.go` | `workers_total`, gauge collector startup |
| `go.mod` | `prometheus/client_golang` dependency |

## Out of Scope

- **Python LLM service metrics** — separate process, can add its own `/metrics`
- **PostgreSQL metrics** — use `postgres_exporter`
- **Grafana dashboards** — deployment-specific
- **Alertmanager rules** — deployment-specific
- **OpenShift ServiceMonitor/PodMonitor** — deployment-specific (trivial to add)
