package config

import (
	"bytes"
	"os"
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
func ExpandEnv(data []byte) []byte {
	tmpl, err := template.New("config").Option("missingkey=zero").Parse(string(data))
	if err != nil {
		// If template parsing fails, return original data
		// This allows YAML without any template syntax to pass through
		return data
	}

	// Build environment map for template
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		// Split only on first = to handle values with = in them
		if idx := bytes.IndexByte([]byte(env), '='); idx > 0 {
			key := env[:idx]
			value := env[idx+1:]
			envMap[key] = value
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, envMap); err != nil {
		// If execution fails, return original data
		return data
	}

	return buf.Bytes()
}
