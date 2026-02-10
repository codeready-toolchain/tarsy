package mcp

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected RecoveryAction
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: NoRetry,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: NoRetry,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: NoRetry,
		},
		{
			name:     "wrapped context canceled",
			err:      errors.Join(errors.New("call failed"), context.Canceled),
			expected: NoRetry,
		},
		{
			name:     "io.EOF - connection",
			err:      io.EOF,
			expected: RetryNewSession,
		},
		{
			name:     "io.ErrUnexpectedEOF",
			err:      io.ErrUnexpectedEOF,
			expected: RetryNewSession,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp 127.0.0.1:8080: connection refused"),
			expected: RetryNewSession,
		},
		{
			name:     "connection reset",
			err:      errors.New("read tcp: connection reset by peer"),
			expected: RetryNewSession,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			expected: RetryNewSession,
		},
		{
			name:     "connection closed",
			err:      errors.New("use of closed network connection"),
			expected: NoRetry, // This is a generic error, not matched by connectionErrors
		},
		{
			name:     "MCP method not found",
			err:      errors.New("JSON-RPC error: method not found"),
			expected: NoRetry,
		},
		{
			name:     "MCP invalid params",
			err:      errors.New("invalid params: missing required field"),
			expected: NoRetry,
		},
		{
			name:     "unknown error",
			err:      errors.New("something unexpected happened"),
			expected: NoRetry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// mockNetError implements net.Error for testing.
type mockNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *mockNetError) Error() string   { return e.msg }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

// Ensure mockNetError implements net.Error at compile time.
var _ net.Error = (*mockNetError)(nil)

func TestClassifyError_NetError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected RecoveryAction
	}{
		{
			name:     "net timeout",
			err:      &mockNetError{msg: "i/o timeout", timeout: true},
			expected: NoRetry,
		},
		{
			name:     "net non-timeout",
			err:      &mockNetError{msg: "connection refused", timeout: false},
			expected: RetryNewSession,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
