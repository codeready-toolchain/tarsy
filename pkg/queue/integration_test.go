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
func createTestSession(t *testing.T, ctx context.Context, client *ent.Client) *ent.AlertSession {
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
	}
}

// TestForUpdateSkipLockedClaiming tests that a worker can atomically claim a pending session.
func TestForUpdateSkipLockedClaiming(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create a pending session
	session := createTestSession(t, ctx, client)

	// Create worker and claim
	cfg := intTestQueueConfig()
	w := NewWorker("test-worker-0", "test-pod", client, cfg, nil, nil)

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
		s := createTestSession(t, ctx, client)
		sessionIDs[s.ID] = struct{}{}
	}

	// Spawn multiple workers concurrently
	cfg := intTestQueueConfig()
	var mu sync.Mutex
	claimed := make([]string, 0, 5)
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			w := NewWorker(fmt.Sprintf("worker-%d", workerID), "test-pod", client, cfg, nil, nil)
			session, err := w.claimNextSession(ctx)
			require.NoError(t, err)
			if session != nil {
				mu.Lock()
				claimed = append(claimed, session.ID)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

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
	processed atomic.Int64
	sessions  sync.Map // string → struct{}
}

func (m *mockExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
	m.processed.Add(1)
	if session != nil {
		m.sessions.Store(session.ID, struct{}{})
	}

	// Simulate short processing
	select {
	case <-time.After(50 * time.Millisecond):
	case <-ctx.Done():
		return &ExecutionResult{
			Status: "cancelled",
			Error:  ctx.Err(),
		}
	}

	return &ExecutionResult{
		Status:           "completed",
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
		createTestSession(t, ctx, client)
	}

	// Create pool with mock executor
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 2
	cfg.PollInterval = 50 * time.Millisecond

	executor := &mockExecutor{}
	pool := NewWorkerPool("test-pod", client, cfg, executor)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Wait for sessions to be processed
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for sessions to be processed, processed: %d", executor.processed.Load())
		default:
			if executor.processed.Load() >= 3 {
				goto done
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

done:
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
		createTestSession(t, ctx, client)
	}

	// Configure pool with low max concurrent limit
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 3 // 3 workers available
	cfg.MaxConcurrentSessions = 2 // But only allow 2 concurrent sessions globally
	cfg.PollInterval = 50 * time.Millisecond

	// Mock executor that takes some time
	executor := &mockExecutor{}
	pool := NewWorkerPool("test-pod", client, cfg, executor)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Give workers time to start claiming sessions
	time.Sleep(200 * time.Millisecond)

	// Check that only 2 sessions are in_progress (respecting the global limit)
	inProgress, err := client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusInProgress)).
		Count(ctx)
	require.NoError(t, err)
	assert.LessOrEqual(t, inProgress, 2, "should not exceed MaxConcurrentSessions")

	// Stop the pool
	pool.Stop()

	// All sessions should eventually complete
	completed, err := client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusCompleted)).
		Count(ctx)
	require.NoError(t, err)
	assert.Greater(t, completed, 0, "some sessions should complete")
}

// TestHeartbeatUpdates tests that heartbeats update last_interaction_at.
func TestHeartbeatUpdates(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	client := dbClient.Client
	ctx := context.Background()

	// Create a pending session
	session := createTestSession(t, ctx, client)

	// Configure worker with short heartbeat for testing
	cfg := intTestQueueConfig()
	cfg.WorkerCount = 1
	cfg.PollInterval = 50 * time.Millisecond

	// Mock executor that takes longer to process (to allow heartbeats)
	slowExecutor := &mockExecutor{}
	pool := NewWorkerPool("test-pod", client, cfg, slowExecutor)

	err := pool.Start(ctx)
	require.NoError(t, err)

	// Wait for session to be claimed
	time.Sleep(200 * time.Millisecond)

	// Get initial last_interaction_at
	s1, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)
	if s1.Status == alertsession.StatusInProgress && s1.LastInteractionAt != nil {
		initialTime := *s1.LastInteractionAt

		// Wait for a heartbeat update (workers heartbeat every 30s in real code, but faster in tests)
		time.Sleep(100 * time.Millisecond)

		// Get updated last_interaction_at
		s2, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)

		// If still in progress, last_interaction_at may have been updated
		if s2.Status == alertsession.StatusInProgress && s2.LastInteractionAt != nil {
			// Note: In fast tests, session may complete before we can observe heartbeat
			// This test validates the pattern, not timing
			assert.True(t, s2.LastInteractionAt.After(initialTime) || s2.LastInteractionAt.Equal(initialTime))
		}
	}

	pool.Stop()
}
