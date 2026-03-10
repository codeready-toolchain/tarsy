package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
)

func TestScoreSessionHandler_MissingSessionID(t *testing.T) {
	s := &Server{}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions//score", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	// Param "id" will return "" since there's no router match

	err := s.scoreSessionHandler(c)
	if assert.Error(t, err) {
		he, ok := err.(*echo.HTTPError)
		if assert.True(t, ok) {
			assert.Equal(t, http.StatusBadRequest, he.Code)
		}
	}
}

func TestScoreSessionHandler_NilScoringExecutor(t *testing.T) {
	s := &Server{}
	e := echo.New()

	// Register the route so echo can extract path params
	e.POST("/api/v1/sessions/:id/score", s.scoreSessionHandler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/test-123/score", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
