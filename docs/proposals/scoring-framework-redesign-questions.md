# Scoring Framework Redesign — Sketch Questions

**Status:** All decisions made
**Related:** [Sketch document](scoring-framework-redesign-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: How should the evaluation be structured and scored?

The current framework has 4 equally-weighted categories (Logical Flow, Consistency, Tool Relevance, Synthesis Quality) that are all primarily process-focused. The prompt explicitly states "Process > Outcome." The core proposal is to flip this: make outcome quality the dominant factor while still evaluating process.

### Option D: Single holistic score, outcome-first evaluation with ceiling mechanic

Keep a single 0-100 score. The prompt structures the evaluation in two phases — first assess outcome (conclusion quality), then assess process — but produces one holistic number. Conclusion quality determines the **score ceiling**: a wrong conclusion caps the score regardless of process quality.

Prompt guidance:
> First assess the investigation outcome — was the conclusion correct, well-supported, and actionable? This determines the score ceiling:
> - Strong, evidence-backed conclusion: eligible for 60-100
> - Partially correct or weakly supported: capped at 40-65
> - Wrong or unsupported conclusion: capped at 0-40
>
> Then assess the process — evidence gathering, tool usage, reasoning quality — to place the score within that ceiling.

The narrative analysis still covers both dimensions in detail (the judge reports its assessment of outcome and process separately), but only one number is extracted.

- **Pro:** Simplest possible change — prompt only, zero schema/extraction/API/UI changes
- **Pro:** The score directly answers the question "was this investigation good?" with outcome as the dominant factor
- **Pro:** Natural for LLM evaluation — holistic assessment with priority guidance, rather than mechanical sub-score allocation
- **Pro:** The two-phase evaluation still structures the judge's thinking (outcome first, then process)
- **Pro:** A wrong conclusion can never hide behind a good process score
- **Con:** Can't query/trend outcome vs process independently (narrative has the detail but it's not structured)
- **Con:** "Score was 45" doesn't tell you if outcome was bad or process was bad — you read the narrative

**Decision:** Option D — single holistic score with outcome-first evaluation and ceiling mechanic. The score answers "was the investigation good?" with conclusion quality as the dominant factor by construction. Two-phase evaluation structures the judge's thinking without requiring structured sub-score extraction. Zero infrastructure changes — the change is purely in the prompt.

_Considered and rejected: Option A (5 sub-scores — extraction complexity for marginal queryable benefit), Option B (2 stored scores — adds schema/API/UI changes; the ceiling mechanic achieves the same priority without separate scores), Option C (detailed sub-categories but 2 stored scores — same as B, unnecessary if single score with ceiling works)._

---

## Q2: How should Outcome vs Process be weighted?

_Folded into Q1._ The ceiling mechanic from Q1/Option D handles weighting implicitly: conclusion quality determines the score ceiling, process quality places the score within that ceiling. No separate weight split needed.

---

## Q3: How should structured data be extracted from the LLM response?

_Mostly folded into Q1._ With a single holistic score, the existing number-on-last-line extraction works unchanged. However, if failure tags (Q4) are added, the extraction method needs revisiting — see Q4 for that decision.

---

## Q4: Should the judge produce failure mode tags?

Failure mode tags are short labels identifying specific problems in the investigation. They could enable aggregation queries like "what % of sessions have premature conclusions?"

### Option D: Reference vocabulary in prompt + server-side string scan into JSON column

Include a list of common failure patterns in the prompt as **guidance, not a constraint**. The judge uses those terms when they fit but describes any problem it finds, even if it doesn't match a predefined term. Terms appear naturally in the narrative.

After receiving the analysis, Go code does a simple `strings.Contains` scan for each known vocabulary term and stores matches as a JSON array on `session_scores`. No LLM output format requirements — the scan happens on text already captured.

The vocabulary consists exclusively of negative failure labels (`premature_conclusion`, `hallucinated_evidence`, etc.), making false-positive matches in positive context extremely unlikely.

Approximate prompt wording:
> Common failure patterns to watch for (use these terms when applicable, but describe any problems you identify even if they don't match these patterns):
> - premature_conclusion — jumped to root cause without exhausting evidence
> - missed_available_tool — an existing tool wasn't used when it should have been
> - unsupported_confidence — confidence level higher than evidence warrants
> - ...

- **Pro:** Guides the judge's evaluation without constraining it — evaluation quality is never sacrificed for tagging
- **Pro:** Zero extraction fragility — no parsing of LLM output, just string matching on text already in hand
- **Pro:** Queryable tags in Postgres (`WHERE failure_tags @> '["premature_conclusion"]'`)
- **Pro:** Vocabulary is exclusively negative terms — virtually no false positives from positive context
- **Pro:** The judge is free to describe novel problems in its own words; the vocabulary is guidance, not a constraint
- **Con:** One new JSON column on `session_scores` (minor migration)
- **Con:** Only catches known vocabulary terms — novel failures described in different words are not captured as tags (but they still appear in the narrative)

**Decision:** Option D — reference vocabulary in the prompt as non-constraining guidance, server-side `strings.Contains` scan to materialize matched tags into a JSON column on `session_scores`. The vocabulary is exclusively negative failure labels, making false positives negligible. The judge is free to use its own language for any problem — evaluation quality always takes priority over tagging consistency.

_Considered and rejected: Option A (controlled vocabulary with LLM-produced JSON — adds extraction fragility), Option B (freeform LLM tags — inconsistent naming defeats aggregation), Option C (no vocabulary at all — misses the opportunity to guide consistent terminology and enable cheap aggregation)._

---

## Q5: Should the judge produce an "improvement suggestions" section?

Beyond scoring and tagging, the judge could produce forward-looking suggestions for improving TARSy. Key constraint: the judge does NOT see agent prompts — only the investigation output (thinking, tool calls with arguments, tool results, final analysis). So it cannot recommend prompt changes. It can, however, observe how agents interact with tools.

### Option D: Expand Turn 2 from "missing tools" to "tool improvement report"

Keep the 2-turn structure. Widen Turn 2's scope from "what tools are missing?" to "what tools are missing or could work better?" The judge already sees every tool call (with arguments) and every tool result, so it can assess:

- Tools that exist but have confusing argument names/structure (agent tried multiple argument combinations)
- Tools that return data in formats that are hard for the agent to use (wall of JSON, unclear structure)
- Tools whose descriptions don't make clear when they're useful (agent never used a tool that was relevant)
- Tools that require argument values the agent had no way to discover

Turn 2 covers:
1. **Missing tools** — new tools that should be built (same as today)
2. **Existing tool improvements** — argument changes, response format improvements, better descriptions

- **Pro:** Grounded in what the judge can actually observe (tool calls, arguments, results)
- **Pro:** Natural extension of existing Turn 2 — same flow, slightly wider scope
- **Pro:** Directly actionable — tool improvements are concrete engineering tasks
- **Pro:** Addresses a real gap — current Turn 2 only suggests new tools, never improvements to existing ones
- **Con:** Changes the semantics of `missing_tools_analysis` (now covers improvements too) — field could be renamed or kept as-is with broader content

**Decision:** Option D — expand Turn 2 from "missing tools only" to a broader tool improvement report covering both missing tools and improvements to existing tools. The judge can observe tool interactions directly (arguments, results, failure patterns) so its suggestions are grounded. No new turn, no new field — just a wider scope for the existing follow-up prompt.

_Considered and rejected: Option A (dedicated improvement section in Turn 1 — the judge can't see prompts so can't recommend prompt changes; risks generic advice), Option B (no suggestions at all — misses the opportunity to improve existing tools), Option C (broad "improvement recommendations" covering prompts/chain/tools — the judge can't see prompts or chain config, only tool interactions)._
