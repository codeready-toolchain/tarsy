package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestJobPairingYAML(t *testing.T) {
	t.Parallel()

	t.Run("nested compose and executive_summary", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte(`
compose:
  llm_provider: claude-sonnet
  llm_backend: langchain
  fallback_list: mid
executive_summary:
  llm_provider: google-default
  llm_backend: google-native
  fallback_list: google-native
`), &d)
		require.NoError(t, err)
		require.NotNil(t, d.Compose)
		assert.Equal(t, "claude-sonnet", d.Compose.LLMProvider)
		assert.Equal(t, LLMBackendLangChain, d.Compose.LLMBackend)
		assert.Equal(t, "mid", d.Compose.FallbackList)
		assert.False(t, d.Compose.fromDeprecated)
		require.NotNil(t, d.ExecutiveSummary)
		assert.Equal(t, "google-default", d.ExecutiveSummary.LLMProvider)
		assert.Equal(t, LLMBackendNativeGemini, d.ExecutiveSummary.LLMBackend)
		assert.Equal(t, "google-native", d.ExecutiveSummary.FallbackList)
		assert.False(t, d.ExecutiveSummary.fromDeprecated)
	})

	t.Run("deprecated compose_* and executive_summary_* keys", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte(`
compose_provider: claude-sonnet
compose_backend: langchain
compose_fallback_list: mid
executive_summary_provider: google-default
executive_summary_backend: google-native
executive_summary_fallback_list: google-native
`), &d)
		require.NoError(t, err)
		require.NotNil(t, d.Compose)
		assert.Equal(t, "claude-sonnet", d.Compose.LLMProvider)
		assert.Equal(t, LLMBackendLangChain, d.Compose.LLMBackend)
		assert.Equal(t, "mid", d.Compose.FallbackList)
		assert.True(t, d.Compose.fromDeprecated)
		require.NotNil(t, d.ExecutiveSummary)
		assert.Equal(t, "google-default", d.ExecutiveSummary.LLMProvider)
		assert.Equal(t, LLMBackendNativeGemini, d.ExecutiveSummary.LLMBackend)
		assert.Equal(t, "google-native", d.ExecutiveSummary.FallbackList)
		assert.True(t, d.ExecutiveSummary.fromDeprecated)
	})

	t.Run("mixing compose block and compose_provider fails", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte(`
compose:
  llm_provider: claude-sonnet
compose_provider: google-default
`), &d)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot set both compose and compose_provider / compose_backend / compose_fallback_list")
	})

	t.Run("empty compose mapping still mixes with compose_provider", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte(`
compose: {}
compose_provider: google-default
`), &d)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot set both compose and compose_provider / compose_backend / compose_fallback_list")
	})

	t.Run("mixing executive_summary block and executive_summary_fallback_list fails", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte(`
executive_summary:
  fallback_list: mid
executive_summary_fallback_list: premium
`), &d)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot set both executive_summary and executive_summary_provider / executive_summary_backend / executive_summary_fallback_list")
	})

	t.Run("chain nested and deprecated keys", func(t *testing.T) {
		t.Parallel()
		var nested ChainConfig
		err := yaml.Unmarshal([]byte(`
alert_types: [test]
compose:
  llm_provider: claude-sonnet
  fallback_list: mid
stages:
  - name: s1
    agents:
      - name: Agent
`), &nested)
		require.NoError(t, err)
		require.NotNil(t, nested.Compose)
		assert.Equal(t, "claude-sonnet", nested.Compose.LLMProvider)
		assert.Equal(t, "mid", nested.Compose.FallbackList)
		assert.False(t, nested.Compose.fromDeprecated)

		var deprecated ChainConfig
		err = yaml.Unmarshal([]byte(`
alert_types: [test]
compose_provider: claude-sonnet
compose_fallback_list: mid
executive_summary_provider: google-default
stages:
  - name: s1
    agents:
      - name: Agent
`), &deprecated)
		require.NoError(t, err)
		require.NotNil(t, deprecated.Compose)
		assert.Equal(t, "claude-sonnet", deprecated.Compose.LLMProvider)
		assert.Equal(t, "mid", deprecated.Compose.FallbackList)
		assert.True(t, deprecated.Compose.fromDeprecated)
		require.NotNil(t, deprecated.ExecutiveSummary)
		assert.Equal(t, "google-default", deprecated.ExecutiveSummary.LLMProvider)
		assert.True(t, deprecated.ExecutiveSummary.fromDeprecated)
	})

	t.Run("mixing chain compose block and compose_provider fails", func(t *testing.T) {
		t.Parallel()
		var c ChainConfig
		err := yaml.Unmarshal([]byte(`
alert_types: [test]
compose:
  llm_provider: claude-sonnet
compose_provider: google-default
stages:
  - name: s1
    agents:
      - name: Agent
`), &c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot set both compose and compose_provider / compose_backend / compose_fallback_list")
	})

	t.Run("deprecated compose_fallback_list only", func(t *testing.T) {
		t.Parallel()
		var d Defaults
		err := yaml.Unmarshal([]byte("compose_fallback_list: mid\n"), &d)
		require.NoError(t, err)
		require.NotNil(t, d.Compose)
		assert.Equal(t, "mid", d.Compose.FallbackList)
		assert.Empty(t, d.Compose.LLMProvider)
		assert.True(t, d.Compose.fromDeprecated)
		assert.Nil(t, d.ExecutiveSummary)
	})

	t.Run("unknown pairing keys hint catalog names", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			yamlSrc string
			wantErr string
		}{
			{
				name:    "provider hints llm_provider",
				yamlSrc: "provider: google-default\n",
				wantErr: `unknown field "provider" (did you mean "llm_provider"?)`,
			},
			{
				name:    "backend hints llm_backend",
				yamlSrc: "backend: google-native\n",
				wantErr: `unknown field "backend" (did you mean "llm_backend"?)`,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var p JobPairing
				err := yaml.Unmarshal([]byte(tt.yamlSrc), &p)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			})
		}
	})
}

func TestMigrateJobPairing(t *testing.T) {
	t.Parallel()

	nested := &JobPairing{LLMProvider: "nested"}

	t.Run("returns nested when deprecated keys are empty", func(t *testing.T) {
		t.Parallel()
		got, err := migrateJobPairing(nested, "", "", "", "compose")
		require.NoError(t, err)
		assert.Equal(t, nested, got)
	})

	t.Run("builds pairing from deprecated keys", func(t *testing.T) {
		t.Parallel()
		got, err := migrateJobPairing(nil, "claude-sonnet", LLMBackendLangChain, "mid", "compose")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "claude-sonnet", got.LLMProvider)
		assert.Equal(t, LLMBackendLangChain, got.LLMBackend)
		assert.Equal(t, "mid", got.FallbackList)
		assert.True(t, got.fromDeprecated)
	})

	t.Run("neither nested nor deprecated returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := migrateJobPairing(nil, "", "", "", "compose")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("mixing nested and deprecated is an exact error", func(t *testing.T) {
		t.Parallel()
		got, err := migrateJobPairing(nested, "p", "", "", "compose")
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Equal(t, "cannot set both compose and compose_provider / compose_backend / compose_fallback_list", err.Error())
	})

	t.Run("empty nested pairing still mixes", func(t *testing.T) {
		t.Parallel()
		got, err := migrateJobPairing(&JobPairing{}, "", "", "mid", "executive_summary")
		assert.Nil(t, got)
		require.Error(t, err)
		assert.Equal(t, "cannot set both executive_summary and executive_summary_provider / executive_summary_backend / executive_summary_fallback_list", err.Error())
	})
}

func TestJobPairingNilMethods(t *testing.T) {
	t.Parallel()
	var p *JobPairing
	assert.Equal(t, "", p.Provider())
	assert.Equal(t, LLMBackend(""), p.Backend())
	assert.Equal(t, "", p.List())
	assert.False(t, p.deprecated())
}
