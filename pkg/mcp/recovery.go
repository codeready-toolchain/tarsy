package mcp

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// RecoveryAction determines how to handle an MCP operation failure.
type RecoveryAction int

const (
	// NoRetry — the error is not recoverable (bad request, auth failure, timeout).
	NoRetry RecoveryAction = iota
	// RetrySameSession — transient error, retry with existing session (rate limit).
	// Reserved for future use: ClassifyError does not currently return this value.
	// Intended for rate-limit / throttle errors once server-side rate limiting is detected.
	RetrySameSession
	// RetryNewSession — transport failure, recreate session and retry.
	RetryNewSession
)

// Recovery configuration constants.
const (
	// MaxRetries is the number of retry attempts after the initial failure.
	MaxRetries = 1

	// ReinitTimeout is the deadline for recreating an MCP session during recovery.
	ReinitTimeout = 10 * time.Second

	// OperationTimeout is the per-call deadline for CallTool and ListTools.
	// Set conservatively: some tools are legitimately slow. The iteration timeout
	// (120s) is the hard ceiling above this.
	OperationTimeout = 90 * time.Second

	// RetryBackoffMin is the minimum jittered backoff between retries.
	RetryBackoffMin = 250 * time.Millisecond

	// RetryBackoffMax is the maximum jittered backoff between retries.
	RetryBackoffMax = 750 * time.Millisecond

	// MCPInitTimeout is the per-server initialization timeout (transport + handshake).
	MCPInitTimeout = 30 * time.Second

	// MCPHealthPingTimeout is the health check ping timeout.
	MCPHealthPingTimeout = 5 * time.Second

	// MCPHealthInterval is the health check loop interval.
	MCPHealthInterval = 15 * time.Second
)

// ClassifyError determines the recovery action for an MCP operation error.
func ClassifyError(err error) RecoveryAction {
	if err == nil {
		return NoRetry
	}

	// Context errors — no retry
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NoRetry
	}

	// Network errors — check timeout vs connection
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return NoRetry // Timeout: don't retry (could be slow server)
		}
		return RetryNewSession
	}

	// Connection-level errors — retry with new session
	if isConnectionError(err) {
		return RetryNewSession
	}

	// MCP JSON-RPC errors — generally not retryable
	if isMCPProtocolError(err) {
		return NoRetry
	}

	// Default: no retry (unknown errors are not safe to retry)
	return NoRetry
}

// isConnectionError detects connection-level transport failures.
func isConnectionError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}

	msg := err.Error()
	connectionErrors := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"connection closed",
		"no such host",
	}
	for _, e := range connectionErrors {
		if strings.Contains(strings.ToLower(msg), e) {
			return true
		}
	}
	return false
}

// isMCPProtocolError detects MCP JSON-RPC protocol errors from the SDK.
// Uses the SDK's typed jsonrpc.Error (WireError) with standard JSON-RPC 2.0
// error codes rather than string matching for robustness.
func isMCPProtocolError(err error) bool {
	var wireErr *jsonrpc.Error
	if !errors.As(err, &wireErr) {
		return false
	}
	switch wireErr.Code {
	case jsonrpc.CodeParseError,
		jsonrpc.CodeInvalidRequest,
		jsonrpc.CodeMethodNotFound,
		jsonrpc.CodeInvalidParams:
		return true
	default:
		return false
	}
}
