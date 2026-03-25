package queue

import (
	"context"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
)

// agentTypeSupportsMemory returns true for agent types whose prompts
// consume MemoryBriefing (Tier 4 injection) and/or benefit from the
// recall_past_investigations tool. Single-shot agents (synthesis,
// exec_summary, scoring) use fixed prompt templates that ignore
// MemoryBriefing, so retrieving memories would be wasteful and would
// spuriously record injected IDs.
func agentTypeSupportsMemory(agentType config.AgentType) bool {
	switch agentType {
	case config.AgentTypeDefault, config.AgentTypeAction, config.AgentTypeOrchestrator:
		return true
	default:
		return false
	}
}

// memoryToolWrapper returns a ToolExecutor wrapping function for the memory tool.
// Returns nil when memory is disabled (no wrapping needed).
func (e *RealSessionExecutor) memoryToolWrapper(session *ent.AlertSession) func(agent.ToolExecutor) agent.ToolExecutor {
	if e.memoryService == nil || e.memoryConfig == nil {
		return nil
	}
	var alertTypePtr *string
	if session.AlertType != "" {
		alertTypePtr = &session.AlertType
	}
	chainID := session.ChainID
	return func(inner agent.ToolExecutor) agent.ToolExecutor {
		return memory.NewToolExecutor(inner, e.memoryService, "default", alertTypePtr, &chainID, nil)
	}
}

// memoryExcludeIDs builds a set of memory IDs to exclude from tool search results.
// Returns nil when briefing is nil (no exclusions).
func memoryExcludeIDs(briefing *agent.MemoryBriefing) map[string]struct{} {
	if briefing == nil || len(briefing.InjectedIDs) == 0 {
		return nil
	}
	ids := make(map[string]struct{}, len(briefing.InjectedIDs))
	for _, id := range briefing.InjectedIDs {
		ids[id] = struct{}{}
	}
	return ids
}

// retrieveMemories fetches the top memories for auto-injection into the agent prompt.
// Returns nil if retrieval fails (best-effort — never blocks the investigation).
func (e *RealSessionExecutor) retrieveMemories(ctx context.Context, session *ent.AlertSession, logger *slog.Logger) *agent.MemoryBriefing {
	project := "default"

	var alertTypePtr *string
	if session.AlertType != "" {
		alertTypePtr = &session.AlertType
	}
	chainIDPtr := &session.ChainID

	memories, err := e.memoryService.FindSimilarWithBoosts(
		ctx, project, session.AlertData, alertTypePtr, chainIDPtr, e.memoryConfig.MaxInject,
	)
	if err != nil {
		logger.Warn("Failed to retrieve memories for injection", "error", err)
		return nil
	}
	if len(memories) == 0 {
		return nil
	}

	briefing := &agent.MemoryBriefing{
		Memories:    make([]agent.MemoryHint, len(memories)),
		InjectedIDs: make([]string, len(memories)),
	}
	for i, m := range memories {
		briefing.Memories[i] = agent.MemoryHint{
			ID:       m.ID,
			Content:  m.Content,
			Category: m.Category,
			Valence:  m.Valence,
			AgeLabel: memory.FormatMemoryAge(m.CreatedAt, m.UpdatedAt),
		}
		briefing.InjectedIDs[i] = m.ID
	}
	logger.Info("Retrieved memories for injection", "count", len(memories))
	return briefing
}
