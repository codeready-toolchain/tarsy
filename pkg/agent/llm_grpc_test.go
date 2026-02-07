package agent

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	llmv1 "github.com/codeready-toolchain/tarsy/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToProtoMessages(t *testing.T) {
	messages := []ConversationMessage{
		{Role: "system", Content: "You are a bot"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "k8s.get_pods", Arguments: `{"ns":"default"}`},
		}},
		{Role: "tool", Content: `{"result":"ok"}`, ToolCallID: "tc1", ToolName: "k8s.get_pods"},
	}

	result := toProtoMessages(messages)
	require.Len(t, result, 4)

	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "You are a bot", result[0].Content)

	assert.Equal(t, "user", result[1].Role)

	// Assistant with tool calls
	assert.Equal(t, "assistant", result[2].Role)
	assert.Equal(t, "Hi", result[2].Content)
	require.Len(t, result[2].ToolCalls, 1)
	assert.Equal(t, "tc1", result[2].ToolCalls[0].Id)
	assert.Equal(t, "k8s.get_pods", result[2].ToolCalls[0].Name)

	// Tool result
	assert.Equal(t, "tool", result[3].Role)
	assert.Equal(t, "tc1", result[3].ToolCallId)
}

func TestToProtoLLMConfig(t *testing.T) {
	cfg := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeGoogle,
		Model:               "gemini-2.5-pro",
		APIKeyEnv:           "GOOGLE_API_KEY",
		MaxToolResultTokens: 950000,
		NativeTools: map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		},
	}

	proto := toProtoLLMConfig(cfg)
	assert.Equal(t, "google", proto.Provider)
	assert.Equal(t, "gemini-2.5-pro", proto.Model)
	assert.Equal(t, "GOOGLE_API_KEY", proto.ApiKeyEnv)
	assert.Equal(t, int32(950000), proto.MaxToolResultTokens)
	assert.Equal(t, "google-native", proto.Backend)
	assert.True(t, proto.NativeTools["google_search"])
}

func TestToProtoLLMConfig_LangChainBackend(t *testing.T) {
	cfg := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeOpenAI,
		Model:               "gpt-5",
		MaxToolResultTokens: 250000,
	}

	proto := toProtoLLMConfig(cfg)
	assert.Equal(t, "langchain", proto.Backend)
}

func TestFromProtoResponse(t *testing.T) {
	t.Run("text delta", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Text{
				Text: &llmv1.TextDelta{Content: "hello"},
			},
		}
		chunk := fromProtoResponse(resp)
		tc, ok := chunk.(*TextChunk)
		require.True(t, ok)
		assert.Equal(t, "hello", tc.Content)
	})

	t.Run("thinking delta", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Thinking{
				Thinking: &llmv1.ThinkingDelta{Content: "hmm"},
			},
		}
		chunk := fromProtoResponse(resp)
		tc, ok := chunk.(*ThinkingChunk)
		require.True(t, ok)
		assert.Equal(t, "hmm", tc.Content)
	})

	t.Run("tool call delta", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_ToolCall{
				ToolCall: &llmv1.ToolCallDelta{
					CallId:    "call1",
					Name:      "k8s.get_pods",
					Arguments: `{"ns":"default"}`,
				},
			},
		}
		chunk := fromProtoResponse(resp)
		tc, ok := chunk.(*ToolCallChunk)
		require.True(t, ok)
		assert.Equal(t, "call1", tc.CallID)
		assert.Equal(t, "k8s.get_pods", tc.Name)
	})

	t.Run("usage info", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Usage{
				Usage: &llmv1.UsageInfo{
					InputTokens:    100,
					OutputTokens:   200,
					TotalTokens:    300,
					ThinkingTokens: 50,
				},
			},
		}
		chunk := fromProtoResponse(resp)
		uc, ok := chunk.(*UsageChunk)
		require.True(t, ok)
		assert.Equal(t, int32(100), uc.InputTokens)
		assert.Equal(t, int32(300), uc.TotalTokens)
	})

	t.Run("error info", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Error{
				Error: &llmv1.ErrorInfo{
					Message:   "rate limited",
					Code:      "429",
					Retryable: true,
				},
			},
		}
		chunk := fromProtoResponse(resp)
		ec, ok := chunk.(*ErrorChunk)
		require.True(t, ok)
		assert.Equal(t, "rate limited", ec.Message)
		assert.True(t, ec.Retryable)
	})

	t.Run("nil content returns nil", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{}
		chunk := fromProtoResponse(resp)
		assert.Nil(t, chunk)
	})
}

func TestToProtoTools(t *testing.T) {
	t.Run("nil tools returns nil", func(t *testing.T) {
		assert.Nil(t, toProtoTools(nil))
	})

	t.Run("empty tools returns nil", func(t *testing.T) {
		assert.Nil(t, toProtoTools([]ToolDefinition{}))
	})

	t.Run("converts tools", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "k8s.get_pods", Description: "Get pods", ParametersSchema: `{"type":"object"}`},
		}
		result := toProtoTools(tools)
		require.Len(t, result, 1)
		assert.Equal(t, "k8s.get_pods", result[0].Name)
	})
}
