package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
)

func TestListSessionsHandler_Validation(t *testing.T) {
	// We only test parameter validation (returns 400 before hitting the service).
	// Happy-path is covered by integration/e2e tests that have a real service.
	s := &Server{}

	tests := []struct {
		name    string
		query   string
		wantErr int
		errMsg  string
		exact   bool
	}{
		{
			name:    "invalid sort_by",
			query:   "sort_by=unknown_field",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid sort_by",
		},
		{
			name:    "invalid sort_order",
			query:   "sort_order=random",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid sort_order",
		},
		{
			name:    "invalid status value",
			query:   "status=bogus",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid status",
		},
		{
			name:    "search too short",
			query:   "search=ab",
			wantErr: http.StatusBadRequest,
			errMsg:  "search query must be at least 3 characters",
		},
		{
			name:    "invalid start_date",
			query:   "start_date=not-a-date",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid start_date",
		},
		{
			name:    "end_date wrong format (not RFC3339)",
			query:   "end_date=2024-01-01",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid end_date",
		},
		{
			name:    "invalid review_status",
			query:   "review_status=bogus",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid review_status",
		},
		{
			name:    "invalid quality_rating",
			query:   "quality_rating=invalid",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid quality_rating",
		},
		{
			name:    "invalid label starts with digit",
			query:   "label=1bad",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid label: must match [A-Za-z][A-Za-z0-9_-]*",
			exact:   true,
		},
		{
			name:    "invalid label starts with hyphen",
			query:   "label=-page",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid label: must match [A-Za-z][A-Za-z0-9_-]*",
			exact:   true,
		},
		{
			name:    "comma-separated labels with one invalid",
			query:   "label=page,1bad",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid label: must match [A-Za-z][A-Za-z0-9_-]*",
			exact:   true,
		},
		{
			name:    "empty label token in comma list",
			query:   "label=page,",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid label: must match [A-Za-z][A-Za-z0-9_-]*",
			exact:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?"+tt.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := s.listSessionsHandler(c)
			if assert.Error(t, err) {
				he, ok := err.(*echo.HTTPError)
				if assert.True(t, ok, "expected echo.HTTPError") {
					assert.Equal(t, tt.wantErr, he.Code)
					if tt.exact {
						assert.Equal(t, tt.errMsg, he.Message)
					} else {
						assert.Contains(t, he.Message, tt.errMsg)
					}
				}
			}
		})
	}

	t.Run("valid sort_by values pass validation", func(t *testing.T) {
		validValues := []string{"created_at", "status", "alert_type", "author", "duration", "score", "quality_rating"}
		for _, v := range validValues {
			t.Run(v, func(t *testing.T) {
				e := echo.New()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?sort_by="+v, nil)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				err := func() (retErr error) {
					defer func() { recover() }()
					return s.listSessionsHandler(c)
				}()

				if err != nil {
					he, ok := err.(*echo.HTTPError)
					if ok {
						assert.NotContains(t, he.Message, "invalid sort_by",
							"sort_by=%s should be accepted", v)
					}
				}
			})
		}
	})

	t.Run("valid label values pass validation", func(t *testing.T) {
		validValues := []string{"page", "false_positive", "Watch", "page,watch"}
		for _, v := range validValues {
			t.Run(v, func(t *testing.T) {
				e := echo.New()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?label="+v, nil)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				err := func() (retErr error) {
					defer func() { recover() }()
					return s.listSessionsHandler(c)
				}()

				if err != nil {
					he, ok := err.(*echo.HTTPError)
					if ok {
						assert.NotContains(t, he.Message, "invalid label",
							"label=%s should be accepted", v)
					}
				}
			})
		}
	})

	t.Run("comma-separated statuses with one invalid", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=completed,bogus", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.listSessionsHandler(c)
		if assert.Error(t, err) {
			he, ok := err.(*echo.HTTPError)
			if assert.True(t, ok) {
				assert.Equal(t, http.StatusBadRequest, he.Code)
				assert.Contains(t, he.Message, "invalid status: bogus")
			}
		}
	})
}

func TestSessionStatusHandler_Validation(t *testing.T) {
	s := &Server{}

	t.Run("missing session id returns 400", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions//status", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.sessionStatusHandler(c)
		if assert.Error(t, err) {
			he, ok := err.(*echo.HTTPError)
			if assert.True(t, ok, "expected echo.HTTPError") {
				assert.Equal(t, http.StatusBadRequest, he.Code)
				assert.Contains(t, he.Message, "session id")
			}
		}
	})
}

type cancelTestPublisher struct {
	sessionStatus []events.SessionStatusPayload
	reviewStatus  []events.ReviewStatusPayload
}

func (p *cancelTestPublisher) PublishTimelineCreated(context.Context, string, events.TimelineCreatedPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishTimelineCompleted(context.Context, string, events.TimelineCompletedPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishStreamChunk(context.Context, string, events.StreamChunkPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishSessionStatus(_ context.Context, _ string, payload events.SessionStatusPayload) error {
	p.sessionStatus = append(p.sessionStatus, payload)
	return nil
}
func (p *cancelTestPublisher) PublishStageStatus(context.Context, string, events.StageStatusPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishChatCreated(context.Context, string, events.ChatCreatedPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishInteractionCreated(context.Context, string, events.InteractionCreatedPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishSessionProgress(context.Context, events.SessionProgressPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishExecutionProgress(context.Context, string, events.ExecutionProgressPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishExecutionStatus(context.Context, string, events.ExecutionStatusPayload) error {
	return nil
}
func (p *cancelTestPublisher) PublishReviewStatus(_ context.Context, _ string, payload events.ReviewStatusPayload) error {
	p.reviewStatus = append(p.reviewStatus, payload)
	return nil
}
func (p *cancelTestPublisher) PublishSessionScoreUpdated(context.Context, string, events.SessionScoreUpdatedPayload) error {
	return nil
}

func TestCancelSessionHandler_Validation(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.POST("/api/v1/sessions/:id/cancel", s.cancelSessionHandler)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/cancel",
			strings.NewReader("{bad"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("over cap reason", func(t *testing.T) {
		body := `{"reason":"` + strings.Repeat("a", services.MaxCancelReasonLength+1) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/cancel",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "must not exceed 500 characters")
	})
}

func TestCancelSessionHandler_PendingAndInProgress(t *testing.T) {
	client := testdb.NewTestClient(t)
	sessionService := newUsageTestSessionService(client.Client)
	pub := &cancelTestPublisher{}
	s := &Server{
		sessionService: sessionService,
		eventPublisher: pub,
	}
	e := echo.New()
	e.POST("/api/v1/sessions/:id/cancel", s.cancelSessionHandler)
	ctx := t.Context()

	createPending := func(t *testing.T) string {
		t.Helper()
		sess, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)
		return sess.ID
	}

	t.Run("empty body cancels pending as api-client", func(t *testing.T) {
		id := createPending(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCancelled, sess.Status)
		require.NotNil(t, sess.CancelledBy)
		assert.Equal(t, "api-client", *sess.CancelledBy)
		assert.Nil(t, sess.CancelReason)
	})

	t.Run("empty json object omits reason", func(t *testing.T) {
		id := createPending(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel",
			strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Nil(t, sess.CancelReason)
	})

	t.Run("trims whitespace-only reason to omitted", func(t *testing.T) {
		id := createPending(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel",
			strings.NewReader(`{"reason":"   "}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Nil(t, sess.CancelReason)
	})

	t.Run("extracts author and reason and publishes events", func(t *testing.T) {
		id := createPending(t)
		pub.sessionStatus = nil
		pub.reviewStatus = nil

		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel",
			strings.NewReader(`{"reason":"duplicate of session abc"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-User", "alice@example.com")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCancelled, sess.Status)
		require.NotNil(t, sess.CancelledBy)
		assert.Equal(t, "alice@example.com", *sess.CancelledBy)
		require.NotNil(t, sess.CancelReason)
		assert.Equal(t, "duplicate of session abc", *sess.CancelReason)
		require.NotNil(t, sess.ReviewStatus)
		assert.Equal(t, alertsession.ReviewStatusReviewed, *sess.ReviewStatus)

		require.Len(t, pub.sessionStatus, 1)
		assert.Equal(t, events.EventTypeSessionStatus, pub.sessionStatus[0].Type)
		assert.Equal(t, alertsession.StatusCancelled, pub.sessionStatus[0].Status)
		require.Len(t, pub.reviewStatus, 1)
		assert.Equal(t, events.EventTypeReviewStatus, pub.reviewStatus[0].Type)
		assert.Equal(t, "system", pub.reviewStatus[0].Actor)
		require.NotNil(t, pub.reviewStatus[0].ReviewStatus)
		assert.Equal(t, string(alertsession.ReviewStatusReviewed), *pub.reviewStatus[0].ReviewStatus)
	})

	t.Run("in_progress does not publish terminal events from handler", func(t *testing.T) {
		id := createPending(t)
		require.NoError(t, sessionService.UpdateSessionStatus(ctx, id, alertsession.StatusInProgress))
		pub.sessionStatus = nil
		pub.reviewStatus = nil

		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCancelling, sess.Status)
		assert.Empty(t, pub.sessionStatus)
		assert.Empty(t, pub.reviewStatus)
	})
}

func TestCancelSessionHandler_ChatOnlyAttribution(t *testing.T) {
	client := testdb.NewTestClient(t)
	sessionService := newUsageTestSessionService(client.Client)
	chatService := services.NewChatService(client.Client)
	stageService := services.NewStageService(client.Client)
	s := &Server{
		sessionService: sessionService,
		chatService:    chatService,
		stageService:   stageService,
	}
	e := echo.New()
	e.POST("/api/v1/sessions/:id/cancel", s.cancelSessionHandler)
	ctx := t.Context()

	createCompleted := func(t *testing.T) string {
		t.Helper()
		sess, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test alert",
			AgentType: "kubernetes",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)
		require.NoError(t, sessionService.UpdateSessionStatus(ctx, sess.ID, alertsession.StatusCompleted))
		return sess.ID
	}

	t.Run("completed session without chat does not set cancel fields", func(t *testing.T) {
		id := createCompleted(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel", nil)
		req.Header.Set("X-Forwarded-User", "alice@example.com")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusConflict, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCompleted, sess.Status)
		assert.Nil(t, sess.CancelledBy)
		assert.Nil(t, sess.CancelReason)
	})

	t.Run("records actor on chat stage without touching session fields", func(t *testing.T) {
		id := createCompleted(t)
		chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
			SessionID: id,
			CreatedBy: "alice@example.com",
		})
		require.NoError(t, err)

		chatID := chatObj.ID
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          id,
			StageName:          "Chat",
			StageIndex:         1,
			ExpectedAgentCount: 1,
			ChatID:             &chatID,
		})
		require.NoError(t, err)

		exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  id,
			AgentName:  "ChatAgent",
			AgentIndex: 1,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)
		require.NoError(t, stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, ""))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+id+"/cancel",
			strings.NewReader(`{"reason":"should not land on session"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-User", "alice@example.com")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// chatExecutor is unset, so HTTP is 409; attribution is written first.
		assert.Equal(t, http.StatusConflict, rec.Code)

		sess, err := sessionService.GetSession(ctx, id, false)
		require.NoError(t, err)
		assert.Equal(t, alertsession.StatusCompleted, sess.Status)
		assert.Nil(t, sess.CancelledBy)
		assert.Nil(t, sess.CancelReason)

		stgAfter, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		require.NotNil(t, stgAfter.ErrorMessage)
		assert.Equal(t, "Cancelled by alice@example.com", *stgAfter.ErrorMessage)

		execAfter, err := stageService.GetAgentExecutionByID(ctx, exec.ID)
		require.NoError(t, err)
		require.NotNil(t, execAfter.ErrorMessage)
		assert.Equal(t, "Cancelled by alice@example.com", *execAfter.ErrorMessage)
	})
}
