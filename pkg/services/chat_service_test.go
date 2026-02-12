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
		assert.ErrorIs(t, err, ErrNotFound)
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
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestChatService_GetOrCreateChat(t *testing.T) {
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

	t.Run("creates chat on first call", func(t *testing.T) {
		chat, created, err := chatService.GetOrCreateChat(ctx, session.ID, "user@example.com")
		require.NoError(t, err)
		assert.True(t, created)
		assert.Equal(t, session.ID, chat.SessionID)
		assert.Equal(t, session.ChainID, chat.ChainID)
	})

	t.Run("returns existing chat on second call", func(t *testing.T) {
		chat1, created1, err := chatService.GetOrCreateChat(ctx, session.ID, "user@example.com")
		require.NoError(t, err)
		assert.False(t, created1) // already exists from previous subtest

		// Should be the same chat
		chat2, created2, err := chatService.GetOrCreateChat(ctx, session.ID, "other@example.com")
		require.NoError(t, err)
		assert.False(t, created2)
		assert.Equal(t, chat1.ID, chat2.ID)
	})

	t.Run("returns ErrNotFound for missing session", func(t *testing.T) {
		_, _, err := chatService.GetOrCreateChat(ctx, "nonexistent", "user@example.com")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("validates session_id required", func(t *testing.T) {
		_, _, err := chatService.GetOrCreateChat(ctx, "", "user@example.com")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("validates author required", func(t *testing.T) {
		_, _, err := chatService.GetOrCreateChat(ctx, session.ID, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestChatService_GetChatBySessionID(t *testing.T) {
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

	t.Run("returns nil for session without chat", func(t *testing.T) {
		chat, err := chatService.GetChatBySessionID(ctx, session.ID)
		require.NoError(t, err)
		assert.Nil(t, chat)
	})

	t.Run("returns chat after creation", func(t *testing.T) {
		_, err := chatService.CreateChat(ctx, models.CreateChatRequest{
			SessionID: session.ID,
			CreatedBy: "user@example.com",
		})
		require.NoError(t, err)

		chat, err := chatService.GetChatBySessionID(ctx, session.ID)
		require.NoError(t, err)
		require.NotNil(t, chat)
		assert.Equal(t, session.ID, chat.SessionID)
	})
}
