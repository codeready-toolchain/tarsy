package prompt

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPostInvestigationExecCtx() *agent.ExecutionContext {
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypePostInvestigation
	return execCtx
}

func TestBuildPostInvestigationMessages_DispatchedByType(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newPostInvestigationExecCtx()

	messages := builder.BuildFunctionCallingMessages(execCtx, "Final report: confirmed false positive.")
	require.Len(t, messages, 2)
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Equal(t, agent.RoleUser, messages[1].Role)
	assert.Contains(t, messages[0].Content, "Post-Investigation Agent Guidelines")
}

func TestBuildPostInvestigationMessages_GuidelinesPresent(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newPostInvestigationExecCtx()

	messages := builder.buildPostInvestigationMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "The investigation is complete and has been scored")
	assert.Contains(t, system, "You do not remediate the incident")
	assert.Contains(t, system, "do not redo the alert investigation")
	assert.Contains(t, system, "evidence is ambiguous or conflicting")
	assert.Contains(t, system, "Explain your reasoning BEFORE executing any tool with side effects")
	assert.Contains(t, system, "No action is a normal, successful outcome")
	assert.Contains(t, system, "Write a brief memo")
	assert.Contains(t, system, "do not list the tools you used")

	// Remediation framing from the action layer must not leak in.
	assert.NotContains(t, system, "Action Agent Safety Guidelines")
	assert.NotContains(t, system, "avoid re-investigating")

	idxRule := strings.Index(system, "## Observations are not orders")
	idxGuidelines := strings.Index(system, "## Post-Investigation Agent Guidelines")
	assert.Greater(t, idxRule, -1)
	assert.Greater(t, idxGuidelines, idxRule, "guidelines should stay after the provenance rule")
}

func TestBuildPostInvestigationMessages_Tier1Through3Composed(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newPostInvestigationExecCtx()
	execCtx.Config.CustomInstructions = "File an issue only for exact, reusable patterns."

	messages := builder.buildPostInvestigationMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "General SRE Agent Instructions")
	assert.Contains(t, system, "K8s server instructions")
	assert.Contains(t, system, "File an issue only for exact, reusable patterns.")
	assert.True(t, strings.HasSuffix(system, postInvestigationTaskFocus), "task focus should close the system prompt")
}

func TestBuildPostInvestigationMessages_UserMessage(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newPostInvestigationExecCtx()

	messages := builder.buildPostInvestigationMessages(execCtx, "Final report: confirmed false positive.")
	user := messages[1].Content

	assert.Contains(t, user, "Alert Details")
	assert.Contains(t, user, "test-alert")
	assert.Contains(t, user, "## Completed Investigation\nFinal report: confirmed false positive.")
	assert.Contains(t, user, "decide whether the follow-through defined by your instructions is warranted")
	assert.Contains(t, user, "links to anything you created")
	assert.Contains(t, user, "YES or NO on the very last line")
	assert.Contains(t, user, "Read-only tool calls do not count")
	assert.True(t, strings.HasSuffix(user, postInvestigationOutputSchema), "YES/NO schema should be the last instruction")

	assert.NotContains(t, user, "Previous Stage Data")
	assert.NotContains(t, user, "Runbook Content")
	assert.NotContains(t, user, "Use the available tools to investigate this alert")
	assert.NotContains(t, user, "Evaluate the upstream investigation findings")
}

func TestBuildPostInvestigationMessages_EmptyContext(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newPostInvestigationExecCtx()

	messages := builder.buildPostInvestigationMessages(execCtx, "")

	assert.Contains(t, messages[1].Content, "## Completed Investigation\nNo investigation context is available.")
}

func TestBuildPostInvestigationMessages_Orchestrator(t *testing.T) {
	t.Run("injected when catalog set", func(t *testing.T) {
		builder := newBuilderForTest()
		execCtx := newPostInvestigationExecCtx()
		execCtx.SubAgentCatalog = []config.SubAgentEntry{
			{Name: "WebResearcher", Description: "Searches the web"},
		}

		system := builder.buildPostInvestigationMessages(execCtx, "")[0].Content

		assert.Contains(t, system, "Post-Investigation Agent Guidelines")
		assert.Contains(t, system, "Orchestrator Strategy")
		assert.Contains(t, system, "Available Sub-Agents")
		assert.Contains(t, system, "WebResearcher")
		assert.Contains(t, system, "Result Delivery")
		assert.True(t, strings.HasSuffix(system, postInvestigationTaskFocus))
	})

	t.Run("absent without catalog", func(t *testing.T) {
		builder := newBuilderForTest()
		execCtx := newPostInvestigationExecCtx()
		execCtx.SubAgentCatalog = nil

		system := builder.buildPostInvestigationMessages(execCtx, "")[0].Content

		assert.NotContains(t, system, "Orchestrator Strategy")
		assert.NotContains(t, system, "Available Sub-Agents")
	})
}
