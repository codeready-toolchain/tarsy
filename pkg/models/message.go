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
}

// MessageResponse wraps a Message
type MessageResponse struct {
	*ent.Message
}
