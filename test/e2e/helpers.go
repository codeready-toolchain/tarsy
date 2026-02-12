package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
)

// ────────────────────────────────────────────────────────────
// HTTP Client Helpers
// ────────────────────────────────────────────────────────────

// SubmitAlert posts an alert and returns the parsed response.
func (app *TestApp) SubmitAlert(t *testing.T, alertType, data string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"alert_type": alertType,
		"data":       data,
	}
	return app.postJSON(t, "/api/v1/alerts", body, http.StatusAccepted)
}

// SubmitAlertWithMCP posts an alert with MCP selection override.
func (app *TestApp) SubmitAlertWithMCP(t *testing.T, alertType, data string, mcpSelection map[string]interface{}) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"alert_type":    alertType,
		"data":          data,
		"mcp_selection": mcpSelection,
	}
	return app.postJSON(t, "/api/v1/alerts", body, http.StatusAccepted)
}

// GetSession retrieves a session by ID.
func (app *TestApp) GetSession(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, fmt.Sprintf("/api/v1/sessions/%s", sessionID), http.StatusOK)
}

// CancelSession cancels a session.
func (app *TestApp) CancelSession(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.postJSON(t, fmt.Sprintf("/api/v1/sessions/%s/cancel", sessionID), nil, http.StatusOK)
}

// SendChatMessage sends a chat message.
func (app *TestApp) SendChatMessage(t *testing.T, sessionID, content string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"content": content,
	}
	return app.postJSON(t, fmt.Sprintf("/api/v1/sessions/%s/chat/messages", sessionID), body, http.StatusAccepted)
}

// GetHealth retrieves the health endpoint response.
func (app *TestApp) GetHealth(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/health", http.StatusOK)
}

func (app *TestApp) postJSON(t *testing.T, path string, body interface{}, expectedStatus int) map[string]interface{} {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, app.BaseURL+path, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, expectedStatus, resp.StatusCode, "POST %s: unexpected status", path)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

func (app *TestApp) getJSON(t *testing.T, path string, expectedStatus int) map[string]interface{} {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, app.BaseURL+path, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, expectedStatus, resp.StatusCode, "GET %s: unexpected status", path)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// ────────────────────────────────────────────────────────────
// Polling Helpers
// ────────────────────────────────────────────────────────────

// WaitForSessionStatus polls the DB until the session reaches the expected status.
func (app *TestApp) WaitForSessionStatus(t *testing.T, sessionID string, expected ...string) string {
	t.Helper()
	var actual string
	require.Eventually(t, func() bool {
		s, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
		if err != nil {
			return false
		}
		actual = string(s.Status)
		for _, exp := range expected {
			if actual == exp {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond,
		"session %s did not reach status %v (last: %s)", sessionID, expected, actual)
	return actual
}

// ────────────────────────────────────────────────────────────
// DB Query Helpers
// ────────────────────────────────────────────────────────────

// QueryTimeline returns all timeline events for a session, ordered by sequence.
func (app *TestApp) QueryTimeline(t *testing.T, sessionID string) []*ent.TimelineEvent {
	t.Helper()
	events, err := app.EntClient.TimelineEvent.Query().
		Where(timelineevent.SessionID(sessionID)).
		Order(ent.Asc(timelineevent.FieldSequenceNumber)).
		All(context.Background())
	require.NoError(t, err)
	return events
}

// QueryStages returns all stages for a session, ordered by index.
func (app *TestApp) QueryStages(t *testing.T, sessionID string) []*ent.Stage {
	t.Helper()
	stages, err := app.EntClient.Stage.Query().
		Where(stage.SessionID(sessionID)).
		Order(ent.Asc(stage.FieldStageIndex)).
		All(context.Background())
	require.NoError(t, err)
	return stages
}

// QueryExecutions returns all agent executions for a session.
func (app *TestApp) QueryExecutions(t *testing.T, sessionID string) []*ent.AgentExecution {
	t.Helper()
	execs, err := app.EntClient.AgentExecution.Query().
		Where(agentexecution.SessionID(sessionID)).
		Order(ent.Asc(agentexecution.FieldStartedAt)).
		All(context.Background())
	require.NoError(t, err)
	return execs
}

// QueryLLMInteractions returns all LLM interaction records for a session.
func (app *TestApp) QueryLLMInteractions(t *testing.T, sessionID string) []*ent.LLMInteraction {
	t.Helper()
	interactions, err := app.EntClient.LLMInteraction.Query().
		Where(llminteraction.SessionID(sessionID)).
		Order(ent.Asc(llminteraction.FieldCreatedAt)).
		All(context.Background())
	require.NoError(t, err)
	return interactions
}

// QueryMCPInteractions returns all MCP interaction records for a session.
func (app *TestApp) QueryMCPInteractions(t *testing.T, sessionID string) []*ent.MCPInteraction {
	t.Helper()
	interactions, err := app.EntClient.MCPInteraction.Query().
		Where(mcpinteraction.SessionID(sessionID)).
		Order(ent.Asc(mcpinteraction.FieldCreatedAt)).
		All(context.Background())
	require.NoError(t, err)
	return interactions
}

// ────────────────────────────────────────────────────────────
// WebSocket Event Projection and Filtering
// ────────────────────────────────────────────────────────────

// ProjectForGolden extracts only the key fields from a WS event for golden comparison.
func ProjectForGolden(event WSEvent) map[string]interface{} {
	projected := map[string]interface{}{"type": event.Type}
	switch event.Type {
	case "session.status":
		projected["status"] = event.Parsed["status"]
	case "stage.status":
		projected["stage_name"] = event.Parsed["stage_name"]
		projected["stage_index"] = event.Parsed["stage_index"]
		projected["status"] = event.Parsed["status"]
	case "timeline_event.created":
		projected["event_type"] = event.Parsed["event_type"]
		projected["status"] = event.Parsed["status"]
	case "timeline_event.completed":
		projected["event_type"] = event.Parsed["event_type"]
		projected["status"] = event.Parsed["status"]
		if content, ok := event.Parsed["content"]; ok {
			projected["content"] = content
		}
	case "chat.created":
		projected["chat_id"] = event.Parsed["chat_id"]
	case "chat.user_message":
		projected["content"] = event.Parsed["content"]
	}
	return projected
}

// FilterEventsForGolden filters, collapses, and projects WS events for golden comparison.
func FilterEventsForGolden(events []WSEvent) []map[string]interface{} {
	var filtered []map[string]interface{}
	lastWasChunk := false
	for _, e := range events {
		switch e.Type {
		case "stream.chunk":
			if !lastWasChunk {
				filtered = append(filtered, map[string]interface{}{"type": "stream.chunk"})
				lastWasChunk = true
			}
		case "connection.established", "subscription.confirmed", "pong", "catchup.overflow":
			continue
		default:
			filtered = append(filtered, ProjectForGolden(e))
			lastWasChunk = false
		}
	}
	return filtered
}
