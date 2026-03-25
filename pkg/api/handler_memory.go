package api

import (
	"errors"
	"net/http"
	"strconv"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// getSessionMemoriesHandler handles GET /api/v1/sessions/:id/memories.
// Returns memories extracted FROM this session (source_session_id).
func (s *Server) getSessionMemoriesHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return c.JSON(http.StatusOK, []models.MemoryResponse{})
	}
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	memories, err := s.memoryService.GetBySessionID(c.Request().Context(), sessionID)
	if err != nil {
		return mapMemoryError(err)
	}
	return c.JSON(http.StatusOK, toMemoryResponses(memories))
}

// getInjectedMemoriesHandler handles GET /api/v1/sessions/:id/injected-memories.
// Returns memories that were injected INTO this session's prompt.
func (s *Server) getInjectedMemoriesHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return c.JSON(http.StatusOK, []models.MemoryResponse{})
	}
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}

	memories, err := s.memoryService.GetInjectedBySessionID(c.Request().Context(), sessionID)
	if err != nil {
		return mapMemoryError(err)
	}
	return c.JSON(http.StatusOK, toMemoryResponses(memories))
}

// listMemoriesHandler handles GET /api/v1/memories.
func (s *Server) listMemoriesHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return c.JSON(http.StatusOK, models.MemoryListResponse{
			Memories: []models.MemoryResponse{}, Page: 1, PageSize: 20, TotalPages: 1,
		})
	}

	params := memory.ListParams{
		Project:  "default",
		Page:     1,
		PageSize: defaultPageSize,
	}

	if v := c.QueryParam("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			params.Page = p
		}
	}
	if v := c.QueryParam("page_size"); v != "" {
		if ps, err := strconv.Atoi(v); err == nil && ps > 0 && ps <= maxPageSize {
			params.PageSize = ps
		}
	}
	if v := c.QueryParam("category"); v != "" {
		params.Category = &v
	}
	if v := c.QueryParam("valence"); v != "" {
		params.Valence = &v
	}
	if v := c.QueryParam("deprecated"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			params.Deprecated = &b
		}
	}
	if v := c.QueryParam("source_session_id"); v != "" {
		params.SourceSessionID = &v
	}

	result, err := s.memoryService.List(c.Request().Context(), params)
	if err != nil {
		return mapMemoryError(err)
	}

	return c.JSON(http.StatusOK, models.MemoryListResponse{
		Memories:   toMemoryResponses(result.Memories),
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	})
}

// getMemoryHandler handles GET /api/v1/memories/:id.
func (s *Server) getMemoryHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return echo.NewHTTPError(http.StatusNotFound, "memory not found")
	}
	memoryID := c.Param("id")
	if memoryID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "memory id is required")
	}

	m, err := s.memoryService.GetByID(c.Request().Context(), memoryID)
	if err != nil {
		return mapMemoryError(err)
	}
	return c.JSON(http.StatusOK, toMemoryResponse(*m))
}

// updateMemoryHandler handles PATCH /api/v1/memories/:id.
func (s *Server) updateMemoryHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return echo.NewHTTPError(http.StatusNotFound, "memory not found")
	}
	memoryID := c.Param("id")
	if memoryID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "memory id is required")
	}

	var req models.UpdateMemoryRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	m, err := s.memoryService.Update(c.Request().Context(), memoryID, memory.UpdateInput{
		Content:    req.Content,
		Category:   req.Category,
		Valence:    req.Valence,
		Deprecated: req.Deprecated,
	})
	if err != nil {
		return mapMemoryError(err)
	}
	return c.JSON(http.StatusOK, toMemoryResponse(*m))
}

// deleteMemoryHandler handles DELETE /api/v1/memories/:id.
func (s *Server) deleteMemoryHandler(c *echo.Context) error {
	if s.memoryService == nil {
		return echo.NewHTTPError(http.StatusNotFound, "memory not found")
	}
	memoryID := c.Param("id")
	if memoryID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "memory id is required")
	}

	if err := s.memoryService.Delete(c.Request().Context(), memoryID); err != nil {
		return mapMemoryError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func mapMemoryError(err error) *echo.HTTPError {
	if errors.Is(err, memory.ErrMemoryNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "memory not found")
	}
	return mapServiceError(err)
}

func toMemoryResponse(m memory.MemoryDetail) models.MemoryResponse {
	return models.MemoryResponse{
		ID:              m.ID,
		Project:         m.Project,
		Content:         m.Content,
		Category:        m.Category,
		Valence:         m.Valence,
		Confidence:      m.Confidence,
		SeenCount:       m.SeenCount,
		SourceSessionID: m.SourceSessionID,
		AlertType:       m.AlertType,
		ChainID:         m.ChainID,
		Deprecated:      m.Deprecated,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		LastSeenAt:      m.LastSeenAt,
	}
}

func toMemoryResponses(memories []memory.MemoryDetail) []models.MemoryResponse {
	result := make([]models.MemoryResponse, 0, len(memories))
	for _, m := range memories {
		result = append(result, toMemoryResponse(m))
	}
	return result
}
