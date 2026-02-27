package prompt

import (
	"strings"
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
	// Auto-injected orchestrator behavioral instructions
	assert.Contains(t, system.Content, "Orchestrator Strategy")
	assert.Contains(t, system.Content, "Dispatch relevant sub-agents in parallel")
	assert.Contains(t, system.Content, "Available Sub-Agents")
	assert.Contains(t, system.Content, "LogAnalyzer")
	assert.Contains(t, system.Content, "GeneralWorker")
	assert.Contains(t, system.Content, "dispatch_agent")
	assert.Contains(t, system.Content, "Sub-agent results are delivered to you automatically")
	assert.Contains(t, system.Content, "[Sub-agent completed]")
	assert.Contains(t, system.Content, orchestratorTaskFocus)
	// Tier 1 instructions
	assert.Contains(t, system.Content, "General SRE Agent Instructions")
	// Tier 2: MCP server instructions (from "kubernetes-server" in test registry)
	assert.Contains(t, system.Content, "K8s server instructions.")
	// Tier 3: custom instructions from agent config
	assert.Contains(t, system.Content, "Be thorough.")
}

func TestBuildOrchestratorMessages_PromptLayerOrder(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeOrchestrator
	execCtx.Config.CustomInstructions = "Domain-specific security instructions."
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs"},
	}

	messages := builder.buildOrchestratorMessages(execCtx, "")
	system := messages[0].Content

	// Verify ordering: Tier 1 → Tier 3 (custom) → behavioral → catalog → delivery → focus
	tier1Pos := strings.Index(system, "General SRE Agent Instructions")
	customPos := strings.Index(system, "Domain-specific security instructions.")
	behavioralPos := strings.Index(system, "Orchestrator Strategy")
	catalogPos := strings.Index(system, "Available Sub-Agents")
	deliveryPos := strings.Index(system, "Result Delivery")
	focusPos := strings.Index(system, orchestratorTaskFocus)

	require.Greater(t, tier1Pos, -1, "Tier 1 should be present")
	require.Greater(t, customPos, -1, "Custom instructions should be present")
	require.Greater(t, behavioralPos, -1, "Behavioral instructions should be present")
	require.Greater(t, catalogPos, -1, "Catalog should be present")
	require.Greater(t, deliveryPos, -1, "Delivery should be present")
	require.Greater(t, focusPos, -1, "Focus should be present")

	assert.Less(t, tier1Pos, customPos, "Tier 1 should come before custom instructions")
	assert.Less(t, customPos, behavioralPos, "Custom instructions should come before behavioral (via ComposeInstructions)")
	assert.Less(t, behavioralPos, catalogPos, "Behavioral should come before catalog")
	assert.Less(t, catalogPos, deliveryPos, "Catalog should come before delivery")
	assert.Less(t, deliveryPos, focusPos, "Delivery should come before focus")
}

func TestBuildOrchestratorMessages_NoCustomInstructions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.Type = config.AgentTypeOrchestrator
	execCtx.Config.CustomInstructions = "" // Built-in Orchestrator case
	execCtx.SubAgentCatalog = []config.SubAgentEntry{}

	messages := builder.buildOrchestratorMessages(execCtx, "")
	system := messages[0].Content

	// Behavioral instructions still present even without custom instructions
	assert.Contains(t, system, "Orchestrator Strategy")
	assert.Contains(t, system, "Dispatch relevant sub-agents in parallel")
	// No stale "Agent-Specific Instructions" header when custom instructions are empty
	assert.NotContains(t, system, "Agent-Specific Instructions")
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
