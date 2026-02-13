// Package api provides HTTP API handlers for TARSy.
package api

import (
	"context"
	"net"
	"net/http"
	"time"

	echo "github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// Server is the HTTP API server.
type Server struct {
	echo               *echo.Echo
	httpServer         *http.Server
	cfg                *config.Config
	dbClient           *database.Client
	alertService       *services.AlertService
	sessionService     *services.SessionService
	workerPool         *queue.WorkerPool
	connManager        *events.ConnectionManager
	healthMonitor      *mcp.HealthMonitor              // nil if MCP disabled
	warningService     *services.SystemWarningsService // nil if MCP disabled
	chatService        *services.ChatService           // nil until set
	chatExecutor       *queue.ChatMessageExecutor      // nil until set
	eventPublisher     agent.EventPublisher            // nil if streaming disabled
	interactionService *services.InteractionService    // nil until set (debug endpoints)
	stageService       *services.StageService          // nil until set (debug endpoints)
	timelineService    *services.TimelineService       // nil until set (timeline endpoint)
}

// NewServer creates a new API server with Echo v5.
func NewServer(
	cfg *config.Config,
	dbClient *database.Client,
	alertService *services.AlertService,
	sessionService *services.SessionService,
	workerPool *queue.WorkerPool,
	connManager *events.ConnectionManager,
) *Server {
	e := echo.New()

	s := &Server{
		echo:           e,
		cfg:            cfg,
		dbClient:       dbClient,
		alertService:   alertService,
		sessionService: sessionService,
		workerPool:     workerPool,
		connManager:    connManager,
	}

	s.setupRoutes()
	return s
}

// SetHealthMonitor sets the MCP health monitor for the health endpoint.
func (s *Server) SetHealthMonitor(monitor *mcp.HealthMonitor) {
	s.healthMonitor = monitor
}

// SetWarningsService sets the system warnings service for the health endpoint.
func (s *Server) SetWarningsService(svc *services.SystemWarningsService) {
	s.warningService = svc
}

// SetChatService sets the chat service for follow-up chat endpoints.
func (s *Server) SetChatService(svc *services.ChatService) {
	s.chatService = svc
}

// SetChatExecutor sets the chat message executor for follow-up chat processing.
func (s *Server) SetChatExecutor(executor *queue.ChatMessageExecutor) {
	s.chatExecutor = executor
}

// SetEventPublisher sets the event publisher for real-time event delivery.
func (s *Server) SetEventPublisher(pub agent.EventPublisher) {
	s.eventPublisher = pub
}

// SetInteractionService sets the interaction service for debug endpoints.
func (s *Server) SetInteractionService(svc *services.InteractionService) {
	s.interactionService = svc
}

// SetStageService sets the stage service for debug endpoints.
func (s *Server) SetStageService(svc *services.StageService) {
	s.stageService = svc
}

// SetTimelineService sets the timeline service for the timeline endpoint.
func (s *Server) SetTimelineService(svc *services.TimelineService) {
	s.timelineService = svc
}

// setupRoutes registers all API routes.
func (s *Server) setupRoutes() {
	// Server-wide body size limit (2 MB) — set slightly above MaxAlertDataSize
	// (1 MB) to account for JSON envelope overhead. Rejects multi-MB/GB payloads
	// at the HTTP read level before deserialization, complementing the
	// application-level MaxAlertDataSize check in submitAlertHandler.
	s.echo.Use(middleware.BodyLimit(2 * 1024 * 1024))

	// Health check
	s.echo.GET("/health", s.healthHandler)

	// API v1
	v1 := s.echo.Group("/api/v1")
	v1.POST("/alerts", s.submitAlertHandler)
	v1.GET("/sessions/:id", s.getSessionHandler)
	v1.POST("/sessions/:id/cancel", s.cancelSessionHandler)
	v1.POST("/sessions/:id/chat/messages", s.sendChatMessageHandler)
	v1.GET("/sessions/:id/timeline", s.getTimelineHandler)

	// Debug/observability endpoints (two-level loading).
	v1.GET("/sessions/:id/debug", s.getDebugListHandler)
	v1.GET("/sessions/:id/debug/llm/:interaction_id", s.getLLMInteractionHandler)
	v1.GET("/sessions/:id/debug/mcp/:interaction_id", s.getMCPInteractionHandler)

	// WebSocket endpoint for real-time event streaming.
	// Auth deferred to Phase 9 (Security) — currently open to any client,
	// consistent with the InsecureSkipVerify origin policy in handler_ws.go.
	s.echo.GET("/ws", s.wsHandler)
}

// Start starts the HTTP server on the given address (non-blocking).
func (s *Server) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.echo,
	}
	return s.httpServer.ListenAndServe()
}

// StartWithListener starts the HTTP server on a pre-created listener.
// Used by test infrastructure to serve on a random OS-assigned port.
func (s *Server) StartWithListener(ln net.Listener) error {
	s.httpServer = &http.Server{Handler: s.echo}
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// healthHandler handles GET /health.
func (s *Server) healthHandler(c *echo.Context) error {
	reqCtx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()

	dbHealth, err := database.Health(reqCtx, s.dbClient.DB())
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, &HealthResponse{
			Status:   "unhealthy",
			Database: dbHealth,
		})
	}

	stats := s.cfg.Stats()
	response := &HealthResponse{
		Status:   "healthy",
		Database: dbHealth,
		Phase:    "2.3 - Queue & Worker System",
		Configuration: ConfigurationStats{
			Agents:       stats.Agents,
			Chains:       stats.Chains,
			MCPServers:   stats.MCPServers,
			LLMProviders: stats.LLMProviders,
		},
	}

	if s.workerPool != nil {
		poolHealth := s.workerPool.Health()
		response.WorkerPool = poolHealth
	}

	// MCP health statuses
	if s.healthMonitor != nil {
		response.MCPHealth = s.healthMonitor.GetStatuses()
		if !s.healthMonitor.IsHealthy() {
			response.Status = "degraded"
		}
	}

	// System warnings
	if s.warningService != nil {
		warnings := s.warningService.GetWarnings()
		if len(warnings) > 0 {
			response.Warnings = warnings
		}
	}

	return c.JSON(http.StatusOK, response)
}
