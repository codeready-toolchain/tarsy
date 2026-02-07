package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// SingleCallController makes a single LLM call and returns the response.
// Used for Phase 3.1 validation only. Real controllers in Phase 3.2.
type SingleCallController struct{}

// NewSingleCallController creates a new single-call controller.
func NewSingleCallController() *SingleCallController {
	return &SingleCallController{}
}

// Run executes a single LLM call without tools.
func (c *SingleCallController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	startTime := time.Now()

	// 1. Build initial messages
	messages := c.buildMessages(execCtx, prevStageContext)

	// 2. Store system + user messages in DB
	for i, msg := range messages {
		_, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      execCtx.SessionID,
			StageID:        execCtx.StageID,
			ExecutionID:    execCtx.ExecutionID,
			SequenceNumber: i + 1,
			Role:           message.Role(msg.Role),
			Content:        msg.Content,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to store message: %w", err)
		}
	}

	// 3. Create TimelineEvent for streaming (empty content, filled on completion)
	event, err := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: len(messages) + 1,
		EventType:      timelineevent.EventTypeFinalAnalysis,
		Content:        "",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create timeline event: %w", err)
	}

	// 4. Call LLM via gRPC
	var fullText strings.Builder
	var thinkingText strings.Builder
	var usage *agent.TokenUsage

	stream, err := execCtx.LLMClient.Generate(ctx, &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil, // No tools in Phase 3.1
	})
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	for chunk := range stream {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			fullText.WriteString(c.Content)
		case *agent.ThinkingChunk:
			thinkingText.WriteString(c.Content)
		case *agent.UsageChunk:
			usage = &agent.TokenUsage{
				InputTokens:    int(c.InputTokens),
				OutputTokens:   int(c.OutputTokens),
				TotalTokens:    int(c.TotalTokens),
				ThinkingTokens: int(c.ThinkingTokens),
			}
		case *agent.ErrorChunk:
			return nil, fmt.Errorf("LLM error: %s (code: %s)", c.Message, c.Code)
		}
	}

	// 5. Complete TimelineEvent
	if err := execCtx.Services.Timeline.CompleteTimelineEvent(
		ctx, event.ID, fullText.String(), nil, nil,
	); err != nil {
		return nil, fmt.Errorf("failed to complete timeline event: %w", err)
	}

	// 6. Store assistant message
	assistantMsg, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
		SessionID:      execCtx.SessionID,
		StageID:        execCtx.StageID,
		ExecutionID:    execCtx.ExecutionID,
		SequenceNumber: len(messages) + 1,
		Role:           message.RoleAssistant,
		Content:        fullText.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to store assistant message: %w", err)
	}

	// 7. Create LLMInteraction debug record
	durationMs := int(time.Since(startTime).Milliseconds())
	var thinkingContentPtr *string
	if thinkingText.Len() > 0 {
		s := thinkingText.String()
		thinkingContentPtr = &s
	}
	var inputTokens, outputTokens, totalTokens *int
	if usage != nil {
		inputTokens = &usage.InputTokens
		outputTokens = &usage.OutputTokens
		totalTokens = &usage.TotalTokens
	}

	_, err = execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         execCtx.StageID,
		ExecutionID:     execCtx.ExecutionID,
		InteractionType: "iteration",
		ModelName:       execCtx.Config.LLMProvider.Model,
		LastMessageID:   &assistantMsg.ID,
		LLMRequest:      map[string]any{"messages_count": len(messages)},
		LLMResponse:     map[string]any{"text_length": fullText.Len()},
		ThinkingContent: thinkingContentPtr,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		DurationMs:      &durationMs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM interaction: %w", err)
	}

	// 8. Return result
	tokenUsage := agent.TokenUsage{}
	if usage != nil {
		tokenUsage = *usage
	}

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: fullText.String(),
		TokensUsed:    tokenUsage,
	}, nil
}

func (c *SingleCallController) buildMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	messages := []agent.ConversationMessage{
		{
			Role: "system",
			Content: fmt.Sprintf("You are %s, an AI SRE agent.\n\n%s",
				execCtx.AgentName, execCtx.Config.CustomInstructions),
		},
	}

	var userContent strings.Builder
	if prevStageContext != "" {
		userContent.WriteString("Previous investigation context:\n")
		userContent.WriteString(prevStageContext)
		userContent.WriteString("\n\nContinue the investigation based on the alert below.\n\n")
	}
	userContent.WriteString("## Alert Data\n\n")
	userContent.WriteString(execCtx.AlertData)

	messages = append(messages, agent.ConversationMessage{
		Role:    "user",
		Content: userContent.String(),
	})

	return messages
}

