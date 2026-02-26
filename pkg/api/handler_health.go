package api

import (
	"context"
	"net/http"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/version"
)

const (
	healthStatusHealthy   = "healthy"
	healthStatusDegraded  = "degraded"
	healthStatusUnhealthy = "unhealthy"
)

// healthHandler handles GET /health.
// Returns a minimal, safe response suitable for unauthenticated access.
// Only tarsy's own components (database, worker_pool) are checked.
// External dependencies (MCP servers, LLM service) are excluded to prevent
// the orchestrator from restarting tarsy when an external service is unhealthy.
func (s *Server) healthHandler(c *echo.Context) error {
	reqCtx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	checks := make(map[string]HealthCheck)
	status := healthStatusHealthy

	_, err := database.Health(reqCtx, s.dbClient.DB())
	if err != nil {
		status = healthStatusUnhealthy
		checks["database"] = HealthCheck{Status: healthStatusUnhealthy, Message: err.Error()}
	} else {
		checks["database"] = HealthCheck{Status: healthStatusHealthy}
	}

	if s.workerPool != nil {
		poolHealth := s.workerPool.Health()
		if poolHealth != nil && !poolHealth.IsHealthy {
			if status == healthStatusHealthy {
				status = healthStatusDegraded
			}
			msg := healthStatusUnhealthy
			if poolHealth.DBError != "" {
				msg = poolHealth.DBError
			}
			checks["worker_pool"] = HealthCheck{Status: healthStatusDegraded, Message: msg}
		} else {
			checks["worker_pool"] = HealthCheck{Status: healthStatusHealthy}
		}
	}

	httpStatus := http.StatusOK
	if status == healthStatusUnhealthy {
		httpStatus = http.StatusServiceUnavailable
	}

	return c.JSON(httpStatus, &HealthResponse{
		Status:  status,
		Version: version.GitCommit,
		Checks:  checks,
	})
}
