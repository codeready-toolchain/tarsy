package api

import (
	"context"
	"net/http"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/llm"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/gin-gonic/gin"
)

// Server represents the API server
type Server struct {
	db    *database.Client
	llm   *llm.Client
	wsHub *WSHub
	
	// Services
	sessionService     *services.SessionService
	stageService       *services.StageService
	timelineService    *services.TimelineService
	messageService     *services.MessageService
	interactionService *services.InteractionService
	eventService       *services.EventService
	chatService        *services.ChatService
}

// NewServer creates a new API server
func NewServer(
	db *database.Client,
	llm *llm.Client,
	wsHub *WSHub,
	sessionService *services.SessionService,
	stageService *services.StageService,
	timelineService *services.TimelineService,
	messageService *services.MessageService,
	interactionService *services.InteractionService,
	eventService *services.EventService,
	chatService *services.ChatService,
) *Server {
	return &Server{
		db:                 db,
		llm:                llm,
		wsHub:              wsHub,
		sessionService:     sessionService,
		stageService:       stageService,
		timelineService:    timelineService,
		messageService:     messageService,
		interactionService: interactionService,
		eventService:       eventService,
		chatService:        chatService,
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
			"phase":    "2.3 - Service Layer Complete",
			"error":    err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "healthy",
		"database": dbHealth,
		"phase":    "2.3 - Service Layer Complete",
	})
}

// CreateAlert creates a new alert session
func (s *Server) CreateAlert(c *gin.Context) {
	// TODO: Implement in Phase 3 (Agent Framework)
	c.JSON(http.StatusNotImplemented, gin.H{
		"message": "Service layer ready - Agent framework coming in Phase 3",
	})
}

// ListSessions lists all sessions
func (s *Server) ListSessions(c *gin.Context) {
	// TODO: Implement in Phase 3 (Agent Framework)
	c.JSON(http.StatusNotImplemented, gin.H{
		"message": "Service layer ready - Agent framework coming in Phase 3",
	})
}

// GetSession retrieves a session by ID
func (s *Server) GetSession(c *gin.Context) {
	// TODO: Implement in Phase 3 (Agent Framework)
	c.JSON(http.StatusNotImplemented, gin.H{
		"message": "Service layer ready - Agent framework coming in Phase 3",
	})
}

// CancelSession cancels a session
func (s *Server) CancelSession(c *gin.Context) {
	// TODO: Implement in Phase 3 (Agent Framework)
	c.JSON(http.StatusNotImplemented, gin.H{
		"message": "Service layer ready - Agent framework coming in Phase 3",
	})
}
