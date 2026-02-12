package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
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
// DB Record Projection for Golden Comparison
// ────────────────────────────────────────────────────────────

// ProjectStageForGolden extracts key fields from a stage record for golden comparison.
func ProjectStageForGolden(s *ent.Stage) map[string]interface{} {
	m := map[string]interface{}{
		"stage_name":  s.StageName,
		"stage_index": s.StageIndex,
		"status":      string(s.Status),
	}
	if s.ErrorMessage != nil {
		m["error_message"] = *s.ErrorMessage
	}
	return m
}

// ProjectTimelineForGolden extracts key fields from a timeline event for golden comparison.
func ProjectTimelineForGolden(te *ent.TimelineEvent) map[string]interface{} {
	m := map[string]interface{}{
		"event_type": string(te.EventType),
		"status":     string(te.Status),
		"sequence":   te.SequenceNumber,
	}
	if te.Content != "" {
		m["content"] = te.Content
	}
	return m
}

// BuildAgentNameIndex creates a map from execution_id → agent_name for
// annotating timeline projections with the agent that produced each event.
func BuildAgentNameIndex(execs []*ent.AgentExecution) map[string]string {
	idx := make(map[string]string, len(execs))
	for _, e := range execs {
		idx[e.ID] = e.AgentName
	}
	return idx
}

// AnnotateTimelineWithAgent adds "agent" field to projected timeline maps
// by looking up execution_id → agent_name. Session-level events (nil execution_id)
// are left without an agent field.
func AnnotateTimelineWithAgent(projected []map[string]interface{}, timeline []*ent.TimelineEvent, agentIndex map[string]string) {
	for i, te := range timeline {
		if te.ExecutionID != nil {
			if name, ok := agentIndex[*te.ExecutionID]; ok {
				projected[i]["agent"] = name
			}
		}
	}
}

// SortTimelineProjection sorts projected timeline maps deterministically.
// Primary sort: agent name (groups events by agent). Then sequence, event_type, content.
// Session-level events (no agent) sort last.
func SortTimelineProjection(projected []map[string]interface{}) {
	sort.SliceStable(projected, func(i, j int) bool {
		agI, _ := projected[i]["agent"].(string)
		agJ, _ := projected[j]["agent"].(string)
		if agI != agJ {
			// Empty agent (session-level) sorts last.
			if agI == "" {
				return false
			}
			if agJ == "" {
				return true
			}
			return agI < agJ
		}
		seqI, _ := projected[i]["sequence"].(int)
		seqJ, _ := projected[j]["sequence"].(int)
		if seqI != seqJ {
			return seqI < seqJ
		}
		etI, _ := projected[i]["event_type"].(string)
		etJ, _ := projected[j]["event_type"].(string)
		if etI != etJ {
			return etI < etJ
		}
		cI, _ := projected[i]["content"].(string)
		cJ, _ := projected[j]["content"].(string)
		return cI < cJ
	})
}

// ────────────────────────────────────────────────────────────
// WebSocket Structural Assertions
// ────────────────────────────────────────────────────────────

// AssertEventsInOrder verifies that each expected event appears in the actual
// WS events in the correct relative order. Extra and duplicate actual events
// are tolerated — only the expected sequence must be found in order.
//
// Infra events (connection.established, subscription.confirmed, pong,
// catchup.overflow) are filtered out before matching.
func AssertEventsInOrder(t *testing.T, actual []WSEvent, expected []testdata.ExpectedEvent) {
	t.Helper()

	// Filter out infra events.
	var filtered []WSEvent
	for _, e := range actual {
		switch e.Type {
		case "connection.established", "subscription.confirmed", "pong", "catchup.overflow":
			continue
		default:
			filtered = append(filtered, e)
		}
	}

	expectedIdx := 0
	actualIdx := 0
	for expectedIdx < len(expected) && actualIdx < len(filtered) {
		exp := expected[expectedIdx]

		// If this expected event is part of an unordered group, collect all
		// group members and match them as a set against upcoming actual events.
		if exp.Group != 0 {
			groupID := exp.Group
			var groupExpected []testdata.ExpectedEvent
			for expectedIdx < len(expected) && expected[expectedIdx].Group == groupID {
				groupExpected = append(groupExpected, expected[expectedIdx])
				expectedIdx++
			}
			// Try to match all group members against actual events (any order).
			matched := make([]bool, len(groupExpected))
			for actualIdx < len(filtered) {
				allMatched := true
				for i := range matched {
					if !matched[i] {
						allMatched = false
						break
					}
				}
				if allMatched {
					break
				}
				foundAny := false
				for i, ge := range groupExpected {
					if !matched[i] && matchesExpected(filtered[actualIdx], ge) {
						matched[i] = true
						foundAny = true
						break
					}
				}
				if foundAny {
					actualIdx++
				} else {
					// Current actual event doesn't match any unmatched group member.
					// Keep scanning — there may be extra events (stream.chunk, duplicates).
					actualIdx++
				}
			}
			// Check all group members were matched.
			for i, m := range matched {
				if !m {
					assert.Failf(t, "unordered group member not found",
						"group %d: missing %s", groupID, formatExpected(groupExpected[i]))
				}
			}
			continue
		}

		// Sequential matching (Group == 0).
		if matchesExpected(filtered[actualIdx], exp) {
			expectedIdx++
		}
		actualIdx++
	}

	if !assert.Equal(t, len(expected), expectedIdx,
		"not all expected WS events found in order (matched %d/%d)", expectedIdx, len(expected)) {
		// Build a readable summary of what was expected vs what we got.
		var sb strings.Builder
		sb.WriteString("Expected events (unmatched from index ")
		sb.WriteString(fmt.Sprintf("%d):\n", expectedIdx))
		for i := expectedIdx; i < len(expected); i++ {
			sb.WriteString(fmt.Sprintf("  [%d] %s", i, formatExpected(expected[i])))
			sb.WriteString("\n")
		}
		sb.WriteString("Actual events received:\n")
		for i, e := range filtered {
			sb.WriteString(fmt.Sprintf("  [%d] type=%s", i, e.Type))
			if s, ok := e.Parsed["status"]; ok {
				sb.WriteString(fmt.Sprintf(" status=%v", s))
			}
			if sn, ok := e.Parsed["stage_name"]; ok {
				sb.WriteString(fmt.Sprintf(" stage_name=%v", sn))
			}
			if et, ok := e.Parsed["event_type"]; ok {
				sb.WriteString(fmt.Sprintf(" event_type=%v", et))
			}
			sb.WriteString("\n")
		}
		t.Log(sb.String())
	}
}

// matchesExpected checks if a WS event matches an expected event spec.
// Only non-empty fields in the expected spec are checked.
func matchesExpected(actual WSEvent, expected testdata.ExpectedEvent) bool {
	if actual.Type != expected.Type {
		return false
	}
	if expected.Status != "" {
		if s, _ := actual.Parsed["status"].(string); s != expected.Status {
			return false
		}
	}
	if expected.StageName != "" {
		if sn, _ := actual.Parsed["stage_name"].(string); sn != expected.StageName {
			return false
		}
	}
	if expected.EventType != "" {
		if et, _ := actual.Parsed["event_type"].(string); et != expected.EventType {
			return false
		}
	}
	if expected.Content != "" {
		if c, _ := actual.Parsed["content"].(string); c != expected.Content {
			return false
		}
	}
	if len(expected.Metadata) > 0 {
		meta, _ := actual.Parsed["metadata"].(map[string]interface{})
		for k, v := range expected.Metadata {
			if av, _ := meta[k].(string); av != v {
				return false
			}
		}
	}
	return true
}

// formatExpected returns a readable string for an expected event.
func formatExpected(e testdata.ExpectedEvent) string {
	s := "type=" + e.Type
	if e.Status != "" {
		s += " status=" + e.Status
	}
	if e.StageName != "" {
		s += " stage_name=" + e.StageName
	}
	if e.EventType != "" {
		s += " event_type=" + e.EventType
	}
	if e.Content != "" {
		c := e.Content
		if len(c) > 60 {
			c = c[:57] + "..."
		}
		s += fmt.Sprintf(" content=%q", c)
	}
	for k, v := range e.Metadata {
		s += fmt.Sprintf(" meta.%s=%q", k, v)
	}
	return s
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
		projected["status"] = event.Parsed["status"]
		if et, ok := event.Parsed["event_type"]; ok && et != nil {
			projected["event_type"] = et
		}
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

// FilterEventsForGolden filters and projects WS events for golden comparison.
// stream.chunk events are excluded entirely because their presence is
// timing-sensitive (depends on how fast events are delivered vs consumed).
func FilterEventsForGolden(events []WSEvent) []map[string]interface{} {
	var filtered []map[string]interface{}
	for _, e := range events {
		switch e.Type {
		case "stream.chunk",
			"connection.established", "subscription.confirmed", "pong", "catchup.overflow":
			continue
		default:
			filtered = append(filtered, ProjectForGolden(e))
		}
	}
	return filtered
}
