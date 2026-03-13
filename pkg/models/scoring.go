package models

import "time"

// SessionScoreResponse is the HTTP response for GET /sessions/:id/score.
type SessionScoreResponse struct {
	ScoreID               string     `json:"score_id"`
	TotalScore            *int       `json:"total_score"`
	ScoreAnalysis         *string    `json:"score_analysis"`
	ToolImprovementReport *string    `json:"tool_improvement_report"`
	FailureTags           []string   `json:"failure_tags,omitempty"`
	PromptHash            *string    `json:"prompt_hash"`
	ScoreTriggeredBy      string     `json:"score_triggered_by"`
	Status                string     `json:"status"`
	StageID               *string    `json:"stage_id"`
	StartedAt             time.Time  `json:"started_at"`
	CompletedAt           *time.Time `json:"completed_at"`
	ErrorMessage          *string    `json:"error_message"`
}
