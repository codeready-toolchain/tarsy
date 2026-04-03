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
	case config.AgentTypeDefault, config.AgentTypeAction:
		return true
	default:
		return false
	}
}

// MemorySubAgentWrap returns a ToolExecutor wrapping function for sub-agent runs
// (memory tool with no search exclusions). Returns nil when memory is disabled.
func MemorySubAgentWrap(mem *memory.Service, memCfg *config.MemoryConfig, sessionID string) func(agent.ToolExecutor) agent.ToolExecutor {
	if mem == nil || memCfg == nil {
		return nil
	}
	return func(inner agent.ToolExecutor) agent.ToolExecutor {
		return memory.NewToolExecutor(inner, mem, sessionID, "default", nil)
	}
}

// memoryToolWrapper returns a ToolExecutor wrapping function for the memory tool.
// Returns nil when memory is disabled (no wrapping needed).
func (e *RealSessionExecutor) memoryToolWrapper(session *ent.AlertSession) func(agent.ToolExecutor) agent.ToolExecutor {
	return MemorySubAgentWrap(e.memoryService, e.memoryConfig, session.ID)
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

// minInjectionScore is the minimum composite score (RRF × confidence × decay)
// for a memory to be auto-injected. Filters very-low-ranked or heavily-decayed
// matches (e.g. a single keyword hit at rank 40+ with 90+ day decay). This is
// a conservative floor; the primary noise control is the similarity threshold
// (0.7) on vector candidates in FindSimilarWithBoosts.
const minInjectionScore = 0.01

// retrieveMemories fetches the top memories for auto-injection into the agent prompt.
// Returns nil if retrieval fails (best-effort — never blocks the investigation).
func (e *RealSessionExecutor) retrieveMemories(ctx context.Context, session *ent.AlertSession, logger *slog.Logger) *agent.MemoryBriefing {
	project := "default"

	memories, err := e.memoryService.FindSimilarWithBoosts(
		ctx, project, session.AlertData, e.memoryConfig.MaxInject,
	)
	if err != nil {
		logger.Warn("Failed to retrieve memories for injection", "error", err)
		return nil
	}

	var filtered []memory.Memory
	for _, m := range memories {
		if m.Score >= minInjectionScore {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	briefing := &agent.MemoryBriefing{
		Memories:    make([]agent.MemoryHint, len(filtered)),
		InjectedIDs: make([]string, len(filtered)),
	}
	for i, m := range filtered {
		briefing.Memories[i] = agent.MemoryHint{
			ID:       m.ID,
			Content:  m.Content,
			Category: m.Category,
			Valence:  m.Valence,
			Score:    m.Similarity,
			AgeLabel: memory.FormatMemoryAge(m.CreatedAt, m.UpdatedAt),
		}
		briefing.InjectedIDs[i] = m.ID
	}
	logger.Info("Retrieved memories for injection", "count", len(filtered))
	return briefing
}
