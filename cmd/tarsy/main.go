// TARSy orchestrator server - provides HTTP/WebSocket API and manages LLM interactions.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/joho/godotenv"

	"github.com/gin-gonic/gin"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
		log.Printf("Warning: Could not load %s file: %v", envPath, err)
		log.Printf("Continuing with existing environment variables...")
	} else {
		log.Printf("Loaded environment from %s", envPath)
	}

	// Get HTTP port from environment (with default)
	httpPort := getEnv("HTTP_PORT", "8080")
	ginMode := getEnv("GIN_MODE", "debug")
	gin.SetMode(ginMode)

	log.Printf("Starting TARSy")
	log.Printf("HTTP Port: %s", httpPort)
	log.Printf("Config Directory: %s", *configDir)

	ctx := context.Background()

	// Initialize configuration system (NEW)
	cfg, err := config.Initialize(ctx, *configDir)
	if err != nil {
		log.Fatalf("Failed to initialize configuration: %v", err)
	}
	stats := cfg.Stats() // For health check endpoint

	// Initialize database
	dbConfig, err := database.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("Failed to load database config: %v", err)
	}

	dbClient, err := database.NewClient(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer func() {
		if err := dbClient.Close(); err != nil {
			log.Printf("Error closing database client: %v", err)
		}
	}()
	log.Println("✓ Connected to PostgreSQL database")
	log.Println("✓ Database schema initialized")

	// Initialize services with configuration (UPDATED)
	sessionService := services.NewSessionService(dbClient.Client, cfg.ChainRegistry, cfg.MCPServerRegistry)
	stageService := services.NewStageService(dbClient.Client)
	messageService := services.NewMessageService(dbClient.Client)
	timelineService := services.NewTimelineService(dbClient.Client)
	interactionService := services.NewInteractionService(dbClient.Client, messageService)
	eventService := services.NewEventService(dbClient.Client)
	chatService := services.NewChatService(dbClient.Client)

	// Mark as used (will be passed to API handlers in Phase 3)
	_ = sessionService
	_ = stageService
	_ = timelineService
	_ = interactionService
	_ = eventService
	_ = chatService

	log.Println("✓ Services initialized")

	// Setup minimal Gin router
	router := gin.Default()

	// Health check endpoint using services
	router.GET("/health", func(c *gin.Context) {
		reqCtx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		dbHealth, err := database.Health(reqCtx, dbClient.DB())
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
			"phase":    "2.2 - Configuration System Complete",
			"services": gin.H{
				"session":     "ready",
				"stage":       "ready",
				"message":     "ready",
				"timeline":    "ready",
				"interaction": "ready",
				"event":       "ready",
				"chat":        "ready",
			},
			"configuration": gin.H{
				"agents":        stats.Agents,
				"chains":        stats.Chains,
				"mcp_servers":   stats.MCPServers,
				"llm_providers": stats.LLMProviders,
			},
		})
	})

	// Start server
	log.Printf("HTTP server listening on :%s", httpPort)
	log.Printf("Health check available at: http://localhost:%s/health", httpPort)
	if err := router.Run(":" + httpPort); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
