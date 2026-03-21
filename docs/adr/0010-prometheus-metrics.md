# ADR-0010: Prometheus Metrics

**Status:** Implemented  
**Date:** 2026-03-12

## Overview

TARSy had no runtime metrics export. This design adds Prometheus metrics to the Go orchestrator, exposing session lifecycle, LLM call performance, MCP tool reliability, worker pool health, HTTP request patterns, and WebSocket connection counts via a `/metrics` endpoint.

The sketch phase established:

- **Labels:** `provider`+`model` for LLM, `server`+`tool` for MCP  
- **HTTP middleware:** Echo middleware with `promhttp` (custom fallback for Echo v5 if needed)  
- **Registration:** Single metrics package  
- **Gauge strategy:** Hybrid — event-driven for local worker gauges, DB-polled for global queue/session gauges  

## Design Principles

1. **Instrument at the boundary** — record metrics where the operation starts and completes, not deep inside helpers.
2. **Labels must be bounded** — every label value comes from a finite, known set (config-driven providers, models, route patterns).
3. **No DB queries for counters** — counters and histograms increment inline with the operation. Only gauges use DB polling.
4. **Prometheus client conventions** — `prometheus.DefaultRegisterer`, standard naming (`_total` for counters, `_seconds` for durations), `promhttp.Handler()`.

## Architecture

### Package layout

A dedicated **metrics** package holds:

- All metric declarations, registered in `init()` against the default registerer.
- A **GaugeCollector** with start/stop lifecycle: on a fixed interval (~15s) it queries storage for global session counts and updates gauges.

### `/metrics` endpoint

Served via `promhttp.Handler()` on the main HTTP server alongside `/health`. Includes standard Go runtime and process metrics from the default registry. Exposed on the app port without auth (same trust model as `/health` — probes and Prometheus scrape target the container port, bypassing auth sidecars).

### Gauge collector

Periodic DB queries drive:

- **`tarsy_sessions_active`** — count of sessions in `in_progress`  
- **`tarsy_sessions_queued`** — count of sessions in `pending`  

The collector depends on a narrow interface implemented by the data layer (pending count and active count), keeping metrics decoupled from worker internals.

### Histogram buckets

Default Prometheus buckets (capped around 10s) are unsuitable for LLM wall time (1–180s) and session durations (tens of seconds to tens of minutes); observations would pile into `+Inf`. Custom bucket sets per concern:

| Bucket set | Values (seconds) |
|------------|------------------|
| **LLM** | 1, 2, 5, 10, 20, 30, 60, 90, 120, 180 |
| **MCP** | 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60 |
| **HTTP** | Prometheus default (`DefBuckets`: 0.005 … 10) |
| **Session** (duration / wait) | 30, 60, 120, 180, 300, 600, 900, 1200, 1800 |

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| S-Q1 | `provider`+`model` labels for LLM | ~15 series per metric is safe; model-level granularity needed for cost/latency tracking |
| S-Q2 | `server`+`tool` labels for MCP | ~30 series is modest; per-tool latency/error rates are the primary diagnostic signal |
| S-Q3 | Echo middleware with `promhttp.Handler()` | Standard pattern; use echo-contrib when v5-compatible, else custom middleware with route template as `path` label |
| S-Q4 | Single metrics package | All metrics discoverable in one place; appropriate at this scale |
| S-Q5 | Hybrid gauges: event-driven local, DB-polled global | Responsive local worker signals; consistent cross-pod queue depth |
| D-Q1 | Custom histogram buckets per metric type | Avoids `+Inf`-only buckets for LLM and session time ranges |
| D-Q2 | Instrument LLM at controller layer with shared helper | Full context (provider, model, usage, errors); one-line call sites |
| D-Q3 | Separate `duration_seconds` and `wait_seconds` histograms | Processing time vs queue wait are different signals; cheap to add |
| D-Q4 | Include Go runtime/process metrics via default registry | Zero effort, universally expected, useful for leaks and saturation |
| D-Q5 | GaugeCollector behind a small counter interface | Clear ownership, testable, decoupled from worker pool |
| D-Q6 | One cohesive rollout for all metric categories | Straightforward instrumentation; metrics are most useful as a complete set |

## Metric Definitions

### Session Lifecycle

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_sessions_submitted_total` | Counter | `alert_type` | — | Alerts submitted via API |
| `tarsy_sessions_terminal_total` | Counter | `alert_type`, `status` | — | Sessions reaching terminal state |
| `tarsy_session_duration_seconds` | Histogram | `alert_type`, `status` | Session buckets (see table above) | Processing time (claim → complete) |
| `tarsy_session_wait_seconds` | Histogram | `alert_type` | Session buckets | Queue wait (submit → claim) |
| `tarsy_sessions_active` | Gauge | — | — | In-progress sessions (DB-polled) |
| `tarsy_sessions_queued` | Gauge | — | — | Pending sessions (DB-polled) |

### Worker Pool

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_workers_total` | Gauge | — | Configured workers (set at startup) |
| `tarsy_workers_active` | Gauge | — | Workers currently processing (event-driven) |
| `tarsy_orphans_recovered_total` | Counter | — | Orphaned sessions recovered |

### LLM Calls

Instrumented at the **agent controller** layer. Provider and model come from execution config. A shared helper records duration, token usage, success/failure, and error classification so each call site stays a single observation after the LLM returns.

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_llm_calls_total` | Counter | `provider`, `model` | — | LLM generate calls |
| `tarsy_llm_errors_total` | Counter | `provider`, `model`, `error_code` | — | LLM failures |
| `tarsy_llm_duration_seconds` | Histogram | `provider`, `model` | LLM buckets | Wall-clock LLM time |
| `tarsy_llm_tokens_total` | Counter | `provider`, `model`, `direction` | — | Tokens (input/output/thinking) |
| `tarsy_llm_fallbacks_total` | Counter | `from_provider`, `to_provider` | — | Provider fallback switches |

### MCP Tool Calls

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_mcp_calls_total` | Counter | `server`, `tool` | — | Tool invocations |
| `tarsy_mcp_errors_total` | Counter | `server`, `tool` | — | Tool failures |
| `tarsy_mcp_duration_seconds` | Histogram | `server`, `tool` | MCP buckets | Tool latency |
| `tarsy_mcp_health_status` | Gauge | `server` | — | Health probe (1 / 0) |

### HTTP API

| Metric | Type | Labels | Buckets | Description |
|--------|------|--------|---------|-------------|
| `tarsy_http_requests_total` | Counter | `method`, `path`, `status_code` | — | Requests handled |
| `tarsy_http_duration_seconds` | Histogram | `method`, `path` | HTTP buckets | Request latency |

`path` should be the route template, not raw URLs, to keep cardinality bounded. `/metrics` and `/health` are excluded from HTTP metrics to limit scrape noise.

### WebSocket

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_ws_connections_active` | Gauge | — | Active WebSocket connections |

## Out of Scope

- **Python LLM service metrics** — separate process; may expose its own `/metrics`  
- **PostgreSQL metrics** — use `postgres_exporter`  
- **Grafana dashboards** — deployment-specific  
- **Alertmanager rules** — deployment-specific  
- **OpenShift ServiceMonitor/PodMonitor** — deployment-specific (straightforward to add)  
