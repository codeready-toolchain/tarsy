package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
