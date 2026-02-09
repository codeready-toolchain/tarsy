package prompt

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "update golden files")

// ---------------------------------------------------------------------------
// Serialization helpers
// ---------------------------------------------------------------------------

// serializeMessages serializes a slice of ConversationMessage into a
// deterministic text format for golden-file comparison.
func serializeMessages(msgs []agent.ConversationMessage) string {
	var sb strings.Builder
	for i, msg := range msgs {
		sb.WriteString(fmt.Sprintf("=== message[%d] role=%s ===\n", i, msg.Role))
		sb.WriteString(msg.Content)
		if i < len(msgs)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Golden file helpers
// ---------------------------------------------------------------------------

// goldenPath returns the path to a golden file for the given test name.
func goldenPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name+".golden")
}

// assertGolden compares actual against a golden file. If -update is set,
// the golden file is (re)written with actual content instead.
func assertGolden(t *testing.T, name string, actual string) {
	t.Helper()
	path := goldenPath(t, name)

	if *update {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(path, []byte(actual), 0o644)
		require.NoError(t, err, "failed to write golden file")
		t.Logf("updated golden file: %s", path)
		return
	}

	expected, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing — run with -update to create: %s", path)

	if string(expected) != actual {
		t.Errorf("golden file mismatch: %s\n%s", path, findFirstDiff(string(expected), actual))
	}
}

// findFirstDiff produces a human-readable description of where two strings diverge.
func findFirstDiff(expected, actual string) string {
	minLen := len(expected)
	if len(actual) < minLen {
		minLen = len(actual)
	}
	for i := 0; i < minLen; i++ {
		if expected[i] != actual[i] {
			start := i - 40
			if start < 0 {
				start = 0
			}
			end := i + 40
			eEnd := end
			if eEnd > len(expected) {
				eEnd = len(expected)
			}
			aEnd := end
			if aEnd > len(actual) {
				aEnd = len(actual)
			}
			return fmt.Sprintf(
				"first diff at byte %d:\n  expected: ...%q...\n  actual:   ...%q...",
				i, expected[start:eEnd], actual[start:aEnd],
			)
		}
	}
	if len(expected) != len(actual) {
		return fmt.Sprintf(
			"strings match until byte %d, then lengths differ: expected %d, actual %d",
			minLen, len(expected), len(actual),
		)
	}
	return "strings are identical"
}

// ---------------------------------------------------------------------------
// Realistic kubernetes-server instructions (matches builtin.go)
// ---------------------------------------------------------------------------

const k8sServerInstructions = `For Kubernetes operations:
- **IMPORTANT: In multi-cluster environments** (when the 'configuration_contexts_list' tool is available):
  * ALWAYS start by calling 'configuration_contexts_list' to see all available contexts and their server URLs
  * Use this information to determine which context to target before performing any operations
  * This prevents working on the wrong cluster and helps you understand the environment
- Be careful with cluster-scoped resource listings in large clusters
- Always prefer namespaced queries when possible
- If you get "server could not find the requested resource" error, check if you're using the namespace parameter correctly:
  * Cluster-scoped resources (Namespace, Node, ClusterRole, PersistentVolume) should NOT have a namespace parameter
  * Namespace-scoped resources (Pod, Deployment, Service, ConfigMap) REQUIRE a namespace parameter`

// synthesisCustomInstructions matches the SynthesisAgent custom instructions from builtin.go.
const synthesisCustomInstructions = `You are an Incident Commander synthesizing results from multiple parallel investigations.

Your task:
1. CRITICALLY EVALUATE each investigation's quality - prioritize results with strong evidence and sound reasoning
2. DISREGARD or deprioritize low-quality results that lack supporting evidence or contain logical errors
3. ANALYZE the original alert using the best available data from parallel investigations
4. INTEGRATE findings from high-quality investigations into a unified understanding
5. RECONCILE conflicting information by assessing which analysis provides better evidence
6. PROVIDE definitive root cause analysis based on the most reliable evidence
7. GENERATE actionable recommendations leveraging insights from the strongest investigations

Focus on solving the original alert/issue, not on meta-analyzing agent performance or comparing approaches.`

// ---------------------------------------------------------------------------
// Fixture builders
// ---------------------------------------------------------------------------

func newIntegrationBuilder() *PromptBuilder {
	registry := newTestMCPRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {Instructions: k8sServerInstructions},
	})
	return NewPromptBuilder(registry)
}

func newIntegrationExecCtx() *agent.ExecutionContext {
	return &agent.ExecutionContext{
		SessionID:      "test-session",
		AgentName:      "KubernetesAgent",
		AlertData:      `{"description": "Test alert scenario", "namespace": "test-namespace"}`,
		AlertType:      "test-investigation",
		RunbookContent: "# Test Runbook\nThis is a test runbook for integration testing.",
		Config: &agent.ResolvedAgentConfig{
			AgentName:          "KubernetesAgent",
			IterationStrategy:  config.IterationStrategyReact,
			MCPServers:         []string{"kubernetes-server"},
			CustomInstructions: "Be thorough.",
		},
	}
}

func newSynthesisExecCtx() *agent.ExecutionContext {
	return &agent.ExecutionContext{
		SessionID:      "test-session",
		AgentName:      "SynthesisAgent",
		AlertData:      `{"description": "Test alert scenario", "namespace": "test-namespace"}`,
		AlertType:      "test-investigation",
		RunbookContent: "# Test Runbook\nThis is a test runbook for integration testing.",
		Config: &agent.ResolvedAgentConfig{
			AgentName:          "SynthesisAgent",
			IterationStrategy:  config.IterationStrategySynthesis,
			MCPServers:         []string{}, // Synthesis has no MCP servers
			CustomInstructions: synthesisCustomInstructions,
		},
	}
}

// realisticInvestigationContext is a brief but structurally realistic
// investigation context matching what FormatInvestigationContext produces.
// It includes all key sections: header, initial request, agent responses,
// tool observations, and final analysis.
const realisticInvestigationContext = `═══════════════════════════════════════════════════════════════════════════════
📋 INVESTIGATION HISTORY
═══════════════════════════════════════════════════════════════════════════════

# Original Investigation

### Initial Investigation Request

Analyze this test-investigation alert and provide actionable recommendations.

## Alert Details

**Alert Type:** test-investigation

## Your Task
Investigate this alert using the available tools.

**Agent Response:**

Thought: I should check the pod status first.

Action: kubernetes-server.pods_list
Action Input: namespace: test-namespace

**Observation:**

Tool Result: kubernetes-server.pods_list:
{"items": [{"metadata": {"name": "pod-1"}, "status": {"phase": "CrashLoopBackOff"}}]}

**Agent Response:**

Thought: Pod-1 is in CrashLoopBackOff. Let me check the logs.

Final Answer: Pod-1 in test-namespace is in CrashLoopBackOff due to database connection timeout to db.example.com:5432.
`

func newChatExecCtx() *agent.ExecutionContext {
	ctx := newIntegrationExecCtx()
	ctx.ChatContext = &agent.ChatContext{
		UserQuestion:        "Can you check if the database service is running?",
		InvestigationContext: realisticInvestigationContext,
	}
	return ctx
}

func newChatExecCtxWithHistory() *agent.ExecutionContext {
	ctx := newChatExecCtx()
	ctx.ChatContext.ChatHistory = []agent.ChatExchange{
		{
			UserQuestion: "What was the root cause?",
			Messages: []agent.ConversationMessage{
				{Role: agent.RoleAssistant, Content: "The root cause was a database connection timeout to db.example.com:5432."},
				{Role: agent.RoleUser, Content: "Tool Result: kubernetes-server.kubectl_get:\n{\"result\": \"Service database is running\"}"},
				{Role: agent.RoleAssistant, Content: "The database service is running. The issue is with connectivity."},
			},
		},
	}
	return ctx
}

func integrationTools() []agent.ToolDefinition {
	return []agent.ToolDefinition{
		{
			Name:             "kubernetes-server.pods_list",
			Description:      "List pods in a namespace",
			ParametersSchema: `{"properties":{"namespace":{"type":"string","description":"Target namespace"}},"required":["namespace"]}`,
		},
		{
			Name:             "kubernetes-server.resources_get",
			Description:      "Get a Kubernetes resource",
			ParametersSchema: `{"properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"name":{"type":"string"},"namespace":{"type":"string"}},"required":["apiVersion","kind","name"]}`,
		},
	}
}

// ===========================================================================
// Investigation mode tests
// ===========================================================================

func TestIntegration_ReActInvestigation(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newIntegrationExecCtx()
	tools := integrationTools()

	messages := builder.BuildReActMessages(execCtx, "", tools)

	require.Len(t, messages, 2)
	assertGolden(t, "react_investigation", serializeMessages(messages))
}

func TestIntegration_ReActInvestigationWithContext(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newIntegrationExecCtx()
	tools := integrationTools()
	prevStageContext := "Agent found OOM issues in pod-1. Memory usage exceeded 512Mi limit."

	messages := builder.BuildReActMessages(execCtx, prevStageContext, tools)

	require.Len(t, messages, 2)
	assertGolden(t, "react_investigation_with_context", serializeMessages(messages))
}

func TestIntegration_NativeThinkingInvestigation(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newIntegrationExecCtx()
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking

	messages := builder.BuildNativeThinkingMessages(execCtx, "")

	require.Len(t, messages, 2)
	assertGolden(t, "native_thinking_investigation", serializeMessages(messages))
}

// ===========================================================================
// Synthesis test
// ===========================================================================

func TestIntegration_Synthesis(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newSynthesisExecCtx()
	prevStageContext := `### Results from parallel stage 'investigation':

**Parallel Execution Summary**: 2/2 agents succeeded

#### Agent 1: KubernetesAgent (google-default, native-thinking)
**Status**: completed

Pod pod-1 is in CrashLoopBackOff state due to OOM kills.

#### Agent 2: LogAgent (anthropic-default, react)
**Status**: completed

Log analysis reveals database connection timeout errors to db.example.com:5432.`

	messages := builder.BuildSynthesisMessages(execCtx, prevStageContext)

	require.Len(t, messages, 2)
	assertGolden(t, "synthesis", serializeMessages(messages))
}

// ===========================================================================
// Chat mode tests
// ===========================================================================

func TestIntegration_ReActChat(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newChatExecCtx()
	tools := integrationTools()

	messages := builder.BuildReActMessages(execCtx, "", tools)

	require.Len(t, messages, 2)
	assertGolden(t, "react_chat", serializeMessages(messages))
}

func TestIntegration_ReActChatWithHistory(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newChatExecCtxWithHistory()
	tools := integrationTools()

	messages := builder.BuildReActMessages(execCtx, "", tools)

	require.Len(t, messages, 2)
	assertGolden(t, "react_chat_with_history", serializeMessages(messages))
}

func TestIntegration_NativeThinkingChat(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newChatExecCtx()
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking

	messages := builder.BuildNativeThinkingMessages(execCtx, "")

	require.Len(t, messages, 2)
	assertGolden(t, "native_thinking_chat", serializeMessages(messages))
}

// ===========================================================================
// Forced conclusion tests
// ===========================================================================

func TestIntegration_ForcedConclusionReAct(t *testing.T) {
	builder := newIntegrationBuilder()
	result := builder.BuildForcedConclusionPrompt(5, config.IterationStrategyReact)

	assertGolden(t, "forced_conclusion_react", result)
}

func TestIntegration_ForcedConclusionNativeThinking(t *testing.T) {
	builder := newIntegrationBuilder()
	result := builder.BuildForcedConclusionPrompt(3, config.IterationStrategyNativeThinking)

	assertGolden(t, "forced_conclusion_native_thinking", result)
}

// ===========================================================================
// Utility prompt tests
// ===========================================================================

func TestIntegration_MCPSummarization(t *testing.T) {
	builder := newIntegrationBuilder()

	systemPrompt := builder.BuildMCPSummarizationSystemPrompt("kubernetes-server", "pods_list", 500)
	userPrompt := builder.BuildMCPSummarizationUserPrompt(
		"Investigating CrashLoopBackOff in pod-1.",
		"kubernetes-server", "pods_list",
		`{"items": [{"metadata": {"name": "pod-1"}, "status": {"phase": "Running"}}]}`,
	)

	combined := systemPrompt + "\n\n=== USER PROMPT ===\n\n" + userPrompt
	assertGolden(t, "mcp_summarization", combined)
}

func TestIntegration_ExecutiveSummary(t *testing.T) {
	builder := newIntegrationBuilder()

	systemPrompt := builder.BuildExecutiveSummarySystemPrompt()
	userPrompt := builder.BuildExecutiveSummaryUserPrompt(
		"Root cause: OOM kill due to memory leak in pod-1. Recommendation: increase memory limit to 1Gi.",
	)

	combined := systemPrompt + "\n\n=== USER PROMPT ===\n\n" + userPrompt
	assertGolden(t, "executive_summary", combined)
}

// ===========================================================================
// Sanity checks: verify golden files match expected structural properties
// ===========================================================================

func TestIntegration_SynthesisSystemHasNoTaskFocus(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newSynthesisExecCtx()

	messages := builder.BuildSynthesisMessages(execCtx, "some results")
	require.NotEmpty(t, messages, "BuildSynthesisMessages returned empty slice")
	systemMsg := messages[0].Content

	// Synthesis should NOT have the taskFocus suffix
	assert.NotContains(t, systemMsg, "Focus on investigation and providing recommendations")
	// But should have the synthesis custom instructions
	assert.Contains(t, systemMsg, "Incident Commander")
}

func TestIntegration_ChatSystemUsesCorrectTier1(t *testing.T) {
	builder := newIntegrationBuilder()
	execCtx := newChatExecCtx()

	messages := builder.BuildReActMessages(execCtx, "", nil)
	require.NotEmpty(t, messages, "BuildReActMessages returned empty slice")
	systemMsg := messages[0].Content

	// Chat mode should use chat instructions, not investigation
	assert.Contains(t, systemMsg, "Chat Assistant Instructions")
	assert.NotContains(t, systemMsg, "General SRE Agent Instructions")
}
