package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalPayload(t *testing.T) {
	t.Run("marshals normal payload", func(t *testing.T) {
		payload := map[string]interface{}{
			"type":       "timeline_event.created",
			"session_id": "abc-123",
			"content":    "some content",
		}

		result, err := marshalPayload(payload)
		require.NoError(t, err)
		assert.Contains(t, result, "timeline_event.created")
		assert.Contains(t, result, "abc-123")
	})

	t.Run("truncates oversized payload", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'a'
		}
		payload := map[string]interface{}{
			"type":       "stream.chunk",
			"event_id":   "evt-123",
			"session_id": "abc-123",
			"content":    string(longContent),
		}

		result, err := marshalPayload(payload)
		require.NoError(t, err)
		assert.Contains(t, result, "truncated")
		assert.Less(t, len(result), 8000)
	})

	t.Run("does not truncate small payload", func(t *testing.T) {
		payload := map[string]interface{}{
			"type":    "ping",
			"content": "hello",
		}

		result, err := marshalPayload(payload)
		require.NoError(t, err)
		assert.NotContains(t, result, "truncated")
	})

	t.Run("truncated payload preserves key fields", func(t *testing.T) {
		longContent := make([]byte, 8000)
		for i := range longContent {
			longContent[i] = 'x'
		}
		payload := map[string]interface{}{
			"type":       "timeline_event.created",
			"event_id":   "evt-456",
			"session_id": "sess-789",
			"content":    string(longContent),
			"metadata":   map[string]string{"key": "value"},
		}

		result, err := marshalPayload(payload)
		require.NoError(t, err)

		// Key fields preserved in truncated form
		assert.Contains(t, result, "timeline_event.created")
		assert.Contains(t, result, "evt-456")
		assert.Contains(t, result, "sess-789")
		assert.Contains(t, result, `"truncated":true`)
		// Large content and metadata stripped
		assert.NotContains(t, result, "xxxx")
	})

	t.Run("boundary: payload just under limit is not truncated", func(t *testing.T) {
		// Build a payload that is just under 7900 bytes
		// The JSON overhead for {"content":"...","type":"t"} is ~24 bytes
		contentSize := 7900 - 30 // well under limit after JSON encoding
		content := make([]byte, contentSize)
		for i := range content {
			content[i] = 'b'
		}
		payload := map[string]interface{}{
			"type":    "t",
			"content": string(content),
		}

		result, err := marshalPayload(payload)
		require.NoError(t, err)
		assert.NotContains(t, result, "truncated")
	})

	t.Run("empty payload", func(t *testing.T) {
		payload := map[string]interface{}{}
		result, err := marshalPayload(payload)
		require.NoError(t, err)
		assert.Equal(t, "{}", result)
	})
}

func TestNewEventPublisher(t *testing.T) {
	publisher := NewEventPublisher(nil)
	assert.NotNil(t, publisher)
	assert.Nil(t, publisher.db)
}
