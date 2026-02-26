package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSubAgentMessages_TaskOnly(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgent = &agent.SubAgentContext{
		Task:         "Find all 5xx errors for service-X in the last 30 min.",
		ParentExecID: "parent-exec-123",
	}

	messages := builder.buildSubAgentMessages(execCtx)
	require.Len(t, messages, 2)

	system := messages[0]
	assert.Equal(t, agent.RoleSystem, system.Role)
	assert.Contains(t, system.Content, "General SRE Agent Instructions")
	assert.Contains(t, system.Content, "sub-agent dispatched by an orchestrator")
	assert.Contains(t, system.Content, "reported back to the orchestrator")

	user := messages[1]
	assert.Equal(t, agent.RoleUser, user.Role)
	assert.Contains(t, user.Content, "## Task")
	assert.Contains(t, user.Content, "Find all 5xx errors for service-X")

	// Sub-agent user message must NOT contain investigation-specific sections
	assert.NotContains(t, user.Content, "Alert Details")
	assert.NotContains(t, user.Content, "Runbook Content")
	assert.NotContains(t, user.Content, "Previous Stage Data")
}

func TestBuildSubAgentMessages_IncludesCustomInstructions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := &agent.ExecutionContext{
		SessionID: "test-session",
		AgentName: "LogAnalyzer",
		Config: &agent.ResolvedAgentConfig{
			AgentName:          "LogAnalyzer",
			Type:               config.AgentTypeDefault,
			LLMBackend:         config.LLMBackendNativeGemini,
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "Focus on OOMKilled events.",
		},
		SubAgent: &agent.SubAgentContext{
			Task:         "Check pod logs for errors",
			ParentExecID: "parent-exec-456",
		},
	}

	messages := builder.buildSubAgentMessages(execCtx)
	require.Len(t, messages, 2)

	system := messages[0]
	assert.Contains(t, system.Content, "Focus on OOMKilled events.")
	assert.Contains(t, system.Content, "K8s server instructions.")

	user := messages[1]
	assert.Contains(t, user.Content, "Check pod logs for errors")
}
