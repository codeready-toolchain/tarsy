package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// Compile-time check that ToolExecutor implements agent.ToolExecutor.
var _ agent.ToolExecutor = (*ToolExecutor)(nil)

// ToolRecallPastInvestigations is the tool name for on-demand memory search.
const ToolRecallPastInvestigations = "recall_past_investigations"

// IsMemoryTool reports whether name is a known memory tool.
func IsMemoryTool(name string) bool {
	return name == ToolRecallPastInvestigations
}

// recallTool is the tool definition exposed to the LLM.
var recallTool = agent.ToolDefinition{
	Name:        ToolRecallPastInvestigations,
	Description: "Search distilled knowledge from past investigations — reusable patterns, procedures, environment quirks, and anti-patterns. Use for situational questions like 'what do we know about this type of workload?', 'how should I handle this category of alert?', or 'what are common false positives here?'. Returns generalized learnings, NOT specific investigation history — will not find particular users, namespaces, or session details.",
	ParametersSchema: `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "What you want to recall — describe the situation, pattern, or question"
    },
    "limit": {
      "type": "integer",
      "description": "Max results to return (default: 10, max: 20)",
      "default": 10
    }
  },
  "required": ["query"]
}`,
}

const (
	recallDefaultLimit = 10
	recallMaxLimit     = 20
)

// ToolExecutor wraps an inner agent.ToolExecutor and intercepts
// recall_past_investigations calls. Everything else passes through.
type ToolExecutor struct {
	inner      agent.ToolExecutor
	service    *Service
	project    string
	alertType  *string
	chainID    *string
	excludeIDs map[string]struct{}
}

// NewToolExecutor creates a memory tool executor.
// inner may be nil (safely handled). excludeIDs contains memory IDs already
// auto-injected into the prompt — they are filtered from tool results.
func NewToolExecutor(
	inner agent.ToolExecutor,
	service *Service,
	project string,
	alertType *string,
	chainID *string,
	excludeIDs map[string]struct{},
) *ToolExecutor {
	return &ToolExecutor{
		inner:      inner,
		service:    service,
		project:    project,
		alertType:  alertType,
		chainID:    chainID,
		excludeIDs: excludeIDs,
	}
}

// ListTools returns the combined tool set: recall_past_investigations + inner tools.
func (te *ToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
	tools := []agent.ToolDefinition{recallTool}

	if te.inner != nil {
		innerTools, err := te.inner.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list inner tools: %w", err)
		}
		for _, t := range innerTools {
			if t.Name == ToolRecallPastInvestigations {
				continue
			}
			tools = append(tools, t)
		}
	}

	return tools, nil
}

// Execute routes the call to recall_past_investigations or the inner executor.
func (te *ToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	if call.Name == ToolRecallPastInvestigations {
		return te.executeRecall(ctx, call)
	}
	if te.inner != nil {
		return te.inner.Execute(ctx, call)
	}
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: fmt.Sprintf("unknown tool: %s", call.Name),
		IsError: true,
	}, nil
}

// Close delegates to the inner executor.
func (te *ToolExecutor) Close() error {
	if te.inner != nil {
		return te.inner.Close()
	}
	return nil
}

func (te *ToolExecutor) executeRecall(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("invalid arguments: %v", err),
			IsError: true,
		}, nil
	}
	if args.Query == "" {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "'query' is required and must be non-empty",
			IsError: true,
		}, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = recallDefaultLimit
	}
	if limit > recallMaxLimit {
		limit = recallMaxLimit
	}

	// Fetch extra candidates so we can filter out already-injected IDs
	fetchLimit := limit + len(te.excludeIDs)
	memories, err := te.service.FindSimilarWithBoosts(
		ctx, te.project, args.Query, te.alertType, te.chainID, fetchLimit,
	)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("memory search failed: %v", err),
			IsError: true,
		}, nil
	}

	// Filter out already-injected memories and apply limit
	var filtered []Memory
	for _, m := range memories {
		if _, excluded := te.excludeIDs[m.ID]; excluded {
			continue
		}
		filtered = append(filtered, m)
		if len(filtered) >= limit {
			break
		}
	}

	if len(filtered) == 0 {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "No relevant memories found for this query.",
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n", len(filtered)))
	for i, m := range filtered {
		age := FormatMemoryAge(m.CreatedAt, m.UpdatedAt)
		sb.WriteString(fmt.Sprintf("\n%d. [%s, %s, score: %.2f, %s] %s", i+1, m.Category, m.Valence, m.Score, age, m.Content))
	}

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: sb.String(),
	}, nil
}
