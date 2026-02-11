package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBuilderForTest() *PromptBuilder {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {Instructions: "K8s server instructions."},
	})
	return NewPromptBuilder(registry)
}

func newFullExecCtx() *agent.ExecutionContext {
	return &agent.ExecutionContext{
		SessionID:      "test-session",
		AgentName:      "TestAgent",
		AlertData:      `{"alert":"test-alert","severity":"critical"}`,
		AlertType:      "kubernetes",
		RunbookContent: "# Test Runbook\n\nStep 1: Check pods",
		Config: &agent.ResolvedAgentConfig{
			AgentName:          "TestAgent",
			IterationStrategy:  config.IterationStrategyReact,
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "Be thorough.",
		},
	}
}

func TestBuildReActMessages_MessageCount(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	tools := []agent.ToolDefinition{
		{Name: "k8s.pods_list", Description: "List pods", ParametersSchema: `{"properties":{"ns":{"type":"string"}}}`},
	}

	messages := builder.BuildReActMessages(execCtx, "", tools)

	require.Len(t, messages, 2, "Should have system + user message")
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Equal(t, agent.RoleUser, messages[1].Role)
}

func TestBuildReActMessages_SystemMessageContent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildReActMessages(execCtx, "", nil)
	systemMsg := messages[0].Content

	// Should contain instructions
	assert.Contains(t, systemMsg, "General SRE Agent Instructions")
	// Should contain ReAct format
	assert.Contains(t, systemMsg, "ReAct")
	assert.Contains(t, systemMsg, "Thought:")
	assert.Contains(t, systemMsg, "Final Answer:")
	// Should contain task focus
	assert.Contains(t, systemMsg, "Focus on investigation")
}

func TestBuildReActMessages_UserMessageContent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	tools := []agent.ToolDefinition{
		{Name: "k8s.pods_list", Description: "List pods"},
	}

	messages := builder.BuildReActMessages(execCtx, "Previous stage context.", tools)
	userMsg := messages[1].Content

	assert.Contains(t, userMsg, "Available tools")
	assert.Contains(t, userMsg, "k8s.pods_list")
	assert.Contains(t, userMsg, "Alert Details")
	assert.Contains(t, userMsg, "test-alert")
	assert.Contains(t, userMsg, "Runbook Content")
	assert.Contains(t, userMsg, "Test Runbook")
	assert.Contains(t, userMsg, "Previous Stage Data")
	assert.Contains(t, userMsg, "Previous stage context.")
	assert.Contains(t, userMsg, "Your Task")
}

func TestBuildReActMessages_NoTools(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildReActMessages(execCtx, "", nil)
	userMsg := messages[1].Content

	assert.NotContains(t, userMsg, "Available tools")
}

func TestBuildReActMessages_NoPrevStageContext(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildReActMessages(execCtx, "", nil)
	userMsg := messages[1].Content

	assert.Contains(t, userMsg, "first stage of analysis")
}

func TestBuildNativeThinkingMessages_MessageCount(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildNativeThinkingMessages(execCtx, "")
	require.Len(t, messages, 2)
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Equal(t, agent.RoleUser, messages[1].Role)
}

func TestBuildNativeThinkingMessages_NoReActFormat(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildNativeThinkingMessages(execCtx, "")

	// System should NOT contain ReAct format instructions
	assert.NotContains(t, messages[0].Content, "Action Input:")
	assert.NotContains(t, messages[0].Content, "REQUIRED FORMAT")
}

func TestBuildNativeThinkingMessages_NoToolDescriptions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildNativeThinkingMessages(execCtx, "")

	// User message should NOT contain tool descriptions (tools are native)
	assert.NotContains(t, messages[1].Content, "Available tools")
}

func TestBuildSynthesisMessages_MessageCount(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildSynthesisMessages(execCtx, "Agent 1 found OOM issues.")
	require.Len(t, messages, 2)
}

func TestBuildSynthesisMessages_UserContent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildSynthesisMessages(execCtx, "Agent 1: memory leak. Agent 2: disk full.")
	userMsg := messages[1].Content

	assert.Contains(t, userMsg, "Synthesize")
	assert.Contains(t, userMsg, "Agent 1: memory leak. Agent 2: disk full.")
	assert.Contains(t, userMsg, "Alert Details")
}

func TestBuildForcedConclusionPrompt_React(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildForcedConclusionPrompt(5, config.IterationStrategyReact)

	assert.Contains(t, result, "5 iterations")
	assert.Contains(t, result, "Final Answer:")
	assert.Contains(t, result, "CRITICAL")
}

func TestBuildForcedConclusionPrompt_NativeThinking(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildForcedConclusionPrompt(3, config.IterationStrategyNativeThinking)

	assert.Contains(t, result, "3 iterations")
	assert.Contains(t, result, "structured conclusion")
	assert.NotContains(t, result, "Final Answer:")
}

func TestBuildForcedConclusionPrompt_UnknownStrategy(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildForcedConclusionPrompt(2, config.IterationStrategy("unknown"))

	// Should fall back to native thinking format
	assert.Contains(t, result, "2 iterations")
	assert.Contains(t, result, "structured conclusion")
}

func TestBuildMCPSummarizationPrompts(t *testing.T) {
	builder := newBuilderForTest()

	systemPrompt := builder.BuildMCPSummarizationSystemPrompt("kubernetes-server", "pods_list", 500)
	assert.Contains(t, systemPrompt, "kubernetes-server.pods_list")
	assert.Contains(t, systemPrompt, "500")

	userPrompt := builder.BuildMCPSummarizationUserPrompt("context here", "kubernetes-server", "pods_list", "big output")
	assert.Contains(t, userPrompt, "context here")
	assert.Contains(t, userPrompt, "kubernetes-server")
	assert.Contains(t, userPrompt, "pods_list")
	assert.Contains(t, userPrompt, "big output")
}

func TestBuildExecutiveSummaryPrompts(t *testing.T) {
	builder := newBuilderForTest()

	systemPrompt := builder.BuildExecutiveSummarySystemPrompt()
	assert.Contains(t, systemPrompt, "executive summaries")

	userPrompt := builder.BuildExecutiveSummaryUserPrompt("The root cause was OOM.")
	assert.Contains(t, userPrompt, "The root cause was OOM.")
}

func TestBuildReActMessages_ChatMode(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "What was the root cause?",
		InvestigationContext: "Previous investigation found OOM.",
	}

	messages := builder.BuildReActMessages(execCtx, "", nil)

	// System should have chat instructions, not investigation instructions
	assert.Contains(t, messages[0].Content, "Chat Assistant Instructions")
	assert.NotContains(t, messages[0].Content, "General SRE Agent Instructions")

	// User should have chat user message
	assert.Contains(t, messages[1].Content, "What was the root cause?")
	assert.Contains(t, messages[1].Content, "Previous investigation found OOM.")
}

func TestBuildNativeThinkingMessages_ChatMode(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "Show me the pod status",
		InvestigationContext: "Investigation context.",
	}

	messages := builder.BuildNativeThinkingMessages(execCtx, "")

	assert.Contains(t, messages[0].Content, "Chat Assistant Instructions")
	assert.Contains(t, messages[1].Content, "Show me the pod status")
}
