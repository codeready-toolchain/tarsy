package models

// MCPServerSelection represents a selected MCP server with optional tool filtering
type MCPServerSelection struct {
	Name  string   `json:"name"`            // MCP server ID
	Tools []string `json:"tools,omitempty"` // Specific tools, empty = all tools
}

// NativeToolsConfig configures native LLM provider tools
type NativeToolsConfig struct {
	GoogleSearch  *bool `json:"google_search,omitempty"`   // nil = provider default
	CodeExecution *bool `json:"code_execution,omitempty"`  // nil = provider default
	URLContext    *bool `json:"url_context,omitempty"`     // nil = provider default
}

// MCPSelectionConfig is the per-alert MCP override configuration
type MCPSelectionConfig struct {
	Servers     []MCPServerSelection `json:"servers"`
	NativeTools *NativeToolsConfig   `json:"native_tools,omitempty"`
}
