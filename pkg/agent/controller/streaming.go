package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// LLMErrorCode identifies the category of error returned by the LLM service.
// Codes originate as strings in the Python gRPC service and are converted at
// the Go boundary when populating PartialOutputError.
type LLMErrorCode string

const (
	LLMErrorMaxRetries         LLMErrorCode = "max_retries"              // Python already retried 3x
	LLMErrorCredentials        LLMErrorCode = "credentials"              // missing or invalid API key
	LLMErrorProviderError      LLMErrorCode = "provider_error"           // upstream provider failure
	LLMErrorInvalidRequest     LLMErrorCode = "invalid_request"          // malformed request
	LLMErrorPartialStreamError LLMErrorCode = "partial_stream_error"     // error mid-stream after partial output
	LLMErrorInitialTimeout     LLMErrorCode = "initial_response_timeout" // no chunks received within deadline
	LLMErrorStallTimeout       LLMErrorCode = "stall_timeout"            // gap between chunks exceeded deadline
)

// PartialOutputError wraps an LLM error that occurred after partial output
// was produced. Callers can inspect PartialText to include it in retry context.
type PartialOutputError struct {
	Cause           error
	PartialText     string
	PartialThinking string
	IsLoop          bool         // true when caused by degenerate loop detection
	Code            LLMErrorCode // error code from LLM service
	Retryable       bool         // whether the LLM service considers the error retryable
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
	return collectStreamWithCallback(stream, nil, nil, 0, 0)
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
// cancelStream is called to abort the gRPC stream when a degenerate loop or
// adaptive timeout is detected; pass nil to disable loop detection.
//
// initialResponseTimeout: max wait for the first chunk (0 = disabled).
// stallTimeout: max gap between consecutive chunks (0 = disabled).
func collectStreamWithCallback(
	stream <-chan agent.Chunk,
	callback StreamCallback,
	cancelStream func(),
	initialResponseTimeout time.Duration,
	stallTimeout time.Duration,
) (*LLMResponse, error) {
	resp := &LLMResponse{}
	var textBuf, thinkingBuf strings.Builder
	var lastLoopCheck int
	loopDetected := false

	// Adaptive timeout setup. A nil channel blocks forever in select,
	// effectively disabling that timeout branch.
	firstChunkReceived := false
	var timer *time.Timer
	var timeoutCh <-chan time.Time

	if initialResponseTimeout > 0 {
		timer = time.NewTimer(initialResponseTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

loop:
	for {
		select {
		case chunk, ok := <-stream:
			if !ok {
				break loop
			}

			// Adaptive timeout bookkeeping: switch/reset timer on chunk arrival.
			if !firstChunkReceived {
				firstChunkReceived = true
				if stallTimeout > 0 {
					if timer == nil {
						timer = time.NewTimer(stallTimeout)
						defer timer.Stop()
					} else {
						timer.Reset(stallTimeout)
					}
					timeoutCh = timer.C
				} else if timer != nil {
					timer.Stop()
					timeoutCh = nil
				}
			} else if stallTimeout > 0 && timer != nil {
				timer.Reset(stallTimeout)
			}

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
					Code:            LLMErrorCode(c.Code),
					Retryable:       c.Retryable,
				}
			}

		case <-timeoutCh:
			if cancelStream != nil {
				cancelStream()
			}
			code := LLMErrorInitialTimeout
			msg := fmt.Sprintf("no response from provider within %s", initialResponseTimeout)
			if firstChunkReceived {
				code = LLMErrorStallTimeout
				msg = fmt.Sprintf("stream stalled: no data for %s", stallTimeout)
			}
			return nil, &PartialOutputError{
				Cause:           fmt.Errorf("adaptive timeout: %s", msg),
				PartialText:     textBuf.String(),
				PartialThinking: thinkingBuf.String(),
				Code:            code,
				Retryable:       true,
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

	// Batch stream.chunk deltas: accumulate per-event deltas and publish
	// combined deltas every chunkFlushInterval. Reduces pg_notify calls
	// from ~50/sec to ~10/sec per active stream.
	const chunkFlushInterval = 50 * time.Millisecond
	var mu sync.Mutex
	var pendingThinkingDelta, pendingTextDelta string
	lastChunkFlush := time.Now()

	// flushPendingDeltas publishes accumulated deltas. Caller must hold mu.
	flushPendingDeltas := func() {
		if pendingThinkingDelta != "" && thinkingEventID != "" {
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeStreamChunk,
					SessionID: execCtx.SessionID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				EventID:           thinkingEventID,
				ParentExecutionID: pid,
				Delta:             pendingThinkingDelta,
			}); pubErr != nil {
				slog.Warn("Failed to publish thinking stream chunk",
					"event_id", thinkingEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
			pendingThinkingDelta = ""
		}
		if pendingTextDelta != "" && textEventID != "" {
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeStreamChunk,
					SessionID: execCtx.SessionID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				EventID:           textEventID,
				ParentExecutionID: pid,
				Delta:             pendingTextDelta,
			}); pubErr != nil {
				slog.Warn("Failed to publish text stream chunk",
					"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
			pendingTextDelta = ""
		}
		lastChunkFlush = time.Now()
	}

	// Periodic flusher ensures buffered deltas are published even when
	// the stream is sparse (no new chunks arriving to trigger inline flush).
	flusherDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(chunkFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				flushPendingDeltas()
				mu.Unlock()
			case <-flusherDone:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	callback := func(chunkType string, delta string) {
		if delta == "" {
			return
		}

		mu.Lock()
		defer mu.Unlock()

		switch chunkType {
		case ChunkTypeThinking:
			if thinkingCreateFailed {
				return
			}
			if thinkingEventID == "" {
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
			pendingThinkingDelta += delta

		case ChunkTypeText:
			if textCreateFailed {
				return
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
					Metadata:          extra,
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
					Metadata:          extra,
					SequenceNumber:    *eventSeq,
				}); pubErr != nil {
					slog.Warn("Failed to publish streaming text created",
						"event_id", textEventID, "session_id", execCtx.SessionID, "error", pubErr)
				}
			}
			pendingTextDelta += delta
		}

		if time.Since(lastChunkFlush) >= chunkFlushInterval {
			flushPendingDeltas()
		}
	}

	resp, err := collectStreamWithCallback(stream, callback, llmCancel,
		execCtx.Config.InitialResponseTimeout, execCtx.Config.StallTimeout)
	close(flusherDone)
	mu.Lock()
	flushPendingDeltas()
	mu.Unlock()
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
			// Stream error: mark events with the appropriate terminal status
			// so they don't stay stuck at status "streaming" indefinitely.
			// Use a detached context: the caller's context (iterCtx) is likely
			// already cancelled/expired, but the DB cleanup must still complete.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			evtStatus := timelineevent.StatusFailed
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				evtStatus = timelineevent.StatusTimedOut
			} else if ctx.Err() != nil {
				evtStatus = timelineevent.StatusCancelled
			}
			markStreamingEventsTerminal(cleanupCtx, execCtx, thinkingEventID, textEventID, err, evtStatus)
		}
		return nil, err
	}

	// Finalize streaming timeline events.
	// Always finalize if the event was created (thinkingEventID/textEventID set),
	// even when resp content is empty. Otherwise the event stays at "streaming"
	// status indefinitely. The empty-delta guard above prevents event creation
	// for purely empty chunks, but we handle the edge case defensively here.
	// Use a detached context: the caller's context (iterCtx) may already be
	// cancelled/expired, but the DB cleanup must still complete.
	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finalizeCancel()

	if thinkingEventID != "" {
		finalizeStreamingEvent(finalizeCtx, execCtx, thinkingEventID, timelineevent.EventTypeLlmThinking, resp.ThinkingText, "thinking")
	}

	if textEventID != "" {
		finalizeStreamingEvent(finalizeCtx, execCtx, textEventID, timelineevent.EventTypeLlmResponse, resp.Text, "text")
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
