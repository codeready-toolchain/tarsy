package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionChannel(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		want      string
	}{
		{
			name:      "formats session channel correctly",
			sessionID: "abc-123",
			want:      "session:abc-123",
		},
		{
			name:      "handles UUID format",
			sessionID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "session:550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "handles empty string",
			sessionID: "",
			want:      "session:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SessionChannel(tt.sessionID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEventTypeConstants(t *testing.T) {
	// Verify event types are non-empty and distinct
	types := []string{
		EventTypeTimelineCreated,
		EventTypeTimelineCompleted,
		EventTypeSessionStatus,
		EventTypeStageStatus,
		EventTypeStreamChunk,
	}

	seen := make(map[string]bool)
	for _, typ := range types {
		assert.NotEmpty(t, typ, "event type should not be empty")
		assert.False(t, seen[typ], "duplicate event type: %s", typ)
		seen[typ] = true
	}
}

func TestGlobalSessionsChannel(t *testing.T) {
	assert.Equal(t, "sessions", GlobalSessionsChannel)
}
