package agent

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSummarizationLLM(t *testing.T) {
	agentNative := map[config.GoogleNativeTool]bool{
		config.GoogleNativeToolGoogleSearch: true,
		config.GoogleNativeToolURLContext:   true,
	}
	agentProvider := &config.LLMProviderConfig{
		Type:        config.LLMProviderTypeGoogle,
		Model:       "claude-opus",
		NativeTools: agentNative,
	}

	flashNative := map[config.GoogleNativeTool]bool{
		config.GoogleNativeToolGoogleSearch: true,
		config.GoogleNativeToolURLContext:   true,
	}
	flashProvider := &config.LLMProviderConfig{
		Type:        config.LLMProviderTypeGoogle,
		Model:       "gemini-3.7-flash",
		NativeTools: flashNative,
	}
	sonnetProvider := &config.LLMProviderConfig{
		Type:  config.LLMProviderTypeVertexAI,
		Model: "claude-sonnet",
	}

	registry := config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
		"google-default":         flashProvider,
		"vertexai-claude-sonnet": sonnetProvider,
	})

	baseExec := func() *ExecutionContext {
		return &ExecutionContext{
			Config: &ResolvedAgentConfig{
				LLMProvider:     agentProvider,
				LLMProviderName: "vertexai-claude-opus",
				LLMBackend:      config.LLMBackendLangChain,
			},
			LLMProviders: registry,
		}
	}

	t.Run("nil defaults and nil server uses agent name and backend", func(t *testing.T) {
		execCtx := baseExec()
		got, err := ResolveSummarizationLLM(execCtx, nil)
		require.NoError(t, err)
		assert.Equal(t, "vertexai-claude-opus", got.ProviderName)
		assert.Equal(t, config.LLMBackendLangChain, got.Backend)
		require.NotNil(t, got.Provider)
		assert.Equal(t, "claude-opus", got.Provider.Model)
		assert.Nil(t, got.Provider.NativeTools)
		assert.NotSame(t, agentProvider, got.Provider)
		assert.True(t, agentProvider.NativeTools[config.GoogleNativeToolGoogleSearch],
			"agent NativeTools map must not be mutated")
		regFlash, err := registry.Get("google-default")
		require.NoError(t, err)
		assert.True(t, regFlash.NativeTools[config.GoogleNativeToolGoogleSearch],
			"registry NativeTools map must not be mutated")
	})

	t.Run("defaults google-default omit backend uses langchain", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
		}
		got, err := ResolveSummarizationLLM(execCtx, nil)
		require.NoError(t, err)
		assert.Equal(t, "google-default", got.ProviderName)
		assert.Equal(t, config.LLMBackendLangChain, got.Backend)
		assert.Equal(t, "gemini-3.7-flash", got.Provider.Model)
		assert.Nil(t, got.Provider.NativeTools)
		assert.True(t, flashProvider.NativeTools[config.GoogleNativeToolGoogleSearch])
	})

	t.Run("defaults google-native uses google-native", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
			LLMBackend:  config.LLMBackendNativeGemini,
		}
		got, err := ResolveSummarizationLLM(execCtx, nil)
		require.NoError(t, err)
		assert.Equal(t, "google-default", got.ProviderName)
		assert.Equal(t, config.LLMBackendNativeGemini, got.Backend)
		assert.Nil(t, got.Provider.NativeTools)
	})

	t.Run("server overlay wins", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
			LLMBackend:  config.LLMBackendNativeGemini,
		}
		got, err := ResolveSummarizationLLM(execCtx, &config.SummarizationConfig{
			LLMProvider: "vertexai-claude-sonnet",
			LLMBackend:  config.LLMBackendLangChain,
		})
		require.NoError(t, err)
		assert.Equal(t, "vertexai-claude-sonnet", got.ProviderName)
		assert.Equal(t, config.LLMBackendLangChain, got.Backend)
		assert.Equal(t, "claude-sonnet", got.Provider.Model)
	})

	t.Run("omitted server backend is langchain not defaults google-native", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
			LLMBackend:  config.LLMBackendNativeGemini,
		}
		got, err := ResolveSummarizationLLM(execCtx, &config.SummarizationConfig{
			LLMProvider: "vertexai-claude-sonnet",
		})
		require.NoError(t, err)
		assert.Equal(t, "vertexai-claude-sonnet", got.ProviderName)
		assert.Equal(t, config.LLMBackendLangChain, got.Backend)
	})

	t.Run("unknown name returns error", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "does-not-exist",
		}
		_, err := ResolveSummarizationLLM(execCtx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does-not-exist")
	})

	t.Run("named provider with nil registry returns error", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.LLMProviders = nil
		execCtx.DefaultSummarization = &config.SummarizationConfig{
			LLMProvider: "google-default",
		}
		_, err := ResolveSummarizationLLM(execCtx, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "provider registry is not available")
	})

	t.Run("empty agent backend becomes langchain", func(t *testing.T) {
		execCtx := baseExec()
		execCtx.Config.LLMBackend = ""
		got, err := ResolveSummarizationLLM(execCtx, nil)
		require.NoError(t, err)
		assert.Equal(t, config.LLMBackendLangChain, got.Backend)
	})
}
