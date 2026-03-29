package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// AssertGolden compares actual output against a golden file.
// If -update flag is set, writes actual to the golden file instead.
func AssertGolden(t *testing.T, goldenPath string, actual []byte) {
	t.Helper()

	if *updateGolden {
		dir := filepath.Dir(goldenPath)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file not found: %s (run with -update to create)", goldenPath)
	assert.Equal(t, string(expected), string(actual), "golden mismatch: %s", goldenPath)
}

// AssertGoldenJSON normalizes JSON and compares against a golden file.
// The actual value is marshalled with sorted keys and indentation.
func AssertGoldenJSON(t *testing.T, goldenPath string, actual interface{}, normalizer *Normalizer) {
	t.Helper()

	data, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)

	if normalizer != nil {
		data = normalizer.NormalizeBytes(data)
	}

	// Ensure trailing newline for clean diffs.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	AssertGolden(t, goldenPath, data)
}

// goldenDir returns the path to the testdata/golden directory for a scenario.
func goldenDir(scenario string) string {
	return filepath.Join("testdata", "golden", scenario)
}

// GoldenPath returns the path to a specific golden file for a scenario.
func GoldenPath(scenario, filename string) string {
	return filepath.Join(goldenDir(scenario), filename)
}

// ────────────────────────────────────────────────────────────
// Human-readable interaction golden files
// ────────────────────────────────────────────────────────────

// AssertGoldenLLMInteraction renders an LLM interaction detail response in a
// human-readable format: metadata as JSON, then conversation messages as
// readable text blocks (not JSON-escaped strings).
func AssertGoldenLLMInteraction(t *testing.T, goldenPath string, detail map[string]interface{}, normalizer *Normalizer) {
	t.Helper()

	var buf strings.Builder

	// ── Metadata section (JSON) ──
	meta := make(map[string]interface{})
	for k, v := range detail {
		if k != "conversation" {
			meta[k] = v
		}
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	require.NoError(t, err)
	buf.Write(metaJSON)
	buf.WriteString("\n")

	// ── Conversation section (human-readable) ──
	conversation, _ := detail["conversation"].([]interface{})
	if len(conversation) > 0 {
		buf.WriteString("\n")
		for _, rawMsg := range conversation {
			msg, _ := rawMsg.(map[string]interface{})
			role, _ := msg["role"].(string)

			// Build header line.
			header := fmt.Sprintf("=== MESSAGE: %s", role)
			if toolCallID, ok := msg["tool_call_id"].(string); ok && toolCallID != "" {
				toolName, _ := msg["tool_name"].(string)
				header += fmt.Sprintf(" (%s, %s)", toolCallID, toolName)
			}
			header += " ==="
			buf.WriteString(header + "\n")

			// Content (rendered as plain text — no JSON escaping).
			if content, _ := msg["content"].(string); content != "" {
				buf.WriteString(content)
				buf.WriteString("\n")
			}

			// Tool calls (for assistant messages).
			if toolCalls, ok := msg["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
				buf.WriteString("--- TOOL_CALLS ---\n")
				for _, rawTC := range toolCalls {
					tc, _ := rawTC.(map[string]interface{})
					callID, _ := tc["id"].(string)
					name, _ := tc["name"].(string)
					args, _ := tc["arguments"].(string)
					buf.WriteString(fmt.Sprintf("[%s] %s(%s)\n", callID, name, args))
				}
			}

			buf.WriteString("\n")
		}
	}

	data := []byte(buf.String())
	if normalizer != nil {
		data = normalizer.NormalizeBytes(data)
	}

	AssertGolden(t, goldenPath, data)
}

// AssertGoldenMCPInteraction renders an MCP interaction detail response in a
// human-readable format: fields as pretty-printed JSON with tool_arguments and
// tool_result expanded for readability.
func AssertGoldenMCPInteraction(t *testing.T, goldenPath string, detail map[string]interface{}, normalizer *Normalizer) {
	t.Helper()

	var buf strings.Builder

	// ── Metadata section (JSON, excluding large nested objects) ──
	meta := make(map[string]interface{})
	for k, v := range detail {
		if k != "tool_arguments" && k != "tool_result" && k != "available_tools" {
			meta[k] = v
		}
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	require.NoError(t, err)
	buf.Write(metaJSON)
	buf.WriteString("\n")

	// ── Tool arguments (pretty-printed) ──
	if args, ok := detail["tool_arguments"]; ok && args != nil {
		buf.WriteString("\n=== TOOL_ARGUMENTS ===\n")
		argsJSON, err := json.MarshalIndent(args, "", "  ")
		require.NoError(t, err)
		buf.Write(argsJSON)
		buf.WriteString("\n")
	}

	// ── Tool result (pretty-printed) ──
	if result, ok := detail["tool_result"]; ok && result != nil {
		buf.WriteString("\n=== TOOL_RESULT ===\n")
		resultJSON, err := json.MarshalIndent(result, "", "  ")
		require.NoError(t, err)
		buf.Write(resultJSON)
		buf.WriteString("\n")
	}

	// ── Available tools (pretty-printed) ──
	if tools, ok := detail["available_tools"]; ok && tools != nil {
		buf.WriteString("\n=== AVAILABLE_TOOLS ===\n")
		toolsJSON, err := json.MarshalIndent(tools, "", "  ")
		require.NoError(t, err)
		buf.Write(toolsJSON)
		buf.WriteString("\n")
	}

	data := []byte(buf.String())
	if normalizer != nil {
		data = normalizer.NormalizeBytes(data)
	}

	AssertGolden(t, goldenPath, data)
}

// AssertSessionTraceGoldens performs the full golden file assertion sequence
// for a session: normalizer registration, timeline projection, trace list,
// and per-interaction golden files. Extracts the pattern shared across
// multiple e2e tests to avoid duplication.
func AssertSessionTraceGoldens(t *testing.T, app *TestApp, sessionID, goldenScenario string) {
	t.Helper()

	traceList := app.GetTraceList(t, sessionID)
	traceStages, ok := traceList["stages"].([]interface{})
	require.True(t, ok, "stages should be an array")
	require.NotEmpty(t, traceStages)

	normalizer := NewNormalizer(sessionID)
	for _, rawStage := range traceStages {
		stg, _ := rawStage.(map[string]interface{})
		stageID, _ := stg["stage_id"].(string)
		normalizer.RegisterStageID(stageID)

		executions, _ := stg["executions"].([]interface{})
		for _, rawExec := range executions {
			exec, _ := rawExec.(map[string]interface{})
			execID, _ := exec["execution_id"].(string)
			normalizer.RegisterExecutionID(execID)

			for _, rawLI := range exec["llm_interactions"].([]interface{}) {
				li, _ := rawLI.(map[string]interface{})
				if id, ok := li["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
			for _, rawMI := range exec["mcp_interactions"].([]interface{}) {
				mi, _ := rawMI.(map[string]interface{})
				if id, ok := mi["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
		}
	}

	traceSessionInteractions, _ := traceList["session_interactions"].([]interface{})
	for _, rawLI := range traceSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
	}

	// Timeline golden.
	execs := app.QueryExecutions(t, sessionID)
	agentIndex := BuildAgentNameIndex(execs)

	timeline := app.QueryTimeline(t, sessionID)
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AnnotateTimelineWithAgent(projectedTimeline, timeline, agentIndex)
	SortTimelineProjection(projectedTimeline)
	AssertGoldenJSON(t, GoldenPath(goldenScenario, "timeline.golden"), projectedTimeline, normalizer)

	// Trace list golden.
	AssertGoldenJSON(t, GoldenPath(goldenScenario, "trace_list.golden"), traceList, normalizer)

	// Per-interaction golden files.
	type interactionEntry struct {
		Kind, ID, AgentName, CreatedAt, Label, ServerName string
	}

	var allInteractions []interactionEntry
	for _, rawStage := range traceStages {
		stg, _ := rawStage.(map[string]interface{})
		for _, rawExec := range stg["executions"].([]interface{}) {
			exec, _ := rawExec.(map[string]interface{})
			agentName, _ := exec["agent_name"].(string)

			var execInteractions []interactionEntry
			for _, rawLI := range exec["llm_interactions"].([]interface{}) {
				li, _ := rawLI.(map[string]interface{})
				execInteractions = append(execInteractions, interactionEntry{
					Kind: "llm", ID: li["id"].(string), AgentName: agentName,
					CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
				})
			}
			for _, rawMI := range exec["mcp_interactions"].([]interface{}) {
				mi, _ := rawMI.(map[string]interface{})
				label := mi["interaction_type"].(string)
				if tn, ok := mi["tool_name"].(string); ok && tn != "" {
					label = tn
				}
				sn, _ := mi["server_name"].(string)
				execInteractions = append(execInteractions, interactionEntry{
					Kind: "mcp", ID: mi["id"].(string), AgentName: agentName,
					CreatedAt: mi["created_at"].(string), Label: label, ServerName: sn,
				})
			}
			sort.SliceStable(execInteractions, func(i, j int) bool {
				a, b := execInteractions[i], execInteractions[j]
				if a.CreatedAt != b.CreatedAt {
					return a.CreatedAt < b.CreatedAt
				}
				return a.ServerName < b.ServerName
			})
			allInteractions = append(allInteractions, execInteractions...)
		}
	}

	for _, rawLI := range traceSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		allInteractions = append(allInteractions, interactionEntry{
			Kind: "llm", ID: li["id"].(string), AgentName: "Session",
			CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
		})
	}

	iterationCounters := make(map[string]int)
	for idx, entry := range allInteractions {
		counterKey := entry.AgentName + "_" + entry.Label
		iterationCounters[counterKey]++
		count := iterationCounters[counterKey]

		label := strings.ReplaceAll(entry.Label, " ", "_")
		filename := fmt.Sprintf("%02d_%s_%s_%s_%d.golden", idx+1, entry.AgentName, entry.Kind, label, count)
		goldenPath := GoldenPath(goldenScenario, filepath.Join("trace_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}
}

// serializeLLMMessages renders a slice of ConversationMessage into a
// deterministic text format suitable for golden-file comparison.
func serializeLLMMessages(msgs []agent.ConversationMessage) string {
	var sb strings.Builder
	for i, msg := range msgs {
		sb.WriteString(fmt.Sprintf("=== message[%d] role=%s ===\n", i, msg.Role))
		sb.WriteString(msg.Content)
		if !strings.HasSuffix(msg.Content, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
