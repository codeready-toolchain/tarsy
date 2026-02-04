package api

import (
	"net/http"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/llm"
	"github.com/gin-gonic/gin"
)

// Server represents the API server
// NOTE: This is a minimal stub for Phase 2.1
// Will be properly implemented in Phase 2.3 (Service Layer)
type Server struct {
	db    *ent.Client
	llm   *llm.Client
	wsHub *WSHub
}

// NewServer creates a new API server
func NewServer(db *ent.Client, llm *llm.Client, wsHub *WSHub) *Server {
	return &Server{
		db:    db,
		llm:   llm,
		wsHub: wsHub,
	}
}

// Health returns the health status
func (s *Server) Health(c *gin.Context) {
	// Simple health check - just return OK if we got here
	// Database connection is tested on startup
	c.JSON(http.StatusOK, gin.H{
		"status":   "healthy",
		"database": "connected",
		"phase":    "2.1 - Schema & Migrations Complete",
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
