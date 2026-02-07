package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockControllerFactory creates a controller that returns a preset result.
type mockControllerFactory struct {
	err error
}

func (m *mockControllerFactory) CreateController(strategy config.IterationStrategy, execCtx *ExecutionContext) (Controller, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &mockController{}, nil
}

type mockController struct{}

func (m *mockController) Run(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error) {
	return &ExecutionResult{Status: ExecutionStatusCompleted, FinalAnalysis: "mock"}, nil
}

func TestAgentFactory_CreateAgent(t *testing.T) {
	t.Run("creates agent successfully", func(t *testing.T) {
		factory := NewAgentFactory(&mockControllerFactory{})
		execCtx := &ExecutionContext{
			Config: &ResolvedAgentConfig{
				IterationStrategy: config.IterationStrategyReact,
			},
		}

		agent, err := factory.CreateAgent(execCtx)
		require.NoError(t, err)
		assert.NotNil(t, agent)
	})

	t.Run("returns error on controller creation failure", func(t *testing.T) {
		factory := NewAgentFactory(&mockControllerFactory{err: errors.New("unsupported")})
		execCtx := &ExecutionContext{
			Config: &ResolvedAgentConfig{
				IterationStrategy: config.IterationStrategy("invalid"),
			},
		}

		_, err := factory.CreateAgent(execCtx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported")
	})
}
