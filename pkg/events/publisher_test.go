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
		// Create a payload that exceeds 7900 bytes
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

		// Should be truncated — contains "truncated" flag
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
}
