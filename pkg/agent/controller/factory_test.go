package controller

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFactory_CreateController(t *testing.T) {
	factory := NewFactory()

	// Minimal execution context for testing
	execCtx := &agent.ExecutionContext{
		SessionID:  "test-session",
		StageID:    "test-stage",
		AgentName:  "test-agent",
		AgentIndex: 1,
	}

	t.Run("unknown agent type returns error", func(t *testing.T) {
		controller, err := factory.CreateController(config.AgentType("invalid"), execCtx)
		require.Error(t, err)
		assert.Nil(t, controller)
		assert.Contains(t, err.Error(), "unknown agent type")
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("default agent type returns IteratingController", func(t *testing.T) {
		controller, err := factory.CreateController(config.AgentTypeDefault, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*IteratingController)
		assert.True(t, ok, "expected IteratingController")
	})

	t.Run("synthesis type returns SingleShotController", func(t *testing.T) {
		pb := prompt.NewPromptBuilder(config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{}))
		synthExecCtx := &agent.ExecutionContext{
			SessionID:     "test-session",
			StageID:       "test-stage",
			AgentName:     "test-agent",
			AgentIndex:    1,
			PromptBuilder: pb,
		}
		controller, err := factory.CreateController(config.AgentTypeSynthesis, synthExecCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*SingleShotController)
		assert.True(t, ok, "expected SingleShotController")
	})

	t.Run("scoring type returns ScoringController", func(t *testing.T) {
		controller, err := factory.CreateController(config.AgentTypeScoring, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*ScoringController)
		assert.True(t, ok, "expected ScoringController")
	})

	t.Run("orchestrator type returns IteratingController", func(t *testing.T) {
		controller, err := factory.CreateController(config.AgentTypeOrchestrator, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*IteratingController)
		assert.True(t, ok, "expected IteratingController")
	})

	t.Run("typo in agent type returns error", func(t *testing.T) {
		typoType := config.AgentType("syntesis") // typo of "synthesis"
		controller, err := factory.CreateController(typoType, execCtx)

		require.Error(t, err)
		assert.Nil(t, controller)
		assert.Contains(t, err.Error(), "unknown agent type")
		assert.Contains(t, err.Error(), "syntesis")
	})
}
