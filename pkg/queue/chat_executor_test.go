package queue

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
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
	defer cancelHeartbeat()

	go executor.runChatHeartbeat(heartbeatCtx, chat.ID)

	// Poll for heartbeat update instead of fixed sleep (resilient under CI load)
	require.Eventually(t, func() bool {
		updated, err := client.Chat.Get(ctx, chat.ID)
		if err != nil {
			return false
		}
		return updated.LastInteractionAt != nil
	}, 2*time.Second, 50*time.Millisecond, "heartbeat did not update last_interaction_at")
}

// ────────────────────────────────────────────────────────────
// mapChatAgentStatus tests
// ────────────────────────────────────────────────────────────

func TestMapChatAgentStatus(t *testing.T) {
	tests := []struct {
		name   string
		status agent.ExecutionStatus
		want   string
	}{
		{"completed", agent.ExecutionStatusCompleted, events.StageStatusCompleted},
		{"failed", agent.ExecutionStatusFailed, events.StageStatusFailed},
		{"timed_out", agent.ExecutionStatusTimedOut, events.StageStatusTimedOut},
		{"cancelled", agent.ExecutionStatusCancelled, events.StageStatusCancelled},
		{"unknown defaults to failed", agent.ExecutionStatus("unknown"), events.StageStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapChatAgentStatus(tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ────────────────────────────────────────────────────────────
// buildChatContext tests
// ────────────────────────────────────────────────────────────

func TestChatMessageExecutor_BuildChatContext_SynthesisPairingWithDuplicateStageNames(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	stageService := services.NewStageService(client.Client)
	timelineService := services.NewTimelineService(client.Client)
	chatService := services.NewChatService(client.Client)

	session := createChatTestSession(t, client.Client)

	// Create two investigation stages with the SAME name — this is the collision scenario.
	investStage1, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Analysis",
		StageIndex:         0,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	synthStage1, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Analysis - Synthesis",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	investStage2, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Analysis",
		StageIndex:         2,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	synthStage2, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Analysis - Synthesis",
		StageIndex:         3,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	// Create agent executions for investigation stages (required for edges).
	exec1, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           investStage1.ID,
		SessionID:         session.ID,
		AgentName:         "agent-1",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           investStage2.ID,
		SessionID:         session.ID,
		AgentName:         "agent-1",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	// Create agent executions for synthesis stages (extractFinalAnalysis reads these).
	synthExec1, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           synthStage1.ID,
		SessionID:         session.ID,
		AgentName:         "synthesizer",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	synthExec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           synthStage2.ID,
		SessionID:         session.ID,
		AgentName:         "synthesizer",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	// Create final_analysis timeline events with distinct content for each synthesis stage.
	synthStage1ID := synthStage1.ID
	synthExec1ID := synthExec1.ID
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &synthStage1ID,
		ExecutionID:    &synthExec1ID,
		SequenceNumber: 1,
		EventType:      "final_analysis",
		Content:        "SYNTHESIS-RESULT-ALPHA",
	})
	require.NoError(t, err)

	synthStage2ID := synthStage2.ID
	synthExec2ID := synthExec2.ID
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &synthStage2ID,
		ExecutionID:    &synthExec2ID,
		SequenceNumber: 1,
		EventType:      "final_analysis",
		Content:        "SYNTHESIS-RESULT-BETA",
	})
	require.NoError(t, err)

	// Also create a timeline event for each investigation agent (so the agent timelines load).
	investStage1ID := investStage1.ID
	exec1ID := exec1.ID
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &investStage1ID,
		ExecutionID:    &exec1ID,
		SequenceNumber: 1,
		EventType:      "llm_response",
		Content:        "investigation 1 findings",
	})
	require.NoError(t, err)

	investStage2ID := investStage2.ID
	exec2ID := exec2.ID
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &investStage2ID,
		ExecutionID:    &exec2ID,
		SequenceNumber: 1,
		EventType:      "llm_response",
		Content:        "investigation 2 findings",
	})
	require.NoError(t, err)

	// Create the chat + message needed for the input.
	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chat.ID,
		Content: "What happened?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Build the executor with the real services.
	executor := &ChatMessageExecutor{
		stageService:    stageService,
		timelineService: timelineService,
	}

	result := executor.buildChatContext(ctx, ChatExecuteInput{
		Chat:    chat,
		Message: msg,
		Session: session,
	})

	// Both synthesis results must appear — if keyed by name, only one would survive.
	assert.Contains(t, result.InvestigationContext, "SYNTHESIS-RESULT-ALPHA",
		"first synthesis result should be present when stage names collide")
	assert.Contains(t, result.InvestigationContext, "SYNTHESIS-RESULT-BETA",
		"second synthesis result should be present when stage names collide")
	assert.Equal(t, "What happened?", result.UserQuestion)
}

// ────────────────────────────────────────────────────────────
// finishStage / createFailedChatExecution tests
// ────────────────────────────────────────────────────────────

func TestChatMessageExecutor_FinishStage_MarksStageFailedWithoutExecutions(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	stageService := services.NewStageService(client.Client)
	session := createChatTestSession(t, client.Client)

	// Create a stage with no agent executions (simulates early-exit before execution creation)
	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Chat Response",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, stage.StatusPending, stg.Status)

	executor := &ChatMessageExecutor{
		stageService: stageService,
		// eventPublisher is nil — publishStageStatus handles nil gracefully
	}

	executor.finishStage(stg.ID, session.ID, "Chat Response", 1, events.StageStatusFailed, "chain not found")

	// Stage must be failed in DB (previously would stay pending)
	updated, err := stageService.GetStageByID(ctx, stg.ID, false)
	require.NoError(t, err)
	assert.Equal(t, stage.StatusFailed, updated.Status)
	assert.NotNil(t, updated.CompletedAt)
	require.NotNil(t, updated.ErrorMessage)
	assert.Contains(t, *updated.ErrorMessage, "chain not found")
}

func TestChatMessageExecutor_CreateFailedChatExecution(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	stageService := services.NewStageService(client.Client)
	session := createChatTestSession(t, client.Client)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Chat Response",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	executor := &ChatMessageExecutor{
		stageService: stageService,
	}

	logger := slog.Default()

	executor.createFailedChatExecution(ctx, stg.ID, session.ID, "chat", "test-provider", "config resolution error", logger)

	// Verify the execution record was created and is in failed state
	executions, err := stageService.GetAgentExecutions(ctx, stg.ID)
	require.NoError(t, err)
	require.Len(t, executions, 1)

	exec := executions[0]
	assert.Equal(t, "chat", exec.AgentName)
	assert.Equal(t, 1, exec.AgentIndex)
	assert.Equal(t, agentexecution.StatusFailed, exec.Status)
	require.NotNil(t, exec.ErrorMessage)
	assert.Contains(t, *exec.ErrorMessage, "config resolution error")
	require.NotNil(t, exec.LlmProvider)
	assert.Equal(t, "test-provider", *exec.LlmProvider)

	// Stage can now be finalized via UpdateStageStatus (the whole point)
	err = stageService.UpdateStageStatus(ctx, stg.ID)
	require.NoError(t, err)

	updated, err := stageService.GetStageByID(ctx, stg.ID, false)
	require.NoError(t, err)
	assert.Equal(t, stage.StatusFailed, updated.Status,
		"UpdateStageStatus should finalize stage as failed when the only execution is failed")
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
		MCPServerRegistry:   config.NewMCPServerRegistry(nil),
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
