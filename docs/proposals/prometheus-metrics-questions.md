# Prometheus Metrics — Sketch Questions

**Status:** Open — decisions pending
**Related:** [Sketch document](prometheus-metrics-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: LLM metric label cardinality

LLM metrics need labels to be useful, but too many label combinations create cardinality explosions in Prometheus (memory, storage, query performance). TARSy currently supports ~5 providers (gemini, openai, anthropic, xai, vertexai) with multiple models each.

### Option A: Provider + model labels

Labels: `provider`, `model` (e.g. `provider="gemini"`, `model="gemini-2.5-flash"`)

- **Pro:** Full visibility per model — can see cost/latency differences between `gemini-2.5-flash` vs `gemini-2.5-pro`.
- **Con:** Higher cardinality. If 5 providers × 3 models = 15 series per metric. Manageable but grows if model configs change frequently.

**Decision:** Option A — ~15 series per metric is well within safe limits, and model-level granularity is directly useful for cost and latency tracking.

_Considered and rejected: Option B/provider-only (loses model-level cost/latency distinction), Option C/model_family (requires maintaining a mapping for no real cardinality benefit at this scale)._

---

## Q2: MCP tool label granularity

MCP metrics can be labeled at the server level, tool level, or both. The number of tools varies per deployment (currently ~10–30 tools across a few MCP servers).

### Option A: Server + tool labels

Labels: `server`, `tool` (e.g. `server="kubectl"`, `tool="get_pods"`)

- **Pro:** Full visibility per tool — can identify which specific tool is slow or failing.
- **Con:** Higher cardinality (number of tools × metric count). Could be 30+ series per metric.

**Decision:** Option A — ~30 series per metric is modest cardinality, and per-tool latency/error rates are the primary diagnostic questions.

_Considered and rejected: Option B/server-only (can't identify which tool is problematic), Option C/hybrid counters+histograms (loses per-tool latency, which is the most useful signal)._

---

## Q3: HTTP metrics approach

TARSy uses Echo v5 for HTTP. There are several ways to add request metrics.

### Option A: Echo middleware with promhttp

Use `echo-contrib` or custom Echo middleware that increments Prometheus counters/histograms per request. Serve `/metrics` via `promhttp.Handler()`.

- **Pro:** Automatic coverage for all routes. Well-established pattern.
- **Con:** Must normalize path params (e.g. `/sessions/:id` not `/sessions/abc-123`) to avoid label explosion. Echo v5 may not have official `echo-contrib` prometheus middleware yet.

**Decision:** Option A — if `echo-contrib` supports Echo v5, use it; otherwise fall back to a small custom middleware using `c.RouteInfo().Path()` for path normalization (same approach, just self-maintained).

_Considered and rejected: Option B/manual handler-level (boilerplate in every handler, easy to miss routes), Option C/custom middleware (functionally identical to A but trades an external dependency for ~20 lines of custom code — either is fine, but prefer the library if available)._

---

## Q4: Metric registration architecture

Prometheus metrics can be organized in different ways within the Go codebase.

### Option A: Single global registry in a `metrics` package

One `pkg/metrics/metrics.go` file declares all Prometheus metrics as package-level vars. Other packages import and use them.

- **Pro:** All metrics in one place for discoverability. Simple.
- **Con:** Creates an import dependency from many packages to `metrics`. Cannot test packages in isolation without pulling in Prometheus.

**Decision:** Option A — all ~20 metrics in a single `pkg/metrics` package. Simple, discoverable, and the standard pattern for this scale.

_Considered and rejected: Option B/per-package metrics files (metrics scattered, harder to get full picture, naming collision risk), Option C/DI collector interfaces (significant boilerplate, over-engineered for ~20 metrics)._

---

## Q5: Queue gauge update strategy

Queue depth and active session gauges need to reflect current state. There are two approaches.

### Option C: Hybrid — event-driven for local, polled for global

Use event-driven for `tarsy_workers_active` (local to this pod's workers), polled for `tarsy_sessions_queued` and `tarsy_sessions_active` (global across all pods).

- **Pro:** Best of both: instant for local metrics, consistent for global metrics.
- **Con:** Two mechanisms to maintain.

**Decision:** Option C — event-driven inc/dec for local worker gauges, periodic DB polling for global queue depth and active sessions. `WorkerPool.Health()` already runs similar queries.

_Considered and rejected: Option A/event-driven only (gauge drift on missed updates, can't track global state across pods), Option B/polling only (unnecessarily stale for local worker state)._
