# ADR-0011: Scoring Framework Redesign

**Status:** Implemented
**Date:** 2026-03-12
**Related:** [ADR-0008: Session Scoring](0008-session-scoring.md)

## Overview

This ADR documents the redesign of the judge evaluation prompts to be outcome-first, the addition of a failure vocabulary with server-side tag extraction, and the expansion of Turn 2 to cover existing tool improvements alongside missing tools.

The scoring infrastructure (ScoringExecutor, ScoringController, 2-turn flow, auto-trigger, re-score API, dashboard) is unchanged. The changes are:

1. **New prompts** in `pkg/agent/prompt/judges.go`
2. **Failure vocabulary + tag extraction** in `pkg/agent/prompt/vocabulary.go` (new) and `pkg/agent/controller/scoring.go`
3. **Schema changes** on `session_scores`: rename `missing_tools_analysis` → `tool_improvement_report`, add `failure_tags` column
4. **Plumbing** to pass tags through ScoringResult → completeScore → DB

## Problem

TARSy's session scoring exists to drive a continuous improvement loop: identify what to change in prompts, tools, chain config, and LLM selection to improve the quality of alert investigations. The scoring infrastructure ([ADR-0008](0008-session-scoring.md)) is solid — ScoringExecutor, ScoringController, session_scores table, dashboard UI all work well.

The problem was with **what the judge evaluates and how it reports findings**:

1. **Outcome and process were conflated, with process over-weighted.** The 4 categories (Logical Flow, Consistency, Tool Relevance, Synthesis Quality) were all primarily process-focused. The prompt stated "Process > Outcome." An investigation that reaches the wrong conclusion via methodical steps could outscore one that nails the root cause through a messy path.

2. **No structured failure signals for aggregation.** The analysis was a narrative. Finding systemic patterns across many sessions required reading every report manually.

3. **Turn 2 (missing tools) only identified new tools, not improvements to existing ones.** The judge sees every tool call with arguments and results, but was only asked about tools that don't exist — never about tools that exist but are hard for agents to use.

## Design Principles

1. **Outcome > Process** — a wrong conclusion can never produce a high score, regardless of process quality. A flawed conclusion indicates a flawed process.
2. **Structured analysis, holistic judgment** — the judge evaluates five broad dimensions before scoring, grounding its thinking. The score itself remains holistic — dimensions interact in ways that fixed-weight formulas cannot capture. Based on EvalPlanner (ICML 2025) showing structured-then-holistic outperforms both pure decomposition and pure holistic evaluation.
3. **Don't constrain the judge** — dimensions are broad and universally applicable; the failure vocabulary is guidance, not a hard requirement; evaluation quality always takes priority over structural compliance
4. **Evidence-anchored** — every claim in a dimension assessment must cite specific timeline events, making evaluations verifiable and actionable
5. **Single source of truth** — the failure vocabulary Go slice drives both prompt generation and tag scanning
6. **Minimal infrastructure changes** — same single score, same extraction method, same 2-turn flow
7. **Backward compatible** — the `failure_tags` column is nullable; old scores with NULL tags work fine

### Why not Decomposed Atomic Evaluation (DeCE)?

DeCE (EMNLP 2025) achieves high correlation with human judgment by decomposing evaluation into independent, mechanically-scorable criteria (precision and recall against a reference answer). We evaluated this approach and rejected it for TARSy because:

1. **No reference answer** — DeCE requires a gold standard to decompose against. TARSy's judge doesn't know the actual root cause; it evaluates reasoning quality under uncertainty.
2. **Interdependent criteria** — in DeCE's domain, "fact X is present" and "fact Y is present" are independent. In TARSy, "conclusion correct" and "all tools used" interact — a correct conclusion despite missing tools means something different than a wrong conclusion despite using all tools. Fixed-weight sums can't capture these interactions without reimplementing holistic judgment in code.
3. **LFQA-E (ICLR 2026)** confirmed that no automatic metric performs comparably to human judgment for long-form evaluation — the class of problem TARSy's evaluation falls into.

The structured-then-holistic approach (EvalPlanner, ICML 2025) captures the consistency benefit of decomposition — forcing the LLM to analyze before judging — without requiring criteria to be independent or mechanically scorable.

## Key Decisions

### D1: Single holistic score with outcome-first ceiling mechanic

Keep a single 0-100 score. The judge evaluates in two phases — first outcome (conclusion quality), then process — but produces one holistic number. Conclusion quality determines the score range via non-overlapping ceilings:

| Outcome quality | Score range |
|---|---|
| Correct, well-supported conclusion | 60-100 |
| Partially correct or weakly supported | 35-59 |
| Wrong or unsupported conclusion | 0-34 |

A flawed conclusion indicates flaws in the process — even if individual steps looked methodical, something went wrong. This natural correlation between process and outcome eliminates the need for overlapping ranges or edge case caveats.

**Rationale:** The score directly answers "was this investigation good?" with outcome as the dominant factor. Two-phase evaluation structures the judge's thinking without requiring structured sub-score extraction. Zero infrastructure changes — the change is purely in the prompt. Rejected: 5 sub-scores (extraction complexity), 2 stored scores (schema/API/UI changes), overlapping ranges (unnecessary given the correlation).

### D2: Start small with ~6 failure tags, dynamic injection from Go slice

A Go slice of `{term, description}` structs is the single source of truth for both prompt injection and post-analysis tag scanning. Adding a tag is a one-line Go change with no prompt template edits.

Starting vocabulary: `premature_conclusion`, `missed_available_tool`, `unsupported_confidence`, `incomplete_evidence`, `hallucinated_evidence`, `wrong_conclusion`.

**Rationale:** Dynamic injection means the prompt updates automatically when the vocabulary changes. Each term is exclusively negative, making false-positive matches negligible. Starting small avoids overlap; vocabulary grows based on observed patterns. Rejected: hardcoded in prompt (requires prompt edits when adding tags), ~12 terms (premature, some overlap).

### D3: Two clearly separated sections in Turn 2

Turn 2 has two explicit sections: Part 1 (Missing Tools) and Part 2 (Existing Tool Improvements). The improvement section guides the judge to assess argument clarity, response format, tool description, and missing discoverability.

**Rationale:** Explicit separation ensures both categories get proper attention. Structured criteria guide the LLM to assess specific observable aspects of tool interactions. Rejected: single unified section (improvements may get less attention when mixed with missing tools).

### D4: Nillable failure_tags column (NULL for pre-redesign scores)

`failure_tags` is `Optional().Nillable()` — NULL means "pre-redesign, not scanned", empty array means "scanned, no failures found".

**Rationale:** Cleanly distinguishes pre-redesign scores from clean scans without backfilling old rows. Rejected: Optional only with empty array default (can't distinguish pre-redesign from clean scores).

## Architecture

### What changes where

```
pkg/agent/prompt/judges.go          ← Rewrite all 4 prompt constants
pkg/agent/prompt/vocabulary.go      ← NEW: FailureTag type, FailureVocabulary slice (single source of truth)
pkg/agent/prompt/builder.go         ← BuildScoringInitialPrompt injects vocabulary dynamically
pkg/agent/controller/scoring.go     ← Add scanFailureTags() (imports vocabulary from prompt/)
                                       Update ScoringResult struct (add FailureTags, rename field)
ent/schema/sessionscore.go          ← Add failure_tags, rename missing_tools_analysis → tool_improvement_report
pkg/queue/scoring_executor.go       ← Pass failure tags in completeScore
pkg/models/scoring.go               ← Add FailureTags, rename field to ToolImprovementReport
pkg/api/handler_scoring.go          ← Map failure tags + renamed field in response
web/dashboard/src/types/api.ts      ← Rename field to tool_improvement_report
web/dashboard/src/pages/ScoringPage.tsx ← Update field reference
test/e2e/scoring_test.go            ← Update scripted responses + assertions
test/e2e/testdata/golden/scoring/   ← Regenerate golden files
```

### Data flow (unchanged except failure tags)

```
ScoringExecutor.executeScoring()
  → buildScoringContext()           (unchanged)
  → ScoringController.Run()
    → Turn 1: score evaluation
      → extractScore(resp.Text)     (unchanged — number on last line)
      → scanFailureTags(analysis)   (NEW — strings.Contains scan)
    → Turn 2: tool improvement report
  → ScoringResult{TotalScore, ScoreAnalysis, ToolImprovementReport, FailureTags}
  → completeScore()                 (adds SetFailureTags)
  → DB: session_scores row
```

## Prompt Design

### System prompt (`judgeSystemPrompt`)

The system prompt shifts from process evaluation ("how well the agents gathered evidence, used available tools, reasoned through the problem") to outcome-first evaluation:

```
You are an expert investigation quality evaluator for TARSy, an automated
incident investigation platform.

TARSy uses agent chains — multi-stage pipelines where AI agents investigate
incidents by calling external tools (MCP tools), analyzing evidence, and
producing findings. Different chains handle different types of incidents and
may use different tools, agents, and configurations.

Your role is to critically evaluate investigation quality. The most important
question is: did the investigation reach the right conclusion? Then: was the
path there efficient and thorough? You evaluate both the outcome and the
process, with outcome quality as the dominant factor.
```

### Turn 1 prompt (`judgePromptScore`)

The new Turn 1 prompt replaces the 4-category framework with structured dimension assessments followed by holistic scoring. This approach is grounded in EvalPlanner (ICML 2025) research showing that LLMs produce more consistent and accurate evaluations when they explicitly analyze along defined dimensions before committing to a score.

The dimensions are deliberately broad — they apply universally to any investigation regardless of alert type. The LLM is never forced into narrow yes/no questions that might be irrelevant; instead, it writes free-form assessments per dimension, naturally using failure vocabulary terms where applicable.

**Step 1 — Dimension Assessments**

The judge evaluates the investigation across five broad dimensions. For each dimension, it writes a 2-4 sentence assessment, with every claim citing specific evidence from the investigation timeline (tool calls, agent responses, or absence of expected actions).

1. **Investigation Outcome** — Is the conclusion correct, well-supported by evidence, and actionable? Were alternative explanations considered? Does the confidence level match the evidence quality?

2. **Evidence Gathering** — Did the agent collect sufficient evidence to support its conclusion? Did it verify claims with direct data, or rely on assumptions? Were relevant data sources left unexplored?

3. **Tool Utilization** — Were available tools used appropriately? Were obvious tools missed? Were tool results interpreted correctly? Did the agent recover from tool failures?

4. **Analytical Reasoning** — Was the reasoning logically sound? Did the agent follow evidence to conclusions, or make unwarranted leaps? Was contradictory evidence addressed?

5. **Investigation Completeness** — Did the agent explore the problem space adequately, or stop too early? Were there wasted loops or irrelevant tangents?

The evidence-anchoring requirement is embedded in the dimension assessment instructions:

```
For each dimension, cite specific evidence from the session data — exact tool
calls, agent responses, or missing actions. Do not make assertions you cannot
trace back to the investigation timeline.

Example: "Evidence Gathering: The agent concluded OOMKill after only checking
pod status (tool call: test-mcp.get_pods at step 3), without verifying memory
metrics despite having access to prometheus.query_range — incomplete_evidence."
```

Any dimension may not be particularly relevant to a given investigation. The judge can note this briefly ("Tool Utilization: Only one tool was relevant here; it was used correctly") and move on.

**Step 2 — Holistic Narrative & Score**

The judge synthesizes the five dimension assessments into an overall narrative and score. The Investigation Outcome dimension determines the score range (outcome-first ceiling mechanic):

| Outcome quality | Score range |
|---|---|
| Correct, well-supported conclusion | 60-100 |
| Partially correct or weakly supported conclusion | 35-59 |
| Wrong or unsupported conclusion | 0-34 |

The remaining four dimensions (Evidence Gathering, Tool Utilization, Analytical Reasoning, Investigation Completeness) determine where the score falls within that range. A flawed conclusion indicates flaws in the process — even if individual steps looked methodical, something went wrong.

**Failure vocabulary section (dynamically injected)**

The prompt includes a reference list of common failure patterns, generated dynamically from the `FailureVocabulary` Go slice at prompt build time. The judge uses these terms when applicable but freely describes any problem it identifies:

```
Common failure patterns to watch for (use these terms when applicable, but
describe any problems you identify even if they don't match these patterns):

- premature_conclusion — reached a diagnosis without gathering sufficient evidence
- missed_available_tool — a relevant tool was available but not used
- unsupported_confidence — stated high confidence without comprehensive evidence
- incomplete_evidence — stopped gathering evidence before covering all relevant dimensions
- hallucinated_evidence — cited or assumed evidence not present in the investigation data
- wrong_conclusion — the final diagnosis is incorrect or contradicted by gathered evidence
```

This section is NOT hardcoded in the prompt template. It is generated from `FailureVocabulary` and injected by `BuildScoringInitialPrompt()`. Adding a tag = adding one entry to the slice.

**Prompt template structure** — the `judgePromptScore` constant uses three format parameters:

```
%[1]s  — session investigation context (alert, runbook, tools, timeline)
%[2]s  — output schema (scoringOutputSchema constant)
%[3]s  — failure vocabulary section (dynamically generated from FailureVocabulary)
```

`BuildScoringInitialPrompt()` builds the vocabulary string and calls `fmt.Sprintf(judgePromptScore, sessionCtx, outputSchema, vocabularySection)`.

**Scoring calibration**

Same bands as before, re-anchored to the outcome-first philosophy:

- 80-100: Correct conclusion, well-supported, efficient process
- 60-79: Correct conclusion with some gaps in evidence or process
- 35-59: Partially correct or weakly supported conclusion
- 0-34: Wrong conclusion, or so little evidence gathered that the conclusion is unsupported

**Output format** — unchanged: narrative analysis followed by total score on the last line.

### Turn 2 prompt (`judgePromptFollowupToolReport`)

Turn 2 has two clearly separated sections:

**Part 1: Missing Tools** — new MCP tools that should be built. Same scope as before, slightly reworded for consistency.

**Part 2: Existing Tool Improvements** — based on observed tool interactions in the investigation. The judge reviews every tool call (arguments, results, how the agent interpreted them) and identifies:

- **Argument clarity** — Did the agent struggle to determine correct arguments? (e.g., tried multiple parameter combinations, guessed values)
- **Response format** — Did the tool return data that was hard for the agent to parse or extract useful information from?
- **Tool description** — Was there a relevant tool the agent didn't use, possibly because its name or description didn't indicate its relevance?
- **Missing discoverability** — Did the tool require argument values the agent had no way to discover from the available context?

For each improvement:
- Tool name (as it appears in the AVAILABLE TOOLS section)
- What to improve (argument names, response format, description, etc.)
- Why (what was observed in the investigation that suggests this improvement)

### Score reminder prompt (`judgePromptScoreReminder`)

Unchanged in structure — still asks for the total score on the last line. Wording updated to reference the new evaluation framework.

## Failure Tag Extraction

### Vocabulary (single source of truth)

Defined in `pkg/agent/prompt/vocabulary.go` — a new file in the prompt package. This location is chosen because both consumers need access:

- `BuildScoringInitialPrompt()` in `prompt/builder.go` — same package, direct access
- `scanFailureTags()` in `controller/scoring.go` — imports from `prompt/` (the controller did not previously import `prompt`, but `prompt` does not import `controller`, so adding `controller → prompt` introduces no cycle)

```go
// pkg/agent/prompt/vocabulary.go

type FailureTag struct {
    Term        string
    Description string
}

var FailureVocabulary = []FailureTag{
    {"premature_conclusion", "reached a diagnosis without gathering sufficient evidence"},
    {"missed_available_tool", "a relevant tool was available but not used"},
    {"unsupported_confidence", "stated high confidence without comprehensive evidence"},
    {"incomplete_evidence", "stopped gathering evidence before covering all relevant dimensions"},
    {"hallucinated_evidence", "cited or assumed evidence not present in the investigation data"},
    {"wrong_conclusion", "the final diagnosis is incorrect or contradicted by gathered evidence"},
}
```

The type and slice are exported (`FailureTag`, `FailureVocabulary`) so `controller/scoring.go` can import them.

**Prompt injection**: `BuildScoringInitialPrompt()` iterates `FailureVocabulary` to generate the vocabulary section dynamically and injects it into the prompt template via a format parameter.

**Tag scanning**: `scanFailureTags()` in `controller/scoring.go` iterates `prompt.FailureVocabulary` for `strings.Contains` matching.

Adding a new tag = add one entry to this slice. The prompt updates automatically and the prompt hash changes (see Prompt Hash section).

### Scanning

After `extractScore()` returns the analysis text, scan it for vocabulary terms:

```go
// In pkg/agent/controller/scoring.go

func scanFailureTags(analysis string) []string {
    tags := make([]string, 0)
    for _, ft := range prompt.FailureVocabulary {
        if strings.Contains(analysis, ft.Term) {
            tags = append(tags, ft.Term)
        }
    }
    return tags
}
```

`tags` is initialized as an empty slice (not nil) so that JSON marshaling produces `[]` instead of `null` — preserving the distinction between "scanned, no matches" (`[]`) and "pre-redesign, not scanned" (`NULL`).

No deduplication needed — `strings.Contains` returns true once per term regardless of how many times it appears. The result is a `[]string` of matched terms in vocabulary order.

### ScoringResult update

```go
type ScoringResult struct {
    TotalScore            int      `json:"total_score"`
    ScoreAnalysis         string   `json:"score_analysis"`
    ToolImprovementReport string   `json:"tool_improvement_report"`
    FailureTags           []string `json:"failure_tags"`
}
```

The `FailureTags` field is populated by `scanFailureTags()` in `ScoringController.Run()` right after score extraction succeeds, before building the result.

## Schema Changes

### `ent/schema/sessionscore.go`

**Rename** `missing_tools_analysis` → `tool_improvement_report` (field and DB column). Turn 2 now covers both missing tools and existing tool improvements, making the old name misleading.

**Add** one new field:

```go
field.JSON("failure_tags", []string{}).
    Optional().
    Nillable().
    Comment("Failure vocabulary terms found in score_analysis, NULL for pre-redesign scores"),
```

- **NULL** = pre-redesign score, not scanned
- **`[]`** (empty array) = scanned, no failures matched
- **`["tag1", "tag2"]`** = scanned, these failures matched

No new indexes needed — the JSONB column supports `@>` containment queries natively. A GIN index can be added later if aggregation query performance requires it.

### Migration

A single `make migrate-create` + review. The migration contains:

1. `ALTER TABLE session_scores RENAME COLUMN missing_tools_analysis TO tool_improvement_report` — rename existing column
2. `ALTER TABLE session_scores ADD COLUMN failure_tags jsonb` — add new nullable column (existing rows get NULL, no backfill needed)

## Plumbing Changes

### `pkg/agent/prompt/builder.go`

`BuildScoringInitialPrompt()` gains vocabulary injection. The function signature stays the same (two string parameters), but internally it builds the vocabulary section from `FailureVocabulary` before formatting:

```go
func (b *PromptBuilder) BuildScoringInitialPrompt(sessionInvestigationContext, outputSchema string) string {
    var vocabSection strings.Builder
    for _, ft := range FailureVocabulary {
        fmt.Fprintf(&vocabSection, "- %s — %s\n", ft.Term, ft.Description)
    }
    return fmt.Sprintf(judgePromptScore, sessionInvestigationContext, outputSchema, vocabSection.String())
}
```

The `PromptBuilder` interface in `pkg/agent/context.go` is unchanged — the vocabulary injection is an internal implementation detail.

### `pkg/agent/controller/scoring.go`

In `Run()`, after successful score extraction and before building the result:

```go
failureTags := scanFailureTags(analysis)

result := ScoringResult{
    TotalScore:            score,
    ScoreAnalysis:         analysis,
    ToolImprovementReport: toolImprovementResp.Text,
    FailureTags:           failureTags,
}
```

### `pkg/queue/scoring_executor.go`

In `completeScore()`, add `SetFailureTags`:

```go
func (e *ScoringExecutor) completeScore(scoreID, finalAnalysisJSON, promptHash string) error {
    var result controller.ScoringResult
    if err := json.Unmarshal([]byte(finalAnalysisJSON), &result); err != nil {
        return fmt.Errorf("failed to parse scoring result: %w", err)
    }

    now := time.Now()
    return e.dbClient.SessionScore.UpdateOneID(scoreID).
        SetTotalScore(result.TotalScore).
        SetScoreAnalysis(result.ScoreAnalysis).
        SetToolImprovementReport(result.ToolImprovementReport).
        SetFailureTags(result.FailureTags).
        SetPromptHash(promptHash).
        SetStatus(sessionscore.StatusCompleted).
        SetCompletedAt(now).
        Exec(context.Background())
}
```

No nil check needed — `scanFailureTags` always returns a non-nil slice (`make([]string, 0)`), so `FailureTags` is always safe to pass to `SetFailureTags`. The DB gets `[]` (empty JSON array) when no tags match, preserving the distinction from `NULL` (pre-redesign scores).

### `pkg/models/scoring.go`

Add `FailureTags`, rename `MissingToolsAnalysis` → `ToolImprovementReport`:

```go
type SessionScoreResponse struct {
    ScoreID               string     `json:"score_id"`
    TotalScore            *int       `json:"total_score"`
    ScoreAnalysis         *string    `json:"score_analysis"`
    ToolImprovementReport *string    `json:"tool_improvement_report"`
    FailureTags           []string   `json:"failure_tags,omitempty"`
    PromptHash            *string    `json:"prompt_hash"`
    ScoreTriggeredBy      string     `json:"score_triggered_by"`
    Status                string     `json:"status"`
    StageID               *string    `json:"stage_id"`
    StartedAt             time.Time  `json:"started_at"`
    CompletedAt           *time.Time `json:"completed_at"`
    ErrorMessage          *string    `json:"error_message"`
}
```

### `pkg/api/handler_scoring.go`

Map `FailureTags` and renamed field in `getScoreHandler`. The ent `Nillable()` JSON field generates `*[]string`, which requires nil-safe dereferencing to the response struct's `[]string`:

```go
var failureTags []string
if score.FailureTags != nil {
    failureTags = *score.FailureTags
}

return c.JSON(http.StatusOK, &models.SessionScoreResponse{
    // ... existing fields ...
    ToolImprovementReport: score.ToolImprovementReport,
    FailureTags:           failureTags,
})
```

## Prompt Hash

The `combinedPromptsHash` in `judges.go` hashes all prompt constants together. Since the vocabulary is now injected dynamically (not part of the static prompt constants), the hash computation includes the vocabulary — otherwise adding or removing a vocabulary term would change the rendered prompt but leave the hash unchanged, silently preventing automatic re-scoring detection.

The `init()` function in `judges.go` includes the formatted vocabulary string:

```go
func init() {
    vocabStr := FormatVocabularyForHash(FailureVocabulary)
    combinedPromptsHash = sha256.Sum256([]byte(
        judgeSystemPrompt + judgePromptScore + judgePromptScoreReminder +
        judgePromptFollowupToolReport + vocabStr,
    ))
}
```

`FormatVocabularyForHash` produces a deterministic string from the vocabulary slice (concatenating all terms and descriptions). Any change to `FailureVocabulary` — adding, removing, or editing a term — changes the hash.

## Implementation

Delivered in two PRs, each independently deployable and green on CI.

### PR 1: Prompt rewrite + vocabulary infrastructure

Purely prompt-side changes. No schema changes, no plumbing, no renames. The existing extraction/storage pipeline handles the new prompt output unchanged — `extractScore()` still finds the number on the last line, `score_analysis` stores the new dimension-based narrative, `missing_tools_analysis` stores the broader tool report (naming is stale but functional). The prompt hash changes, so old and new scores are distinguishable.

1. Create `pkg/agent/prompt/vocabulary.go` with `FailureTag` type and `FailureVocabulary` slice
2. Rewrite all 4 prompt constants in `judges.go` (dimensions, ceiling mechanic, `%[3]s` vocabulary placeholder, expanded Turn 2)
3. Update `init()` in `judges.go` to include vocabulary in hash computation
4. Update `BuildScoringInitialPrompt()` in `builder.go` to inject vocabulary dynamically
5. Regenerate prompt golden files (`prompt_turn1.golden`, `prompt_turn2.golden`)

### PR 2: Schema + failure tags + rename + plumbing

All tightly coupled changes that must land together: the column rename, new column, extraction logic, API contract change, frontend, and all test updates.

**Schema + migration:**
1. Rename `missing_tools_analysis` → `tool_improvement_report` in `ent/schema/sessionscore.go`
2. Add `failure_tags` field to `ent/schema/sessionscore.go`
3. `make migrate-create` + review migration (should contain RENAME COLUMN + ADD COLUMN)
4. `make generate` to regenerate ent code

**Controller + plumbing:**
5. Add `scanFailureTags()` in `controller/scoring.go` (imports `prompt.FailureVocabulary`)
6. Update `ScoringResult` struct: add `FailureTags`, rename `MissingToolsAnalysis` → `ToolImprovementReport`
7. Wire tag scanning into `Run()`, update variable names for Turn 2
8. Update `completeScore()` in `scoring_executor.go` (renamed field + failure tags)

**API + frontend:**
9. Update `SessionScoreResponse` in `models/scoring.go` (renamed field + failure tags)
10. Update `getScoreHandler` in `handler_scoring.go` (renamed field + nil-safe `*[]string` → `[]string`)
11. Update frontend: `web/dashboard/src/types/api.ts` and `web/dashboard/src/pages/ScoringPage.tsx`
12. Update dashboard score badge colors to reflect the new outcome-first score ranges

**Tests:**
13. Unit tests for `scanFailureTags()`
14. Update `scriptScoringSuccess()` and assertions in `scoring_test.go`
15. Update unit tests in `scoring_test.go` for renamed field
16. Regenerate golden files
17. Run full e2e test suite
