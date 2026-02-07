package models

import (
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/message"
)

// CreateMessageRequest contains fields for creating a message
type CreateMessageRequest struct {
	SessionID      string       `json:"session_id"`
	StageID        string       `json:"stage_id"`
	ExecutionID    string       `json:"execution_id"`
	SequenceNumber int          `json:"sequence_number"`
	Role           message.Role `json:"role"`
	Content        string       `json:"content"`
	ToolCalls      []ToolCallData `json:"tool_calls,omitempty"`   // For assistant messages
	ToolCallID     string         `json:"tool_call_id,omitempty"` // For tool messages
	ToolName       string         `json:"tool_name,omitempty"`    // For tool messages
}

// ToolCallData represents a tool call in an assistant message.
type ToolCallData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// MessageResponse wraps a Message
type MessageResponse struct {
	*ent.Message
}
