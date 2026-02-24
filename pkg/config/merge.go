package config

// mergeAgents merges built-in and user-defined agent configurations.
// User-defined agents override built-in agents with the same name.
func mergeAgents(builtinAgents map[string]BuiltinAgentConfig, userAgents map[string]AgentConfig) map[string]*AgentConfig {
	result := make(map[string]*AgentConfig)

	// First, convert built-in agents to AgentConfig format
	for name, builtin := range builtinAgents {
		// Defensive copy of MCPServers slice to prevent shared state
		mcpCopy := make([]string, len(builtin.MCPServers))
		copy(mcpCopy, builtin.MCPServers)
		result[name] = &AgentConfig{
			Type:               builtin.Type,
			Description:        builtin.Description,
			MCPServers:         mcpCopy,
			CustomInstructions: builtin.CustomInstructions,
		}
	}

	// Then, override with user-defined agents (or add new ones)
	for name, userAgent := range userAgents {
		agentCopy := userAgent // Create a copy
		result[name] = &agentCopy
	}

	return result
}

// mergeMCPServers merges built-in and user-defined MCP server configurations.
// User-defined servers override built-in servers with the same ID.
func mergeMCPServers(builtinServers map[string]MCPServerConfig, userServers map[string]MCPServerConfig) map[string]*MCPServerConfig {
	result := make(map[string]*MCPServerConfig)

	// First, add built-in servers
	for id, server := range builtinServers {
		serverCopy := server
		result[id] = &serverCopy
	}

	// Then, override with user-defined servers (or add new ones)
	for id, userServer := range userServers {
		serverCopy := userServer
		result[id] = &serverCopy
	}

	return result
}

// mergeChains merges built-in and user-defined chain configurations.
// User-defined chains override built-in chains with the same ID.
func mergeChains(builtinChains map[string]ChainConfig, userChains map[string]ChainConfig) map[string]*ChainConfig {
	result := make(map[string]*ChainConfig)

	// First, add built-in chains
	for id, chain := range builtinChains {
		chainCopy := chain
		result[id] = &chainCopy
	}

	// Then, override with user-defined chains (or add new ones)
	for id, userChain := range userChains {
		chainCopy := userChain
		result[id] = &chainCopy
	}

	return result
}

// mergeLLMProviders merges built-in and user-defined LLM provider configurations.
// User-defined providers override built-in providers with the same name.
func mergeLLMProviders(builtinProviders map[string]LLMProviderConfig, userProviders map[string]LLMProviderConfig) map[string]*LLMProviderConfig {
	result := make(map[string]*LLMProviderConfig)

	// First, add built-in providers
	for name, provider := range builtinProviders {
		providerCopy := provider
		result[name] = &providerCopy
	}

	// Then, override with user-defined providers (or add new ones)
	for name, userProvider := range userProviders {
		providerCopy := userProvider
		result[name] = &providerCopy
	}

	return result
}
