package controller

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/message"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreToolResultMessage_EmptyContent verifies that storeToolResultMessage
// substitutes a placeholder when the tool returns empty content, preventing
// the "validation error on field 'content': required" DB error.
func TestStoreToolResultMessage_EmptyContent(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int

	// Empty content should succeed (placeholder substituted)
	storeToolResultMessage(ctx, execCtx, "call-1", "test-tool", "", &seq)

	// Verify the message was persisted with the placeholder content
	msgs, err := execCtx.Services.Message.GetExecutionMessages(ctx, execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "(empty result)", msgs[0].Content)
	assert.Equal(t, message.RoleTool, msgs[0].Role)
	assert.Equal(t, 1, seq)
}

// TestStoreToolResultMessage_NonEmptyContent verifies that non-empty content
// is stored as-is without placeholder substitution.
func TestStoreToolResultMessage_NonEmptyContent(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int

	storeToolResultMessage(ctx, execCtx, "call-1", "test-tool", `{"pods": []}`, &seq)

	msgs, err := execCtx.Services.Message.GetExecutionMessages(ctx, execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, `{"pods": []}`, msgs[0].Content)
}

// TestStoreObservationMessage_EmptyContent verifies that storeObservationMessage
// substitutes a placeholder for empty observations.
func TestStoreObservationMessage_EmptyContent(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int

	storeObservationMessage(ctx, execCtx, "", &seq)

	msgs, err := execCtx.Services.Message.GetExecutionMessages(ctx, execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "(empty observation)", msgs[0].Content)
	assert.Equal(t, message.RoleUser, msgs[0].Role)
}

// TestStoreAssistantMessage_EmptyText verifies that storeAssistantMessage
// substitutes a placeholder when the LLM returns empty text, preventing
// the fatal "failed to store assistant message: validation error" error.
func TestStoreAssistantMessage_EmptyText(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int
	resp := &LLMResponse{Text: ""}

	msg, err := storeAssistantMessage(ctx, execCtx, resp, &seq)

	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "(empty response)", msg.Content)
	assert.Equal(t, message.RoleAssistant, msg.Role)
}

// TestStoreAssistantMessage_NonEmptyText verifies normal text is stored as-is.
func TestStoreAssistantMessage_NonEmptyText(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int
	resp := &LLMResponse{Text: "The pod is OOMKilled."}

	msg, err := storeAssistantMessage(ctx, execCtx, resp, &seq)

	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "The pod is OOMKilled.", msg.Content)
}

// TestStoreAssistantMessageWithToolCalls_EmptyText verifies that empty text
// with tool calls stores a placeholder.
func TestStoreAssistantMessageWithToolCalls_EmptyText(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	ctx := context.Background()

	var seq int
	resp := &LLMResponse{
		Text: "",
		ToolCalls: []agent.ToolCall{
			{ID: "tc-1", Name: "get_pods", Arguments: `{}`},
		},
	}

	msg, err := storeAssistantMessageWithToolCalls(ctx, execCtx, resp, &seq)

	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "(tool calls only)", msg.Content)
	assert.Equal(t, message.RoleAssistant, msg.Role)
}
