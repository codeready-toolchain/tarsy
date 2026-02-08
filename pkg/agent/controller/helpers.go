package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// LLMResponse holds the fully-collected response from a streaming LLM call.
type LLMResponse struct {
	Text           string
	ThinkingText   string
	ToolCalls      []agent.ToolCall
	CodeExecutions []agent.CodeExecutionChunk
	Usage          *agent.TokenUsage
}

// collectStream drains an LLM chunk channel into a complete LLMResponse.
// Returns an error if an ErrorChunk is received.
func collectStream(stream <-chan agent.Chunk) (*LLMResponse, error) {
	resp := &LLMResponse{}
	var textBuf, thinkingBuf strings.Builder

	for chunk := range stream {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			textBuf.WriteString(c.Content)
		case *agent.ThinkingChunk:
			thinkingBuf.WriteString(c.Content)
		case *agent.ToolCallChunk:
			resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
				ID:        c.CallID,
				Name:      c.Name,
				Arguments: c.Arguments,
			})
		case *agent.CodeExecutionChunk:
			resp.CodeExecutions = append(resp.CodeExecutions, agent.CodeExecutionChunk{
				Code:   c.Code,
				Result: c.Result,
			})
		case *agent.UsageChunk:
			resp.Usage = &agent.TokenUsage{
				InputTokens:    c.InputTokens,
				OutputTokens:   c.OutputTokens,
				TotalTokens:    c.TotalTokens,
				ThinkingTokens: c.ThinkingTokens,
			}
		case *agent.ErrorChunk:
			// Intentionally discards any partially accumulated text/thinking/tool-call
			// data. Callers treat LLM errors as complete failures and retry from scratch,
			// so partial data is not useful in the current design.
			return nil, fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
				c.Message, c.Code, c.Retryable)
		}
	}

	resp.Text = textBuf.String()
	resp.ThinkingText = thinkingBuf.String()
	return resp, nil
}

// callLLM performs a single LLM call with context cancellation support.
// Returns the complete collected response.
func callLLM(
	ctx context.Context,
	llmClient agent.LLMClient,
	input *agent.GenerateInput,
) (*LLMResponse, error) {
	// Derive a cancellable context so the producer goroutine in Generate
	// is always cleaned up when we return.
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	stream, err := llmClient.Generate(llmCtx, input)
	if err != nil {
		return nil, fmt.Errorf("LLM Generate failed: %w", err)
	}

	return collectStream(stream)
}

// accumulateUsage adds token counts from an LLM response to the running total.
func accumulateUsage(total *agent.TokenUsage, resp *LLMResponse) {
	if resp != nil && resp.Usage != nil {
		total.InputTokens += resp.Usage.InputTokens
		total.OutputTokens += resp.Usage.OutputTokens
		total.TotalTokens += resp.Usage.TotalTokens
		total.ThinkingTokens += resp.Usage.ThinkingTokens
	}
}

// recordLLMInteraction creates an LLMInteraction debug record in the database.
// This is best-effort observability — callers intentionally ignore the returned error
// because a failure to record an interaction should never abort the investigation loop.
func recordLLMInteraction(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	iteration int,
	interactionType string,
	messagesCount int,
	resp *LLMResponse,
	lastMessageID *string,
	startTime time.Time,
) error {
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

	_, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         execCtx.StageID,
		ExecutionID:     execCtx.ExecutionID,
		InteractionType: interactionType,
		ModelName:       execCtx.Config.LLMProvider.Model,
		LastMessageID:   lastMessageID,
		LLMRequest:      map[string]any{"messages_count": messagesCount, "iteration": iteration},
		LLMResponse:     map[string]any{"text_length": textLen, "tool_calls_count": toolCallsCount},
		ThinkingContent: thinkingPtr,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		DurationMs:      &durationMs,
	})
	return err
}

// createTimelineEvent creates a new timeline event with content.
// In Phase 3.2, events are always created with their final content
// (after the LLM response is fully collected and parsed).
//
// Best-effort: callers intentionally ignore both return values because timeline
// events are non-critical observability data — a failure to record one should
// never abort the investigation loop. The same applies to createToolCallEvent
// and createToolResultEvent below.
//
// Note: *eventSeq is incremented before the DB call. If the call fails (and
// the caller ignores the error), the next event will have a gap in its sequence
// number. This is acceptable for best-effort observability data.
func createTimelineEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	eventType timelineevent.EventType,
	content string,
	metadata map[string]interface{},
	eventSeq *int,
) (*ent.TimelineEvent, error) {
	*eventSeq++
	return execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *eventSeq,
		EventType:      eventType,
		Content:        content,
		Metadata:       metadata,
	})
}

// createToolCallEvent creates a timeline event for a tool call request.
func createToolCallEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	toolName string,
	args string,
	eventSeq *int,
) (*ent.TimelineEvent, error) {
	return createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmToolCall, args, map[string]interface{}{
		"tool_name": toolName,
	}, eventSeq)
}

// createToolResultEvent creates a timeline event for a tool execution result.
func createToolResultEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	content string,
	isError bool,
	eventSeq *int,
) (*ent.TimelineEvent, error) {
	return createTimelineEvent(ctx, execCtx, timelineevent.EventTypeToolResult, content, map[string]interface{}{
		"is_error": isError,
	}, eventSeq)
}

// storeMessages persists initial conversation messages to DB.
func storeMessages(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	msgSeq *int,
) error {
	for _, msg := range messages {
		*msgSeq++
		_, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      execCtx.SessionID,
			StageID:        execCtx.StageID,
			ExecutionID:    execCtx.ExecutionID,
			SequenceNumber: *msgSeq,
			Role:           message.Role(msg.Role),
			Content:        msg.Content,
		})
		if err != nil {
			return fmt.Errorf("failed to store message: %w", err)
		}
	}
	return nil
}

// storeAssistantMessage persists an assistant text response to DB.
func storeAssistantMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	resp *LLMResponse,
	msgSeq *int,
) (*ent.Message, error) {
	if resp == nil {
		return nil, fmt.Errorf("storeAssistantMessage: resp is nil")
	}
	*msgSeq++
	return execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleAssistant,
		Content:        resp.Text,
	})
}

// storeAssistantMessageWithToolCalls persists an assistant message with tool calls.
func storeAssistantMessageWithToolCalls(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	resp *LLMResponse,
	msgSeq *int,
) (*ent.Message, error) {
	if resp == nil {
		return nil, fmt.Errorf("storeAssistantMessageWithToolCalls: resp is nil")
	}
	*msgSeq++

	var toolCallData []models.ToolCallData
	for _, tc := range resp.ToolCalls {
		toolCallData = append(toolCallData, models.ToolCallData{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}

	return execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleAssistant,
		Content:        resp.Text,
		ToolCalls:      toolCallData,
	})
}

// storeToolResultMessage persists a tool result message to DB.
func storeToolResultMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	callID string,
	toolName string,
	content string,
	msgSeq *int,
) error {
	*msgSeq++
	_, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleTool,
		Content:        content,
		ToolCallID:     callID,
		ToolName:       toolName,
	})
	return err
}

// storeObservationMessage persists a ReAct observation as a user message.
func storeObservationMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	observation string,
	msgSeq *int,
) error {
	*msgSeq++
	_, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleUser,
		Content:        observation,
	})
	return err
}

// isTimeoutError checks if an error is timeout-related.
// Used for consecutive timeout tracking.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") || strings.Contains(errStr, "timed out")
}

// buildForcedConclusionPrompt returns the prompt to force an LLM conclusion.
func buildForcedConclusionPrompt(iteration int) string {
	return fmt.Sprintf(
		"You have reached the maximum number of iterations (%d). "+
			"Based on all the information gathered so far, provide your final analysis and conclusion. "+
			"Do NOT call any more tools. Summarize your findings clearly.",
		iteration,
	)
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
