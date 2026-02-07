package api

import (
	"fmt"
	"net/http"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	"github.com/codeready-toolchain/tarsy/pkg/services"
)

func TestMapServiceError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		expectCode int
		expectMsg  string
	}{
		{
			name:       "validation error maps to 400",
			err:        services.NewValidationError("name", "missing field"),
			expectCode: http.StatusBadRequest,
			expectMsg:  "missing field",
		},
		{
			name:       "not found maps to 404",
			err:        fmt.Errorf("wrapped: %w", services.ErrNotFound),
			expectCode: http.StatusNotFound,
			expectMsg:  "resource not found",
		},
		{
			name:       "not cancellable maps to 409",
			err:        services.ErrNotCancellable,
			expectCode: http.StatusConflict,
			expectMsg:  "session is not in a cancellable state",
		},
		{
			name:       "already exists maps to 409",
			err:        fmt.Errorf("wrapped: %w", services.ErrAlreadyExists),
			expectCode: http.StatusConflict,
			expectMsg:  "resource already exists",
		},
		{
			name:       "unknown error maps to 500",
			err:        fmt.Errorf("something unexpected happened"),
			expectCode: http.StatusInternalServerError,
			expectMsg:  "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			he := mapServiceError(tt.err)
			assert.IsType(t, &echo.HTTPError{}, he)
			assert.Equal(t, tt.expectCode, he.Code)
			assert.Contains(t, he.Error(), tt.expectMsg)
		})
	}
}
