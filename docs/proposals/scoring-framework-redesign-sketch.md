# Scoring Framework Redesign — Outcome-Oriented Evaluation

**Status:** Sketch complete — ready for detailed design

## Problem

TARSy's session scoring exists to drive a continuous improvement loop: identify what to change in prompts, tools, chain config, and LLM selection to improve the quality of alert investigations. The scoring infrastructure ([ADR-0008](../adr/0008-session-scoring.md)) is solid — ScoringExecutor, ScoringController, session_scores table, dashboard UI all work well.

The problem is with **what the judge evaluates and how it reports findings**:

1. **Outcome and process are conflated, with process over-weighted.** The current 4 categories (Logical Flow, Consistency, Tool Relevance, Synthesis Quality) are all primarily process-focused. The prompt states "Process > Outcome." An investigation that reaches the wrong conclusion via methodical steps can outscore one that nails the root cause through a messy path. For the improvement goal, the most important question — "did the investigation produce a correct, useful result?" — is never asked directly.

2. **No structured failure signals for aggregation.** The analysis is a narrative. To find systemic patterns across many sessions, you'd have to read every report manually.

3. **Turn 2 (missing tools) only identifies new tools, not improvements to existing ones.** The judge sees every tool call with arguments and results, but is only asked about tools that don't exist — never about tools that exist but are hard for agents to use.

## Goal

Redesign the evaluation framework — the judge prompt, evaluation structure, and Turn 2 scope — so that scoring reports are directly useful for identifying what to improve in TARSy.

## What Changes

| Component | Change |
|---|---|
| `pkg/agent/prompt/judges.go` | Rewrite evaluation framework: outcome-first with ceiling mechanic, failure vocabulary, wider Turn 2 scope |
| `ent/schema/sessionscore.go` | One new JSON column: `failure_tags` |
| `pkg/agent/controller/scoring.go` | After extracting score, scan narrative for vocabulary terms → populate `failure_tags` |
| `pkg/queue/scoring_executor.go` | Pass failure tags to `completeScore` |
| DB migration | Add `failure_tags` column |

| Component | No change |
|---|---|
| Score extraction logic | Same number-on-last-line parsing |
| `ScoringExecutor` orchestration | Same 2-turn flow |
| `ScoringResult.TotalScore` | Same single 0-100 integer |
| Auto-trigger / re-score API | Same |
| `prompt_hash` versioning | Same |
| Dashboard badges / scoring page | Same (failure tags can be displayed later) |

## Evaluation Framework

### Outcome-first evaluation with ceiling mechanic

The judge evaluates in two phases but produces a single holistic score (0-100):

**Phase 1 — Assess investigation outcome.** Was the conclusion correct, well-supported, and actionable? This determines the score ceiling:
- Strong, evidence-backed conclusion → eligible for 60-100
- Partially correct or weakly supported → capped at 40-65
- Wrong or unsupported conclusion → capped at 0-40

**Phase 2 — Assess investigation process.** Evidence gathering, tool usage, reasoning quality, efficiency. This places the score within the ceiling set by Phase 1.

A wrong conclusion can never hide behind a good process. A correct conclusion reached via a messy path still scores higher than a wrong conclusion reached methodically. The narrative analysis covers both phases in detail.

This replaces the current 4 equal-weight categories (Logical Flow, Consistency, Tool Relevance, Synthesis Quality) and flips the explicit "Process > Outcome" principle.

### Failure vocabulary (reference, not constraint)

The prompt includes a list of common failure patterns as non-constraining guidance. The judge uses these terms when they fit, but freely describes any problem in its own words — evaluation quality always takes priority over tagging consistency.

The vocabulary consists exclusively of negative failure labels (e.g., `premature_conclusion`, `missed_available_tool`, `unsupported_confidence`, `incomplete_evidence`, `hallucinated_evidence`), making false-positive matches in positive context extremely unlikely.

After receiving the analysis, Go code does a simple `strings.Contains` scan for each known vocabulary term and stores matches as a JSON array in a `failure_tags` column on `session_scores`. No LLM output format requirements. No parsing fragility.

This enables aggregation queries like `WHERE failure_tags @> '["premature_conclusion"]'` to detect systemic patterns across sessions.

### Turn 2: Tool improvement report (expanded scope)

Turn 2 widens from "missing tools only" to a broader tool improvement report:

1. **Missing tools** — new MCP tools that should be built (same as today)
2. **Existing tool improvements** — the judge observes every tool call (with arguments) and result, so it can identify:
   - Tools with confusing argument names/structure (agent tried multiple argument combinations)
   - Tools that return data in formats hard for agents to use
   - Tools whose descriptions don't make clear when they're useful
   - Tools that require argument values the agent had no way to discover

The 2-turn flow stays the same. The `missing_tools_analysis` field stores the broader report.

## What Doesn't Change

- **Scoring infrastructure** — executor, auto-trigger, re-score API, WebSocket events, graceful shutdown
- **Single 0-100 score** — same storage, same extraction, same dashboard badges
- **2-turn structure** — score evaluation (Turn 1) + tool report (Turn 2)
- **Score extraction** — number on last line, regex, retry loop
- **Prompt hash versioning** — automatic via `GetCurrentPromptHash()`

## Out of Scope

- Ground truth / human labels — the judge remains LLM-based evaluation
- Multi-judge ensembles
- Score history analytics UI (trending, aggregation dashboards) — enabled by schema changes but built separately
- Structured sub-scores (outcome vs process as separate stored numbers) — single holistic score is sufficient; can be added later if needed
