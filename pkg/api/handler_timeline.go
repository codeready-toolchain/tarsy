package api

import (
	"net/http"

	echo "github.com/labstack/echo/v5"
)

// getTimelineHandler handles GET /api/v1/sessions/:id/timeline.
func (s *Server) getTimelineHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}
	if s.timelineService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "timeline endpoint not configured")
	}

	events, err := s.timelineService.GetSessionTimeline(c.Request().Context(), sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, events)
}
