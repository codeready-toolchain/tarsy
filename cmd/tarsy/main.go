// TARSy orchestrator server — provides HTTP API, manages queue workers,
// and orchestrates session processing.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/codeready-toolchain/tarsy/pkg/api"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
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

func main() {
	// Parse command-line flags
	configDir := flag.String("config-dir",
		getEnv("CONFIG_DIR", "./deploy/config"),
		"Path to configuration directory")
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

	// 4. Initialize services
	alertService := services.NewAlertService(dbClient.Client, cfg.ChainRegistry, cfg.Defaults)
	sessionService := services.NewSessionService(dbClient.Client, cfg.ChainRegistry, cfg.MCPServerRegistry)
	slog.Info("Services initialized")

	// 5. Create stub session executor (replaced by real executor in Phase 3)
	executor := queue.NewStubExecutor()

	// 6. Start worker pool (before HTTP server)
	workerPool := queue.NewWorkerPool(podID, dbClient.Client, cfg.Queue, executor)
	if err := workerPool.Start(ctx); err != nil {
		slog.Error("Failed to start worker pool", "error", err)
		os.Exit(1)
	}

	// 7. Create HTTP server
	httpServer := api.NewServer(cfg, dbClient, alertService, sessionService, workerPool)

	// 8. Start HTTP server (non-blocking)
	go func() {
		addr := ":" + httpPort
		slog.Info("HTTP server listening", "addr", addr)
		if err := httpServer.Start(addr); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	slog.Info("TARSy started successfully",
		"pod_id", podID,
		"workers", cfg.Queue.WorkerCount)

	// 9. Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("Shutdown signal received", "signal", sig)

	// 10. Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, cfg.Queue.GracefulShutdownTimeout)
	defer cancel()

	// Stop worker pool first (wait for active sessions to complete)
	done := make(chan struct{})
	go func() {
		workerPool.Stop()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("Worker pool stopped gracefully")
	case <-shutdownCtx.Done():
		slog.Warn("Shutdown timeout exceeded — incomplete sessions will be orphan-recovered")
	}

	// Stop HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	slog.Info("Shutdown complete")
}
