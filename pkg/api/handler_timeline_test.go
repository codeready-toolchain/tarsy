package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
)

func TestGetTimelineHandler_ServiceNotConfigured(t *testing.T) {
	s := &Server{} // timelineService is nil
	e := timelineTestEcho(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/any-id/timeline", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestGetTimelineHandler_EmptyTimeline(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineSvc := services.NewTimelineService(client.Client)

	session := createTimelineTestSession(t, client.Client)

	s := &Server{timelineService: timelineSvc}
	e := timelineTestEcho(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+session.ID+"/timeline", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var events []*ent.TimelineEvent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	assert.Empty(t, events)
}

func TestGetTimelineHandler_WithEvents(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineSvc := services.NewTimelineService(client.Client)

	session := createTimelineTestSession(t, client.Client)
	stageID, execID := createTimelineTestStageAndExecution(t, client.Client, session.ID)

	// Insert events out of order to verify ordering by sequence_number.
	_, err := timelineSvc.CreateTimelineEvent(context.Background(), models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &stageID,
		ExecutionID:    &execID,
		SequenceNumber: 2,
		EventType:      timelineevent.EventTypeLlmResponse,
		Status:         timelineevent.StatusCompleted,
		Content:        "I'll check the pods.",
	})
	require.NoError(t, err)

	_, err = timelineSvc.CreateTimelineEvent(context.Background(), models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &stageID,
		ExecutionID:    &execID,
		SequenceNumber: 1,
		EventType:      timelineevent.EventTypeLlmThinking,
		Status:         timelineevent.StatusCompleted,
		Content:        "Let me investigate.",
	})
	require.NoError(t, err)

	_, err = timelineSvc.CreateTimelineEvent(context.Background(), models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &stageID,
		ExecutionID:    &execID,
		SequenceNumber: 3,
		EventType:      timelineevent.EventTypeLlmToolCall,
		Status:         timelineevent.StatusCompleted,
		Content:        "get_pods",
		Metadata:       map[string]any{"tool_name": "get_pods"},
	})
	require.NoError(t, err)

	s := &Server{timelineService: timelineSvc}
	e := timelineTestEcho(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+session.ID+"/timeline", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var events []*ent.TimelineEvent
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	require.Len(t, events, 3)

	// Verify ordering by sequence_number.
	assert.Equal(t, 1, events[0].SequenceNumber)
	assert.Equal(t, timelineevent.EventTypeLlmThinking, events[0].EventType)
	assert.Equal(t, "Let me investigate.", events[0].Content)

	assert.Equal(t, 2, events[1].SequenceNumber)
	assert.Equal(t, timelineevent.EventTypeLlmResponse, events[1].EventType)
	assert.Equal(t, "I'll check the pods.", events[1].Content)

	assert.Equal(t, 3, events[2].SequenceNumber)
	assert.Equal(t, timelineevent.EventTypeLlmToolCall, events[2].EventType)
	assert.Equal(t, "get_pods", events[2].Content)
	assert.Equal(t, "get_pods", events[2].Metadata["tool_name"])
}

// ── Helpers ──────────────────────────────────────────────────

// timelineTestEcho creates a minimal echo instance with the timeline route registered.
func timelineTestEcho(s *Server) *echo.Echo {
	e := echo.New()
	e.GET("/api/v1/sessions/:id/timeline", s.getTimelineHandler)
	return e
}

func createTimelineTestSession(t *testing.T, client *ent.Client) *ent.AlertSession {
	t.Helper()
	session, err := client.AlertSession.Create().
		SetID("tl-sess-" + t.Name()).
		SetAlertData("test alert data").
		SetAgentType("test-agent").
		SetAlertType("test-type").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusPending).
		SetAuthor("test").
		Save(context.Background())
	require.NoError(t, err)
	return session
}

func createTimelineTestStageAndExecution(t *testing.T, client *ent.Client, sessionID string) (stageID, execID string) {
	t.Helper()
	stg, err := client.Stage.Create().
		SetID("tl-stage-" + t.Name()).
		SetSessionID(sessionID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		Save(context.Background())
	require.NoError(t, err)

	exec, err := client.AgentExecution.Create().
		SetID("tl-exec-" + t.Name()).
		SetSessionID(sessionID).
		SetStageID(stg.ID).
		SetAgentName("DataCollector").
		SetAgentIndex(1).
		SetLlmBackend("google-native").
		SetStatus(agentexecution.StatusCompleted).
		Save(context.Background())
	require.NoError(t, err)

	return stg.ID, exec.ID
}
