// TARSy orchestrator server — provides HTTP API, manages queue workers,
// and orchestrates session processing.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/api"
	"github.com/codeready-toolchain/tarsy/pkg/cleanup"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	tarsyslack "github.com/codeready-toolchain/tarsy/pkg/slack"
	"github.com/joho/godotenv"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// resolvePodID determines the pod identifier for multi-replica coordination.
// Priority: POD_ID env > HOSTNAME env > "local"
func resolvePodID() string {
	if id := os.Getenv("POD_ID"); id != "" {
		return id
	}
	if hostname := os.Getenv("HOSTNAME"); hostname != "" {
		return hostname
	}
	return "local"
}

func configureLogging() {
	level := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	switch getEnv("LOG_FORMAT", "text") {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	configureLogging()

	// Parse command-line flags
	configDir := flag.String("config-dir",
		getEnv("CONFIG_DIR", "./deploy/config"),
		"Path to configuration directory")
	dashboardDir := flag.String("dashboard-dir",
		getEnv("DASHBOARD_DIR", ""),
		"Path to dashboard build directory (e.g. web/dashboard/dist). Empty = no static serving")
	flag.Parse()

	// Load .env file from config directory
	envPath := filepath.Join(*configDir, ".env")
	if err := godotenv.Load(envPath); err != nil {
		slog.Warn("Could not load .env file, continuing with existing environment",
			"path", envPath, "error", err)
	} else {
		slog.Info("Loaded environment", "path", envPath)
	}

	httpPort := getEnv("HTTP_PORT", "8080")
	podID := resolvePodID()

	slog.Info("Starting TARSy",
		"http_port", httpPort,
		"pod_id", podID,
		"config_dir", *configDir)

	ctx := context.Background()

	// 1. Initialize configuration
	cfg, err := config.Initialize(ctx, *configDir)
	if err != nil {
		slog.Error("Failed to initialize configuration", "error", err)
		os.Exit(1)
	}

	// 2. Initialize database
	dbConfig, err := database.LoadConfigFromEnv()
	if err != nil {
		slog.Error("Failed to load database config", "error", err)
		os.Exit(1)
	}

	dbClient, err := database.NewClient(ctx, dbConfig)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := dbClient.Close(); err != nil {
			slog.Error("Error closing database client", "error", err)
		}
	}()
	slog.Info("Connected to PostgreSQL database")

	// 3. One-time startup orphan cleanup
	if err := queue.CleanupStartupOrphans(ctx, dbClient.Client, podID); err != nil {
		slog.Error("Failed to cleanup startup orphans", "error", err)
		// Non-fatal — continue
	}

	// 4. Initialize masking service and domain services
	maskingService := masking.NewService(
		cfg.MCPServerRegistry,
		masking.AlertMaskingConfig{
			Enabled:      cfg.Defaults.AlertMasking.Enabled,
			PatternGroup: cfg.Defaults.AlertMasking.PatternGroup,
		},
	)

	alertService := services.NewAlertService(dbClient.Client, cfg.ChainRegistry, cfg.Defaults, maskingService)
	sessionService := services.NewSessionService(dbClient.Client, cfg.ChainRegistry, cfg.MCPServerRegistry)
	slog.Info("Services initialized")

	// 4a. Start cleanup service (retention + event TTL)
	eventService := services.NewEventService(dbClient.Client)
	cleanupService := cleanup.NewService(cfg.Retention, sessionService, eventService)
	cleanupService.Start(ctx)
	defer cleanupService.Stop()

	// 5. Create LLM client and session executor
	// Note: grpc.NewClient uses lazy dialing; actual connection happens on first RPC call
	llmAddr := getEnv("LLM_SERVICE_ADDR", "localhost:50051")
	llmClient, err := agent.NewGRPCLLMClient(llmAddr)
	if err != nil {
		slog.Error("Failed to initialize LLM client", "addr", llmAddr, "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := llmClient.Close(); err != nil {
			slog.Error("Error closing LLM client", "error", err)
		}
	}()
	slog.Info("LLM client initialized", "addr", llmAddr)

	// 5a. Initialize streaming infrastructure
	eventPublisher := events.NewEventPublisher(dbClient.DB())
	catchupQuerier := events.NewEventServiceAdapter(eventService)
	connManager := events.NewConnectionManager(catchupQuerier, 10*time.Second)

	// Start NotifyListener (dedicated pgx connection for LISTEN)
	notifyListener := events.NewNotifyListener(dbConfig.DSN(), connManager)
	if err := notifyListener.Start(ctx); err != nil {
		slog.Error("Failed to start NotifyListener", "error", err)
		os.Exit(1)
	}
	defer notifyListener.Stop(ctx)

	// Wire listener ↔ manager bidirectional link
	connManager.SetListener(notifyListener)
	slog.Info("Streaming infrastructure initialized")

	// Subscribe to the cancellations channel for cross-pod session cancellation.
	// The handler is registered later (after workerPool and chatExecutor are created)
	// because it depends on them. The subscription itself is safe to set up early.
	if err := notifyListener.Subscribe(ctx, events.CancellationsChannel); err != nil {
		slog.Error("Failed to subscribe to cancellations channel", "error", err)
		os.Exit(1)
	}

	// 5b. Initialize MCP infrastructure
	warningsService := services.NewSystemWarningsService()
	mcpFactory := mcp.NewClientFactory(cfg.MCPServerRegistry, maskingService)

	// MCP startup validation: attempt to connect to all configured servers.
	// Failures are non-fatal — TARSy starts degraded with warnings visible
	// on the dashboard. The HealthMonitor handles recovery and warning cleanup.
	mcpServerIDs := cfg.AllMCPServerIDs()
	if len(mcpServerIDs) > 0 {
		validationClient, err := mcpFactory.CreateClient(ctx, mcpServerIDs)
		if err != nil {
			slog.Warn("MCP client creation failed — starting degraded", "error", err)
		} else {
			failed := validationClient.FailedServers()
			if len(failed) > 0 {
				slog.Warn("MCP servers failed startup validation — starting degraded", "failed_servers", failed)
				for serverID, errMsg := range failed {
					warningsService.AddWarning("mcp_health",
						fmt.Sprintf("MCP server %q unreachable at startup: %s", serverID, errMsg),
						"Server will be retried by the health monitor.", serverID)
				}
			} else {
				slog.Info("MCP servers validated", "count", len(mcpServerIDs))
			}
			_ = validationClient.Close()
		}
	}

	// Start HealthMonitor (background goroutine)
	var healthMonitor *mcp.HealthMonitor
	if len(mcpServerIDs) > 0 {
		healthMonitor = mcp.NewHealthMonitor(mcpFactory, cfg.MCPServerRegistry, warningsService)
		healthMonitor.Start(ctx)
		defer healthMonitor.Stop()
		slog.Info("MCP health monitor started")
	}

	// 5c. Create RunbookService
	tokenEnv := "GITHUB_TOKEN"
	if cfg.GitHub != nil && cfg.GitHub.TokenEnv != "" {
		tokenEnv = cfg.GitHub.TokenEnv
	}
	githubToken := os.Getenv(tokenEnv)
	runbookService := runbook.NewService(cfg.Runbooks, githubToken, cfg.Defaults.Runbook)

	if githubToken == "" && cfg.Runbooks != nil && cfg.Runbooks.RepoURL != "" {
		warningsService.AddWarning("runbook", "GitHub token not configured",
			"Set "+tokenEnv+" to access private repos. URL-based runbooks will fall back to default.", "")
	}

	// 5d. Create Slack notification service (optional)
	var slackService *tarsyslack.Service
	if cfg.Slack != nil && cfg.Slack.Enabled {
		slackToken := os.Getenv(cfg.Slack.TokenEnv)
		slackService = tarsyslack.NewService(tarsyslack.ServiceConfig{
			Token:        slackToken,
			Channel:      cfg.Slack.Channel,
			DashboardURL: cfg.DashboardURL,
		})
		if slackToken == "" {
			warningsService.AddWarning("slack", "Slack bot token not configured",
				"Set "+cfg.Slack.TokenEnv+" to enable Slack notifications.", "")
		} else {
			slog.Info("Slack notifications enabled", "channel", cfg.Slack.Channel)
		}
	} else {
		slog.Info("Slack notifications disabled")
	}

	executor := queue.NewRealSessionExecutor(cfg, dbClient.Client, llmClient, eventPublisher, mcpFactory, runbookService)

	// 6. Start worker pool (before HTTP server)
	workerPool := queue.NewWorkerPool(podID, dbClient.Client, cfg.Queue, executor, eventPublisher, slackService)
	if err := workerPool.Start(ctx); err != nil {
		slog.Error("Failed to start worker pool", "error", err)
		os.Exit(1)
	}

	// 6a. Create chat message executor (for follow-up chat processing)
	chatService := services.NewChatService(dbClient.Client)
	chatExecutor := queue.NewChatMessageExecutor(
		cfg, dbClient.Client, llmClient, mcpFactory, eventPublisher,
		queue.ChatMessageExecutorConfig{
			SessionTimeout:    cfg.Queue.SessionTimeout,
			HeartbeatInterval: cfg.Queue.HeartbeatInterval,
		},
		runbookService,
	)
	slog.Info("Chat message executor initialized")

	// 6b. Register cross-pod cancellation handler.
	// When any pod publishes a cancel NOTIFY, every pod (including the sender)
	// attempts a local cancel. The owning pod will find the session and cancel it.
	notifyListener.RegisterHandler(events.CancellationsChannel, func(payload []byte) {
		sessionID := string(payload)
		workerPool.CancelSession(sessionID)
		chatExecutor.CancelBySessionID(context.Background(), sessionID)
	})
	slog.Info("Cross-pod cancellation handler registered")

	// 7. Create HTTP server
	httpServer := api.NewServer(cfg, dbClient, alertService, sessionService, workerPool, connManager)
	if healthMonitor != nil {
		httpServer.SetHealthMonitor(healthMonitor)
	}
	httpServer.SetWarningsService(warningsService)
	httpServer.SetChatService(chatService)
	httpServer.SetChatExecutor(chatExecutor)
	httpServer.SetEventPublisher(eventPublisher)
	httpServer.SetCancelNotifier(eventPublisher)
	httpServer.SetRunbookService(runbookService)

	// 7a. Wire trace and timeline endpoints.
	messageService := services.NewMessageService(dbClient.Client)
	interactionService := services.NewInteractionService(dbClient.Client, messageService)
	stageService := services.NewStageService(dbClient.Client)
	timelineService := services.NewTimelineService(dbClient.Client)
	httpServer.SetInteractionService(interactionService)
	httpServer.SetStageService(stageService)
	httpServer.SetTimelineService(timelineService)

	// 7b. Wire dashboard static file serving (optional).
	if *dashboardDir != "" {
		httpServer.SetDashboardDir(*dashboardDir)
		slog.Info("Dashboard directory configured", "dir", *dashboardDir)
	}

	// 8. Validate wiring and start HTTP server (non-blocking)
	if err := httpServer.ValidateWiring(); err != nil {
		slog.Error("HTTP server wiring incomplete", "error", err)
		os.Exit(1)
	}

	errCh := make(chan error, 1)
	go func() {
		addr := ":" + httpPort
		slog.Info("HTTP server listening", "addr", addr)
		if err := httpServer.Start(addr); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			errCh <- err
		}
	}()

	slog.Info("TARSy started successfully",
		"pod_id", podID,
		"workers", cfg.Queue.WorkerCount)

	// 9. Wait for shutdown signal or server error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		slog.Info("Shutdown signal received", "signal", sig)
	case err := <-errCh:
		slog.Error("Server error triggered shutdown", "error", err)
	}

	// 10. Graceful shutdown
	workerShutdownCtx, workerCancel := context.WithTimeout(ctx, cfg.Queue.GracefulShutdownTimeout)
	defer workerCancel()

	// Stop chat executor first (chat executions are lighter, shorter)
	chatDone := make(chan struct{})
	go func() {
		chatExecutor.Stop()
		close(chatDone)
	}()

	select {
	case <-chatDone:
		slog.Info("Chat executor stopped gracefully")
	case <-workerShutdownCtx.Done():
		slog.Warn("Chat executor shutdown timeout exceeded")
	}

	// Stop worker pool (wait for active sessions to complete)
	done := make(chan struct{})
	go func() {
		workerPool.Stop()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("Worker pool stopped gracefully")
	case <-workerShutdownCtx.Done():
		slog.Warn("Shutdown timeout exceeded — incomplete sessions will be orphan-recovered")
	}

	// Stop HTTP server with its own timeout budget
	httpShutdownCtx, httpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer httpCancel()
	if err := httpServer.Shutdown(httpShutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("Shutdown complete")
}
