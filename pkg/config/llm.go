package config

import (
	"fmt"
	"sync"
)

// LLMProviderConfig defines LLM provider configuration
type LLMProviderConfig struct {
	// Provider type (required)
	Type LLMProviderType `yaml:"type" validate:"required"`

	// Model name (required)
	Model string `yaml:"model" validate:"required"`

	// Environment variable name for API key
	APIKeyEnv string `yaml:"api_key_env,omitempty"`

	// For VertexAI/GCP
	ProjectEnv  string `yaml:"project_env,omitempty"`
	LocationEnv string `yaml:"location_env,omitempty"`

	// Optional custom endpoint/base URL
	BaseURL string `yaml:"base_url,omitempty"`

	// Maximum tokens for tool results (required, min 1000)
	MaxToolResultTokens int `yaml:"max_tool_result_tokens" validate:"required,min=1000"`

	// Google-specific native tools
	NativeTools map[GoogleNativeTool]bool `yaml:"native_tools,omitempty"`
}

// LLMProviderRegistry stores LLM provider configurations in memory with thread-safe access
type LLMProviderRegistry struct {
	providers map[string]*LLMProviderConfig
	mu        sync.RWMutex
}

// NewLLMProviderRegistry creates a new LLM provider registry
func NewLLMProviderRegistry(providers map[string]*LLMProviderConfig) *LLMProviderRegistry {
	// Defensive copy to prevent external mutation
	copied := make(map[string]*LLMProviderConfig, len(providers))
	for k, v := range providers {
		copied[k] = v
	}
	return &LLMProviderRegistry{
		providers: copied,
	}
}

// Get retrieves an LLM provider configuration by name (thread-safe)
func (r *LLMProviderRegistry) Get(name string) (*LLMProviderConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, exists := r.providers[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrLLMProviderNotFound, name)
	}
	return provider, nil
}

// GetAll returns all LLM provider configurations (thread-safe, returns copy)
func (r *LLMProviderRegistry) GetAll() map[string]*LLMProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]*LLMProviderConfig, len(r.providers))
	for k, v := range r.providers {
		result[k] = v
	}
	return result
}

// Has checks if an LLM provider exists in the registry (thread-safe)
func (r *LLMProviderRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.providers[name]
	return exists
}

// Len returns the number of LLM providers in the registry (thread-safe)
func (r *LLMProviderRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers)
}
