package controller

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// storeMessages persists initial conversation messages to DB.
func storeMessages(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	msgSeq *int,
) error {
	for _, msg := range messages {
		*msgSeq++
		_, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      execCtx.SessionID,
			StageID:        execCtx.StageID,
			ExecutionID:    execCtx.ExecutionID,
			SequenceNumber: *msgSeq,
			Role:           message.Role(msg.Role),
			Content:        msg.Content,
		})
		if err != nil {
			return fmt.Errorf("failed to store message: %w", err)
		}
	}
	return nil
}

// storeAssistantMessage persists an assistant text response to DB.
func storeAssistantMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	resp *LLMResponse,
	msgSeq *int,
) (*ent.Message, error) {
	if resp == nil {
		return nil, fmt.Errorf("storeAssistantMessage: resp is nil")
	}
	content := resp.Text
	if content == "" {
		content = "(empty response)"
	}
	*msgSeq++
	return execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleAssistant,
		Content:        content,
	})
}

// storeAssistantMessageWithToolCalls persists an assistant message with tool calls.
func storeAssistantMessageWithToolCalls(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	resp *LLMResponse,
	msgSeq *int,
) (*ent.Message, error) {
	if resp == nil {
		return nil, fmt.Errorf("storeAssistantMessageWithToolCalls: resp is nil")
	}
	// LLMs commonly return empty text when making tool calls. The DB schema
	// requires non-empty content, so use a placeholder for the audit record.
	content := resp.Text
	if content == "" {
		content = "(tool calls only)"
	}
	*msgSeq++

	var toolCallData []models.ToolCallData
	for _, tc := range resp.ToolCalls {
		toolCallData = append(toolCallData, models.ToolCallData{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}

	return execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleAssistant,
		Content:        content,
		ToolCalls:      toolCallData,
	})
}

// storeToolResultMessage persists a tool result message to DB.
// Logs slog.Error on failure but does not abort the investigation loop —
// the in-memory messages slice is authoritative during execution.
func storeToolResultMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	callID string,
	toolName string,
	content string,
	msgSeq *int,
) {
	// DB schema requires non-empty content. MCP tools may return empty results
	// (e.g. a namespace list with no items). Use a placeholder to satisfy the
	// constraint while preserving the tool call in the audit trail.
	if content == "" {
		content = "(empty result)"
	}
	*msgSeq++
	if _, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleTool,
		Content:        content,
		ToolCallID:     callID,
		ToolName:       toolName,
	}); err != nil {
		slog.Error("Failed to store tool result message",
			"session_id", execCtx.SessionID, "tool", toolName, "error", err)
	}
}

// storeObservationMessage persists a ReAct observation as a user message.
// Logs slog.Error on failure but does not abort the investigation loop —
// the in-memory messages slice is authoritative during execution.
func storeObservationMessage(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	observation string,
	msgSeq *int,
) {
	if observation == "" {
		observation = "(empty observation)"
	}
	*msgSeq++
	if _, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: *msgSeq,
		Role:           message.RoleUser,
		Content:        observation,
	}); err != nil {
		slog.Error("Failed to store observation message",
			"session_id", execCtx.SessionID, "error", err)
	}
}
