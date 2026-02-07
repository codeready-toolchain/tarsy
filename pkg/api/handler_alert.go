package api

import (
	"net/http"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// submitAlertHandler handles POST /api/v1/alerts.
// Creates a session in "pending" status and returns immediately with session_id.
func (s *Server) submitAlertHandler(c *echo.Context) error {
	// 1. Bind HTTP request
	var req SubmitAlertRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// 2. Validate required fields
	if req.Data == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "data field is required")
	}

	// 3. Enforce alert data size limit
	if len(req.Data) > agent.MaxAlertDataSize {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "alert data exceeds maximum size of 1 MB")
	}

	// 4. Transform to service input
	input := services.SubmitAlertInput{
		AlertType: req.AlertType,
		Runbook:   req.Runbook,
		Data:      req.Data,
		MCP:       req.MCP,
		Author:    extractAuthor(c),
	}

	// 5. Call service
	session, err := s.alertService.SubmitAlert(c.Request().Context(), input)
	if err != nil {
		return mapServiceError(err)
	}

	// 6. Return response
	return c.JSON(http.StatusAccepted, &AlertResponse{
		SessionID: session.ID,
		Status:    "queued",
		Message:   "Alert submitted for processing",
	})
}
