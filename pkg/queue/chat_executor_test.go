package queue

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────
// Submit tests (unit-level, using real DB for one-at-a-time enforcement)
// ────────────────────────────────────────────────────────────

func TestChatMessageExecutor_Submit_RejectsWhenStopped(t *testing.T) {
	executor := &ChatMessageExecutor{
		stopped:     true,
		activeExecs: make(map[string]context.CancelFunc),
	}

	_, err := executor.Submit(context.Background(), ChatExecuteInput{
		Chat:    &ent.Chat{ID: "chat-1"},
		Message: &ent.ChatUserMessage{ID: "msg-1"},
		Session: &ent.AlertSession{ID: "session-1"},
	})
	assert.ErrorIs(t, err, ErrShuttingDown)
}

func TestChatMessageExecutor_Submit_RejectsActiveExecution(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	// Create session and chat
	chatService := services.NewChatService(client.Client)
	stageService := services.NewStageService(client.Client)

	session := createChatTestSession(t, client.Client)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	// Create an active stage for the chat
	chatID := chat.ID
	_, err = stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Chat Response",
		StageIndex:         1,
		ExpectedAgentCount: 1,
		ChatID:             &chatID,
	})
	require.NoError(t, err)

	// Create executor with only the stageService (enough for Submit's active check)
	executor := &ChatMessageExecutor{
		stageService: stageService,
		activeExecs:  make(map[string]context.CancelFunc),
	}

	_, err = executor.Submit(ctx, ChatExecuteInput{
		Chat:    chat,
		Message: &ent.ChatUserMessage{ID: "msg-1"},
		Session: session,
	})
	assert.ErrorIs(t, err, ErrChatExecutionActive)
}

func TestChatMessageExecutor_Submit_AllowsWhenNoActiveExecution(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	chatService := services.NewChatService(client.Client)
	stageService := services.NewStageService(client.Client)

	session := createChatTestSession(t, client.Client)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	// Create a completed stage (should not block)
	chatID := chat.ID
	completedStage, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Chat Response",
		StageIndex:         1,
		ExpectedAgentCount: 1,
		ChatID:             &chatID,
	})
	require.NoError(t, err)

	err = client.Stage.UpdateOneID(completedStage.ID).
		SetStatus(stage.StatusCompleted).
		Exec(ctx)
	require.NoError(t, err)

	// Create a real chat user message (FK constraint requires it)
	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chat.ID,
		Content: "What happened?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Now submit should succeed in creating the stage (execute will be a no-op goroutine
	// that fails on config resolution, which is fine for this test)
	executor := &ChatMessageExecutor{
		stageService: stageService,
		activeExecs:  make(map[string]context.CancelFunc),
		// cfg is nil — the goroutine will fail early, but Submit should return stageID
		cfg:             stubConfig(),
		timelineService: services.NewTimelineService(client.Client),
	}

	stageID, err := executor.Submit(ctx, ChatExecuteInput{
		Chat:    chat,
		Message: msg,
		Session: session,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, stageID)

	// Wait for the background goroutine to finish
	executor.wg.Wait()
}

// ────────────────────────────────────────────────────────────
// Cancellation tests
// ────────────────────────────────────────────────────────────

func TestChatMessageExecutor_CancelExecution(t *testing.T) {
	executor := &ChatMessageExecutor{
		activeExecs: make(map[string]context.CancelFunc),
	}

	// Register a mock execution
	ctx, cancel := context.WithCancel(context.Background())
	executor.registerExecution("chat-1", cancel)

	// Cancel should succeed
	assert.True(t, executor.CancelExecution("chat-1"))
	assert.Error(t, ctx.Err()) // context should be cancelled

	// Cancel unknown chat should return false
	assert.False(t, executor.CancelExecution("unknown"))
}

func TestChatMessageExecutor_Stop(t *testing.T) {
	executor := &ChatMessageExecutor{
		activeExecs: make(map[string]context.CancelFunc),
	}

	// Register a mock execution
	ctx, cancel := context.WithCancel(context.Background())
	executor.registerExecution("chat-1", cancel)

	// Track a goroutine
	executor.wg.Add(1)
	go func() {
		defer executor.wg.Done()
		<-ctx.Done() // Wait for cancellation
	}()

	// Stop should cancel all and wait
	done := make(chan struct{})
	go func() {
		executor.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success - Stop completed
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not complete in time")
	}

	assert.True(t, executor.stopped)
	assert.Error(t, ctx.Err())
}

// ────────────────────────────────────────────────────────────
// Heartbeat test
// ────────────────────────────────────────────────────────────

func TestChatMessageExecutor_Heartbeat(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	chatService := services.NewChatService(client.Client)

	session := createChatTestSession(t, client.Client)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	executor := &ChatMessageExecutor{
		dbClient:   client.Client,
		execConfig: ChatMessageExecutorConfig{HeartbeatInterval: 100 * time.Millisecond},
	}

	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)

	go executor.runChatHeartbeat(heartbeatCtx, chat.ID)

	// Wait for at least one heartbeat
	time.Sleep(250 * time.Millisecond)
	cancelHeartbeat()

	// Verify last_interaction_at was updated
	updated, err := client.Chat.Get(ctx, chat.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.LastInteractionAt)
}

// ────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────

// stubConfig returns a minimal config that has a chain with a ChatAgent.
// The chain won't actually be found by a real GetChain call, but prevents
// nil panics in Submit's goroutine early-exit paths.
func stubConfig() *config.Config {
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test",
			IterationStrategy: "react",
		},
		ChainRegistry:       config.NewChainRegistry(nil),
		AgentRegistry:       config.NewAgentRegistry(nil),
		LLMProviderRegistry: config.NewLLMProviderRegistry(nil),
	}
}

// createChatTestSession creates a minimal session for chat executor tests.
func createChatTestSession(t *testing.T, client *ent.Client) *ent.AlertSession {
	t.Helper()
	sessionID := uuid.New().String()
	session, err := client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData("test alert").
		SetAgentType("kubernetes").
		SetAlertType("kubernetes").
		SetChainID("k8s-analysis").
		SetStatus(alertsession.StatusCompleted).
		Save(context.Background())
	require.NoError(t, err)
	return session
}
