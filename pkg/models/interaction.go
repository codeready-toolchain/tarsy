package models

import "github.com/codeready-toolchain/tarsy/ent"

// CreateLLMInteractionRequest contains fields for creating an LLM interaction
type CreateLLMInteractionRequest struct {
	SessionID        string         `json:"session_id"`
	StageID          string         `json:"stage_id"`
	ExecutionID      string         `json:"execution_id"`
	InteractionType  string         `json:"interaction_type"` // "iteration", "final_analysis", "executive_summary", "chat_response"
	ModelName        string         `json:"model_name"`
	LastMessageID    *string        `json:"last_message_id,omitempty"`
	LLMRequest       map[string]any `json:"llm_request"`
	LLMResponse      map[string]any `json:"llm_response"`
	ThinkingContent  *string        `json:"thinking_content,omitempty"`
	ResponseMetadata map[string]any `json:"response_metadata,omitempty"`
	InputTokens      *int           `json:"input_tokens,omitempty"`
	OutputTokens     *int           `json:"output_tokens,omitempty"`
	TotalTokens      *int           `json:"total_tokens,omitempty"`
	DurationMs       *int           `json:"duration_ms,omitempty"`
	ErrorMessage     *string        `json:"error_message,omitempty"`
}

// CreateMCPInteractionRequest contains fields for creating an MCP interaction
type CreateMCPInteractionRequest struct {
	SessionID       string         `json:"session_id"`
	StageID         string         `json:"stage_id"`
	ExecutionID     string         `json:"execution_id"`
	InteractionType string         `json:"interaction_type"` // "tool_call", "tool_list"
	ServerName      string         `json:"server_name"`
	ToolName        *string        `json:"tool_name,omitempty"`
	ToolArguments   map[string]any `json:"tool_arguments,omitempty"`
	ToolResult      map[string]any `json:"tool_result,omitempty"`
	AvailableTools  map[string]any `json:"available_tools,omitempty"`
	DurationMs      *int           `json:"duration_ms,omitempty"`
	ErrorMessage    *string        `json:"error_message,omitempty"`
}

// LLMInteractionListItem contains metadata for collapsed list view
type LLMInteractionListItem struct {
	ID              string  `json:"id"`
	InteractionType string  `json:"interaction_type"`
	ModelName       string  `json:"model_name"`
	DurationMs      *int    `json:"duration_ms,omitempty"`
	ErrorMessage    *string `json:"error_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// MCPInteractionListItem contains metadata for collapsed list view
type MCPInteractionListItem struct {
	ID              string  `json:"id"`
	InteractionType string  `json:"interaction_type"`
	ServerName      string  `json:"server_name"`
	ToolName        *string `json:"tool_name,omitempty"`
	DurationMs      *int    `json:"duration_ms,omitempty"`
	ErrorMessage    *string `json:"error_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// LLMInteractionResponse wraps full LLM interaction details
type LLMInteractionResponse struct {
	*ent.LLMInteraction
}

// MCPInteractionResponse wraps full MCP interaction details
type MCPInteractionResponse struct {
	*ent.MCPInteraction
}

// ConversationResponse contains reconstructed conversation
type ConversationResponse struct {
	Messages []*ent.Message `json:"messages"`
}
