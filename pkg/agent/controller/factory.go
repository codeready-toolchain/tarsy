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
		return nil, fmt.Errorf("iteration strategy is required (must be one of: native-thinking, langchain, synthesis, synthesis-native-thinking)")
	case config.IterationStrategyNativeThinking, config.IterationStrategyLangChain:
		return NewFunctionCallingController(), nil
	case config.IterationStrategySynthesis, config.IterationStrategySynthesisNativeThinking:
		return NewSynthesisController(), nil
	default:
		return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
	}
}
