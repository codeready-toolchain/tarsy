# Phase 5.1: Chain Orchestration + Session Completion — Open Questions

**Status**: ✅ All Questions Resolved
**Last Updated**: 2026-02-10
**Related**: `docs/phase5.1-chain-orchestration-design.md`

---

## Q1: Context Building — What to Pass Between Stages

**Status**: ✅ Decided — **Only `final_analysis` from each previous stage**

**Decision:** Pass only `final_analysis` from each completed stage. This matches old TARSy behavior and keeps context focused — the final analysis is the agent's own synthesis of all its work, purpose-built to carry forward what the next stage needs. Passing raw intermediate data (tool results, thinking) would duplicate what the agent already summarized and risk filling the context window.

**Note**: Follow-up chat (Phase 5.3) will need richer context from the investigation — not just `final_analysis` but detailed stage information. That's a different concern: chat context building uses `FormatInvestigationContext()` (already exists) and operates on the full timeline, not on inter-stage `prevStageContext`. The two paths are independent — `prevStageContext` is for agent-to-agent handoff during chain execution; chat context is for post-investigation Q&A. Details in Phase 5.3 design.

---

## Q2: MCP Client Lifecycle in Multi-Stage Chains

**Status**: ✅ Decided — **One MCP client per agent execution**

**Decision:** Each agent execution creates its own MCP client with exactly the servers it needs (create before agent runs, `defer Close()` after). Clean lifecycle boundaries outweigh the subprocess churn from stdio transports. In Phase 5.1 (single-agent stages) this is equivalent to per-stage, but framing it as per-agent-execution means Phase 5.2 parallel execution works without refactoring — each goroutine already has its own isolated client. Also sets the right foundation for future features where agents may become more autonomous. Departs from old TARSy (per-session shared client) but is consistent with the existing per-session isolation pattern in new TARSy. If profiling shows subprocess churn is a problem, we can optimize with lazy server initialization later.

---

## Q3: Executive Summary — Timeline Event Placement

**Status**: ✅ Decided — **Make `stage_id` and `execution_id` optional on TimelineEvent**

**Decision:** Make `stage_id` and `execution_id` optional on the TimelineEvent schema, enabling true session-level events. The change is minimal: 4 schema lines (`Optional()` on fields, remove `Required()` on edges), ~10 lines of application code (conditional field setting in `TimelineService.CreateTimelineEvent()`), and one auto-generated Atlas migration. Using `Optional()` without `Nillable()` keeps the Go type as `string` (not `*string`), so existing code that reads `event.StageID` is unaffected — NULL reads back as `""`. Queries like `StageIDEQ(id)` naturally exclude NULL rows. This lays a proper foundation for future session-level event types (notifications, system events, etc.) rather than hacking placement onto the last stage.

---

## Q4: Executive Summary Failure Handling

**Status**: ✅ Decided — **Fail-open**

**Decision:** Session completes successfully even if executive summary generation fails. Error stored in `AlertSession.executive_summary_error` (field already exists). The investigation is the valuable work; the summary is convenience. Basic retry is handled at the LLM client level — no application-level retry needed here. Consistent with "investigation-availability-first" philosophy (same reasoning as fail-open summarization in Phase 4.3).

---

## Q5: Per-Alert MCP Override Scope in Multi-Stage Chains

**Status**: ✅ Decided — **Override applies to ALL stages**

**Decision:** Per-alert `mcp_selection` override applies uniformly to all stages in the chain. Consistent with existing "replace, not merge" semantics — the override IS the authoritative server set for the entire investigation. The common use case is pointing an investigation at a specific environment (e.g., "use this k8s cluster"), which applies to all stages. Resolved once in the executor, passed to each stage's MCP client creation.

---

## Q6: Parallel Stage Handling in Phase 5.1

**Status**: ✅ Decided — **Error with clear message**

**Decision:** If the executor encounters a parallel stage (multiple agents or replicas > 1), return an error with a clear message. Simplest approach; Phase 5.2 is implemented immediately after and no one is using new TARSy yet. The guard is removed when Phase 5.2 lands.

---

## Q7: Executive Summary — Sequence Number for Timeline Events

**Status**: ✅ Resolved — **Moot after Q3**

With Q3 decided (session-level events), the executive summary has no stage_id, so it lives outside the per-stage sequence number space. A hardcoded high value (9999) ensures correct ordering within the session-wide timeline, and `created_at` provides a natural fallback since the event is always created last.
