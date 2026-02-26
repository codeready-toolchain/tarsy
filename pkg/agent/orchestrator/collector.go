package orchestrator

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// ResultCollector adapts SubAgentRunner to the agent.SubAgentResultCollector
// interface, formatting raw SubAgentResult values into ConversationMessages
// via FormatSubAgentResult.
type ResultCollector struct {
	runner *SubAgentRunner
}

// NewResultCollector creates a ResultCollector wrapping the given runner.
func NewResultCollector(runner *SubAgentRunner) agent.SubAgentResultCollector {
	return &ResultCollector{runner: runner}
}

func (c *ResultCollector) TryDrainResult() (agent.ConversationMessage, bool) {
	result, ok := c.runner.TryGetNext()
	if !ok {
		return agent.ConversationMessage{}, false
	}
	return FormatSubAgentResult(result), true
}

func (c *ResultCollector) WaitForResult(ctx context.Context) (agent.ConversationMessage, error) {
	result, err := c.runner.WaitForNext(ctx)
	if err != nil {
		return agent.ConversationMessage{}, err
	}
	return FormatSubAgentResult(result), nil
}

func (c *ResultCollector) HasPending() bool {
	return c.runner.HasPending()
}
