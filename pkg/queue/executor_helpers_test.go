package queue

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/stretchr/testify/require"
)

func TestResolveChatSubAgents(t *testing.T) {
	t.Parallel()

	chainRefs := config.SubAgentRefs{{Name: "ChainSub"}}
	chatRefs := config.SubAgentRefs{{Name: "ChatSub"}}

	t.Run("chat_overrides_chain", func(t *testing.T) {
		t.Parallel()
		chain := &config.ChainConfig{SubAgents: chainRefs}
		chat := &config.ChatConfig{SubAgents: chatRefs}
		got := resolveChatSubAgents(chain, chat)
		require.Len(t, got, 1)
		require.Equal(t, "ChatSub", got[0].Name)
	})

	t.Run("chain_when_chat_nil", func(t *testing.T) {
		t.Parallel()
		chain := &config.ChainConfig{SubAgents: chainRefs}
		got := resolveChatSubAgents(chain, nil)
		require.Len(t, got, 1)
		require.Equal(t, "ChainSub", got[0].Name)
	})

	t.Run("chain_when_chat_empty_sub_agents", func(t *testing.T) {
		t.Parallel()
		chain := &config.ChainConfig{SubAgents: chainRefs}
		chat := &config.ChatConfig{}
		got := resolveChatSubAgents(chain, chat)
		require.Len(t, got, 1)
		require.Equal(t, "ChainSub", got[0].Name)
	})

	t.Run("chain_when_chat_sub_agents_empty_slice", func(t *testing.T) {
		t.Parallel()
		chain := &config.ChainConfig{SubAgents: chainRefs}
		chat := &config.ChatConfig{SubAgents: config.SubAgentRefs{}}
		got := resolveChatSubAgents(chain, chat)
		require.Len(t, got, 1)
		require.Equal(t, "ChainSub", got[0].Name)
	})

	t.Run("nil_when_no_refs", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, resolveChatSubAgents(&config.ChainConfig{}, &config.ChatConfig{}))
		require.Nil(t, resolveChatSubAgents(nil, nil))
	})
}

func TestApplyNativeToolsOverrideKeepsReasoningEffort(t *testing.T) {
	t.Parallel()

	orig := &config.LLMProviderConfig{
		Type:            config.LLMProviderTypeGoogle,
		Model:           "gemini-3.8-flash",
		ReasoningEffort: config.ReasoningEffortHigh,
		NativeTools: map[config.GoogleNativeTool]bool{
			config.GoogleNativeToolGoogleSearch: true,
		},
	}
	resolved := &agent.ResolvedAgentConfig{LLMProvider: orig}

	applyNativeToolsOverride(resolved, &models.NativeToolsConfig{
		CodeExecution: new(true),
	})

	require.NotSame(t, orig, resolved.LLMProvider)
	require.Equal(t, config.ReasoningEffortHigh, resolved.LLMProvider.ReasoningEffort)
	require.Equal(t, config.ReasoningEffortHigh, orig.ReasoningEffort)
	require.Equal(t, map[config.GoogleNativeTool]bool{
		config.GoogleNativeToolGoogleSearch:  true,
		config.GoogleNativeToolCodeExecution: true,
	}, resolved.LLMProvider.NativeTools)
	require.Equal(t, map[config.GoogleNativeTool]bool{
		config.GoogleNativeToolGoogleSearch: true,
	}, orig.NativeTools)

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		resolved := &agent.ResolvedAgentConfig{}
		applyNativeToolsOverride(resolved, &models.NativeToolsConfig{GoogleSearch: new(false)})
		require.Nil(t, resolved.LLMProvider)
	})
}
