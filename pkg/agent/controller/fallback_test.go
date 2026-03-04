package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fallbackProviders() []agent.ResolvedFallbackEntry {
	return []agent.ResolvedFallbackEntry{
		{
			ProviderName: "fallback-1",
			Backend:      config.LLMBackendNativeGemini,
			Config:       &config.LLMProviderConfig{Model: "fallback-model-1"},
		},
		{
			ProviderName: "fallback-2",
			Backend:      config.LLMBackendLangChain,
			Config:       &config.LLMProviderConfig{Model: "fallback-model-2"},
		},
	}
}

func newTestFallbackState() *FallbackState {
	return &FallbackState{
		OriginalProvider:     "primary-provider",
		OriginalBackend:      config.LLMBackendLangChain,
		CurrentProviderIndex: -1,
		AttemptedProviders:   []string{"primary-provider"},
	}
}

func makePartialError(code LLMErrorCode) error {
	return &PartialOutputError{
		Cause:     fmt.Errorf("test error (code: %s)", code),
		Code:      code,
		Retryable: false,
	}
}

// ────────────────────────────────────────────────────────────
// shouldFallback tests
// ────────────────────────────────────────────────────────────

func TestShouldFallback_MaxRetries_Immediate(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorMaxRetries), providers)
	assert.True(t, result, "max_retries should trigger immediate fallback")
}

func TestShouldFallback_Credentials_Immediate(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorCredentials), providers)
	assert.True(t, result, "credentials should trigger immediate fallback")
}

func TestShouldFallback_ProviderError_AfterOneRetry(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	// First occurrence — should NOT trigger fallback (let Go retry)
	result := state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.False(t, result, "first provider_error should not trigger fallback")

	// Second consecutive — should trigger
	result = state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.True(t, result, "second consecutive provider_error should trigger fallback")
}

func TestShouldFallback_InvalidRequest_AfterOneRetry(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorInvalidRequest), providers)
	assert.False(t, result, "first invalid_request should not trigger fallback")

	result = state.shouldFallback(makePartialError(LLMErrorInvalidRequest), providers)
	assert.True(t, result, "second consecutive invalid_request should trigger fallback")
}

func TestShouldFallback_PartialStreamError_AfterTwo(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorPartialStreamError), providers)
	assert.False(t, result, "first partial_stream_error should not trigger")

	result = state.shouldFallback(makePartialError(LLMErrorPartialStreamError), providers)
	assert.True(t, result, "second consecutive partial_stream_error should trigger")
}

func TestShouldFallback_LoopError_NeverTriggers(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	loopErr := &PartialOutputError{
		Cause:  fmt.Errorf("loop detected"),
		IsLoop: true,
		Code:   LLMErrorPartialStreamError,
	}

	for i := 0; i < 5; i++ {
		result := state.shouldFallback(loopErr, providers)
		assert.False(t, result, "loop error should never trigger fallback")
	}
}

func TestShouldFallback_LoopError_BreaksConsecutiveStreak(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	loopErr := &PartialOutputError{
		Cause:  fmt.Errorf("loop detected"),
		IsLoop: true,
		Code:   LLMErrorPartialStreamError,
	}

	// provider_error → count=1
	state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)

	// loop error resets the streak
	state.shouldFallback(loopErr, providers)
	assert.Equal(t, 0, state.ConsecutiveProviderErrors, "loop should reset provider streak")
	assert.Equal(t, 0, state.ConsecutivePartialErrors, "loop should reset partial streak")

	// Next provider_error starts from 0 again — no fallback
	result := state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.False(t, result, "provider_error after loop should not trigger (streak was reset)")
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)
}

func TestShouldFallback_EmptyFallbackList(t *testing.T) {
	state := newTestFallbackState()

	result := state.shouldFallback(makePartialError(LLMErrorMaxRetries), nil)
	assert.False(t, result, "should not trigger with empty fallback list")

	result = state.shouldFallback(makePartialError(LLMErrorMaxRetries), []agent.ResolvedFallbackEntry{})
	assert.False(t, result, "should not trigger with empty fallback list")
}

func TestShouldFallback_AllProvidersExhausted(t *testing.T) {
	state := newTestFallbackState()
	state.CurrentProviderIndex = 1 // already at last index
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorMaxRetries), providers)
	assert.False(t, result, "should not trigger when all providers exhausted")
}

func TestShouldFallback_ConsecutiveCounterReset(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	// One provider_error — counter at 1
	state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)

	// Partial stream error resets the provider error counter
	state.shouldFallback(makePartialError(LLMErrorPartialStreamError), providers)
	assert.Equal(t, 0, state.ConsecutiveProviderErrors)
	assert.Equal(t, 1, state.ConsecutivePartialErrors)

	// Provider error resets the partial counter
	state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.Equal(t, 0, state.ConsecutivePartialErrors)
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)
}

func TestShouldFallback_NonPartialError_TreatedAsProviderError(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	plainErr := fmt.Errorf("gRPC transport failure")

	result := state.shouldFallback(plainErr, providers)
	assert.False(t, result, "first non-POE error should not trigger")

	result = state.shouldFallback(plainErr, providers)
	assert.True(t, result, "second consecutive non-POE error should trigger")
}

func TestShouldFallback_UnknownCode_TreatedAsProviderError(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	unknownErr := &PartialOutputError{
		Cause: fmt.Errorf("something new"),
		Code:  LLMErrorCode("new_error_code"),
	}

	result := state.shouldFallback(unknownErr, providers)
	assert.False(t, result, "first unknown code should not trigger")

	result = state.shouldFallback(unknownErr, providers)
	assert.True(t, result, "second consecutive unknown code should trigger")
}

func TestShouldFallback_InitialTimeout_AfterOneRetry(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorInitialTimeout), providers)
	assert.False(t, result, "first initial_response_timeout should not trigger fallback")
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)

	result = state.shouldFallback(makePartialError(LLMErrorInitialTimeout), providers)
	assert.True(t, result, "second consecutive initial_response_timeout should trigger fallback")
}

func TestShouldFallback_StallTimeout_AfterTwo(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	result := state.shouldFallback(makePartialError(LLMErrorStallTimeout), providers)
	assert.False(t, result, "first stall_timeout should not trigger fallback")
	assert.Equal(t, 1, state.ConsecutivePartialErrors)
	assert.Equal(t, 0, state.ConsecutiveProviderErrors, "stall_timeout should increment partial counter, not provider")

	result = state.shouldFallback(makePartialError(LLMErrorStallTimeout), providers)
	assert.True(t, result, "second consecutive stall_timeout should trigger fallback")
}

func TestShouldFallback_MixedErrors_BreaksConsecutiveCount(t *testing.T) {
	state := newTestFallbackState()
	providers := fallbackProviders()

	// provider_error → partial_stream_error → provider_error: none should trigger
	// because consecutive counts reset when the error type changes.
	state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)

	state.shouldFallback(makePartialError(LLMErrorPartialStreamError), providers)
	assert.Equal(t, 0, state.ConsecutiveProviderErrors, "provider_error count reset by partial")
	assert.Equal(t, 1, state.ConsecutivePartialErrors)

	result := state.shouldFallback(makePartialError(LLMErrorProviderError), providers)
	assert.False(t, result, "alternating errors should never reach threshold")
	assert.Equal(t, 0, state.ConsecutivePartialErrors, "partial count reset by provider_error")
	assert.Equal(t, 1, state.ConsecutiveProviderErrors)
}

// ────────────────────────────────────────────────────────────
// FallbackState lifecycle tests
// ────────────────────────────────────────────────────────────

func TestFallbackState_ConsumesClearCache(t *testing.T) {
	state := newTestFallbackState()

	assert.False(t, state.consumeClearCache(), "initially no clear cache needed")

	state.ClearCacheNeeded = true
	assert.True(t, state.consumeClearCache(), "should return true and consume")
	assert.False(t, state.consumeClearCache(), "should be false after consumption")
}

func TestFallbackState_HasFallbackOccurred(t *testing.T) {
	state := newTestFallbackState()
	assert.False(t, state.HasFallbackOccurred())

	state.CurrentProviderIndex = 0
	assert.True(t, state.HasFallbackOccurred())
}

func TestFallbackState_ResetCounters(t *testing.T) {
	state := newTestFallbackState()
	state.ConsecutiveProviderErrors = 3
	state.ConsecutivePartialErrors = 2

	state.resetCounters()
	assert.Equal(t, 0, state.ConsecutiveProviderErrors)
	assert.Equal(t, 0, state.ConsecutivePartialErrors)
}

// ────────────────────────────────────────────────────────────
// tryFallback integration tests (with real DB)
// ────────────────────────────────────────────────────────────

func TestTryFallback_SwapsProviderAndRecordsEvents(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = fallbackProviders()

	state := NewFallbackState(execCtx)
	// Simulate immediate-fallback error
	state.ConsecutiveProviderErrors = providerErrorThreshold + 1

	eventSeq := 0
	err := makePartialError(LLMErrorMaxRetries)

	result := tryFallback(context.Background(), execCtx, state, err, &eventSeq)
	require.True(t, result, "tryFallback should succeed")

	// Verify provider was swapped
	assert.Equal(t, "fallback-1", execCtx.Config.LLMProviderName)
	assert.Equal(t, config.LLMBackendNativeGemini, execCtx.Config.LLMBackend)
	assert.Equal(t, "fallback-model-1", execCtx.Config.LLMProvider.Model)

	// Verify state updated
	assert.Equal(t, 0, state.CurrentProviderIndex)
	assert.True(t, state.ClearCacheNeeded)
	assert.Contains(t, state.AttemptedProviders, "fallback-1")
	assert.Equal(t, 0, state.ConsecutiveProviderErrors, "counters should be reset")

	// Verify timeline event was created (eventSeq should have incremented)
	assert.Equal(t, 1, eventSeq, "a timeline event should have been created")
}

func TestTryFallback_ExhaustsFallbackList(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = fallbackProviders()

	state := NewFallbackState(execCtx)
	eventSeq := 0

	// First fallback: success
	result := tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	require.True(t, result)

	// Second fallback: success
	result = tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	require.True(t, result)
	assert.Equal(t, "fallback-2", execCtx.Config.LLMProviderName)

	// Third fallback: exhausted
	result = tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	assert.False(t, result, "should fail when all providers exhausted")
}

func TestTryFallback_CancelledContext(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.ResolvedFallbackProviders = fallbackProviders()

	state := NewFallbackState(execCtx)
	eventSeq := 0

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := tryFallback(ctx, execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	assert.False(t, result, "should not fallback with cancelled context")
}

func TestTryFallback_UpdatesExecutionRecord(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = fallbackProviders()

	state := NewFallbackState(execCtx)
	eventSeq := 0

	result := tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	require.True(t, result)

	// Verify execution record was updated
	exec, err := execCtx.Services.Stage.GetAgentExecutionByID(
		context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)

	require.NotNil(t, exec.OriginalLlmProvider, "original_llm_provider should be set")
	assert.Equal(t, "primary", *exec.OriginalLlmProvider)
	require.NotNil(t, exec.OriginalLlmBackend, "original_llm_backend should be set")
	assert.Equal(t, string(config.LLMBackendLangChain), *exec.OriginalLlmBackend)
	assert.Equal(t, "fallback-1", *exec.LlmProvider)
	assert.Equal(t, string(config.LLMBackendNativeGemini), exec.LlmBackend)
}

func TestTryFallback_PreservesOriginalOnSecondFallback(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = fallbackProviders()

	state := NewFallbackState(execCtx)
	eventSeq := 0

	// First fallback
	tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)

	// Second fallback
	tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)

	// Verify original is still "primary" (not "fallback-1")
	exec, err := execCtx.Services.Stage.GetAgentExecutionByID(
		context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)

	require.NotNil(t, exec.OriginalLlmProvider)
	assert.Equal(t, "primary", *exec.OriginalLlmProvider, "original should be preserved across multiple fallbacks")
	assert.Equal(t, "fallback-2", *exec.LlmProvider, "current should be updated to latest fallback")
}

func TestNativeToolsDroppedOnFallback(t *testing.T) {
	t.Run("no drop when target is google-native", func(t *testing.T) {
		provider := &config.LLMProviderConfig{
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch: true,
			},
		}
		dropped := nativeToolsDroppedOnFallback(provider, config.LLMBackendNativeGemini)
		assert.Nil(t, dropped)
	})

	t.Run("drops enabled tools when target is langchain", func(t *testing.T) {
		provider := &config.LLMProviderConfig{
			NativeTools: map[config.GoogleNativeTool]bool{
				config.GoogleNativeToolGoogleSearch:  true,
				config.GoogleNativeToolCodeExecution: false, // disabled — not dropped
				config.GoogleNativeToolURLContext:    true,
			},
		}
		dropped := nativeToolsDroppedOnFallback(provider, config.LLMBackendLangChain)
		assert.Len(t, dropped, 2)
		assert.Contains(t, dropped, "google_search")
		assert.Contains(t, dropped, "url_context")
	})

	t.Run("no drop when no native tools configured", func(t *testing.T) {
		provider := &config.LLMProviderConfig{}
		dropped := nativeToolsDroppedOnFallback(provider, config.LLMBackendLangChain)
		assert.Nil(t, dropped)
	})
}

func TestTryFallback_NativeToolsDroppedMetadata(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "hello"}}},
		},
	}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.LLMProvider.NativeTools = map[config.GoogleNativeTool]bool{
		config.GoogleNativeToolGoogleSearch: true,
		config.GoogleNativeToolURLContext:   true,
	}
	// Fallback directly to langchain backend
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		{
			ProviderName: "langchain-fb",
			Backend:      config.LLMBackendLangChain,
			Config:       &config.LLMProviderConfig{Model: "gpt-fallback"},
		},
	}

	state := NewFallbackState(execCtx)
	eventSeq := 0

	result := tryFallback(context.Background(), execCtx, state,
		makePartialError(LLMErrorMaxRetries), &eventSeq)
	require.True(t, result)

	// Verify the timeline event was created (contains native_tools_dropped metadata)
	assert.Equal(t, 1, eventSeq)

	// Verify the provider was swapped to langchain
	assert.Equal(t, "langchain-fb", execCtx.Config.LLMProviderName)
	assert.Equal(t, config.LLMBackendLangChain, execCtx.Config.LLMBackend)
}
