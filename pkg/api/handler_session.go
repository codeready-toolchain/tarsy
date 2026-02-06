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

	if err := s.sessionService.CancelSession(c.Request().Context(), sessionID); err != nil {
		return mapServiceError(err)
	}

	// Also try to cancel on this pod via worker pool
	if s.workerPool != nil {
		s.workerPool.CancelSession(sessionID)
	}

	return c.JSON(http.StatusOK, &CancelResponse{
		SessionID: sessionID,
		Message:   "Session cancellation requested",
	})
}
