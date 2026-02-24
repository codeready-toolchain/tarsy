package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestListSessionsHandler_Validation(t *testing.T) {
	// We only test parameter validation (returns 400 before hitting the service).
	// Happy-path is covered by integration/e2e tests that have a real service.
	s := &Server{}

	tests := []struct {
		name    string
		query   string
		wantErr int
		errMsg  string
	}{
		{
			name:    "invalid sort_by",
			query:   "sort_by=unknown_field",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid sort_by",
		},
		{
			name:    "invalid sort_order",
			query:   "sort_order=random",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid sort_order",
		},
		{
			name:    "invalid status value",
			query:   "status=bogus",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid status",
		},
		{
			name:    "search too short",
			query:   "search=ab",
			wantErr: http.StatusBadRequest,
			errMsg:  "search query must be at least 3 characters",
		},
		{
			name:    "invalid start_date",
			query:   "start_date=not-a-date",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid start_date",
		},
		{
			name:    "end_date wrong format (not RFC3339)",
			query:   "end_date=2024-01-01",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid end_date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?"+tt.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := s.listSessionsHandler(c)
			if assert.Error(t, err) {
				he, ok := err.(*echo.HTTPError)
				if assert.True(t, ok, "expected echo.HTTPError") {
					assert.Equal(t, tt.wantErr, he.Code)
					assert.Contains(t, he.Message, tt.errMsg)
				}
			}
		})
	}

	t.Run("comma-separated statuses with one invalid", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=completed,bogus", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.listSessionsHandler(c)
		if assert.Error(t, err) {
			he, ok := err.(*echo.HTTPError)
			if assert.True(t, ok) {
				assert.Equal(t, http.StatusBadRequest, he.Code)
				assert.Contains(t, he.Message, "invalid status: bogus")
			}
		}
	})
}

func TestSessionStatusHandler_Validation(t *testing.T) {
	s := &Server{}

	t.Run("missing session id returns 400", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions//status", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.sessionStatusHandler(c)
		if assert.Error(t, err) {
			he, ok := err.(*echo.HTTPError)
			if assert.True(t, ok, "expected echo.HTTPError") {
				assert.Equal(t, http.StatusBadRequest, he.Code)
				assert.Contains(t, he.Message, "session id")
			}
		}
	})
}
