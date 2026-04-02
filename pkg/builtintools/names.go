// Package builtintools holds wire names for built-in tools that use a single
// plain identifier (not MCP server.tool form). Add or rename tools here only;
// PlainToolKinds must list every name exactly once.
package builtintools

// Kind classifies built-in plain tools for MCP error text and routing helpers.
type Kind uint8

const (
	// KindOrchestration is agent orchestration built-ins (dispatch, cancel, list).
	KindOrchestration Kind = iota
	// KindSkill is skill-loading built-ins.
	KindSkill
	// KindMemory is investigation memory / search built-ins.
	KindMemory
)

// Wire names — single source of truth for these string literals.
const (
	DispatchAgent            = "dispatch_agent"
	CancelAgent              = "cancel_agent"
	ListAgents               = "list_agents"
	LoadSkill                = "load_skill"
	RecallPastInvestigations = "recall_past_investigations"
	SearchPastSessions       = "search_past_sessions"
)

// PlainToolKinds maps wire name → category. Must include every const above.
var PlainToolKinds = map[string]Kind{
	DispatchAgent:            KindOrchestration,
	CancelAgent:              KindOrchestration,
	ListAgents:               KindOrchestration,
	LoadSkill:                KindSkill,
	RecallPastInvestigations: KindMemory,
	SearchPastSessions:       KindMemory,
}

// KindForPlainTool reports the category for a built-in plain tool name.
func KindForPlainTool(name string) (Kind, bool) {
	k, ok := PlainToolKinds[name]
	return k, ok
}
