package config

import (
	"errors"
	"fmt"
)

var (
	// ErrConfigNotFound indicates configuration file was not found
	ErrConfigNotFound = errors.New("configuration file not found")

	// ErrInvalidYAML indicates YAML parsing failed
	ErrInvalidYAML = errors.New("invalid YAML syntax")

	// ErrValidationFailed indicates configuration validation failed
	ErrValidationFailed = errors.New("configuration validation failed")

	// ErrAgentNotFound indicates agent was not found in registry
	ErrAgentNotFound = errors.New("agent not found")

	// ErrChainNotFound indicates chain was not found in registry
	ErrChainNotFound = errors.New("chain not found")

	// ErrMCPServerNotFound indicates MCP server was not found in registry
	ErrMCPServerNotFound = errors.New("MCP server not found")

	// ErrLLMProviderNotFound indicates LLM provider was not found in registry
	ErrLLMProviderNotFound = errors.New("LLM provider not found")

	// ErrInvalidReference indicates an invalid cross-reference in configuration
	ErrInvalidReference = errors.New("invalid configuration reference")

	// ErrMissingRequiredField indicates a required field is missing
	ErrMissingRequiredField = errors.New("missing required field")

	// ErrInvalidValue indicates a field has an invalid value
	ErrInvalidValue = errors.New("invalid field value")
)

// ValidationError wraps configuration validation errors with context
type ValidationError struct {
	Component string // Component being validated (agent, chain, mcp_server, llm_provider)
	ID        string // ID of the component
	Field     string // Field name (optional)
	Err       error  // Underlying error
}

// Error returns formatted error message
func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s '%s': field '%s': %v", e.Component, e.ID, e.Field, e.Err)
	}
	return fmt.Sprintf("%s '%s': %v", e.Component, e.ID, e.Err)
}

// Unwrap returns the underlying error
func (e *ValidationError) Unwrap() error {
	return e.Err
}

// NewValidationError creates a new validation error
func NewValidationError(component, id, field string, err error) *ValidationError {
	return &ValidationError{
		Component: component,
		ID:        id,
		Field:     field,
		Err:       err,
	}
}

// LoadError wraps configuration loading errors with file context
type LoadError struct {
	File string // Configuration file being loaded
	Err  error  // Underlying error
}

// Error returns formatted error message
func (e *LoadError) Error() string {
	return fmt.Sprintf("failed to load %s: %v", e.File, e.Err)
}

// Unwrap returns the underlying error
func (e *LoadError) Unwrap() error {
	return e.Err
}

// NewLoadError creates a new load error
func NewLoadError(file string, err error) *LoadError {
	return &LoadError{
		File: file,
		Err:  err,
	}
}
