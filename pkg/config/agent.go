// Package config provides configuration management for the Tarsy system,
// including agent, chain, MCP server, and LLM provider configurations.
package config

import (
	"fmt"
	"sync"
	"time"
)

// AgentConfig defines agent configuration (metadata only — see agent.AgentFactory for instantiation).
type AgentConfig struct {
	// Agent type determines controller + wrapper selection
	Type AgentType `yaml:"type,omitempty"`

	// Human-readable description
	Description string `yaml:"description,omitempty"`

	// MCP servers this agent uses
	MCPServers []string `yaml:"mcp_servers" validate:"omitempty"`

	// Custom instructions override built-in agent behavior
	CustomInstructions string `yaml:"custom_instructions"`

	// LLM backend for this agent
	LLMBackend LLMBackend `yaml:"llm_backend,omitempty"`

	// Max iterations for this agent (forces conclusion when reached, no pause/resume)
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// Per-agent native tool overrides (Google/Gemini). Merges with the LLM
	// provider's NativeTools on a per-key basis: agent keys override provider keys,
	// missing keys fall through to the provider default.
	NativeTools map[GoogleNativeTool]bool `yaml:"native_tools,omitempty"`

	// Orchestrator-specific configuration (only valid when Type == orchestrator)
	Orchestrator *OrchestratorConfig `yaml:"orchestrator,omitempty"`
}

// OrchestratorConfig holds orchestrator-specific settings.
// Resolved at runtime by merging defaults.orchestrator → agent-level orchestrator.
type OrchestratorConfig struct {
	MaxConcurrentAgents *int           `yaml:"max_concurrent_agents,omitempty"`
	AgentTimeout        *time.Duration `yaml:"agent_timeout,omitempty"`
	MaxBudget           *time.Duration `yaml:"max_budget,omitempty"`
}

// AgentRegistry stores agent configurations in memory with thread-safe access
type AgentRegistry struct {
	agents map[string]*AgentConfig
	mu     sync.RWMutex
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry(agents map[string]*AgentConfig) *AgentRegistry {
	// Defensive copy to prevent external mutation
	copied := make(map[string]*AgentConfig, len(agents))
	for k, v := range agents {
		copied[k] = v
	}
	return &AgentRegistry{
		agents: copied,
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

// Len returns the number of agents in the registry (thread-safe)
func (r *AgentRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}
