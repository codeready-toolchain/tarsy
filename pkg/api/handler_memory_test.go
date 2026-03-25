package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

func setupMemoryRoutes(s *Server) *echo.Echo {
	e := echo.New()
	v1 := e.Group("/api/v1")
	v1.GET("/sessions/:id/memories", s.getSessionMemoriesHandler)
	v1.GET("/sessions/:id/injected-memories", s.getInjectedMemoriesHandler)
	v1.GET("/memories", s.listMemoriesHandler)
	v1.GET("/memories/:id", s.getMemoryHandler)
	v1.PATCH("/memories/:id", s.updateMemoryHandler)
	v1.DELETE("/memories/:id", s.deleteMemoryHandler)
	return e
}

func TestGetSessionMemoriesHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-123/memories", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp []models.MemoryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestGetInjectedMemoriesHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-123/injected-memories", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp []models.MemoryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestListMemoriesHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp models.MemoryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Memories)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, 20, resp.PageSize)
	assert.Equal(t, 1, resp.TotalPages)
}

func TestGetMemoryHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories/mem-123", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateMemoryHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/memories/mem-123",
		strings.NewReader(`{"content": "updated"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteMemoryHandler_NilService(t *testing.T) {
	s := &Server{}
	e := setupMemoryRoutes(s)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/memories/mem-123", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMapMemoryError(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		he := mapMemoryError(memory.ErrMemoryNotFound)
		assert.Equal(t, http.StatusNotFound, he.Code)
	})
}
