package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// PartialOutputError wraps an LLM error that occurred after partial output
// was produced. Callers can inspect PartialText to include it in retry context.
type PartialOutputError struct {
	Cause           error
	PartialText     string
	PartialThinking string
	IsLoop          bool // true when caused by degenerate loop detection
}

func (e *PartialOutputError) Error() string { return e.Cause.Error() }
func (e *PartialOutputError) Unwrap() error { return e.Cause }

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
// Delegates to collectStreamWithCallback with a nil callback and no loop detection.
func collectStream(stream <-chan agent.Chunk) (*LLMResponse, error) {
	return collectStreamWithCallback(stream, nil, nil)
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

// Loop detection parameters.
const (
	loopCheckInterval = 2000 // check for loops every N chars of accumulated text
	loopMinPatternLen = 30   // shortest repeating unit to look for
	loopMaxPatternLen = 500  // longest repeating unit to try
	loopMinRepeats    = 5    // how many consecutive repetitions trigger detection
	loopWindowSize    = 6000 // only inspect the tail of the text buffer
)

// detectTextLoop checks the tail of text for a substring that repeats at
// least loopMinRepeats times consecutively. Returns true and the byte offset
// where the first repetition starts (safe truncation point).
func detectTextLoop(text string) (bool, int) {
	n := len(text)
	window := loopWindowSize
	if window > n {
		window = n
	}
	tail := text[n-window:]

	for patLen := loopMinPatternLen; patLen <= loopMaxPatternLen; patLen++ {
		if patLen*(loopMinRepeats+1) > len(tail) {
			break
		}
		pattern := tail[len(tail)-patLen:]
		count := 1
		pos := len(tail) - patLen*2
		for pos >= 0 && tail[pos:pos+patLen] == pattern {
			count++
			pos -= patLen
		}
		if count >= loopMinRepeats {
			truncateAt := n - (count * patLen)
			return true, truncateAt
		}
	}
	return false, 0
}

// collectStreamWithCallback collects a stream while calling back for real-time delivery.
// The callback is optional (nil = buffered mode, same as collectStream).
// cancelStream is called to abort the gRPC stream when a degenerate loop is
// detected; pass nil to disable loop detection.
func collectStreamWithCallback(
	stream <-chan agent.Chunk,
	callback StreamCallback,
	cancelStream func(),
) (*LLMResponse, error) {
	resp := &LLMResponse{}
	var textBuf, thinkingBuf strings.Builder
	var lastLoopCheck int
	loopDetected := false

	for chunk := range stream {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			if loopDetected {
				continue // discard further text after loop detected
			}
			textBuf.WriteString(c.Content)
			if callback != nil {
				callback(ChunkTypeText, c.Content)
			}
			// Periodic loop detection
			if cancelStream != nil && textBuf.Len()-lastLoopCheck >= loopCheckInterval {
				lastLoopCheck = textBuf.Len()
				if detected, truncAt := detectTextLoop(textBuf.String()); detected {
					loopLen := textBuf.Len() - truncAt
					slog.Warn("Detected degenerate loop in LLM text output, cancelling stream",
						"text_len", textBuf.Len(), "truncate_at", truncAt, "loop_chars", loopLen)
					text := textBuf.String()[:truncAt]
					textBuf.Reset()
					textBuf.WriteString(text)
					loopDetected = true
					cancelStream()
				}
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
			if loopDetected {
				continue // expected error from stream cancellation
			}
			return nil, &PartialOutputError{
				Cause: fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
					c.Message, c.Code, c.Retryable),
				PartialText:     textBuf.String(),
				PartialThinking: thinkingBuf.String(),
			}
		}
	}

	resp.Text = textBuf.String()
	resp.ThinkingText = thinkingBuf.String()

	if loopDetected {
		return nil, &PartialOutputError{
			Cause:           fmt.Errorf("LLM output stuck in repetitive loop, cancelled after %d chars of text", len(resp.Text)),
			PartialText:     resp.Text,
			PartialThinking: resp.ThinkingText,
			IsLoop:          true,
		}
	}

	return resp, nil
}

// StreamedResponse wraps an LLMResponse with information about streaming
// timeline events that were created during the LLM call. Controllers should
// check these flags and skip creating duplicate events.
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
	pid := parentExecID(execCtx)
	pidPtr := parentExecIDPtr(execCtx)

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
					SessionID:         execCtx.SessionID,
					StageID:           &execCtx.StageID,
					ExecutionID:       &execCtx.ExecutionID,
					ParentExecutionID: pidPtr,
					SequenceNumber:    *eventSeq,
					EventType:         timelineevent.EventTypeLlmThinking,
					Content:           "",
					Metadata:          thinkingMeta,
				})
				if createErr != nil {
					slog.Warn("Failed to create streaming thinking event", "session_id", execCtx.SessionID, "error", createErr)
					thinkingCreateFailed = true
					return
				}
				thinkingEventID = event.ID
				if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
					BasePayload: events.BasePayload{
						Type:      events.EventTypeTimelineCreated,
						SessionID: execCtx.SessionID,
						Timestamp: event.CreatedAt.Format(time.RFC3339Nano),
					},
					EventID:           thinkingEventID,
					StageID:           execCtx.StageID,
					ExecutionID:       execCtx.ExecutionID,
					ParentExecutionID: pid,
					EventType:         timelineevent.EventTypeLlmThinking,
					Status:            timelineevent.StatusStreaming,
					Content:           "",
					Metadata:          thinkingMeta,
					SequenceNumber:    *eventSeq,
				}); pubErr != nil {
					slog.Warn("Failed to publish streaming thinking created",
						"event_id", thinkingEventID, "session_id", execCtx.SessionID, "error", pubErr)
				}
			}
			// Publish only the new delta — clients concatenate locally.
			// This keeps each pg_notify payload small (avoids 8 KB limit).
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeStreamChunk,
					SessionID: execCtx.SessionID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				EventID:           thinkingEventID,
				ParentExecutionID: pid,
				Delta:             delta,
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
					SessionID:         execCtx.SessionID,
					StageID:           &execCtx.StageID,
					ExecutionID:       &execCtx.ExecutionID,
					ParentExecutionID: pidPtr,
					SequenceNumber:    *eventSeq,
					EventType:         timelineevent.EventTypeLlmResponse,
					Content:           "",
					Metadata:          extra, // nil when not forced conclusion
				})
				if createErr != nil {
					slog.Warn("Failed to create streaming text event", "session_id", execCtx.SessionID, "error", createErr)
					textCreateFailed = true
					return
				}
				textEventID = event.ID
				if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
					BasePayload: events.BasePayload{
						Type:      events.EventTypeTimelineCreated,
						SessionID: execCtx.SessionID,
						Timestamp: event.CreatedAt.Format(time.RFC3339Nano),
					},
					EventID:           textEventID,
					StageID:           execCtx.StageID,
					ExecutionID:       execCtx.ExecutionID,
					ParentExecutionID: pid,
					EventType:         timelineevent.EventTypeLlmResponse,
					Status:            timelineevent.StatusStreaming,
					Content:           "",
					Metadata:          extra, // nil when not forced conclusion
					SequenceNumber:    *eventSeq,
				}); pubErr != nil {
					slog.Warn("Failed to publish streaming text created",
						"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
				}
			}
			// Publish only the new delta — clients concatenate locally.
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeStreamChunk,
					SessionID: execCtx.SessionID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				EventID:           textEventID,
				ParentExecutionID: pid,
				Delta:             delta,
			}); pubErr != nil {
				slog.Warn("Failed to publish text stream chunk",
					"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		}
	}

	resp, err := collectStreamWithCallback(stream, callback, llmCancel)
	if err != nil {
		var poe *PartialOutputError
		if errors.As(err, &poe) && poe.IsLoop {
			// Loop detected: finalize streaming events with truncated text
			// (the valid portion before the loop started).
			if thinkingEventID != "" {
				finalizeStreamingEvent(ctx, execCtx, thinkingEventID, timelineevent.EventTypeLlmThinking, poe.PartialThinking, "thinking")
			}
			if textEventID != "" {
				finalizeStreamingEvent(ctx, execCtx, textEventID, timelineevent.EventTypeLlmResponse, poe.PartialText, "text")
			}
		} else {
			// Stream error: mark events as failed so they don't stay stuck
			// at status "streaming" indefinitely.
			// Use a detached context: the caller's context (iterCtx) is likely
			// already cancelled/expired, but the DB cleanup must still complete.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			markStreamingEventsFailed(cleanupCtx, execCtx, thinkingEventID, textEventID, err)
		}
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
