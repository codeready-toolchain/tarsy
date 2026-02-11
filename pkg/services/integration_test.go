package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceIntegration tests multiple services working together
func TestServiceIntegration(t *testing.T) {
	client := testdb.NewTestClient(t)
	ctx := context.Background()

	// Initialize all services
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	messageService := NewMessageService(client.Client)
	timelineService := NewTimelineService(client.Client)
	interactionService := NewInteractionService(client.Client, messageService)
	eventService := NewEventService(client.Client)

	t.Run("full session lifecycle", func(t *testing.T) {
		// 1. Create session with initial stage
		sessionReq := models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "Pod crashing in production namespace",
			AgentType: "kubernetes",
			AlertType: "pod-crash",
			ChainID:   "k8s-deep-analysis",
			Author:    "test@example.com",
		}
		session, err := sessionService.CreateSession(ctx, sessionReq)
		require.NoError(t, err)
		assert.Equal(t, sessionReq.SessionID, session.ID)

		// 2. Get stages - should have initial stage
		stages, err := stageService.GetStagesBySession(ctx, session.ID, true)
		require.NoError(t, err)
		assert.Len(t, stages, 1)
		initialStage := stages[0]
		assert.Len(t, initialStage.Edges.AgentExecutions, 1)
		initialExec := initialStage.Edges.AgentExecutions[0]

		// 3. Add messages to the agent conversation
		_, err = messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        initialStage.ID,
			ExecutionID:    initialExec.ID,
			SequenceNumber: 1,
			Role:           message.RoleSystem,
			Content:        "You are a Kubernetes troubleshooting agent",
		})
		require.NoError(t, err)

		msg2, err := messageService.CreateMessage(ctx, models.CreateMessageRequest{
			SessionID:      session.ID,
			StageID:        initialStage.ID,
			ExecutionID:    initialExec.ID,
			SequenceNumber: 2,
			Role:           message.RoleUser,
			Content:        "Analyze the pod crash",
		})
		require.NoError(t, err)

		// 4. Create timeline events
		thinkingEvent, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        &initialStage.ID,
			ExecutionID:    &initialExec.ID,
			SequenceNumber: 1,
			EventType:      timelineevent.EventTypeLlmThinking,
			Content:        "Analyzing pod status...",
		})
		require.NoError(t, err)

		// 5. Update timeline event (streaming)
		err = timelineService.UpdateTimelineEvent(ctx, thinkingEvent.ID, "Analyzing pod status... checking logs...")
		require.NoError(t, err)

		// 6. Create LLM interaction with last_message_id
		llmInteraction, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         initialStage.ID,
			ExecutionID:     initialExec.ID,
			InteractionType: "iteration",
			ModelName:       "gemini-2.0-flash",
			LastMessageID:   &msg2.ID,
			LLMRequest:      map[string]any{"prompt": "analyze"},
			LLMResponse:     map[string]any{"text": "analysis result"},
			InputTokens:     intPtr(100),
			OutputTokens:    intPtr(200),
		})
		require.NoError(t, err)

		// 7. Complete timeline event with link to LLM interaction
		err = timelineService.CompleteTimelineEvent(ctx, thinkingEvent.ID, "Analysis complete: Pod crashed due to OOM", &llmInteraction.ID, nil)
		require.NoError(t, err)

		// 8. Create MCP interaction
		toolName := "kubectl-get-pods"
		_, err = interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
			SessionID:       session.ID,
			StageID:         initialStage.ID,
			ExecutionID:     initialExec.ID,
			InteractionType: "tool_call",
			ServerName:      "kubernetes-server",
			ToolName:        &toolName,
			ToolArguments:   map[string]any{"namespace": "production"},
			ToolResult:      map[string]any{"pods": []string{"pod-1", "pod-2"}},
		})
		require.NoError(t, err)

		// 9. Update agent status to completed
		err = stageService.UpdateAgentExecutionStatus(ctx, initialExec.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, initialExec.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)

		// 10. Aggregate stage status
		err = stageService.UpdateStageStatus(ctx, initialStage.ID)
		require.NoError(t, err)

		// 11. Verify timeline
		timeline, err := timelineService.GetSessionTimeline(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, timeline, 1)

		// 12. Verify messages
		messages, err := messageService.GetExecutionMessages(ctx, initialExec.ID)
		require.NoError(t, err)
		assert.Len(t, messages, 2)

		// 13. Verify interactions
		llmInteractions, err := interactionService.GetLLMInteractionsList(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, llmInteractions, 1)

		mcpInteractions, err := interactionService.GetMCPInteractionsList(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, mcpInteractions, 1)

		// 14. Reconstruct conversation
		conversation, err := interactionService.ReconstructConversation(ctx, llmInteraction.ID)
		require.NoError(t, err)
		assert.Len(t, conversation, 2)

		// 15. Create event for WebSocket
		_, err = eventService.CreateEvent(ctx, models.CreateEventRequest{
			SessionID: session.ID,
			Channel:   "session:" + session.ID,
			Payload:   map[string]any{"type": "status_update", "status": "completed"},
		})
		require.NoError(t, err)

		// 16. Cleanup events
		count, err := eventService.CleanupSessionEvents(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})
}

func intPtr(i int) *int {
	return &i
}
