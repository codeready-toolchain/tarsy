package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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
			name:     "connection closed string (not sentinel)",
			err:      errors.New("use of closed network connection"),
			expected: NoRetry, // errors.New creates a distinct error, not net.ErrClosed
		},
		{
			name:     "net.ErrClosed sentinel",
			err:      net.ErrClosed,
			expected: RetryNewSession,
		},
		{
			name:     "wrapped net.ErrClosed",
			err:      fmt.Errorf("operation failed: %w", net.ErrClosed),
			expected: RetryNewSession,
		},
		{
			name:     "MCP method not found (typed)",
			err:      &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "method not found"},
			expected: NoRetry,
		},
		{
			name:     "MCP invalid params (typed)",
			err:      &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid params"},
			expected: NoRetry,
		},
		{
			name:     "MCP parse error (typed)",
			err:      &jsonrpc.Error{Code: jsonrpc.CodeParseError, Message: "parse error"},
			expected: NoRetry,
		},
		{
			name:     "wrapped MCP error",
			err:      fmt.Errorf("call failed: %w", &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "invalid request"}),
			expected: NoRetry,
		},
		{
			name:     "MCP protocol error string (not typed)",
			err:      errors.New("JSON-RPC error: method not found"),
			expected: NoRetry, // Not a typed error, falls to default NoRetry
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
