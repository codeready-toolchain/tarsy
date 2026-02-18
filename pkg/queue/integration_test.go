package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestSession creates an alert session in pending status.
func createTestSession(ctx context.Context, t *testing.T, client *ent.Client) *ent.AlertSession {
	t.Helper()
	session, err := client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("test alert data").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusPending).
		SetAuthor("test-user").
		Save(ctx)
	require.NoError(t, err)
	return session
}

// intTestQueueConfig returns a queue config suitable for integration tests.
func intTestQueueConfig() *config.QueueConfig {
	return &config.QueueConfig{
		WorkerCount:             2,
		MaxConcurrentSessions:   10,
		PollInterval:            100 * time.Millisecond,
		PollIntervalJitter:      0,
		SessionTimeout:          30 * time.Second,
		GracefulShutdownTimeout: 10 * time.Second,
		OrphanDetectionInterval: 1 * time.Second,
		OrphanThreshold:         2 * time.Second,
		HeartbeatInterval:       30 * time.Second,
	}
}

// awaitCondition polls until condition returns true or the timeout elapses.
func awaitCondition(t *testing.T, timeout, interval time.Duration, msg string, condition func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out: %s", msg)
		default:
			if condition() {
				return
			}
			time.Sleep(interval)
		}
	}
}

// TestForUpdateSkipLockedClaiming tests that a worker can atomically claim a pending session.
func TestForUpdateSkipLockedClaiming(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create a pending session
	session := createTestSession(ctx, t, client)

	// Create worker and claim
	cfg := intTestQueueConfig()
	w := NewWorker("test-worker-0", "test-pod", client, cfg, nil, nil, nil, nil)

	claimed, err := w.claimNextSession(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed, "worker should claim the pending session")
	assert.Equal(t, session.ID, claimed.ID)
	assert.Equal(t, alertsession.StatusInProgress, claimed.Status)
	require.NotNil(t, claimed.PodID)
	assert.Equal(t, "test-pod", *claimed.PodID)

	// Second claim should return ErrNoSessionsAvailable
	claimed2, err := w.claimNextSession(ctx)
	assert.ErrorIs(t, err, ErrNoSessionsAvailable)
	assert.Nil(t, claimed2, "no more pending sessions should be available")
}

// TestConcurrentClaimsDifferentSessions tests that concurrent workers claim different sessions.
func TestConcurrentClaimsDifferentSessions(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create multiple pending sessions
	sessionIDs := make(map[string]struct{})
	for i := 0; i < 5; i++ {
		s := createTestSession(ctx, t, client)
		sessionIDs[s.ID] = struct{}{}
	}

	// Spawn multiple workers concurrently
	cfg := intTestQueueConfig()
	var mu sync.Mutex
	claimed := make([]string, 0, 5)
	errCh := make(chan error, 5)
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			w := NewWorker(fmt.Sprintf("worker-%d", workerID), "test-pod", client, cfg, nil, nil, nil, nil)
			session, err := w.claimNextSession(ctx)
			if err != nil {
				errCh <- fmt.Errorf("worker-%d claim failed: %w", workerID, err)
				return
			}
			if session != nil {
				mu.Lock()
				claimed = append(claimed, session.ID)
				mu.Unlock()
			} else {
				errCh <- fmt.Errorf("worker-%d got nil session without error", workerID)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	// Check for errors from goroutines
	for err := range errCh {
		require.NoError(t, err)
	}

	// All 5 sessions should be claimed, each by exactly one worker (no duplicates)
	assert.Len(t, claimed, 5, "all 5 sessions should be claimed")

	// Verify no duplicates
	seen := make(map[string]struct{})
	for _, id := range claimed {
		_, dup := seen[id]
		assert.False(t, dup, "session %s claimed by multiple workers", id)
		seen[id] = struct{}{}
	}

	// All claimed sessions should be from the original set
	for _, id := range claimed {
		_, ok := sessionIDs[id]
		assert.True(t, ok, "claimed session %s was not in original set", id)
	}
}

// TestOrphanRecovery tests that orphaned sessions are detected and recovered.
func TestOrphanRecovery(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create a session that simulates a crash (in_progress with old heartbeat)
	staleBeat := time.Now().Add(-10 * time.Minute) // Way past orphan threshold
	session, err := client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("orphan test data").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusInProgress).
		SetPodID("crashed-pod").
		SetLastInteractionAt(staleBeat).
		SetAuthor("test-user").
		Save(ctx)
	require.NoError(t, err)

	// Run orphan detection
	cfg := intTestQueueConfig()
	cfg.OrphanThreshold = 1 * time.Second // Very short for test

	pool := &WorkerPool{
		podID:  "test-pod",
		client: client,
		config: cfg,
	}

	err = pool.detectAndRecoverOrphans(ctx)
	require.NoError(t, err)

	// Verify session is now timed_out
	updated, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.StatusTimedOut, updated.Status)

	// Verify orphan metrics tracked
	pool.orphans.mu.Lock()
	assert.Equal(t, 1, pool.orphans.orphansRecovered)
	pool.orphans.mu.Unlock()
}

// TestStartupOrphanCleanup tests the one-time startup orphan cleanup.
func TestStartupOrphanCleanup(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	podID := "startup-test-pod"

	// Create sessions that belong to this pod
	for i := 0; i < 3; i++ {
		_, err := client.AlertSession.Create().
			SetID(uuid.New().String()).
			SetAlertData("startup orphan data").
			SetAgentType("test-agent").
			SetAlertType("test-alert").
			SetChainID("test-chain").
			SetStatus(alertsession.StatusInProgress).
			SetPodID(podID).
			SetAuthor("test-user").
			Save(ctx)
		require.NoError(t, err)
	}

	// Also create a session for a different pod (should not be affected)
	otherSession, err := client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("other pod data").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusInProgress).
		SetPodID("other-pod").
		SetAuthor("test-user").
		Save(ctx)
	require.NoError(t, err)

	// Run startup cleanup
	err = CleanupStartupOrphans(ctx, client, podID)
	require.NoError(t, err)

	// Verify this pod's sessions are timed_out (startup orphans are marked as timed_out)
	sessions, err := client.AlertSession.Query().
		Where(alertsession.PodID(podID)).
		All(ctx)
	require.NoError(t, err)
	for _, s := range sessions {
		assert.Equal(t, alertsession.StatusTimedOut, s.Status, "session %s should be timed_out", s.ID)
	}

	// Verify other pod's session is untouched
	other, err := client.AlertSession.Get(ctx, otherSession.ID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.StatusInProgress, other.Status, "other pod's session should be untouched")
}

// mockExecutor counts executions and tracks which sessions were processed.
type mockExecutor struct {
	processed  atomic.Int64
	sessions   sync.Map // string → struct{}
	inProgress atomic.Int64
	releaseCh  chan struct{} // optional: blocks execution until closed
}

func (m *mockExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
	m.processed.Add(1)
	if session != nil {
		m.sessions.Store(session.ID, struct{}{})
	}

	// Track in-progress sessions
	m.inProgress.Add(1)
	defer m.inProgress.Add(-1)

	// If releaseCh is set, block until it's closed (for deterministic tests)
	if m.releaseCh != nil {
		select {
		case <-m.releaseCh:
			// Released, continue
		case <-ctx.Done():
			return &ExecutionResult{
				Status: alertsession.StatusCancelled,
				Error:  ctx.Err(),
			}
		}
	} else {
		// Default behavior: simulate short processing
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return &ExecutionResult{
				Status: alertsession.StatusCancelled,
				Error:  ctx.Err(),
			}
		}
	}

	return &ExecutionResult{
		Status:           alertsession.StatusCompleted,
		FinalAnalysis:    "Mock analysis",
		ExecutiveSummary: "Mock summary",
	}
}

// TestPoolEndToEndWithMockExecutor tests the full worker pool lifecycle.
func TestPoolEndToEndWithMockExecutor(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create pending sessions
	for i := 0; i < 3; i++ {
		createTestSession(ctx, t, client)
	}

	// Create pool with mock executor
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 2
	cfg.PollInterval = 50 * time.Millisecond

	executor := &mockExecutor{}
	pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Wait for sessions to be processed
	awaitCondition(t, 10*time.Second, 100*time.Millisecond,
		fmt.Sprintf("waiting for sessions to be processed, processed: %d", executor.processed.Load()),
		func() bool { return executor.processed.Load() >= 3 })

	// Stop the pool gracefully
	pool.Stop()

	// All sessions should be completed
	sessions, err := client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusCompleted)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, sessions, 3, "all 3 sessions should be completed")

	// Health should show all workers
	health := pool.Health()
	assert.Equal(t, 2, health.TotalWorkers)
}

// TestCapacityLimits tests that the global max concurrent limit is enforced.
func TestCapacityLimits(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create multiple pending sessions
	for i := 0; i < 5; i++ {
		createTestSession(ctx, t, client)
	}

	// Configure pool: use 2 workers matching MaxConcurrentSessions to avoid races
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 2           // Match MaxConcurrentSessions to avoid startup races
	cfg.MaxConcurrentSessions = 2 // Global limit
	cfg.PollInterval = 50 * time.Millisecond
	cfg.OrphanDetectionInterval = 1 * time.Hour // Disable orphan detection during test

	// Mock executor with release channel for deterministic control
	releaseCh := make(chan struct{})
	executor := &mockExecutor{
		releaseCh: releaseCh,
	}
	pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Wait until exactly MaxConcurrentSessions sessions are in progress
	awaitCondition(t, 5*time.Second, 10*time.Millisecond,
		fmt.Sprintf("waiting for %d sessions in progress, got: %d", cfg.MaxConcurrentSessions, executor.inProgress.Load()),
		func() bool { return executor.inProgress.Load() == int64(cfg.MaxConcurrentSessions) })

	// Give the system a moment to stabilize
	time.Sleep(100 * time.Millisecond)

	// Verify exactly MaxConcurrentSessions are in progress (no races with 2 workers)
	assert.Equal(t, int64(cfg.MaxConcurrentSessions), executor.inProgress.Load(),
		"should have exactly MaxConcurrentSessions in progress")

	// Verify the database also shows MaxConcurrentSessions in_progress
	dbInProgress, err := client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusInProgress)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, cfg.MaxConcurrentSessions, dbInProgress, "DB should show MaxConcurrentSessions in_progress")

	// Release executions to complete
	close(releaseCh)

	// Wait for first batch to complete
	awaitCondition(t, 5*time.Second, 10*time.Millisecond,
		fmt.Sprintf("waiting for first batch to complete, in_progress: %d", executor.inProgress.Load()),
		func() bool { return executor.inProgress.Load() == 0 })

	// Workers should now claim remaining sessions (3 more)
	// Wait for all 5 sessions to be processed
	awaitCondition(t, 5*time.Second, 50*time.Millisecond,
		fmt.Sprintf("waiting for all sessions to be processed, processed: %d", executor.processed.Load()),
		func() bool { return executor.processed.Load() >= 5 })

	// Stop the pool
	pool.Stop()

	// Verify all 5 sessions completed
	completedCount, err := client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusCompleted)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, completedCount, "all 5 sessions should complete")
}

// TestHeartbeatUpdates tests that heartbeats update last_interaction_at.
func TestHeartbeatUpdates(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create a pending session
	session := createTestSession(ctx, t, client)

	// Configure pool with short heartbeat interval and blocking executor
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 1
	cfg.PollInterval = 50 * time.Millisecond
	cfg.HeartbeatInterval = 100 * time.Millisecond // Short interval for testing

	// Mock executor that blocks until released (to keep session in_progress)
	releaseCh := make(chan struct{})
	executor := &mockExecutor{
		releaseCh: releaseCh,
	}
	pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Wait for session to be claimed
	awaitCondition(t, 5*time.Second, 10*time.Millisecond,
		"waiting for session to be claimed",
		func() bool {
			s, err := client.AlertSession.Get(ctx, session.ID)
			require.NoError(t, err)
			return s.Status == alertsession.StatusInProgress && s.LastInteractionAt != nil
		})

	// Get initial last_interaction_at
	s1, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, alertsession.StatusInProgress, s1.Status)
	require.NotNil(t, s1.LastInteractionAt)
	initialTime := *s1.LastInteractionAt

	// Wait for at least one heartbeat to occur (heartbeat interval is 100ms)
	time.Sleep(250 * time.Millisecond)

	// Get updated last_interaction_at
	s2, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, alertsession.StatusInProgress, s2.Status, "session should still be in progress")
	require.NotNil(t, s2.LastInteractionAt)

	// Verify heartbeat actually updated the timestamp
	assert.True(t, s2.LastInteractionAt.After(initialTime), "last_interaction_at should be updated by heartbeat")

	// Release executor and stop pool
	close(releaseCh)
	pool.Stop()
}

// nilExecutor returns a nil *ExecutionResult for testing the nil-guard.
type nilExecutor struct {
	blockUntilCtxDone bool
	processed         atomic.Int64
}

func (e *nilExecutor) Execute(ctx context.Context, _ *ent.AlertSession) *ExecutionResult {
	e.processed.Add(1)
	if e.blockUntilCtxDone {
		<-ctx.Done()
	}
	return nil
}

// TestNilExecutionResultGuard tests that a nil *ExecutionResult from
// SessionExecutor.Execute does not panic and is translated into the correct
// terminal status.
func TestNilExecutionResultGuard(t *testing.T) {
	t.Run("nil result without context error marks session failed", func(t *testing.T) {
		dbClient := testdb.NewTestClient(t)
		client := dbClient.Client
		ctx := context.Background()

		session := createTestSession(ctx, t, client)

		cfg := intTestQueueConfig()
		cfg.WorkerCount = 1
		cfg.PollInterval = 50 * time.Millisecond

		executor := &nilExecutor{blockUntilCtxDone: false}
		pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

		require.NoError(t, pool.Start(ctx))

		// Wait for processing
		awaitCondition(t, 5*time.Second, 50*time.Millisecond,
			"waiting for session to be processed",
			func() bool { return executor.processed.Load() >= 1 })

		pool.Stop()

		updated, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusFailed, updated.Status)
		require.NotNil(t, updated.ErrorMessage)
		assert.Contains(t, *updated.ErrorMessage, "executor returned nil result")
	})

	t.Run("nil result with deadline exceeded marks session timed_out", func(t *testing.T) {
		dbClient := testdb.NewTestClient(t)
		client := dbClient.Client
		ctx := context.Background()

		session := createTestSession(ctx, t, client)

		cfg := intTestQueueConfig()
		cfg.WorkerCount = 1
		cfg.PollInterval = 50 * time.Millisecond
		cfg.SessionTimeout = 200 * time.Millisecond

		executor := &nilExecutor{blockUntilCtxDone: true}
		pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

		require.NoError(t, pool.Start(ctx))

		// Wait for processing (must exceed the 200ms timeout)
		awaitCondition(t, 5*time.Second, 50*time.Millisecond,
			"waiting for session to be processed",
			func() bool { return executor.processed.Load() >= 1 })

		// Give the worker time to persist the terminal status
		time.Sleep(100 * time.Millisecond)
		pool.Stop()

		updated, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusTimedOut, updated.Status)
		require.NotNil(t, updated.ErrorMessage)
		assert.Contains(t, *updated.ErrorMessage, "timed out")
		assert.Contains(t, *updated.ErrorMessage, "200ms")
	})

	t.Run("nil result with cancellation marks session cancelled", func(t *testing.T) {
		dbClient := testdb.NewTestClient(t)
		client := dbClient.Client
		ctx := context.Background()

		session := createTestSession(ctx, t, client)

		cfg := intTestQueueConfig()
		cfg.WorkerCount = 1
		cfg.PollInterval = 50 * time.Millisecond
		cfg.SessionTimeout = 30 * time.Second // Long timeout so cancellation wins

		executor := &nilExecutor{blockUntilCtxDone: true}
		pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil)

		require.NoError(t, pool.Start(ctx))

		// Wait for session to be claimed (in_progress)
		awaitCondition(t, 5*time.Second, 10*time.Millisecond,
			"waiting for session to be claimed",
			func() bool {
				s, err := client.AlertSession.Get(ctx, session.ID)
				require.NoError(t, err)
				return s.Status == alertsession.StatusInProgress
			})

		// Cancel the session via the pool (simulates API-triggered cancellation)
		cancelled := pool.CancelSession(session.ID)
		require.True(t, cancelled, "CancelSession should find the active session")

		// Wait for the executor to finish and status to be persisted
		awaitCondition(t, 5*time.Second, 50*time.Millisecond,
			"waiting for session to reach terminal status",
			func() bool {
				s, err := client.AlertSession.Get(ctx, session.ID)
				require.NoError(t, err)
				return s.Status == alertsession.StatusCancelled
			})

		pool.Stop()

		updated, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCancelled, updated.Status)
	})
}
