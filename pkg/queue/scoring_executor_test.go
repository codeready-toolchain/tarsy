package queue

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   alertsession.Status
		expected bool
	}{
		{"completed", alertsession.StatusCompleted, true},
		{"failed", alertsession.StatusFailed, true},
		{"cancelled", alertsession.StatusCancelled, true},
		{"timed_out", alertsession.StatusTimedOut, true},
		{"pending", alertsession.StatusPending, false},
		{"in_progress", alertsession.StatusInProgress, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTerminalStatus(tt.status)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestScoringExecutor_StopIdempotent(t *testing.T) {
	exec := &ScoringExecutor{}

	assert.NotPanics(t, func() { exec.Stop(0) })
	assert.NotPanics(t, func() { exec.Stop(0) })
	assert.True(t, exec.stopped)
}

func TestScoringExecutor_StopWaitsBeforeCancelling(t *testing.T) {
	exec := &ScoringExecutor{
		activeCancels: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an in-flight goroutine that completes quickly.
	exec.wg.Add(1)
	exec.trackCancel("score-1", cancel)
	go func() {
		defer exec.wg.Done()
		time.Sleep(50 * time.Millisecond)
	}()

	start := time.Now()
	exec.Stop(5 * time.Second)
	elapsed := time.Since(start)

	// Should have completed well within the grace period (goroutine takes ~50ms).
	assert.Less(t, elapsed, 1*time.Second)
	// Context should NOT have been cancelled (goroutine finished naturally).
	assert.NoError(t, ctx.Err())
}

func TestScoringExecutor_StopCancelsAfterGracePeriod(t *testing.T) {
	exec := &ScoringExecutor{
		activeCancels: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate an in-flight goroutine that blocks until cancelled.
	exec.wg.Add(1)
	exec.trackCancel("score-1", cancel)
	go func() {
		defer exec.wg.Done()
		<-ctx.Done()
	}()

	gracePeriod := 100 * time.Millisecond
	start := time.Now()
	exec.Stop(gracePeriod)
	elapsed := time.Since(start)

	// Should have waited approximately the grace period before cancelling.
	assert.GreaterOrEqual(t, elapsed, gracePeriod)
	assert.Less(t, elapsed, 2*time.Second)
	// Context should have been cancelled by Stop().
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestScoringExecutor_ScoreSessionAsyncRejectedWhenStopped(t *testing.T) {
	exec := &ScoringExecutor{}
	exec.Stop(0)

	assert.NotPanics(t, func() {
		exec.ScoreSessionAsync("session-123", "auto", true)
	})
}

func TestScoringExecutor_SubmitScoringRejectedWhenStopped(t *testing.T) {
	exec := &ScoringExecutor{}
	exec.Stop(0)

	_, err := exec.SubmitScoring(t.Context(), "session-123", "user", false)
	assert.ErrorIs(t, err, ErrShuttingDown)
}

func TestMapScoringAgentStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   agent.ExecutionStatus
		expected string
	}{
		{"completed", agent.ExecutionStatusCompleted, events.StageStatusCompleted},
		{"failed", agent.ExecutionStatusFailed, events.StageStatusFailed},
		{"timed_out", agent.ExecutionStatusTimedOut, events.StageStatusTimedOut},
		{"cancelled", agent.ExecutionStatusCancelled, events.StageStatusCancelled},
		{"unknown defaults to failed", agent.ExecutionStatus("unknown"), events.StageStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapScoringAgentStatus(tt.status)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestScoringExecutor_PublishScoreUpdatedNilPublisher(t *testing.T) {
	exec := &ScoringExecutor{}

	assert.NotPanics(t, func() {
		exec.publishScoreUpdated("session-123", events.ScoringStatusInProgress)
	})
	assert.NotPanics(t, func() {
		exec.publishScoreUpdated("session-456", events.ScoringStatusMemorizing)
	})
	assert.NotPanics(t, func() {
		exec.publishScoreUpdated("session-456", events.ScoringStatusCompleted)
	})
	assert.NotPanics(t, func() {
		exec.publishScoreUpdated("session-789", events.ScoringStatusFailed)
	})
}

func TestScoringExecutor_PublishScoreUpdatedWithPublisher(t *testing.T) {
	tests := []struct {
		name           string
		scoringStatus  events.ScoringStatus
		expectedStatus events.ScoringStatus
	}{
		{"in_progress", events.ScoringStatusInProgress, events.ScoringStatusInProgress},
		{"memorizing", events.ScoringStatusMemorizing, events.ScoringStatusMemorizing},
		{"completed", events.ScoringStatusCompleted, events.ScoringStatusCompleted},
		{"failed", events.ScoringStatusFailed, events.ScoringStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &mockScoreEventPublisher{}
			exec := &ScoringExecutor{eventPublisher: pub}

			exec.publishScoreUpdated("session-abc", tt.scoringStatus)

			assert.Equal(t, 1, pub.callCount)
			require.NotNil(t, pub.lastPayload)
			assert.Equal(t, events.EventTypeSessionScoreUpdated, pub.lastPayload.Type)
			assert.Equal(t, "session-abc", pub.lastPayload.SessionID)
			assert.Equal(t, tt.expectedStatus, pub.lastPayload.ScoringStatus)
			assert.NotEmpty(t, pub.lastPayload.Timestamp)
		})
	}
}

// mockScoreEventPublisher captures PublishSessionScoreUpdated calls.
type mockScoreEventPublisher struct {
	mockEventPublisher
	callCount   int
	lastPayload *events.SessionScoreUpdatedPayload
}

func (m *mockScoreEventPublisher) PublishSessionScoreUpdated(_ context.Context, _ string, payload events.SessionScoreUpdatedPayload) error {
	m.callCount++
	m.lastPayload = &payload
	return nil
}

func TestScoringExecutor_RunFeedbackReflectorAsyncRejectedWhenStopped(t *testing.T) {
	exec := &ScoringExecutor{}
	exec.Stop(0)

	assert.NotPanics(t, func() {
		exec.RunFeedbackReflectorAsync("session-123", "Great investigation", "accurate")
	})
}

func TestScoringExecutor_RunFeedbackReflectorAsyncNilMemoryService(t *testing.T) {
	exec := &ScoringExecutor{
		activeCancels: make(map[string]context.CancelFunc),
	}

	assert.NotPanics(t, func() {
		exec.RunFeedbackReflectorAsync("session-123", "great work", "accurate")
	})

	assert.NotPanics(t, func() {
		exec.Stop(0)
	})
}

func TestResolveScoringProviderName(t *testing.T) {
	tests := []struct {
		name     string
		defaults *config.Defaults
		chain    *config.ChainConfig
		scoring  *config.ScoringConfig
		expected string
	}{
		{
			name:     "nil everything",
			expected: "",
		},
		{
			name:     "from defaults",
			defaults: &config.Defaults{LLMProvider: "default-provider"},
			expected: "default-provider",
		},
		{
			name:     "defaults.Scoring overrides defaults.LLMProvider",
			defaults: &config.Defaults{LLMProvider: "default-provider", Scoring: &config.ScoringConfig{LLMProvider: "scoring-default"}},
			expected: "scoring-default",
		},
		{
			name:     "chain overrides defaults.Scoring",
			defaults: &config.Defaults{LLMProvider: "default-provider", Scoring: &config.ScoringConfig{LLMProvider: "scoring-default"}},
			chain:    &config.ChainConfig{LLMProvider: "chain-provider"},
			expected: "chain-provider",
		},
		{
			name:     "chain overrides defaults",
			defaults: &config.Defaults{LLMProvider: "default-provider"},
			chain:    &config.ChainConfig{LLMProvider: "chain-provider"},
			expected: "chain-provider",
		},
		{
			name:     "scoring overrides chain",
			defaults: &config.Defaults{LLMProvider: "default-provider"},
			chain:    &config.ChainConfig{LLMProvider: "chain-provider"},
			scoring:  &config.ScoringConfig{LLMProvider: "scoring-provider"},
			expected: "scoring-provider",
		},
		{
			name:     "full hierarchy: scoring overrides chain overrides defaults.Scoring overrides defaults",
			defaults: &config.Defaults{LLMProvider: "default-provider", Scoring: &config.ScoringConfig{LLMProvider: "scoring-default"}},
			chain:    &config.ChainConfig{LLMProvider: "chain-provider"},
			scoring:  &config.ScoringConfig{LLMProvider: "scoring-provider"},
			expected: "scoring-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveScoringProviderName(tt.defaults, tt.chain, tt.scoring)
			assert.Equal(t, tt.expected, got)
		})
	}
}
