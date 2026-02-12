package api

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/ent/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// buildDebugListResponse tests
// ============================================================================

func TestBuildDebugListResponse_Empty(t *testing.T) {
	resp := buildDebugListResponse(nil, nil, nil)
	require.NotNil(t, resp)
	assert.Empty(t, resp.Stages)
}

func TestBuildDebugListResponse_StageWithNoInteractions(t *testing.T) {
	stages := []*ent.Stage{
		{
			ID:        "stg-1",
			StageName: "investigation",
			Edges: ent.StageEdges{
				AgentExecutions: []*ent.AgentExecution{
					{ID: "exec-1", AgentName: "DataCollector", AgentIndex: 1},
				},
			},
		},
	}

	resp := buildDebugListResponse(stages, nil, nil)
	require.Len(t, resp.Stages, 1)
	assert.Equal(t, "stg-1", resp.Stages[0].StageID)
	assert.Equal(t, "investigation", resp.Stages[0].StageName)
	require.Len(t, resp.Stages[0].Executions, 1)
	assert.Equal(t, "exec-1", resp.Stages[0].Executions[0].ExecutionID)
	assert.Equal(t, "DataCollector", resp.Stages[0].Executions[0].AgentName)
	assert.Empty(t, resp.Stages[0].Executions[0].LLMInteractions)
	assert.Empty(t, resp.Stages[0].Executions[0].MCPInteractions)
	// Verify empty slices (not nil) for clean JSON.
	assert.NotNil(t, resp.Stages[0].Executions[0].LLMInteractions)
	assert.NotNil(t, resp.Stages[0].Executions[0].MCPInteractions)
}

func TestBuildDebugListResponse_GroupingAndSorting(t *testing.T) {
	now := time.Now()

	stages := []*ent.Stage{
		{
			ID:        "stg-1",
			StageName: "investigation",
			Edges: ent.StageEdges{
				AgentExecutions: []*ent.AgentExecution{
					{ID: "exec-1", AgentName: "Agent1", AgentIndex: 1},
				},
			},
		},
		{
			ID:        "stg-2",
			StageName: "validation",
			Edges: ent.StageEdges{
				// Deliberately out of order to verify sorting.
				AgentExecutions: []*ent.AgentExecution{
					{ID: "exec-3", AgentName: "AgentB", AgentIndex: 2},
					{ID: "exec-2", AgentName: "AgentA", AgentIndex: 1},
				},
			},
		},
	}

	inputTokens := 100
	llmInteractions := []*ent.LLMInteraction{
		{ID: "llm-1", ExecutionID: "exec-1", InteractionType: llminteraction.InteractionTypeIteration, ModelName: "m", InputTokens: &inputTokens, CreatedAt: now},
		{ID: "llm-2", ExecutionID: "exec-2", InteractionType: llminteraction.InteractionTypeIteration, ModelName: "m", CreatedAt: now},
		{ID: "llm-3", ExecutionID: "exec-3", InteractionType: llminteraction.InteractionTypeIteration, ModelName: "m", CreatedAt: now},
	}

	toolName := "get_pods"
	mcpInteractions := []*ent.MCPInteraction{
		{ID: "mcp-1", ExecutionID: "exec-1", InteractionType: mcpinteraction.InteractionTypeToolCall, ServerName: "k8s", ToolName: &toolName, CreatedAt: now},
	}

	resp := buildDebugListResponse(stages, llmInteractions, mcpInteractions)

	// Two stages.
	require.Len(t, resp.Stages, 2)

	// Stage 1: investigation with one execution.
	assert.Equal(t, "stg-1", resp.Stages[0].StageID)
	assert.Equal(t, "investigation", resp.Stages[0].StageName)
	require.Len(t, resp.Stages[0].Executions, 1)
	assert.Equal(t, "exec-1", resp.Stages[0].Executions[0].ExecutionID)
	require.Len(t, resp.Stages[0].Executions[0].LLMInteractions, 1)
	assert.Equal(t, "llm-1", resp.Stages[0].Executions[0].LLMInteractions[0].ID)
	require.Len(t, resp.Stages[0].Executions[0].MCPInteractions, 1)
	assert.Equal(t, "mcp-1", resp.Stages[0].Executions[0].MCPInteractions[0].ID)

	// Stage 2: validation with two executions — sorted by agent_index.
	assert.Equal(t, "stg-2", resp.Stages[1].StageID)
	assert.Equal(t, "validation", resp.Stages[1].StageName)
	require.Len(t, resp.Stages[1].Executions, 2)
	// AgentA (index=1) should come before AgentB (index=2).
	assert.Equal(t, "exec-2", resp.Stages[1].Executions[0].ExecutionID)
	assert.Equal(t, "AgentA", resp.Stages[1].Executions[0].AgentName)
	assert.Equal(t, "exec-3", resp.Stages[1].Executions[1].ExecutionID)
	assert.Equal(t, "AgentB", resp.Stages[1].Executions[1].AgentName)

	// AgentA has one LLM, no MCP.
	require.Len(t, resp.Stages[1].Executions[0].LLMInteractions, 1)
	assert.Equal(t, "llm-2", resp.Stages[1].Executions[0].LLMInteractions[0].ID)
	assert.Empty(t, resp.Stages[1].Executions[0].MCPInteractions)

	// AgentB has one LLM, no MCP.
	require.Len(t, resp.Stages[1].Executions[1].LLMInteractions, 1)
	assert.Equal(t, "llm-3", resp.Stages[1].Executions[1].LLMInteractions[0].ID)
	assert.Empty(t, resp.Stages[1].Executions[1].MCPInteractions)
}

func TestBuildDebugListResponse_StageWithNoExecutions(t *testing.T) {
	stages := []*ent.Stage{
		{
			ID:        "stg-1",
			StageName: "empty-stage",
			// No AgentExecutions edge.
		},
	}

	resp := buildDebugListResponse(stages, nil, nil)
	require.Len(t, resp.Stages, 1)
	assert.Empty(t, resp.Stages[0].Executions)
	assert.NotNil(t, resp.Stages[0].Executions) // Not nil — clean JSON.
}

// ============================================================================
// toLLMListItem tests
// ============================================================================

func TestToLLMListItem(t *testing.T) {
	inputTokens := 100
	outputTokens := 30
	totalTokens := 130
	durationMs := 42
	now := time.Now()

	li := &ent.LLMInteraction{
		ID:              "int-1",
		InteractionType: llminteraction.InteractionTypeIteration,
		ModelName:       "gemini-2.0-flash",
		InputTokens:     &inputTokens,
		OutputTokens:    &outputTokens,
		TotalTokens:     &totalTokens,
		DurationMs:      &durationMs,
		CreatedAt:       now,
	}

	item := toLLMListItem(li)
	assert.Equal(t, "int-1", item.ID)
	assert.Equal(t, "iteration", item.InteractionType)
	assert.Equal(t, "gemini-2.0-flash", item.ModelName)
	assert.Equal(t, &inputTokens, item.InputTokens)
	assert.Equal(t, &outputTokens, item.OutputTokens)
	assert.Equal(t, &totalTokens, item.TotalTokens)
	assert.Equal(t, &durationMs, item.DurationMs)
	assert.Nil(t, item.ErrorMessage)
	assert.Equal(t, now.Format(time.RFC3339Nano), item.CreatedAt)
}

func TestToLLMListItem_ErrorMessage(t *testing.T) {
	errMsg := "rate limited"
	li := &ent.LLMInteraction{
		ID:              "int-err",
		InteractionType: llminteraction.InteractionTypeIteration,
		ModelName:       "test-model",
		ErrorMessage:    &errMsg,
		CreatedAt:       time.Now(),
	}

	item := toLLMListItem(li)
	require.NotNil(t, item.ErrorMessage)
	assert.Equal(t, "rate limited", *item.ErrorMessage)
}

// ============================================================================
// toMCPListItem tests
// ============================================================================

func TestToMCPListItem(t *testing.T) {
	toolName := "get_pods"
	durationMs := 15
	now := time.Now()

	mi := &ent.MCPInteraction{
		ID:              "mcp-1",
		InteractionType: mcpinteraction.InteractionTypeToolCall,
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		DurationMs:      &durationMs,
		CreatedAt:       now,
	}

	item := toMCPListItem(mi)
	assert.Equal(t, "mcp-1", item.ID)
	assert.Equal(t, "tool_call", item.InteractionType)
	assert.Equal(t, "kubernetes", item.ServerName)
	require.NotNil(t, item.ToolName)
	assert.Equal(t, "get_pods", *item.ToolName)
	assert.Equal(t, &durationMs, item.DurationMs)
	assert.Equal(t, now.Format(time.RFC3339Nano), item.CreatedAt)
}

func TestToMCPListItem_NilToolName(t *testing.T) {
	mi := &ent.MCPInteraction{
		ID:              "mcp-list",
		InteractionType: mcpinteraction.InteractionTypeToolList,
		ServerName:      "kubernetes",
		CreatedAt:       time.Now(),
	}

	item := toMCPListItem(mi)
	assert.Equal(t, "tool_list", item.InteractionType)
	assert.Nil(t, item.ToolName)
}

// ============================================================================
// toLLMDetailResponse tests
// ============================================================================

func TestToLLMDetailResponse_EmptyConversation(t *testing.T) {
	li := &ent.LLMInteraction{
		ID:              "int-1",
		InteractionType: llminteraction.InteractionTypeIteration,
		ModelName:       "test-model",
		LlmRequest:      map[string]any{"messages_count": 2},
		LlmResponse:     map[string]any{"text_length": 42},
		CreatedAt:       time.Now(),
	}

	resp := toLLMDetailResponse(li, nil)
	assert.Equal(t, "int-1", resp.ID)
	assert.Empty(t, resp.Conversation)
}

func TestToLLMDetailResponse_WithConversation(t *testing.T) {
	thinking := "Let me check pods."
	li := &ent.LLMInteraction{
		ID:              "int-2",
		InteractionType: llminteraction.InteractionTypeIteration,
		ModelName:       "gemini-2.0-flash",
		ThinkingContent: &thinking,
		LlmRequest:      map[string]any{},
		LlmResponse:     map[string]any{},
		CreatedAt:       time.Now(),
	}

	toolCallID := "call-1"
	toolName := "get_pods"
	messages := []*ent.Message{
		{
			ID:      "msg-1",
			Role:    message.RoleSystem,
			Content: "You are an SRE.",
		},
		{
			ID:      "msg-2",
			Role:    message.RoleUser,
			Content: "Pod OOMKilled",
		},
		{
			ID:      "msg-3",
			Role:    message.RoleAssistant,
			Content: "I'll check pods.",
			ToolCalls: []schema.MessageToolCall{
				{ID: "call-1", Name: "get_pods", Arguments: `{"ns":"default"}`},
			},
		},
		{
			ID:         "msg-4",
			Role:       message.RoleTool,
			Content:    "pod-1 Running",
			ToolCallID: &toolCallID,
			ToolName:   &toolName,
		},
	}

	resp := toLLMDetailResponse(li, messages)
	require.Len(t, resp.Conversation, 4)

	// System message
	assert.Equal(t, "system", resp.Conversation[0].Role)
	assert.Equal(t, "You are an SRE.", resp.Conversation[0].Content)
	assert.Nil(t, resp.Conversation[0].ToolCalls)

	// User message
	assert.Equal(t, "user", resp.Conversation[1].Role)
	assert.Equal(t, "Pod OOMKilled", resp.Conversation[1].Content)

	// Assistant with tool calls
	assert.Equal(t, "assistant", resp.Conversation[2].Role)
	require.Len(t, resp.Conversation[2].ToolCalls, 1)
	assert.Equal(t, "call-1", resp.Conversation[2].ToolCalls[0].ID)
	assert.Equal(t, "get_pods", resp.Conversation[2].ToolCalls[0].Name)
	assert.Equal(t, `{"ns":"default"}`, resp.Conversation[2].ToolCalls[0].Arguments)

	// Tool result
	assert.Equal(t, "tool", resp.Conversation[3].Role)
	assert.Equal(t, "pod-1 Running", resp.Conversation[3].Content)
	require.NotNil(t, resp.Conversation[3].ToolCallID)
	assert.Equal(t, "call-1", *resp.Conversation[3].ToolCallID)
	require.NotNil(t, resp.Conversation[3].ToolName)
	assert.Equal(t, "get_pods", *resp.Conversation[3].ToolName)

	// Thinking content
	require.NotNil(t, resp.ThinkingContent)
	assert.Equal(t, "Let me check pods.", *resp.ThinkingContent)
}

func TestToLLMDetailResponse_InlineConversationFallback(t *testing.T) {
	li := &ent.LLMInteraction{
		ID:              "int-sum",
		InteractionType: llminteraction.InteractionTypeSummarization,
		ModelName:       "test-model",
		LlmRequest: map[string]any{
			"messages_count": 2,
			"iteration":      0,
			"conversation": []any{
				map[string]any{"role": "system", "content": "You are a summarizer."},
				map[string]any{"role": "user", "content": "Summarize this data."},
				map[string]any{"role": "assistant", "content": "Here is the summary."},
			},
		},
		LlmResponse: map[string]any{"text_length": 20, "tool_calls_count": 0},
		CreatedAt:   time.Now(),
	}

	// No Message records — should fall back to inline conversation.
	resp := toLLMDetailResponse(li, nil)
	require.Len(t, resp.Conversation, 3)
	assert.Equal(t, "system", resp.Conversation[0].Role)
	assert.Equal(t, "You are a summarizer.", resp.Conversation[0].Content)
	assert.Equal(t, "user", resp.Conversation[1].Role)
	assert.Equal(t, "Summarize this data.", resp.Conversation[1].Content)
	assert.Equal(t, "assistant", resp.Conversation[2].Role)
	assert.Equal(t, "Here is the summary.", resp.Conversation[2].Content)

	// Inline conversation should be stripped from llm_request in the response.
	_, hasConv := resp.LLMRequest["conversation"]
	assert.False(t, hasConv, "conversation should be stripped from llm_request")
	assert.Equal(t, 2, resp.LLMRequest["messages_count"], "other llm_request fields preserved")
	assert.Equal(t, 0, resp.LLMRequest["iteration"], "other llm_request fields preserved")
}

func TestToLLMDetailResponse_MessageRecordsTakePrecedence(t *testing.T) {
	li := &ent.LLMInteraction{
		ID:              "int-iter",
		InteractionType: llminteraction.InteractionTypeIteration,
		ModelName:       "test-model",
		LlmRequest: map[string]any{
			"messages_count": 2,
			"conversation": []any{
				map[string]any{"role": "system", "content": "inline system"},
			},
		},
		LlmResponse: map[string]any{},
		CreatedAt:   time.Now(),
	}

	messages := []*ent.Message{
		{ID: "msg-1", Role: message.RoleSystem, Content: "DB system prompt"},
	}

	// Message records exist — inline conversation should be ignored.
	resp := toLLMDetailResponse(li, messages)
	require.Len(t, resp.Conversation, 1)
	assert.Equal(t, "DB system prompt", resp.Conversation[0].Content)
}

// ============================================================================
// extractInlineConversation tests
// ============================================================================

func TestExtractInlineConversation_NoConversationKey(t *testing.T) {
	result := extractInlineConversation(map[string]any{"messages_count": 2})
	assert.Nil(t, result)
}

func TestExtractInlineConversation_InvalidType(t *testing.T) {
	result := extractInlineConversation(map[string]any{"conversation": "not-an-array"})
	assert.Nil(t, result)
}

func TestExtractInlineConversation_SkipsMalformedEntries(t *testing.T) {
	result := extractInlineConversation(map[string]any{
		"conversation": []any{
			map[string]any{"role": "system", "content": "valid"},
			"not-a-map",
			map[string]any{"content": "no-role"}, // empty role → skipped
			map[string]any{"role": "user", "content": "also valid"},
		},
	})
	require.Len(t, result, 2)
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "user", result[1].Role)
}

// ============================================================================
// toMCPDetailResponse tests
// ============================================================================

func TestToMCPDetailResponse(t *testing.T) {
	toolName := "get_pods"
	durationMs := 22
	now := time.Now()

	mi := &ent.MCPInteraction{
		ID:              "mcp-1",
		InteractionType: mcpinteraction.InteractionTypeToolCall,
		ServerName:      "kubernetes",
		ToolName:        &toolName,
		ToolArguments:   map[string]any{"namespace": "default"},
		ToolResult:      map[string]any{"pods": []any{"pod-1", "pod-2"}},
		DurationMs:      &durationMs,
		CreatedAt:       now,
	}

	resp := toMCPDetailResponse(mi)
	assert.Equal(t, "mcp-1", resp.ID)
	assert.Equal(t, "tool_call", resp.InteractionType)
	assert.Equal(t, "kubernetes", resp.ServerName)
	require.NotNil(t, resp.ToolName)
	assert.Equal(t, "get_pods", *resp.ToolName)
	assert.Equal(t, map[string]any{"namespace": "default"}, resp.ToolArguments)
	assert.NotNil(t, resp.ToolResult)
	assert.Equal(t, &durationMs, resp.DurationMs)
	assert.Equal(t, now.Format(time.RFC3339Nano), resp.CreatedAt)
}

func TestToMCPDetailResponse_NilOptionalFields(t *testing.T) {
	mi := &ent.MCPInteraction{
		ID:              "mcp-2",
		InteractionType: mcpinteraction.InteractionTypeToolList,
		ServerName:      "argocd",
		AvailableTools:  []any{"deploy", "rollback"},
		CreatedAt:       time.Now(),
	}

	resp := toMCPDetailResponse(mi)
	assert.Nil(t, resp.ToolName)
	assert.Nil(t, resp.ToolArguments)
	assert.Nil(t, resp.ToolResult)
	assert.Nil(t, resp.DurationMs)
	assert.Nil(t, resp.ErrorMessage)
	require.Len(t, resp.AvailableTools, 2)
}

// ============================================================================
// schemaToolCallToModel tests
// ============================================================================

func TestSchemaToolCallToModel(t *testing.T) {
	tc := schema.MessageToolCall{
		ID:        "call-42",
		Name:      "k8s.get_pods",
		Arguments: `{"namespace":"kube-system"}`,
	}

	result := schemaToolCallToModel(tc)
	assert.Equal(t, "call-42", result.ID)
	assert.Equal(t, "k8s.get_pods", result.Name)
	assert.Equal(t, `{"namespace":"kube-system"}`, result.Arguments)
}
