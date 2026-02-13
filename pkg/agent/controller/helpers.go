package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// accumulateUsage adds token counts from an LLM response to the running total.
func accumulateUsage(total *agent.TokenUsage, resp *LLMResponse) {
	if resp != nil {
		accumulateTokenUsage(total, resp.Usage)
	}
}

// accumulateTokenUsage adds token counts from a TokenUsage to the running total.
// Accepts *agent.TokenUsage directly, avoiding the need to wrap usage in a
// throwaway LLMResponse (e.g., when accumulating summarization usage).
func accumulateTokenUsage(total *agent.TokenUsage, usage *agent.TokenUsage) {
	if usage == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.TotalTokens += usage.TotalTokens
	total.ThinkingTokens += usage.ThinkingTokens
}

// recordLLMInteraction creates an LLMInteraction record in the database.
// Logs slog.Error on failure but does not abort the investigation loop —
// the in-memory state is authoritative during execution.
func recordLLMInteraction(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	iteration int,
	interactionType string,
	messagesCount int,
	resp *LLMResponse,
	lastMessageID *string,
	startTime time.Time,
) {
	durationMs := int(time.Since(startTime).Milliseconds())

	var thinkingPtr *string
	var inputTokens, outputTokens, totalTokens *int
	var textLen, toolCallsCount int

	if resp != nil {
		if resp.ThinkingText != "" {
			thinkingPtr = &resp.ThinkingText
		}
		if resp.Usage != nil {
			inputTokens = &resp.Usage.InputTokens
			outputTokens = &resp.Usage.OutputTokens
			totalTokens = &resp.Usage.TotalTokens
		}
		textLen = len(resp.Text)
		toolCallsCount = len(resp.ToolCalls)
	}

	llmResponseMeta := map[string]any{
		"text_length":      textLen,
		"tool_calls_count": toolCallsCount,
	}

	// Add code execution data if present
	if resp != nil && len(resp.CodeExecutions) > 0 {
		var codeExecs []map[string]string
		for _, ce := range resp.CodeExecutions {
			codeExecs = append(codeExecs, map[string]string{
				"code":   ce.Code,
				"result": ce.Result,
			})
		}
		llmResponseMeta["code_executions"] = codeExecs
	}

	// Add grounding count summary if present
	if resp != nil && len(resp.Groundings) > 0 {
		llmResponseMeta["groundings_count"] = len(resp.Groundings)
	}

	// Build response_metadata with full grounding details for dashboard rendering.
	responseMeta := buildResponseMetadata(resp)

	if _, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:        execCtx.SessionID,
		StageID:          &execCtx.StageID,
		ExecutionID:      &execCtx.ExecutionID,
		InteractionType:  interactionType,
		ModelName:        execCtx.Config.LLMProvider.Model,
		LastMessageID:    lastMessageID,
		LLMRequest:       map[string]any{"messages_count": messagesCount, "iteration": iteration},
		LLMResponse:      llmResponseMeta,
		ResponseMetadata: responseMeta,
		ThinkingContent:  thinkingPtr,
		InputTokens:      inputTokens,
		OutputTokens:     outputTokens,
		TotalTokens:      totalTokens,
		DurationMs:       &durationMs,
	}); err != nil {
		slog.Error("Failed to record LLM interaction",
			"session_id", execCtx.SessionID, "type", interactionType, "error", err)
	}
}

// isTimeoutError checks if an error is a context deadline timeout.
// Used for consecutive timeout tracking. Only matches errors that wrap
// context.DeadlineExceeded — string-based matching is intentionally avoided
// because callers now propagate the original error with its full chain.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// generateCallID creates a unique ID for a tool call.
func generateCallID() string {
	return uuid.New().String()
}

// buildToolNameSet creates a set of available tool names for quick lookup.
func buildToolNameSet(tools []agent.ToolDefinition) map[string]bool {
	set := make(map[string]bool, len(tools))
	for _, t := range tools {
		set[t.Name] = true
	}
	return set
}

// failedResult creates a failed ExecutionResult from iteration state.
// state must not be nil — callers always pass the locally-created IterationState
// from the top of their Run() method.
func failedResult(state *agent.IterationState, totalUsage agent.TokenUsage) *agent.ExecutionResult {
	return &agent.ExecutionResult{
		Status: agent.ExecutionStatusFailed,
		Error: fmt.Errorf("aborted after %d consecutive timeouts (iteration %d/%d): %s",
			state.ConsecutiveTimeoutFailures, state.CurrentIteration, state.MaxIterations, state.LastErrorMessage),
		TokensUsed: totalUsage,
	}
}

// tokenUsageFromResp extracts token usage from an LLM response.
func tokenUsageFromResp(resp *LLMResponse) agent.TokenUsage {
	if resp == nil || resp.Usage == nil {
		return agent.TokenUsage{}
	}
	return *resp.Usage
}

// buildResponseMetadata constructs the response_metadata map from grounding
// chunks in the LLM response. Returns nil when there are no groundings,
// so the optional DB field remains NULL for non-grounded responses.
func buildResponseMetadata(resp *LLMResponse) map[string]any {
	if resp == nil || len(resp.Groundings) == 0 {
		return nil
	}

	groundings := make([]map[string]any, 0, len(resp.Groundings))
	for _, g := range resp.Groundings {
		entry := map[string]any{}

		// Classify as google_search or url_context based on whether
		// WebSearchQueries is populated.
		if len(g.WebSearchQueries) > 0 {
			entry["type"] = "google_search"
			entry["queries"] = g.WebSearchQueries
		} else {
			entry["type"] = "url_context"
		}

		if len(g.Sources) > 0 {
			sources := make([]map[string]string, len(g.Sources))
			for i, s := range g.Sources {
				sources[i] = map[string]string{"uri": s.URI, "title": s.Title}
			}
			entry["sources"] = sources
		}

		if len(g.Supports) > 0 {
			supports := make([]map[string]any, len(g.Supports))
			for i, s := range g.Supports {
				supports[i] = map[string]any{
					"start_index":    s.StartIndex,
					"end_index":      s.EndIndex,
					"text":           s.Text,
					"source_indices": s.GroundingChunkIndices,
				}
			}
			entry["supports"] = supports
		}

		groundings = append(groundings, entry)
	}

	return map[string]any{"groundings": groundings}
}
