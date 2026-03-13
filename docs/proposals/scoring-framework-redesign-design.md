# Scoring Framework Redesign — Detailed Design

**Status:** Final

**Related:** [Sketch](scoring-framework-redesign-sketch.md) | [Design questions](scoring-framework-redesign-design-questions.md) | [ADR-0008: Session Scoring](../adr/0008-session-scoring.md)

## Overview

This design implements the decisions from the [sketch](scoring-framework-redesign-sketch.md): rewrite the judge evaluation prompts to be outcome-first, add a failure vocabulary with server-side tag extraction, and expand Turn 2 to cover existing tool improvements alongside missing tools.

The scoring infrastructure (ScoringExecutor, ScoringController, 2-turn flow, auto-trigger, re-score API, dashboard) is unchanged. The changes are:

1. **New prompts** in `pkg/agent/prompt/judges.go`
2. **Failure vocabulary + tag extraction** in `pkg/agent/prompt/vocabulary.go` (new) and `pkg/agent/controller/scoring.go`
3. **Schema changes** on `session_scores`: rename `missing_tools_analysis` → `tool_improvement_report`, add `failure_tags` column
4. **Plumbing** to pass tags through ScoringResult → completeScore → DB

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

The current system prompt focuses on process evaluation ("how well the agents gathered evidence, used available tools, reasoned through the problem"). The new version shifts to outcome-first evaluation.

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

Same bands as today, re-anchored to the outcome-first philosophy:

- 80-100: Correct conclusion, well-supported, efficient process
- 60-79: Correct conclusion with some gaps in evidence or process
- 35-59: Partially correct or weakly supported conclusion
- 0-34: Wrong conclusion, or so little evidence gathered that the conclusion is unsupported

**Output format** — unchanged: narrative analysis followed by total score on the last line.

### Turn 2 prompt (`judgePromptFollowupToolReport`)

The existing Turn 2 asks only about missing tools. The new version has two clearly separated sections:

**Part 1: Missing Tools** — new MCP tools that should be built. Same scope as today, slightly reworded for consistency.

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
- `scanFailureTags()` in `controller/scoring.go` — imports from `prompt/` (the controller currently does not import `prompt`, but `prompt` does not import `controller`, so adding `controller → prompt` introduces no cycle)

```go
// pkg/agent/prompt/vocabulary.go

// FailureTag defines a failure pattern term and its description for the scoring vocabulary.
type FailureTag struct {
    Term        string
    Description string
}

// FailureVocabulary is the single source of truth for failure pattern terms.
// Used by BuildScoringInitialPrompt() for prompt injection and by
// controller.scanFailureTags() for post-analysis tag extraction.
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

A single `make migrate-create` + review. The migration will contain:

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

The `combinedPromptsHash` in `judges.go` currently hashes all prompt constants together. Since the vocabulary is now injected dynamically (not part of the static prompt constants), the hash computation must include the vocabulary — otherwise adding or removing a vocabulary term changes the rendered prompt but leaves the hash unchanged, silently preventing automatic re-scoring detection.

The `init()` function in `judges.go` is updated to include the formatted vocabulary string:

```go
func init() {
    vocabStr := FormatVocabularyForHash(FailureVocabulary)
    combinedPromptsHash = sha256.Sum256([]byte(
        judgeSystemPrompt + judgePromptScore + judgePromptScoreReminder +
        judgePromptFollowupToolReport + vocabStr,
    ))
}
```

`FormatVocabularyForHash` produces a deterministic string from the vocabulary slice (e.g., concatenating all terms and descriptions). This ensures that any change to `FailureVocabulary` — adding, removing, or editing a term — changes the hash.

## Testing

### E2E test updates

The scripted LLM response in `scriptScoringSuccess()` needs updating to match the new prompt format. The response should include failure vocabulary terms so the tag scanning can be tested:

```go
func scriptScoringSuccess(llm *ScriptedLLMClient) {
    llm.AddSequential(LLMScriptEntry{
        Chunks: []agent.Chunk{
            &agent.TextChunk{Content: "## Dimension Assessments\n\n" +
                "**Investigation Outcome:** The conclusion is correct — pod-1 is " +
                "OOMKilled, matching the evidence from get_pods (step 2). " +
                "Confidence is appropriate given the evidence gathered.\n\n" +
                "**Evidence Gathering:** The agent showed incomplete_evidence — " +
                "after confirming pod status via test-mcp.get_pods (step 2), it " +
                "did not check memory metrics or resource limits despite having " +
                "access to prometheus.query_range and get_resource_limits.\n\n" +
                "**Tool Utilization:** missed_available_tool — get_resource_limits " +
                "was available but never called. The agent relied solely on pod " +
                "status from test-mcp.get_pods.\n\n" +
                "**Analytical Reasoning:** The reasoning from pod status to " +
                "OOMKill was sound, but the agent did not consider alternative " +
                "explanations for the restart.\n\n" +
                "**Investigation Completeness:** The agent stopped after a single " +
                "tool call (step 2), leaving memory and resource dimensions " +
                "unexplored.\n\n" +
                "## Overall Assessment\n\n" +
                "Correct conclusion with significant gaps in evidence gathering " +
                "and tool utilization. The outcome places this in the 60-100 " +
                "range, but process weaknesses pull it toward the lower end.\n\n70"},
            &agent.UsageChunk{InputTokens: 500, OutputTokens: 100, TotalTokens: 600},
        },
    })
    // Turn 2: tool improvement report
    llm.AddSequential(LLMScriptEntry{
        Chunks: []agent.Chunk{
            &agent.TextChunk{Content: "## Missing Tools\n\n" +
                "1. **get_memory_metrics** — Fetch Prometheus memory usage time series.\n\n" +
                "## Existing Tool Improvements\n\n" +
                "1. **test-mcp.get_pods** — Response format: include resource limits in pod listing."},
            &agent.UsageChunk{InputTokens: 600, OutputTokens: 80, TotalTokens: 680},
        },
    })
}
```

Assertions updated to verify:
- `score.FailureTags` contains `["missed_available_tool", "incomplete_evidence"]` (in vocabulary order)
- Score analysis contains dimension headings
- API response includes `failure_tags`
- Golden files regenerated

### Unit tests

Add unit tests for `scanFailureTags()`:

- Empty analysis → empty slice
- Analysis with no vocabulary terms → empty slice
- Analysis with one vocabulary term → `["term"]`
- Analysis with multiple vocabulary terms → all matched, in vocabulary order
- Analysis with partial match (e.g., "conclusion" but not "premature_conclusion") → not matched

Add unit tests for the updated `extractScore()` (unchanged logic, but verify it still works with new prompt format).

## Implementation Plan

Two PRs, each independently deployable and green on CI.

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
12. Update dashboard score badge colors to reflect the new outcome-first score ranges. The `getScoreColor()` thresholds in `web/dashboard/src/components/common/ScoreBadge.tsx` and `getScoreColorHex()` in `web/dashboard/src/pages/ScoringPage.tsx` currently use green >= 80 / yellow >= 60 / red < 60. Verify these align with the new calibration bands (80-100 excellent, 60-79 good with gaps, 35-59 partial, 0-34 wrong) and adjust if needed.

**Tests:**
12. Unit tests for `scanFailureTags()`
13. Update `scriptScoringSuccess()` and assertions in `scoring_test.go`
14. Update unit tests in `scoring_test.go` for renamed field
15. Regenerate golden files
16. Run full e2e test suite
