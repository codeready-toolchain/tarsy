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

// DeleteChatMessage removes a chat user message by ID.
// Used to clean up orphaned messages when async submission is rejected.
func (s *ChatService) DeleteChatMessage(httpCtx context.Context, messageID string) error {
	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()
	return s.client.ChatUserMessage.DeleteOneID(messageID).Exec(ctx)
}

// GetOrCreateChat returns the existing chat for a session, or creates one if
// it doesn't exist yet. Returns (chat, created, error) where created indicates
// whether a new chat was created.
func (s *ChatService) GetOrCreateChat(httpCtx context.Context, sessionID, author string) (*ent.Chat, bool, error) {
	if sessionID == "" {
		return nil, false, NewValidationError("session_id", "required")
	}
	if author == "" {
		return nil, false, NewValidationError("author", "required")
	}

	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	// Try to find existing chat
	existing, err := s.client.Chat.Query().
		Where(chat.SessionIDEQ(sessionID)).
		Only(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, fmt.Errorf("failed to query chat: %w", err)
	}

	// No chat exists — create one
	session, err := s.client.AlertSession.Get(ctx, sessionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, false, ErrNotFound
		}
		return nil, false, fmt.Errorf("failed to get session: %w", err)
	}

	chatID := uuid.New().String()
	chatObj, err := s.client.Chat.Create().
		SetID(chatID).
		SetSessionID(sessionID).
		SetCreatedAt(time.Now()).
		SetCreatedBy(author).
		SetChainID(session.ChainID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			// Race: another request created the chat first — fetch it
			existing, queryErr := s.client.Chat.Query().
				Where(chat.SessionIDEQ(sessionID)).
				Only(ctx)
			if queryErr != nil {
				return nil, false, fmt.Errorf("failed to query chat after constraint error: %w", queryErr)
			}
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("failed to create chat: %w", err)
	}

	return chatObj, true, nil
}

// GetChatBySessionID returns the chat for a session, or nil if none exists.
func (s *ChatService) GetChatBySessionID(httpCtx context.Context, sessionID string) (*ent.Chat, error) {
	if sessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}

	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	chatObj, err := s.client.Chat.Query().
		Where(chat.SessionIDEQ(sessionID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // no chat — not an error
		}
		return nil, fmt.Errorf("failed to get chat by session: %w", err)
	}
	return chatObj, nil
}
