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
		assert.Equal(t, "system", string(msg1.Role))

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

	t.Run("gets messages up to sequence", func(t *testing.T) {
		messages, err := messageService.GetMessagesUpToSequence(ctx, exec.ID, 1)
		require.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, "system", string(messages[0].Role))
	})
}
