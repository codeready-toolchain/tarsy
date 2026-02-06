# Phase 2: Queue & Worker System - Design Questions

This document contains questions and concerns about the proposed queue and worker system that need discussion before finalizing the design.

**Status**: ✅ All Questions Decided  
**Created**: 2026-02-05  
**Updated**: 2026-02-06  
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

**Status**: ✅ **DECIDED**

**Context:**
The design proposes using `alert_sessions` table as the queue (status-based filtering) rather than a separate `job_queue` table.

**Question:**
Should we use the sessions table directly as the queue, or create a dedicated queue table?

**Decision**: **Option A - Sessions Table as Queue**

**Rationale:**
- Keep it simple - proven pattern from old TARSy
- Old TARSy uses sessions table with status-based filtering (`status = 'pending'`)
- Single source of truth (no sync issues between queue and sessions)
- Direct access to session data (no joins needed)
- Sufficient for expected load and features
- Can add priority/scheduling via additional fields if needed later (YAGNI principle)

**Implementation:**
```sql
-- Queue query
SELECT session_id, alert_data, chain_id, agent_type, alert_type, mcp_selection
FROM alert_sessions
WHERE status = 'pending'
  AND deleted_at IS NULL
ORDER BY started_at ASC  -- FIFO
LIMIT 1
FOR UPDATE SKIP LOCKED;
```

---

### Q2: Concurrency Control Strategy

**Status**: ✅ **DECIDED**

**Context:**
Design proposes using `FOR UPDATE SKIP LOCKED` for session claiming and semaphore for per-process concurrency limits.

**Question:**
How should we control concurrency across multiple replicas/pods?

**Decision**: **Database-Based Hard Limit (Old TARSy Pattern)**

Old TARSy achieves hard global limits using just the database with a two-step process:

1. **Check Capacity** - `SELECT COUNT(*) FROM alert_sessions WHERE status = 'in_progress'`
2. **Claim if Under Limit** - Use `FOR UPDATE SKIP LOCKED` to claim session atomically

**Implementation Pattern:**
```go
func (w *Worker) pollAndProcess(ctx context.Context) error {
    // Step 1: Check global capacity
    activeCount, err := w.countActiveSessions(ctx)
    if err != nil {
        return err
    }
    
    if activeCount >= w.config.Queue.MaxConcurrentSessions {
        // At capacity - wait before retry
        return ErrAtCapacity
    }
    
    // Step 2: Claim session (if still available)
    session, err := w.claimNextSession(ctx)
    if err != nil {
        return err
    }
    
    // Step 3: Process
    return w.processSession(ctx, session)
}
```

**Why This Works:**
- Database provides global view across all pods
- Polling loop naturally coordinates (1-2s intervals)
- Small race window (check → claim) but self-correcting
- No Redis or additional infrastructure needed
- Proven pattern from old TARSy

**Rationale:**
- Keep it simple - replicate old TARSy's proven approach
- Database queries provide distributed coordination
- Hard limit enforcement without additional dependencies
- Slight overshoot possible (e.g., 11 instead of 10) but acceptable
- Next poll cycle self-corrects any overshoot

**Note**: We do NOT need per-process semaphores (Option B) because the database check provides global coordination.

---

### Q3: Orphan Detection Leadership

**Status**: ✅ **DECIDED**

**Context:**
Design proposes all pods run orphan detection independently (no leader election).

**Question:**
Should orphan detection run on all pods or just a single elected leader?

**Decision**: **Option A - All Pods (Old TARSy Pattern)**

**How Old TARSy Does It:**
From `history_cleanup_service.py`:
```python
"""
Runs as background task on each pod (idempotent - multiple pods
running cleanup simultaneously is safe).
"""
```

Every pod runs orphan detection background task (every 10 minutes by default).

**Implementation:**
```go
func (p *WorkerPool) runOrphanDetection(ctx context.Context) {
    ticker := time.NewTicker(p.config.Queue.OrphanDetectionInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            // Find orphaned sessions
            orphans, err := p.findOrphanedSessions(ctx)
            
            // Reset each to PENDING (idempotent - safe if multiple pods do it)
            for _, session := range orphans {
                session.Update().
                    SetStatus("pending").
                    ClearPodID().
                    Save(ctx)
            }
        }
    }
}
```

**Why This Works:**
- Recovery is idempotent (UPDATE status = 'pending' WHERE session_id = ...)
- If two pods recover same orphan simultaneously → harmless duplicate UPDATEs
- Simple implementation (no leader election complexity)
- No single point of failure
- Proven pattern from old TARSy

**Rationale:**
- Keep it simple - replicate old TARSy's approach
- Idempotent operations make concurrent execution safe
- No need for leader election complexity
- All pods can detect and recover (redundancy)
- Duplicate queries acceptable (runs every 10 minutes, not frequently)

---

### Q4: Session Timeout & Cancellation

**Status**: ✅ **DECIDED**

**Context:**
Need to handle sessions that take too long (e.g., stuck LLM calls, infinite loops) AND support manual cancellation.

**Decision**: **Hierarchical Timeouts with Context Propagation + Manual Cancellation**

## Requirements

### 1. **Hierarchical Timeout Levels**

Different operations have different timeout budgets:

```yaml
# Configuration
timeouts:
  session_timeout: 15m           # Global session timeout (configurable)
  llm_interaction_timeout: 2m    # Per-LLM-call timeout (hardcoded in code)
  mcp_interaction_timeout: 2m    # Per-MCP-call timeout (hardcoded in code)
```

**Why hierarchical**:
- Prevents single stuck operation from consuming entire session budget
- Session gets 15 minutes total
- Each LLM call gets max 2 minutes (fails fast if stuck)
- Each MCP tool call gets max 2 minutes (fails fast if tool hangs)
- LLM can recover (skip bad tool, try other tools/iterations)

### 2. **Two Cancellation Scenarios**

**A. Timeout (automatic)**:
- Session exceeds 15m → status: `timed_out`
- LLM call exceeds 2m → mark interaction with error, continue or fail
- MCP call exceeds 2m → mark interaction with error, LLM can retry/skip

**B. Manual Cancellation**:
- User cancels via API → status: `cancelled`
- Same cleanup logic as timeout (different status)

### 3. **Status Update Strategy**

When timeout/cancellation occurs during processing:

```go
// If LLM interaction in progress but incomplete:
llm_interaction.error_message = "Timeout: exceeded 2m"
llm_interaction.completed_at = now

// Update entity statuses
agent_execution.status = "timed_out"  // or "cancelled"

// Stage/session status determined by aggregation logic
// (see phase2-database-schema-questions.md for full rules)
```

**Important**: For **parallel agents**, timeout of one agent is treated as an error (like failure). The overall stage status is determined by the normal aggregation logic based on `success_policy`:
- `success_policy: "all"` - One timeout → stage may fail (unless others succeed and we have partial success logic)
- `success_policy: "any"` - One timeout → stage succeeds if ANY other agent completes

See `docs/phase2-database-schema-questions.md` (lines 97-129) for complete stage status aggregation rules.

**No special partial state needed** - just update entity statuses, let aggregation logic determine final stage/session status.

## Implementation

### Context-Based Timeout Propagation

```go
// Worker level - Session timeout
func (w *Worker) processSession(ctx context.Context, session *ent.AlertSession) error {
    // Apply session-level timeout (15m)
    sessionCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
    defer cancel()
    
    // Store cancel function for manual cancellation support
    w.activeSessions[session.ID] = cancel
    defer delete(w.activeSessions, session.ID)
    
    // Execute with timeout context
    result := w.sessionExecutor.Execute(sessionCtx, session)
    
    // Check why execution stopped
    if errors.Is(sessionCtx.Err(), context.DeadlineExceeded) {
        // Timeout - update all entities
        return w.handleSessionTimeout(ctx, session)
    }
    
    return w.updateSessionResult(ctx, session, result)
}

// Agent execution level - propagates context
func (e *AgentExecutor) Execute(ctx context.Context, agent Agent) error {
    // Create LLM interaction record
    llmInteraction := createLLMInteraction(...)
    
    // Apply LLM-level timeout (2m)
    llmCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
    defer cancel()
    
    // Call LLM service with timeout
    response, err := e.llmClient.Generate(llmCtx, request)
    
    if errors.Is(err, context.DeadlineExceeded) {
        // LLM call timed out
        llmInteraction.Update().
            SetErrorMessage("LLM interaction timed out after 2m").
            SetCompletedAt(time.Now()).
            Save(ctx)
        return ErrLLMTimeout // Agent can handle this
    }
    
    // Success - continue
    return nil
}

// MCP interaction level - propagates context
func (m *MCPClient) CallTool(ctx context.Context, tool string, args map[string]any) error {
    // Apply MCP-level timeout (2m)
    mcpCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
    defer cancel()
    
    result, err := m.transport.Call(mcpCtx, tool, args)
    
    if errors.Is(err, context.DeadlineExceeded) {
        // MCP call timed out
        mcpInteraction.Update().
            SetErrorMessage("MCP tool call timed out after 2m").
            SetCompletedAt(time.Now()).
            Save(ctx)
        return ErrMCPTimeout // LLM can retry or skip
    }
    
    return nil
}
```

### Manual Cancellation Support

```go
// Worker maintains map of active sessions
type Worker struct {
    activeSessions map[string]context.CancelFunc  // session_id → cancel function
    mu             sync.RWMutex
}

// API endpoint for cancellation
func (s *SessionService) CancelSession(ctx context.Context, sessionID string) error {
    // Update database status
    session, err := s.client.AlertSession.Get(ctx, sessionID)
    if err != nil {
        return err
    }
    
    if session.Status != alertsession.StatusInProgress {
        return ErrNotCancellable
    }
    
    // Set cancellation flag
    _, err = session.Update().
        SetStatus(alertsession.StatusCancelling).  // Intermediate state
        Save(ctx)
    
    // Signal worker to cancel (if it's on this pod)
    s.workerPool.CancelSession(sessionID)
    
    return err
}

// Worker checks for cancellation
func (w *Worker) CancelSession(sessionID string) {
    w.mu.RLock()
    defer w.mu.RUnlock()
    
    if cancel, ok := w.activeSessions[sessionID]; ok {
        cancel()  // Triggers context.Canceled in execution
    }
}

// Execution checks context periodically
func (e *AgentExecutor) Execute(ctx context.Context) error {
    for iteration := 0; iteration < maxIterations; iteration++ {
        // Check if cancelled
        if ctx.Err() != nil {
            if errors.Is(ctx.Err(), context.Canceled) {
                return ErrCancelled  // Manual cancellation
            }
            if errors.Is(ctx.Err(), context.DeadlineExceeded) {
                return ErrTimeout    // Timeout
            }
        }
        
        // Do work...
    }
}
```

## Configuration

```yaml
# deploy/config/tarsy.yaml

timeouts:
  # Session-level timeout
  session_timeout: 15m
  
  # Sub-operation timeouts
  llm_interaction_timeout: 2m
  mcp_interaction_timeout: 2m
  
  # Graceful shutdown - matches session_timeout to avoid interrupting active sessions
  # IMPORTANT: Kubernetes deployments must set terminationGracePeriodSeconds >= this value
  graceful_shutdown_timeout: 15m
```

```go
// pkg/config/timeouts.go

type TimeoutConfig struct {
    SessionTimeout          time.Duration `yaml:"session_timeout" validate:"required"`
    LLMInteractionTimeout   time.Duration `yaml:"llm_interaction_timeout" validate:"required"`
    MCPInteractionTimeout   time.Duration `yaml:"mcp_interaction_timeout" validate:"required"`
    GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout" validate:"required"`
}

// Built-in defaults
func DefaultTimeoutConfig() *TimeoutConfig {
    return &TimeoutConfig{
        SessionTimeout:          15 * time.Minute,
        LLMInteractionTimeout:   2 * time.Minute,
        MCPInteractionTimeout:   2 * time.Minute,
        GracefulShutdownTimeout: 15 * time.Minute, // Match session timeout for complete graceful shutdown
    }
}
```

**Kubernetes Deployment Configuration:**

```yaml
# deploy/kubernetes/deployment.yaml

apiVersion: apps/v1
kind: Deployment
metadata:
  name: tarsy-backend
spec:
  template:
    spec:
      # CRITICAL: Must be >= graceful_shutdown_timeout to allow sessions to complete
      # Default 30s is too short for 15m sessions
      terminationGracePeriodSeconds: 900  # 15 minutes (matches session_timeout)
      
      containers:
      - name: backend
        image: tarsy-backend:latest
        # ...
```

**Why Match Session Timeout:**
- Prevents interrupting healthy active sessions during deployments
- Ensures sessions complete naturally (better UX, no wasted LLM tokens)
- Orphan recovery becomes true safety net, not primary mechanism
- Worth the wait: deployments are not urgent, investigations are valuable

## Benefits

✅ **Hierarchical timeouts** prevent single operation from blocking entire session
✅ **Context propagation** cleanly cancels entire operation tree
✅ **Manual cancellation** via API with proper status tracking
✅ **Fast failure** for stuck operations (2m, not 15m)
✅ **Recovery possible** (LLM can skip bad tool, continue investigation)
✅ **Clean status tracking** (timed_out vs cancelled)
✅ **No special state** needed (just update existing entities)

**Rationale:**
- Context is Go's standard cancellation mechanism
- Hierarchical timeouts provide defense in depth
- Manual cancellation essential for user control
- Different statuses (timed_out vs cancelled) aid debugging
- No partial state complexity - just mark entities appropriately

---

## 🔧 High Priority (Implementation Details)

### Q5: Worker Startup Strategy

**Status**: ✅ **DECIDED**

**Context:**
Workers need to start when the Go process starts. Need to determine startup order relative to other services.

**Question:**
When and how should the worker pool start?

**Old TARSy Approach:**

From `main.py` lifespan (startup sequence):
1. Database initialization
2. **One-time orphan cleanup** (startup only)
3. AlertService initialization
4. MCP health monitor start
5. Event system start
6. History cleanup service start
7. **SessionClaimWorker start** ← Queue worker
8. HTTP server starts accepting requests

**Decision**: **Start Before HTTP Server (with Go improvements)**

**Go Startup Sequence:**

```go
func main() {
    ctx := context.Background()
    
    // 1. Load configuration
    cfg, err := config.Initialize(ctx, *configDir)
    if err != nil {
        log.Fatal("Failed to load configuration", "error", err)
    }
    
    // 2. Initialize database
    dbClient, err := database.NewClient(cfg.Database)
    if err != nil {
        log.Fatal("Failed to connect to database", "error", err)
    }
    defer dbClient.Close()
    
    // 3. Run migrations
    if err := database.RunMigrations(dbClient); err != nil {
        log.Fatal("Failed to run migrations", "error", err)
    }
    
    // 4. One-time startup orphan cleanup
    if err := cleanupOrphanedSessions(ctx, dbClient, cfg); err != nil {
        log.Error("Failed to cleanup orphaned sessions during startup", "error", err)
        // Non-fatal - continue
    }
    
    // 5. Initialize services
    sessionService := services.NewSessionService(dbClient, cfg)
    // ... other services ...
    
    // 6. Create session executor (Phase 3 - stub for now)
    sessionExecutor := executor.NewSessionExecutor(cfg, dbClient)
    
    // 7. Start worker pool (BEFORE HTTP server)
    workerPool := queue.NewWorkerPool(podID, dbClient, cfg, sessionExecutor)
    if err := workerPool.Start(ctx); err != nil {
        log.Fatal("Failed to start worker pool", "error", err)
    }
    defer workerPool.Stop()
    
    // 8. Start orphan detection background task (already running in worker pool)
    // (Runs periodically, separate from one-time startup cleanup)
    
    // 9. Start HTTP server (non-blocking)
    httpServer := api.NewServer(cfg, sessionService, workerPool)
    go func() {
        if err := httpServer.Start(); err != nil && err != http.ErrServerClosed {
            log.Fatal("HTTP server error", "error", err)
        }
    }()
    
    log.Info("TARSy started successfully")
    
    // 10. Wait for shutdown signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    <-sigCh
    
    log.Info("Shutdown signal received")
    
    // 11. Graceful shutdown (workers first, then HTTP)
    shutdownCtx, cancel := context.WithTimeout(ctx, cfg.Timeouts.GracefulShutdownTimeout)
    defer cancel()
    
    // Stop worker pool gracefully
    // - Workers immediately stop accepting NEW sessions (no more claims)
    // - Workers wait for CURRENT sessions to complete naturally
    // - Timeout after graceful_shutdown_timeout (default: 15m, matches session_timeout)
    // - If timeout exceeded, incomplete sessions become orphans (recovered on next startup)
    // This prevents interrupting healthy sessions during deployments (K8s rolling updates, etc.)
    // NOTE: Kubernetes terminationGracePeriodSeconds must be >= 900s (15m) to support this
    workerPool.Stop()
    
    // Stop HTTP server
    if err := httpServer.Shutdown(shutdownCtx); err != nil {
        log.Error("HTTP server shutdown error", "error", err)
    }
    
    log.Info("Shutdown complete")
}
```

**Key Improvements over Old TARSy:**

1. **Explicit Startup Order**: Clear dependency chain in code
2. **One-time Orphan Cleanup**: Separate from periodic orphan detection
   - Startup: Clean up crashed sessions from previous run
   - Runtime: Periodic background task (every 10 minutes)
3. **Non-blocking HTTP Server**: Server starts in goroutine, doesn't block main
4. **Graceful Shutdown**: Workers stop before HTTP (finish current work)
5. **Fail-fast**: Fatal errors exit immediately (database, config)
6. **Go concurrency**: Can start independent services in parallel if needed

**Why Workers Start Before HTTP:**
- ✅ No race condition (session created but no worker to process it)
- ✅ Workers ready when first API request arrives
- ✅ Health checks can report worker status immediately
- ✅ Matches old TARSy pattern (proven design)

**Why Not Configurable (no CLI flag):**
- Workers are core functionality, not optional
- Separation of concerns better achieved via deployment (separate pods) not flags
- Simpler code (no conditional logic)
- For testing: Use test-specific setup, not production flags

**Rationale:**
- Replicates proven old TARSy startup sequence
- Go's explicit error handling improves reliability
- Clear shutdown sequence prevents orphaned sessions
- Non-blocking HTTP server allows graceful coordination

---

### Q6: Poll Interval Strategy

**Status**: ✅ **DECIDED**

**Context:**
Design proposes fixed poll interval with jitter for workers to poll the database for pending sessions.

**Question:**
Should poll interval be fixed or adaptive? What values should we use?

**Decision**: **Option A Modified - Fixed Interval (1s) with Jitter (500ms)**

**Rationale:**

**Old TARSy uses 1s fixed interval** (no jitter) - proven pattern that works well.

**Key considerations:**
1. **DB Load is manageable** - Poll query is very cheap:
   - Uses index on `status` column
   - Returns 0-1 rows, no joins
   - `FOR UPDATE SKIP LOCKED` is lock-free
   - Typical execution: < 1ms
   
2. **Total load = replicas × poll rate**:
   - 3 replicas × 1/sec = 3 queries/sec (trivial for PostgreSQL)
   - 5 replicas × 1/sec = 5 queries/sec (trivial)
   - 10 replicas × 1/sec = 10 queries/sec (trivial)
   - 100 replicas × 1/sec = 100 queries/sec (still manageable, but consider increasing interval via config)

3. **User experience**:
   - Average latency = poll_interval / 2
   - 1s poll → ~0.5s average pickup time
   - 2s poll → ~1s average pickup time
   - Users prefer faster response

4. **Jitter benefits**:
   - Without jitter: All replicas poll at same time (thundering herd)
   - With ±500ms jitter: Queries spread across 0.5s-1.5s window
   - Smoother, more distributed DB load
   - With 5 replicas: Highly likely one replica polls within 0.5s of alert creation

**Configuration:**

```yaml
# deploy/config/tarsy.yaml

queue:
  # Number of worker goroutines per replica/pod
  # Each worker independently polls and processes sessions
  # With 1 replica: 5 workers = up to 5 concurrent sessions
  # With 3 replicas: 15 workers total = up to 15 concurrent sessions globally
  worker_count: 5
  
  # Maximum concurrent sessions being processed across ALL replicas/pods
  # This is a GLOBAL limit enforced by database query before claiming
  # Examples:
  #   - 1 replica with 5 workers: can handle up to 5 concurrent sessions
  #   - 3 replicas with 5 workers each: can handle up to 5 concurrent sessions total (global limit)
  # Set this to match worker_count for single-replica deployments
  max_concurrent_sessions: 5
  
  # Poll interval configuration
  # Base interval for checking pending sessions in database
  poll_interval: 1s
  
  # Random jitter to distribute queries across replicas
  # Actual interval will be: poll_interval ± poll_interval_jitter
  # Example with 5 replicas: queries spread across 0.5s-1.5s instead of all at 1s
  poll_interval_jitter: 500ms
  
  # NOTE: For deployments with high number of replicas (50+), consider increasing
  # poll_interval to reduce aggregate database load. With 100 replicas:
  # - 1s interval = 100 queries/sec
  # - 5s interval = 10 queries/sec
  # Most deployments (2-10 replicas) work well with 1s default.
```

**Go Configuration:**

```go
// pkg/config/queue.go

type QueueConfig struct {
    WorkerCount             int           `yaml:"worker_count" validate:"required,min=1"`
    MaxConcurrentSessions   int           `yaml:"max_concurrent_sessions" validate:"required,min=1"`
    PollInterval            time.Duration `yaml:"poll_interval" validate:"required"`
    PollIntervalJitter      time.Duration `yaml:"poll_interval_jitter" validate:"required"`
    // ... other fields ...
}

func DefaultQueueConfig() *QueueConfig {
    return &QueueConfig{
        WorkerCount:           5,
        MaxConcurrentSessions: 5,  // Match worker_count for single-replica default
        PollInterval:          1 * time.Second,      // Match old TARSy
        PollIntervalJitter:    500 * time.Millisecond, // Distribute load
        // ... other defaults ...
    }
}
```

**Implementation note:**

```go
// Worker poll loop with jitter
func (w *Worker) pollLoop(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-time.After(w.getNextPollDelay()):
            // Poll for work
            w.checkAndClaimSession(ctx)
        }
    }
}

func (w *Worker) getNextPollDelay() time.Duration {
    jitter := time.Duration(rand.Int63n(int64(2 * w.config.PollIntervalJitter)))
    return w.config.PollInterval - w.config.PollIntervalJitter + jitter
    // Example: 1s - 500ms + [0..1000ms] = [500ms..1500ms]
}
```

---

### Q7: Session Result Updates

**Status**: ✅ **DECIDED**

**Context:**
During session processing, the worker needs to persist results, stream real-time updates to WebSocket clients, and handle crash recovery. This is critical for UX -- users must see live progress, not just a final result.

**Question:**
How should workers update session results? Single update at end, progressive DB writes, or streaming?

**Decision**: **Hybrid: Progressive DB Writes + Transient WebSocket Streaming**

Combines progressive database writes (for state persistence and crash recovery) with transient streaming events (for real-time LLM token delivery). Mirrors old TARSy's proven architecture but leverages the improved TimelineEvent design to eliminate frontend de-duplication complexity.

**Rationale:**

### Why Not Single Update at End?

- Users see nothing until the entire session completes (potentially 15 minutes)
- If worker crashes mid-session, ALL progress is lost (no intermediate state in DB)
- Contradicts the real-time UX requirement (WebSocket streaming, live timeline)

### Why Not DB Updates for Every Token?

- 5 concurrent sessions × 1-3 parallel agents = 5-15 concurrent LLM streams
- At ~10-50 tokens/sec each, DB writes per chunk batch = 10-150 writes/sec
- Pure write amplification for ephemeral data (nobody queries half-streamed content)
- NOTIFY alone delivers the same real-time UX without DB overhead

### The Hybrid Approach

**Principle:** Write to DB for **state transitions** (things that matter for crash recovery and queries). Use NOTIFY/WebSocket for **ephemeral streaming** (LLM tokens that only matter to live clients).

**What gets written to DB immediately:**

| Event | DB Write | Why |
|-------|----------|-----|
| Session claimed | `alert_session.status = in_progress` | Crash recovery, orphan detection |
| Stage starts | Create `Stage` record | State tracking |
| Agent execution starts | Create `AgentExecution` record | State tracking |
| LLM streaming begins | Create `TimelineEvent` (status: `streaming`) | Crash recovery, timeline skeleton |
| LLM streaming completes | Update `TimelineEvent` (status: `completed`, content: final text) | Permanent record |
| LLM interaction completes | Create `LLMInteraction` record | Debug/audit |
| MCP tool call starts | Create `TimelineEvent` (status: `streaming`) | Timeline, crash recovery |
| MCP tool call completes | Update `TimelineEvent` (status: `completed`) + Create `MCPInteraction` | Permanent record |
| Heartbeat | Update `alert_session.last_interaction_at` | Orphan detection |
| Stage completes | Update `Stage` status | State tracking |
| Session completes | Update `alert_session` (status, final_analysis, etc.) + cleanup events | Final state |

**What gets streamed via NOTIFY/WebSocket only (transient, no DB persistence):**

| Event | Delivery | Why Transient |
|-------|----------|---------------|
| LLM token chunks | Event table → NOTIFY → WebSocket | High frequency, ephemeral, only for live clients |
| Progress indicators | NOTIFY → WebSocket | UX feedback only |

### LLM Token Streaming Flow

```
1. LLM call starts
   → DB: Create TimelineEvent (event_id=X, status=streaming, content="")
   → Event table: {type: "timeline_event.created", event_id: X, status: "streaming"}
   → NOTIFY → WebSocket → Frontend creates placeholder for event_id X

2. Tokens arrive (high frequency)
   → Event table: {type: "stream.chunk", event_id: X, content: "Analyzing the pod..."}
   → NOTIFY → WebSocket → Frontend appends/replaces content for event_id X
   → NO DB update to TimelineEvent

3. Streaming completes
   → DB: Update TimelineEvent (event_id=X, status=completed, content="<final full text>")
   → DB: Create LLMInteraction (full API details, linked to TimelineEvent)
   → Event table: {type: "timeline_event.completed", event_id: X, content: "<final>"}
   → NOTIFY → WebSocket → Frontend replaces content, marks done
```

**Total DB writes per TimelineEvent: exactly 2** (create + complete).
**Total DB writes per streaming token: 0**.

### Frontend Logic (Trivially Simple)

```typescript
// Frontend WebSocket handler - simple state machine
function handleEvent(event: WSEvent) {
    switch (event.type) {
        case "timeline_event.created":
            // Add new event to timeline (status: streaming)
            timeline.add({ id: event.event_id, status: "streaming", content: "" });
            break;

        case "stream.chunk":
            // Update content for existing event (still streaming)
            timeline.updateContent(event.event_id, event.content);
            break;

        case "timeline_event.completed":
            // Replace with final content, mark as done
            timeline.update(event.event_id, {
                status: "completed",
                content: event.content
            });
            break;
    }
}

// Race condition handling: status only moves forward
// streaming → completed/failed/timed_out/cancelled (never backward)
// If chunk arrives after completed → ignore (stale)
```

**No de-duplication logic needed.** Unlike old TARSy where chunks and DB records were separate concepts, new TARSy's TimelineEvent exists from the start. The `event_id` is the single source of identity. Status transitions are monotonic.

### Why This Is Better Than Old TARSy

| Aspect | Old TARSy | New TARSy |
|--------|-----------|-----------|
| Streaming entity | None (chunks are orphaned) | TimelineEvent (exists from start) |
| De-duplication | Complex (reconcile chunks vs DB record) | Trivial (same event_id, monotonic status) |
| Crash recovery | Partial (hooks may not fire) | Clean (TimelineEvent in `streaming` status = incomplete) |
| Frontend complexity | High (assemble chunks, detect duplicates) | Low (state machine on event_id) |
| DB writes during streaming | 0 (all transient) | Same: 0 for tokens, 2 per event lifecycle |

### Crash Recovery

If a worker crashes mid-streaming:
- TimelineEvent records with `status = streaming` indicate incomplete work
- Orphan detection picks up the session (via `last_interaction_at`)
- On recovery, incomplete TimelineEvents can be marked as `failed`/`timed_out`
- No data loss for completed interactions (already persisted)

### Impact on Worker/Executor Design

The `SessionExecutor` (Phase 3) receives dependencies for progressive writes:

```go
// SessionExecutor writes to DB and publishes events DURING execution
type SessionExecutor interface {
    // Execute processes a session, writing results progressively.
    // The executor creates TimelineEvents, LLMInteractions, MCPInteractions,
    // and publishes WebSocket events during execution - NOT at the end.
    // Returns only the terminal status and any final summary fields.
    Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult
}

// ExecutionResult is lightweight - just the terminal state.
// All intermediate state was already written during execution.
type ExecutionResult struct {
    Status           string  // completed, failed, timed_out, cancelled
    FinalAnalysis    string  // Final analysis text (if completed)
    ExecutiveSummary string  // Executive summary (if completed)
    Error            error   // Error details (if failed/timed_out)
}
```

The worker's role is simple:
1. Claim session (set `in_progress`)
2. Call `executor.Execute(ctx, session)` -- executor handles all progressive writes internally
3. Update session terminal status based on `ExecutionResult`

### Cross-Pod Event Delivery

For multi-replica deployments, streaming events must reach WebSocket clients connected to ANY pod, not just the pod running the session. The `Event` table handles this:

```
Worker Pod (processing session)
  → Writes to Event table + NOTIFY
  
All Pods (including non-processing ones)
  → PostgreSQL NOTIFY listener picks up event
  → Broadcasts to local WebSocket clients subscribed to that session

Client connected to Pod B can see real-time updates from session running on Pod A.
```

Event table records are automatically cleaned up on session completion (see Event Cleanup Strategy in `phase2-database-persistence-design.md`).

### Heartbeat for Orphan Detection

Workers periodically update `last_interaction_at` during processing:

```go
// Heartbeat goroutine runs alongside session execution
func (w *Worker) runHeartbeat(ctx context.Context, sessionID string) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            w.client.AlertSession.UpdateOneID(sessionID).
                SetLastInteractionAt(time.Now()).
                Exec(ctx)
        }
    }
}
```

This ensures orphan detection can distinguish between "worker is alive and processing" vs "worker crashed" based on `last_interaction_at` freshness.

---

### Q8: Error Handling & Recovery

**Status**: ✅ **DECIDED**

**Context:**
Sessions can fail for many reasons (LLM errors, MCP failures, timeouts, crashes). Error recovery exists at two distinct architectural layers: **session-level** (queue/worker system) and **sub-operation-level** (agent executor).

**Question:**
How should we handle failed sessions and enable recovery?

**Decision**: **Option A - Manual Session-Level Recovery + Sub-Operation Auto-Recovery in Phase 3**

At the **session level** (Phase 2.3: Queue/Worker), failed sessions stay in `failed` status. No automatic session restart. Manual retry via API only. This matches old TARSy's behavior.

At the **sub-operation level** (Phase 3: Agent Framework), the executor implements built-in retry and correction mechanisms WITHIN a session. These are critical for reliability and must match (or improve) old TARSy's proven patterns.

**Rationale:**

### Why No Automatic Session-Level Retry?

1. **Old TARSy doesn't do it** -- sessions that exhaust all sub-operation retries stay in `failed` status
2. **Sub-operation retries handle most transient failures** -- by the time a session fails, it's usually a persistent issue (bad config, unavailable service, etc.)
3. **Avoids retry storms** -- automatic session restart could cascade (e.g., all 5 workers retrying sessions against a down LLM service)
4. **Keeps queue system simple** -- retry logic belongs in the executor, not the queue
5. **Operator control** -- manual retry via API lets operators investigate before retrying

### Sub-Operation Auto-Recovery (Phase 3 Responsibility)

These mechanisms run WITHIN a session, inside the `SessionExecutor`. The queue/worker system doesn't know about them -- it just sees a session in `in_progress` until the executor returns a terminal result.

**Old TARSy's recovery mechanisms that new TARSy MUST replicate:**

#### 1. LLM API Retries

| Error Type | Retries | Backoff | Notes |
|------------|---------|---------|-------|
| Rate limit (HTTP 429) | 3 | Exponential 2^n sec | Extracts retry-after header if available |
| Timeout | 3 | Fixed 5s | Configurable timeout (default 120s) |
| Empty response | 3 | Fixed 3s | Injects error message on final attempt |
| Other API errors | 3 | Exponential | Network errors, 5xx, etc. |

#### 2. ReAct Format Correction (Unlimited, Within Iteration Loop)

When LLM returns a response that doesn't follow ReAct format:
- Parser detects `MALFORMED` response
- Specific feedback generated based on what's missing (e.g., "Action Input without Action", "Thought without action or final answer")
- Format correction reminder sent back to LLM as next message
- LLM gets another chance to respond correctly
- No explicit retry limit -- uses the iteration limit (max 30) as natural bound
- Old TARSy also has recovery heuristics (e.g., `_recover_missing_action()` attempts to find action in malformed output)

#### 3. MCP Tool Call Retries

| Error Type | Action | Retries | Backoff |
|------------|--------|---------|---------|
| HTTP 5xx (server errors) | Retry with new session | 1 | Session reinit (10s timeout) |
| HTTP 404 (session not found) | Retry with new session | 1 | Session reinit |
| HTTP 429 (rate limit) | Retry same session | 1 | Jittered 0.25-0.75s |
| Transport errors (connection reset, DNS) | Retry with new session | 1 | Session reinit |
| Auth errors (401/403) | No retry | 0 | Permanent failure |
| Client errors (4xx except 404/429) | No retry | 0 | Permanent failure |
| JSON-RPC semantic errors | No retry | 0 | Error observation to LLM |
| Operation timeout | No retry | 0 | Permanent failure |

#### 4. Database Operation Retries

| Error Type | Retries | Backoff |
|------------|---------|---------|
| Deadlock (40P01) | 3 | Exponential 0.1s-2s + jitter |
| Serialization failure (40001) | 3 | Exponential 0.1s-2s + jitter |
| Connection exception (08*) | 3 | Exponential 0.1s-2s + jitter |
| Too many connections (53300) | 3 | Exponential 0.1s-2s + jitter |

#### 5. Iteration Safety Guards

| Mechanism | Limit | Action |
|-----------|-------|--------|
| Max iterations | 30 (configurable per agent/chain/stage) | Force conclusion, pause, or fail |
| Consecutive timeouts | 2 | Stop iteration loop |
| Unknown tool | N/A | Error observation sent to LLM with available tools list |

### Error Classification Principle

Old TARSy classifies errors into:
- **Transient** (retry): rate limits, timeouts, empty responses, server errors, transport errors, DB deadlocks
- **Permanent** (no retry): auth errors, client errors, JSON-RPC semantic errors, operation timeouts (already waited long enough)

New TARSy should follow the same classification. The executor handles transient errors internally with retries. Permanent errors bubble up as session failure.

### Impact on Queue/Worker System (Phase 2.3)

Minimal. The queue system only needs:

```go
// Worker sees the terminal result from executor
result := w.sessionExecutor.Execute(ctx, session)

// Update based on whatever the executor decided
// The executor already handled all internal retries
w.updateSessionTerminalStatus(ctx, session, result)
```

The queue system does NOT need:
- Retry count fields on session
- Exponential backoff logic
- Error classification
- Dead letter queue

All of that complexity lives in the executor (Phase 3).

### Manual Retry

No special retry API needed. Users can simply re-send the alert via the existing alert submission endpoint (`POST /api/v1/alerts`) with the original alert data. This creates a brand new session -- the failed session remains in the DB as a historical record for debugging. Each session is a complete, independent unit.

---

### Q9: Multi-Stage Session Handling

**Status**: ✅ **DECIDED**

**Decision**: **Option A - Worker Processes Entire Session**

**Context:**
Sessions have multiple stages (from chain config). Phase 2.3 focuses on claiming sessions, but stage orchestration is complex.

**Question:**
How should workers handle multi-stage sessions?

**Analysis:**

Old TARSy uses exactly this pattern: one worker claims the session and processes all stages sequentially in a single async task (`_execute_chain_stages()` loops through stages in order). There is no per-stage queueing. If a stage fails, the session immediately fails and stops.

Old TARSy also supports **pause/resume** for sessions and parallel agents -- when `max_iterations` is reached and `force_conclusion_at_max_iterations=False`, the session is paused with conversation state saved to DB for later resumption. This adds significant complexity (state serialization, selective parallel agent resume, context reconstruction).

**Decision: Option A with key simplification -- drop pause/resume entirely.**

```go
// SessionExecutor (Phase 3) handles stage orchestration internally.
// The worker only sees: claim → execute → terminal status.
//
// Inside the executor, stages execute sequentially:
//   for _, stageDef := range chainConfig.Stages {
//       stageResult := executor.executeStage(ctx, session, stageDef)
//       if stageResult.Failed() {
//           return &ExecutionResult{Status: stageResult.Status, Error: stageResult.Error}
//       }
//   }
```

**Stage Failure Behavior:**
- If a stage fails/times_out/is cancelled → session immediately stops, no subsequent stages run
- Session status reflects the failure (failed/timed_out/cancelled)
- Same as old TARSy behavior

**Parallel Agents Within a Stage:**
- If an individual parallel agent fails, the stage status is determined by the `success_policy` configuration
- See [Stage Status Aggregation Logic in DB schema questions](phase2-database-schema-questions.md) for full rules
- This is handled by the SessionExecutor (Phase 3), not the queue system

**Max Iteration Limit:**
- When max iterations reached → **always force conclusion** (no pause)
- Old TARSy's `force_conclusion_at_max_iterations` toggle is dropped
- Simplifies the entire flow: no conversation state serialization, no resume logic, no selective parallel agent re-execution

**Why Not Per-Stage Queueing (Option B):**
- Adds unnecessary complexity (stage-level queue entries, coordination between stages)
- No benefit: stages are sequential by design (each stage depends on previous stage's output)
- Old TARSy doesn't do it and it works fine

**Why Not Hybrid with Pause/Resume (Option C):**
- Old TARSy's pause/resume is complex: saving conversation state, reconstructing context on resume, parallel agent selective resume
- Rarely used in practice -- most sessions complete within iteration limits
- Force conclusion at max iterations achieves the same goal with far less complexity
- Can revisit if user demand materializes (but unlikely)

**Impact on Queue/Worker System:**
- No impact on worker code -- worker calls `sessionExecutor.Execute()` and gets back a terminal `ExecutionResult`
- All stage orchestration logic is internal to the SessionExecutor (Phase 3)
- Worker doesn't know or care about stages -- it only handles claiming, heartbeat, terminal status, and cleanup

---

## ⚙️ Medium Priority (Optimization & Features)

### Q10: Worker Count Configuration

**Status**: ✅ **DECIDED**

**Decision**: **Option A - Static Configuration**

Fixed `worker_count` set at startup via config. Simple, predictable resource usage. Kubernetes handles horizontal scaling (more pods) when more throughput is needed. Dynamic in-process scaling adds auto-tuning complexity, oscillation risk, and harder resource reasoning with minimal practical benefit.

---

### Q11: Priority Queue Support

**Status**: ✅ **DECIDED**

**Decision**: **Option A - FIFO Only**

Simple `ORDER BY started_at ASC`. Fair, prevents starvation, no extra schema or logic. Can add a priority field later if the need arises.

---

### Q12: Scheduled Sessions

**Status**: ✅ **DECIDED**

**Decision**: **Option A - No Scheduling**

Sessions processed immediately when created. No delayed execution, no `scheduled_at` field. Can add later if needed.

---

### Q13: Queue Observability

**Status**: ✅ **DECIDED**

**Decision**: **Metrics out of scope. Health checks only (matching old TARSy level).**

Prometheus metrics, dashboards, and alerting rules are deferred to a future phase. Old TARSy doesn't have metrics either. For now, we keep basic health checks (DB connectivity, worker pool running) at the same level as old TARSy. Metrics can be added later as a dedicated effort.

---

### Q14: Session Cancellation

**Status**: ✅ **DECIDED** (covered by Q4)

Already decided in Q4: **Context cancellation** with `CancelFunc` map. API sets DB status to `cancelled` + calls stored `CancelFunc` for immediate context propagation. See Q4 for full details and code examples.

---

### Q15: Testing Database Requirements

**Status**: ✅ **DECIDED**

**Decision**: **Real PostgreSQL for DB tests, no DB for unit tests.**

PostgreSQL infra already exists for local dev and GitHub CI. Integration tests that need DB (claiming, `FOR UPDATE SKIP LOCKED`, concurrency) use real Postgres. Unit tests (worker logic, pool lifecycle, config parsing, etc.) use mocked interfaces and don't need a database at all.

---

## 📊 Low Priority (Out of Scope)

### Q16-Q18: Metrics Dashboard, Multi-Tenant Isolation, Backpressure API

**Status**: ⏸️ **OUT OF SCOPE**

Metrics, multi-tenant queue isolation, and backpressure API are all out of scope for the current new TARSy plan. Old TARSy doesn't have these either. Can be revisited in the future if needed.

---

## Summary of Decisions

### ✅ Critical Decisions (ALL COMPLETE)

All critical architecture questions have been decided:

1. **Q1 - Queue Strategy**: ✅ **Sessions table as queue** (status-based filtering, FIFO with `FOR UPDATE SKIP LOCKED`)
2. **Q2 - Concurrency Control**: ✅ **Database-based hard limits** (COUNT(*) check before claim, no Redis needed)
3. **Q3 - Orphan Detection**: ✅ **All pods run independently** (idempotent recovery, no leader election)
4. **Q4 - Session Timeout**: ✅ **Hierarchical timeouts + manual cancellation** (15m session, 2m LLM/MCP, context propagation)
5. **Q5 - Worker Startup**: ✅ **Start before HTTP server** (clear dependency chain, one-time orphan cleanup)
6. **Q6 - Poll Interval**: ✅ **Fixed 1s + 500ms jitter** (proven pattern from old TARSy, configurable for high-replica deployments)
7. **Q7 - Result Updates**: ✅ **Progressive DB writes + transient streaming** (DB writes on state transitions, NOTIFY/WebSocket for LLM tokens, 2 DB writes per TimelineEvent, no frontend de-duplication)
8. **Q8 - Error Handling**: ✅ **Manual session-level retry + sub-operation auto-recovery in Phase 3** (no automatic session restart, executor handles LLM/MCP/DB retries internally)
9. **Q9 - Multi-Stage Handling**: ✅ **Worker processes entire session** (sequential stages, stage failure = session failure, no pause/resume, always force conclusion at max iterations)

### ⚙️ Medium Priority (Can Defer)

- Q10-Q18: Various optimizations and features

All critical questions are decided. Ready to update the main design document and proceed with implementation.

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
