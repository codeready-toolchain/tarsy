# Prometheus Metrics for TARSy

**Status:** Sketch complete — ready for detailed design

## Problem

TARSy has no runtime metrics export. The `/health` endpoint provides a boolean view of database and worker pool liveness, but no quantitative signals for capacity planning, performance monitoring, or incident detection. Internal data like queue depth, active sessions, LLM latencies, and MCP tool failures is either logged ephemerally or only visible through DB queries.

Prometheus metrics would give operators dashboards and alerts for:

- Queue pressure and processing throughput
- LLM call performance and cost (token usage)
- MCP tool reliability
- Session lifecycle and outcome distribution
- HTTP API behavior

## Relationship to Existing System

TARSy already tracks much of this data internally:

- **WorkerPool.Health()** computes queue depth, active sessions, active workers, orphans recovered — but only surfaces a boolean in `/health`.
- **LLMInteraction** records in the DB store provider, model, token usage, duration, and error codes per call.
- **MCPInteraction** records store tool name, server ID, duration, and success/failure per call.
- **AlertSession** tracks status transitions (pending → in_progress → completed/failed/timed_out/cancelled) with timestamps.
- **MCP HealthMonitor** already runs periodic health checks with success/failure tracking.

Prometheus metrics would expose these as time-series counters and histograms rather than point-in-time snapshots or DB-resident audit data.

## Metric Categories

### 1. Session Lifecycle

Tracks alert processing from submission through terminal state.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_sessions_submitted_total` | Counter | `alert_type` | Alerts submitted via API |
| `tarsy_sessions_terminal_total` | Counter | `alert_type`, `status` | Sessions reaching terminal state |
| `tarsy_session_duration_seconds` | Histogram | `alert_type`, `status` | Wall-clock time from claim to completion |
| `tarsy_sessions_active` | Gauge | | Currently in-progress sessions (DB-polled, global) |
| `tarsy_sessions_queued` | Gauge | | Pending sessions waiting for a worker (DB-polled, global) |

### 2. Worker Pool

Tracks processing capacity and health.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_workers_total` | Gauge | | Total configured workers |
| `tarsy_workers_active` | Gauge | | Workers currently processing a session (event-driven, local) |
| `tarsy_orphans_recovered_total` | Counter | | Sessions recovered by orphan detection |

Worker gauges are updated via event-driven inc/dec (purely local to this pod). Session queue and active gauges are DB-polled to reflect global state across pods.

### 3. LLM Calls

Tracks LLM provider performance and cost. Labeled by `provider` + `model` for full per-model visibility (~15 series per metric).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_llm_calls_total` | Counter | `provider`, `model` | LLM calls made |
| `tarsy_llm_errors_total` | Counter | `provider`, `model`, `error_code` | LLM call failures |
| `tarsy_llm_duration_seconds` | Histogram | `provider`, `model` | LLM call latency |
| `tarsy_llm_tokens_total` | Counter | `provider`, `model`, `direction` | Token usage (direction: input/output/thinking) |
| `tarsy_llm_fallbacks_total` | Counter | `from_provider`, `to_provider` | Provider fallback events |

### 4. MCP Tool Calls

Tracks tool execution reliability and performance. Labeled by `server` + `tool` for full per-tool visibility (~30 series per metric).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_mcp_calls_total` | Counter | `server`, `tool` | MCP tool calls made |
| `tarsy_mcp_errors_total` | Counter | `server`, `tool` | MCP tool call failures |
| `tarsy_mcp_duration_seconds` | Histogram | `server`, `tool` | MCP tool call latency |
| `tarsy_mcp_health_status` | Gauge | `server` | Current health (1=healthy, 0=unhealthy) |

### 5. HTTP API

Tracks API request patterns and performance. Implemented as Echo middleware using `echo-contrib` Prometheus middleware (or a small custom middleware with `c.RouteInfo().Path()` if v5 support is unavailable).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_http_requests_total` | Counter | `method`, `path`, `status_code` | HTTP requests handled |
| `tarsy_http_duration_seconds` | Histogram | `method`, `path` | HTTP request latency |

### 6. WebSocket Connections

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `tarsy_ws_connections_active` | Gauge | | Active WebSocket connections |

## Architecture

All metrics are declared in a single `pkg/metrics` package as package-level vars registered against the default Prometheus registry. Other packages (`pkg/queue`, `pkg/mcp`, `pkg/agent`, `pkg/api`) import `pkg/metrics` and call `.Inc()`, `.Observe()`, etc. at the appropriate points.

The `/metrics` endpoint is served via `promhttp.Handler()` on the existing Echo server alongside `/health`.

## Out of Scope

- **Python LLM service metrics** — the gRPC service is a separate process; it can add its own `/metrics` independently.
- **PostgreSQL metrics** — covered by `postgres_exporter` or cloud-native DB monitoring.
- **Custom Grafana dashboards** — the sketch covers what to expose, not how to visualize it.
- **Alertmanager rules** — alert definitions depend on deployment context.
