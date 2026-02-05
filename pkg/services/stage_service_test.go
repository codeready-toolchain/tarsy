package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStageService_CreateStage(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := NewSessionService(client.Client)
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
	sessionService := NewSessionService(client.Client)
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
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "KubernetesAgent",
			AgentIndex:        1,
			IterationStrategy: "react",
		}

		exec, err := stageService.CreateAgentExecution(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, req.AgentName, exec.AgentName)
		assert.Equal(t, req.AgentIndex, exec.AgentIndex)
		assert.Equal(t, agentexecution.StatusPending, exec.Status)
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

func TestStageService_UpdateAgentStatus(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := NewSessionService(client.Client)
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
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         "TestAgent",
		AgentIndex:        1,
		IterationStrategy: "react",
	}
	exec, err := stageService.CreateAgentExecution(ctx, execReq)
	require.NoError(t, err)

	t.Run("updates status successfully", func(t *testing.T) {
		err := stageService.UpdateAgentStatus(ctx, exec.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)

		updated, err := stageService.GetAgentExecutionByID(ctx, exec.ID)
		require.NoError(t, err)
		assert.Equal(t, agentexecution.StatusActive, updated.Status)
		assert.NotNil(t, updated.StartedAt)
	})

	t.Run("sets completed_at for terminal states", func(t *testing.T) {
		err := stageService.UpdateAgentStatus(ctx, exec.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)

		updated, err := stageService.GetAgentExecutionByID(ctx, exec.ID)
		require.NoError(t, err)
		assert.Equal(t, agentexecution.StatusCompleted, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
		assert.NotNil(t, updated.DurationMs)
	})

	t.Run("returns ErrNotFound for missing execution", func(t *testing.T) {
		err := stageService.UpdateAgentStatus(ctx, "nonexistent", agentexecution.StatusCompleted, "")
		require.Error(t, err)
		assert.Equal(t, ErrNotFound, err)
	})
}

func TestStageService_AggregateStageStatus(t *testing.T) {
	t.Run("success_policy=all - all agents must complete", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := NewSessionService(client.Client)
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
				StageID:           stg.ID,
				SessionID:         session.ID,
				AgentName:         "TestAgent",
				AgentIndex:        i,
				IterationStrategy: "react",
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete all agents
		for _, exec := range executions {
			err = stageService.UpdateAgentStatus(ctx, exec.ID, agentexecution.StatusActive, "")
			require.NoError(t, err)
			err = stageService.UpdateAgentStatus(ctx, exec.ID, agentexecution.StatusCompleted, "")
			require.NoError(t, err)
		}

		// Aggregate should set stage to completed
		err = stageService.AggregateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusCompleted, updated.Status)
		assert.NotNil(t, updated.CompletedAt)
	})

	t.Run("success_policy=all - one agent fails", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := NewSessionService(client.Client)
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
				StageID:           stg.ID,
				SessionID:         session.ID,
				AgentName:         "TestAgent",
				AgentIndex:        i,
				IterationStrategy: "react",
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete 2, fail 1
		err = stageService.UpdateAgentStatus(ctx, executions[0].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[0].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[1].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[1].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[2].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[2].ID, agentexecution.StatusFailed, "test error")
		require.NoError(t, err)

		// Aggregate should set stage to failed
		err = stageService.AggregateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusFailed, updated.Status)
		assert.NotNil(t, updated.ErrorMessage)
	})

	t.Run("success_policy=any - at least one succeeds", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := NewSessionService(client.Client)
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
				StageID:           stg.ID,
				SessionID:         session.ID,
				AgentName:         "TestAgent",
				AgentIndex:        i,
				IterationStrategy: "react",
			})
			require.NoError(t, err)
			executions = append(executions, exec)
		}

		// Complete 1, fail 2
		err = stageService.UpdateAgentStatus(ctx, executions[0].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[0].ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[1].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[1].ID, agentexecution.StatusFailed, "error")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[2].ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, executions[2].ID, agentexecution.StatusFailed, "error")
		require.NoError(t, err)

		// Aggregate should set stage to completed (one succeeded)
		err = stageService.AggregateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusCompleted, updated.Status)
	})

	t.Run("stage remains active while agents are pending/active", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		stageService := NewStageService(client.Client)
		sessionService := NewSessionService(client.Client)
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
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "Agent1",
			AgentIndex:        1,
			IterationStrategy: "react",
		})
		require.NoError(t, err)

		exec2, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:           stg.ID,
			SessionID:         session.ID,
			AgentName:         "Agent2",
			AgentIndex:        2,
			IterationStrategy: "react",
		})
		require.NoError(t, err)

		// Complete one, leave one active
		err = stageService.UpdateAgentStatus(ctx, exec1.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, exec1.ID, agentexecution.StatusCompleted, "")
		require.NoError(t, err)
		err = stageService.UpdateAgentStatus(ctx, exec2.ID, agentexecution.StatusActive, "")
		require.NoError(t, err)

		// Stage should remain active
		err = stageService.AggregateStageStatus(ctx, stg.ID)
		require.NoError(t, err)

		updated, err := stageService.GetStageByID(ctx, stg.ID, false)
		require.NoError(t, err)
		assert.Equal(t, stage.StatusActive, updated.Status)
	})
}

func TestStageService_GetStagesBySession(t *testing.T) {
	client := testdb.NewTestClient(t)
	stageService := NewStageService(client.Client)
	sessionService := NewSessionService(client.Client)
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
