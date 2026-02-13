package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

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
		case "created_at", "status", "alert_type", "author", "duration":
			params.SortBy = v
		default:
			return echo.NewHTTPError(http.StatusBadRequest, "invalid sort_by: must be created_at, status, alert_type, author, or duration")
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

// cancelSessionHandler handles POST /api/v1/sessions/:id/cancel.
func (s *Server) cancelSessionHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	// Try to cancel the investigation (DB status in_progress → cancelling).
	sessionErr := s.sessionService.CancelSession(c.Request().Context(), sessionID)

	// Always try to cancel on this pod via worker pool, regardless of DB result.
	if s.workerPool != nil {
		s.workerPool.CancelSession(sessionID)
	}

	// Always try to cancel any active chat execution — a chat may be running
	// even when the session is already completed/failed/timed_out.
	chatCancelled := false
	if s.chatExecutor != nil {
		chatCancelled = s.chatExecutor.CancelBySessionID(c.Request().Context(), sessionID)
	}

	// Return success if either the session or a chat was cancelled.
	if sessionErr != nil && !chatCancelled {
		return mapServiceError(sessionErr)
	}

	return c.JSON(http.StatusOK, &CancelResponse{
		SessionID: sessionID,
		Message:   "Session cancellation requested",
	})
}
