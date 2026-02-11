package models

import (
	"encoding/json"
	"fmt"
)

// MCPServerSelection represents a selected MCP server with optional tool filtering
type MCPServerSelection struct {
	Name  string   `json:"name"`            // MCP server ID
	Tools []string `json:"tools,omitempty"` // Specific tools, empty = all tools
}

// NativeToolsConfig configures native LLM provider tools
type NativeToolsConfig struct {
	GoogleSearch  *bool `json:"google_search,omitempty"`  // nil = provider default
	CodeExecution *bool `json:"code_execution,omitempty"` // nil = provider default
	URLContext    *bool `json:"url_context,omitempty"`    // nil = provider default
}

// MCPSelectionConfig is the per-alert MCP override configuration
type MCPSelectionConfig struct {
	Servers     []MCPServerSelection `json:"servers"`
	NativeTools *NativeToolsConfig   `json:"native_tools,omitempty"`
}

// ParseMCPSelectionConfig deserializes a JSON map (from ent storage) into MCPSelectionConfig.
// Returns nil with no error if the raw map is nil or empty (no override).
func ParseMCPSelectionConfig(raw map[string]interface{}) (*MCPSelectionConfig, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MCP selection: %w", err)
	}
	var cfg MCPSelectionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal MCP selection: %w", err)
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("MCP selection must have at least one server")
	}
	return &cfg, nil
}
