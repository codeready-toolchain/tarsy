package queue

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
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
