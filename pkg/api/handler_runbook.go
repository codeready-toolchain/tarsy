package api

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
)

// handleListRunbooks handles GET /api/v1/runbooks.
// Returns available runbook URLs from the configured GitHub repository.
// Fail-open: returns empty array on error or when the service is not configured.
func (s *Server) handleListRunbooks(c *echo.Context) error {
	if s.runbookService == nil {
		return c.JSON(http.StatusOK, []string{})
	}

	runbooks, err := s.runbookService.ListRunbooks(c.Request().Context())
	if err != nil {
		slog.Warn("Failed to list runbooks", "error", err)
		return c.JSON(http.StatusOK, []string{})
	}

	return c.JSON(http.StatusOK, runbooks)
}
