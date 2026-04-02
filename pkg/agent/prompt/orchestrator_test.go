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

func TestOrchestratorPrompt_WebResearcherOnlyWhenListedInCatalog(t *testing.T) {
	// Dispatch guidance for WebResearcher must come from the sub-agent Description
	// (built-in or YAML), not from global orchestratorBehavioralInstructions — chains
	// may omit WebResearcher from sub_agents entirely.
	longDesc := "Public web research; use for GitHub repos and OpenShift Route URLs."

	noWeb := InjectOrchestratorSections("Base.", []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
		{Name: "GeneralWorker", Description: "General-purpose"},
	})
	assert.NotContains(t, noWeb, "**WebResearcher**", "orchestrator prompt must not mention WebResearcher when it is not in the catalog")

	withWeb := InjectOrchestratorSections("Base.", []config.SubAgentEntry{
		{Name: "WebResearcher", Description: longDesc, NativeTools: []string{"google_search", "url_context"}},
	})
	assert.Contains(t, withWeb, "**WebResearcher**: "+longDesc)
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

func TestInjectOrchestratorSections_Content(t *testing.T) {
	catalog := []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
		{Name: "GeneralWorker", Description: "General-purpose agent"},
	}

	result := InjectOrchestratorSections("Base system prompt.", catalog)

	assert.Contains(t, result, "Base system prompt.")
	assert.Contains(t, result, "Orchestrator Strategy")
	assert.Contains(t, result, "Prefer dispatching sub-agents")
	assert.Contains(t, result, "Available Sub-Agents")
	assert.Contains(t, result, "LogAnalyzer")
	assert.Contains(t, result, "GeneralWorker")
	assert.Contains(t, result, "dispatch_agent")
	assert.Contains(t, result, "Sub-agent results are delivered to you automatically")
	assert.Contains(t, result, "NEVER predict, fabricate, or speculate")
	assert.Contains(t, result, "keep a mental checklist of every agent you dispatch")
}

func TestInjectOrchestratorSections_BaseContentIsPrefix(t *testing.T) {
	base := "Existing agent system prompt with custom instructions."
	result := InjectOrchestratorSections(base, []config.SubAgentEntry{
		{Name: "Worker", Description: "A worker"},
	})

	assert.True(t, strings.HasPrefix(result, base),
		"injected prompt must start with the original base content")
	behavioralPos := strings.Index(result, "Orchestrator Strategy")
	assert.Greater(t, behavioralPos, len(base),
		"orchestrator sections must come after the base content")
}

func TestOrchestratorTaskFocus(t *testing.T) {
	focus := OrchestratorTaskFocus()
	assert.NotEmpty(t, focus)
	assert.Contains(t, focus, "Prefer sub-agents when")
}

func TestInjectOrchestratorSections_ViaBuilder(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs", MCPServers: []string{"loki"}},
		{Name: "GeneralWorker", Description: "General-purpose agent"},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	require.Len(t, messages, 2)

	system := messages[0]
	assert.Equal(t, agent.RoleSystem, system.Role)
	assert.Contains(t, system.Content, "Orchestrator Strategy")
	assert.Contains(t, system.Content, "Available Sub-Agents")
	assert.Contains(t, system.Content, "LogAnalyzer")
	assert.Contains(t, system.Content, "GeneralWorker")
	assert.Contains(t, system.Content, orchestratorTaskFocus)
	assert.Contains(t, system.Content, "General SRE Agent Instructions")
	assert.Contains(t, system.Content, "K8s server instructions.")
	assert.Contains(t, system.Content, "Be thorough.")
}

func TestInjectOrchestratorSections_PromptLayerOrder(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.CustomInstructions = "Domain-specific security instructions."
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs"},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	system := messages[0].Content

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

func TestInjectOrchestratorSections_NoCustomInstructions(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.Config.CustomInstructions = ""
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs"},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "")
	system := messages[0].Content

	assert.Contains(t, system, "Orchestrator Strategy")
	assert.Contains(t, system, "Prefer dispatching sub-agents")
	assert.NotContains(t, system, "Agent-Specific Instructions")
}

func TestInjectOrchestratorSections_UserIncludesAlert(t *testing.T) {
	builder := newBuilderForTest()
	execCtx := newFullExecCtx()
	execCtx.SubAgentCatalog = []config.SubAgentEntry{
		{Name: "LogAnalyzer", Description: "Analyzes logs"},
	}

	messages := builder.BuildFunctionCallingMessages(execCtx, "Previous findings")
	require.Len(t, messages, 2)

	user := messages[1]
	assert.Equal(t, agent.RoleUser, user.Role)
	assert.Contains(t, user.Content, "Alert Details")
	assert.Contains(t, user.Content, "test-alert")
	assert.Contains(t, user.Content, "Previous findings")
}
