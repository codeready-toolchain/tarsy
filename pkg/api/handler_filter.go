package api

import (
	"net/http"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
)

// FilterOptionsResponse is returned by GET /api/v1/sessions/filter-options.
type FilterOptionsResponse struct {
	AlertTypes []string `json:"alert_types"`
	ChainIDs   []string `json:"chain_ids"`
	Statuses   []string `json:"statuses"`
}

// filterOptionsHandler handles GET /api/v1/sessions/filter-options.
func (s *Server) filterOptionsHandler(c *echo.Context) error {
	ctx := c.Request().Context()

	alertTypes, err := s.sessionService.GetDistinctAlertTypes(ctx)
	if err != nil {
		return mapServiceError(err)
	}

	chainIDs, err := s.sessionService.GetDistinctChainIDs(ctx)
	if err != nil {
		return mapServiceError(err)
	}

	// Statuses are the static enum values — always return all possible values.
	statuses := []string{
		string(alertsession.StatusPending),
		string(alertsession.StatusInProgress),
		string(alertsession.StatusCancelling),
		string(alertsession.StatusCompleted),
		string(alertsession.StatusFailed),
		string(alertsession.StatusCancelled),
		string(alertsession.StatusTimedOut),
	}

	return c.JSON(http.StatusOK, FilterOptionsResponse{
		AlertTypes: alertTypes,
		ChainIDs:   chainIDs,
		Statuses:   statuses,
	})
}
