// Package e2e provides end-to-end test infrastructure for the tarsy pipeline.
package e2e

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/api"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	tarsyslack "github.com/codeready-toolchain/tarsy/pkg/slack"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/codeready-toolchain/tarsy/test/util"
)

// TestApp boots a complete TARSy instance for e2e testing.
type TestApp struct {
	// Core
	Config    *config.Config
	DBClient  *database.Client
	EntClient *ent.Client

	// Mocks / test wiring
	LLMClient  *ScriptedLLMClient
	MCPFactory *mcp.ClientFactory // real factory backed by in-memory MCP SDK servers

	// Real infrastructure
	EventPublisher *events.EventPublisher
	ConnManager    *events.ConnectionManager
	NotifyListener *events.NotifyListener
	WorkerPool     *queue.WorkerPool
	ChatExecutor   *queue.ChatMessageExecutor
	Server         *api.Server

	// Runtime
	BaseURL string // e.g. "http://127.0.0.1:54321"
	WSURL   string // e.g. "ws://127.0.0.1:54321/ws"

	t *testing.T
}

// testAppConfig holds options accumulated before creating the TestApp.
type testAppConfig struct {
	cfg                   *config.Config
	llmClient             *ScriptedLLMClient
	mcpServers            map[string]map[string]mcpsdk.ToolHandler
	workerCount           int
	maxConcurrentSessions int
	sessionTimeout        time.Duration
	chatTimeout           time.Duration
	dbClient              *database.Client    // injected DB client (for multi-replica tests)
	podID                 string              // custom pod ID (for multi-replica tests)
	slackService          *tarsyslack.Service // optional Slack service (for Slack notification tests)
}

// TestAppOption configures the test app.
type TestAppOption func(*testAppConfig)

// WithConfig sets a custom config.
func WithConfig(cfg *config.Config) TestAppOption {
	return func(c *testAppConfig) { c.cfg = cfg }
}

// WithLLMClient sets a pre-scripted LLM client.
func WithLLMClient(client *ScriptedLLMClient) TestAppOption {
	return func(c *testAppConfig) { c.llmClient = client }
}

// WithMCPServers sets in-memory MCP SDK servers.
// Maps serverID → (toolName → handler).
func WithMCPServers(servers map[string]map[string]mcpsdk.ToolHandler) TestAppOption {
	return func(c *testAppConfig) { c.mcpServers = servers }
}

// WithWorkerCount sets the number of worker pool goroutines.
func WithWorkerCount(n int) TestAppOption {
	return func(c *testAppConfig) { c.workerCount = n }
}

// WithMaxConcurrentSessions sets the maximum number of concurrently executing sessions.
func WithMaxConcurrentSessions(n int) TestAppOption {
	return func(c *testAppConfig) { c.maxConcurrentSessions = n }
}

// WithSessionTimeout sets the timeout for investigation session execution.
func WithSessionTimeout(d time.Duration) TestAppOption {
	return func(c *testAppConfig) { c.sessionTimeout = d }
}

// WithChatTimeout sets the timeout for chat message execution.
func WithChatTimeout(d time.Duration) TestAppOption {
	return func(c *testAppConfig) { c.chatTimeout = d }
}

// WithDBClient injects a pre-created database client, skipping the default
// per-test schema creation. Used for multi-replica tests where multiple
// TestApp instances share the same database schema.
func WithDBClient(client *database.Client) TestAppOption {
	return func(c *testAppConfig) { c.dbClient = client }
}

// WithPodID overrides the auto-generated pod ID. Required for multi-replica
// tests so each replica gets a distinct identity for worker claiming and
// orphan detection.
func WithPodID(id string) TestAppOption {
	return func(c *testAppConfig) { c.podID = id }
}

// WithSlackService injects a Slack notification service into the worker pool.
// Used for testing Slack integration with a mock API server.
func WithSlackService(svc *tarsyslack.Service) TestAppOption {
	return func(c *testAppConfig) { c.slackService = svc }
}

// NewTestApp creates and starts a full TARSy test instance.
// Shutdown is registered via t.Cleanup automatically.
func NewTestApp(t *testing.T, opts ...TestAppOption) *TestApp {
	t.Helper()

	// Apply options.
	tc := &testAppConfig{
		workerCount:    1,
		sessionTimeout: 30 * time.Second,
		chatTimeout:    30 * time.Second,
	}
	for _, opt := range opts {
		opt(tc)
	}
	if tc.maxConcurrentSessions == 0 {
		tc.maxConcurrentSessions = tc.workerCount
	}

	// Default config if not provided.
	if tc.cfg == nil {
		tc.cfg = defaultTestConfig()
	}

	// Ensure QueueConfig exists with test-appropriate settings.
	if tc.cfg.Queue == nil {
		tc.cfg.Queue = &config.QueueConfig{}
	}
	tc.cfg.Queue.WorkerCount = tc.workerCount
	tc.cfg.Queue.MaxConcurrentSessions = tc.maxConcurrentSessions
	tc.cfg.Queue.PollInterval = 100 * time.Millisecond
	tc.cfg.Queue.PollIntervalJitter = 50 * time.Millisecond
	tc.cfg.Queue.SessionTimeout = tc.sessionTimeout
	tc.cfg.Queue.HeartbeatInterval = 5 * time.Second
	tc.cfg.Queue.GracefulShutdownTimeout = 10 * time.Second
	tc.cfg.Queue.OrphanDetectionInterval = 1 * time.Minute
	tc.cfg.Queue.OrphanThreshold = 1 * time.Minute

	// Default LLM client if not provided.
	if tc.llmClient == nil {
		tc.llmClient = NewScriptedLLMClient()
	}

	// 1. Database — need both *database.Client (for API server) and *ent.Client (for executors).
	var dbClient *database.Client
	if tc.dbClient != nil {
		dbClient = tc.dbClient
	} else {
		dbClient = testdb.NewTestClient(t)
	}
	entClient := dbClient.Client

	// 2. Event publishing — real, backed by test DB.
	eventPublisher := events.NewEventPublisher(dbClient.DB())

	// 3. Streaming infrastructure.
	eventService := services.NewEventService(entClient)
	adapter := events.NewEventServiceAdapter(eventService)
	connManager := events.NewConnectionManager(adapter, 5*time.Second)

	// 4. NotifyListener — real, dedicated pgx connection.
	baseConnStr := util.GetBaseConnectionString(t)
	notifyListener := events.NewNotifyListener(baseConnStr, connManager)
	ctx := context.Background()
	require.NoError(t, notifyListener.Start(ctx))
	connManager.SetListener(notifyListener)

	// 5. MCP — in-memory servers if configured.
	var mcpFactory *mcp.ClientFactory
	if len(tc.mcpServers) > 0 {
		mcpFactory = SetupInMemoryMCP(t, tc.mcpServers)
	}

	// 6. Domain services.
	alertService := services.NewAlertService(entClient, tc.cfg.ChainRegistry, tc.cfg.Defaults, nil)
	sessionService := services.NewSessionService(entClient, tc.cfg.ChainRegistry, tc.cfg.MCPServerRegistry)
	chatService := services.NewChatService(entClient)

	// 7. RunbookService (nil config/token → uses defaults).
	runbookService := runbook.NewService(tc.cfg.Runbooks, "", tc.cfg.Defaults.Runbook)

	// 8. Session executor.
	sessionExecutor := queue.NewRealSessionExecutor(tc.cfg, entClient, tc.llmClient, eventPublisher, mcpFactory, runbookService)

	// 9. Worker pool.
	podID := tc.podID
	if podID == "" {
		podID = fmt.Sprintf("e2e-test-%s", t.Name())
	}
	workerPool := queue.NewWorkerPool(podID, entClient, tc.cfg.Queue, sessionExecutor, eventPublisher, tc.slackService)
	require.NoError(t, workerPool.Start(ctx))

	// 10. Chat executor.
	chatExecutor := queue.NewChatMessageExecutor(
		tc.cfg, entClient, tc.llmClient, mcpFactory, eventPublisher,
		queue.ChatMessageExecutorConfig{
			SessionTimeout:    tc.chatTimeout,
			HeartbeatInterval: tc.cfg.Queue.HeartbeatInterval,
		},
		runbookService,
	)

	// 11. HTTP server on random port.
	server := api.NewServer(tc.cfg, dbClient, alertService, sessionService, workerPool, connManager)
	server.SetChatService(chatService)
	server.SetChatExecutor(chatExecutor)
	server.SetEventPublisher(eventPublisher)

	// Trace/observability and timeline endpoints.
	messageService := services.NewMessageService(entClient)
	interactionService := services.NewInteractionService(entClient, messageService)
	stageService := services.NewStageService(entClient)
	timelineService := services.NewTimelineService(entClient)
	server.SetInteractionService(interactionService)
	server.SetStageService(stageService)
	server.SetTimelineService(timelineService)

	require.NoError(t, server.ValidateWiring(), "server wiring incomplete — did you forget a Set* call?")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = server.StartWithListener(ln)
	}()

	addr := ln.Addr().String()
	baseURL := fmt.Sprintf("http://%s", addr)
	wsURL := fmt.Sprintf("ws://%s/api/v1/ws", addr)

	app := &TestApp{
		Config:         tc.cfg,
		DBClient:       dbClient,
		EntClient:      entClient,
		LLMClient:      tc.llmClient,
		MCPFactory:     mcpFactory,
		EventPublisher: eventPublisher,
		ConnManager:    connManager,
		NotifyListener: notifyListener,
		WorkerPool:     workerPool,
		ChatExecutor:   chatExecutor,
		Server:         server,
		BaseURL:        baseURL,
		WSURL:          wsURL,
		t:              t,
	}

	// Register cleanup in reverse-creation order.
	t.Cleanup(func() {
		chatExecutor.Stop()
		workerPool.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		notifyListener.Stop(context.Background())
		// DB cleanup handled by testdb.NewTestClient/SetupTestDatabase
	})

	return app
}

// defaultTestConfig creates a minimal config suitable for tests that don't
// provide their own. Tests typically override this via WithConfig.
func defaultTestConfig() *config.Config {
	maxIter := 5
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:   "test-provider",
			LLMBackend:    config.LLMBackendNativeGemini,
			MaxIterations: &maxIter,
		},
		AgentRegistry:       config.NewAgentRegistry(nil),
		ChainRegistry:       config.NewChainRegistry(nil),
		MCPServerRegistry:   config.NewMCPServerRegistry(nil),
		LLMProviderRegistry: config.NewLLMProviderRegistry(nil),
	}
}
