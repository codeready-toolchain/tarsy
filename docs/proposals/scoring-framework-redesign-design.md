# Scoring Framework Redesign — Detailed Design

**Status:** Final

**Related:** [Sketch](scoring-framework-redesign-sketch.md) | [Design questions](scoring-framework-redesign-design-questions.md) | [ADR-0008: Session Scoring](../adr/0008-session-scoring.md)

## Overview

This design implements the decisions from the [sketch](scoring-framework-redesign-sketch.md): rewrite the judge evaluation prompts to be outcome-first, add a failure vocabulary with server-side tag extraction, and expand Turn 2 to cover existing tool improvements alongside missing tools.

The scoring infrastructure (ScoringExecutor, ScoringController, 2-turn flow, auto-trigger, re-score API, dashboard) is unchanged. The changes are:

1. **New prompts** in `pkg/agent/prompt/judges.go`
2. **Failure tag extraction** in `pkg/agent/controller/scoring.go`
3. **One new DB column** (`failure_tags`) on `session_scores`
4. **Plumbing** to pass tags through ScoringResult → completeScore → DB

## Design Principles

1. **Outcome > Process** — a wrong conclusion can never produce a high score, regardless of process quality. A flawed conclusion indicates a flawed process.
2. **Structured analysis, holistic judgment** — the judge evaluates five broad dimensions before scoring, grounding its thinking. The score itself remains holistic — dimensions interact in ways that fixed-weight formulas cannot capture. Based on EvalPlanner (ICML 2025) showing structured-then-holistic outperforms both pure decomposition and pure holistic evaluation.
3. **Don't constrain the judge** — dimensions are broad and universally applicable; the failure vocabulary is guidance, not a hard requirement; evaluation quality always takes priority over structural compliance
4. **Evidence-anchored** — every claim in a dimension assessment must cite specific timeline events, making evaluations verifiable and actionable
5. **Single source of truth** — the failure vocabulary Go slice drives both prompt generation and tag scanning
6. **Minimal infrastructure changes** — same single score, same extraction method, same 2-turn flow
7. **Backward compatible** — the `failure_tags` column is nullable; old scores with NULL tags work fine

## Architecture

### What changes where

```
pkg/agent/prompt/judges.go          ← Rewrite all 4 prompt constants + dynamic vocabulary injection
pkg/agent/controller/scoring.go     ← Add failureTag type, failureVocabulary slice, scanFailureTags()
                                       Update ScoringResult struct
ent/schema/sessionscore.go          ← Add failure_tags JSON field (Optional, Nillable)
pkg/queue/scoring_executor.go       ← Pass failure tags in completeScore
pkg/models/scoring.go               ← Add FailureTags to API response
pkg/api/handler_scoring.go          ← Map failure tags in response
pkg/agent/prompt/builder.go         ← BuildScoringInitialPrompt injects vocabulary dynamically
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
  → ScoringResult{TotalScore, ScoreAnalysis, MissingToolsAnalysis, FailureTags}
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

**Part 1 — Dimension Assessments**

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

**Part 2 — Holistic Narrative & Score**

The judge synthesizes the five dimension assessments into an overall narrative and score. The Investigation Outcome dimension determines the score range (outcome-first ceiling mechanic):

| Outcome quality | Score range |
|---|---|
| Correct, well-supported conclusion | 60-100 |
| Partially correct or weakly supported conclusion | 35-59 |
| Wrong or unsupported conclusion | 0-34 |

The remaining four dimensions (Evidence Gathering, Tool Utilization, Analytical Reasoning, Investigation Completeness) determine where the score falls within that range. A flawed conclusion indicates flaws in the process — even if individual steps looked methodical, something went wrong.

**Failure vocabulary section (dynamically injected)**

The prompt includes a reference list of common failure patterns, generated dynamically from the `failureVocabulary` Go slice at prompt build time. The judge uses these terms when applicable but freely describes any problem it identifies:

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

This section is NOT hardcoded in the prompt template. It is generated from `failureVocabulary` and injected by `BuildScoringInitialPrompt()`. Adding a tag = adding one entry to the slice.

**Scoring calibration**

Same bands as today, re-anchored to the outcome-first philosophy:

- 80-100: Correct conclusion, well-supported, efficient process
- 60-79: Correct conclusion with some gaps in evidence or process
- 35-59: Partially correct or weakly supported conclusion
- 0-34: Wrong conclusion, or so little evidence gathered that the conclusion is unsupported

**Output format** — unchanged: narrative analysis followed by total score on the last line.

### Turn 2 prompt (`judgePromptFollowupMissingTools`)

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

A Go slice of `failureTag` structs, each containing a term and description. This slice drives both prompt generation and post-analysis scanning:

```go
type failureTag struct {
    Term        string
    Description string
}

var failureVocabulary = []failureTag{
    {"premature_conclusion", "reached a diagnosis without gathering sufficient evidence"},
    {"missed_available_tool", "a relevant tool was available but not used"},
    {"unsupported_confidence", "stated high confidence without comprehensive evidence"},
    {"incomplete_evidence", "stopped gathering evidence before covering all relevant dimensions"},
    {"hallucinated_evidence", "cited or assumed evidence not present in the investigation data"},
    {"wrong_conclusion", "the final diagnosis is incorrect or contradicted by gathered evidence"},
}
```

**Prompt injection**: `BuildScoringInitialPrompt()` iterates `failureVocabulary` to generate the vocabulary section dynamically and injects it into the prompt template via a format parameter.

**Tag scanning**: `scanFailureTags()` iterates `failureVocabulary` for `strings.Contains` matching.

Adding a new tag = add one entry to this slice. The prompt updates automatically (and the prompt hash changes since the rendered content changes).

### Scanning

After `extractScore()` returns the analysis text, scan it for vocabulary terms:

```go
func scanFailureTags(analysis string) []string {
    var tags []string
    for _, ft := range failureVocabulary {
        if strings.Contains(analysis, ft.Term) {
            tags = append(tags, ft.Term)
        }
    }
    return tags
}
```

No deduplication needed — `strings.Contains` returns true once per term regardless of how many times it appears. The result is a `[]string` of matched terms in vocabulary order.

### ScoringResult update

```go
type ScoringResult struct {
    TotalScore           int      `json:"total_score"`
    ScoreAnalysis        string   `json:"score_analysis"`
    MissingToolsAnalysis string   `json:"missing_tools_analysis"`
    FailureTags          []string `json:"failure_tags"`
}
```

The `FailureTags` field is populated by `scanFailureTags()` in `ScoringController.Run()` right after score extraction succeeds, before building the result.

## Schema Change

### `ent/schema/sessionscore.go`

Add one field:

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

A standard `make migrate-create` + review. The column is nullable with no default, so existing rows get NULL. No backfill needed.

## Plumbing Changes

### `pkg/agent/prompt/builder.go`

`BuildScoringInitialPrompt()` gains vocabulary injection. The prompt template uses a format parameter for the vocabulary section. The builder iterates `failureVocabulary` to generate:

```
- premature_conclusion — reached a diagnosis without gathering sufficient evidence
- missed_available_tool — a relevant tool was available but not used
...
```

And injects this into the prompt template alongside the session context and output schema.

### `pkg/agent/controller/scoring.go`

In `Run()`, after successful score extraction and before building the result:

```go
failureTags := scanFailureTags(analysis)

result := ScoringResult{
    TotalScore:           score,
    ScoreAnalysis:        analysis,
    MissingToolsAnalysis: missingToolsResp.Text,
    FailureTags:          failureTags,
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
    update := e.dbClient.SessionScore.UpdateOneID(scoreID).
        SetTotalScore(result.TotalScore).
        SetScoreAnalysis(result.ScoreAnalysis).
        SetMissingToolsAnalysis(result.MissingToolsAnalysis).
        SetPromptHash(promptHash).
        SetStatus(sessionscore.StatusCompleted).
        SetCompletedAt(now)

    if result.FailureTags != nil {
        update = update.SetFailureTags(result.FailureTags)
    }

    return update.Exec(context.Background())
}
```

### `pkg/models/scoring.go`

Add `FailureTags` to the API response:

```go
type SessionScoreResponse struct {
    ScoreID              string     `json:"score_id"`
    TotalScore           *int       `json:"total_score"`
    ScoreAnalysis        *string    `json:"score_analysis"`
    MissingToolsAnalysis *string    `json:"missing_tools_analysis"`
    FailureTags          []string   `json:"failure_tags,omitempty"`
    PromptHash           *string    `json:"prompt_hash"`
    ScoreTriggeredBy     string     `json:"score_triggered_by"`
    Status               string     `json:"status"`
    StageID              *string    `json:"stage_id"`
    StartedAt            time.Time  `json:"started_at"`
    CompletedAt          *time.Time `json:"completed_at"`
    ErrorMessage         *string    `json:"error_message"`
}
```

### `pkg/api/handler_scoring.go`

Map `FailureTags` in `getScoreHandler`:

```go
return c.JSON(http.StatusOK, &models.SessionScoreResponse{
    // ... existing fields ...
    FailureTags: score.FailureTags,
})
```

## Prompt Hash

The `combinedPromptsHash` in `judges.go` currently hashes all prompt constants together. Since the vocabulary is now injected dynamically (not part of the static prompt constants), the hash computation needs to include the vocabulary.

Two approaches:
1. Include the formatted vocabulary string in the hash computation
2. Keep hashing the prompt templates as-is — the template itself changes (new format parameter), so the hash changes

Either way, the hash changes when prompts change, which is the desired behavior. The simplest approach: keep hashing the prompt templates (which now contain the vocabulary placeholder) and note that vocabulary changes also change the rendered prompt (and should trigger re-scoring if desired).

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

### Phase 1: Prompt rewrite + tag extraction

1. Define `failureTag` type and `failureVocabulary` slice in `scoring.go`
2. Write new prompt constants in `judges.go` (with vocabulary placeholder)
3. Update `BuildScoringInitialPrompt()` in `builder.go` to inject vocabulary dynamically
4. Add `scanFailureTags()` in `scoring.go`
5. Update `ScoringResult` struct with `FailureTags`
6. Wire tag scanning into `Run()`
7. Unit tests for `scanFailureTags()`

### Phase 2: Schema + plumbing

1. Add `failure_tags` field to `ent/schema/sessionscore.go`
2. `make migrate-create` + review migration
3. `make generate` to regenerate ent code
4. Update `completeScore()` in `scoring_executor.go`
5. Update `SessionScoreResponse` in `models/scoring.go`
6. Update `getScoreHandler` in `handler_scoring.go`

### Phase 3: Tests

1. Update `scriptScoringSuccess()` and assertions in `scoring_test.go`
2. Regenerate golden files
3. Run full e2e test suite
