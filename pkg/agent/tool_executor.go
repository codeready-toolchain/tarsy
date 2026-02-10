package agent

import (
	"context"
	"fmt"
)

// ToolExecutor abstracts tool/MCP execution for iteration controllers.
// Phase 3.2: stub implementation. Phase 4: real MCP client.
type ToolExecutor interface {
	// Execute runs a single tool call and returns the result.
	// The result is always a string (tool output or error message).
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)

	// ListTools returns available tool definitions for the current execution.
	// Returns nil if no tools are configured.
	ListTools(ctx context.Context) ([]ToolDefinition, error)

	// Close releases resources (MCP transports, subprocesses).
	// No-op for StubToolExecutor.
	Close() error
}

// ToolResult represents the output of a tool execution.
type ToolResult struct {
	CallID  string // Matches the ToolCall.ID
	Name    string // Tool name (server.tool format)
	Content string // Tool output (text)
	IsError bool   // Whether the tool returned an error
}

// StubToolExecutor returns canned responses for testing.
// Will be replaced by MCP client in Phase 4.
type StubToolExecutor struct {
	tools []ToolDefinition
}

// NewStubToolExecutor creates a stub executor with the given tool definitions.
func NewStubToolExecutor(tools []ToolDefinition) *StubToolExecutor {
	return &StubToolExecutor{tools: tools}
}

func (s *StubToolExecutor) Execute(_ context.Context, call ToolCall) (*ToolResult, error) {
	return &ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: fmt.Sprintf("[stub] Tool %q called with args: %s", call.Name, call.Arguments),
		IsError: false,
	}, nil
}

func (s *StubToolExecutor) ListTools(_ context.Context) ([]ToolDefinition, error) {
	return s.tools, nil
}

func (s *StubToolExecutor) Close() error { return nil }
