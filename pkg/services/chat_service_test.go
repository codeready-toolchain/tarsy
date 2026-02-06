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

func TestChatService_CreateChat(t *testing.T) {
	client := testdb.NewTestClient(t)
	chatService := NewChatService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test alert",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	t.Run("creates chat successfully", func(t *testing.T) {
		req := models.CreateChatRequest{
			SessionID: session.ID,
			CreatedBy: "test@example.com",
		}

		chat, err := chatService.CreateChat(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, session.ID, chat.SessionID)
		assert.Equal(t, session.ChainID, chat.ChainID)
		assert.Equal(t, req.CreatedBy, *chat.CreatedBy)
	})

	t.Run("validates session_id required", func(t *testing.T) {
		req := models.CreateChatRequest{
			CreatedBy: "test@example.com",
		}

		_, err := chatService.CreateChat(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("validates created_by required", func(t *testing.T) {
		req := models.CreateChatRequest{
			SessionID: session.ID,
		}

		_, err := chatService.CreateChat(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		req := models.CreateChatRequest{
			SessionID: "nonexistent",
			CreatedBy: "test@example.com",
		}

		_, err := chatService.CreateChat(ctx, req)
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestChatService_AddChatMessage(t *testing.T) {
	client := testdb.NewTestClient(t)
	chatService := NewChatService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	t.Run("adds message successfully", func(t *testing.T) {
		req := models.AddChatMessageRequest{
			ChatID:  chat.ID,
			Content: "What caused this issue?",
			Author:  "test@example.com",
		}

		msg, err := chatService.AddChatMessage(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.Content, msg.Content)
		assert.Equal(t, req.Author, msg.Author)
		assert.NotNil(t, msg.CreatedAt)
	})

	t.Run("validates required fields", func(t *testing.T) {
		tests := []struct {
			name    string
			req     models.AddChatMessageRequest
			wantErr string
		}{
			{
				name: "missing chat_id",
				req: models.AddChatMessageRequest{
					Content: "test message",
					Author:  "test@example.com",
				},
				wantErr: "chat_id",
			},
			{
				name: "missing content",
				req: models.AddChatMessageRequest{
					ChatID: chat.ID,
					Author: "test@example.com",
				},
				wantErr: "content",
			},
			{
				name: "missing author",
				req: models.AddChatMessageRequest{
					ChatID:  chat.ID,
					Content: "test message",
				},
				wantErr: "author",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := chatService.AddChatMessage(ctx, tt.req)
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
			})
		}
	})

	t.Run("returns ErrNotFound for nonexistent chat", func(t *testing.T) {
		req := models.AddChatMessageRequest{
			ChatID:  "nonexistent-chat-id",
			Content: "test message",
			Author:  "test@example.com",
		}

		_, err := chatService.AddChatMessage(ctx, req)
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestChatService_GetChatHistory(t *testing.T) {
	client := testdb.NewTestClient(t)
	chatService := NewChatService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	// Add messages
	_, err = chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chat.ID,
		Content: "Question 1",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	_, err = chatService.AddChatMessage(ctx, models.AddChatMessageRequest{
		ChatID:  chat.ID,
		Content: "Question 2",
		Author:  "test@example.com",
	})
	require.NoError(t, err)

	t.Run("retrieves chat history", func(t *testing.T) {
		history, err := chatService.GetChatHistory(ctx, chat.ID)
		require.NoError(t, err)
		assert.Equal(t, chat.ID, history.Chat.ID)
		assert.Len(t, history.UserMessages, 2)
	})

	t.Run("returns ErrNotFound for missing chat", func(t *testing.T) {
		_, err := chatService.GetChatHistory(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("validates empty chatID", func(t *testing.T) {
		_, err := chatService.GetChatHistory(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestChatService_BuildChatContext(t *testing.T) {
	client := testdb.NewTestClient(t)
	chatService := NewChatService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "Pod crashed in production",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Add final analysis to session
	err = client.AlertSession.UpdateOneID(session.ID).
		SetFinalAnalysis("Root cause: OOM killed the pod").
		Exec(ctx)
	require.NoError(t, err)

	chat, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	t.Run("builds context from parent session", func(t *testing.T) {
		chatContext, err := chatService.BuildChatContext(ctx, chat.ID)
		require.NoError(t, err)
		assert.Contains(t, chatContext, "Pod crashed in production")
		assert.Contains(t, chatContext, "Root cause: OOM killed the pod")
	})

	t.Run("returns ErrNotFound for missing chat", func(t *testing.T) {
		_, err := chatService.BuildChatContext(ctx, "nonexistent")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("validates empty chatID", func(t *testing.T) {
		_, err := chatService.BuildChatContext(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}
