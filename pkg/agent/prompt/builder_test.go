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
			Type:               config.AgentTypeDefault,
			LLMBackend:         config.LLMBackendLangChain,
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "Be thorough.",
		},
	}
}

func TestBuildFunctionCallingMessages_MessageCount(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Equal(t, agent.RoleUser, messages[1].Role)
}

func TestBuildFunctionCallingMessages_NoTextToolDescriptions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "")

	// System should NOT contain text-based format instructions (tools are bound natively)
	assert.NotContains(t, messages[0].Content, "Action Input:")
	assert.NotContains(t, messages[0].Content, "REQUIRED FORMAT")
}

func TestBuildFunctionCallingMessages_NoToolDescriptions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "")

	// User message should NOT contain tool descriptions (tools are bound natively)
	assert.NotContains(t, messages[1].Content, "Available tools")
}

func TestBuildFunctionCallingMessages_UserContent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "Previous stage context.")
	userMsg := messages[1].Content

	assert.Contains(t, userMsg, "Alert Details")
	assert.Contains(t, userMsg, "test-alert")
	assert.Contains(t, userMsg, "Runbook Content")
	assert.Contains(t, userMsg, "Test Runbook")
	assert.Contains(t, userMsg, "Previous Stage Data")
	assert.Contains(t, userMsg, "Previous stage context.")
	assert.Contains(t, userMsg, "Your Task")
}

func TestBuildFunctionCallingMessages_NoPrevStageContext(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	userMsg := messages[1].Content

	assert.Contains(t, userMsg, "first stage of analysis")
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

func TestBuildForcedConclusionPrompt(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildForcedConclusionPrompt(3)

	assert.Contains(t, result, "3 iterations")
	assert.Contains(t, result, "structured conclusion")
	assert.NotContains(t, result, "Final Answer:")
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

func TestBuildFunctionCallingMessages_ChatMode(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "Show me the pod status",
		InvestigationContext: "Investigation context.",
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")

	assert.Contains(t, messages[0].Content, "Chat Assistant Instructions")
	assert.Contains(t, messages[1].Content, "Show me the pod status")
}
