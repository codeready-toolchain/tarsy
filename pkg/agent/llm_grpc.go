package agent

import (
	"context"
	"fmt"
	"io"
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
func NewGRPCLLMClient(addr string) (*GRPCLLMClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LLM service at %s: %w", addr, err)
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
		ApiKeyEnv:           cfg.APIKeyEnv,
		BaseUrl:             cfg.BaseURL,
		MaxToolResultTokens: int32(cfg.MaxToolResultTokens),
	}
	// Resolve VertexAI fields
	if cfg.ProjectEnv != "" {
		pc.Project = os.Getenv(cfg.ProjectEnv)
	}
	if cfg.LocationEnv != "" {
		pc.Location = os.Getenv(cfg.LocationEnv)
	}
	// Map native tools
	if len(cfg.NativeTools) > 0 {
		pc.NativeTools = make(map[string]bool, len(cfg.NativeTools))
		for tool, enabled := range cfg.NativeTools {
			pc.NativeTools[string(tool)] = enabled
		}
	}
	// Determine backend
	switch cfg.Type {
	case config.LLMProviderTypeGoogle:
		pc.Backend = "google-native"
	default:
		pc.Backend = "langchain"
	}
	return pc
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
		return nil
	}
}
