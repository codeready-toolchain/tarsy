package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
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
			name:          "chat explicitly disabled in chain",
			sessionStatus: alertsession.StatusCompleted,
			chain:         disabledChain,
			wantContains:  "not enabled",
		},
		{
			name:          "no chat config in chain (enabled by default)",
			sessionStatus: alertsession.StatusCompleted,
			chain:         noChatChain,
			wantEmpty:     true,
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

func TestMapChatExecutorError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantSubstr string
	}{
		{
			name:       "ErrChatExecutionActive maps to 409",
			err:        queue.ErrChatExecutionActive,
			wantCode:   http.StatusConflict,
			wantSubstr: "already being generated",
		},
		{
			name:       "wrapped ErrChatExecutionActive maps to 409",
			err:        fmt.Errorf("submit failed: %w", queue.ErrChatExecutionActive),
			wantCode:   http.StatusConflict,
			wantSubstr: "already being generated",
		},
		{
			name:       "ErrShuttingDown maps to 503",
			err:        queue.ErrShuttingDown,
			wantCode:   http.StatusServiceUnavailable,
			wantSubstr: "shutting down",
		},
		{
			name:       "ValidationError maps to 400",
			err:        services.NewValidationError("content", "required"),
			wantCode:   http.StatusBadRequest,
			wantSubstr: "content",
		},
		{
			name:       "unknown error maps to 500",
			err:        errors.New("unexpected failure"),
			wantCode:   http.StatusInternalServerError,
			wantSubstr: "failed to process",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpErr := mapChatExecutorError(tt.err)
			assert.Equal(t, tt.wantCode, httpErr.Code)
			assert.Contains(t, fmt.Sprintf("%v", httpErr.Message), tt.wantSubstr)
		})
	}
}
