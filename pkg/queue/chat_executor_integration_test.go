package queue

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	util "github.com/codeready-toolchain/tarsy/test/util"
)

// ────────────────────────────────────────────────────────────
// Helper: build a config with chat enabled
// ────────────────────────────────────────────────────────────

func chatTestConfig(chainID string, chain *config.ChainConfig) *config.Config {
	maxIter := 1
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:   "test-provider",
			LLMBackend:    config.LLMBackendLangChain,
			MaxIterations: &maxIter,
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent": {
				LLMBackend:    config.LLMBackendLangChain,
				MaxIterations: &maxIter,
			},
			"ChatAgent": {
				LLMBackend:    config.LLMBackendLangChain,
				MaxIterations: &maxIter,
			},
			"SynthesisAgent": {
				Type:          config.AgentTypeSynthesis,
				LLMBackend:    config.LLMBackendLangChain,
				MaxIterations: &maxIter,
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {
				Type:  config.LLMProviderTypeGoogle,
				Model: "test-model",
			},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			chainID: chain,
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(nil),
	}
}

// ────────────────────────────────────────────────────────────
// Integration tests
// ────────────────────────────────────────────────────────────

func TestChatExecutor_FirstMessage_ExecutesThroughAgentFramework(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "chat-test-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	// Chat LLM response — agent produces a final answer in 1 call
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: I see the investigation context. The user is asking about the alert.\nFinal Answer: Based on the investigation, the pod was OOM killed due to a memory leak."},
			}},
		},
	}

	cfg := chatTestConfig(chainID, chain)
	publisher := &testEventPublisher{}

	chatExecutor := NewChatMessageExecutor(cfg, entClient, llm, nil, publisher,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	// Create a completed session (investigation already done)
	session, err := entClient.AlertSession.Create().
		SetID("session-chat-1").
		SetAlertData("Pod-1 OOMKilled").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	// Create investigation stage (already completed)
	_, err = entClient.Stage.Create().
		SetID("inv-stage-1").
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	// Create chat + message
	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "What caused the OOM?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Submit
	stageID, err := chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg,
		Session: session,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, stageID)

	// Wait for execution to complete
	chatExecutor.wg.Wait()

	// Verify chat Stage record
	chatStage, err := entClient.Stage.Get(ctx, stageID)
	require.NoError(t, err)
	assert.Equal(t, "Chat Response", chatStage.StageName)
	assert.Equal(t, 2, chatStage.StageIndex) // investigation was 1
	assert.Equal(t, stage.StatusCompleted, chatStage.Status)
	assert.NotNil(t, chatStage.ChatID)
	assert.Equal(t, chatObj.ID, *chatStage.ChatID)

	// Verify AgentExecution record
	execs, err := entClient.AgentExecution.Query().
		Where(agentexecution.StageIDEQ(stageID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, execs, 1)
	assert.Equal(t, "ChatAgent", execs[0].AgentName)
	assert.Equal(t, agentexecution.StatusCompleted, execs[0].Status)

	// Verify timeline events: user_question + final_analysis
	tlEvents, err := entClient.TimelineEvent.Query().
		Where(timelineevent.SessionIDEQ(session.ID)).
		All(ctx)
	require.NoError(t, err)

	var hasUserQuestion, hasFinalAnalysis bool
	for _, ev := range tlEvents {
		if ev.EventType == timelineevent.EventTypeUserQuestion {
			hasUserQuestion = true
			assert.Equal(t, "What caused the OOM?", ev.Content)
			assert.Equal(t, timelineevent.StatusCompleted, ev.Status,
				"user_question must be persisted as completed (fire-and-forget) so the API returns the same status as the WS event")
		}
		if ev.EventType == timelineevent.EventTypeFinalAnalysis {
			hasFinalAnalysis = true
			assert.Contains(t, ev.Content, "memory leak")
		}
	}
	assert.True(t, hasUserQuestion, "should have user_question timeline event")
	assert.True(t, hasFinalAnalysis, "should have final_analysis timeline event")

	// Verify stage events were published
	require.GreaterOrEqual(t, len(publisher.stageStatuses), 2)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)
	assert.Equal(t, "Chat Response", publisher.stageStatuses[0].StageName)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[len(publisher.stageStatuses)-1].Status)

	// Verify user_question timeline event was published via WS (prod bug fix).
	// Without the PublishTimelineCreated call in chat_executor, the dashboard
	// would never receive the user question for rendering.
	publisher.mu.Lock()
	var foundUserQuestionWS bool
	for _, evt := range publisher.timelineCreated {
		if evt.EventType == timelineevent.EventTypeUserQuestion {
			foundUserQuestionWS = true
			assert.Equal(t, "What caused the OOM?", evt.Content)
			assert.Equal(t, session.ID, evt.SessionID)
			break
		}
	}
	publisher.mu.Unlock()
	assert.True(t, foundUserQuestionWS, "user_question must be published via WS for dashboard rendering")
}

func TestChatExecutor_ContextAccumulation(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "ctx-accum-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	// Two chat executions: first builds context, second should see it
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Chat message 1 response
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Analyzing OOM.\nFinal Answer: The pod was OOM killed because of excessive memory usage in container app-1."},
			}},
			// Chat message 2 response
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: User wants restart details.\nFinal Answer: You can restart it with kubectl rollout restart deployment/app-1."},
			}},
		},
	}

	cfg := chatTestConfig(chainID, chain)
	publisher := &testEventPublisher{}

	chatExecutor := NewChatMessageExecutor(cfg, entClient, llm, nil, publisher,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	// Create session with a pre-existing investigation timeline event
	session, err := entClient.AlertSession.Create().
		SetID("session-accum-1").
		SetAlertData("Pod OOMKilled").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	// Add investigation stage with two parallel agent executions
	// (simulates completed parallel investigation — exercises provider display in context)
	_, err = entClient.Stage.Create().
		SetID("inv-stage-accum").
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(2).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	invExecID1 := "inv-exec-accum-1"
	_, err = entClient.AgentExecution.Create().
		SetID(invExecID1).
		SetStageID("inv-stage-accum").
		SetSessionID(session.ID).
		SetAgentName("TestAgent").
		SetAgentIndex(1).
		SetLlmBackend("google-native").
		SetLlmProvider("gemini-2.5-pro").
		SetStatus(agentexecution.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	invExecID2 := "inv-exec-accum-2"
	_, err = entClient.AgentExecution.Create().
		SetID(invExecID2).
		SetStageID("inv-stage-accum").
		SetSessionID(session.ID).
		SetAgentName("TestAgent").
		SetAgentIndex(2).
		SetLlmBackend("google-native").
		SetLlmProvider("gemini-2.5-pro").
		SetStatus(agentexecution.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	timelineService := services.NewTimelineService(entClient)
	invStageID := "inv-stage-accum"
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &invStageID,
		ExecutionID:    &invExecID1,
		SequenceNumber: 1,
		EventType:      timelineevent.EventTypeFinalAnalysis,
		Content:        "Investigation: Pod-1 has been OOM killed 5 times in the last hour.",
	})
	require.NoError(t, err)
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        &invStageID,
		ExecutionID:    &invExecID2,
		SequenceNumber: 2,
		EventType:      timelineevent.EventTypeFinalAnalysis,
		Content:        "Agent-2: Memory spike correlates with deployment at 14:03.",
	})
	require.NoError(t, err)

	// Create chat + first message
	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg1, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "What caused the OOM?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Submit first message
	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg1,
		Session: session,
	})
	require.NoError(t, err)
	chatExecutor.wg.Wait()

	// Now send second message
	msg2, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "How do I restart it?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg2,
		Session: session,
	})
	require.NoError(t, err)
	chatExecutor.wg.Wait()

	// Verify the 2nd LLM call's input contains context from:
	// 1. The original investigation (final_analysis)
	// 2. The first chat exchange (user_question + final_analysis from chat msg 1)
	require.GreaterOrEqual(t, len(llm.capturedInputs), 2)

	secondInput := llm.capturedInputs[1]
	var foundInvestigation, foundFirstExchange, foundLLMProvider, foundAgent2 bool
	for _, msg := range secondInput.Messages {
		if strings.Contains(msg.Content, "OOM killed 5 times") {
			foundInvestigation = true
		}
		if strings.Contains(msg.Content, "Memory spike correlates") {
			foundAgent2 = true
		}
		if strings.Contains(msg.Content, "What caused the OOM?") || strings.Contains(msg.Content, "excessive memory usage") {
			foundFirstExchange = true
		}
		if strings.Contains(msg.Content, "gemini-2.5-pro") {
			foundLLMProvider = true
		}
	}
	assert.True(t, foundInvestigation, "2nd chat call should see agent-1 investigation context")
	assert.True(t, foundAgent2, "2nd chat call should see agent-2 investigation context")
	assert.True(t, foundFirstExchange, "2nd chat call should see first chat exchange context")
	assert.True(t, foundLLMProvider, "chat context should include agent's LLM provider name from AgentExecution")
}

func TestChatExecutor_OneAtATimeEnforcement(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "oaat-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	// LLM that blocks until channel is fed (simulates slow execution)
	slowCh := make(chan agent.Chunk)
	slowLLM := &slowMockLLMClient{responseCh: slowCh}

	cfg := chatTestConfig(chainID, chain)
	publisher := &testEventPublisher{}

	chatExecutor := NewChatMessageExecutor(cfg, entClient, slowLLM, nil, publisher,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	session, err := entClient.AlertSession.Create().
		SetID("session-oaat-1").
		SetAlertData("Test alert").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.Stage.Create().
		SetID("inv-stage-oaat").
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg1, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "First question",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	msg2, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "Second question",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Submit first message (will block on slow LLM)
	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg1,
		Session: session,
	})
	require.NoError(t, err)

	// Wait for the execution to be registered (poll instead of fixed sleep)
	waitForActiveExecution(t, chatExecutor, chatObj.ID)

	// Submit second message should fail: one-at-a-time
	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg2,
		Session: session,
	})
	assert.ErrorIs(t, err, ErrChatExecutionActive)

	// Release the slow LLM
	slowCh <- &agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Answer 1."}
	close(slowCh)

	// Wait for first execution to complete
	chatExecutor.wg.Wait()

	// Now second message should succeed
	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg2,
		Session: session,
	})
	// May succeed or fail depending on stage status - but should NOT get ErrChatExecutionActive
	if err != nil {
		assert.NotErrorIs(t, err, ErrChatExecutionActive)
	}
}

func TestChatExecutor_CancellationEndToEnd(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "cancel-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	// LLM that blocks until cancelled
	blockCh := make(chan agent.Chunk)
	slowLLM := &slowMockLLMClient{responseCh: blockCh}

	cfg := chatTestConfig(chainID, chain)
	publisher := &testEventPublisher{}

	chatExecutor := NewChatMessageExecutor(cfg, entClient, slowLLM, nil, publisher,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	session, err := entClient.AlertSession.Create().
		SetID("session-cancel-1").
		SetAlertData("Test alert").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.Stage.Create().
		SetID("inv-stage-cancel").
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "What happened?",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Submit
	stageID, err := chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg,
		Session: session,
	})
	require.NoError(t, err)

	// Wait for execution to be registered (poll instead of fixed sleep)
	waitForActiveExecution(t, chatExecutor, chatObj.ID)

	// Cancel the execution
	cancelled := chatExecutor.CancelExecution(chatObj.ID)
	assert.True(t, cancelled)

	// Close the channel so the LLM goroutine unblocks
	close(blockCh)

	// Wait for completion
	done := make(chan struct{})
	go func() {
		chatExecutor.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not complete after cancellation")
	}

	// Verify stage status reflects cancellation or failure
	chatStage, err := entClient.Stage.Get(ctx, stageID)
	require.NoError(t, err)
	// Stage should be in a terminal state (cancelled or failed)
	assert.NotEqual(t, stage.StatusPending, chatStage.Status)

	// Verify terminal stage event was published
	require.NotEmpty(t, publisher.stageStatuses, "expected at least one stage status event")
	lastStatus := publisher.stageStatuses[len(publisher.stageStatuses)-1]
	assert.Contains(t, []string{events.StageStatusCancelled, events.StageStatusFailed}, lastStatus.Status)
}

func TestChatExecutor_AcceptsInProgressSession(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "inprog-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Test."},
			}},
		},
	}

	cfg := chatTestConfig(chainID, chain)

	chatExecutor := NewChatMessageExecutor(cfg, entClient, llm, nil, nil,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	// Create an in_progress session (investigation still running)
	session, err := entClient.AlertSession.Create().
		SetID("session-inprog-1").
		SetAlertData("Test alert").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusInProgress).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	// Create chat for the session (this is technically allowed — the check
	// is done at the API layer via isChatAvailable, not in the executor)
	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "Test",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// The executor itself doesn't check session status — that's the API's job.
	// Verify the executor still accepts the submission (API gating is separate).
	// However, the chat stage will be created for an in-progress session.
	// This confirms the executor doesn't itself enforce session status.
	stageID, err := chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg,
		Session: session,
	})
	// Submit should succeed (executor doesn't check session status)
	require.NoError(t, err)
	assert.NotEmpty(t, stageID)

	chatExecutor.wg.Wait()
}

func TestChatExecutor_CancelBySessionID(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()

	chainID := "cancel-session-chain"
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
		Chat: &config.ChatConfig{Enabled: true},
	}

	// LLM that blocks
	blockCh := make(chan agent.Chunk)
	slowLLM := &slowMockLLMClient{responseCh: blockCh}

	cfg := chatTestConfig(chainID, chain)

	chatExecutor := NewChatMessageExecutor(cfg, entClient, slowLLM, nil, nil,
		ChatMessageExecutorConfig{
			SessionTimeout:    30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
		},
		nil,
	)
	defer chatExecutor.Stop()

	session, err := entClient.AlertSession.Create().
		SetID("session-cancel-by-sid").
		SetAlertData("Test alert").
		SetAgentType("kubernetes").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.Stage.Create().
		SetID("inv-stage-csid").
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	chatService := services.NewChatService(entClient)
	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	msg, err := chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chatObj.ID,
		Content: "test",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	// Submit
	_, err = chatExecutor.Submit(ctx, ChatExecuteInput{
		Chat:    chatObj,
		Message: msg,
		Session: session,
	})
	require.NoError(t, err)

	// Wait for execution to be registered (poll instead of fixed sleep)
	waitForActiveExecution(t, chatExecutor, chatObj.ID)

	// Cancel by session ID (as the cancel session handler would)
	cancelled := chatExecutor.CancelBySessionID(ctx, session.ID)
	assert.True(t, cancelled)

	close(blockCh)

	done := make(chan struct{})
	go func() {
		chatExecutor.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not complete after CancelBySessionID")
	}
}

// ────────────────────────────────────────────────────────────
// Slow LLM mock — blocks until channel is fed or closed
// ────────────────────────────────────────────────────────────

type slowMockLLMClient struct {
	responseCh chan agent.Chunk
	// If responseCh is closed, Generate returns context.Canceled
}

func (m *slowMockLLMClient) Generate(ctx context.Context, _ *agent.GenerateInput) (<-chan agent.Chunk, error) {
	ch := make(chan agent.Chunk, 10)
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				ch <- &agent.ErrorChunk{Message: fmt.Sprintf("context cancelled: %v", ctx.Err())}
				return
			case chunk, ok := <-m.responseCh:
				if !ok {
					return // channel closed
				}
				ch <- chunk
			}
		}
	}()
	return ch, nil
}

func (m *slowMockLLMClient) Close() error { return nil }

// waitForActiveExecution polls until the chat executor registers an active
// execution for the given chat ID (resilient under CI load vs fixed sleep).
func waitForActiveExecution(t *testing.T, executor *ChatMessageExecutor, chatID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		executor.mu.RLock()
		defer executor.mu.RUnlock()
		_, ok := executor.activeExecs[chatID]
		return ok
	}, 5*time.Second, 20*time.Millisecond, "execution was not registered for chat %s", chatID)
}
