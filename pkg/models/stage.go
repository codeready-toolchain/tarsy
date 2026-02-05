package models

import "github.com/codeready-toolchain/tarsy/ent"

// CreateStageRequest contains fields for creating a new stage
type CreateStageRequest struct {
	SessionID          string  `json:"session_id"`
	StageName          string  `json:"stage_name"`
	StageIndex         int     `json:"stage_index"`
	ExpectedAgentCount int     `json:"expected_agent_count"`
	ParallelType       *string `json:"parallel_type,omitempty"` // "multi_agent" or "replica"
	SuccessPolicy      *string `json:"success_policy,omitempty"` // "all" or "any"
	ChatID             *string `json:"chat_id,omitempty"`
	ChatUserMessageID  *string `json:"chat_user_message_id,omitempty"`
}

// CreateAgentExecutionRequest contains fields for creating a new agent execution
type CreateAgentExecutionRequest struct {
	StageID           string `json:"stage_id"`
	SessionID         string `json:"session_id"`
	AgentName         string `json:"agent_name"`
	AgentIndex        int    `json:"agent_index"`
	IterationStrategy string `json:"iteration_strategy"`
}

// UpdateAgentStatusRequest contains fields for updating agent execution status
type UpdateAgentStatusRequest struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// StageResponse wraps a Stage with optional loaded edges
type StageResponse struct {
	*ent.Stage
}

// AgentExecutionResponse wraps an AgentExecution with optional loaded edges
type AgentExecutionResponse struct {
	*ent.AgentExecution
}
