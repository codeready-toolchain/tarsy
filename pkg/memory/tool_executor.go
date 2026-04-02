package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
)

// Compile-time check that ToolExecutor implements agent.ToolExecutor.
var _ agent.ToolExecutor = (*ToolExecutor)(nil)

// ToolRecallPastInvestigations / ToolSearchPastSessions — wire names from pkg/builtintools.
const (
	ToolRecallPastInvestigations = builtintools.RecallPastInvestigations
	ToolSearchPastSessions       = builtintools.SearchPastSessions
)

// IsMemoryTool reports whether name is a known memory tool.
func IsMemoryTool(name string) bool {
	k, ok := builtintools.KindForPlainTool(name)
	return ok && k == builtintools.KindMemory
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

var searchSessionsTool = agent.ToolDefinition{
	Name: ToolSearchPastSessions,
	Description: `Search past investigation sessions by keywords in alert data. Use to check if a specific entity (e.g. user, namespace, workload, IP, service) was investigated before — critical for escalation and pattern-of-behavior decisions. Pass specific identifiers as the query — one or a few key terms per call. For unrelated identifiers, make separate calls. Do NOT pass natural language sentences. Returns a focused summary of matching investigations including conclusions, quality assessments, and human review corrections.

Good queries: 'john-doe' (single identifier), 'john-doe my-namespace' (both must match — narrows results), 'nginx-proxy', 'coolify'
Bad queries: 'check if user john-doe was investigated before in my-namespace' (natural language — every word must match, will return nothing)`,
	ParametersSchema: `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Specific identifiers to search for — short terms, not natural language"
    },
    "alert_type": {
      "type": "string",
      "description": "Filter by alert type (optional)"
    },
    "days_back": {
      "type": "integer",
      "description": "How far back to search in days (default: 30)",
      "default": 30
    },
    "limit": {
      "type": "integer",
      "description": "Max sessions to return (default: 5, max: 10)",
      "default": 5
    }
  },
  "required": ["query"]
}`,
}

const (
	recallDefaultLimit        = 10
	recallMaxLimit            = 20
	sessionSearchDefaultLimit = 5
	sessionSearchMaxLimit     = 10
	sessionSearchDefaultDays  = 30
)

// ToolExecutor wraps an inner agent.ToolExecutor and intercepts
// memory tool calls. Everything else passes through.
type ToolExecutor struct {
	inner      agent.ToolExecutor
	service    *Service
	sessionID  string
	project    string
	excludeIDs map[string]struct{}
}

// NewToolExecutor creates a memory tool executor.
// inner may be nil (safely handled). sessionID is the current session
// (excluded from search_past_sessions results to avoid returning itself).
// excludeIDs contains memory IDs already auto-injected into the prompt —
// they are filtered from recall tool results.
func NewToolExecutor(
	inner agent.ToolExecutor,
	service *Service,
	sessionID string,
	project string,
	excludeIDs map[string]struct{},
) *ToolExecutor {
	return &ToolExecutor{
		inner:      inner,
		service:    service,
		sessionID:  sessionID,
		project:    project,
		excludeIDs: excludeIDs,
	}
}

// ListTools returns the combined tool set: memory tools + inner tools.
// Memory tools are only included when the service is available.
func (te *ToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
	var tools []agent.ToolDefinition
	if te.service != nil {
		tools = append(tools, recallTool, searchSessionsTool)
	}

	if te.inner != nil {
		innerTools, err := te.inner.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list inner tools: %w", err)
		}
		for _, t := range innerTools {
			if IsMemoryTool(t.Name) {
				continue
			}
			tools = append(tools, t)
		}
	}

	return tools, nil
}

// Execute routes the call to the appropriate handler or the inner executor.
func (te *ToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	call.Name = mcp.NormalizeBuiltinPlainToolName(call.Name)
	switch call.Name {
	case ToolRecallPastInvestigations:
		return te.executeRecall(ctx, call)
	case ToolSearchPastSessions:
		return te.executeSessionSearch(ctx, call)
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
	if te.service == nil {
		return &agent.ToolResult{
			CallID: call.ID, Name: call.Name,
			Content: "memory service is not available", IsError: true,
		}, nil
	}

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
		ctx, te.project, args.Query, fetchLimit,
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
	sb.WriteString("<historical_context>\n")
	sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n", len(filtered)))
	for i, m := range filtered {
		age := FormatMemoryAge(m.CreatedAt, m.UpdatedAt)
		sb.WriteString(fmt.Sprintf("\n%d. [%s, %s, score: %.2f, %s] %s", i+1, m.Category, m.Valence, m.Similarity, age, m.Content))
	}
	sb.WriteString("\n\nThese are learnings from PAST incidents — they suggest where to look, not what you will find NOW.\n</historical_context>")

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: sb.String(),
	}, nil
}

func (te *ToolExecutor) executeSessionSearch(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	if te.service == nil {
		return &agent.ToolResult{
			CallID: call.ID, Name: call.Name,
			Content: "memory service is not available", IsError: true,
		}, nil
	}

	var args struct {
		Query     string `json:"query"`
		AlertType string `json:"alert_type"`
		DaysBack  int    `json:"days_back"`
		Limit     int    `json:"limit"`
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

	if args.DaysBack <= 0 {
		args.DaysBack = sessionSearchDefaultDays
	}
	limit := args.Limit
	if limit <= 0 {
		limit = sessionSearchDefaultLimit
	}
	if limit > sessionSearchMaxLimit {
		limit = sessionSearchMaxLimit
	}

	var alertTypePtr *string
	if args.AlertType != "" {
		alertTypePtr = &args.AlertType
	}

	sessions, err := te.service.SearchSessions(ctx, SessionSearchParams{
		Query:            args.Query,
		AlertType:        alertTypePtr,
		DaysBack:         args.DaysBack,
		Limit:            limit,
		ExcludeSessionID: te.sessionID,
	})
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("session search failed: %v", err),
			IsError: true,
		}, nil
	}

	if len(sessions) == 0 {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "No matching sessions found for this query.",
		}, nil
	}

	sessionIDs := make([]string, len(sessions))
	for i, s := range sessions {
		sessionIDs[i] = s.SessionID
	}
	chatMap, err := te.service.FetchChatExchanges(ctx, sessionIDs)
	if err != nil {
		slog.Warn("Failed to fetch chat exchanges for session search", "error", err)
	}
	for i := range sessions {
		sessions[i].ChatExchanges = chatMap[sessions[i].SessionID]
	}

	userPrompt := buildSessionSummarizationPrompt(args.Query, sessions)

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: userPrompt,
		RequiredSummarization: &agent.SummarizationRequest{
			SystemPrompt:    sessionSummarizationSystemPrompt,
			UserPrompt:      userPrompt,
			TransformResult: wrapHistoricalContext("REMINDER: The above is HISTORICAL data from past sessions — it describes what was found THEN, not what is happening NOW. Use it for context and pattern recognition only. Your current investigation must rely on live tool results and sub-agent findings."),
		},
	}, nil
}

const sessionSummarizationSystemPrompt = `You are a summarization assistant for TARSy, an automated incident investigation platform. You are given a set of past investigation sessions that matched a keyword search, along with the original search query.

Produce a focused digest covering:
- Whether the searched entity (e.g. user, workload, service, IP) was investigated before
- What the conclusions were for each matching investigation
- Any human review corrections or quality assessments
- Key findings from follow-up conversations (if any)
- Patterns across multiple investigations of the same entity

Preserve entity identifiers exactly as they appear — do not paraphrase names, namespaces, or IPs. Silently omit sessions that matched coincidentally but involve a different entity than the one queried.

Be concise but complete. Present the findings; do not interpret them or suggest next steps.`

// wrapHistoricalContext returns a TransformResult function that wraps content
// in <historical_context> tags with the given reminder text.
func wrapHistoricalContext(reminder string) func(string) string {
	return func(content string) string {
		return "<historical_context>\n" + content + "\n\n" + reminder + "\n</historical_context>"
	}
}

func buildSessionSummarizationPrompt(query string, sessions []SessionSearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search query: %s\n\nMatched sessions (%d):\n", query, len(sessions)))

	for i, s := range sessions {
		sb.WriteString(fmt.Sprintf("\n--- Session %d (ID: %s, %s) ---\n", i+1, s.SessionID, s.CreatedAt.Format("2006-01-02 15:04")))
		sb.WriteString(fmt.Sprintf("Alert type: %s\n", s.AlertType))
		sb.WriteString(fmt.Sprintf("Alert data:\n%s\n", s.AlertData))

		if s.FinalAnalysis != nil {
			sb.WriteString(fmt.Sprintf("Investigation conclusions:\n%s\n", *s.FinalAnalysis))
		} else {
			sb.WriteString("Investigation conclusions: (none recorded)\n")
		}

		if s.QualityRating != nil {
			sb.WriteString(fmt.Sprintf("Quality assessment: %s\n", *s.QualityRating))
		}
		if s.InvestigationFeedback != nil {
			sb.WriteString(fmt.Sprintf("Human review feedback: %s\n", *s.InvestigationFeedback))
		}

		if len(s.ChatExchanges) > 0 {
			sb.WriteString(fmt.Sprintf("Follow-up conversations (%d):\n", len(s.ChatExchanges)))
			for j, ce := range s.ChatExchanges {
				sb.WriteString(fmt.Sprintf("  Q%d: %s\n", j+1, ce.Question))
				answer := ce.Answer
				if answer == "" {
					answer = "(no response recorded)"
				}
				sb.WriteString(fmt.Sprintf("  A%d: %s\n", j+1, answer))
			}
		}
	}

	return sb.String()
}
