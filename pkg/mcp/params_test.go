package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseActionInput_Empty(t *testing.T) {
	result, err := ParseActionInput("")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, result)
}

func TestParseActionInput_Whitespace(t *testing.T) {
	result, err := ParseActionInput("   \n  ")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, result)
}

func TestParseActionInput_JSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name:  "json object",
			input: `{"namespace": "default", "limit": 10}`,
			expected: map[string]any{
				"namespace": "default",
				"limit":     float64(10),
			},
		},
		{
			name:  "json object with nested",
			input: `{"filter": {"app": "nginx"}, "namespace": "prod"}`,
			expected: map[string]any{
				"filter":    map[string]any{"app": "nginx"},
				"namespace": "prod",
			},
		},
		{
			name:  "json array wraps in input",
			input: `["pod1", "pod2"]`,
			expected: map[string]any{
				"input": []any{"pod1", "pod2"},
			},
		},
		{
			name:  "json string wraps in input",
			input: `"hello world"`,
			expected: map[string]any{
				"input": "hello world",
			},
		},
		{
			name:  "json number wraps in input",
			input: `42`,
			expected: map[string]any{
				"input": float64(42),
			},
		},
		{
			name:  "json boolean wraps in input",
			input: `true`,
			expected: map[string]any{
				"input": true,
			},
		},
		{
			name:  "json false wraps in input",
			input: `false`,
			expected: map[string]any{
				"input": false,
			},
		},
		{
			name:  "json null wraps in input",
			input: `null`,
			expected: map[string]any{
				"input": nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseActionInput(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseActionInput_YAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name: "yaml with nested list",
			input: `namespaces:
  - default
  - kube-system
label: app=nginx`,
			expected: map[string]any{
				"namespaces": []any{"default", "kube-system"},
				"label":      "app=nginx",
			},
		},
		{
			name: "yaml with nested map",
			input: `selector:
  app: nginx
  env: prod`,
			expected: map[string]any{
				"selector": map[string]any{
					"app": "nginx",
					"env": "prod",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseActionInput(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseActionInput_KeyValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name:  "colon separated",
			input: "namespace: default",
			expected: map[string]any{
				"namespace": "default",
			},
		},
		{
			name:  "equals separated",
			input: "namespace=default",
			expected: map[string]any{
				"namespace": "default",
			},
		},
		{
			name:  "comma separated multiple",
			input: "namespace: default, limit: 10",
			expected: map[string]any{
				"namespace": "default",
				"limit":     int64(10),
			},
		},
		{
			name:  "newline separated multiple",
			input: "namespace: default\nlimit: 10",
			expected: map[string]any{
				"namespace": "default",
				"limit":     int64(10),
			},
		},
		{
			name:  "mixed separators",
			input: "namespace: default, verbose=true\nlimit: 5",
			expected: map[string]any{
				"namespace": "default",
				"verbose":   true,
				"limit":     int64(5),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseActionInput(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseActionInput_RawString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name:  "plain text",
			input: "get all pods in the default namespace",
			expected: map[string]any{
				"input": "get all pods in the default namespace",
			},
		},
		{
			name:  "single word",
			input: "default",
			expected: map[string]any{
				"input": "default",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseActionInput(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCoerceValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected any
	}{
		{name: "true", input: "true", expected: true},
		{name: "True", input: "True", expected: true},
		{name: "TRUE", input: "TRUE", expected: true},
		{name: "false", input: "false", expected: false},
		{name: "False", input: "False", expected: false},
		{name: "null", input: "null", expected: nil},
		{name: "none", input: "none", expected: nil},
		{name: "None", input: "None", expected: nil},
		{name: "integer", input: "42", expected: int64(42)},
		{name: "negative integer", input: "-5", expected: int64(-5)},
		{name: "float", input: "3.14", expected: 3.14},
		{name: "NaN stays string", input: "NaN", expected: "NaN"},
		{name: "Inf stays string", input: "Inf", expected: "Inf"},
		{name: "-Inf stays string", input: "-Inf", expected: "-Inf"},
		{name: "+Inf stays string", input: "+Inf", expected: "+Inf"},
		{name: "string", input: "hello", expected: "hello"},
		{name: "whitespace", input: "  hello  ", expected: "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := coerceValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseActionInput_JSONPriority(t *testing.T) {
	// JSON with colon-separated values should parse as JSON, not key-value
	input := `{"key": "value"}`
	result, err := ParseActionInput(input)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"key": "value"}, result)
}

func TestParseActionInput_SimpleYAMLFallsToKeyValue(t *testing.T) {
	// Simple key: value without complex structures should be handled by
	// key-value parser, not YAML, to avoid false positives
	input := "namespace: default"
	result, err := ParseActionInput(input)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"namespace": "default"}, result)
}
