package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

const (
	// wrapUpReserveCap is the maximum leftover parent time reserved for a
	// text-only force-conclusion Generate. wrapUpReserve is min(LLMCallTimeout, this).
	wrapUpReserveCap = 3 * time.Minute

	interruptExecution        = "execution interrupted"
	interruptForcedConclusion = "forced conclusion interrupted"
	interruptSubAgentWait     = "sub-agent wait interrupted"
)

func wrapUpReserve(llmCallTimeout time.Duration) time.Duration {
	return min(llmCallTimeout, wrapUpReserveCap)
}

// remainingTime returns time until ctx's deadline. ok is false when ctx has no deadline.
func remainingTime(ctx context.Context) (time.Duration, bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	return time.Until(deadline), true
}

// contextTerminalResult returns a cancelled/timed_out result when the parent
// context is canceled or its deadline has already expired. nil means proceed.
func contextTerminalResult(ctx context.Context, usage agent.TokenUsage, prefix string) *agent.ExecutionResult {
	if errors.Is(ctx.Err(), context.Canceled) {
		return interruptedResult(ctx, agent.ExecutionStatusCancelled, usage, prefix)
	}
	remaining, hasDeadline := remainingTime(ctx)
	if hasDeadline && remaining <= 0 {
		return interruptedResult(ctx, agent.ExecutionStatusTimedOut, usage, prefix)
	}
	if status, done := agent.StatusFromContextErr(ctx); done {
		return interruptedResult(ctx, status, usage, prefix)
	}
	return nil
}

func interruptedResult(ctx context.Context, status agent.ExecutionStatus, usage agent.TokenUsage, prefix string) *agent.ExecutionResult {
	cause := context.Cause(ctx)
	if cause == nil {
		cause = ctx.Err()
	}
	if cause == nil {
		cause = context.DeadlineExceeded
	}
	return &agent.ExecutionResult{
		Status:     status,
		Error:      fmt.Errorf("%s: %w", prefix, cause),
		TokensUsed: usage,
	}
}

// shouldWrapUpOnTimeouts is true when consecutive timeouts happened under a
// clamped iteration budget (stuck near the reserve), not with plenty of time left.
func shouldWrapUpOnTimeouts(remaining, reserve, iterationTimeout time.Duration) bool {
	return remaining-reserve < iterationTimeout
}
