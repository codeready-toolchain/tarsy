package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateIfNeeded(t *testing.T) {
	t.Run("passes through normal payload", func(t *testing.T) {
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: "abc-123",
			},
			Content: "some content",
		})

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)
		assert.Contains(t, result, EventTypeTimelineCreated)
		assert.Contains(t, result, "abc-123")
	})

	t.Run("truncates oversized payload", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'a'
		}
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: "abc-123",
			},
			EventID: "evt-123",
			Content: string(longContent),
		})

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)
		assert.Contains(t, result, "truncated")
		assert.Less(t, len(result), 8000)
	})

	t.Run("does not truncate small payload", func(t *testing.T) {
		payload, _ := json.Marshal(StreamChunkPayload{
			BasePayload: BasePayload{
				Type: EventTypeStreamChunk,
			},
			Delta: "hello",
		})

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)
		assert.NotContains(t, result, "truncated")
	})

	t.Run("truncated payload preserves key fields", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'x'
		}
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: "sess-789",
			},
			EventID: "evt-456",
			Content: string(longContent),
		})

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)

		assert.Contains(t, result, EventTypeTimelineCreated)
		assert.Contains(t, result, "evt-456")
		assert.Contains(t, result, "sess-789")
		assert.Contains(t, result, `"truncated":true`)
		assert.NotContains(t, result, "xxxx")
	})

	t.Run("boundary: payload just under limit is not truncated", func(t *testing.T) {
		// Build a payload whose JSON is just under 7900 bytes.
		// Marshal an empty struct first to measure the overhead of the struct's
		// fixed fields (keys, quotes, separators). The 20-byte safety margin
		// accounts for JSON encoding variability: if new fields with non-zero
		// defaults are added to TimelineCreatedPayload, the base overhead grows
		// and the margin prevents the test from flipping unexpectedly.
		base, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{Type: "t"},
		})
		contentSize := 7900 - len(base) - 20
		content := make([]byte, contentSize)
		for i := range content {
			content[i] = 'b'
		}
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{Type: "t"},
			Content:     string(content),
		})
		require.LessOrEqual(t, len(payload), 7900, "test payload should be under limit")

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)
		assert.NotContains(t, result, "truncated")
	})

	t.Run("empty JSON object", func(t *testing.T) {
		result, err := truncateIfNeeded("{}")
		require.NoError(t, err)
		assert.Equal(t, "{}", result)
	})
}

func TestInjectDBEventIDAndTruncate(t *testing.T) {
	t.Run("injects db_event_id into normal payload", func(t *testing.T) {
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: "sess-1",
			},
			EventID: "evt-1",
			Content: "hello",
		})

		result, err := injectDBEventIDAndTruncate(payload, 42)
		require.NoError(t, err)
		assert.Contains(t, result, `"db_event_id":42`)
		assert.Contains(t, result, "evt-1")
	})

	t.Run("truncated payload preserves db_event_id", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'x'
		}
		payload, _ := json.Marshal(TimelineCreatedPayload{
			BasePayload: BasePayload{
				Type:      EventTypeTimelineCreated,
				SessionID: "sess-789",
			},
			EventID: "evt-456",
			Content: string(longContent),
		})

		result, err := injectDBEventIDAndTruncate(payload, 42)
		require.NoError(t, err)
		assert.Contains(t, result, `"truncated":true`)
		assert.Contains(t, result, `"db_event_id":42`)
		assert.Contains(t, result, "evt-456")
	})

	t.Run("truncated payload without session_id omits it", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'x'
		}
		payload, _ := json.Marshal(StreamChunkPayload{
			BasePayload: BasePayload{
				Type: EventTypeStreamChunk,
			},
			EventID: "evt-789",
			Delta:   string(longContent),
		})

		result, err := injectDBEventIDAndTruncate(payload, 99)
		require.NoError(t, err)
		assert.Contains(t, result, `"truncated":true`)
		assert.Contains(t, result, `"db_event_id":99`)
	})
}

func TestNewEventPublisher(t *testing.T) {
	publisher := NewEventPublisher(nil)
	assert.NotNil(t, publisher)
	assert.Nil(t, publisher.db)
}

func TestStageStatusPayload_JSON(t *testing.T) {
	payload := StageStatusPayload{
		BasePayload: BasePayload{
			Type:      EventTypeStageStatus,
			SessionID: "sess-123",
			Timestamp: "2026-02-10T12:00:00Z",
		},
		StageID:    "stage-456",
		StageName:  "investigation",
		StageIndex: 1,
		Status:     StageStatusStarted,
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded StageStatusPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, EventTypeStageStatus, decoded.Type)
	assert.Equal(t, "sess-123", decoded.SessionID)
	assert.Equal(t, "stage-456", decoded.StageID)
	assert.Equal(t, "investigation", decoded.StageName)
	assert.Equal(t, 1, decoded.StageIndex)
	assert.Equal(t, StageStatusStarted, decoded.Status)
	assert.Equal(t, "2026-02-10T12:00:00Z", decoded.Timestamp)
}

func TestStageStatusPayload_EmptyStageID(t *testing.T) {
	// StageID can be empty on "started" events (stage not yet created in DB)
	payload := StageStatusPayload{
		BasePayload: BasePayload{
			Type:      EventTypeStageStatus,
			SessionID: "sess-123",
			Timestamp: "2026-02-10T12:00:00Z",
		},
		StageName:  "investigation",
		StageIndex: 1,
		Status:     StageStatusStarted,
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	// StageID should be omitted from JSON due to omitempty
	assert.NotContains(t, string(data), "stage_id")
}

func TestSessionProgressPayload_JSON(t *testing.T) {
	payload := SessionProgressPayload{
		BasePayload: BasePayload{
			Type:      EventTypeSessionProgress,
			SessionID: "sess-100",
			Timestamp: "2026-02-13T10:00:00Z",
		},
		CurrentStageName:  "analysis",
		CurrentStageIndex: 2,
		TotalStages:       3,
		ActiveExecutions:  1,
		StatusText:        "Starting stage: analysis",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded SessionProgressPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, EventTypeSessionProgress, decoded.Type)
	assert.Equal(t, "sess-100", decoded.SessionID)
	assert.Equal(t, "analysis", decoded.CurrentStageName)
	assert.Equal(t, 2, decoded.CurrentStageIndex)
	assert.Equal(t, 3, decoded.TotalStages)
	assert.Equal(t, 1, decoded.ActiveExecutions)
	assert.Equal(t, "Starting stage: analysis", decoded.StatusText)
}

func TestExecutionProgressPayload_JSON(t *testing.T) {
	payload := ExecutionProgressPayload{
		BasePayload: BasePayload{
			Type:      EventTypeExecutionProgress,
			SessionID: "sess-200",
			Timestamp: "2026-02-13T10:00:00Z",
		},
		StageID:     "stg-1",
		ExecutionID: "exec-1",
		Phase:       ProgressPhaseInvestigating,
		Message:     "Iteration 1/5",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded ExecutionProgressPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, EventTypeExecutionProgress, decoded.Type)
	assert.Equal(t, "sess-200", decoded.SessionID)
	assert.Equal(t, "stg-1", decoded.StageID)
	assert.Equal(t, "exec-1", decoded.ExecutionID)
	assert.Equal(t, ProgressPhaseInvestigating, decoded.Phase)
	assert.Equal(t, "Iteration 1/5", decoded.Message)
}

func TestInteractionCreatedPayload_JSON(t *testing.T) {
	payload := InteractionCreatedPayload{
		BasePayload: BasePayload{
			Type:      EventTypeInteractionCreated,
			SessionID: "sess-300",
			Timestamp: "2026-02-13T10:00:00Z",
		},
		StageID:         "stg-2",
		ExecutionID:     "exec-2",
		InteractionID:   "int-1",
		InteractionType: InteractionTypeLLM,
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded InteractionCreatedPayload
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, EventTypeInteractionCreated, decoded.Type)
	assert.Equal(t, "sess-300", decoded.SessionID)
	assert.Equal(t, "int-1", decoded.InteractionID)
	assert.Equal(t, InteractionTypeLLM, decoded.InteractionType)
	assert.Equal(t, "stg-2", decoded.StageID)
	assert.Equal(t, "exec-2", decoded.ExecutionID)
}
