# Session Scoring — Design Questions

**Status:** All decisions made
**Related:** [Design document](session-scoring-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where does scoring live in the architecture?

The core architectural question: how does scoring relate to the existing session → stage → agent execution hierarchy? This affects data modeling, the executor flow, progress reporting, and how scoring interacts with the real-time event system.

### Option A: Expand the stage concept with explicit stage types (chosen)

Add a `stage_type` enum column to the `stages` table (values: `investigation`, `synthesis`, `chat`, `exec_summary`, `scoring`). Everything that runs an LLM — investigation, synthesis, exec summary, scoring, chat — becomes a typed stage. Stage types enable composable context filtering:

| Need | Stage types included |
|------|---------------------|
| Build next-stage context | `investigation`, `synthesis` |
| Build chat context | `investigation`, `synthesis`, `chat` |
| Build scoring context | `investigation`, `synthesis`, `exec_summary` |
| Main timeline view | `investigation`, `synthesis` |
| Full session view | all |

The executor flow: investigation stages run in the chain loop (fail-fast, context passing). Exec summary and scoring run as typed stages *after* the loop (fail-open). Chat stages remain on-demand via `ChatMessageExecutor`.

- **Pro:** Unified execution model — everything goes through stage → agent execution infrastructure (status, timing, token tracking, timeline events, LLM interactions).
- **Pro:** Stage types solve an existing problem: implicit type detection via `chat_id` presence or name suffix " - Synthesis" is fragile.
- **Pro:** Composable context filtering by type is clean and extensible.
- **Pro:** Marginal cost of running exec summary as a stage is near-zero once the infrastructure exists.
- **Pro:** UI pages can filter to show only relevant stage types.
- **Con:** Requires schema migration to add `stage_type` and backfill existing stages.
- **Con:** Exec summary refactoring needed (currently special-cased with sequence 999999 timeline event).

The `session_scores` table remains as a **denormalized query target** for analytics (score trends, distributions, missing tools aggregation), while the stage provides execution tracking.

**Decision:** Option A — Expand stages with explicit types. Unifies the execution model, solves existing implicit-type-detection problems, and enables composable context/UI filtering. The refactoring cost is paid once and benefits all current and future stage types.

_Considered and rejected: Session-level activity like executive summary (would create a parallel execution model and still need stage types for chat/investigation filtering), Post-session job (unnecessary infrastructure complexity with no existing job system to piggyback on), Session-level actions/jobs as a new entity (would re-implement stage infrastructure for a parallel concept while still needing stage types anyway)._

---

## Q2: Should scoring run inline (within the executor) or async (after the worker finishes)?

Given Q1's decision that scoring is a typed stage, this asks: does the scoring stage run inside `Execute()` (inline, after the exec summary stage) or is it fired off asynchronously after the session is marked complete?

### Option B: Async after session completion (chosen)

The executor returns after the exec summary stage. The worker marks the session complete, then fires off the scoring stage in a background goroutine via the `ScoringExecutor`. The scoring stage still creates a Stage record and AgentExecution, just not within the main executor flow.

- **Pro:** Zero impact on session completion latency. Users see investigation results immediately.
- **Pro:** Scoring can have its own timeout independent of the session timeout.
- **Pro:** If scoring fails or hangs, the session is already safely completed.
- **Pro:** Aligns with fail-open principle: scoring is observational and should never block the investigation.
- **Con:** Goroutine lifecycle management — needs careful handling during worker shutdown (graceful drain).
- **Con:** Dashboard needs to handle the "score not yet available" state (scoring stage appears after session is already "completed").

**Decision:** Option B — Async after session completion. Scoring is post-work that must not delay the investigation. The session is marked `completed` first, then scoring fires asynchronously.

The stage type system provides a natural "sub-status" — the scoring stage's own status (`pending`, `active`, `completed`, `failed`) indicates scoring progress without any new field on the session table. The frontend derives the scoring state by checking for a scoring-type stage and its status.

Re-scoring creates a new scoring stage (new `stage_index`, new `session_scores` row). Old scoring stages and score records are kept as history — simpler than deletion logic, and the dashboard uses the latest completed scoring stage.

_Considered and rejected: Inline in Execute() (blocks session completion for 10–30s, session timeout applies to scoring), Inline with separate timeout (still adds latency, user shouldn't wait for scoring)._

---

## Q3: Where does scoring orchestration logic live?

The ScoringController handles the LLM conversation (2-turn flow, score extraction). Services handle DB CRUD. What's missing is the orchestration glue: gather context from DB, resolve config, create scoring stage + agent execution, run the controller, store results, publish events.

In the existing codebase, orchestration lives at the executor level (`RealSessionExecutor`). Services are thin DB wrappers. The question is whether scoring orchestration is a method on the existing executor or a separate component.

### Option B: Separate ScoringExecutor (chosen)

A small, focused `ScoringExecutor` in `pkg/queue/` with a single method: `ScoreSession(ctx, sessionID, triggeredBy) error`. Takes only the dependencies it needs (config, dbClient, llmClient, promptBuilder, agentFactory, eventPublisher).

- **Pro:** Follows the executor pattern without bloating `RealSessionExecutor`.
- **Pro:** Clean dependency for both callers: worker (auto-trigger) and API handler (re-score endpoint).
- **Pro:** Focused scope — easy to understand, test, and modify.
- **Con:** New type, but small and justified.

**Decision:** Option B — Separate `ScoringExecutor` in `pkg/queue/`. Same executor pattern, scoped to the scoring workflow. The worker calls it async after session completion (auto-trigger). The API handler calls it for on-demand re-scoring. Keeps `RealSessionExecutor.Execute()` focused on the investigation chain.

_Considered and rejected: Method on RealSessionExecutor (bloats the executor, creates awkward dependency from API → queue), "ScoringService" as full orchestrator (breaks the naming convention where services are thin DB wrappers)._

---

## Q4: How should scoring be triggered?

When does scoring actually run?

### Option C: Automatic + API (chosen)

Scoring runs automatically for chains that have scoring enabled (worker fires `ScoringExecutor.ScoreSession()` async after session completion), AND there's an API endpoint (`POST /api/v1/sessions/:id/score`) for on-demand re-scoring.

- **Pro:** Consistent automated coverage for the improvement loop + ability to re-score on demand.
- **Pro:** Both paths use the same `ScoringExecutor.ScoreSession()` — no duplication.
- **Pro:** API endpoint enables tooling (batch re-scoring after prompt changes).

**Decision:** Option C — Automatic + API. Worker auto-triggers after successful completion (if chain scoring enabled). API endpoint for on-demand re-scoring.

_Considered and rejected: Automatic-only (no way to re-score or score on demand), API-only (inconsistent coverage, no automated improvement loop)._

---

## Q5: How should scoring results appear in the dashboard?

How do users consume scoring data?

### Decision: Badge + detail view + dedicated scoring page

Three levels of detail, progressively deeper:

1. **Session list**: Score badge (color-coded, e.g. "72/100") on session list items. Quick visual indicator of investigation quality.
2. **Session detail page**: Score visible in the session view. Enough to see the score at a glance alongside the investigation.
3. **Dedicated scoring page**: Reached from the session detail (e.g. click the score badge or a link). Shows the full scoring reports (score analysis, missing tools report) and the scoring stage timeline (collapsed by default). Start minimal — just the reports and timeline for now. Analytics (trends, distributions, aggregated missing tools) can be added later as the page evolves.

This keeps the session list and detail views uncluttered while giving a clear path to the full scoring data.

_Considered and rejected: Putting full reports inline on the session detail page (too much clutter), Building a heavy analytics page upfront (premature — enhance later when there's data to analyze)._

---

## Q6: What investigation context should be passed to the scoring LLM?

The scoring LLM needs to evaluate the investigation. What data does it receive?

### Option B: Full timeline (chosen)

Reconstruct the full investigation timeline: every LLM turn, every tool call with arguments and results, every intermediate analysis. Everything that can help the scoring agent make a judgment.

- **Pro:** The scorer sees everything the agent did, enabling evaluation of methodology, tool selection, and logical flow.
- **Pro:** This is what the existing scoring prompts expect — they reference "all MCP tool interactions and their results."
- **Pro:** Comprehensive analysis requires comprehensive context.
- **Con:** Can be very large (tens of thousands of tokens for complex investigations). Higher cost per scoring call.
- **Con:** May exceed context window for very long investigations — truncation of oldest tool results as a fallback.

**Decision:** Option B — Full timeline. The scoring agent needs the complete picture to evaluate investigation quality. Context includes: all LLM turns, all tool calls with arguments and results, intermediate reasoning, and final analysis. Filtered by stage type (`investigation` + `synthesis` + `exec_summary`, excluding `chat` and `scoring` stages). For very long sessions, truncation of the oldest tool results is a pragmatic fallback.

_Considered and rejected: Final analysis only (can't evaluate methodology, only the end result), Curated context with summarized tool results (loses nuance, adds summarization complexity)._

---

## Q7: Should scoring support re-scoring?

Can a session be scored more than once?

### Option A: Single score per session

One score per session, ever. The partial unique index (`session_id` WHERE `status IN ('pending', 'in_progress')`) prevents concurrent scoring, and completed scores are permanent.

- **Pro:** Simple. One score, one truth.
- **Con:** Can't re-evaluate after improving scoring prompts. Can't compare scoring versions.

### Option B: Multiple scores per session (keep all, use latest)

Allow multiple scores per session. Each re-score creates a new scoring stage and a new `session_scores` row. Old ones are preserved as history. The UI always shows the latest completed score. Re-scoring is triggered on demand via API.

- **Pro:** Simpler than deletion — just create new records, no soft-delete or cleanup logic.
- **Pro:** History is preserved for free. Enables A/B testing of scoring prompts via `prompt_hash`.
- **Pro:** The partial unique index already prevents concurrent in-progress scorings while allowing multiple completed scores.
- **Pro:** Aligns with stage model — stages are immutable records of what happened.
- **Con:** DB accumulates old scoring records (negligible storage cost).

### Option C: Replace-on-rescore (latest wins)

Re-scoring deletes or soft-deletes the previous score. Only one score exists at a time, but it can be replaced.

- **Pro:** Simple UI — always one score.
- **Con:** Deletion logic, race conditions on concurrent re-score requests.
- **Con:** Loses history. Can't compare prompt versions.

**Decision:** Option B — Keep all scores, use the latest. Re-scoring creates a new scoring stage and `session_scores` row. Old records stay as history. The dashboard shows the latest completed score. Re-scoring is on-demand via API (`POST /api/v1/sessions/:id/score`).

**Schema note:** The existing `session_scores` schema already supports multiple scores per session (O2M edge, partial unique index only on in-progress rows). One migration needed: add an optional `stage_id` FK to `session_scores` to link each score to its scoring stage for traceability. Historical scores (if any exist before migration) will have `stage_id = NULL`.

_Considered and rejected: Single score per session (can't re-evaluate after prompt improvements), Replace-on-rescore (deletion logic is more complex than just creating new records, loses history)._
