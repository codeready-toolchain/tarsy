// Package controller provides iteration strategy implementations for agents.
package controller

import (
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Factory creates controllers by iteration strategy.
// Implements agent.ControllerFactory.
type Factory struct{}

// NewFactory creates a new controller factory.
func NewFactory() *Factory {
	return &Factory{}
}

// CreateController builds a Controller for the given strategy.
func (f *Factory) CreateController(strategy config.IterationStrategy, execCtx *agent.ExecutionContext) (agent.Controller, error) {
	switch strategy {
	case "":
		return nil, fmt.Errorf("iteration strategy is required (must be one of: react, native-thinking, synthesis, synthesis-native-thinking)")
	case config.IterationStrategyReact:
		return NewReActController(), nil
	case config.IterationStrategyNativeThinking:
		return NewNativeThinkingController(), nil
	case config.IterationStrategySynthesis:
		return NewSynthesisController(), nil
	case config.IterationStrategySynthesisNativeThinking:
		// Same controller — backend difference handled by LLMProviderConfig
		return NewSynthesisController(), nil
	default:
		return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
	}
}
