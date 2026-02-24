package config

import (
	"fmt"
	"sync"
)

// ChainConfig defines a multi-stage agent chain configuration
type ChainConfig struct {
	// Alert types this chain handles (required, min 1)
	AlertTypes []string `yaml:"alert_types" validate:"required,min=1"`

	// Human-readable description
	Description string `yaml:"description,omitempty"`

	// Stages to execute (required, min 1)
	Stages []StageConfig `yaml:"stages" validate:"required,min=1,dive"`

	// Optional chat configuration
	Chat *ChatConfig `yaml:"chat,omitempty"`

	// Optional scoring configuration
	Scoring *ScoringConfig `yaml:"scoring,omitempty"`

	// Chain-level LLM provider override
	LLMProvider string `yaml:"llm_provider,omitempty"`

	// LLM provider for executive summary generation (overrides LLMProvider for this purpose)
	ExecutiveSummaryProvider string `yaml:"executive_summary_provider,omitempty"`

	// Chain-level LLM backend override
	LLMBackend LLMBackend `yaml:"llm_backend,omitempty"`

	// Chain-level max iterations override
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// Chain-level MCP servers override
	MCPServers []string `yaml:"mcp_servers,omitempty"`
}

// StageConfig defines a single stage in a chain
type StageConfig struct {
	// Stage name (required)
	Name string `yaml:"name" validate:"required"`

	// Agents to execute (always use array, min 1)
	// Single agent: [{name: "AgentName"}]
	// Multiple agents: [{name: "Agent1"}, {name: "Agent2"}]
	Agents []StageAgentConfig `yaml:"agents" validate:"required,min=1,dive"`

	// Replicas for simple redundancy (default: 1)
	// Run same agent N times with same config
	Replicas int `yaml:"replicas,omitempty" validate:"omitempty,min=1"`

	// Success policy for parallel execution ("all" or "any")
	SuccessPolicy SuccessPolicy `yaml:"success_policy,omitempty"`

	// Stage-level max iterations override
	MaxIterations *int `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`

	// Stage-level MCP servers override
	MCPServers []string `yaml:"mcp_servers,omitempty"`

	// Optional synthesis configuration (for parallel execution)
	Synthesis *SynthesisConfig `yaml:"synthesis,omitempty"`
}

// ChainRegistry stores chain configurations in memory with thread-safe access
type ChainRegistry struct {
	chains map[string]*ChainConfig
	mu     sync.RWMutex
}

// NewChainRegistry creates a new chain registry
func NewChainRegistry(chains map[string]*ChainConfig) *ChainRegistry {
	// Defensive copy to prevent external mutation
	copied := make(map[string]*ChainConfig, len(chains))
	for k, v := range chains {
		copied[k] = v
	}
	return &ChainRegistry{
		chains: copied,
	}
}

// Get retrieves a chain configuration by ID (thread-safe)
func (r *ChainRegistry) Get(chainID string) (*ChainConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain, exists := r.chains[chainID]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrChainNotFound, chainID)
	}
	return chain, nil
}

// GetByAlertType retrieves the first chain that handles the given alert type (thread-safe)
func (r *ChainRegistry) GetByAlertType(alertType string) (*ChainConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chainID := r.findChainIDByAlertType(alertType)
	if chainID == "" {
		return nil, fmt.Errorf("%w for alert type: %s", ErrChainNotFound, alertType)
	}
	return r.chains[chainID], nil
}

// GetIDByAlertType retrieves the chain ID that handles the given alert type (thread-safe)
func (r *ChainRegistry) GetIDByAlertType(alertType string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chainID := r.findChainIDByAlertType(alertType)
	if chainID == "" {
		return "", fmt.Errorf("%w for alert type: %s", ErrChainNotFound, alertType)
	}
	return chainID, nil
}

// findChainIDByAlertType is an unexported helper that assumes the lock is held
func (r *ChainRegistry) findChainIDByAlertType(alertType string) string {
	for chainID, chain := range r.chains {
		for _, at := range chain.AlertTypes {
			if at == alertType {
				return chainID
			}
		}
	}
	return ""
}

// GetAll returns all chain configurations (thread-safe, returns copy)
func (r *ChainRegistry) GetAll() map[string]*ChainConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]*ChainConfig, len(r.chains))
	for k, v := range r.chains {
		result[k] = v
	}
	return result
}

// Has checks if a chain exists in the registry (thread-safe)
func (r *ChainRegistry) Has(chainID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.chains[chainID]
	return exists
}

// Len returns the number of chains in the registry (thread-safe)
func (r *ChainRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chains)
}
