package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
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
	}, nil, &eventSeq)

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
	}, nil, &eventSeq)

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
