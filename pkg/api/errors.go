package api

import (
	"errors"
	"log/slog"
	"net/http"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// mapServiceError maps service-layer errors to HTTP error responses.
func mapServiceError(err error) *echo.HTTPError {
	var validErr *services.ValidationError
	if errors.As(err, &validErr) {
		return echo.NewHTTPError(http.StatusBadRequest, validErr.Error())
	}
	if errors.Is(err, services.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "resource not found")
	}
	if errors.Is(err, services.ErrNotCancellable) {
		return echo.NewHTTPError(http.StatusConflict, "session is not in a cancellable state")
	}
	if errors.Is(err, services.ErrAlreadyExists) {
		return echo.NewHTTPError(http.StatusConflict, "resource already exists")
	}

	// Unexpected error
	slog.Error("Unexpected service error", "error", err)
	return echo.NewHTTPError(http.StatusInternalServerError, "internal server error")
}
