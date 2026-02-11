package api

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestIsChatAvailable(t *testing.T) {
	enabledChain := &config.ChainConfig{
		Chat: &config.ChatConfig{Enabled: true},
	}
	disabledChain := &config.ChainConfig{
		Chat: &config.ChatConfig{Enabled: false},
	}
	noChatChain := &config.ChainConfig{}

	tests := []struct {
		name          string
		sessionStatus alertsession.Status
		chain         *config.ChainConfig
		wantEmpty     bool   // true means chat IS available
		wantContains  string // substring in the reason if not available
	}{
		{
			name:          "completed session with chat enabled",
			sessionStatus: alertsession.StatusCompleted,
			chain:         enabledChain,
			wantEmpty:     true,
		},
		{
			name:          "failed session with chat enabled",
			sessionStatus: alertsession.StatusFailed,
			chain:         enabledChain,
			wantEmpty:     true,
		},
		{
			name:          "timed_out session with chat enabled",
			sessionStatus: alertsession.StatusTimedOut,
			chain:         enabledChain,
			wantEmpty:     true,
		},
		{
			name:          "pending session",
			sessionStatus: alertsession.StatusPending,
			chain:         enabledChain,
			wantContains:  "still processing",
		},
		{
			name:          "in_progress session",
			sessionStatus: alertsession.StatusInProgress,
			chain:         enabledChain,
			wantContains:  "still processing",
		},
		{
			name:          "cancelling session",
			sessionStatus: alertsession.StatusCancelling,
			chain:         enabledChain,
			wantContains:  "being cancelled",
		},
		{
			name:          "cancelled session",
			sessionStatus: alertsession.StatusCancelled,
			chain:         enabledChain,
			wantContains:  "cancelled sessions",
		},
		{
			name:          "chat disabled in chain",
			sessionStatus: alertsession.StatusCompleted,
			chain:         disabledChain,
			wantContains:  "not enabled",
		},
		{
			name:          "no chat config in chain",
			sessionStatus: alertsession.StatusCompleted,
			chain:         noChatChain,
			wantContains:  "not enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isChatAvailable(tt.sessionStatus, tt.chain)
			if tt.wantEmpty {
				assert.Empty(t, result, "expected chat to be available")
			} else {
				assert.NotEmpty(t, result, "expected chat to be unavailable")
				assert.Contains(t, result, tt.wantContains)
			}
		})
	}
}
