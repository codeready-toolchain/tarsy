package config

// Config is the umbrella configuration object that encapsulates
// all registries, defaults, and configuration state.
// This is the primary object returned by Initialize() and used throughout the application.
type Config struct {
	configDir string // Configuration directory path (for reference)

	// System-wide defaults
	Defaults *Defaults

	// Queue and worker pool configuration
	Queue *QueueConfig

	// Component registries
	AgentRegistry       *AgentRegistry
	ChainRegistry       *ChainRegistry
	MCPServerRegistry   *MCPServerRegistry
	LLMProviderRegistry *LLMProviderRegistry
}

// Initialize is defined in loader.go

// Stats contains statistics about loaded configuration
type Stats struct {
	Agents       int
	Chains       int
	MCPServers   int
	LLMProviders int
}

// Stats returns configuration statistics for logging/monitoring
func (c *Config) Stats() Stats {
	s := Stats{}
	if c.AgentRegistry != nil {
		s.Agents = c.AgentRegistry.Len()
	}
	if c.ChainRegistry != nil {
		s.Chains = c.ChainRegistry.Len()
	}
	if c.MCPServerRegistry != nil {
		s.MCPServers = c.MCPServerRegistry.Len()
	}
	if c.LLMProviderRegistry != nil {
		s.LLMProviders = c.LLMProviderRegistry.Len()
	}
	return s
}

// ConfigDir returns the configuration directory path
func (c *Config) ConfigDir() string {
	return c.configDir
}

// GetAgent retrieves an agent configuration by name.
// This is a convenience method that wraps AgentRegistry.Get().
func (c *Config) GetAgent(name string) (*AgentConfig, error) {
	return c.AgentRegistry.Get(name)
}

// GetChain retrieves a chain configuration by ID.
// This is a convenience method that wraps ChainRegistry.Get().
func (c *Config) GetChain(chainID string) (*ChainConfig, error) {
	return c.ChainRegistry.Get(chainID)
}

// GetChainByAlertType retrieves the first chain that handles the given alert type.
// This is a convenience method that wraps ChainRegistry.GetByAlertType().
func (c *Config) GetChainByAlertType(alertType string) (*ChainConfig, error) {
	return c.ChainRegistry.GetByAlertType(alertType)
}

// GetMCPServer retrieves an MCP server configuration by ID.
// This is a convenience method that wraps MCPServerRegistry.Get().
func (c *Config) GetMCPServer(serverID string) (*MCPServerConfig, error) {
	return c.MCPServerRegistry.Get(serverID)
}

// GetLLMProvider retrieves an LLM provider configuration by name.
// This is a convenience method that wraps LLMProviderRegistry.Get().
func (c *Config) GetLLMProvider(name string) (*LLMProviderConfig, error) {
	return c.LLMProviderRegistry.Get(name)
}

// AllMCPServerIDs returns a sorted list of all configured MCP server IDs.
func (c *Config) AllMCPServerIDs() []string {
	return c.MCPServerRegistry.ServerIDs()
}
