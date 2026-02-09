package prompt

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatToolDescriptions_Empty(t *testing.T) {
	result := FormatToolDescriptions(nil)
	assert.Equal(t, "No tools available.", result)

	result = FormatToolDescriptions([]agent.ToolDefinition{})
	assert.Equal(t, "No tools available.", result)
}

func TestFormatToolDescriptions_SingleTool_NoSchema(t *testing.T) {
	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "List pods in a namespace"},
	}
	result := FormatToolDescriptions(tools)
	assert.Contains(t, result, "1. **k8s.get_pods**: List pods in a namespace")
	assert.Contains(t, result, "**Parameters**: None")
}

func TestFormatToolDescriptions_WithSchema(t *testing.T) {
	tools := []agent.ToolDefinition{
		{
			Name:        "k8s.get_pods",
			Description: "List pods",
			ParametersSchema: `{
				"type": "object",
				"properties": {
					"namespace": {
						"type": "string",
						"description": "The namespace to list pods from"
					},
					"labels": {
						"type": "string",
						"description": "Label selector"
					}
				},
				"required": ["namespace"]
			}`,
		},
	}
	result := FormatToolDescriptions(tools)
	assert.Contains(t, result, "**k8s.get_pods**: List pods")
	assert.Contains(t, result, "**Parameters**:")
	assert.Contains(t, result, "labels (optional, string): Label selector")
	assert.Contains(t, result, "namespace (required, string): The namespace to list pods from")
}

func TestFormatToolDescriptions_MultipleTools(t *testing.T) {
	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "List pods"},
		{Name: "k8s.get_logs", Description: "Get logs"},
	}
	result := FormatToolDescriptions(tools)
	assert.Contains(t, result, "1. **k8s.get_pods**")
	assert.Contains(t, result, "2. **k8s.get_logs**")
}

func TestExtractParameters_Nil(t *testing.T) {
	params := extractParameters(nil)
	assert.Nil(t, params)
}

func TestExtractParameters_NoProperties(t *testing.T) {
	schema := map[string]any{"type": "object"}
	params := extractParameters(schema)
	assert.Nil(t, params)
}

func TestExtractParameters_RequiredAndOptional(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Resource name",
			},
			"ns": map[string]any{
				"type":        "string",
				"description": "Namespace",
			},
		},
		"required": []any{"name"},
	}
	params := extractParameters(schema)
	require.Len(t, params, 2)
	// Alphabetical: name, ns
	assert.Contains(t, params[0], "name (required, string): Resource name")
	assert.Contains(t, params[1], "ns (optional, string): Namespace")
}

func TestExtractParameters_WithDefault(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max results",
				"default":     float64(100), // JSON numbers are float64
			},
		},
	}
	params := extractParameters(schema)
	require.Len(t, params, 1)
	assert.Contains(t, params[0], "[default: 100]")
}

func TestExtractParameters_WithEnum(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"format": map[string]any{
				"type":        "string",
				"description": "Output format",
				"enum":        []any{"json", "yaml", "table"},
			},
		},
	}
	params := extractParameters(schema)
	require.Len(t, params, 1)
	assert.Contains(t, params[0], `choices: ["json", "yaml", "table"]`)
}

func TestExtractParameters_DeterministicOrder(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"z_param": map[string]any{"type": "string"},
			"a_param": map[string]any{"type": "string"},
			"m_param": map[string]any{"type": "string"},
		},
	}
	params := extractParameters(schema)
	require.Len(t, params, 3)
	assert.Contains(t, params[0], "a_param")
	assert.Contains(t, params[1], "m_param")
	assert.Contains(t, params[2], "z_param")
}

func TestFormatToolDescriptions_InvalidJSON(t *testing.T) {
	tools := []agent.ToolDefinition{
		{Name: "tool.test", Description: "Test", ParametersSchema: "not json"},
	}
	result := FormatToolDescriptions(tools)
	assert.Contains(t, result, "**Parameters**: None")
}
