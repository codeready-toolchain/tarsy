package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageService_CreateAndRetrieve(t *testing.T) {
	client := testdb.NewTestClient(t)
	messageService := NewMessageService(client.Client)
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
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	t.Run("creates and retrieves messages", func(t *testing.T) {
		// Create messages
		msg1, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 1,
			Role:           message.RoleSystem,
			Content:        "You are a helpful assistant",
		})
		require.NoError(t, err)
		assert.Equal(t, message.RoleSystem, msg1.Role)

		msg2, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			Role:           message.RoleUser,
			Content:        "Hello",
		})
		require.NoError(t, err)

		// Get messages
		messages, err := messageService.GetExecutionMessages(ctx, exec.ID)
		require.NoError(t, err)
		assert.Len(t, messages, 2)
		assert.Equal(t, msg1.ID, messages[0].ID)
		assert.Equal(t, msg2.ID, messages[1].ID)
	})

	t.Run("rejects invalid role", func(t *testing.T) {
		_, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 10,
			Role:           message.Role("admin"),
			Content:        "Should fail",
		})
		require.Error(t, err)
		var validationErr *ValidationError
		require.ErrorAs(t, err, &validationErr)
		assert.Equal(t, "role", validationErr.Field)
	})

	t.Run("gets messages up to sequence", func(t *testing.T) {
		messages, err := messageService.GetMessagesUpToSequence(ctx, exec.ID, 1)
		require.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, message.RoleSystem, messages[0].Role)
	})

	t.Run("creates message with tool calls", func(t *testing.T) {
		msg, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 10,
			Role:           message.RoleAssistant,
			Content:        "Let me check the logs",
			ToolCalls: []models.ToolCallData{
				{
					ID:        "call_123",
					Name:      "get_logs",
					Arguments: `{"namespace":"default"}`,
				},
				{
					ID:        "call_456",
					Name:      "get_pods",
					Arguments: `{"label":"app=web"}`,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, message.RoleAssistant, msg.Role)
		assert.Len(t, msg.ToolCalls, 2)
		assert.Equal(t, "call_123", msg.ToolCalls[0].ID)
		assert.Equal(t, "get_logs", msg.ToolCalls[0].Name)
		assert.Equal(t, `{"namespace":"default"}`, msg.ToolCalls[0].Arguments)
	})

	t.Run("creates assistant message with tool calls and empty content", func(t *testing.T) {
		msg, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 15,
			Role:           message.RoleAssistant,
			Content:        "", // LLM responded with only tool calls, no text
			ToolCalls: []models.ToolCallData{
				{
					ID:        "call_empty",
					Name:      "get_events",
					Arguments: `{"namespace":"kube-system"}`,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, message.RoleAssistant, msg.Role)
		assert.Equal(t, "", msg.Content)
		assert.Len(t, msg.ToolCalls, 1)
		assert.Equal(t, "call_empty", msg.ToolCalls[0].ID)
	})

	t.Run("rejects assistant message with empty content and no tool calls", func(t *testing.T) {
		_, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 16,
			Role:           message.RoleAssistant,
			Content:        "",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "content")
	})

	t.Run("creates tool response message", func(t *testing.T) {
		toolCallID := "call_789"
		msg, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 11,
			Role:           message.RoleTool,
			Content:        `{"pods": ["pod-1", "pod-2"]}`,
			ToolCallID:     toolCallID,
			ToolName:       "get_pods",
		})
		require.NoError(t, err)
		assert.Equal(t, message.RoleTool, msg.Role)
		require.NotNil(t, msg.ToolCallID)
		assert.Equal(t, toolCallID, *msg.ToolCallID)
		require.NotNil(t, msg.ToolName)
		assert.Equal(t, "get_pods", *msg.ToolName)
	})

	t.Run("gets stage messages across executions", func(t *testing.T) {
		// Create a second execution in the same stage
		exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "TestAgent2",
			AgentIndex:        2,
			IterationStrategy: "react",
		})
		require.NoError(t, err)

		// Create messages in both executions
		_, err = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 20,
			Role:           message.RoleUser,
			Content:        "Message in exec1",
		})
		require.NoError(t, err)

		_, err = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec2.ID,
			SequenceNumber: 1,
			Role:           message.RoleUser,
			Content:        "Message in exec2",
		})
		require.NoError(t, err)

		// Get all messages for the stage
		messages, err := messageService.GetStageMessages(ctx, stg.ID)
		require.NoError(t, err)
		// Should have all messages from both executions
		// (original 2 + tool call + empty-content tool call + tool response + 2 new = 7)
		assert.Len(t, messages, 7)
	})
}
