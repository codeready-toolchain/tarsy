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
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
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
// Delegates to collectStreamWithCallback with a nil callback.
func collectStream(stream <-chan agent.Chunk) (*LLMResponse, error) {
	return collectStreamWithCallback(stream, nil)
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

	// Add grounding data if present
	if resp != nil && len(resp.Groundings) > 0 {
		llmResponseMeta["groundings_count"] = len(resp.Groundings)
	}

	if _, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
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
	}); err != nil {
		slog.Error("Failed to record LLM interaction",
			"session_id", execCtx.SessionID, "type", interactionType, "error", err)
	}
}

// createTimelineEvent creates a new timeline event with content and publishes
// it for real-time delivery via WebSocket.
//
// Logs slog.Error on DB failure but does not abort the investigation loop —
// the in-memory state is authoritative during execution.
//
// Note: *eventSeq is incremented before the DB call. If the call fails,
// the next event will have a gap in its sequence number.
func createTimelineEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	eventType timelineevent.EventType,
	content string,
	metadata map[string]interface{},
	eventSeq *int,
) {
	*eventSeq++

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
		slog.Error("Failed to create timeline event",
			"session_id", execCtx.SessionID, "event_type", eventType, "error", err)
		return
	}

	publishTimelineCreated(ctx, execCtx, event, eventType, content, metadata, *eventSeq)
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
	publishErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
		Type:           events.EventTypeTimelineCreated,
		EventID:        event.ID,
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		EventType:      string(eventType),
		Status:         string(timelineevent.StatusCompleted),
		Content:        content,
		Metadata:       metadata,
		SequenceNumber: seqNum,
		Timestamp:      event.CreatedAt.Format(time.RFC3339Nano),
	})
	if publishErr != nil {
		slog.Warn("Failed to publish timeline event",
			"event_id", event.ID, "session_id", execCtx.SessionID, "error", publishErr)
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
	var thinkingCreateFailed, textCreateFailed bool

	callback := func(chunkType string, delta string) {
		if delta == "" {
			return // Skip empty chunks — nothing to create or publish
		}

		switch chunkType {
		case ChunkTypeThinking:
			if thinkingCreateFailed {
				return // event creation already failed — skip to avoid retry spam
			}
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
					slog.Warn("Failed to create streaming thinking event", "session_id", execCtx.SessionID, "error", createErr)
					thinkingCreateFailed = true
					return
				}
				thinkingEventID = event.ID
				if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
					Type:           events.EventTypeTimelineCreated,
					EventID:        thinkingEventID,
					SessionID:      execCtx.SessionID,
					StageID:        execCtx.StageID,
					ExecutionID:    execCtx.ExecutionID,
					EventType:      string(timelineevent.EventTypeLlmThinking),
					Status:         string(timelineevent.StatusStreaming),
					Content:        "",
					SequenceNumber: *eventSeq,
					Timestamp:      event.CreatedAt.Format(time.RFC3339Nano),
				}); pubErr != nil {
					slog.Warn("Failed to publish streaming thinking created",
						"event_id", thinkingEventID, "session_id", execCtx.SessionID, "error", pubErr)
				}
			}
			// Publish only the new delta — clients concatenate locally.
			// This keeps each pg_notify payload small (avoids 8 KB limit).
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				Type:      events.EventTypeStreamChunk,
				EventID:   thinkingEventID,
				Delta:     delta,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}); pubErr != nil {
				slog.Warn("Failed to publish thinking stream chunk",
					"event_id", thinkingEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}

		case ChunkTypeText:
			if textCreateFailed {
				return // event creation already failed — skip to avoid retry spam
			}
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
					slog.Warn("Failed to create streaming text event", "session_id", execCtx.SessionID, "error", createErr)
					textCreateFailed = true
					return
				}
				textEventID = event.ID
				if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
					Type:           events.EventTypeTimelineCreated,
					EventID:        textEventID,
					SessionID:      execCtx.SessionID,
					StageID:        execCtx.StageID,
					ExecutionID:    execCtx.ExecutionID,
					EventType:      string(timelineevent.EventTypeLlmResponse),
					Status:         string(timelineevent.StatusStreaming),
					Content:        "",
					SequenceNumber: *eventSeq,
					Timestamp:      event.CreatedAt.Format(time.RFC3339Nano),
				}); pubErr != nil {
					slog.Warn("Failed to publish streaming text created",
						"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
				}
			}
			// Publish only the new delta — clients concatenate locally.
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				Type:      events.EventTypeStreamChunk,
				EventID:   textEventID,
				Delta:     delta,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}); pubErr != nil {
				slog.Warn("Failed to publish text stream chunk",
					"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		}
	}

	resp, err := collectStreamWithCallback(stream, callback)
	if err != nil {
		// Mark any streaming timeline events as failed so they don't stay
		// stuck at status "streaming" indefinitely.
		markStreamingEventsFailed(ctx, execCtx, thinkingEventID, textEventID, err)
		return nil, err
	}

	// Finalize streaming timeline events.
	// Always finalize if the event was created (thinkingEventID/textEventID set),
	// even when resp content is empty. Otherwise the event stays at "streaming"
	// status indefinitely. The empty-delta guard above prevents event creation
	// for purely empty chunks, but we handle the edge case defensively here.
	if thinkingEventID != "" {
		finalizeStreamingEvent(ctx, execCtx, thinkingEventID, resp.ThinkingText, "thinking")
	}

	if textEventID != "" {
		finalizeStreamingEvent(ctx, execCtx, textEventID, resp.Text, "text")
	}

	return &StreamedResponse{
		LLMResponse:          resp,
		ThinkingEventCreated: thinkingEventID != "",
		TextEventCreated:     textEventID != "",
	}, nil
}

// finalizeStreamingEvent completes or fails a streaming timeline event.
// If content is non-empty, the event is completed normally. If content is
// empty (edge case: event created but all chunks were empty), it is marked
// as failed to avoid leaving it stuck at "streaming" status.
func finalizeStreamingEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	eventID, content, label string,
) {
	if content != "" {
		if complErr := execCtx.Services.Timeline.CompleteTimelineEvent(ctx, eventID, content, nil, nil); complErr != nil {
			slog.Warn("Failed to complete streaming "+label+" event",
				"event_id", eventID, "session_id", execCtx.SessionID, "error", complErr)
		}
		if execCtx.EventPublisher != nil {
			if pubErr := execCtx.EventPublisher.PublishTimelineCompleted(ctx, execCtx.SessionID, events.TimelineCompletedPayload{
				Type:      events.EventTypeTimelineCompleted,
				EventID:   eventID,
				Content:   content,
				Status:    string(timelineevent.StatusCompleted),
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}); pubErr != nil {
				slog.Warn("Failed to publish "+label+" completed",
					"event_id", eventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		} else {
			slog.Error("EventPublisher is nil, skipping "+label+" completion publish",
				"event_id", eventID, "session_id", execCtx.SessionID)
		}
		return
	}

	// Edge case: event was created but content is empty.
	// CompleteTimelineEvent rejects empty content, so mark as failed instead.
	slog.Warn("Streaming "+label+" event has no content, marking as failed",
		"event_id", eventID, "session_id", execCtx.SessionID)
	failContent := "No content produced"
	if failErr := execCtx.Services.Timeline.FailTimelineEvent(ctx, eventID, failContent); failErr != nil {
		slog.Warn("Failed to fail empty streaming "+label+" event",
			"event_id", eventID, "session_id", execCtx.SessionID, "error", failErr)
	}
	if execCtx.EventPublisher != nil {
		if pubErr := execCtx.EventPublisher.PublishTimelineCompleted(ctx, execCtx.SessionID, events.TimelineCompletedPayload{
			Type:      events.EventTypeTimelineCompleted,
			EventID:   eventID,
			Content:   failContent,
			Status:    string(timelineevent.StatusFailed),
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}); pubErr != nil {
			slog.Warn("Failed to publish "+label+" failure",
				"event_id", eventID, "session_id", execCtx.SessionID, "error", pubErr)
		}
	} else {
		slog.Error("EventPublisher is nil, skipping "+label+" failure publish",
			"event_id", eventID, "session_id", execCtx.SessionID)
	}
}

// markStreamingEventsFailed marks any in-flight streaming timeline events
// as failed. Called when collectStreamWithCallback returns an error so that
// events don't remain stuck at status "streaming" indefinitely.
func markStreamingEventsFailed(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	thinkingEventID, textEventID string,
	streamErr error,
) {
	failEvent := func(eventID string) {
		if eventID == "" {
			return
		}
		// Update DB status to failed with error message as content
		failContent := fmt.Sprintf("Streaming failed: %s", streamErr.Error())
		updateErr := execCtx.Services.Timeline.FailTimelineEvent(ctx, eventID, failContent)
		if updateErr != nil {
			slog.Warn("Failed to mark streaming event as failed",
				"event_id", eventID, "session_id", execCtx.SessionID, "error", updateErr)
			return
		}
		// Notify WebSocket clients
		if execCtx.EventPublisher != nil {
			if pubErr := execCtx.EventPublisher.PublishTimelineCompleted(ctx, execCtx.SessionID, events.TimelineCompletedPayload{
				Type:      events.EventTypeTimelineCompleted,
				EventID:   eventID,
				Status:    string(timelineevent.StatusFailed),
				Content:   failContent,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}); pubErr != nil {
				slog.Warn("Failed to publish streaming event failure",
					"event_id", eventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		} else {
			slog.Error("EventPublisher is nil, skipping streaming event failure publish",
				"event_id", eventID, "session_id", execCtx.SessionID)
		}
	}

	failEvent(thinkingEventID)
	failEvent(textEventID)
}

// createToolCallEvent creates a streaming llm_tool_call timeline event.
// The event starts with status "streaming" (DB default) and empty content.
// Arguments are stored in metadata (not content) so they survive the content
// update on completion. Publishes timeline_event.created with "streaming" status.
// Completed via completeToolCallEvent after tool execution returns.
func createToolCallEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	serverID, toolName string,
	arguments string,
	eventSeq *int,
) (*ent.TimelineEvent, error) {
	*eventSeq++

	metadata := map[string]interface{}{
		"server_name": serverID,
		"tool_name":   toolName,
		"arguments":   arguments,
	}

	// Create event with empty content (streaming lifecycle — content set on completion)
	event, err := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *eventSeq,
		EventType:      timelineevent.EventTypeLlmToolCall,
		Content:        "",
		Metadata:       metadata,
	})
	if err != nil {
		return nil, err
	}

	// Publish with "streaming" status (not "completed" — tool is still executing)
	if execCtx.EventPublisher != nil {
		if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
			Type:           events.EventTypeTimelineCreated,
			EventID:        event.ID,
			SessionID:      execCtx.SessionID,
			StageID:        execCtx.StageID,
			ExecutionID:    execCtx.ExecutionID,
			EventType:      string(timelineevent.EventTypeLlmToolCall),
			Status:         string(timelineevent.StatusStreaming),
			Content:        "",
			Metadata:       metadata,
			SequenceNumber: *eventSeq,
			Timestamp:      event.CreatedAt.Format(time.RFC3339Nano),
		}); pubErr != nil {
			slog.Warn("Failed to publish tool call created",
				"event_id", event.ID, "session_id", execCtx.SessionID, "error", pubErr)
		}
	}

	return event, nil
}

// completeToolCallEvent completes an llm_tool_call timeline event with the tool result.
// Called after ToolExecutor.Execute() returns. The content is the storage-truncated
// raw result. Metadata is enriched with is_error via read-modify-write merge.
//
// The completed event's WebSocket payload only includes {"is_error": bool} in
// metadata. Full tool context (server_name, tool_name, arguments) was included
// in the original timeline_event.created message and is persisted in the DB via
// the metadata merge. Clients correlate completed ↔ created events by event_id.
func completeToolCallEvent(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	event *ent.TimelineEvent,
	content string,
	isError bool,
) {
	if event == nil {
		return
	}

	completionMeta := map[string]interface{}{"is_error": isError}

	if err := execCtx.Services.Timeline.CompleteTimelineEventWithMetadata(
		ctx, event.ID, content, completionMeta, nil, nil,
	); err != nil {
		slog.Warn("Failed to complete tool call event",
			"event_id", event.ID, "session_id", execCtx.SessionID, "error", err)
	}

	// Publish completion to WebSocket
	if execCtx.EventPublisher != nil {
		if pubErr := execCtx.EventPublisher.PublishTimelineCompleted(ctx, execCtx.SessionID, events.TimelineCompletedPayload{
			Type:      events.EventTypeTimelineCompleted,
			EventID:   event.ID,
			Content:   content,
			Status:    string(timelineevent.StatusCompleted),
			Metadata:  completionMeta,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}); pubErr != nil {
			slog.Warn("Failed to publish tool call completed",
				"event_id", event.ID, "session_id", execCtx.SessionID, "error", pubErr)
		}
	}
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
// Logs slog.Error on failure but does not abort the investigation loop —
// the in-memory messages slice is authoritative during execution.
func storeToolResultMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	callID string,
	toolName string,
	content string,
	msgSeq *int,
) {
	*msgSeq++
	if _, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleTool,
		Content:        content,
		ToolCallID:     callID,
		ToolName:       toolName,
	}); err != nil {
		slog.Error("Failed to store tool result message",
			"session_id", execCtx.SessionID, "tool", toolName, "error", err)
	}
}

// storeObservationMessage persists a ReAct observation as a user message.
// Logs slog.Error on failure but does not abort the investigation loop —
// the in-memory messages slice is authoritative during execution.
func storeObservationMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	observation string,
	msgSeq *int,
) {
	*msgSeq++
	if _, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleUser,
		Content:        observation,
	}); err != nil {
		slog.Error("Failed to store observation message",
			"session_id", execCtx.SessionID, "error", err)
	}
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

// toolCallResult holds the outcome of executeToolCall for the caller to
// integrate into its conversation format (ReAct observation vs NativeThinking
// tool message).
type toolCallResult struct {
	// Content is the tool result content to feed back to the LLM.
	// May be summarized if summarization was triggered.
	Content string
	// IsError is true if the tool execution itself failed.
	IsError bool
	// Usage is non-nil when summarization produced token usage to accumulate.
	Usage *agent.TokenUsage
}

// executeToolCall runs a single tool call through the full lifecycle:
//  1. Normalize and split tool name for events/summarization
//  2. Create streaming llm_tool_call event (dashboard spinner)
//  3. Execute the tool via ToolExecutor
//  4. Complete the tool call event with storage-truncated result
//  5. Optionally summarize large non-error results
//
// Returns the result content (possibly summarized) and whether the call failed.
// Callers are responsible for appending the result to their conversation and
// recording state changes (RecordFailure, message storage, etc.).
func executeToolCall(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	call agent.ToolCall,
	messages []agent.ConversationMessage,
	eventSeq *int,
) toolCallResult {
	// Step 1: Normalize and split tool name
	normalizedName := mcp.NormalizeToolName(call.Name)
	serverID, toolName, splitErr := mcp.SplitToolName(normalizedName)
	if splitErr != nil {
		serverID = ""
		toolName = call.Name
	}

	// Step 2: Create streaming llm_tool_call event (dashboard shows spinner)
	toolCallEvent, createErr := createToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, eventSeq)
	if createErr != nil {
		slog.Warn("Failed to create tool call event", "error", createErr, "tool", call.Name)
	}

	// Step 3: Execute the tool
	result, toolErr := execCtx.ToolExecutor.Execute(ctx, call)
	if toolErr != nil {
		errContent := fmt.Sprintf("Error executing tool: %s", toolErr.Error())
		completeToolCallEvent(ctx, execCtx, toolCallEvent, errContent, true)
		return toolCallResult{Content: errContent, IsError: true}
	}

	// Step 4: Complete tool call event with storage-truncated result
	storageTruncated := mcp.TruncateForStorage(result.Content)
	completeToolCallEvent(ctx, execCtx, toolCallEvent, storageTruncated, result.IsError)

	// Step 5: Summarize if applicable (non-error results only)
	content := result.Content
	var usage *agent.TokenUsage
	if !result.IsError {
		convContext := buildConversationContext(messages)
		sumResult, sumErr := maybeSummarize(ctx, execCtx, serverID, toolName,
			result.Content, convContext, eventSeq)
		if sumErr == nil && sumResult.WasSummarized {
			content = sumResult.Content
			usage = sumResult.Usage
		}
	}

	return toolCallResult{Content: content, IsError: result.IsError, Usage: usage}
}

// tokenUsageFromResp extracts token usage from an LLM response.
func tokenUsageFromResp(resp *LLMResponse) agent.TokenUsage {
	if resp == nil || resp.Usage == nil {
		return agent.TokenUsage{}
	}
	return *resp.Usage
}

// ============================================================================
// Native tool event helpers
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
