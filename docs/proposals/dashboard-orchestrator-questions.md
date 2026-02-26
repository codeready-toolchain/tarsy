# Dashboard Orchestrator Support — Design Questions

**Status:** All questions decided
**Related:** [Design document](dashboard-orchestrator-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How to associate sub-agent WebSocket events with their parent orchestrator?

Sub-agent events (`execution.status`, `execution.progress`, `timeline_event.created`, `stream.chunk`) arrive on the same `session:{id}` channel and carry only `execution_id` — not `parent_execution_id`. The dashboard needs to know which sub-agent belongs to which orchestrator to render events correctly (e.g., avoid creating phantom agent cards in StageContent, route streaming to the right section).

### Option A: Add `parent_execution_id` to WS event payloads (backend change)

Add `parent_execution_id` (nullable) to `ExecutionStatusPayload`, `ExecutionProgressPayload`, `TimelineCreatedPayload`, and `StreamChunkPayload`. Sub-agent events carry their parent's execution ID; regular agents have `null`.

- **Pro:** Dashboard has all info from the event itself — no lookup needed. Clean, deterministic.
- **Pro:** Minimal frontend complexity. Filter `parent_execution_id != null` events from top-level processing.
- **Con:** Requires a backend change in PR7 (modifying WS payloads). Small scope — adding one optional field to 4 payload structs.

**Decision:** Option A, extended — both DB and WS.

1. **DB:** Add `parent_execution_id` (nullable) to the `TimelineEvent` schema. Set at creation time (immutable). Populated from `ExecutionContext.SubAgent.ParentExecID` for sub-agent events; `NULL` for regular agents and orchestrators. This makes the REST timeline response self-describing — no cross-reference via `ExecutionOverview.sub_agents` needed.
2. **WS:** Add `parent_execution_id` (nullable, `omitempty`) to `TimelineCreatedPayload`, `TimelineCompletedPayload`, `StreamChunkPayload`, `ExecutionStatusPayload`, and `ExecutionProgressPayload`. Populated from `ExecutionContext.SubAgent.ParentExecID` at publish time.

Both paths (REST for terminated sessions, WS for active sessions) are consistent and self-describing. The dashboard can filter/route sub-agent events immediately without client-side cross-referencing.

_Considered and rejected: Option B — client-side REST mapping (race conditions, phantom entries, extra re-fetches), Option C — hybrid mapping (same issues with more complexity)._

---

## Q2: How to display sub-agent detail in the Reasoning view?

The Reasoning view (SessionDetailPage → ConversationTimeline → StageContent) shows the orchestrator's timeline items. Sub-agents' own timeline items (thinking, tool calls, final_analysis) are separate events with the sub-agent's `execution_id`.

### Option A: Collapsible sub-agent cards inline in the orchestrator's timeline

After the orchestrator's `dispatch_agent` tool call or after the sub-agent result message, render a collapsible card showing the sub-agent's name, task, status, and (when expanded) its own timeline items. Cards are embedded inline in the orchestrator's reasoning flow.

- **Pro:** Shows the full orchestrator workflow with sub-agents inline — natural reading order. Operators see exactly **when** each sub-agent was dispatched and when its results arrived.
- **Pro:** Progressive disclosure — collapsed by default, expand for details.
- **Pro:** Expanded cards show live streaming updates for active sub-agents and completed timeline items for finished sub-agents — same experience as the top-level timeline.
- **Con:** Complex rendering — mixing orchestrator FlowItems with sub-agent cards requires careful interleaving.
- **Con:** Sub-agent timeline items need to be fetched/routed separately from the orchestrator's.

**Decision:** Option A — inline cards provide the best temporal context. Cards are collapsed by default showing name, task, status, duration, tokens. Users expand to see the sub-agent's own timeline items (live streaming for active, completed items for finished). The orchestrator's own `dispatch_agent` tool calls and `[Sub-agent completed]` result messages provide the interleaving anchor points.

_Considered and rejected: Option B — sub-agent section below orchestrator timeline (loses temporal context of when each was dispatched/completed), Option C — summary only, no drill-down (operators can't see sub-agent reasoning without switching to Trace view)._

---

## Q3: How to display sub-agents in the Trace view?

The Trace view (StageAccordion → ParallelExecutionTabs) currently handles parallel agents with tabs. Orchestrator sub-agents are nested inside the parent execution's `TraceExecutionGroup.sub_agents`.

### Option A: Nested section within the parent execution's tab/panel

In the single-agent case (orchestrator is the only agent in stage), show orchestrator's metadata and interactions first, then a "Sub-Agents" section with tabs (one per sub-agent). Each sub-agent tab shows metadata + interaction cards.

In the parallel case (orchestrator is one of multiple agents), the orchestrator's tab panel gets the nested Sub-Agents section.

- **Pro:** Reuses the existing `ParallelExecutionTabs` pattern for sub-agent tabs.
- **Pro:** Visual hierarchy: stage → orchestrator → sub-agents.
- **Pro:** Sub-agent interactions are visible without leaving the stage accordion.
- **Con:** Deep nesting: stage accordion → orchestrator → sub-agent tabs → interaction cards. Can feel cramped.

**Decision:** Option A — nested sub-agent tabs within the orchestrator's execution panel. Reuses the proven `ParallelExecutionTabs` pattern and preserves the stage → orchestrator → sub-agents hierarchy.

_Considered and rejected: Option B — flat tabs alongside orchestrator (loses hierarchy, confusing with real parallel agents), Option C — accordion-in-accordion (cluttered nesting)._

---

## Q4: How to count interactions (including sub-agents)?

`countStageInteractions` sums LLM + MCP interactions across `stage.executions[]`. Sub-agent interactions are nested in `execution.sub_agents[].llm_interactions` and `execution.sub_agents[].mcp_interactions`. The count affects badges on stage accordions and the progress header.

### Option C: Include sub-agent interactions in total with a sub-agent indicator

Total count includes everything (recursive), but add a badge like "+ 3 sub-agents" next to the interaction counts.

- **Pro:** Operators see the total scope AND know sub-agents are involved.
- **Pro:** Fits existing UI — one total count plus a "sub-agents" chip.
- **Con:** Slightly more complex than a flat count.

**Decision:** Option C — recursive total for interaction counts, plus a "sub-agents" chip indicating how many sub-agents ran. Operators who want the breakdown expand the stage.

_Considered and rejected: Option A — total only (misleading without sub-agent signal), Option B — separate counts (cluttered badges)._

---

## Q5: How to handle sub-agent timeline items in the Reasoning view?

Sub-agent timeline events arrive as flat items in the `GET /sessions/:id/timeline` response and via WS `timeline_event.created`. They have the sub-agent's `execution_id` but the same `stage_id` as the orchestrator. The timeline parser groups items by stage and execution.

**Decision:** Implied by Q2. Partition at StageContent level — after grouping items by `execution_id`, StageContent partitions groups into "orchestrator" and "sub-agents" using `ExecutionOverview.sub_agents` (or `parent_execution_id` from the timeline response per Q1). The orchestrator group renders as the main timeline. Sub-agent groups feed the inline cards decided in Q2. No changes to the timeline parser — items flow through the existing pipeline. Real-time works naturally.

---

## Q6: How to filter sub-agent execution status events?

**Decision:** Implied by Q1. Since `parent_execution_id` is on all WS payloads, the SessionDetailPage WS handler can immediately identify sub-agent events (`parent_execution_id != null`). Sub-agent `execution.status` and `execution.progress` events are stored in a separate `subAgentExecutionStatuses` map instead of the top-level `executionStatuses` map. StageContent receives both maps and routes sub-agent statuses to the inline cards. No phantom agent cards.

---

## Q7: One PR or split into multiple?

### Option A: Single PR

All changes in one PR: backend (DB migration, WS payload changes, REST response updates) + frontend (types, Reasoning view, Trace view, real-time, tests).

- **Pro:** Coherent — everything ships together, no intermediate broken states.
- **Pro:** Simpler review context — reviewer sees the full picture.
- **Con:** Larger PR spanning both Go and TypeScript.

**Decision:** Option A — single PR. The backend changes (Q1: `parent_execution_id` on `TimelineEvent` + WS payloads) are small and exist solely to serve the dashboard. Shipping them separately would leave an intermediate state where the backend sends data nobody consumes.

_Considered and rejected: Option B — foundation + views split (foundation alone not useful), Option C — Trace then Reasoning split (two PRs for one feature, unnecessary overhead)._
