package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/chat"
	"github.com/google/uuid"
	"github.com/codeready-toolchain/tarsy/pkg/models"
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
	context := fmt.Sprintf("Original Alert: %s\n\n", chatObj.Edges.Session.AlertData)
	
	if chatObj.Edges.Session.FinalAnalysis != nil {
		context += fmt.Sprintf("Investigation Summary: %s\n\n", *chatObj.Edges.Session.FinalAnalysis)
	}

	return context, nil
}
