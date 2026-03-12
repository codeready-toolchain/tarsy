# Prometheus Metrics — Design Questions

**Status:** Open — decisions pending
**Related:** [Design document](prometheus-metrics-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Histogram bucket configuration

Histogram buckets determine the resolution of latency distributions. Wrong buckets produce useless percentiles. TARSy has four distinct latency profiles:

- **LLM calls:** 1–180 seconds (streaming, can be very slow)
- **MCP tool calls:** 0.1–60 seconds (kubectl/monitoring queries)
- **HTTP requests:** 0.005–10 seconds (most are fast API calls)
- **Session duration:** 30 seconds to 30 minutes (full investigation cycles)

### Option A: Custom buckets per metric type

Define specific bucket sets for each latency profile:

```go
LLMBuckets     = []float64{1, 2, 5, 10, 20, 30, 60, 90, 120, 180}
MCPBuckets     = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60}
HTTPBuckets    = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
SessionBuckets = []float64{30, 60, 120, 180, 300, 600, 900, 1200, 1800}
```

- **Pro:** Optimal resolution for each metric. LLM buckets focus on the 1–180s range where most calls land.
- **Con:** Four bucket definitions to maintain.

**Decision:** Option A — defaults are broken for LLM (>10s calls all land in +Inf) and session duration (30s–30min). Custom buckets are a few lines and make percentiles actually work. Changing buckets later loses historical continuity.

_Considered and rejected: Option B/defaults everywhere (LLM and session histograms produce only +Inf percentiles), Option C/single wide-range (poor resolution in each individual range, high memory per series)._

---

## Q2: LLM metrics instrumentation layer

LLM calls flow through: controller (`callLLM`/`callLLMWithStreaming`) → gRPC client (`Generate`) → Python service. The question is where to increment Prometheus metrics.

### Option A: Controller layer (`pkg/agent/controller/`)

Instrument at the `callLLM`/`callLLMWithStreaming` call sites and in `collectStreamWithCallback` (for usage/error chunks).

- **Pro:** Full context available — `execCtx.Config.LLMProviderName` and `execCtx.Config.LLMProvider.Model` give exact labels. Token usage arrives as `UsageChunk` in the stream. Error codes are typed (`LLMErrorCode`). Covers all call paths (iterating, synthesis, exec_summary, scoring, force-conclusion, summarization).
- **Con:** Multiple call sites to instrument (~6 places across `iterating.go`, `single_shot.go`, `scoring.go`, `summarize.go`). Risk of missing one.

**Decision:** Option A with a shared helper function. All label values (provider name, model, usage, error codes) are directly available at the controller. A helper like `observeLLMCall(provider, model, duration, usage, err)` keeps each call site to one line.

_Considered and rejected: Option B/gRPC client (lacks provider name, can't capture usage/errors from stream), Option C/wrapper decorator (channel interception adds complexity for no real benefit)._

---

## Q3: Session duration semantics

`tarsy_session_duration_seconds` can measure different time spans. Sessions have three timestamps: `created_at` (submission), `started_at` (worker claim), `completed_at` (terminal).

### Option C: Both metrics

`tarsy_session_duration_seconds` for processing time (`started_at → completed_at`), `tarsy_session_wait_seconds` for queue wait time (`started_at - created_at`).

- **Pro:** Full visibility. Can derive end-to-end from the sum. Queue wait is a separate, actionable signal.
- **Con:** One more histogram (~5 additional series with `alert_type` + `status` labels).

**Decision:** Option C — processing time and queue wait are fundamentally different signals. One extra histogram is cheap.

_Considered and rejected: Option A/processing only (queue wait invisible), Option B/end-to-end only (conflates queue delay with processing time)._

---

## Q4: Go runtime and process metrics

`promhttp.Handler()` serves the default Prometheus registry, which by default includes Go runtime metrics (`go_goroutines`, `go_memstats_*`, `go_gc_*`) and process metrics (`process_cpu_seconds_total`, `process_resident_memory_bytes`, etc.).

### Option A: Keep default collectors (include runtime metrics)

Use `promhttp.Handler()` as-is.

- **Pro:** Zero effort. Go runtime metrics are standard and useful for debugging memory leaks, goroutine leaks, GC pressure. Every Go service exposes them.
- **Con:** Adds ~40 series to `/metrics` output. Minimal impact.

**Decision:** Option A — standard Go runtime/process metrics included via default `promhttp.Handler()`. Zero effort, universally expected.

_Considered and rejected: Option B/custom registry (loses useful runtime diagnostics for no meaningful benefit)._

---

## Q5: Gauge polling goroutine ownership

The DB-polled gauges (`tarsy_sessions_active`, `tarsy_sessions_queued`) need a goroutine that periodically queries the database.

### Option A: `GaugeCollector` in `pkg/metrics`

A standalone struct in the metrics package with `Start(ctx)`/`Stop()` lifecycle, receiving a minimal `SessionCounter` interface.

- **Pro:** Metrics package owns all metric-related logic. Clean lifecycle.
- **Con:** `pkg/metrics` gains a dependency on a `SessionCounter` interface.

**Decision:** Option A — `GaugeCollector` in `pkg/metrics` with a `SessionCounter` interface for the DB queries. Clean ownership, testable, decoupled from the worker pool.

```go
type SessionCounter interface {
    PendingCount(ctx context.Context) (int, error)
    ActiveCount(ctx context.Context) (int, error)
}
```

_Considered and rejected: Option B/piggyback on WorkerPool.Health() (couples worker pool to metrics, Health() is on-demand not periodic), Option C/goroutine in main.go (not testable, business logic in main)._

---

## Q6: Implementation phasing

The full set of metrics spans 6 categories and ~10 files. This can be done in one PR or phased.

### Option A: Single PR

Implement everything in one change: `pkg/metrics` package, all instrumentation points, HTTP middleware, gauge collector, `/metrics` endpoint.

- **Pro:** Metrics are useful together — LLM latency without session duration context is less valuable. One review cycle. Complete feature.
- **Con:** Large PR (~300–400 lines across ~10 files). Harder to review.

**Decision:** Option A — single PR. ~300–400 lines of straightforward instrumentation code is well within a single reviewable change. Metrics are most useful as a complete set.

_Considered and rejected: Option B/two phases (overhead without risk reduction), Option C/three phases (three review cycles for straightforward code)._
