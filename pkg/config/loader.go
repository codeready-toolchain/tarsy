package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"dario.cat/mergo"
	"gopkg.in/yaml.v3"
)

// TarsyYAMLConfig represents the complete tarsy.yaml file structure
type TarsyYAMLConfig struct {
	System      *SystemYAMLConfig          `yaml:"system"`
	MCPServers  map[string]MCPServerConfig `yaml:"mcp_servers"`
	Agents      map[string]AgentConfig     `yaml:"agents"`
	AgentChains map[string]ChainConfig     `yaml:"agent_chains"`
	Defaults    *Defaults                  `yaml:"defaults"`
	Queue       *QueueConfig               `yaml:"queue"`
}

// SystemYAMLConfig groups system-wide infrastructure settings.
type SystemYAMLConfig struct {
	DashboardURL     string              `yaml:"dashboard_url"`
	AllowedWSOrigins []string            `yaml:"allowed_ws_origins"`
	GitHub           *GitHubYAMLConfig   `yaml:"github"`
	Runbooks         *RunbooksYAMLConfig `yaml:"runbooks"`
	Slack            *SlackYAMLConfig    `yaml:"slack"`
	Retention        *RetentionConfig    `yaml:"retention"`
}

// SlackYAMLConfig holds Slack notification settings from YAML.
type SlackYAMLConfig struct {
	Enabled  *bool  `yaml:"enabled,omitempty"`
	TokenEnv string `yaml:"token_env,omitempty"`
	Channel  string `yaml:"channel,omitempty"`
}

// GitHubYAMLConfig holds GitHub integration settings from YAML.
type GitHubYAMLConfig struct {
	TokenEnv string `yaml:"token_env,omitempty"` // Defaults to "GITHUB_TOKEN" if omitted
}

// RunbooksYAMLConfig holds runbook system settings from YAML.
type RunbooksYAMLConfig struct {
	RepoURL        string   `yaml:"repo_url,omitempty"`
	CacheTTL       string   `yaml:"cache_ttl,omitempty"` // Parsed to time.Duration
	AllowedDomains []string `yaml:"allowed_domains,omitempty"`
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
//  5. Apply MCP server defaults (e.g. size_threshold_tokens)
//  6. Build in-memory registries
//  7. Apply default values
//  8. Validate all configuration
//  9. Return Config ready for use
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

	// 5. Apply MCP server defaults (before validation)
	for _, server := range mcpServers {
		if server.Summarization != nil && server.Summarization.Enabled && server.Summarization.SizeThresholdTokens == 0 {
			server.Summarization.SizeThresholdTokens = DefaultSizeThresholdTokens
		}
	}

	// 6. Build registries
	agentRegistry := NewAgentRegistry(agents)
	mcpServerRegistry := NewMCPServerRegistry(mcpServers)
	chainRegistry := NewChainRegistry(chains)
	llmProviderRegistry := NewLLMProviderRegistry(llmProvidersMerged)

	// 7. Resolve defaults (YAML overrides built-in)
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
	if defaults.AlertMasking == nil {
		defaults.AlertMasking = &AlertMaskingDefaults{
			Enabled:      true,
			PatternGroup: "security",
		}
	}

	// Resolve queue config (merge user YAML with built-in defaults)
	// Start with defaults, then merge user config on top to preserve unset defaults
	queueConfig := DefaultQueueConfig()
	if tarsyConfig.Queue != nil {
		// Merge user-provided config into defaults (non-zero values override)
		if err := mergo.Merge(queueConfig, tarsyConfig.Queue, mergo.WithOverride); err != nil {
			return nil, fmt.Errorf("failed to merge queue config: %w", err)
		}
	}

	// Resolve system config (GitHub + Runbooks + Slack + Retention + DashboardURL + WS Origins)
	githubCfg := resolveGitHubConfig(tarsyConfig.System)
	runbooksCfg := resolveRunbooksConfig(tarsyConfig.System)
	slackCfg := resolveSlackConfig(tarsyConfig.System)
	retentionCfg := resolveRetentionConfig(tarsyConfig.System)
	dashboardURL := resolveDashboardURL(tarsyConfig.System)
	allowedWSOrigins := resolveAllowedWSOrigins(tarsyConfig.System)

	return &Config{
		configDir:           configDir,
		Defaults:            defaults,
		Queue:               queueConfig,
		GitHub:              githubCfg,
		Runbooks:            runbooksCfg,
		Slack:               slackCfg,
		Retention:           retentionCfg,
		DashboardURL:        dashboardURL,
		AllowedWSOrigins:    allowedWSOrigins,
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

// resolveGitHubConfig resolves GitHub configuration from system YAML, applying defaults.
func resolveGitHubConfig(sys *SystemYAMLConfig) *GitHubConfig {
	cfg := &GitHubConfig{
		TokenEnv: "GITHUB_TOKEN",
	}

	if sys != nil && sys.GitHub != nil && sys.GitHub.TokenEnv != "" {
		cfg.TokenEnv = sys.GitHub.TokenEnv
	}

	return cfg
}

// resolveRunbooksConfig resolves runbook configuration from system YAML, applying defaults.
func resolveRunbooksConfig(sys *SystemYAMLConfig) *RunbookConfig {
	cfg := &RunbookConfig{
		CacheTTL:       1 * time.Minute,
		AllowedDomains: []string{"github.com", "raw.githubusercontent.com"},
	}

	if sys == nil || sys.Runbooks == nil {
		return cfg
	}

	rb := sys.Runbooks
	if rb.RepoURL != "" {
		cfg.RepoURL = rb.RepoURL
	}
	if rb.CacheTTL != "" {
		if d, err := time.ParseDuration(rb.CacheTTL); err == nil {
			cfg.CacheTTL = d
		} else {
			slog.Warn("Invalid cache_ttl in runbooks config, using default",
				"value", rb.CacheTTL,
				"default", cfg.CacheTTL,
				"error", err)
		}
	}
	if len(rb.AllowedDomains) > 0 {
		cfg.AllowedDomains = rb.AllowedDomains
	}

	return cfg
}

// resolveSlackConfig resolves Slack configuration from system YAML, applying defaults.
func resolveSlackConfig(sys *SystemYAMLConfig) *SlackConfig {
	cfg := &SlackConfig{
		Enabled:  false,
		TokenEnv: "SLACK_BOT_TOKEN",
	}

	if sys == nil || sys.Slack == nil {
		return cfg
	}

	s := sys.Slack
	if s.Enabled != nil {
		cfg.Enabled = *s.Enabled
	}
	if s.TokenEnv != "" {
		cfg.TokenEnv = s.TokenEnv
	}
	if s.Channel != "" {
		cfg.Channel = s.Channel
	}

	return cfg
}

// resolveDashboardURL resolves the dashboard base URL from system YAML, applying defaults.
func resolveDashboardURL(sys *SystemYAMLConfig) string {
	if sys != nil && sys.DashboardURL != "" {
		return sys.DashboardURL
	}
	return "http://localhost:5173"
}

// resolveRetentionConfig resolves retention configuration from system YAML, applying defaults.
func resolveRetentionConfig(sys *SystemYAMLConfig) *RetentionConfig {
	cfg := DefaultRetentionConfig()

	if sys == nil || sys.Retention == nil {
		return cfg
	}

	r := sys.Retention
	if r.SessionRetentionDays > 0 {
		cfg.SessionRetentionDays = r.SessionRetentionDays
	}
	if r.EventTTL > 0 {
		cfg.EventTTL = r.EventTTL
	}
	if r.CleanupInterval > 0 {
		cfg.CleanupInterval = r.CleanupInterval
	}

	return cfg
}

// resolveAllowedWSOrigins returns additional WebSocket origin patterns from system YAML.
func resolveAllowedWSOrigins(sys *SystemYAMLConfig) []string {
	if sys != nil {
		return sys.AllowedWSOrigins
	}
	return nil
}
