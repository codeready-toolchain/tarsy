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

	// RequiredSummarization signals that the tool's raw result must always
	// be summarized by an LLM before being returned to the agent.
	//
	// This is different from auto-summarization of regular MCP tools:
	//
	//   Auto-summarization (maybeSummarize): triggered only when a regular
	//   MCP tool result exceeds a token threshold. The dashboard shows two
	//   events — the tool call with the raw result, and a separate
	//   mcp_tool_summary card underneath with the condensed version.
	//
	//   Required summarization: always triggered regardless of size (e.g.
	//   search_past_sessions returns raw DB rows that need LLM distillation).
	//   The dashboard shows a single tool call card with the summary as its
	//   content — no separate summary event. The raw data is preserved only
	//   in the trace (MCP interaction record) for debugging.
	//
	// When set, Content holds the raw data for trace storage. The controller
	// runs the LLM call, records the interaction, and replaces Content with
	// the summary in both the timeline event and the agent conversation.
	RequiredSummarization *SummarizationRequest
}

// SummarizationRequest carries the LLM prompts for a required summarization.
// The controller passes these to callSummarizationLLM.
type SummarizationRequest struct {
	SystemPrompt    string
	UserPrompt      string
	TransformResult func(summary string) string // Post-processes summarized output; nil = use as-is
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
