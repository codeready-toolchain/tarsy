# Dashboard Orchestrator Support — Implementation Design

**Status:** Backend complete — frontend ready for implementation
**Related:** [orchestrator-impl-design.md](orchestrator-impl-design.md), [questions](dashboard-orchestrator-questions.md)
**Last updated:** 2026-02-26

## Overview

This document designs the dashboard changes needed to display orchestrator agent executions with clear parent-child hierarchy. The backend (PR0–PR6) already serves nested sub-agent data in API responses and publishes WebSocket events for sub-agent executions. The dashboard must surface this data in both the Reasoning view (SessionDetailPage) and the Trace view (TracePage), with real-time streaming support.

The core challenge: orchestrator sub-agents are not parallel agents (statically defined at config time) — they're dynamically dispatched at runtime. The dashboard must show **when** they were dispatched, **what task** they received, their **real-time progress**, and their **results** — all nested under the parent orchestrator execution.

## Design Principles

1. **Backend already done.** The API responses (`ExecutionOverview.sub_agents`, `TraceExecutionGroup.sub_agents`) already nest sub-agents correctly. The dashboard consumes existing data — no new API endpoints needed.

2. **Reuse existing components.** The dashboard has battle-tested patterns for parallel agents (tabbed views, agent cards, streaming renderers). Adapt these for orchestrator sub-agents rather than building from scratch.

3. **Progressive disclosure.** Show the orchestrator's own reasoning flow at the top level. Sub-agent details are available via expand/drill-down, not forced onto the user.

4. **Real-time from day one.** Sub-agent progress must stream in real-time, matching the existing experience for regular agents. No "refresh to see sub-agent results."

## Backend Changes (DONE)

All backend changes are implemented and tested.

### `TimelineEvent` schema — `parent_execution_id` ✅

Nullable column on `TimelineEvent`, set at creation time from `ExecutionContext.SubAgent.ParentExecID`. `NULL` for regular and orchestrator agents; set for sub-agents. Makes the REST timeline response self-describing — the dashboard can partition events without cross-referencing `ExecutionOverview.sub_agents`.

**What was done:**
- `ent/schema/timelineevent.go`: Added `parent_execution_id` field (nullable, immutable) + edge to `AgentExecution`
- `ent/schema/agentexecution.go`: Added `sub_agent_timeline_events` back-reference edge
- `pkg/models/timeline.go`: Added `ParentExecutionID *string` to `CreateTimelineEventRequest`
- `pkg/services/timeline_service.go`: Threads `ParentExecutionID` via `SetNillableParentExecutionID`
- `pkg/agent/controller/timeline.go`: Added `parentExecID()` / `parentExecIDPtr()` helpers; threaded through all `CreateTimelineEvent` + `PublishTimeline*` call sites
- `pkg/agent/controller/streaming.go`: Threaded through all streaming event creation + chunk publishing
- `pkg/agent/controller/summarize.go`: Threaded through MCP summarization streaming events
- `pkg/agent/controller/helpers.go`: Threaded through `publishExecutionProgress`
- `pkg/agent/orchestrator/runner.go`: Added to `task_assigned` timeline event in `Dispatch`
- `pkg/database/migrations/20260226223249_add_parent_execution_id_to_timeline_events.up.sql`: Column + FK + index
- Ent code regenerated; all generated structs include `parent_execution_id` with `json:"parent_execution_id,omitempty"`

### WebSocket payloads — `parent_execution_id` ✅

Added `ParentExecutionID string` with `json:"parent_execution_id,omitempty"` to all relevant WS payloads in `pkg/events/payloads.go`:

| Payload | Source |
|---------|--------|
| `TimelineCreatedPayload` | `execCtx.SubAgent.ParentExecID` at publish call sites in `streaming.go`, `timeline.go`, `summarize.go` |
| `TimelineCompletedPayload` | Same — threaded through from the created event |
| `StreamChunkPayload` | Same — carried from the streaming event context |
| `ExecutionStatusPayload` | Field present; populated when called for sub-agents |
| `ExecutionProgressPayload` | `execCtx.SubAgent.ParentExecID` |

For regular agents and orchestrators themselves, this field is `""` / omitted. For sub-agents, it carries the parent orchestrator's execution ID.

### REST timeline response — `parent_execution_id` ✅

The `GET /sessions/:id/timeline` endpoint returns raw `TimelineEvent` entities. The new column is automatically included in the JSON response via ent's generated `json:"parent_execution_id,omitempty"` tag.

### Tests ✅

- Unit tests: `parentExecID` / `parentExecIDPtr` helpers (controller/timeline_test.go)
- Integration tests: `CreateTimelineEvent` with/without `ParentExecutionID` (services/timeline_service_test.go)
- API handler test: `parent_execution_id` in JSON response (api/handler_timeline_test.go)
- E2E test: sub-agent timeline events carry `parent_execution_id` (test/e2e/orchestrator_test.go)

## Architecture

### What the Backend Provides (after changes above)

**Session Detail API** (`GET /sessions/:id`):
- `ExecutionOverview` includes `parent_execution_id`, `task`, and `sub_agents: ExecutionOverview[]`
- Top-level `stages[].executions[]` only has orchestrator (parent) executions
- Sub-agents are nested inside their parent's `sub_agents` array

**Trace API** (`GET /sessions/:id/trace`):
- `TraceExecutionGroup` includes `sub_agents: TraceExecutionGroup[]`
- Top-level `stages[].executions[]` only has orchestrator (parent) executions
- Sub-agents are nested with their own `llm_interactions` and `mcp_interactions`

**Timeline API** (`GET /sessions/:id/timeline`):
- Flat list of all timeline events (orchestrator + sub-agents)
- Each event has `execution_id` and now `parent_execution_id`
- Dashboard partitions using `parent_execution_id` — no cross-reference needed

**WebSocket events**:
- All events carry `parent_execution_id` (nullable)
- Dashboard filters sub-agent events immediately on arrival

### Component Change Map

```text
SessionDetailPage
  ├─ WS handler ────────── Filters sub-agent events into separate
  │                         subAgentExecutionStatuses map using parent_execution_id.
  └─ ConversationTimeline
      └─ StageContent ───── Partitions execution groups into orchestrator + sub-agents.
          └─ SubAgentCard ─ NEW: Collapsible inline card per sub-agent.
                             Collapsed: name, task, status, duration, tokens.
                             Expanded: sub-agent's own timeline items + streaming.

TracePage
  └─ TraceTimeline
      └─ StageAccordion ── Detects orchestrator (sub_agents.length > 0).
          └─ SubAgentTabs ─ NEW or reused ParallelExecutionTabs pattern.
                             Nested tabs within orchestrator's execution panel.

Types/session.ts ──── Add parent_execution_id, task, sub_agents to ExecutionOverview.
                      Add parent_execution_id to TimelineEvent.
Types/events.ts ───── Add parent_execution_id to all relevant WS payload types.
```

### Data Flow for Sub-Agent Events

```text
Active session (WS path):
1. Sub-agent starts → execution.status { execution_id: "sub-123", parent_execution_id: "orch-456" }
   → Dashboard stores in subAgentExecutionStatuses (not top-level executionStatuses)
2. Sub-agent streams → timeline_event.created { execution_id: "sub-123", parent_execution_id: "orch-456" }
   → Dashboard routes to sub-agent card's streaming state
3. Sub-agent completes → execution.status { execution_id: "sub-123", status: "completed" }
   → Sub-agent card updates status

Terminated session (REST path):
1. GET /sessions/:id → ExecutionOverview.sub_agents nests sub-agents under parent
2. GET /sessions/:id/timeline → Each event has parent_execution_id for partitioning
   → StageContent partitions items by parent_execution_id
```

## Reasoning View (SessionDetailPage)

### Timeline Item Flow for Orchestrator Stages

When the orchestrator runs, the timeline contains:
1. **Orchestrator thinking** — LLM reasoning about what to investigate
2. **`dispatch_agent` tool calls** — tool call items with `server_name: "orchestrator"`, `tool_name: "dispatch_agent"`
3. **Sub-agent card** — inline, collapsed, anchored to the dispatch tool call result
4. **More thinking** — LLM processing, possibly dispatching more agents
5. **`list_agents` / `cancel_agent`** — status check or cancellation tool calls
6. **Sub-agent result messages** — injected user-role messages like `[Sub-agent completed] LogAnalyzer (exec abc): ...`
7. **Final analysis** — orchestrator's synthesized output

All orchestrator items flow through the existing pipeline as normal `FlowItem`s. Sub-agent items are partitioned out at the `StageContent` level.

### Sub-Agent Inline Cards

Sub-agent cards appear inline in the orchestrator's reasoning flow, anchored to `dispatch_agent` tool call results. Each card is **collapsed by default**, showing:
- Agent name, task description, status chip, duration, token usage

When **expanded**, the card shows the sub-agent's own timeline items:
- For **active sub-agents**: live streaming (thinking, tool calls) via the same `StreamingContentRenderer`
- For **completed sub-agents**: full timeline items (thinking, tool calls, final analysis)

**Anchoring strategy:** Sub-agent cards are placed after the `dispatch_agent` tool call result for the corresponding sub-agent. The tool call result content contains `execution_id: "sub-123"`. This links the card to the right position in the flow. When a `[Sub-agent completed]` result message arrives later, the card updates its status — it doesn't move or duplicate.

### StageContent Partitioning

`StageContent` receives all timeline items grouped by `execution_id` (existing `groupItemsByExecution`). It partitions groups into orchestrator and sub-agents:

1. Build a `Set<string>` of sub-agent execution IDs from `executionOverviews[].sub_agents` or from `parent_execution_id` on WS events
2. Execution groups whose ID is in the sub-agent set → feed into inline `SubAgentCard` components
3. Remaining groups → render as the main orchestrator timeline (existing path)
4. The `isMultiAgent` check considers only non-sub-agent groups — sub-agents don't trigger the parallel agent tab UI

### Orchestrator Detection

An execution is an orchestrator if its `ExecutionOverview` has `sub_agents` array with length > 0. Fallback: timeline contains tool calls with `server_name: "orchestrator"`.

### WS Event Filtering

The SessionDetailPage WS handler checks `parent_execution_id` on incoming events:
- `execution.status` / `execution.progress` with `parent_execution_id != null` → stored in `subAgentExecutionStatuses` map (separate from top-level `executionStatuses`)
- `timeline_event.created` / `stream.chunk` with `parent_execution_id != null` → stored in `subAgentStreamingEvents` map (separate from top-level `streamingEvents`)

StageContent receives both maps and routes sub-agent data to the inline cards.

## Trace View (TracePage)

### With Orchestrator Sub-Agents

The trace API already nests `sub_agents` inside `TraceExecutionGroup`. The trace view renders this nesting.

**Single-agent stage** (orchestrator is the only agent): `StageAccordion` shows orchestrator metadata and interactions, then a "Sub-Agents" section with tabs (one per sub-agent). Each sub-agent tab shows metadata + interaction cards. Reuses the `ParallelExecutionTabs` pattern.

**Parallel stage** (orchestrator alongside other agents): The orchestrator's tab panel gets the nested Sub-Agents section. Other agents render as normal tabs.

### Interaction Count Adjustments

`countStageInteractions` recursively includes sub-agent interactions in the total count. A separate "N sub-agents" chip on the stage accordion signals that nesting is involved. Operators expand for the breakdown.

```typescript
function countStageInteractions(stage: TraceStageGroup): { total: number; llm: number; mcp: number; subAgentCount: number } {
  let llm = 0, mcp = 0, subAgentCount = 0;
  for (const exec of stage.executions) {
    llm += exec.llm_interactions.length;
    mcp += exec.mcp_interactions.length;
    if (exec.sub_agents?.length) {
      subAgentCount += exec.sub_agents.length;
      for (const sub of exec.sub_agents) {
        llm += sub.llm_interactions.length;
        mcp += sub.mcp_interactions.length;
      }
    }
  }
  return { total: llm + mcp, llm, mcp, subAgentCount };
}
```

## TypeScript Type Updates

### `ExecutionOverview` (types/session.ts)

```typescript
export interface ExecutionOverview {
  // ... existing fields ...
  parent_execution_id?: string | null;
  task?: string | null;
  sub_agents?: ExecutionOverview[];
}
```

### `TimelineEvent` (types/session.ts)

```typescript
export interface TimelineEvent {
  // ... existing fields ...
  parent_execution_id?: string | null;
}
```

### `TraceExecutionGroup` (types/trace.ts)

Already has `sub_agents?: TraceExecutionGroup[]` — no change needed.

### WebSocket Event Payloads (types/events.ts)

Add `parent_execution_id?: string` to:
- `TimelineCreatedPayload`
- `TimelineCompletedPayload`
- `StreamChunkPayload`
- `ExecutionStatusPayload`
- `ExecutionProgressPayload`

## Trace Helper Updates

### `findExecutionOverview`

Currently searches `session.stages[].executions[]` — needs to also search nested `sub_agents`:

```typescript
export function findExecutionOverview(session, executionId): ExecutionOverview | undefined {
  for (const stage of session.stages ?? []) {
    for (const exec of stage.executions ?? []) {
      if (exec.execution_id === executionId) return exec;
      const sub = exec.sub_agents?.find(s => s.execution_id === executionId);
      if (sub) return sub;
    }
  }
  return undefined;
}
```

### `countStageInteractions`

Updated to recursively count sub-agent interactions and return `subAgentCount` (see Trace View section above).

### `formatStageForCopy` / `formatEntireFlowForCopy`

Updated to include sub-agent interactions in the copy output, indented under their parent execution.

## Dashboard List View (DashboardPage)

No changes needed. `DashboardSessionItem` doesn't include execution-level details. Sessions with orchestrator agents look the same in the list.

## Implementation — Single PR

All changes ship in one PR (PR7): backend (DB migration, WS payload changes) + frontend (types, components, helpers, tests).

### Work Items

1. ~~**Backend: DB migration** — `parent_execution_id` on `TimelineEvent`. Update `CreateTimelineEventRequest`, `TimelineService`, controller publish sites.~~ ✅
2. ~~**Backend: WS payloads** — Add `parent_execution_id` to 5 payload structs. Thread from `ExecutionContext` at publish call sites.~~ ✅
3. **Frontend: Type updates** — `ExecutionOverview`, `TimelineEvent`, WS event payloads.
4. **Frontend: WS handler** — SessionDetailPage: filter sub-agent events into separate maps using `parent_execution_id`.
5. **Frontend: StageContent** — Partition execution groups into orchestrator + sub-agents. Render orchestrator as main timeline.
6. **Frontend: SubAgentCard** — New component: collapsible card showing sub-agent metadata. Expands to show timeline items + streaming.
7. **Frontend: Trace view** — `StageAccordion`: detect orchestrator, render sub-agent tabs section. Update `countStageInteractions` for recursive counts + sub-agent chip.
8. **Frontend: Trace helpers** — Update `findExecutionOverview` for nested search. Update copy formatting.
9. **Frontend: Tests** — Vitest tests for SubAgentCard, StageContent partitioning, trace helper updates, WS event filtering.

## Decided Questions

| # | Question | Decision | Reference |
|---|----------|----------|-----------|
| Q1 | How to associate sub-agent events with parent? | DB column + WS payload `parent_execution_id` on all events | [Q1](dashboard-orchestrator-questions.md) |
| Q2 | Sub-agent display in Reasoning view? | Collapsible inline cards anchored to dispatch tool calls | [Q2](dashboard-orchestrator-questions.md) |
| Q3 | Sub-agent display in Trace view? | Nested tabs within orchestrator's execution panel | [Q3](dashboard-orchestrator-questions.md) |
| Q4 | How to count interactions? | Recursive total + "N sub-agents" chip | [Q4](dashboard-orchestrator-questions.md) |
| Q5 | How to handle sub-agent timeline items? | Partition at StageContent level (implied by Q2) | [Q5](dashboard-orchestrator-questions.md) |
| Q6 | How to filter sub-agent status events? | Filter using `parent_execution_id` from WS payload (implied by Q1) | [Q6](dashboard-orchestrator-questions.md) |
| Q7 | One PR or split? | Single PR (backend + frontend) | [Q7](dashboard-orchestrator-questions.md) |

## Edge Cases

### Orchestrator as Sole Agent in Stage (Common Case)
The orchestrator is typically the only agent in its stage. StageContent renders it as a single agent (no tabs). Sub-agent inline cards appear within the orchestrator's timeline.

### Orchestrator in Parallel Stage (Rare)
Multiple agents in the same stage, one of which is an orchestrator. The tabbed view shows all top-level agents; the orchestrator tab has sub-agent inline cards inside it.

### Multiple Orchestrators in Same Stage (Edge)
Each orchestrator has its own sub-agents. Sub-agents are scoped by `parent_execution_id` — no collision.

### Sub-Agent Failure
Backend sets `status: "failed"` on the sub-agent `ExecutionOverview`. The orchestrator itself may complete successfully. The sub-agent card shows failure state clearly without implying the orchestrator failed.

### Session Cancellation
All executions (orchestrator + sub-agents) end up `cancelled`. The dashboard already handles cancelled state — just needs to show it for nested sub-agents too.

### No Sub-Agents Dispatched
Orchestrator runs but decides no sub-agents are needed. `sub_agents` is empty. Renders exactly like a normal agent — no special UI.
