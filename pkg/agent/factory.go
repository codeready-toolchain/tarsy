package agent

import (
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// AgentFactory creates Agent instances from resolved configuration.
type AgentFactory struct {
	controllerFactory ControllerFactory
}

// ControllerFactory creates controllers by strategy.
// Implemented by the controller package to avoid import cycles.
type ControllerFactory interface {
	CreateController(strategy config.IterationStrategy, execCtx *ExecutionContext) (Controller, error)
}

// NewAgentFactory creates a new agent factory.
func NewAgentFactory(controllerFactory ControllerFactory) *AgentFactory {
	return &AgentFactory{controllerFactory: controllerFactory}
}

// CreateAgent builds an Agent instance for the given execution context.
func (f *AgentFactory) CreateAgent(execCtx *ExecutionContext) (Agent, error) {
	controller, err := f.controllerFactory.CreateController(execCtx.Config.IterationStrategy, execCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create controller for strategy %q: %w",
			execCtx.Config.IterationStrategy, err)
	}
	return NewBaseAgent(controller), nil
}
