package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialize(t *testing.T) {
	// Create temporary config directory with valid config files
	configDir := setupTestConfigDir(t)

	// Set required environment variables for all built-in providers
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XAI_API_KEY", "test-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	t.Setenv("KUBECONFIG", "/test/kubeconfig")

	ctx := context.Background()
	cfg, err := Initialize(ctx, configDir)

	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify registries are populated
	assert.NotNil(t, cfg.AgentRegistry)
	assert.NotNil(t, cfg.ChainRegistry)
	assert.NotNil(t, cfg.MCPServerRegistry)
	assert.NotNil(t, cfg.LLMProviderRegistry)
	assert.NotNil(t, cfg.Defaults)

	// Verify built-in configs are loaded
	assert.True(t, cfg.AgentRegistry.Has("KubernetesAgent"))
	assert.True(t, cfg.ChainRegistry.Has("kubernetes-agent-chain"))
	assert.True(t, cfg.MCPServerRegistry.Has("kubernetes-server"))
	assert.True(t, cfg.LLMProviderRegistry.Has("google-default"))

	// Verify stats
	stats := cfg.Stats()
	assert.Greater(t, stats.Agents, 0)
	assert.Greater(t, stats.Chains, 0)
	assert.Greater(t, stats.MCPServers, 0)
	assert.Greater(t, stats.LLMProviders, 0)
}

func TestInitializeConfigNotFound(t *testing.T) {
	ctx := context.Background()
	_, err := Initialize(ctx, "/nonexistent/directory")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load configuration")
}

func TestInitializeInvalidYAML(t *testing.T) {
	configDir := t.TempDir()

	// Write invalid YAML
	invalidYAML := `{{{`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(invalidYAML), 0644)
	require.NoError(t, err)

	// Create empty llm-providers.yaml
	err = os.WriteFile(filepath.Join(configDir, "llm-providers.yaml"), []byte("llm_providers: {}"), 0644)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = Initialize(ctx, configDir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load configuration")
}

func TestInitializeValidationFailure(t *testing.T) {
	configDir := t.TempDir()

	// Write YAML with invalid references
	invalidConfig := `
agent_chains:
  test-chain:
    alert_types: ["test"]
    stages:
      - name: "stage1"
        agents:
          - name: "NonexistentAgent"  # Invalid agent reference
`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(invalidConfig), 0644)
	require.NoError(t, err)

	// Create empty llm-providers.yaml
	err = os.WriteFile(filepath.Join(configDir, "llm-providers.yaml"), []byte("llm_providers: {}"), 0644)
	require.NoError(t, err)

	// Set all required environment variables for built-in providers
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XAI_API_KEY", "test-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	ctx := context.Background()
	_, err = Initialize(ctx, configDir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
	assert.Contains(t, err.Error(), "NonexistentAgent")
}

func TestLoadTarsyYAML(t *testing.T) {
	configDir := t.TempDir()

	config := `
defaults:
  llm_provider: "test-provider"
  max_iterations: 25

agents:
  test-agent:
    mcp_servers:
      - "test-server"
    custom_instructions: "Test instructions"

mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "test-command"

agent_chains:
  test-chain:
    alert_types: ["test"]
    stages:
      - name: "stage1"
        agents:
          - name: "test-agent"
`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(config), 0644)
	require.NoError(t, err)

	loader := &configLoader{configDir: configDir}
	tarsyConfig, err := loader.loadTarsyYAML()

	require.NoError(t, err)
	assert.NotNil(t, tarsyConfig.Defaults)
	assert.Equal(t, "test-provider", tarsyConfig.Defaults.LLMProvider)
	assert.Equal(t, 25, *tarsyConfig.Defaults.MaxIterations)
	assert.Len(t, tarsyConfig.Agents, 1)
	assert.Len(t, tarsyConfig.MCPServers, 1)
	assert.Len(t, tarsyConfig.AgentChains, 1)
}

func TestLoadLLMProvidersYAML(t *testing.T) {
	configDir := t.TempDir()

	config := `
llm_providers:
  test-provider:
    type: google
    model: test-model
    api_key_env: TEST_API_KEY
    max_tool_result_tokens: 100000
`
	err := os.WriteFile(filepath.Join(configDir, "llm-providers.yaml"), []byte(config), 0644)
	require.NoError(t, err)

	loader := &configLoader{configDir: configDir}
	providers, err := loader.loadLLMProvidersYAML()

	require.NoError(t, err)
	assert.Len(t, providers, 1)
	provider := providers["test-provider"]
	assert.Equal(t, LLMProviderTypeGoogle, provider.Type)
	assert.Equal(t, "test-model", provider.Model)
	assert.Equal(t, "TEST_API_KEY", provider.APIKeyEnv)
}

func TestEnvironmentVariableInterpolationInConfig(t *testing.T) {
	configDir := t.TempDir()

	config := `
mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "{{.TEST_COMMAND}}"
      args:
        - "{{.TEST_ARG1}}"
        - "{{.TEST_ARG2}}"
`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(config), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(configDir, "llm-providers.yaml"), []byte("llm_providers: {}"), 0644)
	require.NoError(t, err)

	// Set environment variables
	t.Setenv("TEST_COMMAND", "test-cmd")
	t.Setenv("TEST_ARG1", "arg1-value")
	t.Setenv("TEST_ARG2", "arg2-value")
	// Set all required environment variables for built-in providers
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XAI_API_KEY", "test-key")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	ctx := context.Background()
	cfg, err := Initialize(ctx, configDir)

	require.NoError(t, err)
	server, err := cfg.MCPServerRegistry.Get("test-server")
	require.NoError(t, err)
	assert.Equal(t, "test-cmd", server.Transport.Command)
	assert.Equal(t, []string{"arg1-value", "arg2-value"}, server.Transport.Args)
}

// Helper function to set up test config directory
func setupTestConfigDir(t *testing.T) string {
	dir := t.TempDir()

	// Create minimal valid tarsy.yaml
	tarsyYAML := `
defaults:
  llm_provider: "google-default"
  max_iterations: 20

agents: {}
mcp_servers: {}
agent_chains: {}
`
	err := os.WriteFile(filepath.Join(dir, "tarsy.yaml"), []byte(tarsyYAML), 0644)
	require.NoError(t, err)

	// Create minimal valid llm-providers.yaml
	llmYAML := `
llm_providers: {}
`
	err = os.WriteFile(filepath.Join(dir, "llm-providers.yaml"), []byte(llmYAML), 0644)
	require.NoError(t, err)

	return dir
}
