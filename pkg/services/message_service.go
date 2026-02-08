package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/schema"
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
func (s *MessageService) CreateMessage(ctx context.Context, req models.CreateMessageRequest) (*ent.Message, error) {
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
	if string(req.Role) == "" {
		return nil, NewValidationError("role", "required")
	}
	if err := message.RoleValidator(req.Role); err != nil {
		return nil, NewValidationError("role", fmt.Sprintf("invalid role %q: %v", req.Role, err))
	}
	// Content is required for most messages, but assistant messages that
	// contain tool calls can legally have empty content (the LLM responds
	// with only tool invocations and no accompanying text).
	if req.Content == "" && !(req.Role == message.RoleAssistant && len(req.ToolCalls) > 0) {
		return nil, NewValidationError("content", "required")
	}

	messageID := uuid.New().String()
	builder := s.client.Message.Create().
		SetID(messageID).
		SetSessionID(req.SessionID).
		SetStageID(req.StageID).
		SetExecutionID(req.ExecutionID).
		SetSequenceNumber(req.SequenceNumber).
		SetRole(req.Role).
		SetContent(req.Content).
		SetCreatedAt(time.Now())

	// Tool-related fields
	if len(req.ToolCalls) > 0 {
		toolCalls := make([]schema.MessageToolCall, len(req.ToolCalls))
		for i, tc := range req.ToolCalls {
			toolCalls[i] = schema.MessageToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
		}
		builder = builder.SetToolCalls(toolCalls)
	}
	if req.ToolCallID != "" {
		builder = builder.SetToolCallID(req.ToolCallID)
	}
	if req.ToolName != "" {
		builder = builder.SetToolName(req.ToolName)
	}

	msg, err := builder.Save(ctx)
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
