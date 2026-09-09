package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuiltinLabelMap(t *testing.T) {
	t.Parallel()

	m := BuiltinLabelMap()
	assert.False(t, m.Multi)
	require.Len(t, m.Labels, 3)
	assert.Equal(t, []string{"watch", "action", "noise"}, labelNames(m))
	assert.Contains(t, m.Instructions, "prefer watch over action")
	assert.Contains(t, m.Instructions, "not whether automated tools already ran (actions_executed)")
	assert.Contains(t, m.Instructions, "omitted LABELS line is not a close verdict")
	for _, spec := range m.Labels {
		assert.Contains(t, spec.Description, "Do not use when")
	}
	assert.Contains(t, m.Labels[1].Description, "Not the same as actions_executed")
	assert.Contains(t, m.Labels[2].Description, "Omitting the LABELS line is not noise")

	m.Labels[0].Label = "mutated"
	assert.Equal(t, "watch", BuiltinLabelMap().Labels[0].Label)
}

func TestInjectBuiltinLabelMap(t *testing.T) {
	t.Parallel()

	t.Run("nil catalog injects builtin", func(t *testing.T) {
		t.Parallel()
		got, overridden := injectBuiltinLabelMap(nil)
		require.False(t, overridden)
		require.Contains(t, got, LabelMapBuiltin)
		assert.Equal(t, []string{"watch", "action", "noise"}, labelNames(got[LabelMapBuiltin]))
	})

	t.Run("empty catalog injects builtin", func(t *testing.T) {
		t.Parallel()
		got, overridden := injectBuiltinLabelMap(map[string]LabelMap{})
		require.False(t, overridden)
		require.Len(t, got, 1)
		require.Contains(t, got, LabelMapBuiltin)
	})

	t.Run("custom map keeps builtin inject", func(t *testing.T) {
		t.Parallel()
		src := map[string]LabelMap{
			"ops-attention": {Labels: []LabelSpec{{Label: "page", Description: "Page the on-call."}}},
		}
		got, overridden := injectBuiltinLabelMap(src)
		require.False(t, overridden)
		require.Len(t, got, 2)
		assert.Equal(t, "page", got["ops-attention"].Labels[0].Label)
		assert.Equal(t, []string{"watch", "action", "noise"}, labelNames(got[LabelMapBuiltin]))
		_, stillSrc := src[LabelMapBuiltin]
		assert.False(t, stillSrc, "inject must not mutate the YAML map")
	})

	t.Run("YAML builtin is full replace", func(t *testing.T) {
		t.Parallel()
		src := map[string]LabelMap{
			LabelMapBuiltin: {
				Instructions: "Custom only.",
				Labels:       []LabelSpec{{Label: "monitor", Description: "Look"}},
			},
		}
		got, overridden := injectBuiltinLabelMap(src)
		require.True(t, overridden)
		require.Len(t, got, 1)
		assert.Equal(t, "Custom only.", got[LabelMapBuiltin].Instructions)
		assert.Equal(t, []string{"monitor"}, labelNames(got[LabelMapBuiltin]))
		assert.NotContains(t, labelNames(got[LabelMapBuiltin]), "watch")
	})
}

func TestResolveLabelMap(t *testing.T) {
	t.Parallel()

	custom := LabelMap{Labels: []LabelSpec{{Label: "page", Description: "Page the on-call."}}}
	catalog := map[string]LabelMap{
		LabelMapBuiltin:    BuiltinLabelMap(),
		"ops-attention": custom,
	}

	tests := []struct {
		name    string
		layers  []string
		want    string
		wantErr string
	}{
		{name: "no layers uses builtin", want: LabelMapBuiltin},
		{name: "empty layers use builtin", layers: []string{"", ""}, want: LabelMapBuiltin},
		{name: "defaults only", layers: []string{"ops-attention", ""}, want: "ops-attention"},
		{name: "chain wins over defaults", layers: []string{"ops-attention", "ops-attention"}, want: "ops-attention"},
		{name: "chain builtin wins over defaults custom", layers: []string{"ops-attention", LabelMapBuiltin}, want: LabelMapBuiltin},
		{name: "last non-empty wins", layers: []string{"", "ops-attention"}, want: "ops-attention"},
		{name: "unknown name", layers: []string{"ghost"}, wantErr: `unknown label map "ghost"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveLabelMap(catalog, tt.layers...)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.want == LabelMapBuiltin {
				assert.Equal(t, []string{"watch", "action", "noise"}, labelNames(got))
				return
			}
			assert.Equal(t, []string{"page"}, labelNames(got))
		})
	}

	t.Run("clones labels slice", func(t *testing.T) {
		t.Parallel()
		got, err := ResolveLabelMap(catalog, "ops-attention")
		require.NoError(t, err)
		got.Labels[0].Label = "mutated"
		assert.Equal(t, "page", catalog["ops-attention"].Labels[0].Label)
	})

	t.Run("nil catalog", func(t *testing.T) {
		t.Parallel()
		_, err := ResolveLabelMap(nil)
		require.EqualError(t, err, `unknown label map "builtin"`)
	})
}

func TestValidLabelToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{name: "builtin", token: "builtin", want: true},
		{name: "hyphenated catalog key", token: "ops-attention", want: true},
		{name: "underscore", token: "false_positive", want: true},
		{name: "leading capital", token: "Watch", want: true},
		{name: "single letter", token: "a", want: true},
		{name: "letter then digit", token: "page2", want: true},
		{name: "empty", token: "", want: false},
		{name: "leading digit", token: "2fa", want: false},
		{name: "comma", token: "page,watch", want: false},
		{name: "space", token: "page please", want: false},
		{name: "dot", token: "bad.name", want: false},
		{name: "leading hyphen", token: "-page", want: false},
		{name: "leading underscore", token: "_page", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ValidLabelToken(tt.token))
		})
	}
}

func TestLabelMapYAML(t *testing.T) {
	t.Parallel()

	t.Run("trims label and description", func(t *testing.T) {
		t.Parallel()
		var m LabelMap
		err := yaml.Unmarshal([]byte(`
labels:
  - label: " page "
    description: " Page the on-call. "
`), &m)
		require.NoError(t, err)
		require.Len(t, m.Labels, 1)
		assert.Equal(t, "page", m.Labels[0].Label)
		assert.Equal(t, "Page the on-call.", m.Labels[0].Description)
		assert.False(t, m.Multi)
	})

	t.Run("whitespace-only description becomes empty", func(t *testing.T) {
		t.Parallel()
		var m LabelMap
		err := yaml.Unmarshal([]byte(`
labels:
  - label: page
    description: "   "
`), &m)
		require.NoError(t, err)
		require.Len(t, m.Labels, 1)
		assert.Empty(t, m.Labels[0].Description)
	})

	t.Run("rejects unknown map field", func(t *testing.T) {
		t.Parallel()
		var m LabelMap
		err := yaml.Unmarshal([]byte(`
max_labels: 2
labels:
  - label: page
    description: Intervene
`), &m)
		require.Error(t, err)
		assert.Equal(t, `unknown field "max_labels"`, err.Error())
	})

	t.Run("rejects name instead of label", func(t *testing.T) {
		t.Parallel()
		var m LabelMap
		err := yaml.Unmarshal([]byte(`
labels:
  - name: page
    description: Intervene
`), &m)
		require.Error(t, err)
		assert.Equal(t, `unknown field "name" (did you mean "label"?)`, err.Error())
	})
}

func labelNames(m LabelMap) []string {
	names := make([]string, len(m.Labels))
	for i, spec := range m.Labels {
		names[i] = spec.Label
	}
	return names
}
