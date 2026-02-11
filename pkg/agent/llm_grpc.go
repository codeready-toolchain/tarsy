package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	llmv1 "github.com/codeready-toolchain/tarsy/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCLLMClient implements LLMClient by calling the Python LLM service via gRPC.
type GRPCLLMClient struct {
	conn   *grpc.ClientConn
	client llmv1.LLMServiceClient
}

// NewGRPCLLMClient creates a new gRPC LLM client.
// Uses insecure (plaintext) transport — the Python LLM service is expected to
// run as a sidecar or on localhost. If the service is ever deployed across a
// network boundary, this must be upgraded to TLS.
func NewGRPCLLMClient(addr string) (*GRPCLLMClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client for %s: %w", addr, err)
	}
	return &GRPCLLMClient{
		conn:   conn,
		client: llmv1.NewLLMServiceClient(conn),
	}, nil
}

// Generate sends a conversation to the LLM and returns a channel of chunks.
func (c *GRPCLLMClient) Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error) {
	req := toProtoRequest(input)

	stream, err := c.client.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("gRPC Generate call failed: %w", err)
	}

	ch := make(chan Chunk, 32)
	go func() {
		defer close(ch)
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				select {
				case ch <- &ErrorChunk{Message: err.Error(), Retryable: false}:
				case <-ctx.Done():
				}
				return
			}
			chunk := fromProtoResponse(resp)
			if chunk != nil {
				select {
				case ch <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// Close releases the gRPC connection.
func (c *GRPCLLMClient) Close() error {
	return c.conn.Close()
}

// ────────────────────────────────────────────────────────────
// Proto conversion helpers
// ────────────────────────────────────────────────────────────

func toProtoRequest(input *GenerateInput) *llmv1.GenerateRequest {
	req := &llmv1.GenerateRequest{
		SessionId:   input.SessionID,
		ExecutionId: input.ExecutionID,
		Messages:    toProtoMessages(input.Messages),
		Tools:       toProtoTools(input.Tools),
	}
	if input.Config != nil {
		req.LlmConfig = toProtoLLMConfig(input.Config)
	}
	// Backend is set by the caller based on iteration strategy, not derived from provider type
	if req.LlmConfig != nil && input.Backend != "" {
		req.LlmConfig.Backend = input.Backend
	}
	return req
}

func toProtoMessages(msgs []ConversationMessage) []*llmv1.ConversationMessage {
	out := make([]*llmv1.ConversationMessage, len(msgs))
	for i, m := range msgs {
		pm := &llmv1.ConversationMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallId: m.ToolCallID,
			ToolName:   m.ToolName,
		}
		for _, tc := range m.ToolCalls {
			pm.ToolCalls = append(pm.ToolCalls, &llmv1.ToolCall{
				Id:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
		out[i] = pm
	}
	return out
}

func toProtoLLMConfig(cfg *config.LLMProviderConfig) *llmv1.LLMConfig {
	pc := &llmv1.LLMConfig{
		Provider:            string(cfg.Type),
		Model:               cfg.Model,
		ApiKeyEnv:           cfg.APIKeyEnv,      // Sent as env-var name; Python resolves the secret
		CredentialsEnv:      cfg.CredentialsEnv, // Sent as env-var name; Python resolves the credentials file path
		BaseUrl:             cfg.BaseURL,
		MaxToolResultTokens: clampToInt32(cfg.MaxToolResultTokens),
	}
	// Resolve VertexAI fields — values (not env names) are sent over gRPC
	if cfg.ProjectEnv != "" {
		pc.Project = os.Getenv(cfg.ProjectEnv)
		if pc.Project == "" {
			slog.Warn("VertexAI project env var is configured but empty",
				"env_var", cfg.ProjectEnv)
		}
	}
	if cfg.LocationEnv != "" {
		pc.Location = os.Getenv(cfg.LocationEnv)
		if pc.Location == "" {
			slog.Warn("VertexAI location env var is configured but empty",
				"env_var", cfg.LocationEnv)
		}
	}
	// Map native tools
	if len(cfg.NativeTools) > 0 {
		pc.NativeTools = make(map[string]bool, len(cfg.NativeTools))
		for tool, enabled := range cfg.NativeTools {
			pc.NativeTools[string(tool)] = enabled
		}
	}
	// Backend is set by toProtoRequest() from input.Backend (resolved from iteration strategy).
	return pc
}

// clampToInt32 converts an int to int32, clamping to math.MaxInt32 if needed.
func clampToInt32(v int) int32 {
	if v > math.MaxInt32 {
		slog.Warn("int value exceeds int32 range, clamping",
			"value", v, "clamped_to", math.MaxInt32)
		return math.MaxInt32
	}
	return int32(v)
}

func toProtoTools(tools []ToolDefinition) []*llmv1.ToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]*llmv1.ToolDefinition, len(tools))
	for i, t := range tools {
		out[i] = &llmv1.ToolDefinition{
			Name:             t.Name,
			Description:      t.Description,
			ParametersSchema: t.ParametersSchema,
		}
	}
	return out
}

func fromProtoResponse(resp *llmv1.GenerateResponse) Chunk {
	// Final-only responses (is_final=true with no content) are normal —
	// the Python service sends these to mark stream completion.
	if resp.Content == nil {
		if !resp.IsFinal {
			slog.Warn("GenerateResponse with nil content and is_final=false, skipping")
		}
		return nil
	}

	switch c := resp.Content.(type) {
	case *llmv1.GenerateResponse_Text:
		return &TextChunk{Content: c.Text.Content}
	case *llmv1.GenerateResponse_Thinking:
		return &ThinkingChunk{Content: c.Thinking.Content}
	case *llmv1.GenerateResponse_ToolCall:
		return &ToolCallChunk{
			CallID:    c.ToolCall.CallId,
			Name:      c.ToolCall.Name,
			Arguments: c.ToolCall.Arguments,
		}
	case *llmv1.GenerateResponse_CodeExecution:
		return &CodeExecutionChunk{
			Code:   c.CodeExecution.Code,
			Result: c.CodeExecution.Result,
		}
	case *llmv1.GenerateResponse_Grounding:
		g := c.Grounding
		chunk := &GroundingChunk{
			WebSearchQueries:     g.WebSearchQueries,
			SearchEntryPointHTML: g.SearchEntryPointHtml,
		}
		for _, gc := range g.GroundingChunks {
			chunk.Sources = append(chunk.Sources, GroundingSource{
				URI:   gc.Uri,
				Title: gc.Title,
			})
		}
		for _, gs := range g.GroundingSupports {
			chunk.Supports = append(chunk.Supports, GroundingSupport{
				StartIndex:            int(gs.StartIndex),
				EndIndex:              int(gs.EndIndex),
				Text:                  gs.Text,
				GroundingChunkIndices: intSliceFromInt32(gs.GroundingChunkIndices),
			})
		}
		return chunk
	case *llmv1.GenerateResponse_Usage:
		return &UsageChunk{
			InputTokens:    int(c.Usage.InputTokens),
			OutputTokens:   int(c.Usage.OutputTokens),
			TotalTokens:    int(c.Usage.TotalTokens),
			ThinkingTokens: int(c.Usage.ThinkingTokens),
		}
	case *llmv1.GenerateResponse_Error:
		return &ErrorChunk{
			Message:   c.Error.Message,
			Code:      c.Error.Code,
			Retryable: c.Error.Retryable,
		}
	default:
		slog.Warn("Unknown GenerateResponse content type, skipping chunk",
			"type", fmt.Sprintf("%T", resp.Content))
		return nil
	}
}

// intSliceFromInt32 converts a []int32 to []int.
func intSliceFromInt32(s []int32) []int {
	if len(s) == 0 {
		return nil
	}
	out := make([]int, len(s))
	for i, v := range s {
		out[i] = int(v)
	}
	return out
}
