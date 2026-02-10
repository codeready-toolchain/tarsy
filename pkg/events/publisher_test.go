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
			Type:      EventTypeTimelineCreated,
			SessionID: "abc-123",
			Content:   "some content",
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
			Type:      EventTypeStreamChunk,
			EventID:   "evt-123",
			SessionID: "abc-123",
			Content:   string(longContent),
		})

		result, err := truncateIfNeeded(string(payload))
		require.NoError(t, err)
		assert.Contains(t, result, "truncated")
		assert.Less(t, len(result), 8000)
	})

	t.Run("does not truncate small payload", func(t *testing.T) {
		payload, _ := json.Marshal(StreamChunkPayload{
			Type:  EventTypeStreamChunk,
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
			Type:      EventTypeTimelineCreated,
			EventID:   "evt-456",
			SessionID: "sess-789",
			Content:   string(longContent),
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
		// Marshal an empty struct first to measure the overhead.
		base, _ := json.Marshal(TimelineCreatedPayload{Type: "t"})
		contentSize := 7900 - len(base) - 20 // 20 bytes safety margin
		content := make([]byte, contentSize)
		for i := range content {
			content[i] = 'b'
		}
		payload, _ := json.Marshal(TimelineCreatedPayload{
			Type:    "t",
			Content: string(content),
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
			Type:      EventTypeTimelineCreated,
			EventID:   "evt-1",
			SessionID: "sess-1",
			Content:   "hello",
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
			Type:      EventTypeTimelineCreated,
			EventID:   "evt-456",
			SessionID: "sess-789",
			Content:   string(longContent),
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
			Type:    EventTypeStreamChunk,
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
