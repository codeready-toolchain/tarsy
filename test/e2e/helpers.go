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
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
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

// SubmitAlertWithRunbook posts an alert with a runbook URL and returns the parsed response.
func (app *TestApp) SubmitAlertWithRunbook(t *testing.T, alertType, data, runbookURL string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"alert_type": alertType,
		"data":       data,
		"runbook":    runbookURL,
	}
	return app.postJSON(t, "/api/v1/alerts", body, http.StatusAccepted)
}

// SubmitAlertWithFingerprint posts an alert with a Slack message fingerprint and returns the parsed response.
func (app *TestApp) SubmitAlertWithFingerprint(t *testing.T, alertType, data, fingerprint string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"alert_type":                alertType,
		"data":                      data,
		"slack_message_fingerprint": fingerprint,
	}
	return app.postJSON(t, "/api/v1/alerts", body, http.StatusAccepted)
}

// GetRunbooks calls GET /api/v1/runbooks and returns the parsed JSON array.
func (app *TestApp) GetRunbooks(t *testing.T) []interface{} {
	t.Helper()
	return app.getJSONArray(t, "/api/v1/runbooks", http.StatusOK)
}

// GetSession retrieves a session by ID.
func (app *TestApp) GetSession(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, fmt.Sprintf("/api/v1/sessions/%s", sessionID), http.StatusOK)
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, expectedStatus, resp.StatusCode, "GET %s: unexpected status", path)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// GetTimeline calls GET /api/v1/sessions/:id/timeline.
// Returns the parsed JSON array of timeline events.
func (app *TestApp) GetTimeline(t *testing.T, sessionID string) []interface{} {
	t.Helper()
	return app.getJSONArray(t, "/api/v1/sessions/"+sessionID+"/timeline", http.StatusOK)
}

func (app *TestApp) getJSONArray(t *testing.T, path string, expectedStatus int) []interface{} {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, app.BaseURL+path, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, expectedStatus, resp.StatusCode, "GET %s: unexpected status", path)
	var result []interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// ────────────────────────────────────────────────────────────
// Dashboard API Helpers
// ────────────────────────────────────────────────────────────

// GetSessionList calls GET /api/v1/sessions with optional query params.
func (app *TestApp) GetSessionList(t *testing.T, queryParams string) map[string]interface{} {
	t.Helper()
	path := "/api/v1/sessions"
	if queryParams != "" {
		path += "?" + queryParams
	}
	return app.getJSON(t, path, http.StatusOK)
}

// GetActiveSessions calls GET /api/v1/sessions/active.
func (app *TestApp) GetActiveSessions(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/active", http.StatusOK)
}

// GetSessionSummary calls GET /api/v1/sessions/:id/summary.
func (app *TestApp) GetSessionSummary(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/"+sessionID+"/summary", http.StatusOK)
}

// GetSessionStatus calls GET /api/v1/sessions/:id/status.
func (app *TestApp) GetSessionStatus(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/"+sessionID+"/status", http.StatusOK)
}

// GetFilterOptions calls GET /api/v1/sessions/filter-options.
func (app *TestApp) GetFilterOptions(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/filter-options", http.StatusOK)
}

// GetSystemWarnings calls GET /api/v1/system/warnings.
func (app *TestApp) GetSystemWarnings(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/system/warnings", http.StatusOK)
}

// GetMCPServers calls GET /api/v1/system/mcp-servers.
func (app *TestApp) GetMCPServers(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/system/mcp-servers", http.StatusOK)
}

// GetDefaultTools calls GET /api/v1/system/default-tools.
func (app *TestApp) GetDefaultTools(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/system/default-tools", http.StatusOK)
}

// GetAlertTypes calls GET /api/v1/alert-types.
func (app *TestApp) GetAlertTypes(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/alert-types", http.StatusOK)
}

// GetHealth calls GET /health.
func (app *TestApp) GetHealth(t *testing.T) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/health", http.StatusOK)
}

// ────────────────────────────────────────────────────────────
// Trace API Helpers
// ────────────────────────────────────────────────────────────

// GetTraceList calls GET /api/v1/sessions/:id/trace.
func (app *TestApp) GetTraceList(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/"+sessionID+"/trace", http.StatusOK)
}

// GetLLMInteractionDetail calls GET /api/v1/sessions/:id/trace/llm/:interaction_id.
func (app *TestApp) GetLLMInteractionDetail(t *testing.T, sessionID, interactionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/"+sessionID+"/trace/llm/"+interactionID, http.StatusOK)
}

// GetMCPInteractionDetail calls GET /api/v1/sessions/:id/trace/mcp/:interaction_id.
func (app *TestApp) GetMCPInteractionDetail(t *testing.T, sessionID, interactionID string) map[string]interface{} {
	t.Helper()
	return app.getJSON(t, "/api/v1/sessions/"+sessionID+"/trace/mcp/"+interactionID, http.StatusOK)
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
// Chat Helpers
// ────────────────────────────────────────────────────────────

// SendChatMessage sends a POST /api/v1/sessions/:id/chat/messages.
// Returns the response map with chat_id, message_id, stage_id.
func (app *TestApp) SendChatMessage(t *testing.T, sessionID, content string) map[string]interface{} {
	t.Helper()
	return app.postJSON(t, "/api/v1/sessions/"+sessionID+"/chat/messages",
		map[string]string{"content": content}, http.StatusAccepted)
}

// CancelSession sends POST /api/v1/sessions/:id/cancel.
func (app *TestApp) CancelSession(t *testing.T, sessionID string) map[string]interface{} {
	t.Helper()
	return app.postJSON(t, "/api/v1/sessions/"+sessionID+"/cancel", nil, http.StatusOK)
}

// WaitForStageStatus polls the DB until the stage reaches a terminal status.
// Returns the terminal status string.
func (app *TestApp) WaitForStageStatus(t *testing.T, stageID string, expected ...string) string {
	t.Helper()
	var actual string
	require.Eventually(t, func() bool {
		s, err := app.EntClient.Stage.Get(context.Background(), stageID)
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
		"stage %s did not reach status %v (last: %s)", stageID, expected, actual)
	return actual
}

// WaitForActiveStage polls the DB until a stage with "active" status exists
// for the given session and returns it. Useful for cancellation tests where you
// need to wait until execution has started before cancelling.
func (app *TestApp) WaitForActiveStage(t *testing.T, sessionID string) *ent.Stage {
	t.Helper()
	var found *ent.Stage
	require.Eventually(t, func() bool {
		stages, err := app.EntClient.Stage.Query().
			Where(stage.SessionID(sessionID), stage.StatusEQ(stage.StatusActive)).
			All(context.Background())
		if err != nil || len(stages) == 0 {
			return false
		}
		found = stages[0]
		return true
	}, 30*time.Second, 100*time.Millisecond,
		"no active stage found for session %s", sessionID)
	return found
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

// QuerySessionsByStatus returns session IDs matching the given status.
func (app *TestApp) QuerySessionsByStatus(t *testing.T, status string) []string {
	t.Helper()
	sessions, err := app.EntClient.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.Status(status))).
		All(context.Background())
	require.NoError(t, err)
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	return ids
}

// WaitForNSessionsInStatus waits until exactly n sessions have the given status.
// It inlines the DB query (instead of calling QuerySessionsByStatus) so that
// transient DB errors cause a retry rather than aborting the test via require.NoError.
func (app *TestApp) WaitForNSessionsInStatus(t *testing.T, n int, status string) {
	t.Helper()
	var lastCount int
	require.Eventually(t, func() bool {
		sessions, err := app.EntClient.AlertSession.Query().
			Where(alertsession.StatusEQ(alertsession.Status(status))).
			All(context.Background())
		if err != nil {
			return false // transient error — let Eventually retry
		}
		lastCount = len(sessions)
		return lastCount == n
	}, 30*time.Second, 100*time.Millisecond,
		"expected %d sessions in status %q, last saw %d", n, status, lastCount)
}

// ────────────────────────────────────────────────────────────
// Goroutine-safe DB polling (no t.FailNow — safe from non-test goroutines)
// ────────────────────────────────────────────────────────────

// CountLLMInteractions returns the current LLM interaction count for a session.
func (app *TestApp) CountLLMInteractions(sessionID string) (int, error) {
	return app.EntClient.LLMInteraction.Query().
		Where(llminteraction.SessionID(sessionID)).
		Count(context.Background())
}

// AwaitLLMInteractionIncrease polls until the LLM interaction count exceeds
// the given baseline, indicating the orchestrator has recorded a new response.
// Returns true on success, false on timeout (30s). The test's own timeout via
// WaitForSessionStatus is the primary failsafe for goroutine callers.
func (app *TestApp) AwaitLLMInteractionIncrease(sessionID string, baseline int) bool {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			n, err := app.EntClient.LLMInteraction.Query().
				Where(llminteraction.SessionID(sessionID)).
				Count(context.Background())
			if err == nil && n > baseline {
				return true
			}
		}
	}
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
	if len(te.Metadata) > 0 {
		m["metadata"] = te.Metadata
	}
	return m
}

// toInt converts a JSON-decoded numeric value (typically float64) to int.
// Returns 0 if the value is nil or not a recognized numeric type.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// ProjectAPITimelineForGolden extracts the same key fields from a JSON-parsed
// timeline event (from the API response) as ProjectTimelineForGolden does from
// an ent object. This enables golden comparison of API and DB results.
func ProjectAPITimelineForGolden(event map[string]interface{}) map[string]interface{} {
	m := map[string]interface{}{
		"event_type": event["event_type"],
		"status":     event["status"],
		"sequence":   toInt(event["sequence_number"]),
	}
	if c, ok := event["content"].(string); ok && c != "" {
		m["content"] = c
	}
	if meta, ok := event["metadata"].(map[string]interface{}); ok && len(meta) > 0 {
		m["metadata"] = meta
	}
	return m
}

// AnnotateAPITimelineWithAgent adds "agent" field to projected API timeline maps
// by looking up execution_id → agent_name. Session-level events (no execution_id)
// are left without an agent field.
func AnnotateAPITimelineWithAgent(projected []map[string]interface{}, apiEvents []interface{}, agentIndex map[string]string) {
	for i, raw := range apiEvents {
		event, _ := raw.(map[string]interface{})
		if execID, ok := event["execution_id"].(string); ok {
			if name, found := agentIndex[execID]; found {
				projected[i]["agent"] = name
			}
		}
	}
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

// AssertAllEventsHaveSessionID verifies that every non-infra WS event carries
// the correct session_id. This is a contract check: the frontend routes events
// by data.session_id, so any event missing it would be silently dropped.
func AssertAllEventsHaveSessionID(t *testing.T, actual []WSEvent, expectedSessionID string) {
	t.Helper()
	for i, e := range actual {
		switch e.Type {
		case "connection.established", "subscription.confirmed", "pong", "catchup.overflow":
			continue
		}
		sid, _ := e.Parsed["session_id"].(string)
		assert.Equalf(t, expectedSessionID, sid,
			"WS event %d (type=%s) has wrong or missing session_id", i, e.Type)
	}
}

// AssertEventsInOrder verifies that each expected event appears in the actual
// WS events in the correct relative order. Extra and duplicate actual events
// are tolerated — only the expected sequence must be found in order.
//
// Infra events (connection.established, subscription.confirmed, pong,
// catchup.overflow) are filtered out before matching.
func AssertEventsInOrder(t *testing.T, actual []WSEvent, expected []testdata.ExpectedEvent) {
	t.Helper()

	// Deduplicate and sort persistent events by db_event_id to eliminate
	// the NOTIFY/catchup race. When the WS client subscribes during session
	// processing, it may receive some events via NOTIFY (real-time) and the
	// same events again via catchup (replay). Without dedup+sort, NOTIFY
	// events can appear before their natural DB order, causing the
	// forward-only matching algorithm to consume them during earlier
	// sequential matches and miss them for later group matches.
	//
	// Strategy: collect only persistent events (those with db_event_id),
	// deduplicate, and sort by db_event_id. Transient events (stream.chunk)
	// are excluded since no expected events match them.
	seen := make(map[float64]bool)
	var filtered []WSEvent
	for _, e := range actual {
		switch e.Type {
		case "connection.established", "subscription.confirmed", "pong", "catchup.overflow":
			continue
		}
		dbID, hasID := e.Parsed["db_event_id"].(float64)
		if !hasID {
			continue // Skip transient events (stream.chunk) — not in expected list
		}
		if seen[dbID] {
			continue // Skip duplicate (same event from NOTIFY + catchup)
		}
		seen[dbID] = true
		filtered = append(filtered, e)
	}
	sort.Slice(filtered, func(i, j int) bool {
		idI, _ := filtered[i].Parsed["db_event_id"].(float64)
		idJ, _ := filtered[j].Parsed["db_event_id"].(float64)
		return idI < idJ
	})

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
				// Two-pass matching: try expected events WITH metadata first,
				// then those without. This prevents a less-specific expected
				// event from greedily matching an actual event that should
				// satisfy a more-specific (metadata-requiring) expected event.
				for pass := 0; pass < 2 && !foundAny; pass++ {
					for i, ge := range groupExpected {
						hasMetadata := len(ge.Metadata) > 0
						if (pass == 0) != hasMetadata {
							continue // pass 0 = metadata-requiring only, pass 1 = rest
						}
						if !matched[i] && matchesExpected(filtered[actualIdx], ge) {
							matched[i] = true
							foundAny = true
							break
						}
					}
				}
				// Advance past this actual event whether it matched a group member or not.
				actualIdx++
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
			av, ok := meta[k]
			if !ok {
				return false
			}
			// Compare as strings to handle bool/numeric metadata values
			// (e.g. forced_conclusion: true → "true", iterations_used: 1 → "1").
			if fmt.Sprintf("%v", av) != v {
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
