package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestExpandEnv(t *testing.T) {
	tests := []struct {
		name  string
		input string
		env   map[string]string
		want  string
	}{
		{
			name:  "simple substitution with {{.VAR}}",
			input: "api_key: {{.API_KEY}}",
			env:   map[string]string{"API_KEY": "secret123"},
			want:  "api_key: secret123",
		},
		{
			name:  "literal ${VAR} is NOT expanded (no collision)",
			input: "pattern: ${USER_ID}",
			env:   map[string]string{"USER_ID": "123"},
			want:  "pattern: ${USER_ID}",
		},
		{
			name:  "literal $VAR is NOT expanded (no collision)",
			input: "regex: ^secret.*$",
			env:   map[string]string{},
			want:  "regex: ^secret.*$",
		},
		{
			name:  "multiple substitutions in one line",
			input: "url: {{.PROTOCOL}}://{{.HOST}}:{{.PORT}}",
			env: map[string]string{
				"PROTOCOL": "https",
				"HOST":     "example.com",
				"PORT":     "443",
			},
			want: "url: https://example.com:443",
		},
		{
			name:  "missing variable expands to empty",
			input: "endpoint: {{.MISSING_VAR}}",
			env:   map[string]string{},
			want:  "endpoint: ",
		},
		{
			name:  "mixed present and missing variables",
			input: "url: {{.PROTOCOL}}://{{.MISSING}}:{{.PORT}}",
			env: map[string]string{
				"PROTOCOL": "https",
				"PORT":     "443",
			},
			want: "url: https://:443",
		},
		{
			name:  "no substitution when no variables",
			input: "static: value",
			env:   map[string]string{"UNUSED": "value"},
			want:  "static: value",
		},
		{
			name:  "variables in YAML array",
			input: "args:\n  - {{.ARG1}}\n  - {{.ARG2}}",
			env: map[string]string{
				"ARG1": "value1",
				"ARG2": "value2",
			},
			want: "args:\n  - value1\n  - value2",
		},
		{
			name:  "variables in nested YAML structure",
			input: "config:\n  host: {{.HOST}}\n  port: {{.PORT}}",
			env: map[string]string{
				"HOST": "localhost",
				"PORT": "5432",
			},
			want: "config:\n  host: localhost\n  port: 5432",
		},
		{
			name:  "special characters in expanded value",
			input: "password: {{.PASSWORD}}",
			env:   map[string]string{"PASSWORD": "p@ssw0rd!#$%"},
			want:  "password: p@ssw0rd!#$%",
		},
		{
			name:  "literal dollar in password is preserved",
			input: "password: p@ss$word",
			env:   map[string]string{},
			want:  "password: p@ss$word",
		},
		{
			name:  "regex pattern with $ preserved",
			input: `pattern: "^\\$[0-9]+$"`,
			env:   map[string]string{},
			want:  `pattern: "^\\$[0-9]+$"`,
		},
		{
			name:  "environment variable with underscores",
			input: "key: {{.MY_LONG_VAR_NAME}}",
			env:   map[string]string{"MY_LONG_VAR_NAME": "value"},
			want:  "key: value",
		},
		{
			name:  "adjacent variables without separator",
			input: "{{.VAR1}}{{.VAR2}}",
			env: map[string]string{
				"VAR1": "hello",
				"VAR2": "world",
			},
			want: "helloworld",
		},
		{
			name:  "variable in quoted string",
			input: `message: "Hello {{.NAME}}"`,
			env:   map[string]string{"NAME": "World"},
			want:  `message: "Hello World"`,
		},
		{
			name:  "empty string variable",
			input: "value: {{.EMPTY}}",
			env:   map[string]string{"EMPTY": ""},
			want:  "value: ",
		},
		{
			name:  "numeric value in environment variable",
			input: "port: {{.PORT_NUMBER}}",
			env:   map[string]string{"PORT_NUMBER": "8080"},
			want:  "port: 8080",
		},
		{
			name: "complex YAML with multiple variables",
			input: `
database:
  host: {{.DB_HOST}}
  port: {{.DB_PORT}}
  user: {{.DB_USER}}
  password: {{.DB_PASSWORD}}
`,
			env: map[string]string{
				"DB_HOST":     "localhost",
				"DB_PORT":     "5432",
				"DB_USER":     "tarsy",
				"DB_PASSWORD": "secret",
			},
			want: `
database:
  host: localhost
  port: 5432
  user: tarsy
  password: secret
`,
		},
		{
			name:  "masking pattern with ${} syntax preserved",
			input: `custom_patterns:\n  - pattern: "user_\${USER_ID}_.*"`,
			env:   map[string]string{"USER_ID": "123"},
			want:  `custom_patterns:\n  - pattern: "user_\${USER_ID}_.*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for k, v := range tt.env {
				t.Setenv(k, v) // Automatic cleanup after test
			}

			result := ExpandEnv([]byte(tt.input))
			assert.Equal(t, tt.want, string(result))
		})
	}
}

func TestExpandEnvPreservesOriginalWhenNoVariables(t *testing.T) {
	input := `
# This is a comment
key: value
nested:
  field: "string value"
  number: 123
  boolean: true
array:
  - item1
  - item2
`

	result := ExpandEnv([]byte(input))
	assert.Equal(t, input, string(result), "Content without variables should be unchanged")
}

func TestExpandEnvWithEmptyInput(t *testing.T) {
	result := ExpandEnv([]byte(""))
	assert.Equal(t, "", string(result), "Empty input should return empty output")
}

func TestExpandEnvPreservesLiteralBackslashN(t *testing.T) {
	// Template expansion preserves literal \n sequences (backslash-n, not newline)
	// Using raw string to ensure we're testing actual literal \n preservation
	input := `path: {{.TEST_PATH}}\nother: value`
	t.Setenv("TEST_PATH", "/usr/bin")

	result := ExpandEnv([]byte(input))
	// The literal \n should be preserved in the output (not converted to newline)
	assert.Contains(t, string(result), `/usr/bin\nother: value`)
}

func TestExpandEnvThreadSafety(t *testing.T) {
	// Template expansion is thread-safe (each call creates new template + reads env)
	// This test ensures our implementation is also thread-safe

	input := []byte("key: {{.TEST_VAR}}")
	t.Setenv("TEST_VAR", "value")

	// Run multiple goroutines concurrently
	const goroutines = 100
	results := make([]string, goroutines)
	done := make(chan bool)

	for i := 0; i < goroutines; i++ {
		go func(index int) {
			results[index] = string(ExpandEnv(input))
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < goroutines; i++ {
		<-done
	}

	// All results should be identical
	expected := "key: value"
	for i, result := range results {
		assert.Equal(t, expected, result, "Result %d should match", i)
	}
}

// TestExpandEnvMalformedTemplates verifies that malformed template syntax
// is passed through unchanged rather than causing errors. This allows the
// YAML parser to handle the content or fail with a clearer error message.
func TestExpandEnvMalformedTemplates(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		description string
	}{
		{
			name:        "unclosed template - missing closing braces",
			input:       "api_key: {{.API_KEY",
			description: "Template starts but never closes",
		},
		{
			name:        "incomplete template - only opening braces",
			input:       "api_key: {{",
			description: "Only opening braces without variable name",
		},
		{
			name:        "single closing brace after variable",
			input:       "api_key: {{.API_KEY}",
			description: "Missing one closing brace",
		},
		{
			name:        "reversed template syntax",
			input:       "api_key: }}.API_KEY{{",
			description: "Template syntax in reverse order",
		},
		{
			name:        "malformed variable name - missing dot",
			input:       "api_key: {{API_KEY}}",
			description: "Variable without leading dot (not valid template syntax)",
		},
		{
			name:        "nested template braces",
			input:       "api_key: {{{{.API_KEY}}}}",
			description: "Extra nested braces",
		},
		{
			name:        "triple opening braces",
			input:       "api_key: {{{.API_KEY}}}",
			description: "Too many opening braces",
		},
		{
			name:        "space in variable name",
			input:       "api_key: {{.API KEY}}",
			description: "Spaces not valid in variable names",
		},
		{
			name:        "special characters in template",
			input:       "api_key: {{.API-KEY!}}",
			description: "Special chars that may not be valid in templates",
		},
		{
			name:        "unclosed with valid YAML around it",
			input:       "host: localhost\napi_key: {{.API_KEY\nport: 8080",
			description: "Unclosed template in middle of valid YAML",
		},
		{
			name:        "multiple malformed templates",
			input:       "key1: {{.VAR1\nkey2: {{.VAR2}",
			description: "Multiple unclosed templates",
		},
		{
			name:        "template with undefined function",
			input:       `api_key: {{.API_KEY | upper}}`,
			description: "Pipeline/function calls not configured in our template",
		},
		{
			name:        "template with invalid field access",
			input:       "api_key: {{.API_KEY.NonExistent.Field}}",
			description: "Nested field access on string values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set some env vars that would be used if template was valid
			t.Setenv("API_KEY", "should-not-appear")
			t.Setenv("VAR1", "should-not-appear")
			t.Setenv("VAR2", "should-not-appear")
			t.Setenv("ITEMS", "should-not-appear")

			result := ExpandEnv([]byte(tt.input))

			// Assert that the input is returned unchanged
			assert.Equal(t, tt.input, string(result),
				"Malformed template should be passed through unchanged: %s", tt.description)

			// Verify environment values did NOT leak through
			assert.NotContains(t, string(result), "should-not-appear",
				"Malformed template should not expand environment variables")
		})
	}
}

// TestExpandEnvPassThroughToYAMLParser verifies that when ExpandEnv returns
// original data due to template errors, the YAML parser can still process it.
// This tests the integration between ExpandEnv and yaml.Unmarshal.
func TestExpandEnvPassThroughToYAMLParser(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectYAMLErr bool
		description   string
	}{
		{
			name: "valid YAML without templates passes through successfully",
			input: `
host: localhost
port: 8080
name: test-server
`,
			expectYAMLErr: false,
			description:   "No templates, valid YAML should parse successfully",
		},
		{
			name: "malformed template but valid YAML structure",
			input: `
host: localhost
api_key: "{{.API_KEY"
port: 8080
`,
			expectYAMLErr: false,
			description:   "Malformed template treated as string literal, YAML parses",
		},
		{
			name: "malformed template with invalid YAML",
			input: `
host: localhost
api_key: {{.API_KEY
  invalid: indentation
port: 8080
`,
			expectYAMLErr: true,
			description:   "Both malformed template AND invalid YAML - YAML parser catches it",
		},
		{
			name: "unclosed template in quoted string is valid YAML",
			input: `
config:
  command: "run"
  args: ["--key", "{{.API_KEY"]
`,
			expectYAMLErr: false,
			description:   "Unclosed template in array, but valid YAML syntax",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Expand environment (should pass through on malformed templates)
			expanded := ExpandEnv([]byte(tt.input))

			// Try to unmarshal as YAML
			var result map[string]any
			err := yaml.Unmarshal(expanded, &result)

			if tt.expectYAMLErr {
				assert.Error(t, err, "Expected YAML parsing to fail: %s", tt.description)
			} else {
				assert.NoError(t, err, "Expected YAML parsing to succeed: %s", tt.description)
				assert.NotNil(t, result, "Parsed YAML should not be nil")
			}
		})
	}
}

// TestExpandEnvReturnsOriginalBytesOnError verifies the exact contract:
// ExpandEnv must return the original byte slice (not a copy) when errors occur.
func TestExpandEnvReturnsOriginalBytesOnError(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "parse error - unclosed template",
			input: "key: {{.VAR",
		},
		{
			name:  "parse error - empty template",
			input: "key: {{}}",
		},
		{
			name:  "parse error - invalid syntax",
			input: "key: {{.VAR1 {{.VAR2}}}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(tt.input)
			result := ExpandEnv(input)

			// Verify the returned data is identical to input
			assert.Equal(t, tt.input, string(result), "Must return original data on error")

			// Verify it's byte-for-byte identical (not just string-equal)
			assert.Equal(t, input, result, "Must return original byte slice on error")
		})
	}
}
