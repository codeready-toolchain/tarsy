# ADR-0008: Session Scoring & Evaluation

**Status:** Implemented
**Date:** 2026-03-09

## Overview

TARSy runs automated incident investigations via agent chains. Completed sessions produce a final analysis and an executive summary, but there is no structured quality evaluation of the investigation itself.

Session scoring evaluates completed investigations to answer: **how good was this investigation?** The scoring produces:

1. **A numeric quality score** (0–100) across four categories: Logical Flow, Consistency, Tool Relevance, Synthesis Quality.
2. **A detailed score analysis** explaining deductions and strengths.
3. **A missing tools report** identifying MCP tools that should be built to improve future investigations.

These evaluation reports feed a continuous improvement loop: identify weak agent behavior, discover missing MCP tooling, tune prompts, and track quality trends over time.

## Design Principles

1. **Non-blocking**: Scoring must never delay session completion or degrade the user-facing investigation experience.
2. **Fail-open**: Scoring failures do not affect session status. A session is "completed" regardless of scoring outcome.
3. **Decoupled from investigation**: Scoring operates on the *output* of an investigation, not within it. It's an observer, not a participant.
4. **Configurable per chain**: Different chains may have different scoring needs (enabled/disabled, different LLM providers, etc.).
5. **Extensible**: The stage type system accommodates future post-investigation activities without major refactoring.
6. **Auditable**: Scoring results are traceable — prompt hash, LLM provider, timing, who triggered it.

## Key Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Where does scoring live in the architecture? | Expand stages with explicit `stage_type` enum | Unifies the execution model, solves existing implicit-type-detection problems, and enables composable context/UI filtering. The refactoring cost is paid once and benefits all current and future stage types. See [ADR-0004](0004-stage-types.md). |
| Q2 | Inline or async? | Async after session completion | Scoring is post-work that must not delay the investigation. Zero impact on session completion latency, independent timeout, fail-open by construction. |
| Q3 | Where does orchestration logic live? | Separate `ScoringExecutor` in `pkg/queue/` | Same executor pattern, scoped to the scoring workflow. Keeps `RealSessionExecutor.Execute()` focused on the investigation chain. Clean dependency for both callers (worker auto-trigger, API re-score). |
| Q4 | How is scoring triggered? | Automatic + API | Worker auto-triggers after successful completion (if chain scoring enabled). `POST /api/v1/sessions/:id/score` for on-demand re-scoring. Both paths use the same `ScoringExecutor.ScoreSession()`. |
| Q5 | Dashboard presentation | Badge → detail → dedicated page | Three progressive levels: color-coded score badge on session list, score indicator on session detail page, dedicated scoring page with full reports and scoring stage timeline. |
| Q6 | What context does the scoring LLM receive? | Full investigation timeline | All LLM turns, tool calls with arguments and results, intermediate reasoning, final analysis. Filtered by stage type (`investigation` + `synthesis` + `exec_summary`). Truncation of oldest tool results as fallback for very long sessions. |
| Q7 | Re-scoring support | Multiple scores per session (keep all, use latest) | Each re-score creates a new scoring stage and `session_scores` row. Old records preserved as history. Enables A/B testing of scoring prompts via `prompt_hash`. Partial unique index prevents concurrent in-progress scorings while allowing multiple completed scores. |

## Architecture

### Stage Type System

The `stage_type` enum column on the `stages` table classifies all LLM-driven activities:

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

This replaces the previous implicit type detection (checking `chat_id` presence, name suffix " - Synthesis") with an explicit, queryable field. The stage type system is fully specified in [ADR-0004: Stage Types](0004-stage-types.md).

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

The ScoringController persists LLM interactions and creates streaming timeline events via `callLLMWithStreaming`, matching the pattern used by other controllers. The ScoringExecutor handles stage/execution bookkeeping (creating records, updating statuses, publishing events, writing to `session_scores`).

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

The worker fires scoring after marking the session complete (step 10 in `processSession`):

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
- **Graceful shutdown**: The worker pool tracks active scoring goroutines and drains them on shutdown via `sync.WaitGroup`.
- **Non-completed sessions**: Scoring is only auto-triggered for sessions with `status: completed`. Failed/cancelled/timed-out sessions are not auto-scored.

### API Endpoint for Re-scoring

`POST /api/v1/sessions/:id/score`

- **Auth**: Same auth as session creation (oauth2-proxy). Any authenticated user may trigger re-scoring — no ownership check. `extractAuthor` is used only to record who triggered the re-score, not for authorization.
- **Preconditions**: Session must exist. Session must be in a terminal state (`completed`, `failed`, etc.). If scoring is already in-progress for this session (checked via partial unique index), return `409 Conflict`.
- **Scoring enabled check**: The API endpoint does NOT require chain scoring to be enabled — re-scoring is always available on demand.
- **`triggeredBy`**: Extracted from the request auth context (same as `extractAuthor`).
- **Response**: `202 Accepted` with the created `session_score` ID. Scoring runs async; the caller polls or watches via WebSocket for the scoring stage status.
- **Abuse protection**: The `409 Conflict` on concurrent in-progress scoring is the only guard currently implemented. Future considerations if abuse is observed: per-user rate limits (e.g. 10/hour), per-session daily caps (e.g. 5/day), and monitoring thresholds.

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

## Data Model

### Stages table (migration)

`stage_type` enum: `investigation`, `synthesis`, `chat`, `exec_summary`, `scoring`.

### session_scores table

The table supports multiple scores per session (O2M relationship, partial unique index only on in-progress rows).

| Field | Type | Purpose |
|-------|------|---------|
| `score_id` | string | PK |
| `session_id` | string | FK to alert_sessions |
| `stage_id` | string | FK to scoring stage (nullable for pre-migration rows) |
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

For very long sessions, truncation of the oldest tool results is a pragmatic fallback to stay within the LLM's context window. No truncation logic is currently implemented — the full context is passed as-is, relying on the large context windows of modern LLMs (1M+ tokens). If context limits become a practical issue, a truncation strategy should be implemented (e.g. removing tool call results from oldest stages first while preserving arguments, final analysis, and synthesis outputs).

## Dashboard Integration

Three levels of detail:

1. **Session list**: Color-coded score badge (e.g. "72/100") on session list items. Color coding: green ≥80, yellow ≥60, red <60.
2. **Session detail page**: Score visible alongside the investigation with link to dedicated scoring view.
3. **Dedicated scoring page**: Reached from the session detail. Shows full scoring reports (score analysis, missing tools report) and the scoring stage timeline (collapsed by default).

### Backend API

- `latest_score` (nullable int) and `scoring_status` (nullable string) added to `DashboardSessionItem` — computed via SQL subquery on `session_scores` (latest completed score per session)
- `latest_score`, `scoring_status`, and `score_id` added to `SessionDetailResponse` — same subquery approach
- `GET /api/v1/sessions/:id/score` — returns the full `SessionScore` record (total_score, score_analysis, missing_tools_analysis, prompt_hash, score_triggered_by, timestamps, status)
- `sort_by=score` option for session list sorting by latest score
- `scoring_status` filter option for session list (scored, not_scored, scoring_in_progress, scoring_failed)

#### Query Performance

The `latest_score` and `scoring_status` fields on `DashboardSessionItem` are computed via per-session SQL subqueries (`SELECT total_score/status FROM session_scores WHERE session_id = ? ORDER BY started_at DESC LIMIT 1`). The `session_scores` schema already has indexes on `(session_id, status)` and `(status, started_at)` which partially cover these queries. If performance degrades at scale, consider adding a denormalized `latest_score_id` FK on `alert_sessions` (updated on scoring completion) to eliminate the subqueries entirely.

### Frontend

- Score badge on session list items (color-coded)
- Score indicator on session detail page with link to dedicated scoring view
- Dedicated scoring page with reports (score analysis, missing tools report) and the scoring stage timeline (collapsed by default)
- Handles "scoring in progress" (spinner), "not scored" (dash), and "scoring failed" (error badge) states
- Real-time updates via existing WebSocket `stage.status` events for the scoring stage

The scoring stage is visible in the session detail's `stages` array (stage_type: "scoring"), so the frontend derives scoring sub-status from stage presence + status. When re-scoring preserves older stages, the frontend picks the **latest** scoring stage (highest `stage_index` where `stage_type === "scoring"`) to avoid displaying stale status.

## Implementation Components

### Existing (pre-design)

- **ScoringController** (`pkg/agent/controller/scoring.go`) — 2-turn LLM flow (score + missing tools). Persists LLM interactions and creates streaming timeline events via `callLLMWithStreaming`.
- **ScoringAgent** (`pkg/agent/scoring_agent.go`) — delegates to controller.
- **Scoring prompts** (`pkg/agent/prompt/judges.go`) — detailed rubric and instructions with prompt hash versioning.
- **SessionScore schema** (`ent/schema/sessionscore.go`) — DB table with score fields, status lifecycle, prompt hash.
- **ResolveScoringConfig** (`pkg/agent/config_resolver.go`) — config resolution hierarchy.
- **ScoringConfig** (`pkg/config/types.go`) — YAML config structure.

### Phase 1: Stage Type System

Implemented as [ADR-0004: Stage Types](0004-stage-types.md):
- `stage_type` enum field (5 values), wired for investigation/synthesis/chat
- Executive summary refactored into a typed stage (`exec_summary`)
- Context-building functions updated to filter by stage type

### Phase 2: Scoring Pipeline

- `ScoringExecutor` in `pkg/queue/scoring_executor.go`
- `stage_id` FK added to `session_scores` schema
- Context gathering: full timeline from DB, filtered by stage type
- Auto-trigger: worker fires scoring goroutine after session completion (with graceful shutdown tracking)
- Re-score API endpoint: `POST /api/v1/sessions/:id/score` (202 Accepted, 409 if in-progress)
- Integration with ScoringController and ResolveScoringConfig
- Results written to both stage/agent-execution and `session_scores`
- Scoring events published for real-time dashboard updates

### Phase 3: Dashboard Integration

- Backend: `latest_score`/`scoring_status` on list and detail responses, `GET /score` endpoint, sort/filter support
- Frontend: score badge, session detail indicator, dedicated scoring page, real-time updates
