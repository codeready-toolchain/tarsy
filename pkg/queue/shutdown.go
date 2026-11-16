package queue

import (
	"context"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// drainCleanupBuffer is allowed after an executor's grace period for the
// post-cancel drain.
const drainCleanupBuffer = 15 * time.Second

// DrainScoringAndPostInvestigation stops the scoring executor, then the
// post-investigation executor, each waiting up to its shutdown timeout for
// in-flight runs to complete before force-cancelling. Call it after the worker
// pool has stopped. Sequential, so a scoring run that completes during its
// drain still starts its post-investigation agents, which then get their own
// drain window. Either executor may be nil.
func DrainScoringAndPostInvestigation(ctx context.Context, cfg *config.QueueConfig, scoring *ScoringExecutor, postInvestigation *PostInvestigationExecutor) {
	if scoring != nil {
		drainExecutor(ctx, "Scoring", cfg.ScoringShutdownTimeout, scoring.Stop)
	}
	if postInvestigation != nil {
		drainExecutor(ctx, "Post-investigation", cfg.PostInvestigationShutdownTimeout, postInvestigation.Stop)
	}
}

// drainExecutor calls stop with the grace period and waits for it to return,
// up to the grace period plus drainCleanupBuffer.
func drainExecutor(ctx context.Context, name string, gracePeriod time.Duration, stop func(time.Duration)) {
	done := make(chan struct{})
	go func() {
		stop(gracePeriod)
		close(done)
	}()

	drainCtx, cancel := context.WithTimeout(ctx, gracePeriod+drainCleanupBuffer)
	defer cancel()

	select {
	case <-done:
		slog.Info(name + " executor stopped gracefully")
	case <-drainCtx.Done():
		slog.Warn(name + " executor shutdown timeout exceeded")
	}
}
