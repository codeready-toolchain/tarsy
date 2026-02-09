package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestFormatChatHistory_Empty(t *testing.T) {
	result := FormatChatHistory(nil)
	assert.Equal(t, "", result)

	result = FormatChatHistory([]agent.ChatExchange{})
	assert.Equal(t, "", result)
}

func TestFormatChatHistory_SingleExchange(t *testing.T) {
	exchanges := []agent.ChatExchange{
		{
			UserQuestion: "What is the pod status?",
			Messages: []agent.ConversationMessage{
				{Role: agent.RoleAssistant, Content: "The pod is running."},
			},
		},
	}

	result := FormatChatHistory(exchanges)

	assert.Contains(t, result, "CHAT HISTORY")
	assert.Contains(t, result, "1 previous exchange")
	assert.NotContains(t, result, "exchanges") // singular
	assert.Contains(t, result, "Exchange 1")
	assert.Contains(t, result, "**USER:**")
	assert.Contains(t, result, "What is the pod status?")
	assert.Contains(t, result, "**ASSISTANT:**")
	assert.Contains(t, result, "The pod is running.")
}

func TestFormatChatHistory_MultipleExchanges(t *testing.T) {
	exchanges := []agent.ChatExchange{
		{
			UserQuestion: "First question",
			Messages:     []agent.ConversationMessage{{Role: agent.RoleAssistant, Content: "First answer"}},
		},
		{
			UserQuestion: "Second question",
			Messages:     []agent.ConversationMessage{{Role: agent.RoleAssistant, Content: "Second answer"}},
		},
	}

	result := FormatChatHistory(exchanges)

	assert.Contains(t, result, "2 previous exchanges")
	assert.Contains(t, result, "Exchange 1")
	assert.Contains(t, result, "Exchange 2")
	assert.Contains(t, result, "First question")
	assert.Contains(t, result, "Second question")
}

func TestFormatChatHistory_WithObservations(t *testing.T) {
	exchanges := []agent.ChatExchange{
		{
			UserQuestion: "Check the logs",
			Messages: []agent.ConversationMessage{
				{Role: agent.RoleAssistant, Content: "Let me check the logs."},
				{Role: agent.RoleUser, Content: "Observation: Logs show OOM events"},
				{Role: agent.RoleAssistant, Content: "The logs show OOM events."},
			},
		},
	}

	result := FormatChatHistory(exchanges)
	assert.Contains(t, result, "**Observation:**")
	assert.Contains(t, result, "Logs show OOM events")
}

func TestFormatChatHistory_WithToolResults(t *testing.T) {
	exchanges := []agent.ChatExchange{
		{
			UserQuestion: "Check the pods",
			Messages: []agent.ConversationMessage{
				{Role: agent.RoleAssistant, Content: "Checking pods now."},
				{Role: agent.RoleTool, Content: "pod-1 Running\npod-2 CrashLoopBackOff", ToolCallID: "tc1", ToolName: "k8s.pods_list"},
				{Role: agent.RoleAssistant, Content: "Pod-2 is crashing."},
			},
		},
	}

	result := FormatChatHistory(exchanges)
	assert.Contains(t, result, "**Observation (tool):**")
	assert.Contains(t, result, "pod-2 CrashLoopBackOff")
}

func TestPluralS(t *testing.T) {
	assert.Equal(t, "", pluralS(1))
	assert.Equal(t, "s", pluralS(0))
	assert.Equal(t, "s", pluralS(2))
	assert.Equal(t, "s", pluralS(10))
}

func TestBuildChatUserMessage(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:        "What caused the crash?",
		InvestigationContext: "Investigation found memory issues.",
		ChatHistory: []agent.ChatExchange{
			{
				UserQuestion: "Previous Q",
				Messages:     []agent.ConversationMessage{{Role: agent.RoleAssistant, Content: "Previous A"}},
			},
		},
	}

	result := builder.buildChatUserMessage(execCtx, nil)

	// Should contain investigation context
	assert.Contains(t, result, "Investigation found memory issues.")
	// Should contain chat history
	assert.Contains(t, result, "CHAT HISTORY")
	assert.Contains(t, result, "Previous Q")
	// Should contain current question
	assert.Contains(t, result, "CURRENT TASK")
	assert.Contains(t, result, "What caused the crash?")
}

func TestBuildChatUserMessage_WithTools(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:        "List pods",
		InvestigationContext: "Context.",
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.pods", Description: "List pods"},
	}

	result := builder.buildChatUserMessage(execCtx, tools)
	assert.Contains(t, result, "Available tools")
	assert.Contains(t, result, "k8s.pods")
}

func TestBuildChatUserMessage_NoChatHistory(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:        "Simple question",
		InvestigationContext: "Context.",
	}

	result := builder.buildChatUserMessage(execCtx, nil)
	assert.NotContains(t, result, "CHAT HISTORY")
	assert.Contains(t, result, "CURRENT TASK")
}
