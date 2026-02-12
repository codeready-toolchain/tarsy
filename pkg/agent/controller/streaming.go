package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
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
//
// extraMetadata (optional): if provided, the first map is merged into the
// metadata of llm_thinking and llm_response streaming events at creation time.
// Used by forceConclusion to tag events with forced_conclusion metadata.
func callLLMWithStreaming(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	llmClient agent.LLMClient,
	input *agent.GenerateInput,
	eventSeq *int,
	extraMetadata ...map[string]interface{},
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

	// Resolve optional extra metadata for streaming events.
	var extra map[string]interface{}
	if len(extraMetadata) > 0 {
		extra = extraMetadata[0]
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
				thinkingMeta := mergeMetadata(map[string]interface{}{"source": "native"}, extra)
				event, createErr := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
					SessionID:      execCtx.SessionID,
					StageID:        &execCtx.StageID,
					ExecutionID:    &execCtx.ExecutionID,
					SequenceNumber: *eventSeq,
					EventType:      timelineevent.EventTypeLlmThinking,
					Content:        "",
					Metadata:       thinkingMeta,
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
					EventType:      timelineevent.EventTypeLlmThinking,
					Status:         timelineevent.StatusStreaming,
					Content:        "",
					Metadata:       thinkingMeta,
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
					StageID:        &execCtx.StageID,
					ExecutionID:    &execCtx.ExecutionID,
					SequenceNumber: *eventSeq,
					EventType:      timelineevent.EventTypeLlmResponse,
					Content:        "",
					Metadata:       extra, // nil when not forced conclusion
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
					EventType:      timelineevent.EventTypeLlmResponse,
					Status:         timelineevent.StatusStreaming,
					Content:        "",
					Metadata:       extra, // nil when not forced conclusion
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
		finalizeStreamingEvent(ctx, execCtx, thinkingEventID, timelineevent.EventTypeLlmThinking, resp.ThinkingText, "thinking")
	}

	if textEventID != "" {
		finalizeStreamingEvent(ctx, execCtx, textEventID, timelineevent.EventTypeLlmResponse, resp.Text, "text")
	}

	return &StreamedResponse{
		LLMResponse:          resp,
		ThinkingEventCreated: thinkingEventID != "",
		TextEventCreated:     textEventID != "",
	}, nil
}

// mergeMetadata combines base metadata with extra metadata.
// Returns base unchanged if extra is nil; returns extra if base is nil.
func mergeMetadata(base, extra map[string]interface{}) map[string]interface{} {
	if extra == nil {
		return base
	}
	if base == nil {
		return extra
	}
	merged := make(map[string]interface{}, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}
