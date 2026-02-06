package queue

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func testQueueConfig() *config.QueueConfig {
	return &config.QueueConfig{
		WorkerCount:             5,
		MaxConcurrentSessions:   5,
		PollInterval:            1 * time.Second,
		PollIntervalJitter:      500 * time.Millisecond,
		SessionTimeout:          15 * time.Minute,
		GracefulShutdownTimeout: 15 * time.Minute,
		OrphanDetectionInterval: 5 * time.Minute,
		OrphanThreshold:         5 * time.Minute,
	}
}

func TestWorkerPollInterval(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil)

	// Poll interval should be within [base - jitter, base + jitter]
	for i := 0; i < 100; i++ {
		d := w.pollInterval()
		assert.GreaterOrEqual(t, d, 500*time.Millisecond, "poll interval below minimum")
		assert.LessOrEqual(t, d, 1500*time.Millisecond, "poll interval above maximum")
	}
}

func TestWorkerPollIntervalNoJitter(t *testing.T) {
	cfg := testQueueConfig()
	cfg.PollIntervalJitter = 0
	w := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil)

	for i := 0; i < 10; i++ {
		d := w.pollInterval()
		assert.Equal(t, 1*time.Second, d, "poll interval should equal base when jitter is 0")
	}
}

func TestWorkerHealth(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("worker-1", "pod-1", nil, cfg, nil, nil)

	h := w.Health()
	assert.Equal(t, "worker-1", h.ID)
	assert.Equal(t, "idle", h.Status)
	assert.Equal(t, "", h.CurrentSessionID)
	assert.Equal(t, 0, h.SessionsProcessed)

	// Simulate working state
	w.setStatus(WorkerStatusWorking, "session-abc")
	h = w.Health()
	assert.Equal(t, "working", h.Status)
	assert.Equal(t, "session-abc", h.CurrentSessionID)

	// Back to idle
	w.setStatus(WorkerStatusIdle, "")
	h = w.Health()
	assert.Equal(t, "idle", h.Status)
	assert.Equal(t, "", h.CurrentSessionID)
}

func TestExecutionResult(t *testing.T) {
	result := &ExecutionResult{
		Status:        "completed",
		FinalAnalysis: "Test analysis",
	}
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, "Test analysis", result.FinalAnalysis)
	assert.Nil(t, result.Error)
}
