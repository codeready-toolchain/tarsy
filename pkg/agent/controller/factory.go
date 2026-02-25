// Package controller provides agent type implementations for controllers.
package controller

import (
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Factory creates controllers by agent type.
// Implements agent.ControllerFactory.
type Factory struct{}

// NewFactory creates a new controller factory.
func NewFactory() *Factory {
	return &Factory{}
}

// CreateController builds a Controller for the given agent type.
func (f *Factory) CreateController(agentType config.AgentType, execCtx *agent.ExecutionContext) (agent.Controller, error) {
	switch agentType {
	case config.AgentTypeDefault:
		return NewIteratingController(), nil
	case config.AgentTypeSynthesis:
		return NewSynthesisController(execCtx.PromptBuilder), nil
	default:
		return nil, fmt.Errorf("unknown agent type: %q", agentType)
	}
}
