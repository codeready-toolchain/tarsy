package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// StageService manages stage and agent execution lifecycle
type StageService struct {
	client *ent.Client
}

// NewStageService creates a new StageService
func NewStageService(client *ent.Client) *StageService {
	return &StageService{client: client}
}

// CreateStage creates a new stage
func (s *StageService) CreateStage(httpCtx context.Context, req models.CreateStageRequest) (*ent.Stage, error) {
	// Validate input
	if req.SessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}
	if req.StageName == "" {
		return nil, NewValidationError("stage_name", "required")
	}
	if req.ExpectedAgentCount <= 0 {
		return nil, NewValidationError("expected_agent_count", "must be positive")
	}
	if req.SuccessPolicy != nil {
		policy := *req.SuccessPolicy
		if policy != "all" && policy != "any" {
			return nil, NewValidationError("success_policy", "invalid: must be 'all' or 'any'")
		}
	}
	if req.ParallelType != nil {
		parallelType := *req.ParallelType
		if parallelType != "multi_agent" && parallelType != "replica" {
			return nil, NewValidationError("parallel_type", "invalid: must be 'multi_agent' or 'replica'")
		}
	}

	// Use timeout context derived from incoming context
	ctx, cancel := context.WithTimeout(httpCtx, 10*time.Second)
	defer cancel()

	stageID := uuid.New().String()
	builder := s.client.Stage.Create().
		SetID(stageID).
		SetSessionID(req.SessionID).
		SetStageName(req.StageName).
		SetStageIndex(req.StageIndex).
		SetExpectedAgentCount(req.ExpectedAgentCount).
		SetStatus(stage.StatusPending)

	if req.ParallelType != nil {
		builder.SetParallelType(stage.ParallelType(*req.ParallelType))
	}
	if req.SuccessPolicy != nil {
		builder.SetSuccessPolicy(stage.SuccessPolicy(*req.SuccessPolicy))
	}
	if req.ChatID != nil {
		builder.SetChatID(*req.ChatID)
	}
	if req.ChatUserMessageID != nil {
		builder.SetChatUserMessageID(*req.ChatUserMessageID)
	}

	stg, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create stage: %w", err)
	}

	return stg, nil
}

// CreateAgentExecution creates a new agent execution
func (s *StageService) CreateAgentExecution(httpCtx context.Context, req models.CreateAgentExecutionRequest) (*ent.AgentExecution, error) {
	// Validate input
	if req.StageID == "" {
		return nil, NewValidationError("stage_id", "required")
	}
	if req.SessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}
	if req.AgentName == "" {
		return nil, NewValidationError("agent_name", "required")
	}
	if req.AgentIndex <= 0 {
		return nil, NewValidationError("agent_index", "must be positive")
	}

	// Use timeout context derived from incoming context
	ctx, cancel := context.WithTimeout(httpCtx, 10*time.Second)
	defer cancel()

	executionID := uuid.New().String()
	builder := s.client.AgentExecution.Create().
		SetID(executionID).
		SetStageID(req.StageID).
		SetSessionID(req.SessionID).
		SetAgentName(req.AgentName).
		SetAgentIndex(req.AgentIndex).
		SetStatus(agentexecution.StatusPending).
		SetIterationStrategy(string(req.IterationStrategy))
	if req.LLMProvider != "" {
		builder.SetLlmProvider(req.LLMProvider)
	}
	execution, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent execution: %w", err)
	}

	return execution, nil
}

// UpdateAgentExecutionStatus updates an agent execution's status
func (s *StageService) UpdateAgentExecutionStatus(ctx context.Context, executionID string, status agentexecution.Status, errorMsg string) error {
	// Use timeout context derived from incoming context
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Fetch the execution first to check current state
	exec, err := s.client.AgentExecution.Get(writeCtx, executionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to get agent execution: %w", err)
	}

	update := s.client.AgentExecution.UpdateOneID(executionID).
		SetStatus(status)

	if status == agentexecution.StatusActive && exec.StartedAt == nil {
		update = update.SetStartedAt(time.Now())
	}

	if status == agentexecution.StatusCompleted ||
		status == agentexecution.StatusFailed ||
		status == agentexecution.StatusCancelled ||
		status == agentexecution.StatusTimedOut {
		now := time.Now()
		update = update.SetCompletedAt(now)

		// Calculate duration if started_at exists
		if exec.StartedAt != nil {
			durationMs := int(now.Sub(*exec.StartedAt).Milliseconds())
			update = update.SetDurationMs(durationMs)
		}
	}

	if errorMsg != "" {
		update = update.SetErrorMessage(errorMsg)
	}

	err = update.Exec(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to update agent status: %w", err)
	}

	return nil
}

// UpdateStageStatus aggregates stage status from all agent executions
func (s *StageService) UpdateStageStatus(ctx context.Context, stageID string) error {
	// Use timeout context derived from incoming context
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Get stage with agent executions
	stg, err := s.client.Stage.Query().
		Where(stage.IDEQ(stageID)).
		WithAgentExecutions().
		Only(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to get stage: %w", err)
	}

	// Guard: if no agent executions exist, don't finalize
	if len(stg.Edges.AgentExecutions) == 0 {
		return nil
	}

	// Check if any agent is still pending or active
	hasActive := false
	hasPending := false
	for _, exec := range stg.Edges.AgentExecutions {
		if exec.Status == agentexecution.StatusPending {
			hasPending = true
		}
		if exec.Status == agentexecution.StatusActive {
			hasActive = true
		}
	}

	// Stage remains active if any agent is pending or active
	if hasPending || hasActive {
		// Ensure stage is active if any agent is working
		if hasActive && stg.Status != stage.StatusActive {
			return s.client.Stage.UpdateOneID(stageID).
				SetStatus(stage.StatusActive).
				SetStartedAt(time.Now()).
				Exec(writeCtx)
		}
		return nil
	}

	// All agents terminated - determine final stage status
	allCompleted := true
	allTimedOut := true
	allCancelled := true
	anyCompleted := false

	for _, exec := range stg.Edges.AgentExecutions {
		if exec.Status == agentexecution.StatusCompleted {
			anyCompleted = true
		} else {
			allCompleted = false
		}
		if exec.Status != agentexecution.StatusTimedOut {
			allTimedOut = false
		}
		if exec.Status != agentexecution.StatusCancelled {
			allCancelled = false
		}
	}

	// Determine final status based on success policy.
	// Resolve nil to "any" (default policy — matches old TARSy behavior).
	policy := stage.SuccessPolicyAny
	if stg.SuccessPolicy != nil {
		policy = *stg.SuccessPolicy
	}

	var finalStatus stage.Status
	var errorMessage string

	if policy == stage.SuccessPolicyAll {
		// All agents must succeed
		if allCompleted {
			finalStatus = stage.StatusCompleted
		} else if allTimedOut {
			finalStatus = stage.StatusTimedOut
			errorMessage = "all agents timed out"
		} else if allCancelled {
			finalStatus = stage.StatusCancelled
			errorMessage = "all agents cancelled"
		} else {
			finalStatus = stage.StatusFailed
			errorMessage = "one or more agents failed"
		}
	} else {
		// At least one agent must succeed (default: policy=any)
		if anyCompleted {
			finalStatus = stage.StatusCompleted
		} else if allTimedOut {
			finalStatus = stage.StatusTimedOut
			errorMessage = "all agents timed out"
		} else if allCancelled {
			finalStatus = stage.StatusCancelled
			errorMessage = "all agents cancelled"
		} else {
			finalStatus = stage.StatusFailed
			errorMessage = "all agents failed"
		}
	}

	// Update stage
	now := time.Now()
	update := s.client.Stage.UpdateOneID(stageID).
		SetStatus(finalStatus).
		SetCompletedAt(now)

	if stg.StartedAt != nil {
		durationMs := int(now.Sub(*stg.StartedAt).Milliseconds())
		update = update.SetDurationMs(durationMs)
	}
	if errorMessage != "" {
		update = update.SetErrorMessage(errorMessage)
	}

	return update.Exec(writeCtx)
}

// ForceStageFailure directly sets a stage to terminal failed state.
// Used as a last-resort fallback when no AgentExecution record exists
// (e.g. config resolution failed before execution could be created)
// and the execution-derived UpdateStageStatus would be a no-op.
func (s *StageService) ForceStageFailure(ctx context.Context, stageID string, errMsg string) error {
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := time.Now()
	update := s.client.Stage.UpdateOneID(stageID).
		SetStatus(stage.StatusFailed).
		SetCompletedAt(now).
		SetErrorMessage(errMsg)

	if err := update.Exec(writeCtx); err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to force stage failure: %w", err)
	}
	return nil
}

// GetStageByID retrieves a stage by ID with optional edges
func (s *StageService) GetStageByID(ctx context.Context, stageID string, withEdges bool) (*ent.Stage, error) {
	query := s.client.Stage.Query().Where(stage.IDEQ(stageID))

	if withEdges {
		query = query.WithAgentExecutions()
	}

	stg, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get stage: %w", err)
	}

	return stg, nil
}

// GetStagesBySession retrieves all stages for a session
func (s *StageService) GetStagesBySession(ctx context.Context, sessionID string, withEdges bool) ([]*ent.Stage, error) {
	query := s.client.Stage.Query().
		Where(stage.SessionIDEQ(sessionID)).
		Order(ent.Asc(stage.FieldStageIndex))

	if withEdges {
		query = query.WithAgentExecutions()
	}

	stages, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stages: %w", err)
	}

	return stages, nil
}

// GetAgentExecutions retrieves all agent executions for a stage
func (s *StageService) GetAgentExecutions(ctx context.Context, stageID string) ([]*ent.AgentExecution, error) {
	executions, err := s.client.AgentExecution.Query().
		Where(agentexecution.StageIDEQ(stageID)).
		Order(ent.Asc(agentexecution.FieldAgentIndex)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent executions: %w", err)
	}

	return executions, nil
}

// GetAgentExecutionByID retrieves an agent execution by ID
func (s *StageService) GetAgentExecutionByID(ctx context.Context, executionID string) (*ent.AgentExecution, error) {
	execution, err := s.client.AgentExecution.Get(ctx, executionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get agent execution: %w", err)
	}

	return execution, nil
}

// GetMaxStageIndex returns the highest stage_index for a session.
// Returns 0 if the session has no stages.
func (s *StageService) GetMaxStageIndex(ctx context.Context, sessionID string) (int, error) {
	if sessionID == "" {
		return 0, NewValidationError("session_id", "required")
	}

	// Query stages ordered by index descending, take first
	stg, err := s.client.Stage.Query().
		Where(stage.SessionIDEQ(sessionID)).
		Order(ent.Desc(stage.FieldStageIndex)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get max stage index: %w", err)
	}

	return stg.StageIndex, nil
}

// GetActiveStageForChat returns any pending or active stage for the given chat.
// Returns nil (not an error) if no active stage exists.
func (s *StageService) GetActiveStageForChat(ctx context.Context, chatID string) (*ent.Stage, error) {
	if chatID == "" {
		return nil, NewValidationError("chat_id", "required")
	}

	stg, err := s.client.Stage.Query().
		Where(
			stage.ChatIDEQ(chatID),
			stage.StatusIn(stage.StatusPending, stage.StatusActive),
		).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // no active stage — not an error
		}
		return nil, fmt.Errorf("failed to query active chat stage: %w", err)
	}

	return stg, nil
}
