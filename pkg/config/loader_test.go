package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	assert.True(t, cfg.ChainRegistry.Has("kubernetes"))
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
	// KUBECONFIG not needed: built-in Go strings aren't template-expanded (only YAML files are)
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

func TestLoadTarsyYAML_OrchestratorFields(t *testing.T) {
	configDir := t.TempDir()

	config := `
defaults:
  llm_provider: "test-provider"
  orchestrator:
    max_concurrent_agents: 5
    agent_timeout: 5m
    max_budget: 30m

agents:
  worker-agent:
    description: "Worker"
    mcp_servers:
      - "test-server"
  orch-agent:
    type: orchestrator
    description: "Orchestrator"
    orchestrator:
      max_concurrent_agents: 3
      agent_timeout: 2m

mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "test-command"

agent_chains:
  test-chain:
    alert_types: ["test"]
    sub_agents: ["worker-agent"]
    stages:
      - name: "stage1"
        sub_agents: ["worker-agent"]
        agents:
          - name: "orch-agent"
            sub_agents: ["worker-agent"]
      - name: "stage2"
        agents:
          - name: "worker-agent"
            type: orchestrator
            sub_agents: ["worker-agent"]
`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(config), 0644)
	require.NoError(t, err)

	loader := &configLoader{configDir: configDir}
	cfg, err := loader.loadTarsyYAML()
	require.NoError(t, err)

	// Defaults orchestrator
	require.NotNil(t, cfg.Defaults.Orchestrator)
	assert.Equal(t, 5, *cfg.Defaults.Orchestrator.MaxConcurrentAgents)
	assert.Equal(t, 5*time.Minute, *cfg.Defaults.Orchestrator.AgentTimeout)
	assert.Equal(t, 30*time.Minute, *cfg.Defaults.Orchestrator.MaxBudget)

	// Agent orchestrator config
	orch := cfg.Agents["orch-agent"]
	assert.Equal(t, AgentTypeOrchestrator, orch.Type)
	require.NotNil(t, orch.Orchestrator)
	assert.Equal(t, 3, *orch.Orchestrator.MaxConcurrentAgents)
	assert.Equal(t, 2*time.Minute, *orch.Orchestrator.AgentTimeout)
	assert.Nil(t, orch.Orchestrator.MaxBudget)

	// Chain-level sub_agents
	chain := cfg.AgentChains["test-chain"]
	assert.Equal(t, SubAgentRefs{{Name: "worker-agent"}}, chain.SubAgents)

	// Stage-level sub_agents
	assert.Equal(t, SubAgentRefs{{Name: "worker-agent"}}, chain.Stages[0].SubAgents)

	// Stage-agent-level sub_agents
	assert.Equal(t, SubAgentRefs{{Name: "worker-agent"}}, chain.Stages[0].Agents[0].SubAgents)

	// Stage-agent type override parsed from YAML
	stage2Agent := chain.Stages[1].Agents[0]
	assert.Equal(t, "worker-agent", stage2Agent.Name)
	assert.Equal(t, AgentTypeOrchestrator, stage2Agent.Type)

	// Stage 1 agent has no type override
	assert.Equal(t, AgentType(""), chain.Stages[0].Agents[0].Type)
}

func TestLoadTarsyYAML_SubAgentRefsLongForm(t *testing.T) {
	configDir := t.TempDir()

	yamlContent := `
agents:
  worker-agent:
    description: "Worker"
  analyzer:
    description: "Analyzer"
  orch-agent:
    type: orchestrator
    description: "Orchestrator"

agent_chains:
  test-chain:
    alert_types: ["test"]
    sub_agents:
      - name: worker-agent
        max_iterations: 5
        llm_provider: fast-provider
      - analyzer
    stages:
      - name: "stage1"
        sub_agents:
          - name: analyzer
            llm_backend: langchain
            mcp_servers: [grafana]
        agents:
          - name: "orch-agent"
            sub_agents:
              - name: worker-agent
                max_iterations: 3
              - name: analyzer
                llm_provider: cheap-provider
`
	err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(yamlContent), 0644)
	require.NoError(t, err)

	loader := &configLoader{configDir: configDir}
	cfg, err := loader.loadTarsyYAML()
	require.NoError(t, err)

	chain := cfg.AgentChains["test-chain"]

	// Chain-level: mixed long-form + short-form
	require.Len(t, chain.SubAgents, 2)
	assert.Equal(t, "worker-agent", chain.SubAgents[0].Name)
	assert.Equal(t, "fast-provider", chain.SubAgents[0].LLMProvider)
	assert.Equal(t, 5, *chain.SubAgents[0].MaxIterations)
	assert.Equal(t, "analyzer", chain.SubAgents[1].Name)
	assert.Empty(t, chain.SubAgents[1].LLMProvider)

	// Stage-level: long-form with llm_backend and mcp_servers
	stageRefs := chain.Stages[0].SubAgents
	require.Len(t, stageRefs, 1)
	assert.Equal(t, "analyzer", stageRefs[0].Name)
	assert.Equal(t, LLMBackendLangChain, stageRefs[0].LLMBackend)
	assert.Equal(t, []string{"grafana"}, stageRefs[0].MCPServers)

	// Stage-agent-level: long-form overrides
	agentRefs := chain.Stages[0].Agents[0].SubAgents
	require.Len(t, agentRefs, 2)
	assert.Equal(t, "worker-agent", agentRefs[0].Name)
	assert.Equal(t, 3, *agentRefs[0].MaxIterations)
	assert.Equal(t, "analyzer", agentRefs[1].Name)
	assert.Equal(t, "cheap-provider", agentRefs[1].LLMProvider)
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

// TestLoadYAMLWithMalformedTemplates verifies that loadYAML properly handles
// malformed template syntax by passing it through to the YAML parser.
// This tests the integration between ExpandEnv's pass-through behavior and loadYAML.
func TestLoadYAMLWithMalformedTemplates(t *testing.T) {
	tests := []struct {
		name          string
		yamlContent   string
		expectSuccess bool
		description   string
	}{
		{
			name: "malformed template but valid YAML - should succeed",
			yamlContent: `
mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "test-cmd"
      # Unclosed template, but YAML parser treats it as a string
      args: ["{{.UNCLOSED_VAR"]
`,
			expectSuccess: true,
			description:   "Malformed template passed through, YAML is valid",
		},
		{
			name: "valid YAML without templates - should succeed",
			yamlContent: `
mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "test-cmd"
      args: ["arg1", "arg2"]
`,
			expectSuccess: true,
			description:   "No templates, just valid YAML",
		},
		{
			name: "malformed template AND invalid YAML - should fail",
			yamlContent: `
mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "test-cmd"
      args: ["{{.UNCLOSED"
        invalid: indentation
`,
			expectSuccess: false,
			description:   "Both malformed template and invalid YAML - YAML parser catches it",
		},
		{
			name: "valid template syntax - should succeed and expand",
			yamlContent: `
mcp_servers:
  test-server:
    transport:
      type: "stdio"
      command: "{{.TEST_CMD}}"
      args: ["{{.TEST_ARG}}"]
`,
			expectSuccess: true,
			description:   "Valid template syntax should expand successfully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory and file
			dir := t.TempDir()
			testFile := filepath.Join(dir, "test.yaml")
			err := os.WriteFile(testFile, []byte(tt.yamlContent), 0644)
			require.NoError(t, err)

			// Set environment variables for valid template expansion
			t.Setenv("TEST_CMD", "expanded-cmd")
			t.Setenv("TEST_ARG", "expanded-arg")

			// Create loader and attempt to load YAML
			loader := &configLoader{configDir: dir}
			var result TarsyYAMLConfig
			err = loader.loadYAML("test.yaml", &result)

			if tt.expectSuccess {
				assert.NoError(t, err, "Expected loadYAML to succeed: %s", tt.description)
				if err == nil {
					// Verify the YAML was parsed
					assert.NotNil(t, result.MCPServers, "MCPServers should be parsed")
				}
			} else {
				assert.Error(t, err, "Expected loadYAML to fail: %s", tt.description)
			}
		})
	}
}

// TestLoadYAMLExpandEnvIntegration verifies that loadYAML correctly calls ExpandEnv
// and receives the original data back when template parsing fails.
func TestLoadYAMLExpandEnvIntegration(t *testing.T) {
	dir := t.TempDir()

	// Test case 1: Malformed template that ExpandEnv passes through
	malformedYAML := `
mcp_servers:
  server1:
    transport:
      type: "stdio"
      command: "cmd"
      args: ["{{.MALFORMED"]
    notes: "This has an unclosed template but valid YAML structure"
`
	testFile1 := filepath.Join(dir, "malformed.yaml")
	err := os.WriteFile(testFile1, []byte(malformedYAML), 0644)
	require.NoError(t, err)

	loader := &configLoader{configDir: dir}
	var result1 TarsyYAMLConfig
	err = loader.loadYAML("malformed.yaml", &result1)

	// Should succeed because YAML is valid even though template is malformed
	assert.NoError(t, err, "loadYAML should succeed with malformed template but valid YAML")
	assert.NotNil(t, result1.MCPServers)
	assert.Contains(t, result1.MCPServers, "server1")

	// The malformed template should be preserved in the parsed data
	assert.Equal(t, "{{.MALFORMED", result1.MCPServers["server1"].Transport.Args[0],
		"Malformed template should be preserved as literal string")

	// Test case 2: Valid template that ExpandEnv processes
	validYAML := `
mcp_servers:
  server2:
    transport:
      type: "stdio"
      command: "{{.TEST_COMMAND}}"
      args: ["{{.TEST_ARG1}}"]
`
	testFile2 := filepath.Join(dir, "valid.yaml")
	err = os.WriteFile(testFile2, []byte(validYAML), 0644)
	require.NoError(t, err)

	t.Setenv("TEST_COMMAND", "expanded-command")
	t.Setenv("TEST_ARG1", "expanded-arg")

	var result2 TarsyYAMLConfig
	err = loader.loadYAML("valid.yaml", &result2)

	assert.NoError(t, err, "loadYAML should succeed with valid template")
	assert.NotNil(t, result2.MCPServers)
	assert.Contains(t, result2.MCPServers, "server2")

	// Valid templates should be expanded
	assert.Equal(t, "expanded-command", result2.MCPServers["server2"].Transport.Command,
		"Valid template should be expanded")
	assert.Equal(t, "expanded-arg", result2.MCPServers["server2"].Transport.Args[0],
		"Valid template should be expanded")
}

// TestLoadYAMLPreservesOriginalDataOnTemplateError verifies that when ExpandEnv
// returns original data due to template errors, loadYAML receives that exact data
// and the YAML parser processes it correctly.
func TestLoadYAMLPreservesOriginalDataOnTemplateError(t *testing.T) {
	dir := t.TempDir()

	// YAML with various malformed templates that should all pass through
	yamlContent := `
mcp_servers:
  test1:
    transport:
      type: "stdio"
      command: "cmd1"
      args: ["{{.UNCLOSED"]
  test2:
    transport:
      type: "stdio"
      command: "cmd2"
      args: ["{{.VAR1", "{{.VAR2}"]
  test3:
    transport:
      type: "stdio"
      command: "cmd3"
      args: ["{{", "}}", "{{.}}"]
`
	testFile := filepath.Join(dir, "malformed-multi.yaml")
	err := os.WriteFile(testFile, []byte(yamlContent), 0644)
	require.NoError(t, err)

	// Set env vars (but they shouldn't be expanded due to malformed syntax)
	t.Setenv("UNCLOSED", "should-not-appear")
	t.Setenv("VAR1", "should-not-appear")
	t.Setenv("VAR2", "should-not-appear")

	loader := &configLoader{configDir: dir}
	var result TarsyYAMLConfig
	err = loader.loadYAML("malformed-multi.yaml", &result)

	// Should succeed - YAML structure is valid even with malformed templates
	require.NoError(t, err, "loadYAML should succeed when YAML structure is valid")

	// Verify all malformed templates are preserved as literal strings
	assert.Equal(t, "{{.UNCLOSED", result.MCPServers["test1"].Transport.Args[0],
		"Malformed template should be preserved")
	assert.Equal(t, "{{.VAR1", result.MCPServers["test2"].Transport.Args[0],
		"Malformed template should be preserved")
	assert.Equal(t, "{{.VAR2}", result.MCPServers["test2"].Transport.Args[1],
		"Malformed template should be preserved")
	assert.Equal(t, "{{", result.MCPServers["test3"].Transport.Args[0],
		"Malformed template should be preserved")
	assert.Equal(t, "}}", result.MCPServers["test3"].Transport.Args[1],
		"Malformed template should be preserved")

	// Verify env vars did NOT leak through
	assert.NotContains(t, result.MCPServers["test1"].Transport.Args[0], "should-not-appear")
	assert.NotContains(t, result.MCPServers["test2"].Transport.Args[0], "should-not-appear")
}

// TestQueueConfigMerging verifies that partial queue config properly merges with defaults
func TestQueueConfigMerging(t *testing.T) {
	tests := []struct {
		name                string
		queueYAML           string
		expectWorkerCount   int
		expectMaxConcurrent int
		expectPollInterval  string
		expectJitter        string
	}{
		{
			name:                "nil queue config uses all defaults",
			queueYAML:           "",
			expectWorkerCount:   5,
			expectMaxConcurrent: 5,
			expectPollInterval:  "1s",
			expectJitter:        "500ms",
		},
		{
			name: "partial queue config merges with defaults",
			queueYAML: `
queue:
  worker_count: 10`,
			expectWorkerCount:   10,      // overridden
			expectMaxConcurrent: 5,       // default
			expectPollInterval:  "1s",    // default
			expectJitter:        "500ms", // default
		},
		{
			name: "multiple fields override preserves unset defaults",
			queueYAML: `
queue:
  worker_count: 20
  max_concurrent_sessions: 15`,
			expectWorkerCount:   20,      // overridden
			expectMaxConcurrent: 15,      // overridden
			expectPollInterval:  "1s",    // default
			expectJitter:        "500ms", // default
		},
		{
			name: "all fields can be overridden",
			queueYAML: `
queue:
  worker_count: 3
  max_concurrent_sessions: 10
  poll_interval: 2s
  poll_interval_jitter: 1s`,
			expectWorkerCount:   3,
			expectMaxConcurrent: 10,
			expectPollInterval:  "2s",
			expectJitter:        "1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()

			// Create tarsy.yaml with queue config
			tarsyYAML := `
defaults:
  llm_provider: "google-default"

agents: {}
mcp_servers: {}
agent_chains: {}
` + tt.queueYAML

			err := os.WriteFile(filepath.Join(configDir, "tarsy.yaml"), []byte(tarsyYAML), 0644)
			require.NoError(t, err)

			// Create minimal llm-providers.yaml
			err = os.WriteFile(filepath.Join(configDir, "llm-providers.yaml"), []byte("llm_providers: {}"), 0644)
			require.NoError(t, err)

			// Set all required environment variables
			t.Setenv("GOOGLE_API_KEY", "test-key")
			t.Setenv("OPENAI_API_KEY", "test-key")
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			t.Setenv("XAI_API_KEY", "test-key")
			t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
			t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

			ctx := context.Background()
			cfg, err := Initialize(ctx, configDir)

			require.NoError(t, err)
			require.NotNil(t, cfg.Queue)

			// Verify queue config values
			assert.Equal(t, tt.expectWorkerCount, cfg.Queue.WorkerCount,
				"WorkerCount should be %d", tt.expectWorkerCount)
			assert.Equal(t, tt.expectMaxConcurrent, cfg.Queue.MaxConcurrentSessions,
				"MaxConcurrentSessions should be %d", tt.expectMaxConcurrent)
			assert.Equal(t, tt.expectPollInterval, cfg.Queue.PollInterval.String(),
				"PollInterval should be %s", tt.expectPollInterval)
			assert.Equal(t, tt.expectJitter, cfg.Queue.PollIntervalJitter.String(),
				"PollIntervalJitter should be %s", tt.expectJitter)
		})
	}
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

func TestResolveGitHubConfig(t *testing.T) {
	t.Run("nil system config uses defaults", func(t *testing.T) {
		cfg := resolveGitHubConfig(nil)
		assert.Equal(t, "GITHUB_TOKEN", cfg.TokenEnv)
	})

	t.Run("nil github section uses defaults", func(t *testing.T) {
		sys := &SystemYAMLConfig{}
		cfg := resolveGitHubConfig(sys)
		assert.Equal(t, "GITHUB_TOKEN", cfg.TokenEnv)
	})

	t.Run("custom token_env is used", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			GitHub: &GitHubYAMLConfig{TokenEnv: "MY_GH_TOKEN"},
		}
		cfg := resolveGitHubConfig(sys)
		assert.Equal(t, "MY_GH_TOKEN", cfg.TokenEnv)
	})

	t.Run("empty token_env falls back to default", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			GitHub: &GitHubYAMLConfig{TokenEnv: ""},
		}
		cfg := resolveGitHubConfig(sys)
		assert.Equal(t, "GITHUB_TOKEN", cfg.TokenEnv)
	})
}

func TestResolveRunbooksConfig(t *testing.T) {
	t.Run("nil system config uses defaults", func(t *testing.T) {
		cfg := resolveRunbooksConfig(nil)
		assert.Equal(t, "", cfg.RepoURL)
		assert.Equal(t, 1*time.Minute, cfg.CacheTTL)
		assert.Equal(t, []string{"github.com", "raw.githubusercontent.com"}, cfg.AllowedDomains)
	})

	t.Run("nil runbooks section uses defaults", func(t *testing.T) {
		sys := &SystemYAMLConfig{}
		cfg := resolveRunbooksConfig(sys)
		assert.Equal(t, "", cfg.RepoURL)
		assert.Equal(t, 1*time.Minute, cfg.CacheTTL)
	})

	t.Run("full config overrides defaults", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			Runbooks: &RunbooksYAMLConfig{
				RepoURL:        "https://github.com/org/repo/tree/main/runbooks",
				CacheTTL:       "5m",
				AllowedDomains: []string{"github.com"},
			},
		}
		cfg := resolveRunbooksConfig(sys)
		assert.Equal(t, "https://github.com/org/repo/tree/main/runbooks", cfg.RepoURL)
		assert.Equal(t, 5*time.Minute, cfg.CacheTTL)
		assert.Equal(t, []string{"github.com"}, cfg.AllowedDomains)
	})

	t.Run("partial config keeps defaults for unset fields", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			Runbooks: &RunbooksYAMLConfig{
				RepoURL: "https://github.com/org/repo/tree/main/runbooks",
			},
		}
		cfg := resolveRunbooksConfig(sys)
		assert.Equal(t, "https://github.com/org/repo/tree/main/runbooks", cfg.RepoURL)
		assert.Equal(t, 1*time.Minute, cfg.CacheTTL)
		assert.Equal(t, []string{"github.com", "raw.githubusercontent.com"}, cfg.AllowedDomains)
	})

	t.Run("invalid cache_ttl keeps default", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			Runbooks: &RunbooksYAMLConfig{
				CacheTTL: "not-a-duration",
			},
		}
		cfg := resolveRunbooksConfig(sys)
		assert.Equal(t, 1*time.Minute, cfg.CacheTTL)
	})
}

func TestResolveRetentionConfig(t *testing.T) {
	t.Run("nil system config uses defaults", func(t *testing.T) {
		cfg := resolveRetentionConfig(nil)
		assert.Equal(t, 365, cfg.SessionRetentionDays)
		assert.Equal(t, 1*time.Hour, cfg.EventTTL)
		assert.Equal(t, 12*time.Hour, cfg.CleanupInterval)
	})

	t.Run("nil retention section uses defaults", func(t *testing.T) {
		sys := &SystemYAMLConfig{}
		cfg := resolveRetentionConfig(sys)
		assert.Equal(t, 365, cfg.SessionRetentionDays)
		assert.Equal(t, 1*time.Hour, cfg.EventTTL)
		assert.Equal(t, 12*time.Hour, cfg.CleanupInterval)
	})

	t.Run("full config overrides defaults", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			Retention: &RetentionConfig{
				SessionRetentionDays: 90,
				EventTTL:             30 * time.Minute,
				CleanupInterval:      6 * time.Hour,
			},
		}
		cfg := resolveRetentionConfig(sys)
		assert.Equal(t, 90, cfg.SessionRetentionDays)
		assert.Equal(t, 30*time.Minute, cfg.EventTTL)
		assert.Equal(t, 6*time.Hour, cfg.CleanupInterval)
	})

	t.Run("partial config keeps defaults for unset fields", func(t *testing.T) {
		sys := &SystemYAMLConfig{
			Retention: &RetentionConfig{
				SessionRetentionDays: 180,
			},
		}
		cfg := resolveRetentionConfig(sys)
		assert.Equal(t, 180, cfg.SessionRetentionDays)
		assert.Equal(t, 1*time.Hour, cfg.EventTTL)
		assert.Equal(t, 12*time.Hour, cfg.CleanupInterval)
	})
}

func TestSystemConfigYAMLLoading(t *testing.T) {
	t.Run("system section parsed from YAML", func(t *testing.T) {
		dir := t.TempDir()

		tarsyYAML := `
system:
  github:
    token_env: "CUSTOM_TOKEN"
  runbooks:
    repo_url: "https://github.com/org/repo/tree/main/runbooks"
    cache_ttl: "2m"
    allowed_domains:
      - "github.com"
defaults:
  llm_provider: "google-default"
  max_iterations: 20
agents: {}
mcp_servers: {}
agent_chains: {}
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tarsy.yaml"), []byte(tarsyYAML), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "llm-providers.yaml"), []byte("llm_providers: {}\n"), 0644))

		cfg, err := load(context.Background(), dir)
		require.NoError(t, err)

		require.NotNil(t, cfg.GitHub)
		assert.Equal(t, "CUSTOM_TOKEN", cfg.GitHub.TokenEnv)

		require.NotNil(t, cfg.Runbooks)
		assert.Equal(t, "https://github.com/org/repo/tree/main/runbooks", cfg.Runbooks.RepoURL)
		assert.Equal(t, 2*time.Minute, cfg.Runbooks.CacheTTL)
		assert.Equal(t, []string{"github.com"}, cfg.Runbooks.AllowedDomains)
	})

	t.Run("no system section uses defaults", func(t *testing.T) {
		dir := setupTestConfigDir(t)

		cfg, err := load(context.Background(), dir)
		require.NoError(t, err)

		require.NotNil(t, cfg.GitHub)
		assert.Equal(t, "GITHUB_TOKEN", cfg.GitHub.TokenEnv)

		require.NotNil(t, cfg.Runbooks)
		assert.Equal(t, "", cfg.Runbooks.RepoURL)
		assert.Equal(t, 1*time.Minute, cfg.Runbooks.CacheTTL)
	})
}

func TestLoadAppliesSummarizationDefaults(t *testing.T) {
	dir := t.TempDir()

	tarsyYAML := `
defaults:
  llm_provider: "google-default"
  max_iterations: 20
agents: {}
mcp_servers:
  my-server:
    transport:
      type: "http"
      url: "https://example.com/mcp"
    summarization:
      summary_max_token_limit: 1200
agent_chains: {}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tarsy.yaml"), []byte(tarsyYAML), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "llm-providers.yaml"), []byte("llm_providers: {}\n"), 0644))

	cfg, err := load(context.Background(), dir)
	require.NoError(t, err)

	server, err := cfg.MCPServerRegistry.Get("my-server")
	require.NoError(t, err)
	require.NotNil(t, server.Summarization)
	assert.Nil(t, server.Summarization.Enabled, "Enabled should be nil when omitted from YAML")
	assert.False(t, server.Summarization.SummarizationDisabled(), "nil Enabled means enabled by default")
	assert.Equal(t, DefaultSizeThresholdTokens, server.Summarization.SizeThresholdTokens,
		"size_threshold_tokens should default to %d when not specified", DefaultSizeThresholdTokens)
	assert.Equal(t, 1200, server.Summarization.SummaryMaxTokenLimit)
}
