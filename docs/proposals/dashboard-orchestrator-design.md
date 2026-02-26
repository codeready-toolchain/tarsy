# Dashboard Orchestrator Support — Implementation Design

**Status:** Draft — pending decisions from [dashboard-orchestrator-questions.md](dashboard-orchestrator-questions.md)
**Related:** [orchestrator-impl-design.md](orchestrator-impl-design.md)
**Last updated:** 2026-02-26

## Overview

This document designs the dashboard changes needed to display orchestrator agent executions with clear parent-child hierarchy. The backend (PR0–PR6) already serves nested sub-agent data in API responses and publishes WebSocket events for sub-agent executions. The dashboard must surface this data in both the Reasoning view (SessionDetailPage) and the Trace view (TracePage), with real-time streaming support.

The core challenge: orchestrator sub-agents are not parallel agents (statically defined at config time) — they're dynamically dispatched at runtime. The dashboard must show **when** they were dispatched, **what task** they received, their **real-time progress**, and their **results** — all nested under the parent orchestrator execution.

## Design Principles

1. **Backend already done.** The API responses (`ExecutionOverview.sub_agents`, `TraceExecutionGroup.sub_agents`) already nest sub-agents correctly. The dashboard consumes existing data — no new API endpoints needed.

2. **Reuse existing components.** The dashboard has battle-tested patterns for parallel agents (tabbed views, agent cards, streaming renderers). Adapt these for orchestrator sub-agents rather than building from scratch.

3. **Progressive disclosure.** Show the orchestrator's own reasoning flow at the top level. Sub-agent details are available via expand/drill-down, not forced onto the user.

4. **Real-time from day one.** Sub-agent progress must stream in real-time, matching the existing experience for regular agents. No "refresh to see sub-agent results."

## Architecture

### What the Backend Already Provides

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
- Each event has `execution_id` — sub-agent events have the sub-agent's execution_id
- No `parent_execution_id` on timeline events (it's on AgentExecution, not TimelineEvent)

**WebSocket events**:
- Sub-agents publish the same events: `timeline_event.created`, `stream.chunk`, `execution.status`, `execution.progress`
- Events carry `execution_id` — the sub-agent's own ID
- No `parent_execution_id` in event payloads

### Component Change Map

```
SessionDetailPage ──────── No structural changes. Types + WS handler updates.
  └─ ConversationTimeline ─ Passes sub-agent overviews to StageContent.
      └─ StageContent ───── Detects orchestrator executions, renders sub-agent
                             section below the orchestrator's own items.

TracePage ─────────────── No structural changes. Types update.
  └─ TraceTimeline ─────── No changes (renders stage accordions as before).
      └─ StageAccordion ── Detects orchestrator, renders sub-agents.
          └─ ParallelExecutionTabs ── Handles sub_agents on TraceExecutionGroup
                                       (or new SubAgentSection component).

Types/session.ts ──── Add parent_execution_id, task, sub_agents to ExecutionOverview.
Types/events.ts ───── Add parent_execution_id to ExecutionStatusPayload + ExecutionProgressPayload.
```

### Data Flow for Sub-Agent Events

```
1. Sub-agent starts → execution.status { execution_id: "sub-123", status: "active" }
2. Sub-agent streams → timeline_event.created + stream.chunk (execution_id: "sub-123")
3. Sub-agent completes → execution.status { execution_id: "sub-123", status: "completed" }

Problem: How does the dashboard know "sub-123" belongs to orchestrator "orch-456"?

Solution options:
  A. Add parent_execution_id to WS event payloads (backend change)
  B. Build a mapping from REST data (ExecutionOverview.sub_agents)
  C. Don't map — just re-fetch on sub-agent events
```

> **Open question:** How to associate sub-agent WS events with their parent orchestrator — see [questions document](dashboard-orchestrator-questions.md), Q1.

## Reasoning View (SessionDetailPage)

### Timeline Item Flow for Orchestrator Stages

When the orchestrator runs, the timeline contains:
1. **Orchestrator thinking** — LLM reasoning about what to investigate
2. **dispatch_agent tool calls** — tool call items with `server_name: "orchestrator"`, `tool_name: "dispatch_agent"`
3. **More thinking** — LLM processing, possibly dispatching more agents
4. **list_agents / cancel_agent** — status check or cancellation tool calls
5. **Sub-agent result messages** — injected user-role messages like `[Sub-agent completed] LogAnalyzer (exec abc): ...`
6. **Final analysis** — orchestrator's synthesized output

All these items already flow through the existing pipeline as normal `FlowItem`s with the orchestrator's `execution_id`. The orchestrator's reasoning view works out of the box.

### Sub-Agent Visibility in Reasoning View

> **Open question:** How to display sub-agent detail in the Reasoning view — see [questions document](dashboard-orchestrator-questions.md), Q2.

### Orchestrator Detection

An execution is an orchestrator if:
- Its `ExecutionOverview` has `sub_agents` array with length > 0
- Or: the timeline contains `dispatch_agent` tool calls for this execution (metadata: `server_name: "orchestrator"`, `tool_name: "dispatch_agent"`)

The first approach (REST-based) is more reliable and immediate. The second is a fallback during streaming before REST data is available.

## Trace View (TracePage)

### Current Structure

```
StageAccordion
├─ Single agent: metadata box + interaction cards
└─ Parallel agents: ParallelExecutionTabs (tabs per agent)
    └─ Per tab: metadata box + interaction cards
```

### With Orchestrator Sub-Agents

The trace API already nests `sub_agents` inside `TraceExecutionGroup`. The trace view needs to render this nesting.

> **Open question:** How to display sub-agents in the Trace view — see [questions document](dashboard-orchestrator-questions.md), Q3.

### Interaction Count Adjustments

`countStageInteractions` currently sums across `stage.executions[]`. Sub-agents are nested inside parent executions. The function needs to optionally include sub-agent interactions in the count, or show them separately.

> **Open question:** How to count interactions — see [questions document](dashboard-orchestrator-questions.md), Q4.

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

These fields already exist in the Go `ExecutionOverview` struct (added in PR2). The TypeScript type just needs to match.

### `TraceExecutionGroup` (types/trace.ts)

Already has `sub_agents?: TraceExecutionGroup[]` — no change needed.

### WebSocket Event Payloads

> **Open question:** Whether to add `parent_execution_id` to WS payloads — see [questions document](dashboard-orchestrator-questions.md), Q1.

## Real-Time Streaming for Sub-Agents

### Timeline Events

Sub-agent timeline events (thinking, tool calls, responses) arrive on the same `session:{id}` WebSocket channel. They have the sub-agent's own `execution_id` and `stage_id` (same stage as the orchestrator).

**Problem:** `StageContent` groups items by `execution_id`. Sub-agent items will create additional execution groups within the same stage, appearing as parallel agents alongside the orchestrator.

> **Open question:** How to handle sub-agent timeline items in the Reasoning view — see [questions document](dashboard-orchestrator-questions.md), Q5.

### Execution Status Events

`execution.status` events for sub-agents carry the sub-agent's `execution_id`. The current `executionStatuses` map in SessionDetailPage stores all execution statuses regardless of parentage.

**Impact on StageContent:** The `mergedExecutions` logic in StageContent builds execution groups from items + streaming + REST overviews + WS statuses. Sub-agent `execution.status` events (with the same `stage_id`) would create phantom agent cards in the orchestrator's stage.

> **Open question:** How to filter sub-agent execution status events — see [questions document](dashboard-orchestrator-questions.md), Q6.

## Dashboard List View (DashboardPage)

No changes needed. `DashboardSessionItem` doesn't include execution-level details. Sessions with orchestrator agents look the same in the list — the chain/stage/status view is sufficient.

## Implementation Phases

> **Open question:** Should this be one PR or split? — see [questions document](dashboard-orchestrator-questions.md), Q7.

### Work Items

1. **Type updates**: Add `parent_execution_id`, `task`, `sub_agents` to `ExecutionOverview`
2. **Trace view**: Render sub-agents nested under parent in `StageAccordion` / `ParallelExecutionTabs`
3. **Reasoning view**: Show sub-agent information within the orchestrator's timeline
4. **Real-time**: Handle sub-agent WS events correctly (filtering, mapping)
5. **Trace helpers**: Update `findExecutionOverview` to search nested sub-agents; update `countStageInteractions` for sub-agent interactions
6. **Timeline parser**: Handle sub-agent timeline events (filtering or nesting)
7. **Testing**: Update/add Vitest tests for new components and modified logic

## Edge Cases

### Orchestrator as Sole Agent in Stage (Common Case)
The orchestrator is typically the only agent in its stage. StageContent renders it as a single agent (no tabs). Sub-agents appear as a nested section within.

### Orchestrator in Parallel Stage (Rare)
Multiple agents in the same stage, one of which is an orchestrator. The tabbed view shows all agents; the orchestrator tab has sub-agent nesting inside it.

### Multiple Orchestrators in Same Stage (Edge)
Each orchestrator has its own sub-agents. Sub-agents are scoped by `parent_execution_id` — no collision.

### Sub-Agent Failure
Backend sets `status: "failed"` on the sub-agent `ExecutionOverview`. The orchestrator itself may complete successfully. Show the sub-agent failure clearly without implying the orchestrator failed.

### Session Cancellation
All executions (orchestrator + sub-agents) end up `cancelled`. The dashboard already handles cancelled state — just needs to show it for nested sub-agents too.

### No Sub-Agents Dispatched
Orchestrator runs but decides no sub-agents are needed (edge case). `sub_agents` is empty. Renders exactly like a normal agent — no special UI.
