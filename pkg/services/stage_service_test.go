package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStageService_CreateStage(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Create session first
	sessionReq := models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	}
	session, err := sessionService.CreateSession(ctx, sessionReq)
	require.NoError(t, err)

	t.Run("creates stage successfully", func(t *testing.T) {
		parallelType := "multi_agent"
		successPolicy := "all"
		req := models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Deep Dive",
			StageIndex:         1,
			ExpectedAgentCount: 3,
			ParallelType:       &parallelType,
			SuccessPolicy:      &successPolicy,
		}

		stg, err := stageService.CreateStage(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.StageName, stg.StageName)
		assert.Equal(t, req.StageIndex, stg.StageIndex)
		assert.Equal(t, req.ExpectedAgentCount, stg.ExpectedAgentCount)
		assert.Equal(t, stage.StatusPending, stg.Status)
		assert.Equal(t, stage.ParallelTypeMultiAgent, *stg.ParallelType)
		assert.Equal(t, stage.SuccessPolicyAll, *stg.SuccessPolicy)
	})

	t.Run("validates required fields", func(t *testing.T) {
		invalidParallelType := "invalid_type"
		tests := []struct {
			name    string
			req     models.CreateStageRequest
			wantErr string
		}{
			{
				name:    "missing session_id",
				req:     models.CreateStageRequest{StageName: "test", ExpectedAgentCount: 1},
				wantErr: "session_id",
			},
			{
				name:    "missing stage_name",
				req:     models.CreateStageRequest{SessionID: session.ID, ExpectedAgentCount: 1},
				wantErr: "stage_name",
			},
			{
				name:    "invalid expected_agent_count",
				req:     models.CreateStageRequest{SessionID: session.ID, StageName: "test", ExpectedAgentCount: 0},
				wantErr: "expected_agent_count",
			},
			{
				name: "invalid parallel_type",
				req: models.CreateStageRequest{
					SessionID:          session.ID,
					StageName:          "test",
					ExpectedAgentCount: 1,
					ParallelType:       &invalidParallelType,
				},
				wantErr: "parallel_type",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := stageService.CreateStage(ctx, tt.req)
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
			})
		}
	})

	t.Run("accepts valid parallel_type values", func(t *testing.T) {
		validTypes := []string{"multi_agent", "replica"}
		for _, pt := range validTypes {
			parallelType := pt
			req := models.CreateStageRequest{
				SessionID:          session.ID,
				StageName:          "test " + pt,
				StageIndex:         10 + len(pt), // Ensure unique index
				ExpectedAgentCount: 1,
				ParallelType:       &parallelType,
			}
			_, err := stageService.CreateStage(ctx, req)
			require.NoError(t, err)
		}
	})
}

func TestStageService_CreateAgentExecution(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Create session and stage
	sessionReq := models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	}
	session, err := sessionService.CreateSession(ctx, sessionReq)
	require.NoError(t, err)

	stageReq := models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test Stage",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	}
	stg, err := stageService.CreateStage(ctx, stageReq)
	require.NoError(t, err)

	t.Run("creates agent execution successfully", func(t *testing.T) {
		req := models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "KubernetesAgent",
			AgentIndex: 1,
			LLMBackend: config.LLMBackendLangChain,
		}

		exec, err := stageService.CreateAgentExecution(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.AgentName, exec.AgentName)
		assert.Equal(t, req.AgentIndex, exec.AgentIndex)
		assert.Equal(t, agentexecution.StatusPending, exec.Status)
		// LLMProvider omitted → should be nil
		assert.Nil(t, exec.LlmProvider)
	})

	t.Run("persists llm_provider when set", func(t *testing.T) {
		req := models.CreateAgentExecutionRequest{
			StageID:     stg.ID,
			SessionID:   session.ID,
			AgentName:   "GeminiAgent",
			AgentIndex:  2,
			LLMBackend:  config.LLMBackendNativeGemini,
			LLMProvider: "gemini-2.5-pro",
		}

		exec, err := stageService.CreateAgentExecution(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, exec.LlmProvider)
		assert.Equal(t, "gemini-2.5-pro", *exec.LlmProvider)

		// Round-trip: re-read from DB to confirm persistence
		reloaded, err := client.AgentExecution.Get(ctx, exec.ID)
		require.NoError(t, err)
		require.NotNil(t, reloaded.LlmProvider)
		assert.Equal(t, "gemini-2.5-pro", *reloaded.LlmProvider)
	})

	t.Run("validates required fields", func(t *testing.T) {
		tests := []struct {
			name    string
			req     models.CreateAgentExecutionRequest
			wantErr string
		}{
			{
				name: "missing stage_id",
				req: models.CreateAgentExecutionRequest{
					SessionID: session.ID, AgentName: "test", AgentIndex: 1,
				},
				wantErr: "stage_id",
			},
			{
				name: "missing agent_name",
				req: models.CreateAgentExecutionRequest{
					StageID: stg.ID, SessionID: session.ID, AgentIndex: 1,
				},
				wantErr: "agent_name",
			},
			{
				name: "invalid agent_index",
				req: models.CreateAgentExecutionRequest{
					StageID: stg.ID, SessionID: session.ID, AgentName: "test", AgentIndex: 0,
				},
				wantErr: "agent_index",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := stageService.CreateAgentExecution(ctx, tt.req)
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
			})
		}
	})
}

func TestStageService_UpdateAgentExecutionStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Setup
	sessionReq := models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	}
	session, err := sessionService.CreateSession(ctx, sessionReq)
	require.NoError(t, err)

	stageReq := models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	}
	stg, err := stageService.CreateStage(ctx, stageReq)
	require.NoError(t, err)

	execReq := models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "TestAgent",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	}
	exec, err := stageService.CreateAgentExecution(ctx, execReq)
	require.NoError(t, err)

	t.Run("updates status successfully", func(t *testing.T) {
		err := stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)

		updated, err := stageService.GetAgentExecutionByID(ctx, exec.ID)
		require.NoError(t, err)
		assert.Equal(t, agentexecution.StatusActive, updated.Status)
		assert.NotNil(t, updated.StartedAt)
	})

	t.Run("sets completed_at for terminal states", func(t *testing.T) {
		err := stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)

		updated, err := stageService.GetAgentExecutionByID(ctx, exec.ID)
		require.NoError(t, err)
		assert.Equal(t, agentexecution.StatusCompleted, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
		assert.NotNil(t, updated.DurationMs)
	})

	t.Run("returns ErrNotFound for missing execution", func(t *testing.T) {
		err := stageService.UpdateAgentExecutionStatus(ctx, "nonexistent", agentexecution.StatusCompleted, "")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestStageService_UpdateStageStatus(t *testing.T) {
	t.Run("success_policy=all - all agents must complete", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		// Setup
		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		successPolicy := "all"
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Parallel Stage",
			StageIndex:         1,
			ExpectedAgentCount: 3,
			SuccessPolicy:      &successPolicy,
		})
		require.NoError(t, err)

		// Create 3 agent executions
		var executions []*ent.AgentExecution
		for i := 1; i <= 3; i++ {
			exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
				StageID:    stg.ID,
				SessionID:  session.ID,
				AgentName:  "TestAgent",
				AgentIndex: i,
				LLMBackend: config.LLMBackendLangChain,
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete all agents
		for _, exec := range executions {
			err = stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, "")
			require.NoError(t, err)
			err = stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusCompleted, "")
			require.NoError(t, err)
		}

		// Aggregate should set stage to completed
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusCompleted, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
	})

	t.Run("success_policy=all - one agent fails", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		successPolicy := "all"
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Parallel Stage",
			StageIndex:         1,
			ExpectedAgentCount: 3,
			SuccessPolicy:      &successPolicy,
		})
		require.NoError(t, err)

		var executions []*ent.AgentExecution
		for i := 1; i <= 3; i++ {
			exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
				StageID:    stg.ID,
				SessionID:  session.ID,
				AgentName:  "TestAgent",
				AgentIndex: i,
				LLMBackend: config.LLMBackendLangChain,
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete 2, fail 1
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[0].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[0].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[1].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[1].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[2].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[2].ID, agentexecution.StatusFailed, "test error")
		require.NoError(t, err)

		// Aggregate should set stage to failed
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusFailed, updated.Status)
		assert.NotNil(t, updated.ErrorMessage)
	})

	t.Run("success_policy=any - at least one succeeds", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		successPolicy := "any"
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Parallel Stage",
			StageIndex:         1,
			ExpectedAgentCount: 3,
			SuccessPolicy:      &successPolicy,
		})
		require.NoError(t, err)

		var executions []*ent.AgentExecution
		for i := 1; i <= 3; i++ {
			exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
				StageID:    stg.ID,
				SessionID:  session.ID,
				AgentName:  "TestAgent",
				AgentIndex: i,
				LLMBackend: config.LLMBackendLangChain,
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete 1, fail 2
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[0].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[0].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[1].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[1].ID, agentexecution.StatusFailed, "error")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[2].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, executions[2].ID, agentexecution.StatusFailed, "error")
		require.NoError(t, err)

		// Aggregate should set stage to completed (one succeeded)
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusCompleted, updated.Status)
	})

	t.Run("nil policy defaults to any (one success → completed)", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		// Create stage with nil SuccessPolicy (no pointer)
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Nil Policy Stage",
			StageIndex:         1,
			ExpectedAgentCount: 2,
			// SuccessPolicy intentionally nil
		})
		require.NoError(t, err)

		// Create 2 agent executions
		exec1, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "Agent1",
			AgentIndex: 1,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "Agent2",
			AgentIndex: 2,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		// Complete 1, fail 1
		err = stageService.UpdateAgentExecutionStatus(ctx, exec1.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, exec1.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, exec2.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, exec2.ID, agentexecution.StatusFailed, "error")
		require.NoError(t, err)

		// nil policy should default to "any" → one success means stage completes
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusCompleted, updated.Status,
			"nil policy should default to 'any': one success = stage completed")
	})

	t.Run("stage remains active while agents are pending/active", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Test",
			StageIndex:         1,
			ExpectedAgentCount: 2,
		})
		require.NoError(t, err)

		exec1, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "Agent1",
			AgentIndex: 1,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "Agent2",
			AgentIndex: 2,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		// Complete one, leave one active
		err = stageService.UpdateAgentExecutionStatus(ctx, exec1.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, exec1.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentExecutionStatus(ctx, exec2.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)

		// Stage should remain active
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusActive, updated.Status)
	})

	t.Run("no-op when stage has zero agent executions", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := setupTestSessionService(t, client.Client)
		ctx := context.Background()

		session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
			SessionID: uuid.New().String(),
			AlertData: "test",
			AgentType: "kubernetes",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)

		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Empty Stage",
			StageIndex:         1,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)

		// Call UpdateStageStatus with no agent executions — should be a no-op
		err = stageService.UpdateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		// Stage should remain pending (not silently completed)
		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusPending, updated.Status)
	})
}

func TestStageService_ForceStageFailure(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	t.Run("marks stage as failed with error message", func(t *testing.T) {
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Config Resolution",
			StageIndex:         1,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)
		assert.Equal(t, stage.StatusPending, stg.Status)

		// Force stage to failed (no agent executions exist — this is the scenario)
		err = stageService.ForceStageFailure(ctx, stg.ID, "failed to resolve agent config: agent not found")
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusFailed, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
		require.NotNil(t, updated.ErrorMessage)
		assert.Contains(t, *updated.ErrorMessage, "failed to resolve agent config")
	})

	t.Run("works even when stage already has executions", func(t *testing.T) {
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "With Execution",
			StageIndex:         2,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)

		// Create a pending execution (simulates the fallback path where
		// CreateAgentExecution succeeded but UpdateAgentExecutionStatus might fail)
		_, err = stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "TestAgent",
			AgentIndex: 1,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)

		err = stageService.ForceStageFailure(ctx, stg.ID, "resolution error")
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusFailed, updated.Status)
	})
}

func TestStageService_GetStagesBySession(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	// Create multiple stages
	for i := 1; i <= 3; i++ {
		_, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Stage",
			StageIndex:         i,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)
	}

	t.Run("retrieves stages in order", func(t *testing.T) {
		stages, err := stageService.GetStagesBySession(ctx, session.ID, false)
		require.NoError(t, err)
		assert.Len(t, stages, 4) // 3 created + 1 initial from session creation

		// Verify ordering
		for i := 0; i < len(stages)-1; i++ {
			assert.Less(t, stages[i].StageIndex, stages[i+1].StageIndex)
		}
	})

	t.Run("loads edges when requested", func(t *testing.T) {
		stages, err := stageService.GetStagesBySession(ctx, session.ID, true)
		require.NoError(t, err)
		assert.NotNil(t, stages[0].Edges.AgentExecutions)
	})
}

func TestStageService_GetAgentExecutions(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	// Setup
	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 3,
	})
	require.NoError(t, err)

	// Create multiple agent executions
	var execIDs []string
	for i := 1; i <= 3; i++ {
		exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  fmt.Sprintf("TestAgent%d", i),
			AgentIndex: i,
			LLMBackend: config.LLMBackendLangChain,
		})
		require.NoError(t, err)
		execIDs = append(execIDs, exec.ID)
	}

	t.Run("retrieves all executions for stage ordered by index", func(t *testing.T) {
		executions, err := stageService.GetAgentExecutions(ctx, stg.ID)
		require.NoError(t, err)
		assert.Len(t, executions, 3)

		// Verify we got back the same executions we created
		retrievedIDs := make([]string, len(executions))
		for i, exec := range executions {
			retrievedIDs[i] = exec.ID
		}
		assert.ElementsMatch(t, execIDs, retrievedIDs)

		// Verify ordering by agent index
		for i := 0; i < len(executions)-1; i++ {
			assert.Less(t, executions[i].AgentIndex, executions[i+1].AgentIndex)
		}

		// Verify agent names match expected pattern
		for i, exec := range executions {
			expectedName := fmt.Sprintf("TestAgent%d", i+1)
			assert.Equal(t, expectedName, exec.AgentName)
		}
	})

	t.Run("returns empty list for stage with no executions", func(t *testing.T) {
		stg2, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "EmptyStage",
			StageIndex:         2,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)

		executions, err := stageService.GetAgentExecutions(ctx, stg2.ID)
		require.NoError(t, err)
		assert.Empty(t, executions)
	})
}

func TestStageService_GetMaxStageIndex(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	t.Run("returns 0 for session with only the initial stage", func(t *testing.T) {
		maxIndex, err := stageService.GetMaxStageIndex(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, maxIndex)
	})

	t.Run("returns highest stage index", func(t *testing.T) {
		_, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Stage 1",
			StageIndex:         1,
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)

		_, err = stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Stage 2",
			StageIndex:         3, // intentional gap
			ExpectedAgentCount: 1,
		})
		require.NoError(t, err)

		maxIndex, err := stageService.GetMaxStageIndex(ctx, session.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, maxIndex)
	})

	t.Run("validates session_id required", func(t *testing.T) {
		_, err := stageService.GetMaxStageIndex(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestStageService_GetActiveStageForChat(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	chatService := NewChatService(client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	chatObj, err := chatService.CreateChat(ctx, models.CreateChatRequest{
		SessionID: session.ID,
		CreatedBy: "test@example.com",
	})
	require.NoError(t, err)

	t.Run("returns nil when no stages exist", func(t *testing.T) {
		active, err := stageService.GetActiveStageForChat(ctx, chatObj.ID)
		require.NoError(t, err)
		assert.Nil(t, active)
	})

	// Hoisted so the "returns nil when stage is completed" subtest can reference it.
	var chatStgID string

	t.Run("returns active stage", func(t *testing.T) {
		chatID := chatObj.ID
		stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          session.ID,
			StageName:          "Chat Response",
			StageIndex:         1,
			ExpectedAgentCount: 1,
			ChatID:             &chatID,
		})
		require.NoError(t, err)
		chatStgID = stg.ID

		active, err := stageService.GetActiveStageForChat(ctx, chatObj.ID)
		require.NoError(t, err)
		require.NotNil(t, active)
		assert.Equal(t, stg.ID, active.ID)
	})

	t.Run("returns nil when stage is completed", func(t *testing.T) {
		require.NotEmpty(t, chatStgID, "expected chatStgID from previous subtest")
		// Complete the specific stage from the previous test
		err := client.Stage.UpdateOneID(chatStgID).
			SetStatus(stage.StatusCompleted).
			Exec(ctx)
		require.NoError(t, err)

		active, err := stageService.GetActiveStageForChat(ctx, chatObj.ID)
		require.NoError(t, err)
		assert.Nil(t, active)
	})

	t.Run("validates chat_id required", func(t *testing.T) {
		_, err := stageService.GetActiveStageForChat(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestStageService_CreateAgentExecution_SubAgent(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Orchestrator Stage",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	// Create the orchestrator execution (top-level, no parent).
	orchestrator, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "Orchestrator",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)
	assert.Nil(t, orchestrator.ParentExecutionID)
	assert.Nil(t, orchestrator.Task)

	t.Run("creates sub-agent with parent and task", func(t *testing.T) {
		task := "Find all 5xx errors in the last 30 minutes"
		req := models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "LogAnalyzer",
			AgentIndex:        1,
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		}

		sub, err := stageService.CreateAgentExecution(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, sub.ParentExecutionID)
		assert.Equal(t, orchestrator.ID, *sub.ParentExecutionID)
		require.NotNil(t, sub.Task)
		assert.Equal(t, task, *sub.Task)

		// Round-trip: re-read from DB.
		reloaded, err := client.AgentExecution.Get(ctx, sub.ID)
		require.NoError(t, err)
		require.NotNil(t, reloaded.ParentExecutionID)
		assert.Equal(t, orchestrator.ID, *reloaded.ParentExecutionID)
		require.NotNil(t, reloaded.Task)
		assert.Equal(t, task, *reloaded.Task)
	})

	t.Run("sub-agent index independent of parent index", func(t *testing.T) {
		// Both the orchestrator and the sub-agent above have the same stage_id.
		// The partial unique indexes allow them to have independent index spaces.
		task := "Check latency metrics"
		sub, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "MetricChecker",
			AgentIndex:        2,
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		})
		require.NoError(t, err)
		assert.Equal(t, 2, sub.AgentIndex)

		allExecs, err := stageService.GetAgentExecutions(ctx, stg.ID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(allExecs), 3) // orchestrator + at least 2 sub-agents
	})

	t.Run("rejects duplicate top-level agent index in same stage", func(t *testing.T) {
		_, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:    stg.ID,
			SessionID:  session.ID,
			AgentName:  "DuplicateTopLevel",
			AgentIndex: 1, // conflicts with the orchestrator created above
			LLMBackend: config.LLMBackendLangChain,
		})
		require.Error(t, err)
		assert.True(t, ent.IsConstraintError(err), "expected constraint error, got: %v", err)
	})

	t.Run("rejects duplicate sub-agent index under same parent", func(t *testing.T) {
		task := "Duplicate task"
		_, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "DuplicateSubAgent",
			AgentIndex:        1, // conflicts with first sub-agent created above
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		})
		require.Error(t, err)
		assert.True(t, ent.IsConstraintError(err), "expected constraint error, got: %v", err)
	})
}

func TestStageService_UpdateStageStatus_ExcludesSubAgents(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Orchestrator Stage",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	// Create orchestrator and complete it.
	orchestrator, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "Orchestrator",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)
	err = stageService.UpdateAgentExecutionStatus(ctx, orchestrator.ID, agentexecution.StatusActive, "")
	require.NoError(t, err)
	err = stageService.UpdateAgentExecutionStatus(ctx, orchestrator.ID, agentexecution.StatusCompleted, "")
	require.NoError(t, err)

	// Create a sub-agent and mark it failed.
	task := "Analyze logs"
	sub, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "LogAnalyzer",
		AgentIndex:        1,
		LLMBackend:        config.LLMBackendNativeGemini,
		ParentExecutionID: &orchestrator.ID,
		Task:              &task,
	})
	require.NoError(t, err)
	err = stageService.UpdateAgentExecutionStatus(ctx, sub.ID, agentexecution.StatusActive, "")
	require.NoError(t, err)
	err = stageService.UpdateAgentExecutionStatus(ctx, sub.ID, agentexecution.StatusFailed, "sub-agent error")
	require.NoError(t, err)

	// Stage status should be completed (based on orchestrator only, ignoring sub-agent).
	err = stageService.UpdateStageStatus(ctx, stg.ID)
	require.NoError(t, err)

	updated, err := stageService.GetStageByID(ctx, stg.ID, false)
	require.NoError(t, err)
	assert.Equal(t, stage.StatusCompleted, updated.Status,
		"stage should be completed: sub-agent failure must not affect stage status")
}

func TestStageService_GetSubAgentExecutions(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	orchestrator, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "Orchestrator",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	// Create 3 sub-agents.
	for i := 1; i <= 3; i++ {
		task := fmt.Sprintf("Task %d", i)
		_, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         fmt.Sprintf("SubAgent%d", i),
			AgentIndex:        i,
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		})
		require.NoError(t, err)
	}

	t.Run("returns sub-agents ordered by index", func(t *testing.T) {
		subs, err := stageService.GetSubAgentExecutions(ctx, orchestrator.ID)
		require.NoError(t, err)
		assert.Len(t, subs, 3)
		for i, sub := range subs {
			assert.Equal(t, i+1, sub.AgentIndex)
			assert.Equal(t, fmt.Sprintf("SubAgent%d", i+1), sub.AgentName)
		}
	})

	t.Run("returns empty for nonexistent parent", func(t *testing.T) {
		subs, err := stageService.GetSubAgentExecutions(ctx, "nonexistent-id")
		require.NoError(t, err)
		assert.Empty(t, subs)
	})

	t.Run("validates parent_execution_id required", func(t *testing.T) {
		_, err := stageService.GetSubAgentExecutions(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestStageService_GetExecutionTree(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	orchestrator, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "Orchestrator",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	// Create 2 sub-agents.
	for i := 1; i <= 2; i++ {
		task := fmt.Sprintf("Task %d", i)
		_, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         fmt.Sprintf("SubAgent%d", i),
			AgentIndex:        i,
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		})
		require.NoError(t, err)
	}

	t.Run("loads execution with sub-agents", func(t *testing.T) {
		tree, err := stageService.GetExecutionTree(ctx, orchestrator.ID)
		require.NoError(t, err)
		assert.Equal(t, orchestrator.ID, tree.ID)
		require.NotNil(t, tree.Edges.SubAgents)
		assert.Len(t, tree.Edges.SubAgents, 2)
		assert.Equal(t, "SubAgent1", tree.Edges.SubAgents[0].AgentName)
		assert.Equal(t, "SubAgent2", tree.Edges.SubAgents[1].AgentName)
	})

	t.Run("returns ErrNotFound for missing execution", func(t *testing.T) {
		_, err := stageService.GetExecutionTree(ctx, "nonexistent")
		assert.Equal(t, ErrNotFound, err)
	})

	t.Run("validates execution_id required", func(t *testing.T) {
		_, err := stageService.GetExecutionTree(ctx, "")
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
}

func TestStageService_SubAgentCascadeDelete(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "cascade test",
		AgentType: "kubernetes",
		ChainID:   "k8s-analysis",
	})
	require.NoError(t, err)

	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          "Test",
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	require.NoError(t, err)

	orchestrator, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:    stg.ID,
		SessionID:  session.ID,
		AgentName:  "Orchestrator",
		AgentIndex: 1,
		LLMBackend: config.LLMBackendLangChain,
	})
	require.NoError(t, err)

	var subIDs []string
	for i := 1; i <= 2; i++ {
		task := fmt.Sprintf("Task %d", i)
		sub, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         fmt.Sprintf("Sub%d", i),
			AgentIndex:        i,
			LLMBackend:        config.LLMBackendNativeGemini,
			ParentExecutionID: &orchestrator.ID,
			Task:              &task,
		})
		require.NoError(t, err)
		subIDs = append(subIDs, sub.ID)
	}

	// Delete the parent orchestrator.
	err = client.AgentExecution.DeleteOneID(orchestrator.ID).Exec(ctx)
	require.NoError(t, err)

	// Sub-agents should be cascade-deleted.
	for _, id := range subIDs {
		_, err := client.AgentExecution.Get(ctx, id)
		assert.True(t, ent.IsNotFound(err), "sub-agent %s should be cascade-deleted", id)
	}
}
