package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
