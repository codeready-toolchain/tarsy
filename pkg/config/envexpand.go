package config

import (
	"bytes"
	"os"
	"strings"
	"text/template"
)

// ExpandEnv expands environment variables in YAML content using Go templates.
// Uses {{.VAR_NAME}} syntax to avoid collision with $ in regex patterns.
//
// This prevents conflicts with literal $ characters commonly found in:
//   - Regex patterns: ^secret.*$, price\$[0-9]+
//   - Passwords: p@ss$word
//   - Shell snippets: $PATH, ${ARRAY[0]}
//
// Examples:
//   - {{.GOOGLE_API_KEY}} → value of GOOGLE_API_KEY environment variable
//   - {{.DB_HOST}}:{{.DB_PORT}} → hostname:port with both variables expanded
//   - pattern: "user_${USER_ID}_.*" → preserved literally ($ not touched)
//
// Missing variables expand to empty string (unless template is malformed).
// Validation should catch required fields that are empty.
//
// DESIGN CHOICE: Malformed templates (parse/execution errors) pass through unchanged.
// Rationale:
//   - Preserves backward compatibility with configs that coincidentally contain {{
//   - Allows YAML parser to potentially handle the syntax (or fail with clearer error)
//   - Template syntax is opt-in: configs without {{.VAR}} work without modification
//   - Trade-off: Typos like "{{.API_KEY" won't error early, but YAML validation
//     will catch missing/invalid values when field is required
func ExpandEnv(data []byte) []byte {
	tmpl, err := template.New("config").Option("missingkey=zero").Parse(string(data))
	if err != nil {
		// Parse error: return original data unchanged
		// This allows YAML without template syntax (or with malformed templates) to pass through
		return data
	}

	// Build environment map for template
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		// Split only on first = to handle values with = in them
		if idx := strings.IndexByte(env, '='); idx > 0 {
			key := env[:idx]
			value := env[idx+1:]
			envMap[key] = value
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, envMap); err != nil {
		// Execution error: return original data unchanged
		// Rare case (parse succeeded but exec failed), but maintains consistent pass-through behavior
		return data
	}

	return buf.Bytes()
}
