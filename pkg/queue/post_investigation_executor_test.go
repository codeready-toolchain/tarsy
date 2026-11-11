package queue

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestEvaluatePostInvestigationGate(t *testing.T) {
	score := func(v int) *int { return &v }

	tests := []struct {
		name       string
		entry      config.PostInvestigationConfig
		totalScore *int
		labels     []string
		want       bool
	}{
		{name: "no gate fields", entry: config.PostInvestigationConfig{}, want: true},
		{name: "no gate fields and unlabeled", entry: config.PostInvestigationConfig{}, labels: nil, want: true},

		{name: "min_score below", entry: config.PostInvestigationConfig{MinScore: score(60)}, totalScore: score(59), want: false},
		{name: "min_score equal", entry: config.PostInvestigationConfig{MinScore: score(60)}, totalScore: score(60), want: true},
		{name: "min_score above", entry: config.PostInvestigationConfig{MinScore: score(60)}, totalScore: score(90), want: true},
		{name: "min_score without score", entry: config.PostInvestigationConfig{MinScore: score(0)}, totalScore: nil, want: false},

		{name: "labels_any hit", entry: config.PostInvestigationConfig{LabelsAny: []string{"action", "noise"}}, labels: []string{"noise"}, want: true},
		{name: "labels_any case-insensitive", entry: config.PostInvestigationConfig{LabelsAny: []string{"noise"}}, labels: []string{"Noise"}, want: true},
		{name: "labels_any miss", entry: config.PostInvestigationConfig{LabelsAny: []string{"action"}}, labels: []string{"watch"}, want: false},
		{name: "labels_any unknown labels", entry: config.PostInvestigationConfig{LabelsAny: []string{"action"}}, labels: nil, want: false},
		{name: "labels_any empty labels", entry: config.PostInvestigationConfig{LabelsAny: []string{"action"}}, labels: []string{}, want: false},

		{name: "labels_none hit", entry: config.PostInvestigationConfig{LabelsNone: []string{"noise"}}, labels: []string{"watch", "NOISE"}, want: false},
		{name: "labels_none miss", entry: config.PostInvestigationConfig{LabelsNone: []string{"noise"}}, labels: []string{"watch"}, want: true},
		{name: "labels_none unlabeled passes", entry: config.PostInvestigationConfig{LabelsNone: []string{"noise"}}, labels: nil, want: true},

		{
			name:       "all conditions pass",
			entry:      config.PostInvestigationConfig{MinScore: score(60), LabelsAny: []string{"action"}, LabelsNone: []string{"noise"}},
			totalScore: score(70),
			labels:     []string{"action"},
			want:       true,
		},
		{
			name:       "one failing condition fails the gate",
			entry:      config.PostInvestigationConfig{MinScore: score(60), LabelsAny: []string{"action"}, LabelsNone: []string{"noise"}},
			totalScore: score(70),
			labels:     []string{"action", "noise"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := evaluatePostInvestigationGate(tt.entry, tt.totalScore, tt.labels)
			assert.Equal(t, tt.want, got)
			if got {
				assert.Empty(t, reason)
			} else {
				assert.NotEmpty(t, reason)
			}
		})
	}
}

func TestPostInvestigationOutcomeFor(t *testing.T) {
	tests := []struct {
		name   string
		status agent.ExecutionStatus
		acted  bool
		want   postInvestigationOutcome
	}{
		{"completed acted", agent.ExecutionStatusCompleted, true, postInvestigationOutcomeCompletedActed},
		{"completed no action", agent.ExecutionStatusCompleted, false, postInvestigationOutcomeCompletedNoAction},
		{"timed out", agent.ExecutionStatusTimedOut, false, postInvestigationOutcomeTimedOut},
		{"cancelled", agent.ExecutionStatusCancelled, false, postInvestigationOutcomeCancelled},
		{"failed", agent.ExecutionStatusFailed, true, postInvestigationOutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, postInvestigationOutcomeFor(tt.status, tt.acted))
		})
	}
}

func TestMaxPostInvestigationTimeout(t *testing.T) {
	t.Run("no entries", func(t *testing.T) {
		cfg := &config.Config{ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"a": {},
		})}
		assert.Equal(t, time.Duration(0), MaxPostInvestigationTimeout(cfg))
	})

	t.Run("default applies to unset timeouts", func(t *testing.T) {
		cfg := &config.Config{ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"a": {PostInvestigation: []config.PostInvestigationConfig{{Name: "x", Timeout: 2 * time.Minute}, {Name: "y"}}},
		})}
		assert.Equal(t, postInvestigationDefaultTimeout, MaxPostInvestigationTimeout(cfg))
	})

	t.Run("longest across chains", func(t *testing.T) {
		cfg := &config.Config{ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			"a": {PostInvestigation: []config.PostInvestigationConfig{{Name: "x", Timeout: 2 * time.Minute}}},
			"b": {PostInvestigation: []config.PostInvestigationConfig{{Name: "y", Timeout: 25 * time.Minute}}},
		})}
		assert.Equal(t, 25*time.Minute, MaxPostInvestigationTimeout(cfg))
	})
}

func TestPostInvestigationExecutor_RunAsyncNoOpWhenStopped(t *testing.T) {
	exec := &PostInvestigationExecutor{}
	exec.Stop(0)

	// A stopped executor must not touch its (nil) dependencies.
	assert.NotPanics(t, func() { exec.RunAsync("session-1", "score-1") })
	exec.wg.Wait()
}

func TestPostInvestigationExecutor_StopIdempotent(t *testing.T) {
	exec := &PostInvestigationExecutor{}

	assert.NotPanics(t, func() { exec.Stop(0) })
	assert.NotPanics(t, func() { exec.Stop(0) })
	assert.True(t, exec.stopped)
}

func TestPostInvestigationExecutor_StopCancelsAfterGracePeriod(t *testing.T) {
	exec := &PostInvestigationExecutor{
		activeCancels: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exec.wg.Add(1)
	exec.trackCancel("stage-1", cancel)
	go func() {
		defer exec.wg.Done()
		<-ctx.Done()
	}()

	gracePeriod := 100 * time.Millisecond
	start := time.Now()
	exec.Stop(gracePeriod)

	assert.GreaterOrEqual(t, time.Since(start), gracePeriod)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)

	// Entries registered after the force-cancel are cancelled immediately.
	lateCtx, lateCancel := context.WithCancel(context.Background())
	defer lateCancel()
	exec.trackCancel("stage-2", lateCancel)
	assert.ErrorIs(t, lateCtx.Err(), context.Canceled)
}
