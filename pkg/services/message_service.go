package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// MessageService manages LLM conversation messages
type MessageService struct {
	client *ent.Client
}

// NewMessageService creates a new MessageService
func NewMessageService(client *ent.Client) *MessageService {
	return &MessageService{client: client}
}

// CreateMessage creates a new message
func (s *MessageService) CreateMessage(_ context.Context, req models.CreateMessageRequest) (*ent.Message, error) {
	// Validate input
	if req.SessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}
	if req.StageID == "" {
		return nil, NewValidationError("stage_id", "required")
	}
	if req.ExecutionID == "" {
		return nil, NewValidationError("execution_id", "required")
	}
	if req.Role == "" {
		return nil, NewValidationError("role", "required")
	}
	if req.Content == "" {
		return nil, NewValidationError("content", "required")
	}

	// Use background context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messageID := uuid.New().String()
	msg, err := s.client.Message.Create().
		SetID(messageID).
		SetSessionID(req.SessionID).
		SetStageID(req.StageID).
		SetExecutionID(req.ExecutionID).
		SetSequenceNumber(req.SequenceNumber).
		SetRole(message.Role(req.Role)).
		SetContent(req.Content).
		SetCreatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return msg, nil
}

// GetExecutionMessages retrieves all messages for an agent execution in order
func (s *MessageService) GetExecutionMessages(ctx context.Context, executionID string) ([]*ent.Message, error) {
	messages, err := s.client.Message.Query().
		Where(message.ExecutionIDEQ(executionID)).
		Order(ent.Asc(message.FieldSequenceNumber)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution messages: %w", err)
	}

	return messages, nil
}

// GetMessagesUpToSequence retrieves messages up to a specific sequence number
func (s *MessageService) GetMessagesUpToSequence(ctx context.Context, executionID string, sequenceNumber int) ([]*ent.Message, error) {
	messages, err := s.client.Message.Query().
		Where(
			message.ExecutionIDEQ(executionID),
			message.SequenceNumberLTE(sequenceNumber),
		).
		Order(ent.Asc(message.FieldSequenceNumber)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	return messages, nil
}

// GetStageMessages retrieves all messages for a stage across all agent executions
func (s *MessageService) GetStageMessages(ctx context.Context, stageID string) ([]*ent.Message, error) {
	messages, err := s.client.Message.Query().
		Where(message.StageIDEQ(stageID)).
		Order(ent.Asc(message.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stage messages: %w", err)
	}

	return messages, nil
}
