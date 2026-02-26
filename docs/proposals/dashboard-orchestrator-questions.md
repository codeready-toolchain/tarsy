# Dashboard Orchestrator Support — Design Questions

**Status:** Open — decisions pending
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

### Option B: Build a client-side mapping from REST data

On page load, build a `Map<string, string>` (execution_id → parent_execution_id) from `ExecutionOverview.sub_agents`. When WS events arrive, check the map. If the execution_id is in the map, it's a sub-agent.

- **Pro:** No backend changes.
- **Con:** Race condition: sub-agent WS events can arrive before REST data is loaded (or before a re-fetch captures the new sub-agent). Events during this window create phantom entries.
- **Con:** Requires re-fetching session detail whenever a new sub-agent is dispatched (to learn about it).
- **Con:** More complex frontend logic — stale maps, timing-dependent behavior.

### Option C: Hybrid — REST mapping + defensive filtering

Use REST mapping (Option B) for normal operation. Add defensive checks in StageContent: don't create phantom agent cards for unknown execution IDs. Re-fetch session detail on `execution.status` events for unknown IDs to discover new sub-agents.

- **Pro:** No backend changes.
- **Pro:** Handles the race condition via re-fetch.
- **Con:** More re-fetches and complex logic.
- **Con:** Brief UI gaps before sub-agents appear (until re-fetch completes).

**Recommendation:** Option A. A single optional field on WS payloads is a trivial backend change that dramatically simplifies frontend logic. The alternative introduces timing-dependent bugs and extra re-fetches. The field is useful for any future feature that nests executions.

---

## Q2: How to display sub-agent detail in the Reasoning view?

The Reasoning view (SessionDetailPage → ConversationTimeline → StageContent) shows the orchestrator's timeline items. Sub-agents' own timeline items (thinking, tool calls, final_analysis) are separate events with the sub-agent's `execution_id`.

### Option A: Collapsible sub-agent cards within the orchestrator's timeline

After the orchestrator's `dispatch_agent` tool call or after the sub-agent result message, render a collapsible card showing the sub-agent's name, task, status, and (when expanded) its own timeline items. Cards are embedded inline in the orchestrator's reasoning flow.

- **Pro:** Shows the full orchestrator workflow with sub-agents inline — natural reading order.
- **Pro:** Progressive disclosure — collapsed by default, expand for details.
- **Con:** Complex rendering — mixing orchestrator FlowItems with sub-agent cards requires careful interleaving.
- **Con:** Sub-agent timeline items need to be fetched/routed separately from the orchestrator's.

### Option B: Sub-agent summary section below the orchestrator's timeline

After all orchestrator FlowItems, render a "Sub-Agents" section with a card per sub-agent showing: name, task, status, duration, tokens, error. Each card can expand to show the sub-agent's own timeline items.

- **Pro:** Clean separation — orchestrator's reasoning flow is uninterrupted.
- **Pro:** Simpler implementation — sub-agent cards are a separate section, not interleaved.
- **Pro:** Natural grouping — all sub-agents visible at a glance with status.
- **Con:** Less context about **when** each sub-agent was dispatched (temporal relationship is lost).

### Option C: No sub-agent timeline in Reasoning view — summary only

Show sub-agent cards (name, task, status, duration, tokens) below the orchestrator's items, but no drill-down into sub-agent timeline items. Users go to the Trace view for interaction-level detail.

- **Pro:** Simplest implementation.
- **Pro:** Reasoning view stays focused on the orchestrator's reasoning flow.
- **Pro:** Trace view is already designed for interaction-level detail.
- **Con:** Operators can't see sub-agent reasoning without switching views.

**Recommendation:** Option B. Clean separation keeps the orchestrator's reasoning flow readable while providing full sub-agent detail on demand. The temporal relationship is already visible through the orchestrator's `dispatch_agent` tool calls and result messages — operators can see "dispatched LogAnalyzer" in the orchestrator's flow and then expand the LogAnalyzer card below for details.

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

### Option B: Flat expansion — sub-agents as additional tabs alongside orchestrator

Show the orchestrator as one tab and each sub-agent as additional tabs at the same level. Mark sub-agent tabs visually (indented label, different color, "sub-agent" badge).

- **Pro:** Simple — reuses existing tab infrastructure.
- **Pro:** Less nesting depth.
- **Con:** Loses hierarchical relationship — orchestrator and sub-agents appear as peers.
- **Con:** Confusing when there are also real parallel agents alongside the orchestrator.

### Option C: Tree/accordion within the parent execution

Render sub-agents as mini-accordions inside the parent execution's panel. Each accordion shows the sub-agent name, task, status, and expands to show interactions.

- **Pro:** Compact — sub-agents are collapsed by default.
- **Pro:** Clear hierarchy within the execution panel.
- **Con:** Accordions inside accordions (stage accordion → sub-agent accordions) — somewhat cluttered.

**Recommendation:** Option A. The tabbed pattern is already proven for parallel agents. Nesting it inside the orchestrator's execution panel provides clear hierarchy. The nesting depth is acceptable — operators are already used to stage → agent → interaction.

---

## Q4: How to count interactions (including sub-agents)?

`countStageInteractions` sums LLM + MCP interactions across `stage.executions[]`. Sub-agent interactions are nested in `execution.sub_agents[].llm_interactions` and `execution.sub_agents[].mcp_interactions`. The count affects badges on stage accordions and the progress header.

### Option A: Include sub-agent interactions in the total

Recursively count all interactions (orchestrator + sub-agents). The stage shows the true total.

- **Pro:** Accurate total — operators see all work done in the stage.
- **Con:** A stage with an orchestrator may show "47 LLM interactions" which is misleading — most are from sub-agents.

### Option B: Show separate counts

Show orchestrator interactions and sub-agent interactions separately in the badge: "3 LLM (orchestrator) + 12 LLM (sub-agents)" or similar.

- **Pro:** Clear attribution.
- **Con:** Cluttered badges. Hard to fit in the current chip layout.

### Option C: Include sub-agent interactions in total with a sub-agent indicator

Total count includes everything, but add a badge like "+ 5 sub-agents" next to the interaction counts.

- **Pro:** Operators see the total scope AND know sub-agents are involved.
- **Pro:** Fits existing UI — one total count plus a "sub-agents" chip.
- **Con:** Slightly more complex than Option A.

**Recommendation:** Option C. The total gives operators a sense of scale, and the sub-agent chip signals that nesting is involved. Operators who want the breakdown can expand the stage.

---

## Q5: How to handle sub-agent timeline items in the Reasoning view?

Sub-agent timeline events arrive as flat items in the `GET /sessions/:id/timeline` response and via WS `timeline_event.created`. They have the sub-agent's `execution_id` but the same `stage_id` as the orchestrator. The timeline parser groups items by stage and execution.

### Option A: Filter sub-agent items from the main timeline; show them only in sub-agent cards

During `parseTimelineToFlow`, exclude items whose `execution_id` belongs to a sub-agent (looked up from `ExecutionOverview.sub_agents`). Sub-agent items are fetched/shown only when the user expands a sub-agent card.

- **Pro:** The orchestrator's reasoning flow is clean — only orchestrator items.
- **Pro:** No phantom agent tabs/cards for sub-agents in StageContent.
- **Con:** Requires knowing sub-agent execution IDs at parse time (from REST data).
- **Con:** Sub-agent items arriving via WS before REST data is loaded would briefly appear in the flow.

### Option B: Don't filter — let sub-agents create execution groups in StageContent

Sub-agent items create additional `ExecutionGroup` entries in StageContent's `groupItemsByExecution`. StageContent detects orchestrator vs. parallel using `ExecutionOverview.sub_agents` and renders sub-agent groups differently (as nested cards instead of peer tabs).

- **Pro:** No filtering needed — items flow through the existing pipeline.
- **Pro:** Real-time: sub-agent items appear immediately as they stream in.
- **Con:** StageContent becomes more complex — needs to distinguish orchestrator sub-agent groups from parallel agent groups.
- **Con:** The orchestrator stage would show multiple execution groups (orchestrator + each sub-agent) — the current single-agent path would wrongly switch to multi-agent tabs.

### Option C: Filter at StageContent level — separate sub-agent execution groups

In `parseTimelineToFlow`, don't filter. In StageContent, after grouping by execution, partition groups into "orchestrator" and "sub-agents" using the `ExecutionOverview.sub_agents` mapping. Render the orchestrator group as the main timeline and sub-agent groups in a nested section below.

- **Pro:** Items flow through existing pipeline (no parse-time filtering).
- **Pro:** Real-time works naturally.
- **Pro:** Clean separation at the component level.
- **Con:** StageContent needs the sub-agent ID mapping.

**Recommendation:** Option C. Filtering at the component level is pragmatic — StageContent already receives `executionOverviews` which contain `sub_agents`. It can build the partition map locally. No changes to the timeline parser. Real-time items flow through the standard pipeline and land in the right groups.

---

## Q6: How to filter sub-agent execution status events?

`execution.status` WS events for sub-agents carry the sub-agent's `execution_id` and `stage_id`. The current `executionStatuses` map in SessionDetailPage stores all statuses, and StageContent's `mergedExecutions` creates execution groups from them. Sub-agent status events would create phantom agent cards.

### Option A: Filter using `parent_execution_id` from WS payload (requires Q1 = Option A)

If Q1 is decided as Option A (add `parent_execution_id` to WS payloads), sub-agent status events are identifiable immediately. Store them in a separate `subAgentStatuses` map or filter them out of `executionStatuses`.

- **Pro:** Clean, immediate filtering. No race conditions.
- **Con:** Depends on Q1 = Option A.

### Option B: Filter using a client-side sub-agent ID set

Build a `Set<string>` of known sub-agent execution IDs from REST `ExecutionOverview.sub_agents`. In the WS handler, skip execution.status events whose ID is in this set (or store them separately).

- **Pro:** No backend change.
- **Con:** Race condition: sub-agent events arrive before the ID is known. Brief phantom cards.

### Option C: Filter at StageContent level

Don't filter in the WS handler. In StageContent, partition `executionStatuses` into orchestrator and sub-agent using `ExecutionOverview.sub_agents`. Sub-agent statuses go to the nested section, not the main execution group.

- **Pro:** Centralized logic in one component.
- **Pro:** Works regardless of Q1 decision.
- **Con:** StageContent becomes responsible for more filtering logic.

**Recommendation:** Option A (if Q1 = A) or Option C (if Q1 ≠ A). With `parent_execution_id` on WS events, filtering at the handler level is cleanest. Without it, StageContent-level partitioning is the pragmatic fallback.

---

## Q7: One PR or split into multiple?

### Option A: Single PR

All dashboard changes in one PR: types, Reasoning view, Trace view, real-time, tests.

- **Pro:** Coherent — everything ships together, no intermediate broken states.
- **Pro:** Simpler review context — reviewer sees the full picture.
- **Con:** Larger PR. Dashboard PRs tend to be UI-heavy with many component changes.

### Option B: Two PRs — foundation + views

PR7a: Types, WS payload backend change (Q1), helper updates, sub-agent filtering logic.
PR7b: Reasoning view sub-agent cards, Trace view nesting, tests.

- **Pro:** Smaller, focused PRs.
- **Con:** PR7a alone doesn't add visible UI — needs PR7b to be useful.
- **Con:** Intermediate state where types are updated but UI doesn't use them.

### Option C: Two PRs — Trace first, then Reasoning

PR7a: Types, Trace view sub-agent nesting (simpler — data is already hierarchical in the API).
PR7b: Reasoning view sub-agent cards, real-time streaming, WS filtering.

- **Pro:** Trace view is simpler and can ship independently.
- **Pro:** Natural progression: basic visibility first, then rich streaming.
- **Con:** Two PRs for a single feature.

**Recommendation:** Option A. The dashboard changes are interconnected (types feed both views, WS filtering affects both). A single PR keeps everything coherent. The orchestrator backend was 6 PRs because each layer was independently testable — the dashboard doesn't have that property.
