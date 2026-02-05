package services

import (
	"context"
	"testing"

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
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	t.Run("creates LLM interaction with all fields", func(t *testing.T) {
		thinking := "Thinking content"
		inputTokens := 100
		outputTokens := 200
		totalTokens := 300
		durationMs := 1500

		req := models.CreateLLMInteractionRequest{
			SessionID:        session.ID,
			StageID:          stg.ID,
			ExecutionID:      exec.ID,
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

func TestInteractionService_CreateMCPInteraction(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

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
			AvailableTools:  map[string]any{"tools": []string{"get", "describe"}},
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
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	// Create interactions
	_, _ = interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "iteration",
		ModelName:       "gemini-2.0-flash",
		LLMRequest:      map[string]any{},
		LLMResponse:     map[string]any{},
	})

	toolName := "kubectl-get"
	_, _ = interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "tool_call",
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		ToolArguments:   map[string]any{},
		ToolResult:      map[string]any{},
	})

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
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	llmInt, _ := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "iteration",
		ModelName:       "gemini-2.0-flash",
		LLMRequest:      map[string]any{"key": "value"},
		LLMResponse:     map[string]any{"result": "data"},
	})

	toolName := "kubectl"
	mcpInt, _ := interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
		SessionID:       session.ID,
		StageID:         stg.ID,
		ExecutionID:     exec.ID,
		InteractionType: "tool_call",
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		ToolArguments:   map[string]any{},
		ToolResult:      map[string]any{},
	})

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
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	t.Run("reconstructs conversation from last_message_id", func(t *testing.T) {
		// Create messages
		_, _ = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 1,
			Role:           "system",
			Content:        "System prompt",
		})

		msg2, _ := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			Role:           "user",
			Content:        "User message",
		})

		_, _ = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 3,
			Role:           "assistant",
			Content:        "Assistant response",
		})

		// Create interaction pointing to msg2
		interaction, _ := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LastMessageID:   &msg2.ID,
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})

		// Reconstruct should get messages 1 and 2 only
		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 2)
		assert.Equal(t, "system", string(conversation[0].Role))
		assert.Equal(t, "user", string(conversation[1].Role))
	})

	t.Run("returns empty conversation when no last_message_id", func(t *testing.T) {
		interaction, _ := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})

		conversation, err := interactionService.ReconstructConversation(ctx, interaction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 0)
	})
}
