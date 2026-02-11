package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolRegisterAndCancelSession(t *testing.T) {
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

func TestPoolUnregisterSession(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	_, cancel := context.WithCancel(context.Background())
	pool.RegisterSession("session-1", cancel)

	// Should find it
	assert.True(t, pool.CancelSession("session-1"))

	// Unregister
	pool.UnregisterSession("session-1")

	// Should not find it anymore
	assert.False(t, pool.CancelSession("session-1"))
}

func TestPoolGetActiveSessionIDs(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	// Empty initially
	ids := pool.getActiveSessionIDs()
	assert.Empty(t, ids)

	// Register sessions
	_, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	_, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	pool.RegisterSession("session-a", cancel1)
	pool.RegisterSession("session-b", cancel2)

	ids = pool.getActiveSessionIDs()
	require.Len(t, ids, 2)
	assert.Contains(t, ids, "session-a")
	assert.Contains(t, ids, "session-b")
}

func TestPoolStopTwiceDoesNotPanic(t *testing.T) {
	pool := &WorkerPool{
		stopCh:         make(chan struct{}),
		activeSessions: make(map[string]context.CancelFunc),
	}

	// First call should close the channel without panic.
	pool.Stop()

	// Second call must not panic (sync.Once guards the close).
	assert.NotPanics(t, func() { pool.Stop() })
}

func TestStubExecutor(t *testing.T) {
	executor := NewStubExecutor()

	// Test with valid context
	result := executor.Execute(context.Background(), nil)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.NotEmpty(t, result.FinalAnalysis)
	assert.NotEmpty(t, result.ExecutiveSummary)
	assert.Nil(t, result.Error)
}

func TestStubExecutorCancelled(t *testing.T) {
	executor := NewStubExecutor()

	// Test with cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := executor.Execute(ctx, nil)
	assert.Equal(t, alertsession.StatusCancelled, result.Status)
	assert.Error(t, result.Error)
}

func TestPoolRegisterSessionConcurrency(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	// Register multiple sessions concurrently
	const numSessions = 100
	for i := 0; i < numSessions; i++ {
		go func(id int) {
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			sessionID := fmt.Sprintf("session-%d", id)
			pool.RegisterSession(sessionID, cancel)
		}(i)
	}

	// Give goroutines time to complete
	require.Eventually(t, func() bool {
		pool.mu.RLock()
		defer pool.mu.RUnlock()
		return len(pool.activeSessions) == numSessions
	}, 1*time.Second, 10*time.Millisecond)
}

func TestPoolCancelNonExistentSession(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	// Cancelling a session that was never registered should return false
	assert.False(t, pool.CancelSession("nonexistent-session"))
}

func TestPoolUnregisterNonExistentSession(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	// Unregistering a session that was never registered should not panic
	assert.NotPanics(t, func() {
		pool.UnregisterSession("nonexistent-session")
	})
}

func TestPoolMultipleSessionLifecycle(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	// Register multiple sessions
	sessions := []string{"session-1", "session-2", "session-3"}

	for _, sid := range sessions {
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		pool.RegisterSession(sid, cancel)
	}

	// Verify all registered
	ids := pool.getActiveSessionIDs()
	require.Len(t, ids, 3)

	// Cancel one session
	assert.True(t, pool.CancelSession("session-2"))

	// Unregister it
	pool.UnregisterSession("session-2")

	// Verify only 2 remain
	ids = pool.getActiveSessionIDs()
	require.Len(t, ids, 2)
	assert.Contains(t, ids, "session-1")
	assert.Contains(t, ids, "session-3")
	assert.NotContains(t, ids, "session-2")
}

func TestPoolRegisterSameSessionTwice(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Register session-1 twice with different cancel functions
	pool.RegisterSession("session-1", cancel1)
	pool.RegisterSession("session-1", cancel2) // Should overwrite

	// Cancelling should use the second cancel function
	assert.True(t, pool.CancelSession("session-1"))

	// ctx2 should be cancelled, ctx1 should not
	assert.Error(t, ctx2.Err())
	assert.NoError(t, ctx1.Err())
}

func TestPoolConcurrentCancellation(t *testing.T) {
	pool := &WorkerPool{
		activeSessions: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool.RegisterSession("session-racy", cancel)

	// Try to cancel the same session from multiple goroutines
	const numGoroutines = 10
	results := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			results <- pool.CancelSession("session-racy")
		}()
	}

	// Collect results
	var trueCount int
	for i := 0; i < numGoroutines; i++ {
		if <-results {
			trueCount++
		}
	}

	// All calls should succeed (CancelSession just calls cancel, doesn't remove)
	assert.Equal(t, numGoroutines, trueCount)
	assert.Error(t, ctx.Err())
}

func TestStubExecutorReturnsAnalysis(t *testing.T) {
	executor := NewStubExecutor()

	result := executor.Execute(context.Background(), nil)

	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.NotEmpty(t, result.FinalAnalysis)
	assert.NotEmpty(t, result.ExecutiveSummary)
	assert.Empty(t, result.ExecutiveSummaryError)
	assert.Nil(t, result.Error)
}