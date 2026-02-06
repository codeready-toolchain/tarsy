# Phase 2: Queue & Worker System - Detailed Design

**Status**: ✅ Design Complete - All Questions Decided  
**Questions Document**: See `phase2-queue-worker-system-questions.md` for decisions  
**Last Updated**: 2026-02-06

## Overview

This document details the Queue & Worker System design for the new TARSy implementation. The queue system manages asynchronous session processing with database-backed job queuing, worker pool management, and graceful concurrency control.

**Phase 2.3 Scope**: This phase focuses on **queue infrastructure and worker orchestration** (job queueing, worker lifecycle, session claiming, concurrency limits). Agent **instantiation and execution** logic is handled in **Phase 3: Agent Framework**.

**Key Design Principles:**
- Database-backed queue (PostgreSQL as single source of truth)
- Worker pool pattern with graceful lifecycle management
- Session claiming with `FOR UPDATE SKIP LOCKED` for multi-replica safety
- Database-based concurrency limits (COUNT(*) check before claim, no semaphore/Redis)
- Orphan detection and recovery (all pods, idempotent, no leader election)
- Progressive DB writes + transient WebSocket streaming (real-time UX)
- Hierarchical timeouts (session: 15m, LLM/MCP: 2m) with context propagation
- Health monitoring (health checks; Prometheus metrics deferred to future phase)

**Major Design Goals:**
- Support horizontal scaling (multiple replicas/pods)
- Prevent duplicate session processing
- Handle worker crashes gracefully
- Provide clear visibility into queue state
- Enable zero-downtime deployments
- Manage resource utilization (CPU, memory, LLM quota)

**Phase Boundary**:
- **Phase 2.3 (this phase)**: Queue infrastructure, worker pool, session claiming, lifecycle management
- **Phase 3**: Agent execution logic, iteration controllers, LLM integration

---

## Architecture Overview

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         HTTP/WebSocket API                      │
│                    (Alert Submission Endpoint)                  │
└────────────────────────┬────────────────────────────────────────┘
                         │ POST /api/v1/alerts
                         │ { data, alert_type, ... }
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                         AlertService                            │
│  - Validate alert request                                       │
│  - Create session record (status: "pending")                    │
│  - Return session_id immediately (non-blocking)                 │
└────────────────────────┬────────────────────────────────────────┘
                         │ Session created in DB
                         │ status = "pending"
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                    PostgreSQL Database                          │
│                                                                 │
│  alert_sessions table:                                          │
│  ┌──────────┬────────────┬───────────┬──────────────┬────────┐  │
│  │session_id│   status   │ pod_id    │last_interact │ ...    │  │
│  ├──────────┼────────────┼───────────┼──────────────┼────────┤  │
│  │ abc-123  │  pending   │   NULL    │     NULL     │   ...  │  │
│  │ def-456  │in_progress │  pod-1    │  2026-02-05  │   ...  │  │
│  │ ghi-789  │ completed  │  pod-2    │  2026-02-05  │   ...  │  │
│  └──────────┴────────────┴───────────┴──────────────┴────────┘  │
│                                                                 │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         │ Multiple worker processes polling
                         ▼
        ┌────────────────┴─────────────────┬─────────────────────┐
        │                                  │                     │
        ▼                                  ▼                     ▼
┌───────────────┐              ┌───────────────┐      ┌───────────────┐
│   Worker 1    │              │   Worker 2    │      │   Worker N    │
│   (Pod 1)     │              │   (Pod 1)     │      │   (Pod 2)     │
├───────────────┤              ├───────────────┤      ├───────────────┤
│ Poll Queue    │              │ Poll Queue    │      │ Poll Queue    │
│ Claim Session │              │ Claim Session │      │ Claim Session │
│ Process       │              │ Process       │      │ Process       │
│ Release       │              │ Release       │      │ Release       │
└───────────────┘              └───────────────┘      └───────────────┘

Worker Pool (per Go process):
- Configurable worker count (default: 5 per process)
- Each worker is a goroutine with its own polling loop
- Concurrency controlled by database (COUNT(*) check before claim)
```

### Session State Machine

```
┌──────────┐
│ pending  │ ◄─── Initial state (session created)
└─────┬────┘
      │
      │ Worker claims session
      │ (FOR UPDATE SKIP LOCKED + pod_id update)
      ▼
┌──────────────┐
│ in_progress  │ ◄─── Worker processing
└──────┬───────┘
       │
       ├── Execution completes normally
       │   ▼
       │  ┌────────────┬─────────────┬────────────┐
       │  │ completed  │   failed    │ timed_out  │
       │  └────────────┴─────────────┴────────────┘
       │
       ├── User requests cancellation via API
       │   ▼
       │  ┌─────────────┐
       │  │ cancelling  │ ◄─── Intermediate: API sets this, worker detects
       │  └──────┬──────┘
       │         │ Worker detects cancelled context
       │         ▼
       │  ┌─────────────┐
       │  │  cancelled  │
       │  └─────────────┘
       │
       └── Worker crash (no heartbeat)
           ▼
          ┌──────────┐
          │ pending  │ ◄─── Orphan recovery (reset for retry)
          └──────────┘

Terminal States: completed, failed, timed_out, cancelled
(no further processing)
```

**State Transitions:**
- `pending` → `in_progress`: Worker claims session
- `in_progress` → `completed`: Successful execution
- `in_progress` → `failed`: Execution error
- `in_progress` → `timed_out`: Session timeout exceeded (15m default)
- `in_progress` → `cancelling`: User requests cancellation via API
- `cancelling` → `cancelled`: Worker detects cancellation, cleans up
- `in_progress` → `pending`: Orphan recovery (worker crash, no heartbeat)

---

## Database-Backed Queue

### Queue Implementation Strategy

**No Separate Queue Table**: Queue is the `alert_sessions` table itself with status-based queries.

**Rationale:**
- Simpler architecture (fewer tables, no sync issues)
- Single source of truth for session state
- Natural integration with existing session lifecycle
- No queue/session consistency problems
- Follows old TARSy pattern (proven design)

**Queue Query:**

```sql
-- Worker queue polling query
SELECT session_id, alert_data, chain_id, agent_type, alert_type, mcp_selection
FROM alert_sessions
WHERE status = 'pending'
  AND deleted_at IS NULL
ORDER BY started_at ASC  -- FIFO (oldest first)
LIMIT 1
FOR UPDATE SKIP LOCKED;  -- Row-level lock, skip if already locked by another transaction
```

**Key Features:**
- `FOR UPDATE SKIP LOCKED`: PostgreSQL-specific, enables lock-free polling
- `ORDER BY started_at ASC`: FIFO (first in, first out)
- `deleted_at IS NULL`: Respect soft deletes
- `LIMIT 1`: Single session per poll (worker processes one at a time)

### Session Claiming Mechanism

**Two-Phase Claim Process:**

1. **Row-Level Lock** (database transaction-level)
   - `FOR UPDATE SKIP LOCKED` in SELECT query
   - Prevents duplicate claims across processes (locked rows are skipped, not blocked on)
   - Automatically released on transaction commit/rollback

2. **Pod Assignment** (persistent state)
   - Update `pod_id` to current pod identifier
   - Update `last_interaction_at` timestamp
   - Change status from `pending` to `in_progress`

**Claim Transaction:**

```go
// pkg/queue/worker.go

func (w *Worker) claimNextSession(ctx context.Context) (*ent.AlertSession, error) {
    var session *ent.AlertSession
    
    // Start transaction
    err := w.client.WithTx(ctx, func(tx *ent.Tx) error {
        // 1. Acquire row-level lock (FOR UPDATE SKIP LOCKED) + fetch session
        session, err = tx.AlertSession.Query().
            Where(
                alertsession.Status(alertsession.StatusPending),
                alertsession.DeletedAtIsNil(),
            ).
            Order(ent.Asc(alertsession.FieldStartedAt)).
            Limit(1).
            ForUpdate(sql.WithLockAction(sql.SkipLocked)).
            Only(ctx)
        
        if err != nil {
            if ent.IsNotFound(err) {
                return ErrNoSessionsAvailable // No sessions to process
            }
            return err
        }
        
        // 2. Claim session (update pod_id, status, timestamp)
        now := time.Now()
        session, err = session.Update().
            SetStatus(alertsession.StatusInProgress).
            SetPodID(w.podID).
            SetLastInteractionAt(now).
            Save(ctx)
        
        return err
    })
    
    if err != nil {
        return nil, err
    }
    
    return session, nil
}
```

**Safety Guarantees:**
- ✅ No duplicate processing (row-level lock via `FOR UPDATE SKIP LOCKED` prevents race conditions)
- ✅ Atomic claim (transaction ensures consistency)
- ✅ Crash recovery (pod_id + timestamp enable orphan detection)

---

## Worker Pool Design

### Worker Pool Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Go Process (Pod)                       │
│                                                             │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                 WorkerPool Manager                     │ │
│  │  - Spawn/manage worker goroutines                      │ │
│  │  - Graceful shutdown coordination                      │ │
│  │  - Health monitoring                                   │ │
│  │  - Metrics collection                                  │ │
│  └────────────────────────────────────────────────────────┘ │
│                             │                               │
│          ┌──────────────────┼──────────────────┐            │
│          │                  │                  │            │
│          ▼                  ▼                  ▼            │
│  ┌─────────────┐    ┌─────────────┐   ┌─────────────┐       │
│  │  Worker 1   │    │  Worker 2   │   │  Worker N   │       │
│  │  goroutine  │    │  goroutine  │   │  goroutine  │       │
│  └─────────────┘    └─────────────┘   └─────────────┘       │
│          │                  │                  │            │
│          └──────────────────┼──────────────────┘            │
│                             │                               │
│                             ▼                               │
│                    ┌─────────────────┐                      │
│                    │   PostgreSQL    │                      │
│                    │  (Concurrency   │                      │
│                    │   via COUNT(*)) │                      │
│                    └─────────────────┘                      │
└─────────────────────────────────────────────────────────────┘
```

### Worker Lifecycle

```
┌──────────┐
│  Start   │
└────┬─────┘
     │
     ▼
┌──────────────┐
│   Polling    │ ◄──────────┐
└────┬─────────┘            │
     │                      │
     │ Session available?   │
     │                      │
     ├─ No ───► Sleep ──────┘
     │          (500ms-1500ms, jittered)
     │
     ├─ Yes
     │
     ▼
┌──────────────┐
│    Claim     │
│   Session    │
└────┬─────────┘
     │
     │ Claim successful?
     │
     ├─ No ───► Back to Polling
     │          (another worker claimed it)
     │
     ├─ Yes
     │
     ▼
┌──────────────┐
│   Process    │ ◄─── Phase 3: Agent execution logic
│   Session    │      (not implemented yet)
└────┬─────────┘
     │
     │ Processing result?
     │
     ├─ Success ────► Update status: completed
     ├─ Failure ────► Update status: failed
     ├─ Timeout ────► Update status: timed_out
     └─ Cancelled ──► Update status: cancelled
     │
     ▼
┌──────────────┐
│   Release    │
│   Session    │
└────┬─────────┘
     │
     └────► Back to Polling
     
     
     Shutdown Signal?
     │
     ▼
┌──────────────┐
│   Graceful   │
│   Shutdown   │
│  - Finish current session
│  - Release resources
│  - Exit
└──────────────┘
```

### Worker Implementation

```go
// pkg/queue/worker.go

type Worker struct {
    id              string              // Unique worker identifier (e.g., "pod-1-worker-3")
    podID           string              // Pod identifier (e.g., "pod-1")
    client          *ent.Client         // Database client
    config          *config.Config      // Configuration
    sessionExecutor SessionExecutor     // Execution interface (Phase 3)
    pool            *WorkerPool         // Parent pool (for cancel registration)
    stopCh          chan struct{}       // Shutdown signal
    wg              *sync.WaitGroup     // For graceful shutdown
}

func NewWorker(id, podID string, client *ent.Client, cfg *config.Config, executor SessionExecutor, pool *WorkerPool) *Worker {
    return &Worker{
        id:              id,
        podID:           podID,
        client:          client,
        config:          cfg,
        sessionExecutor: executor,
        pool:            pool,
        stopCh:          make(chan struct{}),
        wg:              &sync.WaitGroup{},
    }
}

// Start begins the worker polling loop
func (w *Worker) Start(ctx context.Context) {
    w.wg.Add(1)
    go w.run(ctx)
}

// Stop signals graceful shutdown
func (w *Worker) Stop() {
    close(w.stopCh)
    w.wg.Wait()
}

// run is the main worker loop
func (w *Worker) run(ctx context.Context) {
    defer w.wg.Done()
    
    log := slog.With("worker_id", w.id, "pod_id", w.podID)
    log.Info("Worker started")
    
    for {
        select {
        case <-w.stopCh:
            log.Info("Worker shutting down")
            return
        case <-ctx.Done():
            log.Info("Context cancelled, worker shutting down")
            return
        default:
            // Poll and process
            if err := w.pollAndProcess(ctx); err != nil {
                if errors.Is(err, ErrNoSessionsAvailable) || errors.Is(err, ErrAtCapacity) {
                    // No work available or at global capacity limit, sleep and retry
                    time.Sleep(w.pollInterval())
                    continue
                }
                
                // Log error and continue
                log.Error("Error processing session", "error", err)
                time.Sleep(time.Second) // Brief backoff on error
            }
        }
    }
}

func (w *Worker) pollAndProcess(ctx context.Context) error {
    // 1. Check global capacity (database-based, works across all replicas)
    activeCount, err := w.client.AlertSession.Query().
        Where(alertsession.Status(alertsession.StatusInProgress)).
        Count(ctx)
    if err != nil {
        return fmt.Errorf("checking active sessions: %w", err)
    }
    
    if activeCount >= w.config.Queue.MaxConcurrentSessions {
        return ErrAtCapacity // At global limit, wait before retry
    }
    
    // 2. Claim next session (if still available after capacity check)
    session, err := w.claimNextSession(ctx)
    if err != nil {
        return err
    }
    
    log := slog.With("session_id", session.ID, "worker_id", w.id)
    log.Info("Session claimed")
    
    // 3. Create session context with timeout (hierarchical: session gets 15m total)
    // context.WithTimeout provides BOTH timeout AND a CancelFunc for manual cancellation
    sessionCtx, cancelSession := context.WithTimeout(ctx, w.config.Queue.SessionTimeout)
    defer cancelSession()
    
    // Register cancel function so API can trigger cancellation on this pod
    w.pool.RegisterSession(session.ID, cancelSession)
    defer w.pool.UnregisterSession(session.ID)
    
    // 4. Start heartbeat for orphan detection
    heartbeatCtx, cancelHeartbeat := context.WithCancel(sessionCtx)
    defer cancelHeartbeat()
    go w.runHeartbeat(heartbeatCtx, session.ID)
    
    // 5. Process session (delegate to SessionExecutor - Phase 3)
    // The executor writes progressively to DB and publishes events during execution.
    // It does NOT wait until the end to persist results.
    // The executor also applies sub-operation timeouts (LLM: 2m, MCP: 2m) internally.
    result := w.sessionExecutor.Execute(sessionCtx, session)
    
    // 6. Check if session timed out (context.DeadlineExceeded)
    if result.Status == "" && errors.Is(sessionCtx.Err(), context.DeadlineExceeded) {
        result = &ExecutionResult{Status: "timed_out", Error: fmt.Errorf("session timed out after %v", w.config.Queue.SessionTimeout)}
    }
    
    // 7. Stop heartbeat
    cancelHeartbeat()
    
    // 8. Update session terminal status
    // Note: All intermediate state (TimelineEvents, Interactions, Stages) was already
    // written by the executor during processing. This is just the final status update.
    // Use background context — session context may be cancelled but we still need to persist.
    if err := w.updateSessionTerminalStatus(context.Background(), session, result); err != nil {
        log.Error("Failed to update session terminal status", "error", err)
        return err
    }
    
    // 9. Cleanup transient events (no longer needed for WebSocket delivery)
    if err := w.cleanupSessionEvents(context.Background(), session.ID); err != nil {
        log.Warn("Failed to cleanup session events", "error", err)
        // Non-fatal: TTL cleanup will handle it
    }
    
    log.Info("Session processing complete", "status", result.Status)
    
    return nil
}

func (w *Worker) pollInterval() time.Duration {
    // Configurable poll interval with jitter to avoid thundering herd
    base := w.config.Queue.PollInterval // e.g., 1 second
    jitter := time.Duration(rand.Int63n(int64(2 * w.config.Queue.PollIntervalJitter)))
    return base - w.config.Queue.PollIntervalJitter + jitter
    // Example: 1s - 500ms + [0..1000ms] = [500ms..1500ms]
}

// runHeartbeat periodically updates last_interaction_at for orphan detection.
// This ensures the session appears "alive" while the executor is processing.
func (w *Worker) runHeartbeat(ctx context.Context, sessionID string) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := w.client.AlertSession.UpdateOneID(sessionID).
                SetLastInteractionAt(time.Now()).
                Exec(ctx); err != nil {
                slog.Warn("Heartbeat update failed", "session_id", sessionID, "error", err)
            }
        }
    }
}

// updateSessionTerminalStatus writes the final session status.
// All intermediate state was already persisted by the executor during processing.
func (w *Worker) updateSessionTerminalStatus(ctx context.Context, session *ent.AlertSession, result *ExecutionResult) error {
    update := session.Update().
        SetStatus(result.Status).
        SetCompletedAt(time.Now())
    
    if result.Error != nil {
        update = update.SetErrorMessage(result.Error.Error())
    }
    
    if result.FinalAnalysis != "" {
        update = update.SetFinalAnalysis(result.FinalAnalysis)
    }
    
    if result.ExecutiveSummary != "" {
        update = update.SetExecutiveSummary(result.ExecutiveSummary)
    }
    
    _, err := update.Save(ctx)
    
    // Publish terminal session event for WebSocket clients
    if err == nil {
        w.eventPublisher.PublishSessionCompleted(ctx, session.ID, result.Status)
    }
    
    return err
}

// cleanupSessionEvents removes transient Event records used for WebSocket delivery.
// Events are only needed during active sessions. See Event Cleanup Strategy in
// phase2-database-persistence-design.md for details.
func (w *Worker) cleanupSessionEvents(ctx context.Context, sessionID string) error {
    _, err := w.client.Event.Delete().
        Where(event.SessionIDEQ(sessionID)).
        Exec(ctx)
    return err
}

// SessionExecutor is the interface for session processing (implemented in Phase 3).
//
// The executor owns the ENTIRE session lifecycle internally:
// - Executes all stages sequentially (from chain config)
// - If a stage fails → session stops immediately (no subsequent stages)
// - Parallel agents within a stage follow success_policy for status aggregation
// - Always forces conclusion at max iterations (no pause/resume)
//
// IMPORTANT: The executor writes results PROGRESSIVELY during execution:
// - Creates Stage, AgentExecution records as processing progresses
// - Creates TimelineEvents when LLM/MCP operations start (status: streaming)
// - Updates TimelineEvents when operations complete (status: completed, with final content)
// - Creates LLMInteraction/MCPInteraction records on completion (debug/audit)
// - Publishes events to Event table for WebSocket delivery
// - Streams LLM tokens via transient events (NOTIFY/WebSocket, no DB persistence per token)
//
// The worker only handles: claiming, heartbeat, terminal status update, and event cleanup.
// The executor handles: ALL stage orchestration, progressive writes, and event publishing.
type SessionExecutor interface {
    Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult
}

// ExecutionResult is lightweight - just the terminal state.
// All intermediate state (TimelineEvents, Interactions, Stages) was already
// written to DB by the executor during processing.
type ExecutionResult struct {
    Status           string  // completed, failed, timed_out, cancelled
    FinalAnalysis    string  // Final analysis text (if completed)
    ExecutiveSummary string  // Executive summary (if completed)
    Error            error   // Error details (if failed/timed_out)
}
```

### Worker Pool Manager

```go
// pkg/queue/pool.go

type WorkerPool struct {
    podID           string
    client          *ent.Client
    config          *config.Config
    sessionExecutor SessionExecutor
    workers         []*Worker
    stopCh          chan struct{}
    wg              *sync.WaitGroup
    activeSessions  map[string]context.CancelFunc   // session_id → cancel (for manual cancellation)
    mu              sync.RWMutex                    // Protects activeSessions
}

func NewWorkerPool(podID string, client *ent.Client, cfg *config.Config, executor SessionExecutor) *WorkerPool {
    workerCount := cfg.Queue.WorkerCount // e.g., 5
    
    return &WorkerPool{
        podID:           podID,
        client:          client,
        config:          cfg,
        sessionExecutor: executor,
        workers:         make([]*Worker, 0, workerCount),
        stopCh:          make(chan struct{}),
        wg:              &sync.WaitGroup{},
        activeSessions:  make(map[string]context.CancelFunc),
    }
}

func (p *WorkerPool) Start(ctx context.Context) error {
    slog.Info("Starting worker pool", "pod_id", p.podID, "worker_count", p.config.Queue.WorkerCount)
    
    // Spawn workers
    for i := 0; i < p.config.Queue.WorkerCount; i++ {
        workerID := fmt.Sprintf("%s-worker-%d", p.podID, i)
        worker := NewWorker(workerID, p.podID, p.client, p.config, p.sessionExecutor, p)
        p.workers = append(p.workers, worker)
        worker.Start(ctx)
    }
    
    // Start orphan detection background task
    go p.runOrphanDetection(ctx)
    
    slog.Info("Worker pool started")
    return nil
}

func (p *WorkerPool) Stop() {
    slog.Info("Stopping worker pool gracefully")
    
    // Signal all workers to stop (they'll finish current sessions)
    for _, worker := range p.workers {
        worker.Stop()  // Workers stop claiming new sessions, finish current work
    }
    
    // Log active sessions being completed
    activeSessions := p.getActiveSessions()
    if len(activeSessions) > 0 {
        slog.Info("Waiting for active sessions to complete", 
            "count", len(activeSessions),
            "session_ids", activeSessions)
    }
    
    // Wait for all workers to finish (blocks until WaitGroups complete)
    // This is where we wait for sessions to complete naturally
    p.wg.Wait()
    
    // Close pool shutdown channel
    close(p.stopCh)
    
    slog.Info("Worker pool stopped gracefully")
}

// getActiveSessions returns IDs of currently processing sessions (for logging)
func (p *WorkerPool) getActiveSessions() []string {
    p.mu.RLock()
    defer p.mu.RUnlock()
    sessions := make([]string, 0, len(p.activeSessions))
    for id := range p.activeSessions {
        sessions = append(sessions, id)
    }
    return sessions
}

// RegisterSession stores a cancel function for manual cancellation support.
// Called by the worker when it starts processing a session.
func (p *WorkerPool) RegisterSession(sessionID string, cancel context.CancelFunc) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.activeSessions[sessionID] = cancel
}

// UnregisterSession removes the cancel function when session processing ends.
func (p *WorkerPool) UnregisterSession(sessionID string) {
    p.mu.Lock()
    defer p.mu.Unlock()
    delete(p.activeSessions, sessionID)
}

// CancelSession triggers context cancellation for a session on this pod.
// Called by the API handler via SessionService. If the session is not on this pod,
// this is a no-op (the owning pod detects cancellation via DB status check).
func (p *WorkerPool) CancelSession(sessionID string) bool {
    p.mu.RLock()
    defer p.mu.RUnlock()
    if cancel, ok := p.activeSessions[sessionID]; ok {
        cancel()
        return true
    }
    return false // Session not on this pod
}
```

---

## Concurrency Control

### Configurable Limits

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
  
  # Timeout configuration
  session_timeout: 15m                 # Max session processing time
  graceful_shutdown_timeout: 15m       # Match session_timeout to avoid interrupting sessions
  
  # Orphan detection
  orphan_detection_interval: 5m        # How often to scan for orphans
  orphan_threshold: 10m                # Consider orphaned if no update for N minutes
```

### Database-Based Concurrency (Old TARSy Pattern)

Global concurrency limits are enforced using the database, not in-process semaphores. This provides a **global view across all pods** without additional infrastructure (no Redis, no distributed locks).

**Two-Step Process:**

```go
// Step 1: Check global capacity (COUNT of in_progress sessions)
activeCount, err := w.client.AlertSession.Query().
    Where(alertsession.Status(alertsession.StatusInProgress)).
    Count(ctx)

if activeCount >= w.config.Queue.MaxConcurrentSessions {
    return ErrAtCapacity // Wait before retry
}

// Step 2: Claim session (if still available)
session, err := w.claimNextSession(ctx)
```

**Why This Works:**
- Database provides global view across all pods
- Polling loop naturally coordinates (1s intervals with jitter)
- Small race window (check → claim) but self-correcting: next poll cycle corrects any overshoot
- Slight overshoot possible (e.g., 6 instead of 5) but acceptable and temporary
- No Redis or additional infrastructure needed
- Proven pattern from old TARSy

**Concurrency Strategy:**
- **Global Limit**: Database `COUNT(*) WHERE status = 'in_progress'` provides cross-replica enforcement
- **No Per-Process Semaphore**: Database check replaces in-process coordination (simpler, globally consistent)
- **Resource Protection**: Prevents memory exhaustion, LLM quota burnout
- **Self-Correcting**: Any overshoot from check→claim race window corrects on next poll cycle

### Multi-Replica Coordination

**Challenge**: Multiple pods/replicas running simultaneously

**Solution**: Database-level coordination

```
Pod 1 (5 workers)          Pod 2 (5 workers)          PostgreSQL
─────────────────          ─────────────────          ──────────
Worker 1.1 ────┐                                      
Worker 1.2 ────┼───► Poll queue ───┐                 
Worker 1.3 ────┘                    │                 
                                    └───► SELECT ...  
                        ┌───────────     FOR UPDATE   
                        │                SKIP LOCKED  
                        ▼                             
Worker 2.1 ────┐  Poll queue                         
Worker 2.2 ────┼───► (different session)             
Worker 2.3 ────┘                                      
```

**Key Points:**
- `FOR UPDATE SKIP LOCKED` ensures each session claimed by only one worker
- `pod_id` field tracks which pod owns a session
- Workers from different pods safely compete for work
- No coordination needed between pods (database handles it)

---

## Orphan Detection & Recovery

### Orphan Detection

**Orphan Definition**: Session stuck in `in_progress` with no recent `last_interaction_at` update (indicates worker crash).

**Detection Query:**

```sql
-- Find orphaned sessions
SELECT session_id, pod_id, last_interaction_at
FROM alert_sessions
WHERE status = 'in_progress'
  AND last_interaction_at < NOW() - INTERVAL '10 minutes'
  AND deleted_at IS NULL;
```

**Recovery Action:**

```go
// pkg/queue/orphan.go

func (p *WorkerPool) runOrphanDetection(ctx context.Context) {
    ticker := time.NewTicker(p.config.Queue.OrphanDetectionInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-p.stopCh:
            return
        case <-ticker.C:
            if err := p.detectAndRecoverOrphans(ctx); err != nil {
                slog.Error("Orphan detection failed", "error", err)
            }
        }
    }
}

func (p *WorkerPool) detectAndRecoverOrphans(ctx context.Context) error {
    threshold := time.Now().Add(-p.config.Queue.OrphanThreshold)
    
    orphans, err := p.client.AlertSession.Query().
        Where(
            alertsession.Status(alertsession.StatusInProgress),
            alertsession.LastInteractionAtLT(threshold),
            alertsession.DeletedAtIsNil(),
        ).
        All(ctx)
    
    if err != nil {
        return err
    }
    
    if len(orphans) == 0 {
        return nil
    }
    
    slog.Warn("Detected orphaned sessions", "count", len(orphans))
    
    for _, session := range orphans {
        log := slog.With("session_id", session.ID, "old_pod_id", session.PodID)
        
        // Reset session to pending for retry
        _, err := session.Update().
            SetStatus(alertsession.StatusPending).
            ClearPodID().
            ClearLastInteractionAt().
            Save(ctx)
        
        if err != nil {
            log.Error("Failed to recover orphaned session", "error", err)
            continue
        }
        
        log.Info("Recovered orphaned session")
    }
    
    return nil
}
```

**Safety Considerations:**
- **All pods run orphan detection independently** (no leader election needed)
- Operations are idempotent: `UPDATE status = 'pending' WHERE session_id = ...`
- Race condition safe: Multiple pods detecting same orphan just means redundant updates (harmless)
- No single point of failure (every pod can detect and recover orphans)
- Proven pattern from old TARSy (`history_cleanup_service.py`)

---

## Graceful Shutdown

### Philosophy

**Key Principle**: Do NOT interrupt healthy active sessions during deployments (Kubernetes rolling updates, pod restarts, etc.). Wait for sessions to complete naturally before shutting down.

**Why This Matters:**
- Investigation sessions can take 5-15 minutes to complete
- Interrupting mid-investigation wastes progress and LLM tokens
- Better UX: Users get complete results, not partial/failed sessions
- Deployments are not urgent: Can wait up to 15 minutes for graceful shutdown

### Shutdown Sequence

```
SIGTERM received (e.g., kubectl rollout restart)
     │
     ▼
┌──────────────────────────────────────────────────────┐
│  1. Stop accepting NEW sessions                       │
│     - Workers stop polling/claiming from queue        │
│     - Workers finish CURRENT sessions naturally       │
│     - No interruption of active LLM/MCP interactions  │
└──────────────────────┬─────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────┐
│  2. Wait for workers to complete (with timeout)      │
│     - Wait for WaitGroup (all workers done)           │
│     - Max wait: graceful_shutdown_timeout (15m)       │
│     - Log progress: "Waiting for 3 active sessions"   │
└──────────────────────┬─────────────────────────────────┘
                       │
                       ▼
              Two possible outcomes:
                       │
        ┌──────────────┴──────────────┐
        │                              │
        ▼                              ▼
┌────────────────┐          ┌──────────────────────┐
│ All completed  │          │ Timeout exceeded     │
│ (clean exit)   │          │ (force shutdown)     │
│                │          │                      │
│ - All workers  │          │ - Log incomplete     │
│   finished     │          │   sessions           │
│ - Exit 0       │          │ - Sessions become    │
│                │          │   orphans            │
│                │          │ - Next startup will  │
│                │          │   recover them       │
└────────────────┘          └──────────────────────┘
```

**Implementation:**

```go
// cmd/tarsy/main.go

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
    // Clean up sessions from previous run that may have been in-progress when pod crashed
    // This is separate from periodic orphan detection (which runs every 5m)
    if err := cleanupOrphanedSessions(ctx, dbClient, cfg); err != nil {
        slog.Error("Failed to cleanup orphaned sessions during startup", "error", err)
        // Non-fatal - continue
    }
    
    // 5. Initialize services
    sessionService := services.NewSessionService(dbClient, cfg)
    
    // 6. Create session executor (Phase 3 - stub for now)
    sessionExecutor := executor.NewSessionExecutor(cfg, dbClient)
    
    // 7. Start worker pool (BEFORE HTTP server)
    workerPool := queue.NewWorkerPool(podID, dbClient, cfg, sessionExecutor)
    if err := workerPool.Start(ctx); err != nil {
        log.Fatal("Failed to start worker pool", "error", err)
    }
    
    // 8. Start HTTP server (non-blocking)
    httpServer := api.NewServer(cfg, sessionService, workerPool)
    go func() {
        if err := httpServer.Start(); err != nil && err != http.ErrServerClosed {
            log.Fatal("HTTP server error", "error", err)
        }
    }()
    
    slog.Info("TARSy started successfully")
    
    // 9. Wait for shutdown signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    <-sigCh
    
    slog.Info("Shutdown signal received")
    
    // 10. Graceful shutdown (workers first, then HTTP)
    shutdownCtx, cancel := context.WithTimeout(ctx, cfg.Queue.GracefulShutdownTimeout)
    defer cancel()
    
    done := make(chan struct{})
    go func() {
        // Stop worker pool gracefully:
        // - Workers immediately stop accepting NEW sessions (no more claims)
        // - Workers wait for CURRENT sessions to complete naturally
        // - Timeout after graceful_shutdown_timeout (default: 15m, matches session_timeout)
        // - If timeout exceeded, incomplete sessions become orphans (recovered on next startup)
        // NOTE: Kubernetes terminationGracePeriodSeconds must be >= 900s (15m)
        workerPool.Stop()
        close(done)
    }()
    
    select {
    case <-done:
        slog.Info("Graceful shutdown complete")
    case <-shutdownCtx.Done():
        slog.Warn("Shutdown timeout exceeded, forcing exit - incomplete sessions will be orphan-recovered")
    }
    
    // Stop HTTP server
    if err := httpServer.Shutdown(shutdownCtx); err != nil {
        slog.Error("HTTP server shutdown error", "error", err)
    }
    
    slog.Info("Shutdown complete")
}
```

---

## Progressive Writes & Real-Time Streaming

### Design Philosophy

**Write to DB for state transitions** (crash recovery, queries). **Use NOTIFY/WebSocket for ephemeral streaming** (LLM tokens, progress updates that only matter to live clients).

This mirrors old TARSy's proven architecture but leverages the improved TimelineEvent design to eliminate frontend de-duplication complexity. See `phase2-database-persistence-design.md` for the full TimelineEvent and Event schema design.

### What Gets Written to DB (Immediately, During Processing)

The `SessionExecutor` (Phase 3) writes to the database progressively as processing advances -- NOT at the end:

| When | DB Write | Purpose |
|------|----------|---------|
| Session claimed | `alert_session.status = in_progress` | Crash recovery, orphan detection |
| Heartbeat (every 30s) | `alert_session.last_interaction_at = now()` | Orphan detection freshness |
| Stage starts | Create `Stage` record | State tracking |
| Agent execution starts | Create `AgentExecution` record | State tracking |
| LLM/MCP operation starts | Create `TimelineEvent` (status: `streaming`) | Timeline skeleton, crash recovery |
| LLM/MCP operation completes | Update `TimelineEvent` (status: `completed`, content: final) | Permanent record |
| LLM interaction completes | Create `LLMInteraction` record | Debug/audit |
| MCP interaction completes | Create `MCPInteraction` record | Debug/audit |
| Stage completes | Update `Stage` status | State tracking |
| Session completes | Update `alert_session` (status, final_analysis, etc.) | Final state |

**Total DB writes per TimelineEvent: exactly 2** (create with `streaming` status + update to `completed` with final content).

### What Gets Streamed via NOTIFY/WebSocket Only (Transient)

High-frequency data that only matters to live WebSocket clients:

| Event | Delivery | Why Transient |
|-------|----------|---------------|
| LLM token chunks | Event table → NOTIFY → WebSocket | High frequency (~10-50 tokens/sec), ephemeral |
| Progress indicators | NOTIFY → WebSocket | UX feedback only, no persistence value |

**Total DB writes per streaming token: 0.** Tokens flow through the Event table for cross-pod delivery, then are cleaned up on session completion.

### LLM Token Streaming Flow

```
1. LLM call starts
   → DB: Create TimelineEvent (event_id=X, status=streaming, content="")
   → Event table: {type: "timeline_event.created", event_id: X, status: "streaming"}
   → NOTIFY → WebSocket → Frontend creates placeholder for event_id X

2. Tokens arrive (high frequency, ~10-50 tokens/sec)
   → Event table: {type: "stream.chunk", event_id: X, content: "Analyzing the pod..."}
   → NOTIFY → WebSocket → Frontend appends/replaces content for event_id X
   → NO DB update to TimelineEvent (avoids write amplification)

3. Streaming completes
   → DB: Update TimelineEvent (event_id=X, status=completed, content="<final full text>")
   → DB: Create LLMInteraction (full API details, linked to TimelineEvent)
   → Event table: {type: "timeline_event.completed", event_id: X, content: "<final>"}
   → NOTIFY → WebSocket → Frontend replaces content, marks done
```

### Cross-Pod Event Delivery

For multi-replica deployments, streaming events must reach WebSocket clients connected to ANY pod:

```
Worker Pod A (processing session)
  → Writes to Event table + NOTIFY

All Pods (including Pod B, Pod C)
  → PostgreSQL NOTIFY listener receives event
  → Broadcasts to local WebSocket clients subscribed to that session

Result: Client connected to Pod B sees real-time updates from session running on Pod A.
```

Event table records are automatically cleaned up on session completion (see Event Cleanup Strategy in `phase2-database-persistence-design.md`).

### Frontend Integration (Trivially Simple)

The TimelineEvent design eliminates old TARSy's de-duplication complexity:

```
Old TARSy (complex):
  Stream chunks (transient) → DB record created AFTER streaming → Frontend must reconcile

New TARSy (simple):
  DB record created BEFORE streaming (event_id known) → Stream chunks (reference event_id) → DB record updated
  Frontend just tracks by event_id, status only moves forward: streaming → completed
```

Frontend state machine per event:
1. `timeline_event.created` (status: streaming) → create placeholder by `event_id`
2. `stream.chunk` → append/replace content for `event_id`
3. `timeline_event.completed` → replace content with final, mark done
4. Chunk arrives after `completed` → ignore (stale, status never goes backward)

### Crash Recovery

If a worker crashes mid-processing:
- TimelineEvent records with `status = streaming` → incomplete work (can be marked `failed`/`timed_out` on recovery)
- Completed TimelineEvents and Interactions → already persisted, no data loss
- Session `last_interaction_at` → stale, orphan detection picks it up
- Orphan recovery resets session to `pending` → new worker claims and reprocesses

### Worker Responsibilities (Phase 2.3) vs Executor (Phase 3)

**Worker (this phase):**
- Claim session (set `in_progress`)
- Run heartbeat (update `last_interaction_at` every 30s)
- Call `executor.Execute(ctx, session)`
- Update terminal session status from `ExecutionResult`
- Cleanup transient Event records

**Executor (Phase 3):**
- Create/update Stages, AgentExecutions, TimelineEvents, Messages
- Create LLMInteractions, MCPInteractions on completion
- Publish events to Event table for WebSocket delivery
- Stream LLM tokens via transient events
- Handle timeouts and cancellation within the session context

---

## Health Monitoring (Health Checks Only — Metrics Deferred)

### Health Check Endpoint

```go
// pkg/api/server.go

func (s *Server) healthHandler(c echo.Context) error {
    health := s.workerPool.Health()
    
    if !health.IsHealthy {
        return c.JSON(http.StatusServiceUnavailable, health)
    }
    
    return c.JSON(http.StatusOK, health)
}

// pkg/queue/pool.go

type PoolHealth struct {
    IsHealthy          bool                 `json:"is_healthy"`
    PodID              string               `json:"pod_id"`
    ActiveWorkers      int                  `json:"active_workers"`
    TotalWorkers       int                  `json:"total_workers"`
    ActiveSessions     int                  `json:"active_sessions"`
    MaxConcurrent      int                  `json:"max_concurrent"`
    QueueDepth         int                  `json:"queue_depth"`         // Pending sessions
    WorkerStats        []WorkerHealth       `json:"worker_stats"`
    LastOrphanScan     time.Time            `json:"last_orphan_scan"`
    OrphansRecovered   int                  `json:"orphans_recovered"`
}

type WorkerHealth struct {
    ID                 string    `json:"id"`
    Status             string    `json:"status"`  // "idle", "working", "error"
    CurrentSessionID   string    `json:"current_session_id,omitempty"`
    SessionsProcessed  int       `json:"sessions_processed"`
    LastActivity       time.Time `json:"last_activity"`
}

func (p *WorkerPool) Health() *PoolHealth {
    // Query database for queue depth
    queueDepth, _ := p.client.AlertSession.Query().
        Where(alertsession.Status(alertsession.StatusPending)).
        Count(context.Background())
    
    activeSessions, _ := p.client.AlertSession.Query().
        Where(
            alertsession.Status(alertsession.StatusInProgress),
            alertsession.PodID(p.podID),
        ).
        Count(context.Background())
    
    workerStats := make([]WorkerHealth, len(p.workers))
    activeWorkers := 0
    for i, worker := range p.workers {
        stats := worker.Health()
        workerStats[i] = stats
        if stats.Status != "error" {
            activeWorkers++
        }
    }
    
    isHealthy := activeWorkers == len(p.workers) && activeSessions <= p.config.Queue.MaxConcurrentSessions
    
    return &PoolHealth{
        IsHealthy:        isHealthy,
        PodID:            p.podID,
        ActiveWorkers:    activeWorkers,
        TotalWorkers:     len(p.workers),
        ActiveSessions:   activeSessions,
        MaxConcurrent:    p.config.Queue.MaxConcurrentSessions,
        QueueDepth:       queueDepth,
        WorkerStats:      workerStats,
        LastOrphanScan:   p.lastOrphanScan,
        OrphansRecovered: p.orphansRecovered,
    }
}
```

### Metrics (Deferred)

Prometheus metrics, dashboards, and alerting rules are **out of scope** for Phase 2.3 (decided in Q13). Old TARSy doesn't have metrics either. The health check endpoint above provides sufficient operational visibility for now.

**Future phase** can add:
- `tarsy_worker_sessions_claimed_total` (counter)
- `tarsy_worker_sessions_completed_total` (counter)
- `tarsy_worker_sessions_failed_total` (counter)
- `tarsy_worker_session_duration_seconds` (histogram)
- Queue depth, worker utilization gauges

---

## Testing Strategy

**Decision (Q15):** Real PostgreSQL for DB tests, no DB for unit tests.

### Unit Tests (No Database — Mocked Interfaces)

Unit tests focus on worker logic, pool lifecycle, config parsing, and other logic that can be tested with mocked interfaces. No database required.

```go
// pkg/queue/worker_test.go

func TestWorkerPollInterval(t *testing.T) {
    // Test that poll interval with jitter is within expected range
    cfg := testConfig()
    cfg.Queue.PollInterval = 1 * time.Second
    cfg.Queue.PollIntervalJitter = 500 * time.Millisecond
    
    worker := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil)
    
    for i := 0; i < 100; i++ {
        delay := worker.pollInterval()
        assert.GreaterOrEqual(t, delay, 500*time.Millisecond)
        assert.LessOrEqual(t, delay, 1500*time.Millisecond)
    }
}

func TestWorkerPoolCancelSession(t *testing.T) {
    pool := &WorkerPool{
        activeSessions: make(map[string]context.CancelFunc),
    }
    
    // Register a session
    ctx, cancel := context.WithCancel(context.Background())
    pool.RegisterSession("session-1", cancel)
    
    // Cancel should succeed for registered session
    assert.True(t, pool.CancelSession("session-1"))
    assert.Error(t, ctx.Err()) // Context should be cancelled
    
    // Cancel should return false for unknown session
    assert.False(t, pool.CancelSession("unknown"))
}

func TestExecutionResult(t *testing.T) {
    // Test terminal status handling
    result := &ExecutionResult{
        Status:        "completed",
        FinalAnalysis: "Test analysis",
    }
    assert.Equal(t, "completed", result.Status)
    assert.Nil(t, result.Error)
}
```

### Integration Tests (Real PostgreSQL)

Integration tests that need database features (`FOR UPDATE SKIP LOCKED`, concurrency, claiming) use real Postgres. PostgreSQL infra already exists for local dev and GitHub CI.

```go
// pkg/queue/pool_integration_test.go

func TestWorkerClaimSession(t *testing.T) {
    client := setupTestDB(t) // Real Postgres
    defer client.Close()
    
    // Create test session
    session := createTestSession(t, client, alertsession.StatusPending)
    
    // Create worker
    pool := NewWorkerPool("test-pod", client, testConfig(), nil)
    worker := NewWorker("test-worker", "test-pod", client, testConfig(), nil, pool)
    
    // Claim session
    claimed, err := worker.claimNextSession(context.Background())
    require.NoError(t, err)
    require.NotNil(t, claimed)
    require.Equal(t, session.ID, claimed.ID)
    require.Equal(t, "test-pod", claimed.PodID)
    require.Equal(t, alertsession.StatusInProgress, claimed.Status)
}

func TestWorkerClaimSession_NoSessions(t *testing.T) {
    client := setupTestDB(t)
    defer client.Close()
    
    pool := NewWorkerPool("test-pod", client, testConfig(), nil)
    worker := NewWorker("test-worker", "test-pod", client, testConfig(), nil, pool)
    
    claimed, err := worker.claimNextSession(context.Background())
    require.ErrorIs(t, err, ErrNoSessionsAvailable)
    require.Nil(t, claimed)
}

func TestWorkerClaimSession_Concurrent(t *testing.T) {
    client := setupTestDB(t)
    defer client.Close()
    
    // Create single session
    createTestSession(t, client, alertsession.StatusPending)
    
    // Create two workers (simulating different pods)
    pool1 := NewWorkerPool("pod-1", client, testConfig(), nil)
    pool2 := NewWorkerPool("pod-2", client, testConfig(), nil)
    worker1 := NewWorker("worker-1", "pod-1", client, testConfig(), nil, pool1)
    worker2 := NewWorker("worker-2", "pod-2", client, testConfig(), nil, pool2)
    
    // Race: both try to claim at same time
    var wg sync.WaitGroup
    var claimed1, claimed2 *ent.AlertSession
    var err1, err2 error
    
    wg.Add(2)
    go func() {
        defer wg.Done()
        claimed1, err1 = worker1.claimNextSession(context.Background())
    }()
    go func() {
        defer wg.Done()
        claimed2, err2 = worker2.claimNextSession(context.Background())
    }()
    wg.Wait()
    
    // Exactly one should succeed (FOR UPDATE SKIP LOCKED ensures no duplicates)
    successCount := 0
    if err1 == nil && claimed1 != nil {
        successCount++
    }
    if err2 == nil && claimed2 != nil {
        successCount++
    }
    
    assert.Equal(t, 1, successCount, "Exactly one worker should claim the session")
}

func TestWorkerPool_ProcessSessions(t *testing.T) {
    client := setupTestDB(t)
    defer client.Close()
    
    // Create test sessions
    createTestSessions(t, client, 10)
    
    // Mock executor
    executor := &mockSessionExecutor{
        processingTime: 100 * time.Millisecond,
        result: &ExecutionResult{
            Status:        "completed",
            FinalAnalysis: "Test analysis",
        },
    }
    
    // Start worker pool
    pool := NewWorkerPool("test-pod", client, testConfig(), executor)
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    err := pool.Start(ctx)
    require.NoError(t, err)
    
    // Wait for processing
    time.Sleep(2 * time.Second)
    
    // Stop pool
    pool.Stop()
    
    // Verify all sessions processed
    completed, err := client.AlertSession.Query().
        Where(alertsession.StatusIn(
            alertsession.StatusCompleted,
            alertsession.StatusFailed,
        )).
        Count(ctx)
    
    require.NoError(t, err)
    assert.Equal(t, 10, completed)
}

func TestWorkerPool_OrphanRecovery(t *testing.T) {
    client := setupTestDB(t)
    defer client.Close()
    
    // Create orphaned session (in_progress, old timestamp)
    orphan := createTestSession(t, client, alertsession.StatusInProgress)
    oldTime := time.Now().Add(-15 * time.Minute)
    orphan.Update().
        SetPodID("dead-pod").
        SetLastInteractionAt(oldTime).
        SaveX(context.Background())
    
    // Run orphan detection (no pool start needed, test function directly)
    pool := NewWorkerPool("test-pod", client, testConfig(), nil)
    err := pool.detectAndRecoverOrphans(context.Background())
    require.NoError(t, err)
    
    // Verify session reset to pending
    recovered := client.AlertSession.GetX(context.Background(), orphan.ID)
    assert.Equal(t, alertsession.StatusPending, recovered.Status)
    assert.Nil(t, recovered.PodID)
}
```

---

## Configuration

### Queue Configuration Schema

```go
// pkg/config/queue.go

type QueueConfig struct {
    // Worker pool
    WorkerCount            int           `yaml:"worker_count" validate:"required,min=1,max=50"`
    MaxConcurrentSessions  int           `yaml:"max_concurrent_sessions" validate:"required,min=1"`
    
    // Polling
    PollInterval           time.Duration `yaml:"poll_interval" validate:"required"`
    PollIntervalJitter     time.Duration `yaml:"poll_interval_jitter"`
    
    // Timeouts
    SessionTimeout         time.Duration `yaml:"session_timeout" validate:"required"`
    GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout" validate:"required"`
    
    // Orphan detection
    OrphanDetectionInterval time.Duration `yaml:"orphan_detection_interval" validate:"required"`
    OrphanThreshold         time.Duration `yaml:"orphan_threshold" validate:"required"`
    
    // NOTE: No retry config at session level. Failed sessions stay in "failed" status.
    // Sub-operation retries (LLM, MCP, DB) are handled by SessionExecutor (Phase 3).
    // Manual retry: user re-sends alert via POST /api/v1/alerts to create new session.
    //
    // NOTE: LLM/MCP interaction timeouts (2m each) are Phase 3 executor config,
    // not queue config. The executor applies sub-operation timeouts internally.
}

// Built-in defaults
func DefaultQueueConfig() *QueueConfig {
    return &QueueConfig{
        WorkerCount:             5,
        MaxConcurrentSessions:   5,  // Match worker_count for single-replica default
        PollInterval:            1 * time.Second,      // Match old TARSy
        PollIntervalJitter:      500 * time.Millisecond, // Distribute load across replicas
        SessionTimeout:          15 * time.Minute,
        GracefulShutdownTimeout: 15 * time.Minute,  // Match SessionTimeout for complete graceful shutdown
        OrphanDetectionInterval: 5 * time.Minute,
        OrphanThreshold:         10 * time.Minute,
    }
}
```

### Example Configuration

```yaml
# deploy/config/tarsy.yaml

queue:
  worker_count: 5
  max_concurrent_sessions: 5
  
  poll_interval: 1s
  poll_interval_jitter: 500ms
  
  session_timeout: 15m
  graceful_shutdown_timeout: 15m  # Match session_timeout to avoid interrupting sessions
  
  orphan_detection_interval: 5m
  orphan_threshold: 10m
  
  # No session-level retry config. Failed sessions stay in "failed" status.
  # Users re-send the alert via POST /api/v1/alerts to create a new session.
  # Sub-operation retries (LLM 3x, MCP 1x, DB 3x) handled by executor (Phase 3).
```

**Kubernetes Deployment Configuration:**

When deploying to Kubernetes, ensure `terminationGracePeriodSeconds` matches or exceeds `graceful_shutdown_timeout`:

```yaml
# deploy/kubernetes/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tarsy-backend
spec:
  template:
    spec:
      # CRITICAL: Must be >= graceful_shutdown_timeout (default: 15m = 900s)
      # Allows active sessions to complete during rolling updates
      terminationGracePeriodSeconds: 900
      
      containers:
      - name: backend
        image: tarsy-backend:latest
        # ...
```

**Rationale for 15m Graceful Shutdown:**
- Prevents interrupting healthy sessions during deployments
- Better user experience (complete results, no partial investigations)
- Avoids wasted LLM tokens from interrupted sessions
- Deployments can wait - investigations are valuable
- Orphan recovery becomes safety net, not primary mechanism

**Note on Hierarchical Timeouts:**
The queue config only includes `session_timeout` (global session budget) and `graceful_shutdown_timeout`.
Sub-operation timeouts are a Phase 3 (executor) concern:
- `llm_interaction_timeout: 2m` — per-LLM-call timeout (prevents single stuck call from consuming session budget)
- `mcp_interaction_timeout: 2m` — per-MCP-tool-call timeout (prevents hung tool from blocking)
These are applied by the executor internally via `context.WithTimeout` nested under the session context.
```

---

## Integration with Existing Services

### AlertService Integration

```go
// pkg/services/alert_service.go

// SubmitAlert creates a new session from an alert submission.
// This is the queue entry point — the session starts in "pending" status
// and is picked up by the worker pool automatically.
// Matches old TARSy: POST /api/v1/alerts → create session → return immediately.
func (s *AlertService) SubmitAlert(ctx context.Context, req *SubmitAlertRequest) (*ent.AlertSession, error) {
    // Validate request
    if err := s.validateRequest(req); err != nil {
        return nil, err
    }
    
    // Create session in "pending" status
    // Worker pool will pick it up automatically
    session, err := s.client.AlertSession.Create().
        SetID(generateSessionID()).
        SetAlertData(req.Data).
        SetAlertType(req.AlertType).
        SetStatus(alertsession.StatusPending).  // <-- Queue entry point
        SetStartedAt(time.Now()).
        SetNillableMcpSelection(req.MCP).
        SetNillableRunbookURL(req.Runbook).
        Save(ctx)
    
    if err != nil {
        return nil, fmt.Errorf("failed to create session: %w", err)
    }
    
    // Return immediately (non-blocking)
    // Client can poll GET /api/v1/sessions/:id for status updates
    return session, nil
}
```

### API Integration

```go
// pkg/api/routes.go
//
// API routes follow old TARSy conventions:
//   POST   /api/v1/alerts              — Submit alert (creates session)
//   GET    /api/v1/sessions/:id        — Get session status
//   POST   /api/v1/sessions/:id/cancel — Cancel in-progress session

// pkg/api/alert_handler.go

// POST /api/v1/alerts — Submit an alert for investigation.
// Creates a session in "pending" status. Returns immediately with session_id.
// Matches old TARSy endpoint: POST /api/v1/alerts
func (s *Server) submitAlertHandler(c echo.Context) error {
    var req SubmitAlertRequest
    if err := c.Bind(&req); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, err.Error())
    }
    
    // Create session (non-blocking — worker pool picks it up)
    session, err := s.alertService.SubmitAlert(c.Request().Context(), &req)
    if err != nil {
        return err // Handled by centralized HTTPErrorHandler
    }
    
    // Return immediately (matches old TARSy response)
    return c.JSON(http.StatusAccepted, map[string]any{
        "session_id": session.ID,
        "status":     "queued",
        "message":    "Alert submitted for processing",
    })
}

// pkg/api/session_handler.go

// GET /api/v1/sessions/:id — Get session status and details.
func (s *Server) getSessionStatusHandler(c echo.Context) error {
    sessionID := c.Param("id")
    
    session, err := s.sessionService.GetSession(c.Request().Context(), sessionID)
    if err != nil {
        return echo.NewHTTPError(http.StatusNotFound, "Session not found")
    }
    
    return c.JSON(http.StatusOK, session)
}

// POST /api/v1/sessions/:id/cancel — Cancel an in-progress session.
func (s *Server) cancelSessionHandler(c echo.Context) error {
    sessionID := c.Param("id")
    
    err := s.sessionService.CancelSession(c.Request().Context(), sessionID)
    if err != nil {
        if errors.Is(err, ErrNotCancellable) {
            return echo.NewHTTPError(http.StatusConflict, "Session is not in a cancellable state")
        }
        return err // Handled by centralized HTTPErrorHandler
    }
    
    return c.JSON(http.StatusOK, map[string]any{
        "session_id": sessionID,
        "message":    "Session cancellation requested",
    })
}

// Manual retry: No special endpoint needed.
// Users re-send the alert via POST /api/v1/alerts with the original alert data.
// This creates a brand new session. The failed session remains as historical record.
```

**Cancellation Flow:**
1. API handler calls `SessionService.CancelSession(sessionID)`
2. Service validates session is `in_progress`, sets DB status to `cancelling` (intermediate state)
3. Service calls `WorkerPool.CancelSession(sessionID)` which looks up stored `context.CancelFunc`
4. If session is on this pod: `cancel()` triggers `context.Canceled` propagation through executor
5. If session is on another pod: DB status change is detected by that pod's worker (context check)
6. Executor detects cancelled context, updates entity statuses to `cancelled`, returns terminal result

---

## Implementation Checklist

### Phase 2.3: Queue & Worker System

**Queue Infrastructure:**
- [ ] Define queue configuration schema (QueueConfig struct)
- [ ] Implement database query for claiming sessions (`FOR UPDATE SKIP LOCKED`)
- [ ] Create Worker struct with polling loop
- [ ] Implement session claiming logic (two-phase: lock + pod assignment)
- [ ] Add error handling for queue operations

**Worker Pool:**
- [ ] Create WorkerPool manager struct
- [ ] Implement worker spawning (goroutines)
- [ ] Add database-based concurrency control (COUNT(*) check before claim)
- [ ] Implement graceful shutdown logic
- [ ] Add worker health tracking

**Session Processing:**
- [ ] Define SessionExecutor interface (stub for Phase 3)
- [ ] Implement ExecutionResult struct
- [ ] Create mock executor for testing
- [ ] Add session terminal status update logic
- [ ] Implement session timeout handling
- [ ] Add heartbeat goroutine (update `last_interaction_at` every 30s)
- [ ] Add session event cleanup on completion

**Session Cancellation:**
- [ ] Implement `CancelFunc` map in WorkerPool (session_id → cancel function)
- [ ] Add `CancelSession` method to WorkerPool
- [ ] Implement `CancelSession` in SessionService (DB status update + context cancel)
- [ ] Add `POST /api/v1/sessions/:id/cancel` API handler
- [ ] Handle cross-pod cancellation (DB status detection)
- [ ] Test cancellation propagation through context chain

**Orphan Detection:**
- [ ] Implement orphan detection query
- [ ] Create background orphan recovery task (periodic, every 5m, on all pods)
- [ ] Implement one-time startup orphan cleanup (separate from periodic detection)
- [ ] Add orphan logging
- [ ] Test orphan recovery with simulated crashes

**Health & Monitoring:**
- [ ] Implement health check endpoint
- [ ] Create PoolHealth and WorkerHealth structs
- [ ] Implement queue depth monitoring
- [ ] Add worker activity tracking
- [ ] (Deferred) Prometheus metrics — future phase

**Testing:**
- [ ] Unit tests (mocked interfaces, no DB): poll interval logic, pool lifecycle, cancel registration, config parsing
- [ ] Integration tests (real Postgres): session claiming, FOR UPDATE SKIP LOCKED, concurrent claims, orphan recovery
- [ ] Integration tests: worker pool end-to-end (process sessions with mock executor)
- [ ] Graceful shutdown tests
- [ ] Performance/load tests (future)

**Configuration:**
- [ ] Add queue section to tarsy.yaml.example
- [ ] Define built-in queue defaults
- [ ] Implement queue config validation
- [ ] Document configuration options

**Integration:**
- [ ] Implement AlertService.SubmitAlert (create session with "pending" status)
- [ ] Add POST /api/v1/alerts handler (return 202 Accepted with session_id)
- [ ] Add session status polling endpoint
- [ ] Update main.go to start worker pool
- [ ] Add signal handling for graceful shutdown

**Documentation:**
- [ ] Document queue architecture
- [ ] Create worker pool usage guide
- [ ] Document configuration options
- [ ] Add troubleshooting guide
- [ ] Create operator runbook (orphan detection, scaling, etc.)

---

## Design Decisions

**Database-Backed Queue vs Message Broker**: Using PostgreSQL as queue instead of RabbitMQ/Redis. Rationale: Simpler architecture, single dependency, transactional consistency with session state, sufficient performance for expected load (<1000 sessions/day), follows old TARSy pattern.

**No Separate Queue Table**: Queue is the `alert_sessions` table with status-based filtering. Rationale: Simpler schema, single source of truth, no sync issues, natural lifecycle integration.

**FOR UPDATE SKIP LOCKED**: PostgreSQL-specific lock mechanism for claim safety. Rationale: Prevents duplicate processing without application-level coordination, proven pattern, high performance.

**Pod ID Assignment**: Track which pod owns a session via `pod_id` field. Rationale: Enables orphan detection, supports multi-replica deployments, debugging and observability.

**Distributed Orphan Detection**: All pods run orphan detection independently. Rationale: Simpler than leader election, idempotent operations make races harmless, no single point of failure.

**Poll Interval (1s + 500ms jitter)**: Workers poll database every 1s with ±500ms random jitter. Rationale: Matches proven old TARSy pattern (1s), provides ~0.5s average pickup latency (better UX than 2s), jitter distributes load across replicas to prevent thundering herd, DB load is manageable (3-10 queries/sec for typical 3-10 replica deployments), configurable for high-replica environments (50+).

**Database-Based Concurrency Control**: Global concurrent session limit enforced by `COUNT(*) WHERE status = 'in_progress'` check before claiming. Rationale: Provides global view across all pods without additional infrastructure (no Redis, no semaphore), proven old TARSy pattern. Small check→claim race window is self-correcting (next poll cycle), acceptable slight overshoot.

**Hierarchical Timeouts with Context Propagation**: Session timeout (15m configurable) applied via `context.WithTimeout` at worker level. Sub-operation timeouts (LLM: 2m, MCP: 2m) applied by executor (Phase 3). Manual cancellation via stored `CancelFunc`. Rationale: Go's standard cancellation mechanism, defense in depth (prevents single stuck operation from consuming session budget), different statuses for timeout vs cancellation aid debugging.

**Graceful Shutdown**: Workers finish current sessions before exiting on SIGTERM with timeout matching session timeout (15m). Rationale: Prevents interrupting healthy sessions during deployments (K8s rolling updates), better user experience (complete results), no wasted LLM tokens. Requires `terminationGracePeriodSeconds: 900` in K8s deployment. Orphan recovery handles timeout edge cases.

**FIFO Ordering**: Process sessions in order of creation (`ORDER BY started_at ASC`). Rationale: Fairness, predictability, prevents starvation.

**No Priority Queue**: All sessions have equal priority. Rationale: Keep it simple initially, can add priority field later if needed.

**Manual Session-Level Retry Only**: Failed sessions remain in "failed" status. No special retry API -- users re-send the original alert data to `POST /api/v1/alerts` to create a new independent session. Sub-operation retries (LLM 3x, MCP 1x, ReAct correction, DB 3x) are Phase 3 executor concerns. Rationale: Old TARSy has no automatic session restart. Sub-operation retries handle most transient failures. Avoids retry storms against down services. Keeps queue system simple.

**Progressive DB Writes + Transient Streaming**: Executor writes to DB immediately as each operation completes (not at session end). LLM tokens streamed via NOTIFY/WebSocket only (no DB persistence per token). TimelineEvent created at operation start (status: `streaming`), updated once on completion (status: `completed`, final content). Rationale: 2 DB writes per TimelineEvent (create + complete) vs hundreds for per-token updates. Eliminates old TARSy's frontend de-duplication problem (event_id known from start, status monotonic). Heartbeat every 30s for orphan detection. Crash recovery via `streaming` status detection.

**Worker Owns Entire Session (Sequential Stages)**: Worker claims session and executor processes all stages sequentially. Stage failure immediately stops the session. No per-stage queueing, no pause/resume. Always force conclusion at max iterations. Rationale: Same proven pattern as old TARSy (one worker, sequential stages, immediate failure stop). Dropping pause/resume eliminates significant complexity (conversation state serialization, resume context reconstruction, selective parallel agent re-execution) with minimal feature loss. Parallel agents within a stage follow `success_policy` (defined in DB schema). All stage orchestration is internal to SessionExecutor (Phase 3) -- the worker only sees the terminal result.

---

## Decided Against

**Message Broker (RabbitMQ/Redis)**: Not using external message queue. Rationale: Additional dependency, operational complexity, PostgreSQL sufficient for load, transactional consistency harder to maintain.

**Separate Queue Table**: Not creating `job_queue` table. Rationale: Adds complexity, sync issues between queue and sessions, duplicate state management.

**Leader Election for Orphan Detection**: Not implementing leader election. Rationale: Unnecessary complexity, idempotent orphan recovery safe for concurrent execution, simpler deployment.

**Priority Queue**: Not implementing session priority. Rationale: YAGNI (You Aren't Gonna Need It), adds complexity, FIFO is fair and predictable, can add later if needed.

**Automatic Session-Level Retry**: Not implementing automatic retry at the session level in Phase 2.3. Failed sessions stay in `failed` status. No retry count, no dead letter queue, no exponential backoff in the queue system. Rationale: Old TARSy doesn't do it. Sub-operation retries (LLM 3x, MCP 1x, DB 3x) handle most transient failures internally (Phase 3 executor). By the time a session fails, it's usually a persistent issue. Automatic restart risks retry storms. Manual retry via API gives operators investigation opportunity.

**Per-Process Semaphore**: Not using channel-based semaphore for concurrency control. Rationale: Database-based COUNT(*) check provides global coordination across all pods. Semaphore only limits per-process, not globally. Database approach is simpler (no additional data structure) and matches old TARSy pattern.

**Dynamic Worker Scaling**: Not implementing auto-scaling of worker count. Rationale: Kubernetes handles pod scaling (horizontal), worker count per pod is static config (vertical), complex auto-tuning not needed initially.

**Work Stealing**: Workers don't steal work from each other. Rationale: Database queue naturally distributes work, SKIP LOCKED prevents contention, work stealing adds complexity without clear benefit.

**Adaptive Polling (Backoff)**: Not implementing adaptive backoff (e.g., 2s → 30s when idle). Rationale: Fixed interval is simpler and more predictable, 1s polling adds negligible DB load for typical deployments, consistent latency is better UX, can add later if idle-time optimization becomes important.

**Event-Driven Wake-Up (LISTEN/NOTIFY for polling)**: Not using PostgreSQL LISTEN/NOTIFY for immediate worker wake-up. Rationale: Adds complexity, NOTIFY can be lost (not reliable for polling), 1s polling provides sufficient responsiveness (~0.5s average latency), fixed interval is simpler to reason about and debug.

**Single Update at Session End**: Not deferring all DB writes until session completes. Rationale: Users see nothing for up to 15 minutes, all progress lost on crash, contradicts real-time UX requirement.

**Per-Token DB Updates**: Not updating TimelineEvent content in DB for every LLM token. Rationale: 5-15 concurrent streams at ~10-50 tokens/sec = 50-750 DB writes/sec of pure write amplification. NOTIFY/WebSocket delivers the same real-time UX without DB overhead. Final content written once on completion is sufficient for persistence.

**Dead Letter Queue**: Not creating a separate DLQ table. Rationale: Failed sessions remain queryable in `alert_sessions` table (status: `failed`). No benefit to moving them to a separate table. Manual retry resets status to `pending`. Adds unnecessary schema complexity.

**Per-Stage Queueing**: Not queuing stages individually. Rationale: Stages are sequential by design (each depends on previous stage's output). Per-stage queue entries add coordination complexity with no benefit. Old TARSy processes all stages in a single flow and it works fine.

**Session Pause/Resume**: Not implementing pause/resume at iteration limits. Rationale: Old TARSy's pause/resume is complex (conversation state serialization, context reconstruction, selective parallel agent re-execution) and rarely used. Force conclusion at max iterations achieves the same practical goal with far less complexity. Can revisit if user demand materializes.

**Prometheus Metrics (Phase 2.3)**: Not implementing Prometheus counters/histograms in the queue system. Rationale: Old TARSy doesn't have metrics either. Health check endpoint provides sufficient operational visibility. Metrics can be added as a dedicated effort in a future phase.

**Priority Queue**: Not implementing session priority or priority field. Rationale: YAGNI (You Aren't Gonna Need It), FIFO is fair and predictable, prevents starvation. Can add priority column later if needed.

**Scheduled Sessions**: Not implementing delayed execution or `scheduled_at` field. Rationale: Sessions processed immediately on creation. No use case for delayed processing currently. Can add later if needed.

---

## Questions & Decisions

All design questions have been discussed and decided. See `phase2-queue-worker-system-questions.md` for the complete record of decisions and rationale.

**Summary of critical decisions:**
1. **Q1**: Sessions table as queue (no separate queue table)
2. **Q2**: Database-based concurrency limits (no semaphore/Redis)
3. **Q3**: All pods run orphan detection (no leader election)
4. **Q4**: Hierarchical timeouts + manual cancellation (context propagation)
5. **Q5**: Workers start before HTTP server (with one-time orphan cleanup)
6. **Q6**: Fixed 1s poll + 500ms jitter
7. **Q7**: Progressive DB writes + transient WebSocket streaming
8. **Q8**: Manual session-level retry only (no auto-restart)
9. **Q9**: Worker processes entire session (no pause/resume)
10. **Q10-Q15**: Static worker count, FIFO only, no scheduling, health checks only, real Postgres for tests

---

## Next Steps

1. **Begin Phase 2.3 implementation** ⬅️ NEXT
   - Queue configuration schema (QueueConfig struct + validation)
   - Worker struct and polling loop
   - Session claiming logic (FOR UPDATE SKIP LOCKED)
   - WorkerPool manager with database-based concurrency
2. **Add orphan detection**
   - One-time startup cleanup
   - Periodic background task (every 5m, on all pods)
3. **Implement session lifecycle**
   - Session timeout (context.WithTimeout)
   - Heartbeat goroutine
   - Manual cancellation (CancelFunc map + API)
   - Terminal status update + event cleanup
4. **Add health monitoring**
   - Health check endpoint (PoolHealth, WorkerHealth structs)
5. **Write comprehensive tests**
   - Unit tests: worker logic, pool lifecycle, config parsing (mocked interfaces, no DB)
   - Integration tests: claiming, FOR UPDATE SKIP LOCKED, concurrency (real Postgres)
6. **Integrate with services**
   - Implement AlertService.SubmitAlert (pending status)
   - Add API handlers (POST /api/v1/alerts, GET /api/v1/sessions/:id, POST /api/v1/sessions/:id/cancel)
   - Update main.go (startup sequence, signal handling)
7. **Document system**
   - Configuration reference
   - Operator runbook

---

## References

- PostgreSQL Row-Level Locks (FOR UPDATE SKIP LOCKED): https://www.postgresql.org/docs/current/explicit-locking.html
- FOR UPDATE SKIP LOCKED: https://www.2ndquadrant.com/en/blog/what-is-select-skip-locked-for-in-postgresql-9-5/
- Go Worker Pool Pattern: https://gobyexample.com/worker-pools
- Graceful Shutdown in Go: https://pkg.go.dev/os/signal
- Old TARSy Queue: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/orchestration/`
