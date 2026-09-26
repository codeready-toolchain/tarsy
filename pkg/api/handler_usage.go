package api

import (
	"net/http"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// maxUsageSummaryWindow is the maximum allowed end_date - start_date span.
const maxUsageSummaryWindow = 365 * 24 * time.Hour

// usageSummaryHandler handles GET /api/v1/usage/summary.
func (s *Server) usageSummaryHandler(c *echo.Context) error {
	start, end, err := parseUsageWindow(c)
	if err != nil {
		return err
	}

	params := models.UsageSummaryParams{
		StartDate: start,
		EndDate:   end,
		AlertType: c.QueryParam("alert_type"),
		ChainID:   c.QueryParam("chain_id"),
	}

	if v := c.QueryParam("rank_by"); v != "" {
		if !models.ValidUsageRankBy(v) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid rank_by: must be cost or tokens")
		}
		rankBy := models.UsageRankBy(v)
		if rankBy == models.UsageRankByCost && s.sessionService != nil && !s.sessionService.CostEstimationEnabled() {
			return echo.NewHTTPError(http.StatusBadRequest, "rank_by=cost requires cost estimation to be enabled")
		}
		params.RankBy = rankBy
	}

	result, err := s.sessionService.GetUsageSummary(c.Request().Context(), params)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, result)
}

// usageSeriesHandler handles GET /api/v1/usage/series.
func (s *Server) usageSeriesHandler(c *echo.Context) error {
	start, end, err := parseUsageWindow(c)
	if err != nil {
		return err
	}

	result, err := s.sessionService.GetUsageSeries(c.Request().Context(), models.UsageSeriesParams{
		StartDate: start,
		EndDate:   end,
		AlertType: c.QueryParam("alert_type"),
		ChainID:   c.QueryParam("chain_id"),
		Timezone:  c.QueryParam("timezone"),
	})
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, result)
}

// parseUsageWindow reads the shared start_date / end_date rules for usage endpoints.
func parseUsageWindow(c *echo.Context) (time.Time, time.Time, error) {
	startRaw := c.QueryParam("start_date")
	if startRaw == "" {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "start_date is required")
	}
	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "invalid start_date: must be RFC3339")
	}

	endRaw := c.QueryParam("end_date")
	if endRaw == "" {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "end_date is required")
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "invalid end_date: must be RFC3339")
	}

	if !start.Before(end) {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "start_date must be before end_date")
	}
	if end.Sub(start) > maxUsageSummaryWindow {
		return time.Time{}, time.Time{}, echo.NewHTTPError(http.StatusBadRequest, "date window must not exceed 365 days")
	}
	return start, end, nil
}
