package config

import (
	"fmt"
	"sync"
)

// MCPServerConfig defines MCP server configuration
type MCPServerConfig struct {
	// Transport configuration (required)
	Transport TransportConfig `yaml:"transport" validate:"required"`

	// Instructions for LLM when using this MCP server
	Instructions string `yaml:"instructions,omitempty"`

	// Data masking configuration (critical for security)
	DataMasking *MaskingConfig `yaml:"data_masking,omitempty"`

	// Summarization configuration (critical for large responses)
	Summarization *SummarizationConfig `yaml:"summarization,omitempty"`
}

// MCPServerRegistry stores MCP server configurations in memory with thread-safe access
type MCPServerRegistry struct {
	servers map[string]*MCPServerConfig
	mu      sync.RWMutex
}

// NewMCPServerRegistry creates a new MCP server registry
func NewMCPServerRegistry(servers map[string]*MCPServerConfig) *MCPServerRegistry {
	return &MCPServerRegistry{
		servers: servers,
	}
}

// Get retrieves an MCP server configuration by ID (thread-safe)
func (r *MCPServerRegistry) Get(serverID string) (*MCPServerConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	server, exists := r.servers[serverID]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrMCPServerNotFound, serverID)
	}
	return server, nil
}

// GetAll returns all MCP server configurations (thread-safe, returns copy)
func (r *MCPServerRegistry) GetAll() map[string]*MCPServerConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]*MCPServerConfig, len(r.servers))
	for k, v := range r.servers {
		result[k] = v
	}
	return result
}

// Has checks if an MCP server exists in the registry (thread-safe)
func (r *MCPServerRegistry) Has(serverID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.servers[serverID]
	return exists
}
