package queue

import (
	"context"
	"testing"

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
