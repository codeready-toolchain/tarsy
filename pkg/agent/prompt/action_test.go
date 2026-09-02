package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildActionMessages_Structure(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.buildActionMessages(execCtx, "Stage 1: found evidence of compromise.")
	require.Len(t, messages, 2)
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Equal(t, agent.RoleUser, messages[1].Role)
}

func TestBuildActionMessages_SafetyPreamblePresent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.buildActionMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "Action Agent Safety Guidelines")
	assert.Contains(t, system, "Require hard evidence before acting")
	assert.Contains(t, system, "avoid re-investigating")
	assert.Contains(t, system, "evidence is ambiguous or conflicting")
	assert.Contains(t, system, "Explain your reasoning BEFORE executing any action tool")
	assert.Contains(t, system, "Prefer inaction over incorrect action")
	assert.Contains(t, system, "short action memo")
	assert.Contains(t, system, "Do not reprint or re-author the investigation report")
}

func TestBuildActionMessages_Tier1Through3Composed(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction
	execCtx.Config.CustomInstructions = "Only shut down VMs classified MALICIOUS."

	messages := builder.buildActionMessages(execCtx, "")
	system := messages[0].Content

	// Tier 1: general SRE instructions
	assert.Contains(t, system, "General SRE Agent Instructions")
	// Tier 2: MCP server instructions
	assert.Contains(t, system, "K8s server instructions")
	// Tier 3: custom instructions
	assert.Contains(t, system, "Only shut down VMs classified MALICIOUS.")
}

func TestBuildActionMessages_TaskFocusPresent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.buildActionMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "evaluating the upstream investigation findings")
	assert.Contains(t, system, "short action memo, not a copy of the investigation")
}

func TestBuildActionMessages_UserMessageHasContext(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.buildActionMessages(execCtx, "Investigation found root cause: OOM kill")
	user := messages[1].Content

	assert.Contains(t, user, "Alert Details")
	assert.Contains(t, user, "test-alert")
	assert.Contains(t, user, "Runbook Content")
	assert.Contains(t, user, "Investigation found root cause: OOM kill")

	// Action-specific task (not the investigation analysisTask)
	assert.Contains(t, user, "Evaluate the upstream investigation findings")
	assert.Contains(t, user, "short action memo covering")
	assert.Contains(t, user, "Do not copy the investigation report")
	assert.NotContains(t, user, "Use the available tools to investigate this alert")

	// Output schema for YES/NO marker
	assert.Contains(t, user, "YES or NO on the very last line")
}

func TestBuildActionMessages_InjectsOrchestratorWhenCatalogSet(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "GeneralWorker", Description: "Pure reasoning"},
	}

	messages := builder.buildActionMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "Action Agent Safety Guidelines")
	assert.Contains(t, system, "Orchestrator Strategy")
	assert.Contains(t, system, "Available Sub-Agents")
	assert.Contains(t, system, "GeneralWorker")
	assert.Contains(t, system, "Result Delivery")
	assert.Contains(t, system, "evaluating the upstream investigation findings")
}

func TestBuildActionMessages_NoOrchestratorWithoutCatalog(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction
	execCtx.SubAgentCatalog = nil

	messages := builder.buildActionMessages(execCtx, "")
	system := messages[0].Content

	assert.NotContains(t, system, "Orchestrator Strategy")
	assert.NotContains(t, system, "Available Sub-Agents")
}

func TestBuildActionMessages_OmitsCopyPreserveLanguage(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeAction

	messages := builder.buildActionMessages(execCtx, "prior findings")
	require.Len(t, messages, 2)
	combined := messages[0].Content + "\n" + messages[1].Content

	forbidden := []string{
		"Preserve the investigation report",
		"final report becomes the finalAnalysis",
		"preserve the investigation report as-is",
		"amended report that preserves",
		"finalAnalysis",
	}
	for _, phrase := range forbidden {
		t.Run(phrase, func(t *testing.T) {
			assert.NotContains(t, combined, phrase)
		})
	}
}
