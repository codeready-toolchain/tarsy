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
	assert.Equal(t, "k8s.get_pods", result[3].ToolName)
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
	assert.True(t, proto.NativeTools["google_search"])
	// Backend is set by toProtoRequest from input.Backend
	assert.Empty(t, proto.Backend)
}

func TestToProtoRequest_BackendPassthrough(t *testing.T) {
	t.Run("backend from input overrides empty LLMConfig backend", func(t *testing.T) {
		input := &GenerateInput{
			SessionID: "sess-1",
			Config: &config.LLMProviderConfig{
				Type:  config.LLMProviderTypeGoogle,
				Model: "gemini-2.5-pro",
			},
			Backend: config.LLMBackendNativeGemini,
		}
		req := toProtoRequest(input)
		assert.Equal(t, string(config.LLMBackendNativeGemini), req.LlmConfig.Backend)
	})

	t.Run("langchain backend", func(t *testing.T) {
		input := &GenerateInput{
			SessionID: "sess-1",
			Config: &config.LLMProviderConfig{
				Type:  config.LLMProviderTypeOpenAI,
				Model: "gpt-5",
			},
			Backend: config.LLMBackendLangChain,
		}
		req := toProtoRequest(input)
		assert.Equal(t, string(config.LLMBackendLangChain), req.LlmConfig.Backend)
	})

	t.Run("empty backend does not override", func(t *testing.T) {
		input := &GenerateInput{
			SessionID: "sess-1",
			Config: &config.LLMProviderConfig{
				Type:  config.LLMProviderTypeGoogle,
				Model: "gemini-2.5-pro",
			},
		}
		req := toProtoRequest(input)
		// toProtoLLMConfig no longer sets backend, and input.Backend is empty
		assert.Empty(t, req.LlmConfig.Backend)
	})
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

	t.Run("code execution delta", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_CodeExecution{
				CodeExecution: &llmv1.CodeExecutionDelta{
					Code:   "print('hi')",
					Result: "hi",
				},
			},
		}
		chunk := fromProtoResponse(resp)
		ce, ok := chunk.(*CodeExecutionChunk)
		require.True(t, ok)
		assert.Equal(t, "print('hi')", ce.Code)
		assert.Equal(t, "hi", ce.Result)
	})

	t.Run("grounding delta with all fields", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Grounding{
				Grounding: &llmv1.GroundingDelta{
					WebSearchQueries: []string{"Euro 2024 winner", "Spain Euro"},
					GroundingChunks: []*llmv1.GroundingChunkInfo{
						{Uri: "https://uefa.com", Title: "UEFA"},
						{Uri: "https://bbc.com", Title: "BBC Sport"},
					},
					GroundingSupports: []*llmv1.GroundingSupport{
						{
							StartIndex:            0,
							EndIndex:              20,
							Text:                  "Spain won Euro 2024",
							GroundingChunkIndices: []int32{0, 1},
						},
					},
					SearchEntryPointHtml: "<div>widget</div>",
				},
			},
		}
		chunk := fromProtoResponse(resp)
		gc, ok := chunk.(*GroundingChunk)
		require.True(t, ok)
		assert.Equal(t, []string{"Euro 2024 winner", "Spain Euro"}, gc.WebSearchQueries)
		require.Len(t, gc.Sources, 2)
		assert.Equal(t, "https://uefa.com", gc.Sources[0].URI)
		assert.Equal(t, "UEFA", gc.Sources[0].Title)
		assert.Equal(t, "https://bbc.com", gc.Sources[1].URI)
		require.Len(t, gc.Supports, 1)
		assert.Equal(t, 0, gc.Supports[0].StartIndex)
		assert.Equal(t, 20, gc.Supports[0].EndIndex)
		assert.Equal(t, "Spain won Euro 2024", gc.Supports[0].Text)
		assert.Equal(t, []int{0, 1}, gc.Supports[0].GroundingChunkIndices)
		assert.Equal(t, "<div>widget</div>", gc.SearchEntryPointHTML)
	})

	t.Run("grounding delta empty", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{
			Content: &llmv1.GenerateResponse_Grounding{
				Grounding: &llmv1.GroundingDelta{},
			},
		}
		chunk := fromProtoResponse(resp)
		gc, ok := chunk.(*GroundingChunk)
		require.True(t, ok)
		assert.Empty(t, gc.WebSearchQueries)
		assert.Empty(t, gc.Sources)
		assert.Empty(t, gc.Supports)
		assert.Empty(t, gc.SearchEntryPointHTML)
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
		assert.Equal(t, 100, uc.InputTokens)
		assert.Equal(t, 200, uc.OutputTokens)
		assert.Equal(t, 300, uc.TotalTokens)
		assert.Equal(t, 50, uc.ThinkingTokens)
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

	t.Run("final-only response returns nil without warning", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{IsFinal: true}
		chunk := fromProtoResponse(resp)
		assert.Nil(t, chunk)
	})

	t.Run("nil content non-final returns nil", func(t *testing.T) {
		resp := &llmv1.GenerateResponse{}
		chunk := fromProtoResponse(resp)
		assert.Nil(t, chunk)
	})
}

func TestIntSliceFromInt32(t *testing.T) {
	t.Run("converts values", func(t *testing.T) {
		result := intSliceFromInt32([]int32{0, 1, 2, 42})
		assert.Equal(t, []int{0, 1, 2, 42}, result)
	})

	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, intSliceFromInt32(nil))
	})

	t.Run("empty returns nil", func(t *testing.T) {
		assert.Nil(t, intSliceFromInt32([]int32{}))
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
