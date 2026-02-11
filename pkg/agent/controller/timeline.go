package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

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
			slog.Warn("EventPublisher is nil, skipping "+label+" completion publish",
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
		slog.Warn("EventPublisher is nil, skipping "+label+" failure publish",
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
		failContent := "Streaming failed"
		if streamErr != nil {
			failContent = fmt.Sprintf("Streaming failed: %s", streamErr.Error())
		}
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
			slog.Warn("EventPublisher is nil, skipping streaming event failure publish",
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
