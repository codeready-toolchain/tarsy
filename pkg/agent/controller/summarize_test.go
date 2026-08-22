package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
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
		pb := prompt.NewPromptBuilder(registry, nil)
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
		mockLLM := &mockLLMClient{}
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					Enabled:             config.BoolPtr(true),
					SizeThresholdTokens: 5000,
				},
			},
		})
		pb := prompt.NewPromptBuilder(registry, nil)
		execCtx := &agent.ExecutionContext{
			PromptBuilder: pb,
			LLMClient:     mockLLM,
			Config: &agent.ResolvedAgentConfig{
				LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
			},
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods", "short", "", &eventSeq)
		require.NoError(t, err)
		assert.Equal(t, "short", result.Content)
		assert.False(t, result.WasSummarized)
		assert.Equal(t, 0, mockLLM.callCount)
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
		pb := prompt.NewPromptBuilder(registry, nil)
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
		pb := prompt.NewPromptBuilder(registry, nil)
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
		pb := prompt.NewPromptBuilder(registry, nil)

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
			capture: true,
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
		pb := prompt.NewPromptBuilder(registry, nil)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb
		execCtx.Config.LLMProvider.NativeTools = map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		}

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

		require.NotNil(t, mockLLM.lastInput)
		assert.Equal(t, "test-model", mockLLM.lastInput.Config.Model)
		assert.Equal(t, config.LLMBackendLangChain, mockLLM.lastInput.Backend)
		assert.Nil(t, mockLLM.lastInput.Config.NativeTools)
		assert.Equal(t, execCtx.ExecutionID+agent.SummarizationExecutionIDSuffix, mockLLM.lastInput.ExecutionID)
		assert.True(t, execCtx.Config.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
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
		pb := prompt.NewPromptBuilder(registry, nil)

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
		assert.Equal(t, "test-model", interactions[0].ModelName)

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
		pb := prompt.NewPromptBuilder(registry, nil)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		largeContent := strings.Repeat("data ", 200) // Exceeds threshold
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err) // No error — fail-open
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content) // Raw content returned
		assert.Equal(t, 1, mockLLM.callCount, "fail-open should not retry")
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
		pb := prompt.NewPromptBuilder(registry, nil)

		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = pb

		largeContent := strings.Repeat("data ", 200)
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content) // Raw content returned
		assert.Equal(t, 1, mockLLM.callCount, "fail-open should not retry")
	})
}

func TestMaybeSummarizeResolvedProvider(t *testing.T) {
	ctx := t.Context()
	flash := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeGoogle,
		Model: "gemini-flash",
		NativeTools: map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		},
	}
	sonnet := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeVertexAI,
		Model: "claude-sonnet",
	}
	providers := config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
		"google-default":         flash,
		"vertexai-claude-sonnet": sonnet,
	})

	largeContent := strings.Repeat("pod-info ", 100)

	t.Run("above threshold uses resolved defaults provider", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Summarized output"}}},
			},
		}
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
				},
			},
		})
		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = prompt.NewPromptBuilder(registry, nil)
		execCtx.EventPublisher = noopEventPublisher{}
		execCtx.LLMProviders = providers
		execCtx.DefaultSummarization = &config.SummarizationConfig{LLMProvider: "google-default"}
		execCtx.Config.LLMProviderName = "vertexai-claude-opus"
		execCtx.Config.LLMProvider.NativeTools = map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		}

		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "[user]: check pods", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		require.NotNil(t, mockLLM.lastInput)
		assert.Equal(t, "gemini-flash", mockLLM.lastInput.Config.Model)
		assert.Equal(t, config.LLMBackendLangChain, mockLLM.lastInput.Backend)
		assert.Nil(t, mockLLM.lastInput.Config.NativeTools)
		assert.Equal(t, execCtx.ExecutionID+agent.SummarizationExecutionIDSuffix, mockLLM.lastInput.ExecutionID)
		assert.True(t, execCtx.Config.LLMProvider.NativeTools[config.GoogleNativeToolGoogleSearch],
			"investigator NativeTools must be unchanged")
		assert.Equal(t, "vertexai-claude-opus", execCtx.Config.LLMProviderName)

		interactions, err := execCtx.Services.Interaction.GetLLMInteractionsList(ctx, execCtx.SessionID)
		require.NoError(t, err)
		require.Len(t, interactions, 1)
		assert.Equal(t, "gemini-flash", interactions[0].ModelName)

		events, err := execCtx.Services.Timeline.GetSessionTimeline(ctx, execCtx.SessionID)
		require.NoError(t, err)
		var found bool
		for _, evt := range events {
			if evt.EventType == timelineevent.EventTypeMcpToolSummary {
				assert.Equal(t, "gemini-flash", evt.Metadata["summarization_model"])
				assert.Equal(t, "google-default", evt.Metadata["summarization_provider"])
				_, hasFallback := evt.Metadata["summarization_fallback"]
				assert.False(t, hasFallback)
				found = true
				break
			}
		}
		assert.True(t, found, "mcp_tool_summary timeline event should be created")
	})

	t.Run("server overlay wins over defaults", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Opus-quality summary"}}},
			},
		}
		registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
					LLMProvider:         "vertexai-claude-sonnet",
				},
			},
		})
		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = prompt.NewPromptBuilder(registry, nil)
		execCtx.LLMProviders = providers
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
			LLMBackend:  config.LLMBackendNativeGemini,
		}

		eventSeq := 0
		_, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		require.NotNil(t, mockLLM.lastInput)
		assert.Equal(t, "claude-sonnet", mockLLM.lastInput.Config.Model)
		assert.Equal(t, config.LLMBackendLangChain, mockLLM.lastInput.Backend)
	})
}

func TestMaybeSummarizeFallback(t *testing.T) {
	largeContent := strings.Repeat("pod-info ", 100)

	flash := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeGoogle,
		Model: "gemini-flash",
		NativeTools: map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		},
	}
	sonnet := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeVertexAI,
		Model: "claude-sonnet",
		NativeTools: map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		},
	}
	opus := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeVertexAI,
		Model: "claude-opus",
	}
	providers := config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
		"google-default":         flash,
		"vertexai-claude-sonnet": sonnet,
		"vertexai-claude-opus":   opus,
	})

	mcpRegistry := func() *config.MCPServerRegistry {
		return config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
			"test-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
				},
			},
			"overlay-server": {
				Summarization: &config.SummarizationConfig{
					SizeThresholdTokens: 100,
					LLMProvider:         "vertexai-claude-opus",
				},
			},
		})
	}

	fallbackList := func() []agent.ResolvedFallbackEntry {
		sonnetEntry := makeFallbackEntry("vertexai-claude-sonnet", config.LLMBackendLangChain, "claude-sonnet")
		sonnetEntry.Config.NativeTools = map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		}
		return []agent.ResolvedFallbackEntry{
			sonnetEntry,
			makeFallbackEntry("vertexai-claude-opus", config.LLMBackendLangChain, "claude-opus"),
			makeFallbackEntry("google-default", config.LLMBackendNativeGemini, "gemini-flash"),
		}
	}

	setup := func(t *testing.T, mockLLM *mockLLMClient) *agent.ExecutionContext {
		t.Helper()
		execCtx := newTestExecCtx(t, mockLLM, agent.NewStubToolExecutor(nil))
		execCtx.PromptBuilder = prompt.NewPromptBuilder(mcpRegistry(), nil)
		execCtx.EventPublisher = noopEventPublisher{}
		execCtx.LLMProviders = providers
		execCtx.DefaultSummarization = &config.SummarizationConfig{LLMProvider: "google-default"}
		execCtx.Config.LLMProviderName = "vertexai-claude-opus"
		execCtx.Config.LLMProvider = &config.LLMProviderConfig{Model: "claude-opus"}
		execCtx.Config.ResolvedFallbackProviders = fallbackList()
		return execCtx
	}

	assertNoInvestigationFallback := func(t *testing.T, execCtx *agent.ExecutionContext) {
		t.Helper()
		assert.Equal(t, "vertexai-claude-opus", execCtx.Config.LLMProviderName)
		events, err := execCtx.Services.Timeline.GetSessionTimeline(t.Context(), execCtx.SessionID)
		require.NoError(t, err)
		for _, evt := range events {
			assert.NotEqual(t, timelineevent.EventTypeProviderFallback, evt.EventType)
		}
		exec, err := execCtx.Services.Stage.GetAgentExecutionByID(t.Context(), execCtx.ExecutionID)
		require.NoError(t, err)
		assert.Nil(t, exec.OriginalLlmProvider)
	}

	t.Run("flash error uses sonnet without mutating investigator", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Sonnet summary")
		require.Len(t, mockLLM.capturedInputs, 2)
		assert.Equal(t, "gemini-flash", mockLLM.capturedInputs[0].Config.Model)
		assert.False(t, mockLLM.capturedInputs[0].ClearCache)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[1].Config.Model)
		assert.Equal(t, config.LLMBackendLangChain, mockLLM.capturedInputs[1].Backend)
		assert.Nil(t, mockLLM.capturedInputs[1].Config.NativeTools)
		assert.True(t, mockLLM.capturedInputs[1].ClearCache)
		assert.Equal(t, execCtx.ExecutionID+agent.SummarizationExecutionIDSuffix, mockLLM.capturedInputs[1].ExecutionID)
		assert.True(t, sonnet.NativeTools[config.GoogleNativeToolGoogleSearch])

		sticky, ok := execCtx.SummarizationSticky["google-default"]
		require.True(t, ok)
		assert.Equal(t, "vertexai-claude-sonnet", sticky.ProviderName)

		assertNoInvestigationFallback(t, execCtx)

		events, err := execCtx.Services.Timeline.GetSessionTimeline(t.Context(), execCtx.SessionID)
		require.NoError(t, err)
		var found bool
		for _, evt := range events {
			if evt.EventType == timelineevent.EventTypeMcpToolSummary {
				assert.Equal(t, "claude-sonnet", evt.Metadata["summarization_model"])
				assert.Equal(t, "vertexai-claude-sonnet", evt.Metadata["summarization_provider"])
				assert.Equal(t, true, evt.Metadata["summarization_fallback"])
				found = true
				break
			}
		}
		assert.True(t, found, "successful mcp_tool_summary should record summarization_fallback")

		interactions, err := execCtx.Services.Interaction.GetLLMInteractionsList(t.Context(), execCtx.SessionID)
		require.NoError(t, err)
		require.Len(t, interactions, 1)
		assert.Equal(t, "claude-sonnet", interactions[0].ModelName)
	})

	t.Run("sticky skips failed primary on later call", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "First sonnet summary"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Second sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		_, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		require.Equal(t, 2, mockLLM.callCount)

		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Second sonnet summary")
		require.Len(t, mockLLM.capturedInputs, 3)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[2].Config.Model)
		assert.False(t, mockLLM.capturedInputs[2].ClearCache)
		assert.Equal(t, execCtx.ExecutionID+agent.SummarizationExecutionIDSuffix, mockLLM.capturedInputs[2].ExecutionID)
	})

	t.Run("overlay primary is independent of flash sticky", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Opus overlay summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		_, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)

		result, err := maybeSummarize(t.Context(), execCtx, "overlay-server", "get_secrets",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Opus overlay summary")
		require.Len(t, mockLLM.capturedInputs, 3)
		assert.Equal(t, "claude-opus", mockLLM.capturedInputs[2].Config.Model)
		assert.False(t, mockLLM.capturedInputs[2].ClearCache)
	})

	t.Run("sticky failure continues forward not back to flash", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Opus summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		_, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)

		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Opus summary")
		require.Len(t, mockLLM.capturedInputs, 4)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[2].Config.Model)
		assert.False(t, mockLLM.capturedInputs[2].ClearCache)
		assert.Equal(t, "claude-opus", mockLLM.capturedInputs[3].Config.Model)
		assert.True(t, mockLLM.capturedInputs[3].ClearCache)
		assert.Equal(t, execCtx.ExecutionID+agent.SummarizationExecutionIDSuffix, mockLLM.capturedInputs[3].ExecutionID)
		assert.NotEqual(t, "gemini-flash", mockLLM.capturedInputs[2].Config.Model)
		assert.NotEqual(t, "gemini-flash", mockLLM.capturedInputs[3].Config.Model)

		sticky, ok := execCtx.SummarizationSticky["google-default"]
		require.True(t, ok)
		assert.Equal(t, "vertexai-claude-opus", sticky.ProviderName)
	})

	t.Run("exhausted list fail-opens", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{err: assert.AnError},
				{err: assert.AnError},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content)
		assert.Equal(t, 3, mockLLM.callCount, "primary + sonnet + opus; google-default skipped")
		assertNoInvestigationFallback(t, execCtx)
	})

	t.Run("requires native tools still uses claude fallback", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		execCtx.Config.RequiresNativeTools = true
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		require.Len(t, mockLLM.capturedInputs, 2)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[1].Config.Model)
		assert.Equal(t, config.LLMBackendLangChain, mockLLM.capturedInputs[1].Backend)
	})

	t.Run("cancelled context does not walk fallbacks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "should not run"}}},
			},
			onGenerate: func(int) { cancel() },
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		result, err := maybeSummarize(ctx, execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.False(t, result.WasSummarized)
		assert.Equal(t, largeContent, result.Content)
		assert.Equal(t, 1, mockLLM.callCount)
	})

	t.Run("unset summarization provider walks agent fallbacks", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: assert.AnError},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		execCtx.DefaultSummarization = nil
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		require.Len(t, mockLLM.capturedInputs, 2)
		assert.Equal(t, "claude-opus", mockLLM.capturedInputs[0].Config.Model)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[1].Config.Model)
		assert.True(t, mockLLM.capturedInputs[1].ClearCache)
		assert.Equal(t, "vertexai-claude-opus", execCtx.Config.LLMProviderName)
		sticky, ok := execCtx.SummarizationSticky["vertexai-claude-opus"]
		require.True(t, ok)
		assert.Equal(t, "vertexai-claude-sonnet", sticky.ProviderName)
	})

	t.Run("empty summary walks fallbacks", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "   "}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Sonnet summary")
		require.Len(t, mockLLM.capturedInputs, 2)
		assert.Equal(t, "gemini-flash", mockLLM.capturedInputs[0].Config.Model)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[1].Config.Model)
	})

	t.Run("unknown named provider walks fallbacks without generate", func(t *testing.T) {
		mockLLM := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "Sonnet summary"}}},
			},
		}
		execCtx := setup(t, mockLLM)
		execCtx.DefaultSummarization = &config.SummarizationConfig{LLMProvider: "missing-provider"}
		eventSeq := 0
		result, err := maybeSummarize(t.Context(), execCtx, "test-server", "get_pods",
			largeContent, "", &eventSeq)
		require.NoError(t, err)
		assert.True(t, result.WasSummarized)
		assert.Contains(t, result.Content, "Sonnet summary")
		require.Len(t, mockLLM.capturedInputs, 1)
		assert.Equal(t, "claude-sonnet", mockLLM.capturedInputs[0].Config.Model)
		assert.True(t, mockLLM.capturedInputs[0].ClearCache)
		assert.Equal(t, "vertexai-claude-opus", execCtx.Config.LLMProviderName)
	})
}
