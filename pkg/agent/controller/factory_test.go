package controller

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
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

	t.Run("empty string returns error", func(t *testing.T) {
		controller, err := factory.CreateController("", execCtx)
		require.Error(t, err)
		assert.Nil(t, controller)
		assert.Contains(t, err.Error(), "iteration strategy is required")
	})

	t.Run("native-thinking strategy returns FunctionCallingController", func(t *testing.T) {
		controller, err := factory.CreateController(config.IterationStrategyNativeThinking, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*FunctionCallingController)
		assert.True(t, ok, "expected FunctionCallingController")
	})

	t.Run("langchain strategy returns FunctionCallingController", func(t *testing.T) {
		controller, err := factory.CreateController(config.IterationStrategyLangChain, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*FunctionCallingController)
		assert.True(t, ok, "expected FunctionCallingController")
	})

	t.Run("synthesis strategy returns SynthesisController", func(t *testing.T) {
		controller, err := factory.CreateController(config.IterationStrategySynthesis, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*SynthesisController)
		assert.True(t, ok, "expected SynthesisController")
	})

	t.Run("synthesis-native-thinking strategy returns SynthesisController", func(t *testing.T) {
		controller, err := factory.CreateController(config.IterationStrategySynthesisNativeThinking, execCtx)
		require.NoError(t, err)
		require.NotNil(t, controller)

		_, ok := controller.(*SynthesisController)
		assert.True(t, ok, "expected SynthesisController (same for both synthesis strategies)")
	})

	t.Run("unknown strategy returns error", func(t *testing.T) {
		unknownStrategy := config.IterationStrategy("unknown-strategy")
		controller, err := factory.CreateController(unknownStrategy, execCtx)

		require.Error(t, err)
		assert.Nil(t, controller)
		assert.Contains(t, err.Error(), "unknown iteration strategy")
		assert.Contains(t, err.Error(), "unknown-strategy")
	})

	t.Run("typo in strategy returns error", func(t *testing.T) {
		typoStrategy := config.IterationStrategy("langcahin") // typo of "langchain"
		controller, err := factory.CreateController(typoStrategy, execCtx)

		require.Error(t, err)
		assert.Nil(t, controller)
		assert.Contains(t, err.Error(), "unknown iteration strategy")
		assert.Contains(t, err.Error(), "langcahin")
	})
}
