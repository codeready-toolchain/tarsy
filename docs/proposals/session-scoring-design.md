# Session Scoring & Evaluation

**Status:** Final
**Decisions:** [session-scoring-questions.md](session-scoring-questions.md)

## Overview

TARSy runs automated incident investigations via agent chains. Today, completed sessions produce a final analysis and an executive summary, but there is no structured quality evaluation of the investigation itself.

Session scoring evaluates completed investigations to answer: **how good was this investigation?** The scoring produces:

1. **A numeric quality score** (0–100) across four categories: Logical Flow, Consistency, Tool Relevance, Synthesis Quality.
2. **A detailed score analysis** explaining deductions and strengths.
3. **A missing tools report** identifying MCP tools that should be built to improve future investigations.

These evaluation reports feed a continuous improvement loop: identify weak agent behavior, discover missing MCP tooling, tune prompts, and track quality trends over time.

### WIP State

Significant groundwork already exists:

- **ScoringController** (`pkg/agent/controller/scoring.go`) — 2-turn LLM flow (score + missing tools). Currently stateless and operates outside the stage data model (no timeline events, no message storage).
- **ScoringAgent** (`pkg/agent/scoring_agent.go`) — delegates to controller. Does not manage AgentExecution status (expects external lifecycle management).
- **Scoring prompts** (`pkg/agent/prompt/judges.go`) — detailed rubric and instructions with prompt hash versioning.
- **SessionScore schema** (`ent/schema/sessionscore.go`) — DB table with score fields, status lifecycle, prompt hash. Already supports multiple scores per session (O2M edge, partial unique index only on in-progress rows).
- **ResolveScoringConfig** (`pkg/agent/config_resolver.go`) — config resolution hierarchy.
- **ScoringConfig** (`pkg/config/types.go`) — YAML config structure.
- **Config validation** — scoring config validated per chain.

What's missing: the orchestration layer (`ScoringExecutor`), stage type system, executive summary refactoring, and dashboard integration.

## Design Principles

1. **Non-blocking**: Scoring must never delay session completion or degrade the user-facing investigation experience.
2. **Fail-open**: Scoring failures do not affect session status. A session is "completed" regardless of scoring outcome.
3. **Decoupled from investigation**: Scoring operates on the *output* of an investigation, not within it. It's an observer, not a participant.
4. **Configurable per chain**: Different chains may have different scoring needs (enabled/disabled, different LLM providers, etc.).
5. **Extensible**: The stage type system accommodates future post-investigation activities without major refactoring.
6. **Auditable**: Scoring results are traceable — prompt hash, LLM provider, timing, who triggered it.

## Architecture

### Stage Type System

Add a `stage_type` enum column to the `stages` table. All LLM-driven activities become typed stages:

| Stage Type | When Created | Fail Behavior | Created By |
|---|---|---|---|
| `investigation` | Chain loop | Fail-fast | `RealSessionExecutor` |
| `synthesis` | After multi-agent stages | Fail-fast | `RealSessionExecutor` |
| `exec_summary` | After all investigation stages | Fail-open | `RealSessionExecutor` |
| `chat` | On-demand (user follow-up) | Independent | `ChatMessageExecutor` |
| `scoring` | Async after session completion | Fail-open | `ScoringExecutor` |

Stage types enable composable context filtering:

| Need | Stage types included |
|------|---------------------|
| Build next-stage context | `investigation`, `synthesis` |
| Build chat context | `investigation`, `synthesis`, `chat` |
| Build scoring context | `investigation`, `synthesis`, `exec_summary` |
| Main timeline view | `investigation`, `synthesis` |
| Full session view | all |

This replaces the current implicit type detection (checking `chat_id` presence, name suffix " - Synthesis") with an explicit, queryable field.

**Schema migration:**

1. Add `stage_type` enum to `stages` table.
2. Backfill existing stages: stages with `chat_id` → `chat`, stages with name ending " - Synthesis" → `synthesis`, all others → `investigation`.
3. Add `StageType` field to `CreateStageRequest` (`pkg/models/stage.go`).

### Session Flow

```
Session claimed
  → [Investigation Stage 1] → [Investigation Stage 2] → ... → [Stage N]
  → [Exec Summary Stage]
  → Session marked COMPLETED
  → [Scoring Stage] (async, fire-and-forget)
  
Later (on-demand):
  → [Chat Stage] (user follow-up)
  → [Scoring Stage] (re-score via API)
```

The investigation chain and exec summary run inline within `RealSessionExecutor.Execute()`. The session is marked completed. Scoring fires asynchronously afterward — it never delays session completion.

### Executive Summary Refactoring

The current executive summary implementation is a special case: a direct LLM call (not through the agent/controller framework) that creates its own timeline event (sequence 999999) and LLM interaction (type `executive_summary`). It stores results directly on the session (`executive_summary`, `executive_summary_error` fields).

To make it a typed stage:

1. **Create a Stage record** (type: `exec_summary`) and an AgentExecution record, using the standard stage infrastructure.
2. **Run through the agent framework** — either via the existing SingleShotController or a dedicated controller. The exec summary is a single LLM call with no tools, which fits SingleShotController.
3. **Remove special-casing** — the sequence 999999 timeline event convention and `executive_summary` LLM interaction type are no longer needed; the stage infrastructure handles timeline ordering via `stage_index`.
4. **Keep the session-level `executive_summary` field** as a denormalized copy for quick access (session list, Slack notifications). The worker populates it from the exec summary stage's result, same as today. Similarly, `executive_summary_error` stays.
5. **Update `countExpectedStages()`** — currently counts exec summary as +1 for progress reporting. This logic remains correct since exec summary is now a real stage. Scoring is NOT counted (it's async and shouldn't appear in investigation progress).
6. **Update `ExecutionResult`** — `ExecutiveSummary` and `ExecutiveSummaryError` fields stay for the worker to persist the denormalized values.

### Scoring Execution Flow

The `ScoringExecutor` orchestrates the entire scoring workflow:

```
ScoringExecutor.ScoreSession(ctx, sessionID, triggeredBy)
  1. Load session, resolve chain config
  2. Check chain has scoring enabled (for auto-trigger; API re-score bypasses this check)
  3. Gather investigation context from DB
     (full timeline: LLM turns, tool calls + results, intermediate reasoning)
     (filtered by stage type: investigation + synthesis + exec_summary)
  4. Resolve scoring config (chain → defaults hierarchy) via ResolveScoringConfig
  5. Determine stage_index via GetMaxStageIndex (same pattern as chat stages)
  6. Create scoring Stage record (type: scoring)
  7. Create AgentExecution record
  8. Run ScoringController (2-turn LLM conversation)
     a. Turn 1: Score evaluation → total_score + score_analysis
     b. Turn 2: Missing tools analysis → missing_tools_analysis
  9. Update Stage + AgentExecution status
  10. Write to session_scores table (denormalized for analytics)
  11. Publish events (stage status, scoring complete)
```

The ScoringController remains lightweight — no timeline events or message storage within the controller itself. The ScoringExecutor handles all stage/execution bookkeeping around it (creating records, updating statuses, publishing events). This matches the existing pattern where the executor manages infrastructure and the controller manages the LLM conversation.

### ScoringExecutor

A small, focused executor in `pkg/queue/` with a single entry point:

```go
type ScoringExecutor struct {
    cfg            *config.Config
    dbClient       *ent.Client
    llmClient      agent.LLMClient
    promptBuilder  *prompt.PromptBuilder
    agentFactory   *agent.AgentFactory
    eventPublisher agent.EventPublisher
}

func (e *ScoringExecutor) ScoreSession(ctx context.Context, sessionID string, triggeredBy string) error
```

Two callers:
- **Worker** (auto-trigger): fires `ScoreSession()` in a background goroutine after session completion, if chain scoring is enabled.
- **API handler** (re-score): `POST /api/v1/sessions/:id/score` calls `ScoreSession()` for on-demand re-scoring.

### Worker Integration

The worker fires scoring after marking the session complete (step 10 in the current `processSession` flow):

```
10. Update terminal status
10a. Publish terminal session status event
10b. Send Slack notification
--> 10c. Fire scoring goroutine (if chain scoring enabled)
11. Cleanup transient events
```

Key details:

- **Context**: The scoring goroutine gets a fresh `context.Background()` with its own timeout (not the session context, which may be cancelled/timed-out). Scoring timeout is independent.
- **Dependency injection**: The worker receives `ScoringExecutor` at construction time (same pattern as `sessionExecutor`).
- **Graceful shutdown**: The worker pool must track active scoring goroutines and drain them on shutdown. A `sync.WaitGroup` or similar mechanism prevents the process from exiting while scoring is in progress.
- **Chain config**: The worker needs the chain ID from the session to check if scoring is enabled. The `ScoringExecutor.ScoreSession()` resolves the full chain config internally.
- **Non-completed sessions**: Scoring is only auto-triggered for sessions with `status: completed`. Failed/cancelled/timed-out sessions are not auto-scored.

### API Endpoint for Re-scoring

`POST /api/v1/sessions/:id/score`

- **Auth**: Same auth as session creation (oauth2-proxy).
- **Preconditions**: Session must exist. Session must be in a terminal state (`completed`, `failed`, etc.). If scoring is already in-progress for this session (checked via partial unique index), return `409 Conflict`.
- **Scoring enabled check**: The API endpoint does NOT require chain scoring to be enabled — re-scoring is always available on demand.
- **`triggeredBy`**: Extracted from the request auth context (same as `extractAuthor`).
- **Response**: `202 Accepted` with the created `session_score` ID. Scoring runs async; the caller polls or watches via WebSocket for the scoring stage status.

### Scoring as Sub-Status

The scoring stage's own status provides a natural sub-status without any new field on the session table:

- Session `completed` + no scoring stage → not scored
- Session `completed` + scoring stage `pending` → scoring queued
- Session `completed` + scoring stage `active` → scoring in progress
- Session `completed` + scoring stage `completed` → scored
- Session `completed` + scoring stage `failed` → scoring failed
- Session `completed` + scoring stage `timed_out` → scoring timed out
- Session `completed` + scoring stage `cancelled` → scoring cancelled

The frontend derives the scoring state by checking for a scoring-type stage and its status.

### Data Model

#### Stages table (migration)

Add `stage_type` enum: `investigation`, `synthesis`, `chat`, `exec_summary`, `scoring`.

#### session_scores table (existing + migration)

The table already exists and supports multiple scores per session (O2M relationship, partial unique index only on in-progress rows).

| Field | Type | Purpose |
|-------|------|---------|
| `score_id` | string | PK |
| `session_id` | string | FK to alert_sessions |
| `stage_id` | string | **NEW** — FK to scoring stage (nullable for pre-migration rows) |
| `prompt_hash` | string | SHA256 of judge prompts (versioning) |
| `total_score` | int | 0–100 |
| `score_analysis` | text | Detailed evaluation |
| `missing_tools_analysis` | text | Missing MCP tools report |
| `score_triggered_by` | string | Who/what triggered scoring |
| `status` | enum | pending, in_progress, completed, failed, timed_out, cancelled |
| `started_at` | time | When scoring was triggered |
| `completed_at` | time | When scoring finished |
| `error_message` | text | Error details if failed |

### Re-scoring

Re-scoring creates a new scoring stage and a new `session_scores` row. Old records are preserved as history. The dashboard shows the latest completed score. Re-scoring is triggered on-demand via `POST /api/v1/sessions/:id/score`.

The partial unique index prevents concurrent in-progress scorings per session, while allowing multiple completed scores.

### Context Gathering

The scoring LLM receives the full investigation timeline: all LLM turns, all tool calls with arguments and results, intermediate reasoning, and final analysis. Context is filtered by stage type — `investigation` + `synthesis` + `exec_summary` stages only (excluding `chat` and `scoring` stages).

For very long sessions, truncation of the oldest tool results is a pragmatic fallback to stay within the LLM's context window.

## Dashboard Integration

Three levels of detail:

1. **Session list**: Color-coded score badge (e.g. "72/100") on session list items.
2. **Session detail page**: Score visible alongside the investigation. Quick indicator.
3. **Dedicated scoring page**: Reached from the session detail. Shows full scoring reports (score analysis, missing tools report) and the scoring stage timeline (collapsed by default). Start minimal — just the reports. Analytics (trends, distributions) added later.

## Implementation Plan

### Phase 1: Stage Type System - ✅ DONE

See [ADR-0004: Stage Types](../adr/0004-stage-types.md) for full implementation spec.

- **PR 1:** Add `stage_type` enum field (5 values), wire for investigation/synthesis/chat, API/WS changes, chat context simplification. Additive, no behavior changes.
- **PR 2:** Refactor executive summary into a typed stage (`exec_summary`). Update context-building functions to filter by stage type.

### Phase 2: Scoring Pipeline - ✅ DONE

1. Create `ScoringExecutor` in `pkg/queue/scoring_executor.go`
2. Add `stage_id` FK to `session_scores` schema
3. Implement context gathering: build full timeline from DB, filtered by stage type
4. Wire auto-trigger: worker fires scoring goroutine after session completion (with graceful shutdown tracking)
5. Add re-score API endpoint: `POST /api/v1/sessions/:id/score` (202 Accepted, 409 if in-progress)
6. Integrate with existing ScoringController and ResolveScoringConfig
7. Write results to both stage/agent-execution and `session_scores`
8. Publish scoring events for real-time dashboard updates
9. Update ScoringAgent comment to reflect ScoringExecutor (currently references "ScoringService")

### Phase 3: Dashboard Integration

**Backend API additions** (needed before frontend work):

1. Add `latest_score` (nullable int) and `scoring_status` (nullable string) to `DashboardSessionItem` — computed via SQL subquery on `session_scores` (latest completed score per session)
2. Add `latest_score`, `scoring_status`, and `score_id` to `SessionDetailResponse` — same subquery approach
3. Add `GET /api/v1/sessions/:id/score` endpoint — returns the full `SessionScore` record (total_score, score_analysis, missing_tools_analysis, prompt_hash, score_triggered_by, timestamps, status)
4. Add `sort_by=score` option to session list for sorting by latest score
5. Add `scoring_status` filter option to session list (scored, not_scored, scoring_in_progress, scoring_failed)

**Frontend work:**

1. Score badge on session list items (color-coded: green ≥80, yellow ≥60, red <60)
2. Score indicator on session detail page with link to dedicated scoring view
3. Dedicated scoring page with reports (score analysis, missing tools report) and the scoring stage timeline (collapsed by default)
4. Handle "scoring in progress" (spinner), "not scored" (dash), and "scoring failed" (error badge) states
5. Real-time updates via existing WebSocket `stage.status` events for the scoring stage

**Note:** The scoring stage is already visible in the session detail's `stages` array (stage_type: "scoring"), so the frontend can derive scoring sub-status from stage presence + status even before the dedicated endpoints are built.

### Phase 4: Future Enhancements

1. Score analytics (trends, distributions, per-chain averages)
2. Aggregated missing tools report across sessions
3. Additional evaluation types (cost analysis, latency analysis)
