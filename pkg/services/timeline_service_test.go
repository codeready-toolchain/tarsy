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
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	t.Run("creates event with streaming status", func(t *testing.T) {
		req := models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 1,
			EventType:      timelineevent.EventTypeLlmThinking,
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

	t.Run("creates event with empty content for streaming", func(t *testing.T) {
		req := models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			EventType:      timelineevent.EventTypeLlmThinking,
			Content:        "", // Empty content is now allowed for streaming
		}

		event, err := timelineService.CreateTimelineEvent(ctx, req)
		require.NoError(t, err)
		assert.Empty(t, event.Content)
		assert.Equal(t, timelineevent.StatusStreaming, event.Status)
	})

	t.Run("validates required fields", func(t *testing.T) {
		validReq := models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 3,
			EventType:      timelineevent.EventTypeLlmThinking,
			Content:        "test",
		}

		// Missing SessionID
		req := validReq
		req.SessionID = ""
		_, err := timelineService.CreateTimelineEvent(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))

		// StageID and ExecutionID are optional (session-level events omit them)

		// Invalid SequenceNumber (zero)
		req = validReq
		req.SequenceNumber = 0
		_, err = timelineService.CreateTimelineEvent(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))

		// Invalid SequenceNumber (negative)
		req = validReq
		req.SequenceNumber = -1
		_, err = timelineService.CreateTimelineEvent(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))

		// Missing EventType
		req = validReq
		req.EventType = ""
		_, err = timelineService.CreateTimelineEvent(ctx, req)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestTimelineService_UpdateTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	event, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		SequenceNumber: 1,
		EventType:      timelineevent.EventTypeLlmThinking,
		Content:        "Starting...",
	})
	require.NoError(t, err)

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

	t.Run("validates empty eventID", func(t *testing.T) {
		err := timelineService.UpdateTimelineEvent(ctx, "", "content")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("validates empty content", func(t *testing.T) {
		err := timelineService.UpdateTimelineEvent(ctx, event.ID, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestTimelineService_CompleteTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	event, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		SequenceNumber: 1,
		EventType:      timelineevent.EventTypeLlmThinking,
		Content:        "Streaming...",
	})
	require.NoError(t, err)

	t.Run("completes event without links", func(t *testing.T) {
		err := timelineService.CompleteTimelineEvent(ctx, event.ID, "Final analysis complete", nil, nil)
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, "Final analysis complete", updated.Content)
		assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
	})

	t.Run("completes event with links", func(t *testing.T) {
		// Create another event
		event2, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 2,
			EventType:      timelineevent.EventTypeLlmThinking,
			Content:        "Streaming...",
		})
		require.NoError(t, err)

		// Create real interaction entities for foreign key constraints
		messageService := NewMessageService(client.Client)
		interactionService := NewInteractionService(client.Client, messageService)

		llmInt, err := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "iteration",
			ModelName:       "test-model",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
		})
		require.NoError(t, err)

		toolName := "test-tool"
		mcpInt, err := interactionService.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
			SessionID:       session.ID,
			StageID:         stg.ID,
			ExecutionID:     exec.ID,
			InteractionType: "tool_call",
			ServerName:      "test-server",
			ToolName:        &toolName,
			ToolArguments:   map[string]any{},
			ToolResult:      map[string]any{},
		})
		require.NoError(t, err)

		err = timelineService.CompleteTimelineEvent(ctx, event2.ID, "Final analysis complete", &llmInt.ID, &mcpInt.ID)
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event2.ID)
		require.NoError(t, err)
		assert.Equal(t, "Final analysis complete", updated.Content)
		assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
		assert.Equal(t, llmInt.ID, *updated.LlmInteractionID)
		assert.Equal(t, mcpInt.ID, *updated.McpInteractionID)
	})

	t.Run("validates required fields", func(t *testing.T) {
		// Test empty eventID
		err := timelineService.CompleteTimelineEvent(ctx, "", "Final content", nil, nil)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))

		// Test empty Content
		err = timelineService.CompleteTimelineEvent(ctx, event.ID, "", nil, nil)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestTimelineService_CompleteTimelineEventWithMetadata(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	t.Run("merges metadata with existing", func(t *testing.T) {
		event, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 10,
			EventType:      timelineevent.EventTypeLlmToolCall,
			Content:        "",
			Metadata: map[string]interface{}{
				"server_name": "kubernetes-server",
				"tool_name":   "get_pods",
				"arguments":   `{"namespace": "default"}`,
			},
		})
		require.NoError(t, err)

		// Complete with additional metadata
		err = timelineService.CompleteTimelineEventWithMetadata(ctx, event.ID,
			"pod list output here",
			map[string]interface{}{"is_error": false},
			nil, nil)
		require.NoError(t, err)

		// Verify merged metadata
		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, "pod list output here", updated.Content)
		assert.Equal(t, timelineevent.StatusCompleted, updated.Status)
		assert.Equal(t, "kubernetes-server", updated.Metadata["server_name"])
		assert.Equal(t, "get_pods", updated.Metadata["tool_name"])
		assert.Equal(t, false, updated.Metadata["is_error"])
	})

	t.Run("new metadata overrides existing keys", func(t *testing.T) {
		event, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: 11,
			EventType:      timelineevent.EventTypeLlmToolCall,
			Content:        "",
			Metadata: map[string]interface{}{
				"key1": "original",
				"key2": "keep",
			},
		})
		require.NoError(t, err)

		err = timelineService.CompleteTimelineEventWithMetadata(ctx, event.ID,
			"result",
			map[string]interface{}{"key1": "overridden", "key3": "new"},
			nil, nil)
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, "overridden", updated.Metadata["key1"])
		assert.Equal(t, "keep", updated.Metadata["key2"])
		assert.Equal(t, "new", updated.Metadata["key3"])
	})

	t.Run("validates required fields", func(t *testing.T) {
		err := timelineService.CompleteTimelineEventWithMetadata(ctx, "", "content", nil, nil, nil)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))

		err = timelineService.CompleteTimelineEventWithMetadata(ctx, "some-id", "", nil, nil, nil)
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestTimelineService_FailTimelineEvent(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	event, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		SequenceNumber: 1,
		EventType:      timelineevent.EventTypeLlmThinking,
		Content:        "",
	})
	require.NoError(t, err)
	assert.Equal(t, timelineevent.StatusStreaming, event.Status)

	t.Run("marks event as failed with error content", func(t *testing.T) {
		err := timelineService.FailTimelineEvent(ctx, event.ID, "Streaming failed: LLM error: rate limit exceeded")
		require.NoError(t, err)

		updated, err := client.TimelineEvent.Get(ctx, event.ID)
		require.NoError(t, err)
		assert.Equal(t, timelineevent.StatusFailed, updated.Status)
		assert.Equal(t, "Streaming failed: LLM error: rate limit exceeded", updated.Content)
	})

	t.Run("returns ErrNotFound for missing event", func(t *testing.T) {
		err := timelineService.FailTimelineEvent(ctx, "nonexistent-id", "error content")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("validates empty eventID", func(t *testing.T) {
		err := timelineService.FailTimelineEvent(ctx, "", "error content")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("validates empty content", func(t *testing.T) {
		err := timelineService.FailTimelineEvent(ctx, event.ID, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestTimelineService_GetTimelines(t *testing.T) {
	client := testdb.NewTestClient(t)
	timelineService := NewTimelineService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	stageService := NewStageService(client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	})
	require.NoError(t, err)

	// Create events
	for i := 1; i <= 3; i++ {
		_, err := timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
			SessionID:      session.ID,
			StageID:        stg.ID,
			ExecutionID:    exec.ID,
			SequenceNumber: i,
			EventType:      timelineevent.EventTypeLlmThinking,
			Content:        "Event",
		})
		require.NoError(t, err)
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

	t.Run("validates empty sessionID", func(t *testing.T) {
		_, err := timelineService.GetSessionTimeline(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("gets stage timeline", func(t *testing.T) {
		events, err := timelineService.GetStageTimeline(ctx, stg.ID)
		require.NoError(t, err)
		assert.Len(t, events, 3)
	})

	t.Run("validates empty stageID", func(t *testing.T) {
		_, err := timelineService.GetStageTimeline(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("gets agent timeline", func(t *testing.T) {
		events, err := timelineService.GetAgentTimeline(ctx, exec.ID)
		require.NoError(t, err)
		assert.Len(t, events, 3)
	})

	t.Run("validates empty executionID", func(t *testing.T) {
		_, err := timelineService.GetAgentTimeline(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}
