package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Error code constants are defined in streaming.go as LLMErrorCode values.

// Thresholds: fallback triggers when the consecutive error count reaches this value.
const (
	providerErrorThreshold = 2 // provider_error / invalid_request / transport: fallback after 2 consecutive errors
	partialStreamThreshold = 2 // partial_stream_error: fallback after 2 consecutive errors
)

// FallbackState tracks fallback progress within a single execution.
// Initialized once at the start of Run() and carried through all iterations.
type FallbackState struct {
	OriginalProvider          string
	OriginalBackend           config.LLMBackend
	CurrentProviderIndex      int // -1 = primary, 0+ = index into ResolvedFallbackProviders
	AttemptedProviders        []string
	FallbackReason            string
	ConsecutiveProviderErrors int // counts consecutive provider_error / invalid_request / transport failures
	ConsecutivePartialErrors  int // counts consecutive partial_stream_error
	ClearCacheNeeded          bool
}

// NewFallbackState creates a FallbackState initialized from the current provider.
func NewFallbackState(execCtx *agent.ExecutionContext) *FallbackState {
	return &FallbackState{
		OriginalProvider:     execCtx.Config.LLMProviderName,
		OriginalBackend:      execCtx.Config.LLMBackend,
		CurrentProviderIndex: -1,
		AttemptedProviders:   []string{execCtx.Config.LLMProviderName},
	}
}

// shouldFallback inspects the error, updates internal counters, and returns
// whether a fallback switch should happen NOW. It does NOT advance the provider
// index — that happens in applyFallback.
func (s *FallbackState) shouldFallback(err error, fallbackProviders []agent.ResolvedFallbackEntry) bool {
	if len(fallbackProviders) == 0 {
		return false
	}
	if s.CurrentProviderIndex+1 >= len(fallbackProviders) {
		return false // all fallback providers exhausted
	}

	var poe *PartialOutputError
	if !errors.As(err, &poe) {
		// Not an LLM error (e.g. gRPC transport failure) — no error code to
		// inspect. Treat like a provider_error for fallback purposes.
		s.ConsecutiveProviderErrors++
		s.ConsecutivePartialErrors = 0
		return s.ConsecutiveProviderErrors >= providerErrorThreshold
	}

	if poe.IsLoop {
		return false // loop detection is not a provider issue
	}

	switch poe.Code {
	case LLMErrorMaxRetries:
		// Python already retried 3x — fallback immediately
		return true

	case LLMErrorCredentials:
		// Guaranteed failure — fallback immediately
		return true

	case LLMErrorProviderError, LLMErrorInvalidRequest:
		s.ConsecutiveProviderErrors++
		s.ConsecutivePartialErrors = 0
		return s.ConsecutiveProviderErrors >= providerErrorThreshold

	case LLMErrorPartialStreamError:
		s.ConsecutivePartialErrors++
		s.ConsecutiveProviderErrors = 0
		return s.ConsecutivePartialErrors >= partialStreamThreshold

	default:
		// Unknown code — treat conservatively like provider_error
		s.ConsecutiveProviderErrors++
		s.ConsecutivePartialErrors = 0
		return s.ConsecutiveProviderErrors >= providerErrorThreshold
	}
}

// resetCounters resets consecutive error counters after a successful fallback switch.
func (s *FallbackState) resetCounters() {
	s.ConsecutiveProviderErrors = 0
	s.ConsecutivePartialErrors = 0
}

// HasFallbackOccurred returns true if the provider has been switched from the primary.
func (s *FallbackState) HasFallbackOccurred() bool {
	return s.CurrentProviderIndex >= 0
}

// tryFallback checks whether fallback should trigger for the given error and,
// if so, performs the full provider swap: updates execCtx, records a timeline
// event, and updates the execution record. Returns true if the caller should
// retry the LLM call with the new provider.
//
// Returns false (and does nothing) when:
//   - fallback should not trigger (error code / counters)
//   - all fallback providers are exhausted
//   - the parent context is done
func tryFallback(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	state *FallbackState,
	err error,
	eventSeq *int,
) bool {
	if ctx.Err() != nil {
		return false
	}

	if !state.shouldFallback(err, execCtx.Config.ResolvedFallbackProviders) {
		return false
	}

	// Defensive: shouldFallback already rejects exhausted lists, but guard
	// the slice access in case the two checks ever diverge.
	nextIdx := state.CurrentProviderIndex + 1
	if nextIdx >= len(execCtx.Config.ResolvedFallbackProviders) {
		return false
	}

	entry := execCtx.Config.ResolvedFallbackProviders[nextIdx]

	prevProvider := execCtx.Config.LLMProviderName
	prevBackend := execCtx.Config.LLMBackend

	// Check for native tools that will be lost on backend switch.
	droppedTools := nativeToolsDroppedOnFallback(execCtx.Config.LLMProvider, entry.Backend)
	if len(droppedTools) > 0 {
		slog.Warn("Fallback provider does not support native tools; capabilities will be reduced",
			"session_id", execCtx.SessionID,
			"execution_id", execCtx.ExecutionID,
			"dropped_tools", droppedTools,
			"from_backend", prevBackend,
			"to_backend", entry.Backend,
		)
	}

	// Swap provider in the execution config
	execCtx.Config.LLMProvider = entry.Config
	execCtx.Config.LLMProviderName = entry.ProviderName
	execCtx.Config.LLMBackend = entry.Backend

	state.CurrentProviderIndex = nextIdx
	state.FallbackReason = err.Error()
	state.AttemptedProviders = append(state.AttemptedProviders, entry.ProviderName)
	state.ClearCacheNeeded = true
	state.resetCounters()

	slog.Info("Falling back to next LLM provider",
		"session_id", execCtx.SessionID,
		"execution_id", execCtx.ExecutionID,
		"from_provider", prevProvider,
		"from_backend", prevBackend,
		"to_provider", entry.ProviderName,
		"to_backend", entry.Backend,
		"reason", state.FallbackReason,
	)

	// Record provider_fallback timeline event
	meta := map[string]interface{}{
		"original_provider": prevProvider,
		"original_backend":  string(prevBackend),
		"fallback_provider": entry.ProviderName,
		"fallback_backend":  string(entry.Backend),
		"reason":            state.FallbackReason,
		"attempt":           nextIdx + 1,
	}
	if len(droppedTools) > 0 {
		meta["native_tools_dropped"] = droppedTools
	}
	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeProviderFallback,
		fmt.Sprintf("Provider fallback: %s → %s", prevProvider, entry.ProviderName),
		meta, eventSeq)

	// Update execution record (best-effort — don't block on DB failure)
	if execCtx.Services != nil && execCtx.Services.Stage != nil {
		if updateErr := execCtx.Services.Stage.UpdateExecutionProviderFallback(
			ctx, execCtx.ExecutionID,
			state.OriginalProvider, string(state.OriginalBackend),
			entry.ProviderName, string(entry.Backend),
		); updateErr != nil {
			slog.Warn("Failed to update execution fallback record",
				"execution_id", execCtx.ExecutionID, "error", updateErr)
		}
	}

	return true
}

// nativeToolsDroppedOnFallback returns the list of enabled native tool names
// that will be silently lost when switching to a backend that doesn't support
// them (anything other than google-native). Returns nil when no tools are lost.
func nativeToolsDroppedOnFallback(current *config.LLMProviderConfig, targetBackend config.LLMBackend) []string {
	if targetBackend == config.LLMBackendNativeGemini {
		return nil
	}
	var dropped []string
	for tool, enabled := range current.NativeTools {
		if enabled {
			dropped = append(dropped, string(tool))
		}
	}
	return dropped
}

// consumeClearCache returns the current ClearCacheNeeded value and resets it.
// Call this when building the GenerateInput for the next LLM call.
func (s *FallbackState) consumeClearCache() bool {
	if s.ClearCacheNeeded {
		s.ClearCacheNeeded = false
		return true
	}
	return false
}
