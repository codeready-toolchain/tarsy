package events

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimelineCreatedPayload(t *testing.T) {
	t.Run("creates timeline created payload with all fields", func(t *testing.T) {
		payload := TimelineCreatedPayload{
			Type:           EventTypeTimelineCreated,
			EventID:        "event-123",
			SessionID:      "session-abc",
			StageID:        "stage-1",
			ExecutionID:    "exec-1",
			EventType:      timelineevent.EventTypeLlmThinking,
			Status:         timelineevent.StatusStreaming,
			Content:        "Analyzing the alert...",
			Metadata:       map[string]any{"source": "native"},
			SequenceNumber: 5,
			Timestamp:      time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, EventTypeTimelineCreated, payload.Type)
		assert.Equal(t, "event-123", payload.EventID)
		assert.Equal(t, "session-abc", payload.SessionID)
		assert.Equal(t, "stage-1", payload.StageID)
		assert.Equal(t, "exec-1", payload.ExecutionID)
		assert.Equal(t, timelineevent.EventTypeLlmThinking, payload.EventType)
		assert.Equal(t, timelineevent.StatusStreaming, payload.Status)
		assert.Equal(t, "Analyzing the alert...", payload.Content)
		assert.Equal(t, 5, payload.SequenceNumber)
		assert.NotEmpty(t, payload.Timestamp)
		require.NotNil(t, payload.Metadata)
		assert.Equal(t, "native", payload.Metadata["source"])
	})

	t.Run("creates session-level timeline event without stage and execution", func(t *testing.T) {
		payload := TimelineCreatedPayload{
			Type:           EventTypeTimelineCreated,
			EventID:        "event-456",
			SessionID:      "session-xyz",
			EventType:      timelineevent.EventTypeExecutiveSummary,
			Status:         timelineevent.StatusCompleted,
			Content:        "Executive summary content",
			SequenceNumber: 100,
			Timestamp:      time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, "session-xyz", payload.SessionID)
		assert.Empty(t, payload.StageID, "session-level event should have empty stage_id")
		assert.Empty(t, payload.ExecutionID, "session-level event should have empty execution_id")
		assert.Equal(t, timelineevent.EventTypeExecutiveSummary, payload.EventType)
	})

	t.Run("handles empty content for streaming events", func(t *testing.T) {
		payload := TimelineCreatedPayload{
			Type:           EventTypeTimelineCreated,
			EventID:        "event-789",
			SessionID:      "session-123",
			StageID:        "stage-2",
			ExecutionID:    "exec-2",
			EventType:      timelineevent.EventTypeLlmResponse,
			Status:         timelineevent.StatusStreaming,
			Content:        "", // Empty content is allowed for streaming
			SequenceNumber: 1,
			Timestamp:      time.Now().Format(time.RFC3339Nano),
		}

		assert.Empty(t, payload.Content)
		assert.Equal(t, timelineevent.StatusStreaming, payload.Status)
	})

	t.Run("supports various event types", func(t *testing.T) {
		eventTypes := []timelineevent.EventType{
			timelineevent.EventTypeLlmThinking,
			timelineevent.EventTypeLlmResponse,
			timelineevent.EventTypeLlmToolCall,
			timelineevent.EventTypeMcpToolSummary,
			timelineevent.EventTypeCodeExecution,
			timelineevent.EventTypeGoogleSearchResult,
			timelineevent.EventTypeURLContextResult,
			timelineevent.EventTypeExecutiveSummary,
		}

		for _, eventType := range eventTypes {
			payload := TimelineCreatedPayload{
				Type:           EventTypeTimelineCreated,
				EventID:        "event-id",
				SessionID:      "session-id",
				EventType:      eventType,
				Status:         timelineevent.StatusCompleted,
				Content:        "test content",
				SequenceNumber: 1,
				Timestamp:      time.Now().Format(time.RFC3339Nano),
			}

			assert.Equal(t, eventType, payload.EventType)
		}
	})

	t.Run("supports all status types", func(t *testing.T) {
		statuses := []timelineevent.Status{
			timelineevent.StatusStreaming,
			timelineevent.StatusCompleted,
			timelineevent.StatusFailed,
			timelineevent.StatusCancelled,
			timelineevent.StatusTimedOut,
		}

		for _, status := range statuses {
			payload := TimelineCreatedPayload{
				Type:           EventTypeTimelineCreated,
				EventID:        "event-id",
				SessionID:      "session-id",
				EventType:      timelineevent.EventTypeLlmResponse,
				Status:         status,
				Content:        "content",
				SequenceNumber: 1,
				Timestamp:      time.Now().Format(time.RFC3339Nano),
			}

			assert.Equal(t, status, payload.Status)
		}
	})

	t.Run("metadata is optional", func(t *testing.T) {
		payload := TimelineCreatedPayload{
			Type:           EventTypeTimelineCreated,
			EventID:        "event-id",
			SessionID:      "session-id",
			EventType:      timelineevent.EventTypeLlmResponse,
			Status:         timelineevent.StatusCompleted,
			Content:        "content",
			SequenceNumber: 1,
			Timestamp:      time.Now().Format(time.RFC3339Nano),
			Metadata:       nil,
		}

		assert.Nil(t, payload.Metadata)
	})
}

func TestTimelineCompletedPayload(t *testing.T) {
	t.Run("creates timeline completed payload", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "event-123",
			Content:   "Final analysis complete",
			Status:    timelineevent.StatusCompleted,
			Metadata:  map[string]any{"duration_ms": 1500},
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, EventTypeTimelineCompleted, payload.Type)
		assert.Equal(t, "event-123", payload.EventID)
		assert.Equal(t, "Final analysis complete", payload.Content)
		assert.Equal(t, timelineevent.StatusCompleted, payload.Status)
		assert.NotEmpty(t, payload.Timestamp)
		require.NotNil(t, payload.Metadata)
		assert.Equal(t, 1500, payload.Metadata["duration_ms"])
	})

	t.Run("supports failed status", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "event-456",
			Content:   "Streaming failed: rate limit exceeded",
			Status:    timelineevent.StatusFailed,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, timelineevent.StatusFailed, payload.Status)
		assert.Contains(t, payload.Content, "rate limit exceeded")
	})

	t.Run("supports cancelled status", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "event-789",
			Content:   "Operation cancelled",
			Status:    timelineevent.StatusCancelled,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, timelineevent.StatusCancelled, payload.Status)
	})

	t.Run("supports timed out status", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "event-abc",
			Content:   "Operation timed out",
			Status:    timelineevent.StatusTimedOut,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, timelineevent.StatusTimedOut, payload.Status)
	})

	t.Run("metadata is optional", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "event-def",
			Content:   "Completed",
			Status:    timelineevent.StatusCompleted,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Nil(t, payload.Metadata)
	})

	t.Run("tool call completion with is_error metadata", func(t *testing.T) {
		payload := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "tool-event-123",
			Content:   "Tool execution failed: not found",
			Status:    timelineevent.StatusCompleted,
			Metadata:  map[string]any{"is_error": true},
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		require.NotNil(t, payload.Metadata)
		assert.Equal(t, true, payload.Metadata["is_error"])
	})
}

func TestStreamChunkPayload(t *testing.T) {
	t.Run("creates stream chunk payload", func(t *testing.T) {
		payload := StreamChunkPayload{
			Type:      EventTypeStreamChunk,
			EventID:   "event-123",
			Delta:     "The analysis shows ",
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, EventTypeStreamChunk, payload.Type)
		assert.Equal(t, "event-123", payload.EventID)
		assert.Equal(t, "The analysis shows ", payload.Delta)
		assert.NotEmpty(t, payload.Timestamp)
	})

	t.Run("delta contains incremental content only", func(t *testing.T) {
		// Simulate streaming chunks
		chunks := []string{"The ", "answer ", "is ", "42."}

		var payloads []StreamChunkPayload
		for _, delta := range chunks {
			payloads = append(payloads, StreamChunkPayload{
				Type:      EventTypeStreamChunk,
				EventID:   "event-456",
				Delta:     delta,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			})
		}

		assert.Len(t, payloads, 4)
		assert.Equal(t, "The ", payloads[0].Delta)
		assert.Equal(t, "answer ", payloads[1].Delta)
		assert.Equal(t, "is ", payloads[2].Delta)
		assert.Equal(t, "42.", payloads[3].Delta)
	})

	t.Run("handles single character delta", func(t *testing.T) {
		payload := StreamChunkPayload{
			Type:      EventTypeStreamChunk,
			EventID:   "event-789",
			Delta:     ".",
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, ".", payload.Delta)
	})

	t.Run("handles empty delta", func(t *testing.T) {
		// Empty deltas should not be published in practice, but payload structure allows it
		payload := StreamChunkPayload{
			Type:      EventTypeStreamChunk,
			EventID:   "event-abc",
			Delta:     "",
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Empty(t, payload.Delta)
	})

	t.Run("handles multi-line delta", func(t *testing.T) {
		payload := StreamChunkPayload{
			Type:      EventTypeStreamChunk,
			EventID:   "event-def",
			Delta:     "Line 1\nLine 2\nLine 3",
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Contains(t, payload.Delta, "\n")
		assert.Equal(t, "Line 1\nLine 2\nLine 3", payload.Delta)
	})
}

func TestSessionStatusPayload(t *testing.T) {
	t.Run("creates session status payload", func(t *testing.T) {
		payload := SessionStatusPayload{
			Type:      EventTypeSessionStatus,
			SessionID: "session-123",
			Status:    alertsession.StatusInProgress,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, EventTypeSessionStatus, payload.Type)
		assert.Equal(t, "session-123", payload.SessionID)
		assert.Equal(t, alertsession.StatusInProgress, payload.Status)
		assert.NotEmpty(t, payload.Timestamp)
	})

	t.Run("supports all session statuses", func(t *testing.T) {
		statuses := []alertsession.Status{
			alertsession.StatusPending,
			alertsession.StatusInProgress,
			alertsession.StatusCancelling,
			alertsession.StatusCompleted,
			alertsession.StatusFailed,
			alertsession.StatusCancelled,
			alertsession.StatusTimedOut,
		}

		for _, status := range statuses {
			payload := SessionStatusPayload{
				Type:      EventTypeSessionStatus,
				SessionID: "session-456",
				Status:    status,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}

			assert.Equal(t, status, payload.Status)
		}
	})

	t.Run("pending status at session start", func(t *testing.T) {
		payload := SessionStatusPayload{
			Type:      EventTypeSessionStatus,
			SessionID: "session-new",
			Status:    alertsession.StatusPending,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, alertsession.StatusPending, payload.Status)
	})

	t.Run("in_progress status when claimed", func(t *testing.T) {
		payload := SessionStatusPayload{
			Type:      EventTypeSessionStatus,
			SessionID: "session-claimed",
			Status:    alertsession.StatusInProgress,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, alertsession.StatusInProgress, payload.Status)
	})

	t.Run("completed status at session end", func(t *testing.T) {
		payload := SessionStatusPayload{
			Type:      EventTypeSessionStatus,
			SessionID: "session-done",
			Status:    alertsession.StatusCompleted,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, alertsession.StatusCompleted, payload.Status)
	})
}

func TestStageStatusPayload(t *testing.T) {
	t.Run("creates stage status payload with all fields", func(t *testing.T) {
		payload := StageStatusPayload{
			Type:       EventTypeStageStatus,
			SessionID:  "session-123",
			StageID:    "stage-456",
			StageName:  "Deep Dive",
			StageIndex: 2,
			Status:     "completed",
			Timestamp:  time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, EventTypeStageStatus, payload.Type)
		assert.Equal(t, "session-123", payload.SessionID)
		assert.Equal(t, "stage-456", payload.StageID)
		assert.Equal(t, "Deep Dive", payload.StageName)
		assert.Equal(t, 2, payload.StageIndex)
		assert.Equal(t, "completed", payload.Status)
		assert.NotEmpty(t, payload.Timestamp)
	})

	t.Run("stage started event may have empty stage_id", func(t *testing.T) {
		payload := StageStatusPayload{
			Type:       EventTypeStageStatus,
			SessionID:  "session-789",
			StageID:    "", // Empty on "started" before stage creation
			StageName:  "Initial Analysis",
			StageIndex: 1,
			Status:     "started",
			Timestamp:  time.Now().Format(time.RFC3339Nano),
		}

		assert.Empty(t, payload.StageID)
		assert.Equal(t, "started", payload.Status)
	})

	t.Run("supports various stage statuses", func(t *testing.T) {
		statuses := []string{
			"started",
			"completed",
			"failed",
			"timed_out",
			"cancelled",
		}

		for _, status := range statuses {
			payload := StageStatusPayload{
				Type:       EventTypeStageStatus,
				SessionID:  "session-abc",
				StageID:    "stage-def",
				StageName:  "Test Stage",
				StageIndex: 1,
				Status:     status,
				Timestamp:  time.Now().Format(time.RFC3339Nano),
			}

			assert.Equal(t, status, payload.Status)
		}
	})

	t.Run("stage index is 1-based", func(t *testing.T) {
		payload := StageStatusPayload{
			Type:       EventTypeStageStatus,
			SessionID:  "session-123",
			StageID:    "stage-first",
			StageName:  "First Stage",
			StageIndex: 1,
			Status:     "started",
			Timestamp:  time.Now().Format(time.RFC3339Nano),
		}

		assert.Equal(t, 1, payload.StageIndex)
	})

	t.Run("multi-stage session with sequential indices", func(t *testing.T) {
		stages := []StageStatusPayload{
			{
				Type:       EventTypeStageStatus,
				SessionID:  "session-multi",
				StageID:    "stage-1",
				StageName:  "Initial Analysis",
				StageIndex: 1,
				Status:     "completed",
				Timestamp:  time.Now().Format(time.RFC3339Nano),
			},
			{
				Type:       EventTypeStageStatus,
				SessionID:  "session-multi",
				StageID:    "stage-2",
				StageName:  "Deep Dive",
				StageIndex: 2,
				Status:     "started",
				Timestamp:  time.Now().Format(time.RFC3339Nano),
			},
		}

		assert.Equal(t, 1, stages[0].StageIndex)
		assert.Equal(t, 2, stages[1].StageIndex)
		assert.Equal(t, "session-multi", stages[0].SessionID)
		assert.Equal(t, "session-multi", stages[1].SessionID)
	})
}

func TestPayloadTypes(t *testing.T) {
	t.Run("all payload types have correct type field", func(t *testing.T) {
		timelineCreated := TimelineCreatedPayload{
			Type:           EventTypeTimelineCreated,
			EventID:        "e1",
			SessionID:      "s1",
			EventType:      timelineevent.EventTypeLlmResponse,
			Status:         timelineevent.StatusCompleted,
			Content:        "content",
			SequenceNumber: 1,
			Timestamp:      time.Now().Format(time.RFC3339Nano),
		}
		assert.Equal(t, EventTypeTimelineCreated, timelineCreated.Type)

		timelineCompleted := TimelineCompletedPayload{
			Type:      EventTypeTimelineCompleted,
			EventID:   "e2",
			Content:   "content",
			Status:    timelineevent.StatusCompleted,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}
		assert.Equal(t, EventTypeTimelineCompleted, timelineCompleted.Type)

		streamChunk := StreamChunkPayload{
			Type:      EventTypeStreamChunk,
			EventID:   "e3",
			Delta:     "delta",
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}
		assert.Equal(t, EventTypeStreamChunk, streamChunk.Type)

		sessionStatus := SessionStatusPayload{
			Type:      EventTypeSessionStatus,
			SessionID: "s1",
			Status:    alertsession.StatusInProgress,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		}
		assert.Equal(t, EventTypeSessionStatus, sessionStatus.Type)

		stageStatus := StageStatusPayload{
			Type:       EventTypeStageStatus,
			SessionID:  "s1",
			StageID:    "st1",
			StageName:  "Stage",
			StageIndex: 1,
			Status:     "started",
			Timestamp:  time.Now().Format(time.RFC3339Nano),
		}
		assert.Equal(t, EventTypeStageStatus, stageStatus.Type)
	})
}