package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/codeready-toolchain/tarsy/pkg/api"
	"github.com/codeready-toolchain/tarsy/pkg/llm"
	"github.com/codeready-toolchain/tarsy/pkg/session"
	"github.com/joho/godotenv"

	"github.com/gin-contrib/cors"
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

	// Get configuration from environment (with defaults)
	grpcAddr := os.Getenv("GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = "localhost:50051"
	}

	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "8080"
	}

	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = "debug"
	}
	gin.SetMode(ginMode)

	log.Printf("Starting TARSy Go Orchestrator")
	log.Printf("gRPC LLM Service: %s", grpcAddr)
	log.Printf("HTTP Port: %s", httpPort)
	log.Printf("Gin Mode: %s", ginMode)

	// Initialize components
	sessionMgr := session.NewManager()
	log.Println("Initialized session manager")

	llmClient, err := llm.NewClient(grpcAddr)
	if err != nil {
		log.Fatalf("Failed to create LLM client: %v", err)
	}
	defer llmClient.Close()
	log.Println("Connected to LLM service")

	// Initialize WebSocket hub
	wsHub := api.NewWSHub()
	go wsHub.Run()
	log.Println("Started WebSocket hub")

	// Create API server
	server := api.NewServer(sessionMgr, llmClient, wsHub)

	// Setup Gin router
	router := gin.Default()

	// CORS for PoC (allow all origins)
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// Health check
	router.GET("/health", server.Health)

	// API routes
	apiGroup := router.Group("/api")
	{
		apiGroup.POST("/alerts", server.CreateAlert)
		apiGroup.GET("/sessions", server.ListSessions)
		apiGroup.GET("/sessions/:id", server.GetSession)
	}

	// WebSocket endpoint
	router.GET("/ws", gin.WrapF(wsHub.HandleWS))

	// Static files (will serve dashboard)
	router.Static("/static", "./dashboard")
	router.StaticFile("/", "./dashboard/index.html")

	// Start server
	log.Printf("HTTP server listening on :%s", httpPort)
	if err := router.Run(":" + httpPort); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
