package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatAgentCatalog_MCPAgent(t *testing.T) {
	entries := []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
	}
	result := formatAgentCatalog(entries)

	assert.Contains(t, result, "## Available Sub-Agents")
	assert.Contains(t, result, "dispatch_agent")
	assert.Contains(t, result, "**LogAnalyzer**: Analyzes logs")
	assert.Contains(t, result, "MCP tools: loki")
}

func TestFormatAgentCatalog_NativeToolsAgent(t *testing.T) {
	entries := []config.SubAgentEntry{
		{Name: "WebResearcher", Description: "Searches the web", NativeTools: []string{"google_search", "url_context"}},
	}
	result := formatAgentCatalog(entries)

	assert.Contains(t, result, "**WebResearcher**: Searches the web")
	assert.Contains(t, result, "Native tools: google_search, url_context")
}

func TestFormatAgentCatalog_PureReasoningAgent(t *testing.T) {
	entries := []config.SubAgentEntry{
		{Name: "GeneralWorker", Description: "General-purpose agent"},
	}
	result := formatAgentCatalog(entries)

	assert.Contains(t, result, "**GeneralWorker**: General-purpose agent")
	assert.Contains(t, result, "Tools: none (pure reasoning)")
}

func TestFormatAgentCatalog_BothMCPAndNativeTools(t *testing.T) {
	entries := []config.SubAgentEntry{
		{Name: "HybridAgent", Description: "Has both tool types", MCPServers: []string{"loki"}, NativeTools: []string{"google_search"}},
	}
	result := formatAgentCatalog(entries)

	assert.Contains(t, result, "**HybridAgent**: Has both tool types")
	assert.Contains(t, result, "MCP tools: loki")
	assert.Contains(t, result, "Native tools: google_search")
	assert.NotContains(t, result, "pure reasoning")
}

func TestFormatAgentCatalog_MultipleAgents(t *testing.T) {
	entries := []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
		{Name: "WebResearcher", Description: "Searches the web", NativeTools: []string{"google_search"}},
		{Name: "GeneralWorker", Description: "General-purpose"},
	}
	result := formatAgentCatalog(entries)

	assert.Contains(t, result, "LogAnalyzer")
	assert.Contains(t, result, "WebResearcher")
	assert.Contains(t, result, "GeneralWorker")
}

func TestFormatAgentCatalog_EmptyEntries(t *testing.T) {
	result := formatAgentCatalog(nil)

	assert.Contains(t, result, "## Available Sub-Agents")
	assert.Contains(t, result, "dispatch_agent")
	assert.NotContains(t, result, "**")
}

func TestBuildOrchestratorMessages_SystemIncludesCatalog(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeOrchestrator
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
		{Name: "GeneralWorker", Description: "General-purpose agent"},
	}

	messages := builder.buildOrchestratorMessages(execCtx, "")
	require.Len(t, messages, 2)

	system := messages[0]
	assert.Equal(t, agent.RoleSystem, system.Role)
	assert.Contains(t, system.Content, "Available Sub-Agents")
	assert.Contains(t, system.Content, "LogAnalyzer")
	assert.Contains(t, system.Content, "GeneralWorker")
	assert.Contains(t, system.Content, "dispatch_agent")
	assert.Contains(t, system.Content, "Sub-agent results appear automatically")
	assert.Contains(t, system.Content, "[Sub-agent completed]")
	assert.Contains(t, system.Content, orchestratorTaskFocus)
	// Tier 1 instructions
	assert.Contains(t, system.Content, "General SRE Agent Instructions")
	// Tier 2: MCP server instructions (from "kubernetes-server" in test registry)
	assert.Contains(t, system.Content, "K8s server instructions.")
	// Tier 3: custom instructions from agent config
	assert.Contains(t, system.Content, "Be thorough.")
}

func TestBuildOrchestratorMessages_UserIncludesAlert(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeOrchestrator
	execCtx.SubAgentCatalog = []config.SubAgentEntry{}

	messages := builder.buildOrchestratorMessages(execCtx, "Previous findings")
	require.Len(t, messages, 2)

	user := messages[1]
	assert.Equal(t, agent.RoleUser, user.Role)
	assert.Contains(t, user.Content, "Alert Details")
	assert.Contains(t, user.Content, "test-alert")
	assert.Contains(t, user.Content, "Previous findings")
}
