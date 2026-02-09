package prompt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// FormatToolDescriptions formats tool definitions for ReAct prompt injection.
// Includes rich JSON Schema parameter details for LLM guidance.
func FormatToolDescriptions(tools []agent.ToolDefinition) string {
	if len(tools) == 0 {
		return "No tools available."
	}

	var sb strings.Builder
	for i, tool := range tools {
		// Tool name and description
		sb.WriteString(fmt.Sprintf("%d. **%s**: %s\n", i+1, tool.Name, tool.Description))

		// Parse JSON Schema from string
		var schema map[string]any
		if tool.ParametersSchema != "" {
			if err := json.Unmarshal([]byte(tool.ParametersSchema), &schema); err != nil {
				slog.Debug("failed to parse tool ParametersSchema",
					"tool", tool.Name, "error", err)
			}
		}

		// Parameters from JSON Schema
		params := extractParameters(schema)
		if len(params) > 0 {
			sb.WriteString("    **Parameters**:\n")
			for _, p := range params {
				sb.WriteString("    - ")
				sb.WriteString(p)
				sb.WriteString("\n")
			}
		} else {
			sb.WriteString("    **Parameters**: None\n")
		}

		// Blank line between tools (not after last)
		if i < len(tools)-1 {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// extractParameters extracts rich parameter info from a JSON Schema.
func extractParameters(schema map[string]any) []string {
	if schema == nil {
		return nil
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}

	required := make(map[string]bool)
	if reqList, ok := schema["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	// Sort keys for deterministic output (Q8 decision)
	keys := make([]string, 0, len(properties))
	for k := range properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var params []string
	for _, name := range keys {
		propRaw := properties[name]
		prop, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}

		reqLabel := "optional"
		if required[name] {
			reqLabel = "required"
		}
		typeSuffix := ""
		if t, ok := prop["type"].(string); ok {
			typeSuffix = ", " + t
		}
		qualifier := fmt.Sprintf(" (%s%s)", reqLabel, typeSuffix)

		var parts []string
		parts = append(parts, name)
		parts = append(parts, qualifier)

		// Description
		if desc, ok := prop["description"].(string); ok && desc != "" {
			parts = append(parts, ": "+desc)
		}

		// Additional schema hints
		var hints []string
		if def, ok := prop["default"]; ok {
			hints = append(hints, fmt.Sprintf("default: %v", def))
		}
		if enum, ok := prop["enum"].([]any); ok {
			vals := make([]string, 0, len(enum))
			for _, v := range enum {
				vals = append(vals, fmt.Sprintf("%q", v))
			}
			hints = append(hints, "choices: ["+strings.Join(vals, ", ")+"]")
		}
		if len(hints) > 0 {
			parts = append(parts, " ["+strings.Join(hints, "; ")+"]")
		}

		params = append(params, strings.Join(parts, ""))
	}

	return params
}
