package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSubAgentRefs_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    SubAgentRefs
		wantErr string
	}{
		{
			name: "short-form strings only",
			yaml: "sub_agents: [LogAnalyzer, GeneralWorker]",
			want: SubAgentRefs{
				{Name: "LogAnalyzer"},
				{Name: "GeneralWorker"},
			},
		},
		{
			name: "long-form objects only",
			yaml: `sub_agents:
  - name: LogAnalyzer
    max_iterations: 5
    llm_provider: fast-model
  - name: GeneralWorker
    llm_backend: langchain`,
			want: SubAgentRefs{
				{Name: "LogAnalyzer", MaxIterations: intPtr(5), LLMProvider: "fast-model"},
				{Name: "GeneralWorker", LLMBackend: LLMBackendLangChain},
			},
		},
		{
			name: "mixed strings and objects",
			yaml: `sub_agents:
  - LogAnalyzer
  - name: GeneralWorker
    max_iterations: 3`,
			want: SubAgentRefs{
				{Name: "LogAnalyzer"},
				{Name: "GeneralWorker", MaxIterations: intPtr(3)},
			},
		},
		{
			name: "empty list",
			yaml: "sub_agents: []",
			want: SubAgentRefs{},
		},
		{
			name: "object with mcp_servers",
			yaml: `sub_agents:
  - name: LogAnalyzer
    mcp_servers: [grafana, prometheus]`,
			want: SubAgentRefs{
				{Name: "LogAnalyzer", MCPServers: []string{"grafana", "prometheus"}},
			},
		},
		{
			name: "name only object (no overrides)",
			yaml: `sub_agents:
  - name: GeneralWorker`,
			want: SubAgentRefs{
				{Name: "GeneralWorker"},
			},
		},
		// ── Negative cases ──────────────────────────────────────────
		{
			name:    "non-sequence (scalar)",
			yaml:    "sub_agents: LogAnalyzer",
			wantErr: "sub_agents must be a sequence",
		},
		{
			name:    "non-sequence (map)",
			yaml:    "sub_agents:\n  name: LogAnalyzer",
			wantErr: "sub_agents must be a sequence",
		},
		{
			name:    "integer scalar in sequence",
			yaml:    "sub_agents: [42]",
			wantErr: "sub_agents[0]: expected string, got !!int",
		},
		{
			name:    "boolean scalar in sequence",
			yaml:    "sub_agents: [true]",
			wantErr: "sub_agents[0]: expected string, got !!bool",
		},
		{
			name: "unknown key in mapping",
			yaml: `sub_agents:
  - name: LogAnalyzer
    max_iteration: 5`,
			wantErr: `sub_agents[0]: unknown field "max_iteration"`,
		},
		{
			name: "unknown key in second element",
			yaml: `sub_agents:
  - name: LogAnalyzer
  - name: Worker
    foo: bar`,
			wantErr: `sub_agents[1]: unknown field "foo"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target struct {
				SubAgents SubAgentRefs `yaml:"sub_agents"`
			}
			err := yaml.Unmarshal([]byte(tt.yaml), &target)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, target.SubAgents)
		})
	}
}

func TestSubAgentRefs_Names(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		var refs SubAgentRefs
		assert.Nil(t, refs.Names())
	})

	t.Run("extracts names from refs", func(t *testing.T) {
		refs := SubAgentRefs{
			{Name: "LogAnalyzer", LLMProvider: "fast"},
			{Name: "GeneralWorker"},
		}
		assert.Equal(t, []string{"LogAnalyzer", "GeneralWorker"}, refs.Names())
	})

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		refs := SubAgentRefs{}
		assert.Equal(t, []string{}, refs.Names())
	})
}
