# Phase 2: Queue & Worker System - Design Questions

This document contains questions and concerns about the proposed queue and worker system that need discussion before finalizing the design.

**Status**: 🟡 Pending Discussion  
**Created**: 2026-02-05  
**Purpose**: Identify improvements and clarify design decisions for the queue & worker system

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add your answers inline under each question, then we'll update the main design doc.

---

## 🔥 Critical Priority (Architecture & Safety)

### Q1: Queue Implementation Strategy

**Status**: 🔄 **IN DISCUSSION**

**Context:**
The design proposes using `alert_sessions` table as the queue (status-based filtering) rather than a separate `job_queue` table.

**Question:**
Should we use the sessions table directly as the queue, or create a dedicated queue table?

**Options:**

**Option A - Sessions Table as Queue (Proposed)**
- Queue is `SELECT * FROM alert_sessions WHERE status = 'pending'`
- No separate queue table
- Single source of truth

**Option B - Dedicated Queue Table**
```sql
CREATE TABLE job_queue (
    job_id UUID PRIMARY KEY,
    session_id UUID REFERENCES alert_sessions(session_id),
    priority INT DEFAULT 0,
    scheduled_at TIMESTAMP,
    claimed_by VARCHAR,
    claimed_at TIMESTAMP,
    status VARCHAR -- queued, claimed, processing, completed
);
```

**Pros & Cons:**

| Aspect | Option A (Sessions) | Option B (Dedicated Queue) |
|--------|-------------------|---------------------------|
| Complexity | ✅ Simpler | ❌ More complex |
| Consistency | ✅ Single source of truth | ❌ Must sync queue ↔ session |
| Features | ❌ Limited (FIFO only) | ✅ Easy to add priority, scheduling |
| Schema | ✅ Uses existing table | ❌ Additional table |
| Queries | ✅ Direct session access | ❌ Join required |

**Recommendation:**
?

**Rationale:**
?

---

### Q2: Concurrency Control Strategy

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes using `FOR UPDATE SKIP LOCKED` for session claiming and semaphore for per-process concurrency limits.

**Question:**
How should we control concurrency across multiple replicas/pods?

**Options:**

**Option A - Database Advisory Locks Only**
- `FOR UPDATE SKIP LOCKED` handles all coordination
- No application-level limits
- Workers naturally compete for available sessions

**Option B - Database + Application Semaphore (Proposed)**
- `FOR UPDATE SKIP LOCKED` for claim safety
- Per-process semaphore for local limits
- Global view via database query (count in_progress)

**Option C - Distributed Semaphore (Redis)**
- Shared semaphore across all pods
- Global concurrency enforcement
- Requires Redis dependency

**Trade-offs:**

| Aspect | Option A | Option B (Proposed) | Option C |
|--------|----------|-------------------|----------|
| Dependencies | ✅ DB only | ✅ DB only | ❌ DB + Redis |
| Global limits | ❌ No enforcement | ⚠️ Soft (monitoring) | ✅ Hard enforcement |
| Complexity | ✅ Simple | ✅ Moderate | ❌ Complex |
| Performance | ✅ Fast | ✅ Fast | ⚠️ Redis latency |

**Recommendation:**
?

**Rationale:**
?

---

### Q3: Orphan Detection Leadership

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes all pods run orphan detection independently (no leader election).

**Question:**
Should orphan detection run on all pods or just a single elected leader?

**Options:**

**Option A - All Pods (Proposed)**
- Every pod runs orphan detection task
- Idempotent recovery operations
- No coordination needed

**Option B - Single Leader**
- Leader election (e.g., via database lock, Kubernetes lease)
- Only leader runs orphan detection
- Failover if leader dies

**Option C - Distributed with Coordination**
- Pods coordinate via database
- Distribute orphan recovery across pods
- Use advisory locks to prevent races

**Analysis:**

| Aspect | Option A (All Pods) | Option B (Leader) | Option C (Coordinated) |
|--------|-------------------|------------------|----------------------|
| Complexity | ✅ Simple | ❌ Leader election | ❌ Coordination logic |
| Redundancy | ✅ All pods can detect | ❌ SPOF (until failover) | ✅ Distributed |
| Race safety | ✅ Idempotent ops | ✅ No races | ✅ Lock-based |
| Efficiency | ⚠️ Duplicate queries | ✅ Single query | ✅ Optimal |

**Recommendation:**
?

**Rationale:**
?

---

### Q4: Session Timeout Mechanism

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Need to handle sessions that take too long (e.g., stuck LLM calls, infinite loops).

**Question:**
How should we implement session timeout?

**Options:**

**Option A - Worker-Level Context Timeout**
```go
ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
defer cancel()
result := executor.Execute(ctx, session)
```
- Worker enforces timeout via context
- Context propagates to LLM service
- Clean cancellation

**Option B - Database Field + Background Checker**
- Worker updates `last_interaction_at` periodically
- Background task finds stale sessions
- Marks as `timed_out`

**Option C - Hybrid (Proposed)**
- Worker context timeout (primary)
- Background checker (backup for crashed workers)
- Defense in depth

**Considerations:**
- What happens to LLM requests in flight?
- How to handle partial results?
- Should timeout be configurable per chain?
- Do we save partial progress before timing out?

**Recommendation:**
?

**Rationale:**
?

---

## 🔧 High Priority (Implementation Details)

### Q5: Worker Startup Strategy

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Workers need to start when the Go process starts.

**Question:**
When and how should the worker pool start?

**Options:**

**Option A - Start in main.go (Proposed)**
```go
func main() {
    // ... init services ...
    workerPool := queue.NewWorkerPool(...)
    workerPool.Start(ctx)
    // ... start HTTP server ...
}
```

**Option B - Start After HTTP Server Ready**
```go
func main() {
    // ... init services ...
    httpServer.Start()
    // Wait for server ready
    workerPool.Start(ctx)
}
```

**Option C - Configurable Start (CLI flag)**
```go
func main() {
    if *enableWorkers {
        workerPool.Start(ctx)
    }
}
```

**Considerations:**
- Should workers and API be separable (different processes)?
- Does worker pool need HTTP server to be ready first?
- Useful for debugging/testing to disable workers?

**Recommendation:**
?

**Rationale:**
?

---

### Q6: Poll Interval Strategy

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes fixed poll interval (2s) with jitter (1s).

**Question:**
Should poll interval be fixed or adaptive?

**Options:**

**Option A - Fixed Interval with Jitter (Proposed)**
- Base: 2 seconds
- Jitter: ±1 second
- Simple, predictable

**Option B - Adaptive (Backoff)**
- No work found: increase interval (2s → 4s → 8s → max 30s)
- Work found: reset to base (2s)
- Reduces database load when idle

**Option C - Event-Driven (LISTEN/NOTIFY)**
- PostgreSQL LISTEN/NOTIFY for new sessions
- Workers wake up immediately when session created
- No polling during idle periods

**Trade-offs:**

| Aspect | Fixed | Adaptive | Event-Driven |
|--------|-------|----------|-------------|
| Latency | ✅ Consistent (2s) | ⚠️ Variable (2-30s) | ✅ Instant |
| DB load | ⚠️ Constant | ✅ Low when idle | ✅ Minimal |
| Complexity | ✅ Simple | ✅ Moderate | ❌ Complex |
| Reliability | ✅ High | ✅ High | ⚠️ NOTIFY can be lost |

**Recommendation:**
?

**Rationale:**
?

---

### Q7: Session Result Updates

**Status**: 🔄 **IN DISCUSSION**

**Context:**
After processing, worker needs to update session with results (status, final_analysis, etc.).

**Question:**
How should workers update session results?

**Options:**

**Option A - Single Update at End (Proposed)**
```go
session.Update().
    SetStatus("completed").
    SetCompletedAt(time.Now()).
    SetFinalAnalysis(result.Analysis).
    Save(ctx)
```
- One database transaction
- Atomic result commit

**Option B - Progressive Updates**
```go
// Update status to in_progress
session.Update().SetStatus("in_progress").Save(ctx)

// Periodically update last_interaction_at
ticker := time.NewTicker(30 * time.Second)
go func() {
    for range ticker.C {
        session.Update().SetLastInteractionAt(time.Now()).Save(ctx)
    }
}()

// Final update with results
session.Update().SetStatus("completed").SetFinalAnalysis(...).Save(ctx)
```
- Better observability during execution
- Enables better orphan detection
- More database writes

**Option C - Streaming Updates via WebSocket**
- Worker sends progress updates to API
- API broadcasts to WebSocket clients
- Database updated at end only

**Considerations:**
- How do we handle WebSocket streaming during processing?
- Should intermediate updates be stored in database?
- What if worker crashes mid-execution?

**Recommendation:**
?

**Rationale:**
?

---

### Q8: Error Handling & Recovery

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Sessions can fail for many reasons (LLM errors, MCP failures, timeouts, crashes).

**Question:**
How should we handle failed sessions and enable recovery?

**Options:**

**Option A - Manual Recovery Only (Proposed)**
- Failed sessions stay in `failed` status
- Operator can manually retry via API
- No automatic retry

**Option B - Automatic Retry (Limited)**
```go
type Session struct {
    RetryCount    int
    MaxRetries    int
    LastError     string
}
```
- Retry up to N times
- Exponential backoff
- Permanent failure after max retries

**Option C - Separate Dead Letter Queue**
```sql
CREATE TABLE dead_letter_queue (
    session_id UUID,
    failure_reason TEXT,
    retry_count INT,
    next_retry_at TIMESTAMP
);
```
- Failed sessions moved to DLQ
- Separate processing for retries
- Better failure analysis

**Considerations:**
- What failures should be retried? (transient vs permanent)
- Should retry count/strategy be configurable per chain?
- How to prevent infinite retry loops?
- Should we preserve error history across retries?

**Recommendation:**
?

**Rationale:**
?

---

### Q9: Multi-Stage Session Handling

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Sessions have multiple stages (from chain config). Phase 2.3 focuses on claiming sessions, but stage orchestration is complex.

**Question:**
How should workers handle multi-stage sessions?

**Options:**

**Option A - Worker Processes Entire Session (Proposed)**
```go
func (w *Worker) processSession(session *ent.AlertSession) {
    // Execute all stages sequentially
    for _, stage := range session.Stages {
        result := w.executeStage(stage)
        if result.Failed {
            break
        }
    }
}
```
- Worker owns entire session lifecycle
- Simple coordination
- Worker can't release between stages

**Option B - Per-Stage Queueing**
- Each stage is a separate queue item
- Worker processes one stage, re-queues next stage
- Enables stage-level parallelism
- More complex coordination

**Option C - Hybrid (Session claim + Stage execution)**
- Worker claims session (exclusive lock)
- Executes stages internally
- Updates stage status in database
- Can pause/resume between stages

**Considerations:**
- How to handle stage failures (continue vs abort)?
- Can we parallelize stages within a session?
- How to implement pause/resume at stage boundaries?
- Phase 3 (Agent Framework) will implement actual execution logic

**Recommendation:**
?

**Rationale:**
?

---

## ⚙️ Medium Priority (Optimization & Features)

### Q10: Worker Count Configuration

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes fixed worker count (5 per process).

**Question:**
Should worker count be static or dynamic?

**Options:**

**Option A - Static Configuration (Proposed)**
```yaml
queue:
  worker_count: 5
```
- Fixed at startup
- Predictable resource usage
- Simple implementation

**Option B - Dynamic Scaling**
- Auto-scale workers based on queue depth
- Scale up when queue > threshold
- Scale down when idle
- Respect max_concurrent limit

**Option C - Per-Chain Worker Pools**
- Different worker counts for different chains
- Chain-specific resource allocation
- More complex configuration

**Considerations:**
- Do we expect variable load patterns?
- Is horizontal scaling (more pods) sufficient?
- Added complexity worth the benefit?

**Recommendation:**
?

**Rationale:**
?

---

### Q11: Priority Queue Support

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design uses FIFO (first-in, first-out) ordering.

**Question:**
Should we support priority-based session processing?

**Options:**

**Option A - FIFO Only (Proposed)**
```sql
ORDER BY started_at ASC
```
- Simple, fair
- Prevents starvation

**Option B - Priority Field**
```sql
-- Schema
ALTER TABLE alert_sessions ADD COLUMN priority INT DEFAULT 0;

-- Query
ORDER BY priority DESC, started_at ASC
```
- Higher priority processed first
- Within priority: FIFO

**Option C - Dynamic Priority**
- Priority based on wait time (older = higher priority)
- Prevents starvation automatically
- More complex calculation

**Use Cases for Priority:**
- Critical alerts (production outages)
- User-initiated investigations (higher priority)
- Background analysis (lower priority)
- SLA-based prioritization

**Considerations:**
- Do we have different priority levels in old TARSy?
- How often would priority be used?
- Risk of low-priority starvation?

**Recommendation:**
?

**Rationale:**
?

---

### Q12: Scheduled Sessions

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Current design processes sessions immediately when created.

**Question:**
Should we support scheduling sessions for future execution?

**Options:**

**Option A - No Scheduling (Proposed)**
- Sessions processed immediately
- Simple queue logic

**Option B - Add scheduled_at Field**
```sql
ALTER TABLE alert_sessions ADD COLUMN scheduled_at TIMESTAMP DEFAULT NOW();

-- Query
WHERE status = 'pending'
  AND scheduled_at <= NOW()
```
- Support delayed execution
- Use case: rate limiting, scheduled investigations

**Option C - Separate Scheduling Service**
- Dedicated scheduler creates sessions at scheduled time
- Queue remains immediate-only
- Cleaner separation of concerns

**Use Cases:**
- Rate limit alerts (process at most 1 per minute)
- Schedule investigation for future time
- Batch processing (process all at 2am)

**Recommendation:**
?

**Rationale:**
?

---

### Q13: Queue Observability

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design includes basic health checks and metrics.

**Question:**
What level of queue observability do we need?

**Must-Have Metrics:**
- Queue depth (pending sessions)
- Active sessions (in_progress)
- Completed/failed counts
- Worker health

**Nice-to-Have:**
- Queue wait time (time in pending)
- Processing duration (time in_progress)
- Worker utilization (busy vs idle)
- Orphan detection stats
- Queue depth by chain_id/alert_type

**Proposed Implementation:**

```go
type QueueMetrics struct {
    QueueDepth         prometheus.Gauge
    ActiveSessions     prometheus.Gauge
    SessionsCompleted  prometheus.Counter
    SessionsFailed     prometheus.Counter
    QueueWaitTime      prometheus.Histogram
    ProcessingDuration prometheus.Histogram
    WorkerUtilization  prometheus.Gauge
}
```

**Questions:**
- Do we need per-chain metrics?
- Should metrics be exposed via Prometheus /metrics endpoint?
- Do we need custom dashboard (Grafana)?
- What alerting rules do we need?

**Recommendation:**
?

**Rationale:**
?

---

### Q14: Session Cancellation

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Users may want to cancel in-progress sessions.

**Question:**
How should session cancellation work?

**Options:**

**Option A - Status Update Only**
```go
// API handler
session.Update().SetStatus("cancelled").Save(ctx)

// Worker checks periodically
if session.Status == "cancelled" {
    return ErrCancelled
}
```
- Simple implementation
- Polling overhead
- Delayed cancellation

**Option B - Context Cancellation**
```go
// Store context cancel functions
sessionContexts map[string]context.CancelFunc

// Worker creates cancellable context
ctx, cancel := context.WithCancel(ctx)
sessionContexts[session.ID] = cancel

// API handler
if cancel, ok := sessionContexts[session.ID]; ok {
    cancel() // Immediately propagates
}
```
- Immediate cancellation
- Memory overhead (map of contexts)
- Cancellation propagates to LLM service

**Option C - No Cancellation (Proposed)**
- Sessions run to completion or timeout
- No explicit cancellation
- Simpler implementation

**Considerations:**
- How important is cancellation for users?
- Should cancellation be graceful (finish current iteration)?
- What happens to partial results?
- Old TARSy cancellation behavior?

**Recommendation:**
?

**Rationale:**
?

---

### Q15: Testing Database Requirements

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Queue system requires PostgreSQL-specific features (`FOR UPDATE SKIP LOCKED`).

**Question:**
How should we handle testing?

**Options:**

**Option A - Real PostgreSQL (Proposed)**
```go
func setupTestDB(t *testing.T) *ent.Client {
    // Use testcontainers to spin up PostgreSQL
    // Or use test database (requires PostgreSQL installed)
}
```
- Tests real behavior
- Catches PostgreSQL-specific issues
- Slower tests

**Option B - SQLite for Tests**
- SQLite doesn't support `FOR UPDATE SKIP LOCKED`
- Mock or skip concurrency tests
- Faster tests

**Option C - Hybrid**
- Unit tests: SQLite (fast, no concurrency tests)
- Integration tests: PostgreSQL (real behavior)

**Considerations:**
- CI environment has PostgreSQL?
- Test speed vs fidelity trade-off?
- How to test concurrent claiming?

**Recommendation:**
?

**Rationale:**
?

---

## 📊 Low Priority (Future Enhancements)

### Q16: Queue Metrics Dashboard

**Status**: ⏸️ **DEFERRED**

**Context:**
Operators need visibility into queue health.

**Question:**
Should we provide a dashboard for queue monitoring?

**Options:**
- Grafana dashboard (Prometheus data source)
- Custom built-in dashboard (HTML page)
- CLI tool for queue inspection

**Deferred Because:**
- Basic metrics sufficient initially
- Operators can use Prometheus directly
- Dashboard can be added later

---

### Q17: Multi-Tenant Queue Isolation

**Status**: ⏸️ **DEFERRED**

**Context:**
Future requirement for multi-tenant deployments.

**Question:**
How to isolate queues per tenant/namespace?

**Options:**
- Separate database per tenant
- Tenant ID field + filtered queries
- Separate worker pools per tenant

**Deferred Because:**
- Not needed for MVP
- Single-tenant deployment initially
- Can design later when needed

---

### Q18: Queue Backpressure API

**Status**: ⏸️ **DEFERRED**

**Context:**
When queue is overloaded, should we reject new sessions?

**Question:**
Should the API reject session creation when queue is full?

**Options:**

**Option A - Always Accept**
- Queue grows unbounded
- All sessions eventually processed

**Option B - Reject When Full**
```go
if queueDepth > maxQueueSize {
    return HTTP 503 Service Unavailable
}
```

**Deferred Because:**
- Unknown load patterns
- Can add later if needed
- Initial capacity planning first

---

## Summary of Critical Decisions Needed

Before starting implementation, we need to decide:

1. **Q1 - Queue Strategy**: Sessions table vs dedicated queue table
2. **Q2 - Concurrency Control**: How to enforce global limits
3. **Q3 - Orphan Detection**: All pods vs leader election
4. **Q4 - Session Timeout**: Implementation approach
5. **Q7 - Result Updates**: Single update vs progressive updates
6. **Q8 - Error Handling**: Manual retry vs automatic retry
7. **Q9 - Multi-Stage Handling**: How workers process stages

Once these are decided, we can update the main design document and proceed with implementation.

---

## Next Steps

1. Review and discuss each question
2. Make decisions (mark as ✅)
3. Update main design document with decisions
4. Create implementation plan with priorities
5. Begin Phase 2.3 implementation

---

## References

- PostgreSQL Advisory Locks: https://www.postgresql.org/docs/current/explicit-locking.html
- Worker Pool Patterns: https://gobyexample.com/worker-pools
- Queue Implementation Patterns: https://www.openmymind.net/Task-Queues-In-Postgres/
- Old TARSy Queue System: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/orchestration/`
