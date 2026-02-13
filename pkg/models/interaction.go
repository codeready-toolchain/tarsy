package models

// CreateLLMInteractionRequest contains fields for creating an LLM interaction
type CreateLLMInteractionRequest struct {
	SessionID        string         `json:"session_id"`
	StageID          *string        `json:"stage_id,omitempty"`     // nil for session-level interactions
	ExecutionID      *string        `json:"execution_id,omitempty"` // nil for session-level interactions
	InteractionType  string         `json:"interaction_type"`       // "iteration", "final_analysis", "executive_summary", "chat_response"
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

// ────────────────────────────────────────────────────────────
// Debug List (Level 1) — GET /api/v1/sessions/:id/debug
// ────────────────────────────────────────────────────────────

// DebugListResponse is the top-level response for GET /debug.
type DebugListResponse struct {
	Stages              []DebugStageGroup        `json:"stages"`
	SessionInteractions []LLMInteractionListItem `json:"session_interactions"` // Session-level LLM calls (e.g. executive summary)
}

// DebugStageGroup contains executions for one pipeline stage.
type DebugStageGroup struct {
	StageID    string                `json:"stage_id"`
	StageName  string                `json:"stage_name"`
	Executions []DebugExecutionGroup `json:"executions"`
}

// DebugExecutionGroup contains interactions for one agent execution.
type DebugExecutionGroup struct {
	ExecutionID     string                   `json:"execution_id"`
	AgentName       string                   `json:"agent_name"`
	LLMInteractions []LLMInteractionListItem `json:"llm_interactions"`
	MCPInteractions []MCPInteractionListItem `json:"mcp_interactions"`
}

// LLMInteractionListItem contains metadata for collapsed list view.
type LLMInteractionListItem struct {
	ID              string  `json:"id"`
	InteractionType string  `json:"interaction_type"`
	ModelName       string  `json:"model_name"`
	InputTokens     *int    `json:"input_tokens,omitempty"`
	OutputTokens    *int    `json:"output_tokens,omitempty"`
	TotalTokens     *int    `json:"total_tokens,omitempty"`
	DurationMs      *int    `json:"duration_ms,omitempty"`
	ErrorMessage    *string `json:"error_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// MCPInteractionListItem contains metadata for collapsed list view.
type MCPInteractionListItem struct {
	ID              string  `json:"id"`
	InteractionType string  `json:"interaction_type"`
	ServerName      string  `json:"server_name"`
	ToolName        *string `json:"tool_name,omitempty"`
	DurationMs      *int    `json:"duration_ms,omitempty"`
	ErrorMessage    *string `json:"error_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

// ────────────────────────────────────────────────────────────
// LLM Detail (Level 2) — GET /api/v1/sessions/:id/debug/llm/:interaction_id
// ────────────────────────────────────────────────────────────

// LLMInteractionDetailResponse is returned by GET /debug/llm/:interaction_id.
type LLMInteractionDetailResponse struct {
	ID               string                `json:"id"`
	InteractionType  string                `json:"interaction_type"`
	ModelName        string                `json:"model_name"`
	ThinkingContent  *string               `json:"thinking_content,omitempty"`
	InputTokens      *int                  `json:"input_tokens,omitempty"`
	OutputTokens     *int                  `json:"output_tokens,omitempty"`
	TotalTokens      *int                  `json:"total_tokens,omitempty"`
	DurationMs       *int                  `json:"duration_ms,omitempty"`
	ErrorMessage     *string               `json:"error_message,omitempty"`
	LLMRequest       map[string]any        `json:"llm_request"`
	LLMResponse      map[string]any        `json:"llm_response"`
	ResponseMetadata map[string]any        `json:"response_metadata,omitempty"`
	CreatedAt        string                `json:"created_at"`
	Conversation     []ConversationMessage `json:"conversation"`
}

// ConversationMessage is a single message in the reconstructed conversation.
type ConversationMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []MessageToolCall `json:"tool_calls,omitempty"`
	ToolCallID *string           `json:"tool_call_id,omitempty"`
	ToolName   *string           `json:"tool_name,omitempty"`
}

// MessageToolCall mirrors ent/schema.MessageToolCall for API responses.
type MessageToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ────────────────────────────────────────────────────────────
// MCP Detail (Level 2) — GET /api/v1/sessions/:id/debug/mcp/:interaction_id
// ────────────────────────────────────────────────────────────

// MCPInteractionDetailResponse is returned by GET /debug/mcp/:interaction_id.
type MCPInteractionDetailResponse struct {
	ID              string         `json:"id"`
	InteractionType string         `json:"interaction_type"`
	ServerName      string         `json:"server_name"`
	ToolName        *string        `json:"tool_name,omitempty"`
	ToolArguments   map[string]any `json:"tool_arguments,omitempty"`
	ToolResult      map[string]any `json:"tool_result,omitempty"`
	AvailableTools  []any          `json:"available_tools,omitempty"`
	DurationMs      *int           `json:"duration_ms,omitempty"`
	ErrorMessage    *string        `json:"error_message,omitempty"`
	CreatedAt       string         `json:"created_at"`
}
