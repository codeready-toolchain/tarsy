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
	return NewPromptBuilder(registry, nil)
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
	tests := []struct {
		name   string
		reason agent.WrapUpReason
	}{
		{name: "max_iterations", reason: agent.WrapUpReasonMaxIterations},
		{name: "empty reason uses iteration wording", reason: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := builder.BuildForcedConclusionPrompt(3, tt.reason)
			assert.Contains(t, result, "3 iterations")
			assert.Contains(t, result, "structured conclusion")
			assert.NotContains(t, result, "Final Answer:")
			assert.NotContains(t, result, "time budget")
		})
	}
}

func TestBuildForcedConclusionPrompt_TimeBudget(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildForcedConclusionPrompt(3, agent.WrapUpReasonTimeBudget)

	assert.Contains(t, result, "time budget")
	assert.Contains(t, result, "structured conclusion")
	assert.NotContains(t, result, "iteration limit")
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

func TestBuildComposePrompts(t *testing.T) {
	builder := newBuilderForTest()

	systemPrompt := builder.BuildComposeSystemPrompt()
	assert.Contains(t, systemPrompt, "copy-editor")
	assert.Contains(t, systemPrompt, "Those two tracks are not interchangeable")
	assert.Contains(t, systemPrompt, "restarting a pod does not fulfill a recommendation to change a resource limit")
	assert.Contains(t, systemPrompt, "Do not invent a TARSy-standard template")
	assert.Contains(t, systemPrompt, "Do not improve prose")
	assert.Contains(t, systemPrompt, "Do not drop sections")

	userPrompt := builder.BuildComposeUserPrompt("upstream findings", "no action taken")
	assert.Contains(t, userPrompt, "=== UPSTREAM REPORT ===")
	assert.Contains(t, userPrompt, "=== END UPSTREAM REPORT ===")
	assert.Contains(t, userPrompt, "=== ACTION MEMO ===")
	assert.Contains(t, userPrompt, "=== END ACTION MEMO ===")
	assert.Contains(t, userPrompt, "upstream findings")
	assert.Contains(t, userPrompt, "no action taken")
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

func TestBuildFunctionCallingMessages_OrchestratorInjection(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)

	assert.Contains(t, messages[0].Content, "Orchestrator Strategy")
	assert.Contains(t, messages[0].Content, "Available Sub-Agents")
	assert.Contains(t, messages[0].Content, "LogAnalyzer")
	assert.Contains(t, messages[0].Content, "Prefer sub-agents when")
	assert.Contains(t, messages[1].Content, "Alert Details")
}

func TestBuildFunctionCallingMessages_NoOrchestratorWithoutCatalog(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)

	assert.NotContains(t, messages[0].Content, "Orchestrator Strategy")
	assert.NotContains(t, messages[0].Content, "Available Sub-Agents")
}

func TestBuildFunctionCallingMessages_ActionMode(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.BuildFunctionCallingMessages(execCtx, "Investigation found malicious activity.")
	require.Len(t, messages, 2)

	assert.Contains(t, messages[0].Content, "Action Agent Safety Guidelines")
	assert.Contains(t, messages[0].Content, "Require hard evidence")
	assert.Contains(t, messages[0].Content, "Prefer inaction over incorrect action")
	assert.Contains(t, messages[0].Content, "General SRE Agent Instructions")
	assert.Contains(t, messages[0].Content, "evaluating the upstream investigation findings")
	assert.Contains(t, messages[0].Content, "short action memo")
	assert.NotContains(t, messages[0].Content, "Preserve the investigation report")
	assert.Contains(t, messages[1].Content, "Alert Details")
	assert.Contains(t, messages[1].Content, "Investigation found malicious activity.")
	assert.Contains(t, messages[1].Content, "short action memo covering")
	assert.NotContains(t, messages[1].Content, "amended report that preserves")
}

func TestBuildFunctionCallingMessages_ActionModeWithCatalog(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "GeneralWorker", Description: "Pure reasoning"},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "Investigation found malicious activity.")
	require.Len(t, messages, 2)

	assert.Contains(t, messages[0].Content, "Action Agent Safety Guidelines")
	assert.Contains(t, messages[0].Content, "Prefer inaction over incorrect action")
	assert.Contains(t, messages[0].Content, "Orchestrator Strategy")
	assert.Contains(t, messages[0].Content, "Available Sub-Agents")
	assert.Contains(t, messages[0].Content, "GeneralWorker")
	assert.Contains(t, messages[0].Content, "short action memo, not a copy of the investigation")
	assert.NotContains(t, messages[0].Content, "Prefer sub-agents when parallel or specialized work")
}

func TestBuildFunctionCallingMessages_SubAgentMode(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgent = &agent.SubAgentContext{
		Task:         "Find 5xx errors in the last hour",
		ParentExecID: "parent-exec-1",
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "Previous data")
	require.Len(t, messages, 2)

	// System: normal Tier 1-3 instructions
	assert.Contains(t, messages[0].Content, "General SRE Agent Instructions")

	// User: task only, no investigation context
	assert.Contains(t, messages[1].Content, "## Task")
	assert.Contains(t, messages[1].Content, "Find 5xx errors")
	assert.NotContains(t, messages[1].Content, "Alert Details")
	assert.NotContains(t, messages[1].Content, "Previous data")
}

func TestBuildFunctionCallingMessages_ChatModeWithOrchestration(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.ChatContext = &agent.ChatContext{
		UserQuestion:         "Can you check the failing pods?",
		InvestigationContext: "Previous investigation context.",
	}
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)

	system := messages[0].Content
	assert.Contains(t, system, "Chat Assistant Instructions")
	assert.Contains(t, system, "Orchestrator Strategy")
	assert.Contains(t, system, "Available Sub-Agents")
	assert.Contains(t, system, "LogAnalyzer")
	assert.Contains(t, system, "Prefer sub-agents when")

	assert.Contains(t, messages[1].Content, "Can you check the failing pods?")
}

func TestBuildFunctionCallingMessages_EmptyCatalogNoOrchestration(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgentCatalog = []config.SubAgentEntry{}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)

	assert.NotContains(t, messages[0].Content, "Orchestrator Strategy")
	assert.NotContains(t, messages[0].Content, "Available Sub-Agents")
	assert.Contains(t, messages[0].Content, "Focus on investigation")
}
