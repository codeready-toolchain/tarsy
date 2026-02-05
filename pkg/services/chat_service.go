// Package services contains business logic service layer implementations.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/chat"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// ChatService manages follow-up chat conversations
type ChatService struct {
	client *ent.Client
}

// NewChatService creates a new ChatService
func NewChatService(client *ent.Client) *ChatService {
	return &ChatService{client: client}
}

// CreateChat initializes a chat for a session
func (s *ChatService) CreateChat(httpCtx context.Context, req models.CreateChatRequest) (*ent.Chat, error) {
	if req.SessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}
	if req.CreatedBy == "" {
		return nil, NewValidationError("created_by", "required")
	}

	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	// Get session to inherit chain_id
	session, err := s.client.AlertSession.Get(ctx, req.SessionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	chatID := uuid.New().String()
	chatObj, err := s.client.Chat.Create().
		SetID(chatID).
		SetSessionID(req.SessionID).
		SetCreatedAt(time.Now()).
		SetCreatedBy(req.CreatedBy).
		SetChainID(session.ChainID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat: %w", err)
	}

	return chatObj, nil
}

// AddChatMessage adds a user message to the chat
func (s *ChatService) AddChatMessage(httpCtx context.Context, req models.AddChatMessageRequest) (*ent.ChatUserMessage, error) {
	// Validate input
	if req.ChatID == "" {
		return nil, NewValidationError("chat_id", "required")
	}
	if req.Content == "" {
		return nil, NewValidationError("content", "required")
	}
	if req.Author == "" {
		return nil, NewValidationError("author", "required")
	}

	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	// Verify chat exists before creating message (consistent with CreateChat pattern)
	_, err := s.client.Chat.Get(ctx, req.ChatID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to verify chat existence: %w", err)
	}

	messageID := uuid.New().String()
	msg, err := s.client.ChatUserMessage.Create().
		SetID(messageID).
		SetChatID(req.ChatID).
		SetContent(req.Content).
		SetAuthor(req.Author).
		SetCreatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to add chat message: %w", err)
	}

	return msg, nil
}

// GetChatHistory retrieves all messages and response stages for a chat
func (s *ChatService) GetChatHistory(ctx context.Context, chatID string) (*models.ChatHistoryResponse, error) {
	if chatID == "" {
		return nil, NewValidationError("chatID", "required")
	}

	chatObj, err := s.client.Chat.Query().
		Where(chat.IDEQ(chatID)).
		WithUserMessages().
		WithStages().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}

	return &models.ChatHistoryResponse{
		Chat:         chatObj,
		UserMessages: chatObj.Edges.UserMessages,
		Stages:       chatObj.Edges.Stages,
	}, nil
}

// BuildChatContext builds context from parent session artifacts
func (s *ChatService) BuildChatContext(ctx context.Context, chatID string) (string, error) {
	if chatID == "" {
		return "", NewValidationError("chatID", "required")
	}

	// Get chat with parent session
	chatObj, err := s.client.Chat.Query().
		Where(chat.IDEQ(chatID)).
		WithSession(func(q *ent.AlertSessionQuery) {
			q.WithStages().WithTimelineEvents()
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("failed to get chat: %w", err)
	}

	// Build context from parent session's artifacts
	// This is a simplified implementation - in production, this would be more sophisticated
	chatContext := fmt.Sprintf("Original Alert: %s\n\n", chatObj.Edges.Session.AlertData)

	if chatObj.Edges.Session.FinalAnalysis != nil {
		chatContext += fmt.Sprintf("Investigation Summary: %s\n\n", *chatObj.Edges.Session.FinalAnalysis)
	}

	return chatContext, nil
}
