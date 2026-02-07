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
		// Empty string defaults to single-call controller (Phase 3.1)
		return NewSingleCallController(), nil
	case config.IterationStrategyReact:
		return nil, fmt.Errorf("react controller not yet implemented (Phase 3.2)")
	case config.IterationStrategyNativeThinking:
		return nil, fmt.Errorf("native thinking controller not yet implemented (Phase 3.2)")
	case config.IterationStrategySynthesis:
		return nil, fmt.Errorf("synthesis controller not yet implemented (Phase 3.2)")
	case config.IterationStrategySynthesisNativeThinking:
		return nil, fmt.Errorf("synthesis-native-thinking controller not yet implemented (Phase 3.2)")
	default:
		return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
	}
}
