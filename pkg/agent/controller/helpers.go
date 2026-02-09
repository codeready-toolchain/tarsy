package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// LLMResponse holds the fully-collected response from a streaming LLM call.
type LLMResponse struct {
	Text           string
	ThinkingText   string
	ToolCalls      []agent.ToolCall
	CodeExecutions []agent.CodeExecutionChunk
	Groundings     []agent.GroundingChunk
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
		case *agent.GroundingChunk:
			resp.Groundings = append(resp.Groundings, *c)
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

	// Add grounding data if present
	if resp != nil && len(resp.Groundings) > 0 {
		llmResponseMeta["groundings_count"] = len(resp.Groundings)
	}

	_, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         execCtx.StageID,
		ExecutionID:     execCtx.ExecutionID,
		InteractionType: interactionType,
		ModelName:       execCtx.Config.LLMProvider.Model,
		LastMessageID:   lastMessageID,
		LLMRequest:      map[string]any{"messages_count": messagesCount, "iteration": iteration},
		LLMResponse:     llmResponseMeta,
		ThinkingContent: thinkingPtr,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		DurationMs:      &durationMs,
	})
	return err
}

// createTimelineEvent creates a new timeline event with content and publishes
// it for real-time delivery via WebSocket.
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

	// 1. Create TimelineEvent in DB (existing behavior)
	event, err := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *eventSeq,
		EventType:      eventType,
		Content:        content,
		Metadata:       metadata,
	})
	if err != nil {
		return nil, err
	}

	// 2. Publish to WebSocket clients (non-blocking — don't fail execution on publish error)
	publishTimelineCreated(ctx, execCtx, event, eventType, content, metadata, *eventSeq)

	return event, nil
}

// publishTimelineCreated publishes a timeline_event.created message to WebSocket clients.
func publishTimelineCreated(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	event *ent.TimelineEvent,
	eventType timelineevent.EventType,
	content string,
	metadata map[string]interface{},
	seqNum int,
) {
	if execCtx.EventPublisher == nil {
		return
	}
	channel := events.SessionChannel(execCtx.SessionID)
	publishErr := execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
		"type":            events.EventTypeTimelineCreated,
		"event_id":        event.ID,
		"session_id":      execCtx.SessionID,
		"stage_id":        execCtx.StageID,
		"execution_id":    execCtx.ExecutionID,
		"event_type":      string(eventType),
		"status":          "completed",
		"content":         content,
		"metadata":        metadata,
		"sequence_number": seqNum,
		"timestamp":       event.CreatedAt.Format(time.RFC3339Nano),
	})
	if publishErr != nil {
		slog.Warn("Failed to publish timeline event",
			"event_id", event.ID, "error", publishErr)
	}
}

// StreamCallback is called for each chunk during stream collection.
// Used by controllers to publish real-time updates to WebSocket clients.
// chunkType identifies the content type (text or thinking).
// delta is the new content from this chunk only (not accumulated). Clients
// concatenate deltas locally. This keeps each pg_notify payload small and
// avoids hitting PostgreSQL's 8 KB NOTIFY limit on long responses.
type StreamCallback func(chunkType string, delta string)

// ChunkTypeText identifies a text content delta in stream callbacks.
const ChunkTypeText = "text"

// ChunkTypeThinking identifies a thinking content delta in stream callbacks.
const ChunkTypeThinking = "thinking"

// collectStreamWithCallback collects a stream while calling back for real-time delivery.
// The callback is optional (nil = buffered mode, same as collectStream).
func collectStreamWithCallback(
	stream <-chan agent.Chunk,
	callback StreamCallback,
) (*LLMResponse, error) {
	resp := &LLMResponse{}
	var textBuf, thinkingBuf strings.Builder

	for chunk := range stream {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			textBuf.WriteString(c.Content)
			if callback != nil {
				callback(ChunkTypeText, c.Content)
			}
		case *agent.ThinkingChunk:
			thinkingBuf.WriteString(c.Content)
			if callback != nil {
				callback(ChunkTypeThinking, c.Content)
			}
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
		case *agent.GroundingChunk:
			resp.Groundings = append(resp.Groundings, *c)
		case *agent.UsageChunk:
			resp.Usage = &agent.TokenUsage{
				InputTokens:    c.InputTokens,
				OutputTokens:   c.OutputTokens,
				TotalTokens:    c.TotalTokens,
				ThinkingTokens: c.ThinkingTokens,
			}
		case *agent.ErrorChunk:
			return nil, fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
				c.Message, c.Code, c.Retryable)
		}
	}

	resp.Text = textBuf.String()
	resp.ThinkingText = thinkingBuf.String()
	return resp, nil
}

// StreamedResponse wraps an LLMResponse with information about streaming
// timeline events that were created during the LLM call. Controllers should
// check these IDs and skip creating duplicate events.
type StreamedResponse struct {
	*LLMResponse
	// ThinkingEventCreated is true if a streaming llm_thinking timeline event
	// was created (and completed) during the LLM call.
	ThinkingEventCreated bool
	// TextEventCreated is true if a streaming llm_response timeline event
	// was created (and completed) during the LLM call.
	TextEventCreated bool
}

// callLLMWithStreaming performs an LLM call with real-time streaming of chunks
// to WebSocket clients. When EventPublisher is available, it creates streaming
// timeline events for thinking and text content, publishes chunks as they arrive,
// and finalizes events when the stream completes. When EventPublisher is nil,
// it behaves identically to callLLM.
//
// Controllers should check StreamedResponse.ThinkingEventCreated and
// TextEventCreated to avoid creating duplicate timeline events.
func callLLMWithStreaming(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	llmClient agent.LLMClient,
	input *agent.GenerateInput,
	eventSeq *int,
) (*StreamedResponse, error) {
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	stream, err := llmClient.Generate(llmCtx, input)
	if err != nil {
		return nil, fmt.Errorf("LLM Generate failed: %w", err)
	}

	// If no EventPublisher, use simple collection (no streaming events)
	if execCtx.EventPublisher == nil {
		resp, err := collectStream(stream)
		if err != nil {
			return nil, err
		}
		return &StreamedResponse{LLMResponse: resp}, nil
	}

	// Track streaming timeline events
	var thinkingEventID, textEventID string
	channel := events.SessionChannel(execCtx.SessionID)

	callback := func(chunkType string, delta string) {
		switch chunkType {
		case ChunkTypeThinking:
			if thinkingEventID == "" {
				// First thinking chunk — create streaming TimelineEvent
				*eventSeq++
				event, createErr := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
					SessionID:      execCtx.SessionID,
					StageID:        execCtx.StageID,
					ExecutionID:    execCtx.ExecutionID,
					SequenceNumber: *eventSeq,
					EventType:      timelineevent.EventTypeLlmThinking,
					Content:        "",
					Metadata:       map[string]interface{}{"source": "native"},
				})
				if createErr != nil {
					slog.Warn("Failed to create streaming thinking event", "error", createErr)
					return
				}
				thinkingEventID = event.ID
				execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
					"type":            events.EventTypeTimelineCreated,
					"event_id":        thinkingEventID,
					"session_id":      execCtx.SessionID,
					"stage_id":        execCtx.StageID,
					"execution_id":    execCtx.ExecutionID,
					"event_type":      "llm_thinking",
					"status":          "streaming",
					"content":         "",
					"sequence_number": *eventSeq,
					"timestamp":       event.CreatedAt.Format(time.RFC3339Nano),
				})
			}
			// Publish only the new delta — clients concatenate locally.
			// This keeps each pg_notify payload small (avoids 8 KB limit).
			execCtx.EventPublisher.PublishTransient(ctx, channel, map[string]interface{}{
				"type":      events.EventTypeStreamChunk,
				"event_id":  thinkingEventID,
				"delta":     delta,
				"timestamp": time.Now().Format(time.RFC3339Nano),
			})

		case ChunkTypeText:
			if textEventID == "" {
				*eventSeq++
				event, createErr := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
					SessionID:      execCtx.SessionID,
					StageID:        execCtx.StageID,
					ExecutionID:    execCtx.ExecutionID,
					SequenceNumber: *eventSeq,
					EventType:      timelineevent.EventTypeLlmResponse,
					Content:        "",
					Metadata:       nil,
				})
				if createErr != nil {
					slog.Warn("Failed to create streaming text event", "error", createErr)
					return
				}
				textEventID = event.ID
				execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
					"type":            events.EventTypeTimelineCreated,
					"event_id":        textEventID,
					"session_id":      execCtx.SessionID,
					"stage_id":        execCtx.StageID,
					"execution_id":    execCtx.ExecutionID,
					"event_type":      "llm_response",
					"status":          "streaming",
					"content":         "",
					"sequence_number": *eventSeq,
					"timestamp":       event.CreatedAt.Format(time.RFC3339Nano),
				})
			}
			// Publish only the new delta — clients concatenate locally.
			execCtx.EventPublisher.PublishTransient(ctx, channel, map[string]interface{}{
				"type":      events.EventTypeStreamChunk,
				"event_id":  textEventID,
				"delta":     delta,
				"timestamp": time.Now().Format(time.RFC3339Nano),
			})
		}
	}

	resp, err := collectStreamWithCallback(stream, callback)
	if err != nil {
		return nil, err
	}

	// Finalize streaming timeline events
	if thinkingEventID != "" && resp.ThinkingText != "" {
		execCtx.Services.Timeline.CompleteTimelineEvent(ctx, thinkingEventID, resp.ThinkingText, nil, nil)
		execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
			"type":      events.EventTypeTimelineCompleted,
			"event_id":  thinkingEventID,
			"content":   resp.ThinkingText,
			"status":    "completed",
			"timestamp": time.Now().Format(time.RFC3339Nano),
		})
	}

	if textEventID != "" && resp.Text != "" {
		execCtx.Services.Timeline.CompleteTimelineEvent(ctx, textEventID, resp.Text, nil, nil)
		execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
			"type":      events.EventTypeTimelineCompleted,
			"event_id":  textEventID,
			"content":   resp.Text,
			"status":    "completed",
			"timestamp": time.Now().Format(time.RFC3339Nano),
		})
	}

	return &StreamedResponse{
		LLMResponse:          resp,
		ThinkingEventCreated: thinkingEventID != "",
		TextEventCreated:     textEventID != "",
	}, nil
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

// ============================================================================
// Native tool event helpers (Phase 3.2.1)
// ============================================================================

// createCodeExecutionEvents creates timeline events for Gemini code executions.
// Gemini streams executable_code and code_execution_result as separate response
// parts that may arrive non-consecutively. This function buffers code chunks and
// pairs them with their results to produce one timeline event per execution.
// Returns the number of events created.
func createCodeExecutionEvents(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	codeExecutions []agent.CodeExecutionChunk,
	eventSeq *int,
) int {
	created := 0

	// Gemini may stream executable_code and code_execution_result as separate,
	// potentially non-consecutive response parts. The Python provider yields each
	// as a separate CodeExecutionDelta:
	//   - executable_code part  → CodeExecutionDelta{code: "...", result: ""}
	//   - code_execution_result → CodeExecutionDelta{code: "",   result: "..."}
	// After collectStream drains the gRPC stream, codeExecutions contains these
	// chunks in arrival order. We use pendingCode to buffer an executable_code
	// chunk until its matching code_execution_result arrives, then emit the
	// pair as a single timeline event.
	var pendingCode string
	for _, ce := range codeExecutions {
		if ce.Code != "" && ce.Result == "" {
			// executable_code part — buffer the code until its result arrives
			if pendingCode != "" {
				// Previous code never got a result — emit it alone
				content := formatCodeExecution(pendingCode, "")
				createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
					content, map[string]interface{}{"source": "gemini"}, eventSeq)
				created++
			}
			pendingCode = ce.Code
		} else if ce.Result != "" && ce.Code == "" {
			// code_execution_result part — pair with buffered pendingCode
			content := formatCodeExecution(pendingCode, ce.Result)
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
				content, map[string]interface{}{"source": "gemini"}, eventSeq)
			pendingCode = ""
			created++
		} else if ce.Code != "" && ce.Result != "" {
			// Both present in one chunk (defensive — not expected from current Python
			// provider, but handles future changes or alternative providers gracefully)
			if pendingCode != "" {
				content := formatCodeExecution(pendingCode, "")
				createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
					content, map[string]interface{}{"source": "gemini"}, eventSeq)
				created++
			}
			content := formatCodeExecution(ce.Code, ce.Result)
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
				content, map[string]interface{}{"source": "gemini"}, eventSeq)
			pendingCode = ""
			created++
		}
	}

	// Emit any remaining code without result
	if pendingCode != "" {
		content := formatCodeExecution(pendingCode, "")
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeCodeExecution,
			content, map[string]interface{}{"source": "gemini"}, eventSeq)
		created++
	}

	return created
}

// formatCodeExecution formats a code execution pair for timeline event content.
func formatCodeExecution(code, result string) string {
	var sb strings.Builder
	if code != "" {
		sb.WriteString("```python\n")
		sb.WriteString(code)
		sb.WriteString("\n```\n")
	}
	if result != "" {
		sb.WriteString("\nOutput:\n```\n")
		sb.WriteString(result)
		sb.WriteString("\n```")
	}
	return sb.String()
}

// createGroundingEvents creates timeline events for grounding results.
// Determines event type based on whether web_search_queries are present:
//   - With queries → google_search_result
//   - Without queries → url_context_result
//
// Content is human-readable; structured data goes in metadata (Q5 decision).
func createGroundingEvents(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	groundings []agent.GroundingChunk,
	eventSeq *int,
) int {
	created := 0

	for _, g := range groundings {
		if len(g.Sources) == 0 {
			continue // No sources — skip empty grounding
		}

		// Build structured metadata (full data for frontend rich rendering)
		metadata := map[string]interface{}{
			"source":  "gemini",
			"sources": formatGroundingSources(g.Sources),
		}
		if len(g.Supports) > 0 {
			metadata["supports"] = formatGroundingSupports(g.Supports)
		}

		var eventType timelineevent.EventType
		var content string

		if len(g.WebSearchQueries) > 0 {
			// Google Search grounding
			eventType = timelineevent.EventTypeGoogleSearchResult
			metadata["queries"] = g.WebSearchQueries
			content = formatGoogleSearchContent(g.WebSearchQueries, g.Sources)
		} else {
			// URL Context grounding
			eventType = timelineevent.EventTypeURLContextResult
			content = formatUrlContextContent(g.Sources)
		}

		createTimelineEvent(ctx, execCtx, eventType, content, metadata, eventSeq)
		created++
	}

	return created
}

// formatSourceList formats a list of GroundingSource into a comma-separated string.
func formatSourceList(sources []agent.GroundingSource) string {
	var sb strings.Builder
	for i, s := range sources {
		if i > 0 {
			sb.WriteString(", ")
		}
		if s.Title != "" {
			sb.WriteString(s.Title)
			sb.WriteString(" (")
			sb.WriteString(s.URI)
			sb.WriteString(")")
		} else {
			sb.WriteString(s.URI)
		}
	}
	return sb.String()
}

// formatGoogleSearchContent creates a human-readable summary for google_search_result events.
func formatGoogleSearchContent(queries []string, sources []agent.GroundingSource) string {
	var sb strings.Builder
	sb.WriteString("Google Search: ")
	for i, q := range queries {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("'")
		sb.WriteString(q)
		sb.WriteString("'")
	}
	sb.WriteString(" → Sources: ")
	sb.WriteString(formatSourceList(sources))
	return sb.String()
}

// formatUrlContextContent creates a human-readable summary for url_context_result events.
func formatUrlContextContent(sources []agent.GroundingSource) string {
	return "URL Context → Sources: " + formatSourceList(sources)
}

// formatGroundingSources converts grounding sources to a serializable format for metadata.
func formatGroundingSources(sources []agent.GroundingSource) []map[string]string {
	result := make([]map[string]string, 0, len(sources))
	for _, s := range sources {
		result = append(result, map[string]string{
			"uri":   s.URI,
			"title": s.Title,
		})
	}
	return result
}

// formatGroundingSupports converts grounding supports to a serializable format for metadata.
func formatGroundingSupports(supports []agent.GroundingSupport) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(supports))
	for _, s := range supports {
		result = append(result, map[string]interface{}{
			"start_index":             s.StartIndex,
			"end_index":               s.EndIndex,
			"text":                    s.Text,
			"grounding_chunk_indices": s.GroundingChunkIndices,
		})
	}
	return result
}
