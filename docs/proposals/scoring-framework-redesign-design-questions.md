# Scoring Framework Redesign — Design Questions

**Status:** All decisions made
**Related:** [Design document](scoring-framework-redesign-design.md) | [Sketch](scoring-framework-redesign-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Exact Turn 1 prompt wording — how prescriptive should the ceiling mechanic be?

The sketch decided on outcome-first evaluation with a ceiling mechanic. The question is how literally to specify the ceiling ranges in the prompt.

### Option A-revised: Explicit non-overlapping ceilings

Key insight: process and outcome quality are naturally correlated. A "perfect process" that produces a wrong conclusion wasn't actually perfect. A "barely correct" conclusion with "excellent process" is a contradiction. This means overlapping ranges and edge case caveats are unnecessary.

```
SCORING FRAMEWORK

Evaluate in two phases. Phase 1 determines the score range; Phase 2
places the score within it.

Phase 1 — INVESTIGATION OUTCOME (determines score range)

Assess the conclusion quality:

- Correct, well-supported conclusion:        60-100
- Partially correct or weakly supported:      35-59
- Wrong or unsupported conclusion:            0-34

A flawed conclusion indicates flaws in the process — even if individual
steps looked methodical, something went wrong.

Phase 2 — INVESTIGATION PROCESS (places score within range)

Assess evidence gathering, tool usage, reasoning quality, efficiency.
This determines where the score falls within the range set by Phase 1.
```

- **Pro:** Unambiguous — non-overlapping ranges, no edge case confusion
- **Pro:** Simple — classify conclusion, get range, assess process within it
- **Pro:** Acknowledges the natural correlation between process and outcome
- **Pro:** Reproducible scoring behavior across runs

**Decision:** Option A-revised — explicit non-overlapping ceilings with a note that flawed conclusions indicate flawed process. Clean ranges (60-100 / 35-59 / 0-34), no overlaps, no edge case caveats. The natural correlation between process and outcome eliminates the need for "guideline" framing or judgment at boundaries.

_Considered and rejected: Option B (priority guidance without numbers — too vague, LLM might not weight outcome heavily enough), Option C (overlapping ranges as guidelines — overlaps unnecessary given the process/outcome correlation)._

---

## Q2: What should the failure vocabulary list be?

The vocabulary needs to be specific enough to be useful for aggregation, broad enough to cover the common failure modes, and small enough that the prompt isn't bloated. Each term must be inherently negative (to avoid false-positive matches in positive context).

### Option C: Start small (~6 terms), grow later, inject dynamically

Start with a minimal set of clearly distinct, actionable terms. The vocabulary is defined as a single Go slice of `{term, description}` structs — the **single source of truth** for both:

1. **Prompt injection** — `BuildScoringInitialPrompt()` formats the vocabulary section dynamically from the slice and injects it into the prompt template
2. **Post-analysis scan** — `scanFailureTags()` iterates the same slice for `strings.Contains` matching

Adding a new tag = add one entry to the Go slice. The prompt updates automatically. No prompt string edits needed.

Starting vocabulary (~6 terms):

```go
var failureVocabulary = []failureTag{
    {"premature_conclusion", "reached a diagnosis without gathering sufficient evidence"},
    {"missed_available_tool", "a relevant tool was available but not used"},
    {"unsupported_confidence", "stated high confidence without comprehensive evidence"},
    {"incomplete_evidence", "stopped gathering evidence before covering all relevant dimensions"},
    {"hallucinated_evidence", "cited or assumed evidence not present in the investigation data"},
    {"wrong_conclusion", "the final diagnosis is incorrect or contradicted by gathered evidence"},
}
```

- **Pro:** Single source of truth — one slice drives prompt and scanning
- **Pro:** Adding a tag is a one-line Go change, no prompt template edits
- **Pro:** Starts small — each term is clearly distinct and actionable
- **Pro:** Prompt hash changes automatically when vocabulary changes (since the rendered prompt content changes)
- **Con:** Initial coverage is limited — can be extended based on observed narrative patterns

**Decision:** Option C with dynamic injection — start with ~6 terms, define as a Go slice that drives both prompt generation and tag scanning. Adding a term is a single-line change with no prompt template edits. Grow the vocabulary based on what failure patterns appear in narratives but aren't captured by existing terms.

_Considered and rejected: Option A (same terms but hardcoded in prompt — requires prompt edits when adding tags), Option B (~12 terms — premature, some overlap, can grow into this organically)._

---

## Q3: How should Turn 2 structure the existing-tool-improvements section?

Turn 2 currently focuses exclusively on missing tools. The sketch decided to expand it to also cover improvements to existing tools. The question is how to structure this in the prompt.

### Option A: Two clearly separated sections

```
## PART 1: MISSING TOOLS

[existing missing tools prompt, slightly reworded]

## PART 2: EXISTING TOOL IMPROVEMENTS

Now review the tools that WERE used during this investigation. Based on the
tool calls you observed (arguments passed, results returned, how the agent
interpreted the results), identify tools that could be improved:

- **Argument clarity**: Did the agent struggle to determine correct arguments?
- **Response format**: Did the tool return data that was hard for the agent to
  parse or extract useful information from?
- **Tool description**: Was there a relevant tool the agent didn't use, possibly
  because its name or description didn't indicate its relevance?
- **Missing discoverability**: Did the tool require argument values the agent had
  no way to discover from the available context?

For each improvement, provide:
- Tool name (as it appears in the AVAILABLE TOOLS section)
- What to improve (argument names, response format, description, etc.)
- Why (what you observed in the investigation that suggests this improvement)
```

- **Pro:** Clear separation — LLM gives proper attention to both categories
- **Pro:** Structured "tool name + what + why" format produces actionable output
- **Pro:** Evaluation criteria guide the LLM to look at specific observable aspects of tool interactions
- **Con:** Longer prompt — roughly doubles Turn 2 length

**Decision:** Option A — two clearly separated sections. The explicit separation ensures both missing tools and existing tool improvements get proper attention. The structured criteria (argument clarity, response format, description, discoverability) guide the LLM to assess specific observable aspects of tool interactions. Extra prompt length is acceptable for Turn 2 (separate LLM call).

_Considered and rejected: Option B (single unified section — the "improvements" part may get less attention when mixed with the more familiar "missing tools" format)._

---

## Q4: Should the failure_tags column allow NULL or default to empty array?

A minor but concrete schema question. The column behavior affects how pre-existing scores (scored before this redesign) are distinguished from new scores that had no tags.

### Option A: Optional/Nillable (NULL for pre-redesign scores)

```go
field.JSON("failure_tags", []string{}).
    Optional().
    Nillable().
    Comment("Failure vocabulary terms found in score_analysis, NULL for pre-redesign scores"),
```

- **Pro:** Cleanly distinguishes "not scanned" (NULL, pre-redesign) from "scanned, no failures found" (empty array `[]`)
- **Pro:** Standard ent pattern for optional JSON fields
- **Pro:** No backfill needed on migration — old rows stay NULL
- **Con:** Consumers need to handle both NULL and empty array

**Decision:** Option A — Nillable. NULL means "pre-redesign, not scanned." Empty array means "scanned, no failures found." Avoids backfilling old rows.

_Considered and rejected: Option B (Optional only, empty array default — can't distinguish pre-redesign from clean scores)._
