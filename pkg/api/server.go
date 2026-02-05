package api

import (
	"context"
	"net/http"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/llm"
	"github.com/gin-gonic/gin"
)

// Server represents the API server
// NOTE: This is a minimal stub for Phase 2.1
// Will be properly implemented in Phase 2.3 (Service Layer)
type Server struct {
	db    *database.Client
	llm   *llm.Client
	wsHub *WSHub
}

// NewServer creates a new API server
func NewServer(db *database.Client, llm *llm.Client, wsHub *WSHub) *Server {
	return &Server{
		db:    db,
		llm:   llm,
		wsHub: wsHub,
	}
}

// Health returns the health status
func (s *Server) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	dbHealth, err := database.Health(ctx, s.db.DB())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":   "unhealthy",
			"database": dbHealth,
			"phase":    "2.2 - Database Client Complete",
			"error":    err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "healthy",
		"database": dbHealth,
		"phase":    "2.2 - Database Client Complete",
	})
}

// CreateAlert - Stub for Phase 2.3
func (s *Server) CreateAlert(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Not implemented yet - Phase 2.3",
	})
}

// ListSessions - Stub for Phase 2.3
func (s *Server) ListSessions(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Not implemented yet - Phase 2.3",
	})
}

// GetSession - Stub for Phase 2.3
func (s *Server) GetSession(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Not implemented yet - Phase 2.3",
	})
}

// CancelSession - Stub for Phase 2.3
func (s *Server) CancelSession(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "Not implemented yet - Phase 2.3",
	})
}
