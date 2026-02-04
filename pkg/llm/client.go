package llm

import (
	"fmt"
)

// Client represents the LLM gRPC client
// NOTE: This is a minimal stub for Phase 2.1
// Will be properly implemented in Phase 2.3
type Client struct {
	addr string
}

// NewClient creates a new LLM client
func NewClient(addr string) (*Client, error) {
	// Stub - just store the address for now
	return &Client{addr: addr}, nil
}

// Close closes the client connection
func (c *Client) Close() error {
	fmt.Println("LLM client closed (stub)")
	return nil
}
