package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// TarsyYAMLConfig represents the complete tarsy.yaml file structure
type TarsyYAMLConfig struct {
	MCPServers  map[string]MCPServerConfig `yaml:"mcp_servers"`
	Agents      map[string]AgentConfig     `yaml:"agents"`
	AgentChains map[string]ChainConfig     `yaml:"agent_chains"`
	Defaults    *Defaults                  `yaml:"defaults"`
}

// LLMProvidersYAMLConfig represents the complete llm-providers.yaml file structure
type LLMProvidersYAMLConfig struct {
	LLMProviders map[string]LLMProviderConfig `yaml:"llm_providers"`
}

// Initialize loads, validates, and returns ready-to-use configuration.
// This is the primary entry point for configuration loading.
//
// Steps performed:
//  1. Load YAML files from configDir
//  2. Expand environment variables
//  3. Parse YAML into structs
//  4. Merge built-in + user-defined configurations
//  5. Build in-memory registries
//  6. Apply default values
//  7. Validate all configuration
//  8. Return Config ready for use
func Initialize(ctx context.Context, configDir string) (*Config, error) {
	log := slog.With("config_dir", configDir)
	log.Info("Initializing configuration")

	// 1. Load configuration files
	cfg, err := load(ctx, configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// 2. Validate all configuration
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	stats := cfg.Stats()
	log.Info("Configuration initialized successfully",
		"agents", stats.Agents,
		"chains", stats.Chains,
		"mcp_servers", stats.MCPServers,
		"llm_providers", stats.LLMProviders)

	return cfg, nil
}

// load is the internal loader (not exported)
func load(_ context.Context, configDir string) (*Config, error) {
	loader := &configLoader{
		configDir: configDir,
	}

	// 1. Load tarsy.yaml (contains mcp_servers, agents, agent_chains, defaults)
	tarsyConfig, err := loader.loadTarsyYAML()
	if err != nil {
		return nil, NewLoadError("tarsy.yaml", err)
	}

	// 2. Load llm-providers.yaml
	llmProviders, err := loader.loadLLMProvidersYAML()
	if err != nil {
		return nil, NewLoadError("llm-providers.yaml", err)
	}

	// 3. Get built-in configuration
	builtin := GetBuiltinConfig()

	// 4. Merge built-in + user-defined components (user overrides built-in)
	agents := mergeAgents(builtin.Agents, tarsyConfig.Agents)
	mcpServers := mergeMCPServers(builtin.MCPServers, tarsyConfig.MCPServers)
	chains := mergeChains(builtin.ChainDefinitions, tarsyConfig.AgentChains)
	llmProvidersMerged := mergeLLMProviders(builtin.LLMProviders, llmProviders)

	// 5. Build registries
	agentRegistry := NewAgentRegistry(agents)
	mcpServerRegistry := NewMCPServerRegistry(mcpServers)
	chainRegistry := NewChainRegistry(chains)
	llmProviderRegistry := NewLLMProviderRegistry(llmProvidersMerged)

	// 6. Resolve defaults (YAML overrides built-in)
	defaults := tarsyConfig.Defaults
	if defaults == nil {
		defaults = &Defaults{}
	}

	// Apply built-in defaults for any unset values
	if defaults.AlertType == "" {
		defaults.AlertType = builtin.DefaultAlertType
	}
	if defaults.Runbook == "" {
		defaults.Runbook = builtin.DefaultRunbook
	}

	return &Config{
		configDir:           configDir,
		Defaults:            defaults,
		AgentRegistry:       agentRegistry,
		ChainRegistry:       chainRegistry,
		MCPServerRegistry:   mcpServerRegistry,
		LLMProviderRegistry: llmProviderRegistry,
	}, nil
}

// validate performs comprehensive validation on loaded configuration
func validate(cfg *Config) error {
	validator := NewValidator(cfg)
	return validator.ValidateAll()
}

type configLoader struct {
	configDir string
}

func (l *configLoader) loadYAML(filename string, target any) error {
	path := filepath.Join(l.configDir, filename)

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}
		return err
	}

	// Expand environment variables using {{.VAR}} template syntax
	// Note: ExpandEnv passes through original data on parse/execution errors,
	// allowing YAML parser to handle the content (or fail with clearer error message)
	data = ExpandEnv(data)

	// Parse YAML
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}

	return nil
}

func (l *configLoader) loadTarsyYAML() (*TarsyYAMLConfig, error) {
	var config TarsyYAMLConfig

	// Initialize maps to avoid nil maps
	config.MCPServers = make(map[string]MCPServerConfig)
	config.Agents = make(map[string]AgentConfig)
	config.AgentChains = make(map[string]ChainConfig)

	if err := l.loadYAML("tarsy.yaml", &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (l *configLoader) loadLLMProvidersYAML() (map[string]LLMProviderConfig, error) {
	var config LLMProvidersYAMLConfig

	// Initialize map to avoid nil map
	config.LLMProviders = make(map[string]LLMProviderConfig)

	if err := l.loadYAML("llm-providers.yaml", &config); err != nil {
		return nil, err
	}

	return config.LLMProviders, nil
}
