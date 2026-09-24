package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

type cancelSessionRequest struct {
	Reason string `json:"reason"`
}

// getSessionHandler handles GET /api/v1/sessions/:id.
func (s *Server) getSessionHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	detail, err := s.sessionService.GetSessionDetail(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, detail)
}

// listSessionsHandler handles GET /api/v1/sessions.
func (s *Server) listSessionsHandler(c *echo.Context) error {
	params := models.DashboardListParams{
		Page:      1,
		PageSize:  25,
		SortBy:    "created_at",
		SortOrder: "desc",
	}

	// Parse pagination.
	if v := c.QueryParam("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			params.Page = p
		}
	}
	if v := c.QueryParam("page_size"); v != "" {
		if ps, err := strconv.Atoi(v); err == nil && ps > 0 && ps <= 100 {
			params.PageSize = ps
		}
	}

	// Parse sorting.
	if v := c.QueryParam("sort_by"); v != "" {
		switch v {
		case "created_at", "status", "alert_type", "author", "duration", "score", "quality_rating":
			params.SortBy = v
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid sort_by: must be created_at, status, alert_type, author, duration, score, or quality_rating")
		}
	}
	if v := c.QueryParam("sort_order"); v != "" {
		switch v {
		case "asc", "desc":
			params.SortOrder = v
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid sort_order: must be asc or desc")
		}
	}

	// Parse filters.
	if v := c.QueryParam("status"); v != "" {
		// Validate each comma-separated status.
		statuses := strings.Split(v, ",")
		for _, st := range statuses {
			if err := alertsession.StatusValidator(alertsession.Status(st)); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid status: "+st)
			}
		}
		params.Status = v
	}
	params.AlertType = c.QueryParam("alert_type")
	params.ChainID = c.QueryParam("chain_id")
	if v := c.QueryParam("scoring_status"); v != "" {
		switch v {
		case "scored", "not_scored", "scoring_in_progress", "scoring_failed":
			params.ScoringStatus = v
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid scoring_status: must be scored, not_scored, scoring_in_progress, or scoring_failed")
		}
	}
	if v := c.QueryParam("label"); v != "" {
		for label := range strings.SplitSeq(v, ",") {
			if !config.ValidLabelToken(label) {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid label: must match [A-Za-z][A-Za-z0-9_-]*")
			}
		}
		params.Label = v
	}
	if v := c.QueryParam("search"); v != "" {
		if len(v) < 3 {
			return echo.NewHTTPError(http.StatusBadRequest, "search query must be at least 3 characters")
		}
		params.Search = v
	}

	// Parse date range.
	if v := c.QueryParam("start_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid start_date: must be RFC3339")
		}
		params.StartDate = &t
	}
	if v := c.QueryParam("end_date"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid end_date: must be RFC3339")
		}
		params.EndDate = &t
	}

	if v := c.QueryParam("review_status"); v != "" {
		for _, rs := range strings.Split(v, ",") {
			if err := alertsession.ReviewStatusValidator(alertsession.ReviewStatus(rs)); err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "invalid review_status: "+rs)
			}
		}
		params.ReviewStatus = v
	}
	params.Assignee = c.QueryParam("assignee")
	if v := c.QueryParam("quality_rating"); v != "" {
		if err := alertsession.QualityRatingValidator(alertsession.QualityRating(v)); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid quality_rating: "+v)
		}
		params.QualityRating = v
	}

	result, err := s.sessionService.ListSessionsForDashboard(c.Request().Context(), params)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, result)
}

// activeSessionsHandler handles GET /api/v1/sessions/active.
func (s *Server) activeSessionsHandler(c *echo.Context) error {
	result, err := s.sessionService.GetActiveSessions(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, result)
}

// sessionSummaryHandler handles GET /api/v1/sessions/:id/summary.
func (s *Server) sessionSummaryHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	summary, err := s.sessionService.GetSessionSummary(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, summary)
}

// sessionStatusHandler handles GET /api/v1/sessions/:id/status.
func (s *Server) sessionStatusHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	status, err := s.sessionService.GetSessionStatus(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, status)
}

// cancelSessionHandler handles POST /api/v1/sessions/:id/cancel.
func (s *Server) cancelSessionHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	var req cancelSessionRequest
	if c.Request().ContentLength > 0 {
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
		}
	}

	reason, err := services.NormalizeCancelReason(req.Reason)
	if err != nil {
		return mapServiceError(err)
	}
	author := extractAuthor(c)

	written, sessionErr := s.sessionService.CancelSession(c.Request().Context(), sessionID, author, reason)

	if written == alertsession.StatusCancelled {
		s.publishPendingCancelEvents(sessionID)
		alertType := ""
		if sess, getErr := s.sessionService.GetSession(c.Request().Context(), sessionID, false); getErr != nil {
			slog.Warn("Failed to load session for pending-cancel metrics",
				"session_id", sessionID, "error", getErr)
		} else {
			alertType = sess.AlertType
		}
		metrics.SessionsTerminalTotal.WithLabelValues(alertType, string(alertsession.StatusCancelled)).Inc()
	}

	if errors.Is(sessionErr, services.ErrNotCancellable) {
		s.writeChatCancelAttribution(sessionID, author)
	}

	if s.workerPool != nil {
		s.workerPool.CancelSession(sessionID)
	}

	chatCancelled := false
	if s.chatExecutor != nil {
		chatCancelled = s.chatExecutor.CancelBySessionID(c.Request().Context(), sessionID)
	}

	if s.cancelNotifier != nil {
		if err := s.cancelNotifier.NotifyCancelSession(c.Request().Context(), sessionID); err != nil {
			slog.Warn("Failed to broadcast cancel notification", "session_id", sessionID, "error", err)
		}
	}

	if sessionErr != nil && !chatCancelled {
		return mapServiceError(sessionErr)
	}

	return c.JSON(http.StatusOK, &CancelResponse{
		SessionID: sessionID,
		Message:   "Session cancellation requested",
	})
}

func (s *Server) publishPendingCancelEvents(sessionID string) {
	if s.eventPublisher == nil {
		return
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pubCancel()

	if err := s.eventPublisher.PublishSessionStatus(pubCtx, sessionID, events.SessionStatusPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeSessionStatus,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		Status: alertsession.StatusCancelled,
	}); err != nil {
		slog.Warn("Failed to publish session status",
			"session_id", sessionID, "status", alertsession.StatusCancelled, "error", err)
	}

	rs := string(alertsession.ReviewStatusReviewed)
	if err := s.eventPublisher.PublishReviewStatus(pubCtx, sessionID, events.ReviewStatusPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeReviewStatus,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		Actor:        "system",
		ReviewStatus: &rs,
	}); err != nil {
		slog.Warn("Failed to publish review status",
			"session_id", sessionID, "error", err)
	}
}

func (s *Server) writeChatCancelAttribution(sessionID, actor string) {
	if s.chatService == nil || s.stageService == nil {
		return
	}
	ctx, cancel := context.WithTimeoutCause(
		context.Background(), 5*time.Second,
		fmt.Errorf("write chat cancel attribution for session %s: timed out", sessionID),
	)
	defer cancel()

	chatObj, err := s.chatService.GetChatBySessionID(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to look up chat for cancel attribution",
			"session_id", sessionID, "error", err)
		return
	}
	if chatObj == nil {
		return
	}
	stg, err := s.stageService.GetActiveStageForChat(ctx, chatObj.ID)
	if err != nil {
		slog.Warn("Failed to look up chat stage for cancel attribution",
			"session_id", sessionID, "error", err)
		return
	}
	if stg == nil {
		return
	}
	msg := fmt.Sprintf("Cancelled by %s", actor)
	if err := s.stageService.WriteCancelAttribution(stg.ID, msg); err != nil {
		slog.Warn("Failed to write chat cancel attribution",
			"session_id", sessionID, "stage_id", stg.ID, "error", err)
	}
}
