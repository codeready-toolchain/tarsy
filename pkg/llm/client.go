package llm

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/codeready-toolchain/tarsy/pkg/session"
	pb "github.com/codeready-toolchain/tarsy/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps the gRPC connection to LLM service
type Client struct {
	conn        *grpc.ClientConn
	client      pb.LLMServiceClient
	model       string
	temperature *float32
	maxTokens   *int32
}

// NewClient creates a new LLM client with configuration
func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LLM service: %w", err)
	}

	// Load configuration from environment
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.0-flash-thinking-exp-01-21"
	}

	var temperature *float32
	if tempStr := os.Getenv("GEMINI_TEMPERATURE"); tempStr != "" {
		if temp, err := strconv.ParseFloat(tempStr, 32); err == nil {
			temp32 := float32(temp)
			temperature = &temp32
		}
	}

	var maxTokens *int32
	if maxStr := os.Getenv("GEMINI_MAX_TOKENS"); maxStr != "" {
		if max, err := strconv.ParseInt(maxStr, 10, 32); err == nil {
			max32 := int32(max)
			maxTokens = &max32
		}
	}

	log.Printf("LLM Client configured with model: %s", model)

	return &Client{
		conn:        conn,
		client:      pb.NewLLMServiceClient(conn),
		model:       model,
		temperature: temperature,
		maxTokens:   maxTokens,
	}, nil
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	return c.conn.Close()
}

// StreamChunk represents a streaming chunk from the LLM
type StreamChunk struct {
	Content    string
	IsThinking bool
	IsComplete bool
	IsFinal    bool
	Error      string
}

// GenerateStream generates a response with streaming
func (c *Client) GenerateStream(ctx context.Context, sess *session.Session) (<-chan StreamChunk, <-chan error) {
	chunks := make(chan StreamChunk, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(chunks)
		defer close(errs)

		// Convert session messages to protobuf
		pbMessages := make([]*pb.Message, len(sess.Messages))
		for i, msg := range sess.Messages {
			var role pb.Message_Role
			switch msg.Role {
			case session.RoleSystem:
				role = pb.Message_ROLE_SYSTEM
			case session.RoleUser:
				role = pb.Message_ROLE_USER
			case session.RoleAssistant:
				role = pb.Message_ROLE_ASSISTANT
			default:
				role = pb.Message_ROLE_USER
			}

			pbMessages[i] = &pb.Message{
				Role:    role,
				Content: msg.Content,
			}
		}

		// Create request with configuration
		req := &pb.ThinkingRequest{
			SessionId:   sess.ID,
			Messages:    pbMessages,
			Model:       c.model,
			Temperature: c.temperature,
			MaxTokens:   c.maxTokens,
		}

		// Call streaming RPC
		stream, err := c.client.GenerateWithThinking(ctx, req)
		if err != nil {
			errs <- fmt.Errorf("failed to call GenerateWithThinking: %w", err)
			return
		}

		log.Printf("Started streaming for session %s", sess.ID)

		// Receive chunks
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				log.Printf("Stream complete for session %s", sess.ID)
				return
			}
			if err != nil {
				errs <- fmt.Errorf("stream error: %w", err)
				return
			}

			// Process chunk based on type
			switch x := chunk.ChunkType.(type) {
			case *pb.ThinkingChunk_Thinking:
				select {
				case chunks <- StreamChunk{
					Content:    x.Thinking.Content,
					IsThinking: true,
					IsComplete: x.Thinking.IsComplete,
				}:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}

			case *pb.ThinkingChunk_Response:
				select {
				case chunks <- StreamChunk{
					Content:    x.Response.Content,
					IsThinking: false,
					IsComplete: x.Response.IsComplete,
					IsFinal:    x.Response.IsFinal,
				}:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}

			case *pb.ThinkingChunk_Error:
				select {
				case chunks <- StreamChunk{
					Error: x.Error.Message,
				}:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}
			}
		}
	}()

	return chunks, errs
}
