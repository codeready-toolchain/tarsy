# ADR-0011: Scoring Framework Redesign

**Status:** Implemented
**Date:** 2026-03-12
**Related:** [ADR-0008: Session Scoring](0008-session-scoring.md)

## Overview

This ADR documents the redesign of the judge evaluation prompts to be outcome-first, the addition of a failure vocabulary with server-side tag extraction, and the expansion of Turn 2 to cover existing tool improvements alongside missing tools.

The scoring infrastructure (ScoringExecutor, ScoringController, 2-turn flow, auto-trigger, re-score API, dashboard) is unchanged. The changes are:

1. **New prompts** — judge prompts rewritten for outcome-first evaluation, structured dimensions, and expanded Turn 2.
2. **Failure vocabulary + tag extraction** — a shared vocabulary drives both what the judge is told to watch for and deterministic scanning of the analysis text for structured tags.
3. **Schema changes** on session scores — rename the Turn 2 analysis column to reflect both missing and improved tools; add a nullable JSON column for extracted failure tags.
4. **End-to-end wiring** — tags and the renamed field flow from the controller result through persistence and API responses.

## Problem

TARSy's session scoring exists to drive a continuous improvement loop: identify what to change in prompts, tools, chain config, and LLM selection to improve the quality of alert investigations. The scoring infrastructure ([ADR-0008](0008-session-scoring.md)) is solid — ScoringExecutor, ScoringController, session_scores table, dashboard UI all work well.

The problem was with **what the judge evaluates and how it reports findings**:

1. **Outcome and process were conflated, with process over-weighted.** The four legacy categories were primarily process-focused. The prompt emphasized process over outcome. An investigation that reaches the wrong conclusion via methodical steps could outscore one that nails the root cause through a messy path.

2. **No structured failure signals for aggregation.** The analysis was a narrative. Finding systemic patterns across many sessions required reading every report manually.

3. **Turn 2 (missing tools) only identified new tools, not improvements to existing ones.** The judge sees every tool call with arguments and results, but was only asked about tools that don't exist — never about tools that exist but are hard for agents to use.

## Design Principles

1. **Outcome > Process** — a wrong conclusion can never produce a high score, regardless of process quality. A flawed conclusion indicates a flawed process.
2. **Structured analysis, holistic judgment** — the judge evaluates five broad dimensions before scoring, grounding its thinking. The score itself remains holistic — dimensions interact in ways that fixed-weight formulas cannot capture. Based on EvalPlanner (ICML 2025) showing structured-then-holistic outperforms both pure decomposition and pure holistic evaluation.
3. **Don't constrain the judge** — dimensions are broad and universally applicable; the failure vocabulary is guidance, not a hard requirement; evaluation quality always takes priority over structural compliance.
4. **Evidence-anchored** — every claim in a dimension assessment must cite specific timeline events, making evaluations verifiable and actionable.
5. **Single source of truth** — one in-code vocabulary list drives both prompt generation and tag scanning.
6. **Minimal infrastructure changes** — same single score, same extraction method, same 2-turn flow.
7. **Backward compatible** — the failure-tags field is nullable; old scores with NULL tags work fine.

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

**Rationale:** The score directly answers "was this investigation good?" with outcome as the dominant factor. Two-phase evaluation structures the judge's thinking without requiring structured sub-score extraction. Zero infrastructure changes — the change is purely in the prompt. Rejected: five sub-scores (extraction complexity), two stored scores (schema/API/UI changes), overlapping ranges (unnecessary given the correlation).

### D2: Start small with ~6 failure tags, dynamic injection from vocabulary list

A single list of `{term, description}` entries is the source of truth for both prompt injection and post-analysis tag scanning. Adding a tag is a one-line change with no separate prompt template edits.

Starting vocabulary: `premature_conclusion`, `missed_available_tool`, `unsupported_confidence`, `incomplete_evidence`, `hallucinated_evidence`, `wrong_conclusion`.

**Rationale:** Dynamic injection means the prompt updates automatically when the vocabulary changes. Each term is exclusively negative, making false-positive matches negligible. Starting small avoids overlap; vocabulary grows based on observed patterns. Rejected: hardcoded in prompt (requires prompt edits when adding tags), ~12 terms (premature, some overlap).

### D3: Two clearly separated sections in Turn 2

Turn 2 has two explicit sections: Part 1 (Missing Tools) and Part 2 (Existing Tool Improvements). The improvement section guides the judge to assess argument clarity, response format, tool description, and missing discoverability.

**Rationale:** Explicit separation ensures both categories get proper attention. Structured criteria guide the LLM to assess specific observable aspects of tool interactions. Rejected: single unified section (improvements may get less attention when mixed with missing tools).

### D4: Nillable failure_tags column (NULL for pre-redesign scores)

`failure_tags` is optional and nillable — NULL means "pre-redesign, not scanned", empty array means "scanned, no failures found".

**Rationale:** Cleanly distinguishes pre-redesign scores from clean scans without backfilling old rows. Rejected: optional only with empty array default (can't distinguish pre-redesign from clean scores).

## Architecture

**What changes conceptually:** Judge prompts are rewritten and augmented with a dynamically built failure-vocabulary section. After Turn 1, the server scans the judge's analysis text for vocabulary terms and attaches matched tags to the scoring result. The session score row stores both the renamed Turn 2 report field and the optional tag list. API and dashboard consumers expose the renamed field and tags alongside the existing score and analysis.

The overall pipeline is unchanged except for tag extraction and persistence: build scoring context, run the two-turn controller, extract the numeric score from the last line of Turn 1 output, scan the analysis for tags, persist the full result including Turn 2 text and tags.

### Data flow (unchanged except failure tags)

```
ScoringExecutor.executeScoring()
  → buildScoringContext()           (unchanged)
  → ScoringController.Run()
    → Turn 1: score evaluation
      → extractScore(resp.Text)     (unchanged — number on last line)
      → scanFailureTags(analysis)   (NEW — substring match against vocabulary)
    → Turn 2: tool improvement report
  → ScoringResult{TotalScore, ScoreAnalysis, ToolImprovementReport, FailureTags}
  → completeScore()                 (persists tags + renamed Turn 2 field)
  → DB: session_scores row
```

## Prompt Design

### System prompt

The system shifts from process evaluation ("how well the agents gathered evidence, used available tools, reasoned through the problem") to outcome-first evaluation: the evaluator's role is to judge investigation quality with **did the investigation reach the right conclusion?** first, then whether the path was efficient and thorough. Outcome quality is the dominant factor.

### Turn 1

Turn 1 replaces the four-category framework with structured dimension assessments followed by holistic scoring. This approach is grounded in EvalPlanner (ICML 2025) research showing that LLMs produce more consistent and accurate evaluations when they explicitly analyze along defined dimensions before committing to a score.

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

**Failure vocabulary (dynamically injected)**

The prompt includes a reference list of common failure patterns, built from the same vocabulary list used for scanning. The judge uses these terms when applicable but freely describes any problem it identifies:

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

**Scoring calibration**

Same bands as before, re-anchored to the outcome-first philosophy:

- 80-100: Correct conclusion, well-supported, efficient process
- 60-79: Correct conclusion with some gaps in evidence or process
- 35-59: Partially correct or weakly supported conclusion
- 0-34: Wrong conclusion, or so little evidence gathered that the conclusion is unsupported

**Output format** — unchanged: narrative analysis followed by total score on the last line.

### Turn 2

Turn 2 has two clearly separated sections:

**Part 1: Missing Tools** — new MCP tools that should be built. Same scope as before, slightly reworded for consistency.

**Part 2: Existing Tool Improvements** — based on observed tool interactions in the investigation. The judge reviews every tool call (arguments, results, how the agent interpreted them) and identifies:

- **Argument clarity** — Did the agent struggle to determine correct arguments? (e.g., tried multiple parameter combinations, guessed values)
- **Response format** — Did the tool return data that was hard for the agent to parse or extract useful information from?
- **Tool description** — Was there a relevant tool the agent didn't use, possibly because its name or description didn't indicate its relevance?
- **Missing discoverability** — Did the tool require argument values the agent had no way to discover from the available context?

For each improvement: tool name (as in the available-tools section), what to improve, and why (what was observed).

### Score reminder prompt

Unchanged in structure — still asks for the total score on the last line. Wording updated to reference the new evaluation framework.

## Failure Tag Extraction

### Vocabulary (single source of truth)

The failure vocabulary lives alongside other judge prompt material so the prompt builder can inject it and the scoring controller can import the same list without circular dependencies. Both prompt injection and tag scanning iterate the same ordered list of terms and descriptions.

Adding a new tag is a single change to that list; the rendered prompt and scanning behavior stay aligned.

### Scanning

After the numeric score is extracted from Turn 1 output, the analysis text is scanned for each vocabulary term (substring match). Matched terms are collected in vocabulary order; no deduplication beyond one hit per term is needed.

The result uses an empty slice (not absent/null) when scanning ran but found no matches, so serialized results distinguish "scanned, clean" from "never scanned" (NULL in the database for legacy rows).

## Schema Changes

**Rename** the Turn 2 analysis column from a "missing tools only" name to **tool improvement report** — Turn 2 now covers both missing tools and existing-tool improvements.

**Add** `failure_tags` as optional, nillable JSON (array of strings):

- **NULL** — pre-redesign score, not scanned
- **`[]`** — scanned, no failures matched
- **`["tag1", "tag2"]`** — scanned, these failures matched

No new indexes are required initially; JSONB supports containment queries if aggregation needs grow later.

### Migration (high level)

A single migration: rename the existing Turn 2 column, then add the new nullable `failure_tags` column (existing rows remain NULL).
