package agent

import (
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// AgentFactory creates Agent instances from resolved configuration.
type AgentFactory struct {
	controllerFactory ControllerFactory
}

// ControllerFactory creates controllers by agent type.
// Implemented by the controller package to avoid import cycles.
type ControllerFactory interface {
	CreateController(agentType config.AgentType, execCtx *ExecutionContext) (Controller, error)
}

// NewAgentFactory creates a new agent factory.
func NewAgentFactory(controllerFactory ControllerFactory) *AgentFactory {
	return &AgentFactory{controllerFactory: controllerFactory}
}

// CreateAgent builds an Agent instance for the given execution context.
func (f *AgentFactory) CreateAgent(execCtx *ExecutionContext) (Agent, error) {
	if execCtx == nil || execCtx.Config == nil {
		return nil, fmt.Errorf("execution context and config must not be nil")
	}
	controller, err := f.controllerFactory.CreateController(execCtx.Config.Type, execCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create controller for agent type %q: %w",
			execCtx.Config.Type, err)
	}
	if execCtx.Config.Type == config.AgentTypeScoring {
		return NewScoringAgent(controller), nil
	}
	return NewBaseAgent(controller), nil
}
