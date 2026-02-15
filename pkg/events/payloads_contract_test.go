package events

import (
	"encoding/json"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionChannelPayloads_ContainSessionID is a contract test between the
// Go backend and the frontend WebSocket client.
//
// The frontend routes incoming WS events by inspecting `data.session_id` in
// the JSON payload (see websocket.ts handleEvent). ANY payload that is
// broadcast on a session-specific channel (session:{id}) MUST include a
// non-empty `session_id` field — otherwise the frontend silently drops it.
//
// All payload structs embed BasePayload which guarantees session_id is present.
// This test guards against:
//   - A new payload struct that forgets to embed BasePayload
//   - A call site that forgets to populate BasePayload.SessionID
func TestSessionChannelPayloads_ContainSessionID(t *testing.T) {
	const testSessionID = "sess-contract-test"

	// Every payload type that flows through SessionChannel(sessionID).
	// If you add a new payload that goes through a session channel,
	// add it here — the test will fail if session_id is missing.
	tests := []struct {
		name    string
		payload any
	}{
		{
			name: "TimelineCreatedPayload",
			payload: TimelineCreatedPayload{
				BasePayload: BasePayload{
					Type:      EventTypeTimelineCreated,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				EventID:        "evt-1",
				EventType:      timelineevent.EventTypeLlmThinking,
				Status:         timelineevent.StatusStreaming,
				Content:        "test",
				SequenceNumber: 1,
			},
		},
		{
			name: "TimelineCompletedPayload",
			payload: TimelineCompletedPayload{
				BasePayload: BasePayload{
					Type:      EventTypeTimelineCompleted,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				EventID:   "evt-1",
				EventType: timelineevent.EventTypeLlmThinking,
				Content:   "final content",
				Status:    timelineevent.StatusCompleted,
			},
		},
		{
			name: "StreamChunkPayload",
			payload: StreamChunkPayload{
				BasePayload: BasePayload{
					Type:      EventTypeStreamChunk,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				EventID: "evt-1",
				Delta:   "token",
			},
		},
		{
			name: "SessionStatusPayload",
			payload: SessionStatusPayload{
				BasePayload: BasePayload{
					Type:      EventTypeSessionStatus,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				Status: alertsession.StatusInProgress,
			},
		},
		{
			name: "StageStatusPayload",
			payload: StageStatusPayload{
				BasePayload: BasePayload{
					Type:      EventTypeStageStatus,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				StageID:    "stg-1",
				StageName:  "investigation",
				StageIndex: 1,
				Status:     StageStatusStarted,
			},
		},
		{
			name: "ChatCreatedPayload",
			payload: ChatCreatedPayload{
				BasePayload: BasePayload{
					Type:      EventTypeChatCreated,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				ChatID:    "chat-1",
				CreatedBy: "user",
			},
		},
		{
			name: "InteractionCreatedPayload",
			payload: InteractionCreatedPayload{
				BasePayload: BasePayload{
					Type:      EventTypeInteractionCreated,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				StageID:         "stg-1",
				ExecutionID:     "exec-1",
				InteractionID:   "int-1",
				InteractionType: InteractionTypeLLM,
			},
		},
		{
			name: "ExecutionProgressPayload",
			payload: ExecutionProgressPayload{
				BasePayload: BasePayload{
					Type:      EventTypeExecutionProgress,
					SessionID: testSessionID,
					Timestamp: "2026-01-01T00:00:00Z",
				},
				StageID:     "stg-1",
				ExecutionID: "exec-1",
				Phase:       ProgressPhaseInvestigating,
				Message:     "Iteration 1/5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.payload)
			require.NoError(t, err, "failed to marshal %s", tt.name)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(data, &parsed), "failed to unmarshal %s", tt.name)

			sid, ok := parsed["session_id"]
			assert.True(t, ok,
				"%s JSON is missing \"session_id\" field — frontend WS routing will silently drop this event", tt.name)
			assert.Equal(t, testSessionID, sid,
				"%s session_id has wrong value", tt.name)
		})
	}
}

// TestSessionProgressPayload_ContainsSessionID verifies the session.progress
// payload. Although this goes to GlobalSessionsChannel (not a session channel),
// it still carries session_id for the frontend to identify which session it
// belongs to.
func TestSessionProgressPayload_ContainsSessionID(t *testing.T) {
	payload := SessionProgressPayload{
		BasePayload: BasePayload{
			Type:      EventTypeSessionProgress,
			SessionID: "sess-progress",
			Timestamp: "2026-01-01T00:00:00Z",
		},
		CurrentStageName:  "investigation",
		CurrentStageIndex: 1,
		TotalStages:       3,
		ActiveExecutions:  1,
		StatusText:        "Starting stage: investigation",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	sid, ok := parsed["session_id"]
	assert.True(t, ok, "SessionProgressPayload is missing session_id")
	assert.Equal(t, "sess-progress", sid)
}
