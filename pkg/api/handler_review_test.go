package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestUpdateReviewHandler_MissingSessionID(t *testing.T) {
	s := &Server{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions//review", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.updateReviewHandler(c)
	if assert.Error(t, err) {
		he, ok := err.(*echo.HTTPError)
		if assert.True(t, ok) {
			assert.Equal(t, http.StatusBadRequest, he.Code)
		}
	}
}

func TestUpdateReviewHandler_InvalidBody(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.PATCH("/api/v1/sessions/:id/review", s.updateReviewHandler)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/test-123/review",
		strings.NewReader("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateReviewHandler_NilSessionService(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.PATCH("/api/v1/sessions/:id/review", s.updateReviewHandler)

	body := `{"action": "claim"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/test-123/review",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// sessionService is nil — should panic or 500, testing that it doesn't crash silently
	assert.Panics(t, func() {
		e.ServeHTTP(rec, req)
	})
}

func TestGetReviewActivityHandler_MissingSessionID(t *testing.T) {
	s := &Server{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions//review-activity", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := s.getReviewActivityHandler(c)
	if assert.Error(t, err) {
		he, ok := err.(*echo.HTTPError)
		if assert.True(t, ok) {
			assert.Equal(t, http.StatusBadRequest, he.Code)
		}
	}
}

func TestGetTriageHandler_InvalidResolvedLimit(t *testing.T) {
	s := &Server{}
	e := echo.New()

	tests := []struct {
		name  string
		query string
	}{
		{name: "non-numeric", query: "resolved_limit=abc"},
		{name: "negative", query: "resolved_limit=-5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/triage?"+tt.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := s.getTriageHandler(c)
			if assert.Error(t, err) {
				he, ok := err.(*echo.HTTPError)
				if assert.True(t, ok) {
					assert.Equal(t, http.StatusBadRequest, he.Code)
				}
			}
		})
	}
}
