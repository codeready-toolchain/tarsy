// Package models contains request/response models and business domain types.
package models

import "github.com/codeready-toolchain/tarsy/ent"

// CreateChatRequest contains fields for creating a chat
type CreateChatRequest struct {
	SessionID string `json:"session_id"`
	CreatedBy string `json:"created_by"`
}

// AddChatMessageRequest contains fields for adding a chat message
type AddChatMessageRequest struct {
	ChatID  string `json:"chat_id"`
	Content string `json:"content"`
	Author  string `json:"author"`
}

// CreateChatStageRequest contains fields for creating a chat response stage
type CreateChatStageRequest struct {
	ChatID            string `json:"chat_id"`
	ChatUserMessageID string `json:"chat_user_message_id"`
	SessionID         string `json:"session_id"`
	StageIndex        int    `json:"stage_index"`
	AgentName         string `json:"agent_name"`
}

// ChatResponse wraps a Chat with optional loaded edges
type ChatResponse struct {
	*ent.Chat
}
