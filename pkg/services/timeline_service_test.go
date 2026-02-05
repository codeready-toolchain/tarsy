package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimelineService_CreateTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	t.Run("creates event with streaming status", func(t *testing.T) {
		req := models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 1,
			EventType:      "llm_thinking",
			Content:        "Analyzing...",
			Metadata:       map[string]any{"test": "metadata"},
		}

		event, err := timelineService.CreateTimelineEvent(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.Content, event.Content)
		assert.Equal(t, timelineevent.StatusStreaming, event.Status)
		assert.NotNil(t, event.CreatedAt)
		assert.NotNil(t, event.UpdatedAt)
	})
}

func TestTimelineService_UpdateTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	event, _ := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		SequenceNumber: 1,
		EventType:      "llm_thinking",
		Content:        "Starting...",
	})

	t.Run("updates content during streaming", func(t *testing.T) {
		err := timelineService.UpdateTimelineEvent(ctx, event.ID, "Processing... found issue")
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, "Processing... found issue", updated.Content)
		assert.Equal(t, timelineevent.StatusStreaming, updated.Status)
	})

	t.Run("returns ErrNotFound for missing event", func(t *testing.T) {
		err := timelineService.UpdateTimelineEvent(ctx, "nonexistent", "content")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestTimelineService_CompleteTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	event, _ := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		SequenceNumber: 1,
		EventType:      "llm_thinking",
		Content:        "Streaming...",
	})

	t.Run("completes event without links", func(t *testing.T) {
		err := timelineService.CompleteTimelineEvent(ctx, models.CompleteTimelineEventRequest{
			Content: "Final analysis complete",
		}, event.ID)
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, "Final analysis complete", updated.Content)
		assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
	})

	t.Run("completes event with links", func(t *testing.T) {
		// Create another event
		event2, _ := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			EventType:      "llm_thinking",
			Content:        "Streaming...",
		})

		// Create real interaction entities for foreign key constraints
		messageService := NewMessageService(client.Client)
		interactionService := NewInteractionService(client.Client, messageService)

		llmInt, _ := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})

		toolName := "test-tool"
		mcpInt, _ := interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "tool_call",
			ServerName:      "test-server",
			ToolName:        &toolName,
			ToolArguments:   map[string]any{},
			ToolResult:      map[string]any{},
		})

		err := timelineService.CompleteTimelineEvent(ctx, models.CompleteTimelineEventRequest{
			Content:          "Final analysis complete",
			LLMInteractionID: &llmInt.ID,
			MCPInteractionID: &mcpInt.ID,
		}, event2.ID)
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event2.ID)
		require.NoError(t, err)
		assert.Equal(t, "Final analysis complete", updated.Content)
		assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
		assert.Equal(t, llmInt.ID, *updated.LlmInteractionID)
		assert.Equal(t, mcpInt.ID, *updated.McpInteractionID)
	})
}

func TestTimelineService_GetTimelines(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := NewSessionService(client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, _ := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})

	stg, _ := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})

	exec, _ := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})

	// Create events
	for i := 1; i <= 3; i++ {
		_, _ = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: i,
			EventType:      "llm_thinking",
			Content:        "Event",
		})
	}

	t.Run("gets session timeline", func(t *testing.T) {
		events, err := timelineService.GetSessionTimeline(ctx, session.ID)
		require.NoError(t, err)
		assert.Len(t, events, 3)
		// Verify ordering
		assert.Equal(t, 1, events[0].SequenceNumber)
		assert.Equal(t, 2, events[1].SequenceNumber)
		assert.Equal(t, 3, events[2].SequenceNumber)
	})

	t.Run("gets stage timeline", func(t *testing.T) {
		events, err := timelineService.GetStageTimeline(ctx, stg.ID)
		require.NoError(t, err)
		assert.Len(t, events, 3)
	})

	t.Run("gets agent timeline", func(t *testing.T) {
		events, err := timelineService.GetAgentTimeline(ctx, exec.ID)
		require.NoError(t, err)
		assert.Len(t, events, 3)
	})
}
