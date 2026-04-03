package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/orchestrator"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// recordMCPInteraction tests
// ============================================================================

func TestRecordMCPInteraction_Success(t *testing.T) {
	// Successful tool call: valid JSON args, non-error result.
	execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
	ctx := context.Background()
	startTime := time.Now().Add(-50 * time.Millisecond)

	result := &agent.ToolResult{
		CallID:  "call-1",
		Name:    "kubernetes-server__get_pods",
		Content: `{"pods":["app-1","app-2"]}`,
		IsError: false,
	}

	recordMCPInteraction(ctx, execCtx, "kubernetes-server", "get_pods",
		`{"namespace":"default"}`, result, startTime, nil)

	// Query DB to verify the record was created.
	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)

	rec := interactions[0]
	assert.Equal(t, execCtx.SessionID, rec.SessionID)
	assert.Equal(t, execCtx.StageID, rec.StageID)
	assert.Equal(t, execCtx.ExecutionID, rec.ExecutionID)
	assert.Equal(t, mcpinteraction.InteractionTypeToolCall, rec.InteractionType)
	assert.Equal(t, "kubernetes-server", rec.ServerName)
	assert.NotNil(t, rec.ToolName)
	assert.Equal(t, "get_pods", *rec.ToolName)

	// Arguments should be parsed JSON.
	assert.Equal(t, "default", rec.ToolArguments["namespace"])

	// Result should include content and is_error flag.
	assert.NotNil(t, rec.ToolResult)
	assert.Equal(t, false, rec.ToolResult["is_error"])

	// Duration should be positive.
	assert.NotNil(t, rec.DurationMs)
	assert.Greater(t, *rec.DurationMs, 0)

	// No error message.
	assert.Nil(t, rec.ErrorMessage)
}

func TestRecordMCPInteraction_ToolError(t *testing.T) {
	// Tool execution failed: result is nil, error is set.
	execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
	ctx := context.Background()
	startTime := time.Now()

	toolErr := errors.New("connection refused to MCP server")

	recordMCPInteraction(ctx, execCtx, "test-mcp", "get_logs",
		`{"pod":"app-1"}`, nil, startTime, toolErr)

	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)

	rec := interactions[0]
	assert.Equal(t, "test-mcp", rec.ServerName)
	assert.Equal(t, "get_logs", *rec.ToolName)

	// Result should be nil (tool never returned).
	assert.Nil(t, rec.ToolResult)

	// Error message should be set.
	assert.NotNil(t, rec.ErrorMessage)
	assert.Equal(t, "connection refused to MCP server", *rec.ErrorMessage)
}

func TestRecordMCPInteraction_InvalidJSONArgs(t *testing.T) {
	// Arguments are not valid JSON — should fall back to {"raw": ...}.
	execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
	ctx := context.Background()
	startTime := time.Now()

	result := &agent.ToolResult{
		CallID:  "call-1",
		Name:    "test-mcp__get_pods",
		Content: "ok",
		IsError: false,
	}

	recordMCPInteraction(ctx, execCtx, "test-mcp", "get_pods",
		"not-valid-json{{{", result, startTime, nil)

	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)

	rec := interactions[0]
	// Should fall back to raw string.
	assert.Equal(t, "not-valid-json{{{", rec.ToolArguments["raw"])
}

func TestRecordMCPInteraction_EmptyArgs(t *testing.T) {
	// Empty arguments string — tool_arguments should be nil/empty.
	execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
	ctx := context.Background()
	startTime := time.Now()

	result := &agent.ToolResult{
		CallID:  "call-1",
		Name:    "test-mcp__list_items",
		Content: "[]",
		IsError: false,
	}

	recordMCPInteraction(ctx, execCtx, "test-mcp", "list_items",
		"", result, startTime, nil)

	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)

	rec := interactions[0]
	assert.Nil(t, rec.ToolArguments)
}

func TestRecordMCPInteraction_ErrorResult(t *testing.T) {
	// Tool returned a result but with IsError=true (tool-level error, not execution error).
	execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
	ctx := context.Background()
	startTime := time.Now()

	result := &agent.ToolResult{
		CallID:  "call-1",
		Name:    "test-mcp__get_pods",
		Content: "pod not found",
		IsError: true,
	}

	recordMCPInteraction(ctx, execCtx, "test-mcp", "get_pods",
		`{"name":"missing"}`, result, startTime, nil)

	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)

	rec := interactions[0]
	assert.NotNil(t, rec.ToolResult)
	assert.Equal(t, true, rec.ToolResult["is_error"])
	// No execution error — just a tool-level error flag.
	assert.Nil(t, rec.ErrorMessage)
}

// ============================================================================
// executeToolCall tests
// ============================================================================

func TestExecuteToolCall_Success(t *testing.T) {
	// Successful tool execution: returns content, records MCP interaction.
	toolExec := &mockToolExecutor{
		tools: []agent.ToolDefinition{{Name: "test-mcp__get_pods"}},
		results: map[string]*agent.ToolResult{
			"test-mcp__get_pods": {Content: `{"pods":["p1"]}`, IsError: false},
		},
	}
	execCtx := newTestExecCtx(t, &mockLLMClient{}, toolExec)
	ctx := context.Background()
	eventSeq := 0

	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-1",
		Name:      "test-mcp__get_pods",
		Arguments: `{"ns":"default"}`,
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "pods")
	assert.Nil(t, result.Err)

	// Verify MCP interaction was recorded in DB.
	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	assert.Len(t, interactions, 1)
	assert.Equal(t, "test-mcp", interactions[0].ServerName)
	assert.Equal(t, "get_pods", *interactions[0].ToolName)
	assert.Nil(t, interactions[0].ErrorMessage)
}

// ============================================================================
// recordToolListInteractions tests
// ============================================================================

func TestRecordToolListInteractions(t *testing.T) {
	t.Run("records one interaction per server with descriptions", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		ctx := context.Background()

		tools := []agent.ToolDefinition{
			{Name: "kubernetes.get_pods", Description: "Get pods in a namespace"},
			{Name: "kubernetes.get_logs", Description: "Get pod logs"},
			{Name: "argocd.list_apps", Description: "List Argo CD applications"},
		}

		recordToolListInteractions(ctx, execCtx, tools)

		interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
		require.NoError(t, err)
		require.Len(t, interactions, 2)

		// Build a map for order-independent assertions.
		byServer := make(map[string]*ent.MCPInteraction)
		for _, rec := range interactions {
			byServer[rec.ServerName] = rec
		}

		// Verify both servers recorded as tool_list.
		require.Contains(t, byServer, "kubernetes")
		require.Contains(t, byServer, "argocd")
		assert.Equal(t, mcpinteraction.InteractionTypeToolList, byServer["kubernetes"].InteractionType)
		assert.Equal(t, mcpinteraction.InteractionTypeToolList, byServer["argocd"].InteractionType)

		// Verify kubernetes tools include name + description, sorted by name.
		k8sTools := byServer["kubernetes"].AvailableTools
		require.Len(t, k8sTools, 2)
		tool0, ok := k8sTools[0].(map[string]interface{})
		require.True(t, ok, "tool entry should be a map")
		assert.Equal(t, "get_logs", tool0["name"])
		assert.Equal(t, "Get pod logs", tool0["description"])
		tool1, ok := k8sTools[1].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "get_pods", tool1["name"])
		assert.Equal(t, "Get pods in a namespace", tool1["description"])

		// Verify argocd has 1 tool.
		assert.Len(t, byServer["argocd"].AvailableTools, 1)
	})

	t.Run("classifies non-MCP tools by category", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		ctx := context.Background()

		tools := []agent.ToolDefinition{
			{Name: "kubernetes.get_pods", Description: "Get pods"},
			{Name: "dispatch_agent", Description: "Dispatch a sub-agent"},
			{Name: "cancel_agent", Description: "Cancel a running sub-agent"},
			{Name: "list_agents", Description: "List dispatched sub-agents"},
			{Name: "load_skill", Description: "Load skills by name"},
		}

		recordToolListInteractions(ctx, execCtx, tools)

		interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
		require.NoError(t, err)
		require.Len(t, interactions, 3, "expect one record per server: kubernetes, orchestrator, empty")

		byServer := make(map[string]*ent.MCPInteraction)
		for _, rec := range interactions {
			byServer[rec.ServerName] = rec
		}

		require.Contains(t, byServer, "kubernetes")
		require.Contains(t, byServer, orchestrator.OrchestrationServerName)
		require.Contains(t, byServer, "", "built-in tools recorded under empty server")

		orchTools := byServer[orchestrator.OrchestrationServerName].AvailableTools
		require.Len(t, orchTools, 3)
		names := make([]string, len(orchTools))
		for i, raw := range orchTools {
			entry, ok := raw.(map[string]interface{})
			require.True(t, ok)
			names[i] = entry["name"].(string)
		}
		assert.Equal(t, []string{"cancel_agent", "dispatch_agent", "list_agents"}, names)

		builtinTools := byServer[""].AvailableTools
		require.Len(t, builtinTools, 1)
		tool0, ok := builtinTools[0].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "load_skill", tool0["name"])
	})

	t.Run("no-op when tools is nil", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		ctx := context.Background()

		recordToolListInteractions(ctx, execCtx, nil)

		interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
		require.NoError(t, err)
		assert.Empty(t, interactions)
	})
}

// ============================================================================
// executeToolCall tests
// ============================================================================

func TestExecuteToolCall_ToolError(t *testing.T) {
	// Tool execution fails: returns error content, records MCP interaction with error.
	toolExec := &mockToolExecutorFunc{
		tools: []agent.ToolDefinition{{Name: "test-mcp__broken_tool"}},
		executeFn: func(_ context.Context, _ agent.ToolCall) (*agent.ToolResult, error) {
			return nil, errors.New("server unavailable")
		},
	}
	execCtx := newTestExecCtx(t, &mockLLMClient{}, toolExec)
	ctx := context.Background()
	eventSeq := 0

	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-err",
		Name:      "test-mcp__broken_tool",
		Arguments: `{}`,
	}, nil, nil, &eventSeq)

	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "server unavailable")
	assert.NotNil(t, result.Err)

	// Verify MCP interaction recorded with error.
	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	assert.Len(t, interactions, 1)
	assert.NotNil(t, interactions[0].ErrorMessage)
	assert.Contains(t, *interactions[0].ErrorMessage, "server unavailable")
}

func TestExecuteToolCall_ToolTypeClassification(t *testing.T) {
	tests := []struct {
		name         string
		toolCallName string
		wantToolType string
	}{
		{
			name:         "MCP tool with server prefix",
			toolCallName: "kubernetes.get_pods",
			wantToolType: string(ToolTypeMCP),
		},
		{
			name:         "MCP tool with double-underscore format",
			toolCallName: "kubernetes__get_pods",
			wantToolType: string(ToolTypeMCP),
		},
		{
			name:         "load_skill classified as skill",
			toolCallName: "load_skill",
			wantToolType: string(ToolTypeSkill),
		},
		{
			name:         "dispatch_agent classified as orchestrator",
			toolCallName: "dispatch_agent",
			wantToolType: string(ToolTypeOrchestrator),
		},
		{
			name:         "colon-prefixed dispatch_agent classified as orchestrator",
			toolCallName: "google:dispatch_agent",
			wantToolType: string(ToolTypeOrchestrator),
		},
		{
			name:         "colon-prefixed load_skill classified as skill",
			toolCallName: "google:load_skill",
			wantToolType: string(ToolTypeSkill),
		},
		{
			name:         "colon-prefixed recall_past_investigations classified as memory",
			toolCallName: "openai:recall_past_investigations",
			wantToolType: string(ToolTypeMemory),
		},
		{
			name:         "recall_past_investigations classified as memory",
			toolCallName: "recall_past_investigations",
			wantToolType: string(ToolTypeMemory),
		},
		{
			name:         "malformed MCP name without server prefix stays MCP",
			toolCallName: "resources_get",
			wantToolType: string(ToolTypeMCP),
		},
		{
			name:         "url_context classified as google_native",
			toolCallName: "url_context",
			wantToolType: string(ToolTypeNative),
		},
		{
			name:         "google_search classified as google_native",
			toolCallName: "google_search",
			wantToolType: string(ToolTypeNative),
		},
		{
			name:         "code_execution classified as google_native",
			toolCallName: "code_execution",
			wantToolType: string(ToolTypeNative),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolExec := &mockToolExecutorFunc{
				tools: []agent.ToolDefinition{{Name: tt.toolCallName}},
				executeFn: func(_ context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
					return &agent.ToolResult{
						CallID:  call.ID,
						Name:    call.Name,
						Content: "ok",
					}, nil
				},
			}
			execCtx := newTestExecCtx(t, &mockLLMClient{}, toolExec)
			ctx := context.Background()
			eventSeq := 0

			executeToolCall(ctx, execCtx, agent.ToolCall{
				ID:        "tc-classify",
				Name:      tt.toolCallName,
				Arguments: `{}`,
			}, nil, nil, &eventSeq)

			events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
			require.NoError(t, err)
			require.NotEmpty(t, events)

			lastEvent := events[len(events)-1]
			assert.Equal(t, tt.wantToolType, lastEvent.Metadata["tool_type"],
				"tool_type metadata mismatch for %q", tt.toolCallName)
		})
	}
}

func TestExecuteToolCall_GeminiNative_LoadContextAliasSkipsStub(t *testing.T) {
	stub := agent.NewStubToolExecutor(nil)
	execCtx := newTestExecCtx(t, &mockLLMClient{}, stub)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-load-ctx",
		Name:      "load_context",
		Arguments: `{"urls":["https://github.com/example/repo"]}`,
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)
	assert.NotContains(t, result.Content, "[stub]")
	assert.Contains(t, result.Content, "Gemini API")
}

func TestExecuteToolCall_GeminiNative_SkipsStubExecutor(t *testing.T) {
	stub := agent.NewStubToolExecutor(nil)
	execCtx := newTestExecCtx(t, &mockLLMClient{}, stub)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	groundings := []agent.GroundingChunk{
		{
			WebSearchQueries: []string{"cloud-cafe k8s"},
			Sources: []agent.GroundingSource{
				{URI: "https://github.com/example/repo", Title: "Example"},
			},
		},
	}

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-native",
		Name:      "url_context",
		Arguments: `{"url":"https://github.com/example/repo"}`,
	}, nil, groundings, &eventSeq)

	assert.False(t, result.IsError)
	assert.NotContains(t, result.Content, "[stub]")
	assert.Contains(t, result.Content, "Gemini API")
	assert.Contains(t, result.Content, "https://github.com/example/repo")
	assert.Contains(t, result.Content, "cloud-cafe k8s")

	events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
	require.NoError(t, err)
	var llmToolCalls int
	for _, e := range events {
		if e.EventType == timelineevent.EventTypeLlmToolCall {
			llmToolCalls++
		}
	}
	assert.Zero(t, llmToolCalls, "grounding-backed native url_context should not emit duplicate llm_tool_call")

	interactions, err := execCtx.Services.Interaction.GetMCPInteractionsList(ctx, execCtx.SessionID)
	require.NoError(t, err)
	require.Len(t, interactions, 1)
	assert.Equal(t, geminiNativeServerID, interactions[0].ServerName)
	assert.Equal(t, "url_context", *interactions[0].ToolName)
}

func TestExecuteToolCall_GeminiNative_EmptyGroundingDigest(t *testing.T) {
	stub := agent.NewStubToolExecutor(nil)
	execCtx := newTestExecCtx(t, &mockLLMClient{}, stub)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:   "tc-native-2",
		Name: "google_search",
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "No grounding metadata")

	events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
	require.NoError(t, err)
	var llmToolCalls int
	for _, e := range events {
		if e.EventType == timelineevent.EventTypeLlmToolCall {
			llmToolCalls++
		}
	}
	assert.Equal(t, 1, llmToolCalls, "google_search with no grounding should still record llm_tool_call for the synthetic digest")
}

func TestExecuteToolCall_GeminiNative_GoogleSearchTimelineFallbackFromArgs(t *testing.T) {
	stub := agent.NewStubToolExecutor(nil)
	execCtx := newTestExecCtx(t, &mockLLMClient{}, stub)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-gs-fallback",
		Name:      "google_search",
		Arguments: `{"query":"site:github.com \"openshift-openclaw\""}`,
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)

	events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
	require.NoError(t, err)
	var llmToolCalls int
	var inferred bool
	for _, e := range events {
		if e.EventType == timelineevent.EventTypeLlmToolCall {
			llmToolCalls++
		}
		if e.EventType != timelineevent.EventTypeGoogleSearchResult {
			continue
		}
		if b, ok := e.Metadata["inferred_from_tool_arguments"].(bool); ok && b {
			inferred = true
			assert.Contains(t, e.Content, "site:github.com")
			break
		}
	}
	assert.True(t, inferred, "expected google_search_result with inferred_from_tool_arguments")
	assert.Zero(t, llmToolCalls, "tool-args fallback should not duplicate llm_tool_call")
}

func TestExecuteToolCall_GeminiNative_URLContextTimelineFallbackFromArgs(t *testing.T) {
	stub := agent.NewStubToolExecutor(nil)
	execCtx := newTestExecCtx(t, &mockLLMClient{}, stub)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:        "tc-url-fallback",
		Name:      "url_context",
		Arguments: `{"urls":["https://github.com/example/openshift-openclaw"]}`,
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)

	events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
	require.NoError(t, err)
	var inferred bool
	var llmToolCalls int
	for _, e := range events {
		if e.EventType == timelineevent.EventTypeLlmToolCall {
			llmToolCalls++
		}
		if e.EventType != timelineevent.EventTypeURLContextResult {
			continue
		}
		if b, ok := e.Metadata["inferred_from_tool_arguments"].(bool); ok && b {
			inferred = true
			assert.Contains(t, e.Content, "github.com/example/openshift-openclaw")
			break
		}
	}
	assert.True(t, inferred, "expected url_context_result with inferred_from_tool_arguments")
	assert.Zero(t, llmToolCalls, "URL-arg fallback should not duplicate llm_tool_call")
}

func TestExecuteToolCall_LangChain_StillUsesExecutorForNativeToolNames(t *testing.T) {
	toolExec := &mockToolExecutorFunc{
		tools: []agent.ToolDefinition{{Name: "url_context"}},
		executeFn: func(_ context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
			return &agent.ToolResult{
				CallID:  call.ID,
				Name:    call.Name,
				Content: "executor-was-called",
				IsError: false,
			}, nil
		},
	}
	execCtx := newTestExecCtx(t, &mockLLMClient{}, toolExec)
	// Default backend is LangChain — provider-native short-circuit must not apply.
	require.Equal(t, config.LLMBackendLangChain, execCtx.Config.LLMBackend)

	ctx := context.Background()
	eventSeq := 0
	result := executeToolCall(ctx, execCtx, agent.ToolCall{
		ID:   "tc-lc",
		Name: "url_context",
	}, nil, nil, &eventSeq)

	assert.False(t, result.IsError)
	assert.Equal(t, "executor-was-called", result.Content)
}
