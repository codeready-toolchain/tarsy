package controller

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConversationContext(t *testing.T) {
	tests := []struct {
		name     string
		messages []agent.ConversationMessage
		expected string
	}{
		{
			name:     "empty messages returns empty string",
			messages: nil,
			expected: "",
		},
		{
			name: "excludes system messages",
			messages: []agent.ConversationMessage{
				{Role: agent.RoleSystem, Content: "You are a helpful assistant"},
				{Role: agent.RoleUser, Content: "What pods are failing?"},
				{Role: agent.RoleAssistant, Content: "Let me check the pods."},
			},
			expected: "[user]: What pods are failing?\n\n[assistant]: Let me check the pods.\n\n",
		},
		{
			name: "multi-turn conversation",
			messages: []agent.ConversationMessage{
				{Role: agent.RoleSystem, Content: "system prompt"},
				{Role: agent.RoleUser, Content: "question 1"},
				{Role: agent.RoleAssistant, Content: "answer 1"},
				{Role: agent.RoleUser, Content: "Observation: tool output"},
				{Role: agent.RoleAssistant, Content: "answer 2"},
			},
			expected: "[user]: question 1\n\n" +
				"[assistant]: answer 1\n\n" +
				"[user]: Observation: tool output\n\n" +
				"[assistant]: answer 2\n\n",
		},
		{
			name: "includes tool role messages",
			messages: []agent.ConversationMessage{
				{Role: agent.RoleTool, Content: "tool result content"},
			},
			expected: "[tool]: tool result content\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildConversationContext(tt.messages)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestMaybeSummarize(t *testing.T) {
	ctx := t.Context()

	t.Run("returns raw content when below default threshold with nil config", func(t *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: nil, // nil = enabled with defaults
			},
		})
		pb := prompt.NewPromptBuilder(registry)
		execCtx := &agent.ExecutionContext{
			PromptBuilder: pb,
			Config: &agent.ResolvedAgentConfig{
				LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
			},
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods", "small output", "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, "small output", result.Content)
		assert.False(t, result.WasSummarized)
	})

	t.Run("returns raw content when below explicit threshold", func(t *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					Enabled:             config.BoolPtr(true),
					SizeThresholdTokens: 5000,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)
		execCtx := &agent.ExecutionContext{
			PromptBuilder: pb,
			Config: &agent.ResolvedAgentConfig{
				LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
			},
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods", "short", "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, "short", result.Content)
		assert.False(t, result.WasSummarized)
	})

	t.Run("returns raw content when explicitly disabled", func(t *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					Enabled:             config.BoolPtr(false),
					SizeThresholdTokens: 100,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)
		execCtx := &agent.ExecutionContext{
			PromptBuilder: pb,
			Config: &agent.ResolvedAgentConfig{
				LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
			},
		}

		largeContent := strings.Repeat("x", 1000) // way above 100 tokens
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods", largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, largeContent, result.Content)
		assert.False(t, result.WasSummarized)
	})

	t.Run("returns raw content when server not found", func(t *testing.T) {
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{})
		pb := prompt.NewPromptBuilder(registry)
		execCtx := &agent.ExecutionContext{
			PromptBuilder: pb,
			Config: &agent.ResolvedAgentConfig{
				LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
			},
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "unknown-server", "get_pods", "content", "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, "content", result.Content)
		assert.False(t, result.WasSummarized)
	})

	t.Run("returns raw content when PromptBuilder is nil", func(t *testing.T) {
		execCtx := &agent.ExecutionContext{
			PromptBuilder: nil,
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods", "content", "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, "content", result.Content)
		assert.False(t, result.WasSummarized)
	})

	t.Run("triggers summarization with nil config above default threshold", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Summarized output"}}},
			},
		}

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: nil, // nil = enabled with defaults (threshold = DefaultSizeThresholdTokens)
			},
		})
		pb := prompt.NewPromptBuilder(registry)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		// DefaultSizeThresholdTokens is 5000 tokens ≈ 20000 chars
		largeContent := strings.Repeat("event-data ", 2500) // ~27500 chars ≈ 6875 tokens > 5000
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_events",
			largeContent, "[user]: check events", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Summarized output")
		assert.Contains(t, result.Content, "[NOTE: The output from test-server.get_events was")
	})

	t.Run("triggers summarization above threshold", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Summarized: 3 pods found, 1 failing"}}},
			},
		}

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens:  100, // Low threshold
					SummaryMaxTokenLimit: 500,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		// Content that exceeds threshold (100 tokens = 400 chars)
		largeContent := strings.Repeat("pod-info ", 100) // 900 chars = 225 tokens > 100
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "[user]: check pods", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)

		want := "[NOTE: The output from test-server.get_pods was 225 tokens (estimated) " +
			"and has been summarized to preserve context window. " +
			"The full output is available in the tool call event above.]\n\n" +
			"Summarized: 3 pods found, 1 failing"
		assert.Equal(t, want, result.Content)
	})

	t.Run("stores inline conversation in LLM interaction", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Summary result"}}},
			},
		}

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens:  100,
					SummaryMaxTokenLimit: 500,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		largeContent := strings.Repeat("pod-info ", 100)
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "[user]: check pods", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)

		// Verify the LLM interaction was stored with inline conversation.
		interactions, err := execCtx.Services.Interaction.GetLLMInteractionsList(ctx, execCtx.SessionID)
		require.NoError(t, err)
		require.Len(t, interactions, 1)
		assert.Equal(t, "summarization", string(interactions[0].InteractionType))

		// Check inline conversation exists in llm_request.
		llmReq := interactions[0].LlmRequest
		convRaw, ok := llmReq["conversation"]
		require.True(t, ok, "llm_request should contain 'conversation' key")
		convSlice, ok := convRaw.([]any)
		require.True(t, ok)
		require.Len(t, convSlice, 3, "conversation should have system + user + assistant")

		// Verify roles.
		msg0, ok := convSlice[0].(map[string]any)
		require.True(t, ok, "conversation[0] should be map[string]any")
		assert.Equal(t, "system", msg0["role"])
		assert.NotEmpty(t, msg0["content"])

		msg1, ok := convSlice[1].(map[string]any)
		require.True(t, ok, "conversation[1] should be map[string]any")
		assert.Equal(t, "user", msg1["role"])
		assert.NotEmpty(t, msg1["content"])

		msg2, ok := convSlice[2].(map[string]any)
		require.True(t, ok, "conversation[2] should be map[string]any")
		assert.Equal(t, "assistant", msg2["role"])
		assert.Equal(t, "Summary result", msg2["content"])
	})

	t.Run("fail-open on LLM error", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: assert.AnError},
			},
		}

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		largeContent := strings.Repeat("data ", 200) // Exceeds threshold
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err) // No error — fail-open
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content) // Raw content returned
	})

	t.Run("fail-open on empty summary", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "   "}}}, // whitespace-only
			},
		}

		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		largeContent := strings.Repeat("data ", 200)
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content) // Raw content returned
	})
}
