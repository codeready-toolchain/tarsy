package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// InteractionService manages LLM and MCP interactions (trace data)
type InteractionService struct {
	client         *ent.Client
	messageService *MessageService
}

// NewInteractionService creates a new InteractionService
func NewInteractionService(client *ent.Client, messageService *MessageService) *InteractionService {
	return &InteractionService{
		client:         client,
		messageService: messageService,
	}
}

// CreateLLMInteraction creates a new LLM interaction
func (s *InteractionService) CreateLLMInteraction(httpCtx context.Context, req models.CreateLLMInteractionRequest) (*ent.LLMInteraction, error) {
	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	interactionID := uuid.New().String()
	builder := s.client.LLMInteraction.Create().
		SetID(interactionID).
		SetSessionID(req.SessionID).
		SetNillableStageID(req.StageID).
		SetNillableExecutionID(req.ExecutionID).
		SetInteractionType(llminteraction.InteractionType(req.InteractionType)).
		SetModelName(req.ModelName).
		SetLlmRequest(req.LLMRequest).
		SetLlmResponse(req.LLMResponse).
		SetCreatedAt(time.Now())

	if req.LastMessageID != nil {
		builder = builder.SetLastMessageID(*req.LastMessageID)
	}
	if req.ThinkingContent != nil {
		builder = builder.SetThinkingContent(*req.ThinkingContent)
	}
	if req.ResponseMetadata != nil {
		builder = builder.SetResponseMetadata(req.ResponseMetadata)
	}
	if req.InputTokens != nil {
		builder = builder.SetInputTokens(*req.InputTokens)
	}
	if req.OutputTokens != nil {
		builder = builder.SetOutputTokens(*req.OutputTokens)
	}
	if req.TotalTokens != nil {
		builder = builder.SetTotalTokens(*req.TotalTokens)
	}
	if req.DurationMs != nil {
		builder = builder.SetDurationMs(*req.DurationMs)
	}
	if req.ErrorMessage != nil {
		builder = builder.SetErrorMessage(*req.ErrorMessage)
	}

	interaction, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM interaction: %w", err)
	}

	return interaction, nil
}

// CreateMCPInteraction creates a new MCP interaction
func (s *InteractionService) CreateMCPInteraction(httpCtx context.Context, req models.CreateMCPInteractionRequest) (*ent.MCPInteraction, error) {
	ctx, cancel := context.WithTimeout(httpCtx, 5*time.Second)
	defer cancel()

	interactionID := uuid.New().String()
	builder := s.client.MCPInteraction.Create().
		SetID(interactionID).
		SetSessionID(req.SessionID).
		SetStageID(req.StageID).
		SetExecutionID(req.ExecutionID).
		SetInteractionType(mcpinteraction.InteractionType(req.InteractionType)).
		SetServerName(req.ServerName).
		SetCreatedAt(time.Now())

	if req.ToolName != nil {
		builder = builder.SetToolName(*req.ToolName)
	}
	if req.ToolArguments != nil {
		builder = builder.SetToolArguments(req.ToolArguments)
	}
	if req.ToolResult != nil {
		builder = builder.SetToolResult(req.ToolResult)
	}
	if req.AvailableTools != nil {
		builder = builder.SetAvailableTools(req.AvailableTools)
	}
	if req.DurationMs != nil {
		builder = builder.SetDurationMs(*req.DurationMs)
	}
	if req.ErrorMessage != nil {
		builder = builder.SetErrorMessage(*req.ErrorMessage)
	}

	interaction, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP interaction: %w", err)
	}

	return interaction, nil
}

// GetLLMInteractionsList retrieves interaction metadata for list view
func (s *InteractionService) GetLLMInteractionsList(ctx context.Context, sessionID string) ([]*ent.LLMInteraction, error) {
	interactions, err := s.client.LLMInteraction.Query().
		Where(llminteraction.SessionIDEQ(sessionID)).
		Order(ent.Asc(llminteraction.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM interactions: %w", err)
	}

	return interactions, nil
}

// GetLLMInteractionDetail retrieves full interaction details
func (s *InteractionService) GetLLMInteractionDetail(ctx context.Context, interactionID string) (*ent.LLMInteraction, error) {
	interaction, err := s.client.LLMInteraction.Get(ctx, interactionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get LLM interaction: %w", err)
	}

	return interaction, nil
}

// GetMCPInteractionsList retrieves interaction metadata for list view
func (s *InteractionService) GetMCPInteractionsList(ctx context.Context, sessionID string) ([]*ent.MCPInteraction, error) {
	interactions, err := s.client.MCPInteraction.Query().
		Where(mcpinteraction.SessionIDEQ(sessionID)).
		Order(ent.Asc(mcpinteraction.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP interactions: %w", err)
	}

	return interactions, nil
}

// GetMCPInteractionDetail retrieves full interaction details
func (s *InteractionService) GetMCPInteractionDetail(ctx context.Context, interactionID string) (*ent.MCPInteraction, error) {
	interaction, err := s.client.MCPInteraction.Get(ctx, interactionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get MCP interaction: %w", err)
	}

	return interaction, nil
}

// ReconstructConversation rebuilds the conversation from messages.
// Uses the messages_count stored in llm_request metadata to compute a sequence
// range, so that only messages belonging to this specific LLM interaction are
// returned (not messages from other controllers sharing the same execution).
func (s *InteractionService) ReconstructConversation(ctx context.Context, interactionID string) ([]*ent.Message, error) {
	interaction, err := s.GetLLMInteractionDetail(ctx, interactionID)
	if err != nil {
		return nil, err
	}

	if interaction.LastMessageID == nil || interaction.ExecutionID == nil {
		return []*ent.Message{}, nil
	}

	lastMessage, err := s.client.Message.Get(ctx, *interaction.LastMessageID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("last message %s not found: %w", *interaction.LastMessageID, ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get last message: %w", err)
	}

	// Compute the first message's sequence from the messages_count metadata.
	// messages_count is the number of input messages sent to the LLM (excluding
	// the assistant response), so first_seq = last_seq - messages_count.
	if minSeq, ok := firstMessageSequence(interaction.LlmRequest, lastMessage.SequenceNumber); ok {
		return s.messageService.GetMessagesInSequenceRange(
			ctx, *interaction.ExecutionID, minSeq, lastMessage.SequenceNumber,
		)
	}

	// Fallback for older interactions without messages_count metadata.
	return s.messageService.GetMessagesUpToSequence(
		ctx, *interaction.ExecutionID, lastMessage.SequenceNumber,
	)
}

// firstMessageSequence extracts messages_count from llm_request metadata and
// computes the first message sequence number. Returns (minSeq, true) on success.
func firstMessageSequence(llmRequest map[string]interface{}, lastSeq int) (int, bool) {
	if llmRequest == nil {
		return 0, false
	}
	raw, ok := llmRequest["messages_count"]
	if !ok {
		return 0, false
	}
	// JSON numbers deserialize as float64 in Go's map[string]interface{}.
	count, ok := raw.(float64)
	if !ok || count <= 0 {
		return 0, false
	}
	minSeq := lastSeq - int(count)
	if minSeq < 1 {
		minSeq = 1
	}
	return minSeq, true
}
