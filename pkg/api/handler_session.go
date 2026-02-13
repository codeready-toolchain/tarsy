package api

import (
	"net/http"

	echo "github.com/labstack/echo/v5"
)

// getSessionHandler handles GET /api/v1/sessions/:id.
func (s *Server) getSessionHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	session, err := s.sessionService.GetSession(c.Request().Context(), sessionID, false)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, session)
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
