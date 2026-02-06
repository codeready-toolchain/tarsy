// Package config provides configuration management for the Tarsy system,
// including agent, chain, MCP server, and LLM provider configurations.
package config

import (
	"fmt"
	"sync"
)

// AgentConfig defines agent configuration (metadata only - instantiation is Phase 3)
type AgentConfig struct {
	// MCP servers this agent uses
	MCPServers []string `yaml:"mcp_servers" validate:"required,min=1"`

	// Custom instructions override built-in agent behavior
	CustomInstructions string `yaml:"custom_instructions"`

	// Iteration strategy for this agent
	IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`

	// Max iterations for this agent (forces conclusion when reached, no pause/resume)
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}

// AgentRegistry stores agent configurations in memory with thread-safe access
type AgentRegistry struct {
	agents map[string]*AgentConfig
	mu     sync.RWMutex
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry(agents map[string]*AgentConfig) *AgentRegistry {
	return &AgentRegistry{
		agents: agents,
	}
}

// Get retrieves an agent configuration by name (thread-safe)
func (r *AgentRegistry) Get(name string) (*AgentConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}
	return agent, nil
}

// GetAll returns all agent configurations (thread-safe, returns copy)
func (r *AgentRegistry) GetAll() map[string]*AgentConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]*AgentConfig, len(r.agents))
	for k, v := range r.agents {
		result[k] = v
	}
	return result
}

// Has checks if an agent exists in the registry (thread-safe)
func (r *AgentRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.agents[name]
	return exists
}
