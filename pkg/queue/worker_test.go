package queue

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		HeartbeatInterval:       30 * time.Second,
	}
}

func TestWorkerPollInterval(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil, nil)

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
	w := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil, nil)

	for i := 0; i < 10; i++ {
		d := w.pollInterval()
		assert.Equal(t, 1*time.Second, d, "poll interval should equal base when jitter is 0")
	}
}

func TestWorkerHealth(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("worker-1", "pod-1", nil, cfg, nil, nil, nil)

	h := w.Health()
	assert.Equal(t, "worker-1", h.ID)
	assert.Equal(t, WorkerStatusIdle, h.Status)
	assert.Equal(t, "", h.CurrentSessionID)
	assert.Equal(t, 0, h.SessionsProcessed)

	// Simulate working state
	w.setStatus(WorkerStatusWorking, "session-abc")
	h = w.Health()
	assert.Equal(t, WorkerStatusWorking, h.Status)
	assert.Equal(t, "session-abc", h.CurrentSessionID)

	// Back to idle
	w.setStatus(WorkerStatusIdle, "")
	h = w.Health()
	assert.Equal(t, WorkerStatusIdle, h.Status)
	assert.Equal(t, "", h.CurrentSessionID)
}

func TestWorker_PublishSessionStatusNilPublisher(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("worker-1", "pod-1", nil, cfg, nil, nil, nil)

	// Should not panic with nil eventPublisher
	assert.NotPanics(t, func() {
		w.publishSessionStatus(t.Context(), "session-123", alertsession.StatusInProgress)
	})
	assert.NotPanics(t, func() {
		w.publishSessionStatus(t.Context(), "session-456", alertsession.StatusCompleted)
	})
}

func TestWorker_PublishSessionStatusWithPublisher(t *testing.T) {
	cfg := testQueueConfig()
	pub := &mockEventPublisher{}
	w := NewWorker("worker-1", "pod-1", nil, cfg, nil, nil, pub)

	w.publishSessionStatus(t.Context(), "session-abc", alertsession.StatusInProgress)

	// PublishSessionStatus encapsulates both persistent + transient publish
	assert.Equal(t, 1, pub.sessionStatusCount, "should call PublishSessionStatus once")

	// Verify payload contents
	require.NotNil(t, pub.lastSessionStatus)
	assert.Equal(t, "session.status", pub.lastSessionStatus.Type)
	assert.Equal(t, "session-abc", pub.lastSessionStatus.SessionID)
	assert.Equal(t, alertsession.StatusInProgress, pub.lastSessionStatus.Status)
	assert.NotEmpty(t, pub.lastSessionStatus.Timestamp)
}

// mockEventPublisher implements agent.EventPublisher for unit tests.
type mockEventPublisher struct {
	sessionStatusCount int
	lastSessionStatus  *events.SessionStatusPayload
}

func (m *mockEventPublisher) PublishTimelineCreated(_ context.Context, _ string, _ events.TimelineCreatedPayload) error {
	return nil
}

func (m *mockEventPublisher) PublishTimelineCompleted(_ context.Context, _ string, _ events.TimelineCompletedPayload) error {
	return nil
}

func (m *mockEventPublisher) PublishStreamChunk(_ context.Context, _ string, _ events.StreamChunkPayload) error {
	return nil
}

func (m *mockEventPublisher) PublishSessionStatus(_ context.Context, _ string, payload events.SessionStatusPayload) error {
	m.sessionStatusCount++
	m.lastSessionStatus = &payload
	return nil
}

func (m *mockEventPublisher) PublishStageStatus(_ context.Context, _ string, _ events.StageStatusPayload) error {
	return nil
}

func TestWorkerStopIdempotent(t *testing.T) {
	cfg := testQueueConfig()
	w := NewWorker("worker-1", "pod-1", nil, cfg, nil, nil, nil)

	// First stop should succeed
	assert.NotPanics(t, func() { w.Stop() })

	// Second stop should also succeed (no panic)
	assert.NotPanics(t, func() { w.Stop() })
}

func TestWorkerPollIntervalWithNegativeJitter(t *testing.T) {
	cfg := testQueueConfig()
	cfg.PollInterval = 1 * time.Second
	cfg.PollIntervalJitter = -100 * time.Millisecond
	w := NewWorker("test-worker", "test-pod", nil, cfg, nil, nil, nil)

	// Negative jitter should be treated as zero
	for i := 0; i < 10; i++ {
		d := w.pollInterval()
		assert.Equal(t, 1*time.Second, d)
	}
}
