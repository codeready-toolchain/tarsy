package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/joho/godotenv"

	"github.com/gin-gonic/gin"
)

func main() {
	// Load .env file from deploy directory
	envPath := filepath.Join("deploy", ".env")
	if err := godotenv.Load(envPath); err != nil {
		log.Printf("Warning: Could not load %s file: %v", envPath, err)
		log.Printf("Continuing with existing environment variables...")
	} else {
		log.Printf("Loaded configuration from %s", envPath)
	}

	// Get HTTP port from environment (with default)
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = "debug"
	}
	gin.SetMode(ginMode)

	log.Printf("Starting TARSy - Phase 2.1: Schema & Migrations")
	log.Printf("HTTP Port: %s", httpPort)

	// Initialize database
	dbConfig, err := database.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("Failed to load database config: %v", err)
	}

	ctx := context.Background()
	dbClient, err := database.NewClient(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbClient.Close()
	log.Println("✓ Connected to PostgreSQL database")
	log.Println("✓ Database schema initialized")

	// Setup minimal Gin router
	router := gin.Default()

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":   "healthy",
			"database": "connected",
			"phase":    "2.1 - Schema & Migrations Complete",
		})
	})

	// Start server
	log.Printf("HTTP server listening on :%s", httpPort)
	log.Printf("Health check available at: http://localhost:%s/health", httpPort)
	if err := router.Run(":" + httpPort); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
