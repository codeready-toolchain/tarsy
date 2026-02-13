package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// LLMScriptEntry defines a single scripted LLM response.
type LLMScriptEntry struct {
	// Response content (exactly one must be set)
	Chunks []agent.Chunk // Pre-built chunks to return
	Text   string        // Shorthand: auto-wrapped as TextChunk + UsageChunk
	Error  error         // Return error from Generate()

	// Test control
	BlockUntilCancelled bool            // Block Generate() until ctx is cancelled
	WaitCh              <-chan struct{} // Block Generate() until closed, then return normal response
	OnBlock             chan<- struct{} // Notified when Generate() enters its blocking path (BlockUntilCancelled or WaitCh)
}

// ScriptedLLMClient implements agent.LLMClient with a dual-dispatch mock:
// sequential fallback for single-agent stages, plus agent-aware routing for
// parallel stages where call order is non-deterministic.
type ScriptedLLMClient struct {
	mu             sync.Mutex
	sequential     []LLMScriptEntry // consumed in order for non-routed calls
	seqIndex       int
	routes         map[string][]LLMScriptEntry // agentName → per-agent script
	routeIndex     map[string]int              // agentName → current index
	capturedInputs []*agent.GenerateInput
}

// NewScriptedLLMClient creates a new ScriptedLLMClient.
func NewScriptedLLMClient() *ScriptedLLMClient {
	return &ScriptedLLMClient{
		routes:     make(map[string][]LLMScriptEntry),
		routeIndex: make(map[string]int),
	}
}

// AddSequential adds an entry consumed in order for non-routed calls.
// Used for single-agent stages, synthesis, executive summary, chat, summarization, etc.
func (c *ScriptedLLMClient) AddSequential(entry LLMScriptEntry) {
	c.sequential = append(c.sequential, entry)
}

// AddRouted adds an entry for a specific agent name (matched from system prompt).
// Used for parallel stages where agents need differentiated responses.
func (c *ScriptedLLMClient) AddRouted(agentName string, entry LLMScriptEntry) {
	c.routes[agentName] = append(c.routes[agentName], entry)
}

// Generate implements agent.LLMClient.
func (c *ScriptedLLMClient) Generate(ctx context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
	c.mu.Lock()
	c.capturedInputs = append(c.capturedInputs, input)

	// Determine which entry to use: try routed first, then sequential.
	entry, err := c.nextEntry(input)
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}

	// Handle BlockUntilCancelled: wait for context cancellation.
	if entry.BlockUntilCancelled {
		ch := make(chan agent.Chunk)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		if entry.OnBlock != nil {
			entry.OnBlock <- struct{}{}
		}
		return ch, nil
	}

	// Handle WaitCh: block until released, then continue with normal response.
	if entry.WaitCh != nil {
		if entry.OnBlock != nil {
			entry.OnBlock <- struct{}{}
		}
		select {
		case <-entry.WaitCh:
			// Released — fall through to send chunks normally
		case <-ctx.Done():
			ch := make(chan agent.Chunk)
			close(ch)
			return ch, nil
		}
	}

	// Handle error entries.
	if entry.Error != nil {
		return nil, entry.Error
	}

	// Build chunks from entry.
	chunks := entry.Chunks
	if len(chunks) == 0 && entry.Text != "" {
		chunks = []agent.Chunk{
			&agent.TextChunk{Content: entry.Text},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}
	}

	ch := make(chan agent.Chunk, len(chunks))
	for _, chunk := range chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

// Close implements agent.LLMClient.
func (c *ScriptedLLMClient) Close() error { return nil }

// CallCount returns the total number of Generate() calls made.
func (c *ScriptedLLMClient) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.capturedInputs)
}

// nextEntry selects the next script entry using dual dispatch.
// Must be called with c.mu held.
func (c *ScriptedLLMClient) nextEntry(input *agent.GenerateInput) (*LLMScriptEntry, error) {
	// Extract agent name from system prompt for routing.
	agentName := extractAgentName(input)

	// Try routed dispatch first.
	if agentName != "" {
		if entries, ok := c.routes[agentName]; ok {
			idx := c.routeIndex[agentName]
			if idx < len(entries) {
				c.routeIndex[agentName] = idx + 1
				return &entries[idx], nil
			}
		}
	}

	// Fall back to sequential dispatch.
	if c.seqIndex < len(c.sequential) {
		entry := &c.sequential[c.seqIndex]
		c.seqIndex++
		return entry, nil
	}

	return nil, fmt.Errorf("ScriptedLLMClient: no more entries (agent=%q, sequential=%d/%d)",
		agentName, c.seqIndex, len(c.sequential))
}

// extractAgentName extracts the agent name from the system prompt's
// custom instructions section. The prompt builder places custom instructions
// under "## Agent-Specific Instructions", so we look for "You are <Name>"
// within that section to avoid matching the generic "You are an expert SRE"
// from the general instructions.
func extractAgentName(input *agent.GenerateInput) string {
	for _, msg := range input.Messages {
		if msg.Role == agent.RoleSystem {
			content := msg.Content
			// Look within Agent-Specific Instructions section first.
			if idx := strings.Index(content, "## Agent-Specific Instructions"); idx >= 0 {
				content = content[idx:]
			}
			// Find "You are <Name>" in the narrowed content.
			if idx := strings.Index(content, "You are "); idx >= 0 {
				rest := content[idx+len("You are "):]
				end := len(rest)
				for i, ch := range rest {
					if ch == '.' || ch == ',' || ch == '\n' {
						end = i
						break
					}
				}
				name := strings.TrimSpace(rest[:end])
				if name != "" {
					return name
				}
			}
			break
		}
	}
	return ""
}
