package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInteractionService_CreateLLMInteraction(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	t.Run("creates LLM interaction with all fields", func(t *testing.T) {
		thinking := "Thinking content"
		inputTokens := 100
		outputTokens := 200
		totalTokens := 300
		durationMs := 1500

		req := models.CreateLLMInteractionRequest{
			SessionID:        session.ID,
			StageID:          &stg.ID,
			ExecutionID:      &exec.ID,
			InteractionType:  "iteration",
			ModelName:        "gemini-2.0-flash",
			LLMRequest:       map[string]any{"prompt": "test"},
			LLMResponse:      map[string]any{"text": "response"},
			ThinkingContent:  &thinking,
			ResponseMetadata: map[string]any{"grounding": true},
			InputTokens:      &inputTokens,
			OutputTokens:     &outputTokens,
			TotalTokens:      &totalTokens,
			DurationMs:       &durationMs,
		}

		interaction, err := interactionService.CreateLLMInteraction(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.ModelName, interaction.ModelName)
		assert.Equal(t, thinking, *interaction.ThinkingContent)
		assert.Equal(t, inputTokens, *interaction.InputTokens)
	})
}

func TestInteractionService_CreateLLMInteraction_SessionLevel(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Create session-level interaction (nil stage_id, nil execution_id).
	interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		InteractionType: "executive_summary",
		ModelName:       "gemini-2.0-flash",
		LLMRequest:      map[string]any{"conversation": []any{}},
		LLMResponse:     map[string]any{"text_length": 42},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, interaction.ID)
	assert.Nil(t, interaction.StageID)
	assert.Nil(t, interaction.ExecutionID)
	assert.Equal(t, "gemini-2.0-flash", interaction.ModelName)
	assert.Equal(t, llminteraction.InteractionTypeExecutiveSummary, interaction.InteractionType)

	// Reconstruct conversation should return empty (no messages, no execution_id).
	messages, err := interactionService.ReconstructConversation(ctx, interaction.ID)
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestInteractionService_CreateMCPInteraction(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	t.Run("creates MCP tool_call interaction", func(t *testing.T) {
		toolName := "kubectl-get-pods"
		durationMs := 500

		req := models.CreateMCPInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "tool_call",
			ServerName:      "kubernetes-server",
			ToolName:        &toolName,
			ToolArguments:   map[string]any{"namespace": "default"},
			ToolResult:      map[string]any{"pods": []string{"pod-1"}},
			DurationMs:      &durationMs,
		}

		interaction, err := interactionService.CreateMCPInteraction(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.ServerName, interaction.ServerName)
		assert.Equal(t, toolName, *interaction.ToolName)
		assert.Equal(t, durationMs, *interaction.DurationMs)
	})

	t.Run("creates MCP tool_list interaction", func(t *testing.T) {
		req := models.CreateMCPInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "tool_list",
			ServerName:      "kubernetes-server",
			AvailableTools:  []any{map[string]string{"name": "get", "description": "Get resources"}, map[string]string{"name": "describe", "description": "Describe resources"}},
		}

		interaction, err := interactionService.CreateMCPInteraction(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, interaction.AvailableTools)
	})
}

func TestInteractionService_GetInteractionsList(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	// Create interactions
	_, err = interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		StageID:         &stg.ID,
		ExecutionID:     &exec.ID,
		InteractionType: "iteration",
		ModelName:       "gemini-2.0-flash",
		LLMRequest:      map[string]any{},
		LLMResponse:     map[string]any{},
	})
	require.NoError(t, err)

	toolName := "kubectl-get"
	_, err = interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "tool_call",
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		ToolArguments:   map[string]any{},
		ToolResult:      map[string]any{},
	})
	require.NoError(t, err)

	t.Run("retrieves LLM interactions list", func(t *testing.T) {
		interactions, err := interactionService.GetLLMInteractionsList(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, interactions, 1)
	})

	t.Run("retrieves MCP interactions list", func(t *testing.T) {
		interactions, err := interactionService.GetMCPInteractionsList(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, interactions, 1)
	})
}

func TestInteractionService_GetInteractionDetail(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	llmInt, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		StageID:         &stg.ID,
		ExecutionID:     &exec.ID,
		InteractionType: "iteration",
		ModelName:       "gemini-2.0-flash",
		LLMRequest:      map[string]any{"key": "value"},
		LLMResponse:     map[string]any{"result": "data"},
	})
	require.NoError(t, err)

	toolName := "kubectl"
	mcpInt, err := interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "tool_call",
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		ToolArguments:   map[string]any{},
		ToolResult:      map[string]any{},
	})
	require.NoError(t, err)

	t.Run("gets LLM interaction detail", func(t *testing.T) {
		detail, err := interactionService.GetLLMInteractionDetail(ctx, llmInt.ID)
		require.NoError(t, err)
		assert.Equal(t, llmInt.ID, detail.ID)
		assert.NotNil(t, detail.LlmRequest)
	})

	t.Run("gets MCP interaction detail", func(t *testing.T) {
		detail, err := interactionService.GetMCPInteractionDetail(ctx, mcpInt.ID)
		require.NoError(t, err)
		assert.Equal(t, mcpInt.ID, detail.ID)
	})

	t.Run("returns ErrNotFound for missing LLM interaction", func(t *testing.T) {
		_, err := interactionService.GetLLMInteractionDetail(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("returns ErrNotFound for missing MCP interaction", func(t *testing.T) {
		_, err := interactionService.GetMCPInteractionDetail(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestInteractionService_ReconstructConversation(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	t.Run("reconstructs conversation from last_message_id", func(t *testing.T) {
		// Create messages
		_, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 1,
			Role:           message.RoleSystem,
			Content:        "System prompt",
		})
		require.NoError(t, err)

		msg2, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			Role:           message.RoleUser,
			Content:        "User message",
		})
		require.NoError(t, err)

		_, err = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 3,
			Role:           message.RoleAssistant,
			Content:        "Assistant response",
		})
		require.NoError(t, err)

		// Create interaction pointing to msg2
		interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         &stg.ID,
			ExecutionID:     &exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LastMessageID:   &msg2.ID,
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		// Reconstruct should get messages 1 and 2 only
		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 2)
		assert.Equal(t, message.RoleSystem, conversation[0].Role)
		assert.Equal(t, message.RoleUser, conversation[1].Role)
	})

	t.Run("returns empty conversation when no last_message_id", func(t *testing.T) {
		interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         &stg.ID,
			ExecutionID:     &exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 0)
	})

	t.Run("handles last_message_id pointing to first message", func(t *testing.T) {
		// Create a new execution for isolated test
		exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "TestAgent2",
			AgentIndex: 2,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		// Create only one message
		msg1, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec2.ID,
			SequenceNumber: 1,
			Role:           message.RoleSystem,
			Content:        "First message",
		})
		require.NoError(t, err)

		// Create interaction pointing to first message
		interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         &stg.ID,
			ExecutionID:     &exec2.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LastMessageID:   &msg1.ID,
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		// Should get exactly one message
		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 1)
		assert.Equal(t, message.RoleSystem, conversation[0].Role)
	})

	t.Run("handles last_message_id pointing to middle of long conversation", func(t *testing.T) {
		// Create a new execution for isolated test
		exec3, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "TestAgent3",
			AgentIndex: 3,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		// Create 10 messages
		var messages []*ent.Message
		for i := 1; i <= 10; i++ {
			role := message.RoleUser
			if i%2 == 0 {
				role = message.RoleAssistant
			}
			msg, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
				SessionID:      session.ID,
				StageID:        stg.ID,
				ExecutionID:    exec3.ID,
				SequenceNumber: i,
				Role:           role,
				Content:        fmt.Sprintf("Message %d", i),
			})
			require.NoError(t, err)
			messages = append(messages, msg)
		}

		// Create interaction pointing to message 5 (middle)
		interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         &stg.ID,
			ExecutionID:     &exec3.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LastMessageID:   &messages[4].ID, // Message 5 (index 4)
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		// Should get messages 1-5 only (not 6-10)
		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 5)
		assert.Equal(t, "Message 1", conversation[0].Content)
		assert.Equal(t, "Message 5", conversation[4].Content)
	})

	t.Run("returns error for nonexistent interaction", func(t *testing.T) {
		_, err := interactionService.ReconstructConversation(ctx, "nonexistent-id")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("handles execution with no messages at all", func(t *testing.T) {
		// Create a new execution with no messages
		exec4, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "TestAgent4",
			AgentIndex: 4,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		// Create interaction with no last_message_id (no messages created)
		interaction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         &stg.ID,
			ExecutionID:     &exec4.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		// Should get empty conversation
		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 0)
	})
}
