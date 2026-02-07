package agent

import (
	"context"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// LLMClient is the Go-side interface for calling the Python LLM service.
// It wraps the gRPC connection and provides a channel-based streaming API.
type LLMClient interface {
	// Generate sends a conversation to the LLM and returns a stream of chunks.
	// The returned channel is closed when the stream completes.
	// Errors are delivered as ErrorChunk values in the channel.
	Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error)

	// Close releases the gRPC connection.
	Close() error
}

// GenerateInput is the Go-side representation of a Generate request.
type GenerateInput struct {
	SessionID   string
	ExecutionID string
	Messages    []ConversationMessage
	Config      *config.LLMProviderConfig
	Tools       []ToolDefinition // nil = no tools
}

// ConversationMessage is the Go-side message type.
type ConversationMessage struct {
	Role       string     // "system", "user", "assistant", "tool"
	Content    string
	ToolCalls  []ToolCall // For assistant messages
	ToolCallID string     // For tool result messages
	ToolName   string     // For tool result messages
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name             string
	Description      string
	ParametersSchema string // JSON Schema
}

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON
}

// Chunk is the interface for all streaming chunk types.
type Chunk interface {
	chunkType() ChunkType
}

// ChunkType identifies the kind of streaming chunk.
type ChunkType string

const (
	ChunkTypeText          ChunkType = "text"
	ChunkTypeThinking      ChunkType = "thinking"
	ChunkTypeToolCall      ChunkType = "tool_call"
	ChunkTypeCodeExecution ChunkType = "code_execution"
	ChunkTypeUsage         ChunkType = "usage"
	ChunkTypeError         ChunkType = "error"
)

// TextChunk is a chunk of the LLM's text response.
type TextChunk struct{ Content string }

// ThinkingChunk is a chunk of the LLM's internal reasoning.
type ThinkingChunk struct{ Content string }

// ToolCallChunk signals the LLM wants to call a tool.
type ToolCallChunk struct{ CallID, Name, Arguments string }

// CodeExecutionChunk carries Gemini code execution results.
type CodeExecutionChunk struct{ Code, Result string }

// UsageChunk reports token consumption for this LLM call.
type UsageChunk struct{ InputTokens, OutputTokens, TotalTokens, ThinkingTokens int32 }

// ErrorChunk signals an error from the LLM provider.
type ErrorChunk struct {
	Message   string
	Code      string
	Retryable bool
}

func (c *TextChunk) chunkType() ChunkType          { return ChunkTypeText }
func (c *ThinkingChunk) chunkType() ChunkType       { return ChunkTypeThinking }
func (c *ToolCallChunk) chunkType() ChunkType       { return ChunkTypeToolCall }
func (c *CodeExecutionChunk) chunkType() ChunkType  { return ChunkTypeCodeExecution }
func (c *UsageChunk) chunkType() ChunkType          { return ChunkTypeUsage }
func (c *ErrorChunk) chunkType() ChunkType          { return ChunkTypeError }
