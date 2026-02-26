package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Compile-time check that CompositeToolExecutor implements agent.ToolExecutor.
var _ agent.ToolExecutor = (*CompositeToolExecutor)(nil)

// closeTimeout is the maximum time Close() waits for sub-agent goroutines
// to finish. Package-level var to allow tests to use a short duration.
var closeTimeout = 30 * time.Second

// CompositeToolExecutor wraps an MCP tool executor and adds orchestration tools
// (dispatch_agent, cancel_agent, list_agents). It routes calls by name: known
// orchestration tool names go to the SubAgentRunner; everything else goes to
// the inner MCP executor.
type CompositeToolExecutor struct {
	mcpExecutor agent.ToolExecutor
	runner      *SubAgentRunner
	registry    *config.SubAgentRegistry
}

// NewCompositeToolExecutor creates a composite executor. mcpExecutor may be nil
// if the orchestrator has no MCP servers of its own. runner must not be nil.
func NewCompositeToolExecutor(
	mcpExecutor agent.ToolExecutor,
	runner *SubAgentRunner,
	registry *config.SubAgentRegistry,
) *CompositeToolExecutor {
	if runner == nil {
		panic("NewCompositeToolExecutor: runner must not be nil")
	}
	return &CompositeToolExecutor{
		mcpExecutor: mcpExecutor,
		runner:      runner,
		registry:    registry,
	}
}

// ListTools returns the combined tool set: orchestration tools + MCP tools.
func (c *CompositeToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
	tools := make([]agent.ToolDefinition, len(orchestrationTools))
	copy(tools, orchestrationTools)

	if c.mcpExecutor != nil {
		mcpTools, err := c.mcpExecutor.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list MCP tools: %w", err)
		}
		tools = append(tools, mcpTools...)
	}

	return tools, nil
}

// Execute routes the tool call to either the orchestration handler or the MCP executor.
func (c *CompositeToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	if orchestrationToolNames[call.Name] {
		return c.executeOrchestrationTool(ctx, call)
	}
	if c.mcpExecutor != nil {
		return c.mcpExecutor.Execute(ctx, call)
	}
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: fmt.Sprintf("unknown tool: %s", call.Name),
		IsError: true,
	}, nil
}

// Close cancels any still-running sub-agents, waits for them to finish, then
// closes the MCP executor.
//
// Uses context.Background() intentionally: Close() is called from a defer in
// executeAgent, where the parent context may already be cancelled. Cleanup must
// proceed regardless — the 30s timeout is the real upper bound.
func (c *CompositeToolExecutor) Close() error {
	c.runner.CancelAll()

	waitCtx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	c.runner.WaitAll(waitCtx)

	if c.mcpExecutor != nil {
		return c.mcpExecutor.Close()
	}
	return nil
}

func (c *CompositeToolExecutor) executeOrchestrationTool(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	switch call.Name {
	case ToolDispatchAgent:
		return c.handleDispatch(ctx, call)
	case ToolCancelAgent:
		return c.handleCancel(ctx, call)
	case ToolListAgents:
		return c.handleList(call)
	default:
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("unknown orchestration tool: %s", call.Name),
			IsError: true,
		}, nil
	}
}

func (c *CompositeToolExecutor) handleDispatch(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	var args struct {
		Name string `json:"name"`
		Task string `json:"task"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("invalid arguments: %v", err),
			IsError: true,
		}, nil
	}
	if args.Name == "" || args.Task == "" {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "both 'name' and 'task' are required",
			IsError: true,
		}, nil
	}

	execID, err := c.runner.Dispatch(ctx, args.Name, args.Task)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("dispatch failed: %v", err),
			IsError: true,
		}, nil
	}

	resp, _ := json.Marshal(map[string]string{
		"execution_id": execID,
		"status":       "accepted",
	})
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: string(resp),
	}, nil
}

func (c *CompositeToolExecutor) handleCancel(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	var args struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("invalid arguments: %v", err),
			IsError: true,
		}, nil
	}
	if args.ExecutionID == "" {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "'execution_id' is required",
			IsError: true,
		}, nil
	}

	status, err := c.runner.Cancel(args.ExecutionID)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("cancel failed: %v", err),
			IsError: true,
		}, nil
	}

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: fmt.Sprintf("cancel %s: %s", args.ExecutionID, status),
	}, nil
}

func (c *CompositeToolExecutor) handleList(call agent.ToolCall) (*agent.ToolResult, error) {
	statuses := c.runner.List()
	if len(statuses) == 0 {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "No sub-agents dispatched yet.",
		}, nil
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].ExecutionID < statuses[j].ExecutionID
	})

	var b strings.Builder
	for i, s := range statuses {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- %s (exec %s): status=%s, task=%q",
			s.AgentName, s.ExecutionID, s.Status, s.Task)
	}
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: b.String(),
	}, nil
}
