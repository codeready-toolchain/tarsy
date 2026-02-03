package api

import (
	"context"
	"log"
	"net/http"

	"github.com/codeready-toolchain/tarsy/pkg/llm"
	"github.com/codeready-toolchain/tarsy/pkg/session"

	"github.com/gin-gonic/gin"
)

// Server represents the HTTP server
type Server struct {
	sessionMgr *session.Manager
	llmClient  *llm.Client
	wsHub      *WSHub
}

// NewServer creates a new API server
func NewServer(sessionMgr *session.Manager, llmClient *llm.Client, wsHub *WSHub) *Server {
	return &Server{
		sessionMgr: sessionMgr,
		llmClient:  llmClient,
		wsHub:      wsHub,
	}
}

// CreateAlertRequest represents the request body for creating an alert
type CreateAlertRequest struct {
	Message string `json:"message" binding:"required"`
}

// CreateAlert handles POST /api/alerts
func (s *Server) CreateAlert(c *gin.Context) {
	var req CreateAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create session
	sess, err := s.sessionMgr.Create(req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Created session %s", sess.ID)

	// Broadcast session created event
	s.wsHub.Broadcast("session.created", sess.ID, sess.Clone())

	// Start LLM processing in background
	go s.processSession(sess)

	c.JSON(http.StatusOK, sess.Clone())
}

// processSession processes a session with LLM streaming
func (s *Server) processSession(sess *session.Session) {
	ctx := context.Background()

	log.Printf("Starting LLM processing for session %s", sess.ID)
	sess.SetStatus(session.StatusProcessing)

	// Broadcast status update
	s.wsHub.Broadcast("session.status", sess.ID, map[string]interface{}{
		"status": sess.Status,
	})

	// Get stream from LLM
	chunks, errs := s.llmClient.GenerateStream(ctx, sess)

	var accumulatedResponse string

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				// Stream closed
				if accumulatedResponse != "" {
					sess.AddMessage(session.RoleAssistant, accumulatedResponse)
					sess.SetStatus(session.StatusCompleted)

					// Broadcast completion
					s.wsHub.Broadcast("session.completed", sess.ID, sess.Clone())
					log.Printf("Completed session %s", sess.ID)
				}
				return
			}

			if chunk.Error != "" {
				sess.SetError(chunk.Error)
				s.wsHub.Broadcast("session.error", sess.ID, map[string]interface{}{
					"error": chunk.Error,
				})
				return
			}

			if chunk.IsThinking {
				s.wsHub.Broadcast("llm.thinking", sess.ID, map[string]interface{}{
					"content":     chunk.Content,
					"is_complete": chunk.IsComplete,
				})
			} else {
				accumulatedResponse = chunk.Content
				s.wsHub.Broadcast("llm.response", sess.ID, map[string]interface{}{
					"content":     chunk.Content,
					"is_complete": chunk.IsComplete,
					"is_final":    chunk.IsFinal,
				})
			}

		case err := <-errs:
			if err != nil {
				log.Printf("Error processing session %s: %v", sess.ID, err)
				sess.SetError(err.Error())
				s.wsHub.Broadcast("session.error", sess.ID, map[string]interface{}{
					"error": err.Error(),
				})
				return
			}
		}
	}
}

// ListSessions handles GET /api/sessions
func (s *Server) ListSessions(c *gin.Context) {
	sessions := s.sessionMgr.List()
	c.JSON(http.StatusOK, sessions)
}

// GetSession handles GET /api/sessions/:id
func (s *Server) GetSession(c *gin.Context) {
	sessionID := c.Param("id")

	sess, err := s.sessionMgr.Get(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}

	c.JSON(http.StatusOK, sess.Clone())
}

// Health handles GET /health
func (s *Server) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
