// Package api provides HTTP API handlers for TARSy.
package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	echo "github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/codeready-toolchain/tarsy/pkg/version"
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
	interactionService *services.InteractionService    // nil until set (trace endpoints)
	stageService       *services.StageService          // nil until set (trace endpoints)
	timelineService    *services.TimelineService       // nil until set (timeline endpoint)
	runbookService     *runbook.RunbookService         // nil until set (runbook endpoint)
	dashboardDir       string                          // path to dashboard build dir (empty = no static serving)
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

// SetInteractionService sets the interaction service for trace endpoints.
func (s *Server) SetInteractionService(svc *services.InteractionService) {
	s.interactionService = svc
}

// SetStageService sets the stage service for trace endpoints.
func (s *Server) SetStageService(svc *services.StageService) {
	s.stageService = svc
}

// SetTimelineService sets the timeline service for the timeline endpoint.
func (s *Server) SetTimelineService(svc *services.TimelineService) {
	s.timelineService = svc
}

// SetRunbookService sets the runbook service for the runbook listing endpoint.
func (s *Server) SetRunbookService(rs *runbook.RunbookService) {
	s.runbookService = rs
}

// SetDashboardDir sets the path to the dashboard build directory and
// registers static file serving routes. When set and the directory
// contains an index.html, assets are served from /assets/* and a SPA
// fallback is registered for all non-API routes.
//
// Must be called after NewServer (which registers API routes first)
// so that API routes take priority over the wildcard SPA fallback.
func (s *Server) SetDashboardDir(dir string) {
	s.dashboardDir = dir
	s.setupDashboardRoutes()
}

// ValidateWiring checks that all required services have been wired via their
// Set* methods. Call this after all Set* calls and before Start/StartWithListener.
// Returns an error listing every missing service so that wiring gaps are caught
// at startup rather than surfacing as 503s at request time.
//
// Services that are legitimately optional (e.g. healthMonitor / warningService
// when MCP is disabled) are NOT checked here.
func (s *Server) ValidateWiring() error {
	var errs []error
	if s.chatService == nil {
		errs = append(errs, fmt.Errorf("chatService not set (call SetChatService)"))
	}
	if s.chatExecutor == nil {
		errs = append(errs, fmt.Errorf("chatExecutor not set (call SetChatExecutor)"))
	}
	if s.eventPublisher == nil {
		errs = append(errs, fmt.Errorf("eventPublisher not set (call SetEventPublisher)"))
	}
	if s.interactionService == nil {
		errs = append(errs, fmt.Errorf("interactionService not set (call SetInteractionService)"))
	}
	if s.stageService == nil {
		errs = append(errs, fmt.Errorf("stageService not set (call SetStageService)"))
	}
	if s.timelineService == nil {
		errs = append(errs, fmt.Errorf("timelineService not set (call SetTimelineService)"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("server wiring incomplete: %w", errors.Join(errs...))
	}
	return nil
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

	// Session list and filter endpoints (static paths before :id param).
	v1.GET("/sessions", s.listSessionsHandler)
	v1.GET("/sessions/active", s.activeSessionsHandler)
	v1.GET("/sessions/filter-options", s.filterOptionsHandler)

	// Session detail and actions.
	v1.GET("/sessions/:id", s.getSessionHandler)
	v1.GET("/sessions/:id/summary", s.sessionSummaryHandler)
	v1.POST("/sessions/:id/cancel", s.cancelSessionHandler)
	v1.POST("/sessions/:id/chat/messages", s.sendChatMessageHandler)
	v1.GET("/sessions/:id/timeline", s.getTimelineHandler)

	// System endpoints.
	v1.GET("/system/warnings", s.systemWarningsHandler)
	v1.GET("/system/mcp-servers", s.mcpServersHandler)
	v1.GET("/system/default-tools", s.defaultToolsHandler)
	v1.GET("/alert-types", s.alertTypesHandler)
	v1.GET("/runbooks", s.handleListRunbooks)

	// Trace/observability endpoints (two-level loading).
	v1.GET("/sessions/:id/trace", s.getTraceListHandler)
	v1.GET("/sessions/:id/trace/llm/:interaction_id", s.getLLMInteractionHandler)
	v1.GET("/sessions/:id/trace/mcp/:interaction_id", s.getMCPInteractionHandler)

	// WebSocket endpoint for real-time event streaming.
	// Moved under /api/v1 so all sensitive endpoints share a single
	// oauth2-proxy auth rule (/api/*) in Phase 9.
	// Auth deferred to Phase 9 (Security) — currently open to any client,
	// consistent with the InsecureSkipVerify origin policy in handler_ws.go.
	v1.GET("/ws", s.wsHandler)

	// Dashboard static file serving is registered via SetDashboardDir(),
	// called after NewServer. This ensures API routes (registered above)
	// take priority over the wildcard SPA fallback.
}

// setupDashboardRoutes registers static file serving for the dashboard build
// directory. When dashboardDir is set and contains an index.html, Vite-built
// assets are served from /assets/* and all other non-API paths fall back to
// index.html (SPA routing).
//
// Cache headers:
//   - /assets/* — immutable (1 year): Vite-built files include content hashes
//     in their filenames, so aggressive caching is safe.
//   - index.html and other root files — no-cache: forces browser revalidation
//     on every visit so new asset hashes are picked up after deployments.
//
// Uses os.DirFS to create an fs.FS rooted at the dashboard directory, because
// Echo v5's c.File() resolves paths against its internal Filesystem (os.DirFS("."))
// and cannot handle absolute paths. c.FileFS() with an explicit filesystem works
// correctly regardless of the dashboard directory location.
func (s *Server) setupDashboardRoutes() {
	if s.dashboardDir == "" {
		return
	}

	indexPath := filepath.Join(s.dashboardDir, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		slog.Warn("Dashboard directory set but index.html not found — skipping static serving",
			"dir", s.dashboardDir)
		return
	}

	slog.Info("Serving dashboard from disk", "dir", s.dashboardDir)

	dashFS := os.DirFS(s.dashboardDir)

	// Serve hashed Vite assets (JS, CSS, images) from /assets/ with immutable
	// caching. Filenames include content hashes so aggressive caching is safe.
	assetsFS, err := fs.Sub(dashFS, "assets")
	if err == nil {
		s.echo.GET("/assets/*", func(c *echo.Context) error {
			c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			return c.FileFS(c.Param("*"), assetsFS)
		})
	}

	// SPA fallback: all other non-API, non-health, non-ws paths serve index.html.
	// This allows React Router to handle client-side routing.
	// All responses use no-cache so browsers revalidate after deployments.
	s.echo.GET("/*", func(c *echo.Context) error {
		path := c.Request().URL.Path

		// API and health routes are handled by earlier registrations.
		// This is a safety check — shouldn't normally be reached for these.
		if strings.HasPrefix(path, "/api/") || path == "/health" {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}

		c.Response().Header().Set("Cache-Control", "no-cache")

		// Try to serve the exact file first (e.g., /favicon.ico, /robots.txt)
		relPath := strings.TrimPrefix(path, "/")
		if relPath != "" {
			if info, statErr := fs.Stat(dashFS, relPath); statErr == nil && !info.IsDir() {
				return c.FileFS(relPath, dashFS)
			}
		}

		// Fall back to index.html for SPA routing
		return c.FileFS("index.html", dashFS)
	})
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
		Version:  version.Full(),
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
