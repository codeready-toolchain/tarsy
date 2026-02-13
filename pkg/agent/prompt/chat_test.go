package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestBuildChatUserMessage(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "What caused the crash?",
		InvestigationContext: "Investigation found memory issues.",
	}

	result := builder.buildChatUserMessage(execCtx, nil)

	// Alert + runbook sections reuse the same FormatAlertSection / FormatRunbookSection
	// components as investigation agents, giving the chat agent full alert context.
	expected := FormatAlertSection(execCtx.AlertType, execCtx.AlertData) + "\n" +
		FormatRunbookSection(execCtx.RunbookContent) + "\n" +
		"Investigation found memory issues." +
		"\n" + separator + "\n" +
		"🎯 CURRENT TASK\n" +
		separator + "\n\n" +
		"**Question:** What caused the crash?\n\n" +
		"**Your Task:**\n" +
		"Answer the user's question based on the investigation context above.\n" +
		"- Reference investigation history when relevant\n" +
		"- Use tools to get fresh data if needed\n" +
		"- Provide clear, actionable responses\n\n" +
		"Begin your response:\n"
	assert.Equal(t, expected, result)

	// Verify alert data and runbook are actually present in the output
	assert.Contains(t, result, "## Alert Details")
	assert.Contains(t, result, execCtx.AlertData)
	assert.Contains(t, result, "## Runbook Content")
	assert.Contains(t, result, "# Test Runbook")
}

func TestBuildChatUserMessage_WithTools(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "List pods",
		InvestigationContext: "Context.",
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.pods", Description: "List pods"},
	}

	result := builder.buildChatUserMessage(execCtx, tools)

	expected := "Answer the following question using the available tools.\n\n" +
		"Available tools:\n\n" +
		"1. **k8s.pods**: List pods\n" +
		"    **Parameters**: None\n" +
		"\n\n" +
		FormatAlertSection(execCtx.AlertType, execCtx.AlertData) + "\n" +
		FormatRunbookSection(execCtx.RunbookContent) + "\n" +
		"Context." +
		"\n" + separator + "\n" +
		"🎯 CURRENT TASK\n" +
		separator + "\n\n" +
		"**Question:** List pods\n\n" +
		"**Your Task:**\n" +
		"Answer the user's question based on the investigation context above.\n" +
		"- Reference investigation history when relevant\n" +
		"- Use tools to get fresh data if needed\n" +
		"- Provide clear, actionable responses\n\n" +
		"Begin your response:\n"
	assert.Equal(t, expected, result)
}

func TestBuildChatUserMessage_NilChatContext(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = nil

	result := builder.buildChatUserMessage(execCtx, nil)
	assert.Empty(t, result)
}
