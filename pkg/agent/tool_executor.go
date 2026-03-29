package agent

import (
	"context"
	"fmt"
)

// ToolExecutor abstracts tool/MCP execution for iteration controllers.
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

	// RequiredSummarization, when non-nil, instructs the controller to
	// summarize Content via an LLM call before feeding it back to the agent.
	// Content holds the raw data (stored in trace); the summary replaces it
	// in the conversation. Uses the same infrastructure as MCP tool
	// summarization (timeline events, LLM interaction recording, metrics).
	RequiredSummarization *SummarizationRequest
}

// SummarizationRequest carries prompts for a required LLM summarization
// of a tool result. The controller calls callSummarizationLLM with these.
type SummarizationRequest struct {
	SystemPrompt string
	UserPrompt   string
}

// StubToolExecutor returns canned responses for testing.
// The real MCP-backed implementation is in pkg/mcp/executor.go.
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
