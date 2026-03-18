package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestUpdateReviewHandler_Validation(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.PATCH("/api/v1/sessions/review", s.updateReviewHandler)

	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: "{bad json"},
		{name: "empty session_ids", body: `{"session_ids":[],"action":"claim"}`},
		{name: "missing session_ids", body: `{"action":"claim"}`},
		{name: "unknown action", body: `{"session_ids":["id1"],"action":"bogus"}`},
		{name: "resolve without resolution_reason", body: `{"session_ids":["id1"],"action":"resolve"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/review",
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestUpdateReviewHandler_TooManyIDs(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.PATCH("/api/v1/sessions/review", s.updateReviewHandler)

	var ids strings.Builder
	ids.WriteString(`{"session_ids":[`)
	for i := range 51 {
		if i > 0 {
			ids.WriteString(",")
		}
		ids.WriteString(`"id-` + strings.Repeat("x", 5) + `"`)
	}
	ids.WriteString(`],"action":"claim"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/review",
		strings.NewReader(ids.String()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateReviewHandler_NilSessionService(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.PATCH("/api/v1/sessions/review", s.updateReviewHandler)

	body := `{"session_ids":["test-123"],"action":"claim"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/review",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

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

func TestGetTriageGroupHandler_InvalidParams(t *testing.T) {
	s := &Server{}
	e := echo.New()
	e.GET("/api/v1/sessions/triage/:group", s.getTriageGroupHandler)

	tests := []struct {
		name string
		path string
	}{
		{name: "unknown group", path: "/api/v1/sessions/triage/bogus"},
		{name: "page non-numeric", path: "/api/v1/sessions/triage/investigating?page=abc"},
		{name: "page zero", path: "/api/v1/sessions/triage/investigating?page=0"},
		{name: "page_size non-numeric", path: "/api/v1/sessions/triage/resolved?page_size=xyz"},
		{name: "page_size zero", path: "/api/v1/sessions/triage/resolved?page_size=0"},
		{name: "page_size exceeds max", path: "/api/v1/sessions/triage/resolved?page_size=999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
