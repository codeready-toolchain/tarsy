package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// WorkerPool manages a pool of queue workers.
type WorkerPool struct {
	podID           string
	client          *ent.Client
	config          *config.QueueConfig
	sessionExecutor SessionExecutor
	workers         []*Worker
	stopCh          chan struct{}
	stopOnce        sync.Once
	wg              sync.WaitGroup

	// Session cancel registry: session_id → cancel function
	activeSessions map[string]context.CancelFunc
	mu             sync.RWMutex
	started        bool

	// Orphan detection state
	orphans orphanState
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(podID string, client *ent.Client, cfg *config.QueueConfig, executor SessionExecutor) *WorkerPool {
	return &WorkerPool{
		podID:           podID,
		client:          client,
		config:          cfg,
		sessionExecutor: executor,
		workers:         make([]*Worker, 0, cfg.WorkerCount),
		stopCh:          make(chan struct{}),
		activeSessions:  make(map[string]context.CancelFunc),
	}
}

// Start spawns worker goroutines and the orphan detection background task.
// It is safe to call multiple times; subsequent calls are no-ops.
func (p *WorkerPool) Start(ctx context.Context) error {
	if p.started {
		slog.Warn("Worker pool already started, ignoring duplicate Start call", "pod_id", p.podID)
		return nil
	}
	p.started = true

	slog.Info("Starting worker pool", "pod_id", p.podID, "worker_count", p.config.WorkerCount)

	for i := 0; i < p.config.WorkerCount; i++ {
		workerID := fmt.Sprintf("%s-worker-%d", p.podID, i)
		worker := NewWorker(workerID, p.podID, p.client, p.config, p.sessionExecutor, p)
		p.workers = append(p.workers, worker)
		worker.Start(ctx)
	}

	// Start orphan detection
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.runOrphanDetection(ctx)
	}()

	slog.Info("Worker pool started")
	return nil
}

// Stop signals all workers to stop and waits for them to finish.
// Workers finish their current sessions before exiting (graceful shutdown).
func (p *WorkerPool) Stop() {
	slog.Info("Stopping worker pool gracefully")

	// Log active sessions
	active := p.getActiveSessionIDs()
	if len(active) > 0 {
		slog.Info("Waiting for active sessions to complete",
			"count", len(active),
			"session_ids", active)
	}

	// Signal all workers to stop (they finish current sessions)
	for _, worker := range p.workers {
		worker.Stop()
	}

	// Signal orphan detection to stop
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()

	slog.Info("Worker pool stopped gracefully")
}

// RegisterSession stores a cancel function for manual cancellation.
func (p *WorkerPool) RegisterSession(sessionID string, cancel context.CancelFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeSessions[sessionID] = cancel
}

// UnregisterSession removes the cancel function when processing ends.
func (p *WorkerPool) UnregisterSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.activeSessions, sessionID)
}

// CancelSession triggers context cancellation for a session on this pod.
// Returns true if the session was found and cancelled on this pod.
func (p *WorkerPool) CancelSession(sessionID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if cancel, ok := p.activeSessions[sessionID]; ok {
		cancel()
		return true
	}
	return false
}

// Health returns the current health status of the pool.
func (p *WorkerPool) Health() *PoolHealth {
	ctx := context.Background()

	queueDepth, errQ := p.client.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusPending),
			alertsession.DeletedAtIsNil(),
		).
		Count(ctx)
	if errQ != nil {
		slog.Error("Failed to query queue depth for health check",
			"pod_id", p.podID,
			"error", errQ)
	}

	activeSessions, errA := p.client.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusInProgress),
			alertsession.PodIDEQ(p.podID),
		).
		Count(ctx)
	if errA != nil {
		slog.Error("Failed to query active sessions for health check",
			"pod_id", p.podID,
			"error", errA)
	}

	workerStats := make([]WorkerHealth, len(p.workers))
	activeWorkers := 0
	for i, worker := range p.workers {
		stats := worker.Health()
		workerStats[i] = stats
		if stats.Status == string(WorkerStatusWorking) {
			activeWorkers++
		}
	}

	// DB errors affect health status - if we can't reach the DB, we're not healthy
	dbHealthy := errQ == nil && errA == nil
	isHealthy := len(p.workers) > 0 && activeSessions <= p.config.MaxConcurrentSessions && dbHealthy

	p.orphans.mu.Lock()
	lastOrphanScan := p.orphans.lastOrphanScan
	orphansRecovered := p.orphans.orphansRecovered
	p.orphans.mu.Unlock()

	var dbError string
	if !dbHealthy {
		if errQ != nil {
			dbError = fmt.Sprintf("queue depth query failed: %v", errQ)
		} else if errA != nil {
			dbError = fmt.Sprintf("active sessions query failed: %v", errA)
		}
	}

	return &PoolHealth{
		IsHealthy:        isHealthy,
		DBReachable:      dbHealthy,
		DBError:          dbError,
		PodID:            p.podID,
		ActiveWorkers:    activeWorkers,
		TotalWorkers:     len(p.workers),
		ActiveSessions:   activeSessions,
		MaxConcurrent:    p.config.MaxConcurrentSessions,
		QueueDepth:       queueDepth,
		WorkerStats:      workerStats,
		LastOrphanScan:   lastOrphanScan,
		OrphansRecovered: orphansRecovered,
	}
}

// getActiveSessionIDs returns IDs of currently processing sessions (for logging).
func (p *WorkerPool) getActiveSessionIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sessions := make([]string, 0, len(p.activeSessions))
	for id := range p.activeSessions {
		sessions = append(sessions, id)
	}
	return sessions
}
