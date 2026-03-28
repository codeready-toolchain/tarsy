# Memory Recall Improvements — Design Questions

**Status:** All decisions finalized  
**Related:** [Design document](memory-recall-improvements-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Similarity threshold approach

The system currently returns top-k results with no minimum similarity floor. A query about "user john smith" returns 10 generic memories because *something* is always closest in embedding space. The question is how to filter out irrelevant results.

### Option A: Fixed similarity threshold

Add a hard cutoff (e.g., `(1 - distance) >= 0.45`) in the outer query. Memories below this score are discarded. The threshold is a code constant, not configurable.

- **Pro:** Simple to implement — one `WHERE` clause added to the SQL.
- **Pro:** Deterministic and predictable. Easy to reason about.
- **Pro:** Aligns with ADR-0014's "zero manual tuning" principle — the value is a constant, not a config knob.
- **Con:** The "right" threshold depends on the embedding model. Switching models (e.g., from `gemini-embedding-2-preview` to a future model) may require recalibrating.
- **Con:** Embedding similarity distributions vary by domain — a fixed value may be too aggressive for some query types and too lenient for others.

**Decision:** Option A — fixed constant in code, reviewed when the embedding model changes (same as pgvector column dimensions).

_Considered and rejected: Option B (configurable threshold — adds a knob operators won't understand, contradicts "zero manual tuning"), Option C (score-gap detection — complex, noisy, hard to explain)._

---

## Q2: How to incorporate confidence into ranking

Memory confidence currently ranges from 0.3 (poor investigation) to 1.0 (human-confirmed), derived initially from the investigation's quality score. Currently it affects storage lifecycle but not retrieval ranking. The question is whether and how to let confidence influence which memories surface.

During discussion, a deeper issue emerged: the score-derived initial confidence double-counts quality judgment. The Reflector LLM already sees the full quality evaluation (score, failure tags, score analysis, tool improvement report) when deciding what to extract. A real production example demonstrated the problem — a 74-scoring investigation (penalized for process inefficiency, not wrong conclusions) produced 6 excellent, highly specific memories, but these would start at confidence 0.6 and receive a permanent ranking penalty despite being top-quality extractions.

### Option A: Multiplicative confidence factor with flat initial confidence

Two changes:

1. **Flat initial confidence** for all auto-extracted memories (0.7) instead of score-derived (0.3–0.8). The `initialConfidence(score)` function is replaced by a constant. The Reflector is trusted as the quality gate at extraction time.

2. **Multiplicative ranking formula**: `(1 - distance) * (0.7 + 0.3 * confidence)`. Confidence impact scales with semantic relevance — human reviews matter most for highly relevant memories.

Confidence becomes a **pure human/reinforcement signal**:

| State | Confidence | Multiplier |
|---|---|---|
| Unreviewed (baseline) | 0.70 | 0.91 |
| Reinforced 3× | 0.93 | 0.98 |
| Human: accurate | 0.84 | 0.95 |
| Human: partially_accurate | 0.42 | 0.83 |
| Human: inaccurate | deprecated | gone |
| Feedback-created | 0.90 | 0.97 |

- **Pro:** No double-counting — the Reflector already applies quality judgment at extraction time with full visibility into the score.
- **Pro:** Human reviews have maximum impact — the spread from `partially_accurate` (0.83) to `accurate` (0.95) is 15%, meaningful differentiation.
- **Pro:** Confidence becomes a clean signal: "has a human verified this?" rather than a murky mix of automated score + human adjustment.
- **Pro:** Confidence impact scales with relevance — human reviews matter most where it counts.
- **Pro:** Feedback-created memories (0.9) rank highest, reflecting that human feedback is the strongest signal.
- **Con:** Loss of automated safety net — if the Reflector creates a bad memory from a terrible investigation, there's no score-based deprioritization. Mitigated by: the Reflector sees the full quality critique, future investigations deprecate contradicted memories, and the similarity threshold (Q1) limits exposure.
- **Con:** Introduces a magic constant (the 0.7/0.3 split) in the ranking formula.

**Decision:** Option A — flat initial confidence (0.7) + multiplicative ranking. The Reflector is trusted as the quality gate at extraction time. Confidence is reserved for the most valuable signal: human review. The score is still passed to the Reflector (it uses it for extraction decisions), but no longer determines initial confidence.

Code impact: `initialConfidence(score int)` becomes a constant `initialConfidence = 0.7`, the `score` parameter is dropped from `ApplyReflectorActions`.

_Considered and rejected: Option B (additive boost with score-derived confidence — too subtle at +0.05, human review signal diluted to a tiebreaker), Option C (defer confidence in ranking entirely — wastes the human review signal), Option A-original (multiplicative with score-derived confidence — double-counts quality judgment, penalizes good memories from imperfect investigations)._

---

## Q3: Recency signal in ranking

ADR-0014 deferred temporal decay to post-v1: "Future: query-time multiplier `score × e^(-λ × age_in_days)`." The memory store is days old. The question is whether to add recency now.

**Decision:** Option A — exponential decay with 90-day half-life, based on `updated_at` (not `created_at`).

Initial analysis favored deferral, but a deeper look at the full system changed the decision. The key insight: **Reflector reinforcement is the natural counter to decay.** Memories that are still relevant get reinforced during new investigations, which updates `updated_at` and resets the decay clock. Memories that are no longer relevant get no reinforcement and fade naturally. This creates a self-correcting "use it or lose it" mechanism that explicit deprecation alone cannot replicate — deprecation is binary (active or gone), while decay is gradual.

Formula: `EXP(-0.0077 * age_in_days)` where `0.0077 = ln(2) / 90`. Implementation is one multiplier in the `ORDER BY` — no schema changes, no background jobs.

The one trade-off: rare patterns (seen once every 6 months) lose ranking between occurrences. At 180 days without reinforcement, the decay multiplier is 0.25. But when the pattern recurs, the Reflector reinforces the memory (or creates a new one) — the system self-corrects within one investigation cycle. A 90-day half-life is gentle enough that this risk is acceptable.

_Considered and rejected: Option B (defer, rely on deprecation only — lacks gradual decay for the gap between creation and explicit deprecation, doesn't leverage the natural reinforcement→freshness signal)._

---

## Q4: Entity-level recall

The production example shows agents querying for specific users/accounts — a use case the memory system fundamentally cannot serve (memories are generalized by design). The question is whether to address this gap now and how.

### Option B: Session search tool + hybrid search for existing memory recall

Two complementary changes:

**1. New `search_past_sessions` tool** — searches `alert_sessions` for past investigations using PostgreSQL full-text search (`tsvector`) on `alert_data`. Format-independent: works on JSON, plain text, YAML — any alert structure. The agent searches for specific identifiers (namespace, username, workload name) and gets back session summaries.

Implementation:
```sql
-- Generated column, format-independent, auto-synced
ALTER TABLE alert_sessions ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED;
CREATE INDEX idx_sessions_search ON alert_sessions USING gin(search_vector);
```

The `'simple'` configuration preserves identifiers without stemming (important for usernames). The tool accepts a search query + optional filters (alert_type, days_back) and returns an LLM-summarized digest of matching sessions (see refinement #2 below).

**2. Upgrade `recall_past_investigations` to hybrid search** — add `tsvector` to `investigation_memories.content` and combine vector similarity with keyword matching using Reciprocal Rank Fusion (RRF). This fixes the production failure where querying "quantiaia coolify VM" failed to find the Coolify memory because extra query terms shifted the embedding away from "coolify". Research shows pure vector search achieves ~62% precision while hybrid search reaches ~84%.

Implementation:
```sql
ALTER TABLE investigation_memories ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED;
CREATE INDEX idx_memories_search ON investigation_memories USING gin(search_vector);
```

`FindSimilarWithBoosts` becomes a hybrid query: inner CTE fetches candidates from both vector similarity AND keyword match, merged with RRF before the final re-ranking with confidence/scope boosts.

- **Pro:** Directly addresses the entity-recall gap — "was this user investigated before?" becomes answerable via session search.
- **Pro:** Fixes the "exact term not found" failure in memory recall — hybrid search catches what pure vector search misses.
- **Pro:** Format-independent — `tsvector` tokenizes any text, no parsing of alert_data structure needed.
- **Pro:** Clean separation of concerns — `recall_past_investigations` for pattern knowledge, `search_past_sessions` for investigation history.
- **Pro:** Uses PostgreSQL built-in capabilities — no new services, same database, GIN index for performance.
- **Pro:** High-value for escalation logic — seeing 3 prior investigations of the same user is decisive evidence.
- **Con:** Requires database migration (two new tsvector columns + GIN indexes).
- **Con:** RRF fusion adds complexity to the memory retrieval SQL.
- **Con:** Increases the agent's tool surface area (new tool).

**Decision:** Option B — session search tool (tsvector on alert_sessions) + hybrid search for memory recall (tsvector + RRF on investigation_memories). The entity-recall gap is real and high-impact. Full-text search is the right mechanism because it's format-independent and handles exact identifier matching where vector search fails. Hybrid search for the existing memory recall is a natural extension using the same infrastructure.

**Refinements decided during walkthrough:**

1. **Tool descriptions are critical** — the `search_past_sessions` description must explicitly instruct the agent to pass short, identifier-focused queries (not natural language) and make separate calls for unrelated identifiers. This is tightly coupled with the AND search logic — the description is the primary mechanism that ensures the agent uses the tool correctly. Both descriptions must be generic (no domain-specific examples) and work as a complementary pair. See design doc change #6 for the exact wording.

2. **LLM summarization for session results** — returning raw executive summaries is insufficient because they strip entity identifiers (e.g., "personal cloud deployment, no malicious activity" doesn't say which user). The tool uses a single batched LLM call over all matching sessions (with full `final_analysis`, `alert_data`, human feedback) to produce a query-aware digest that preserves entity context and flags when a match is about a different entity than the one queried.

3. **Same model as the current agent** — the summarization call uses the same LLM provider/model and retry/fallback infrastructure as the calling agent. Follows the existing tool summarization pattern for simplicity.

4. **No fallback to raw data** — if the summarization call fails after retries/provider fallback, the tool returns an error. Returning unsummarized data would risk the exact entity-confusion problem this solves.

5. **Only completed sessions** — in-progress, failed, cancelled sessions are filtered out (no reliable conclusions).

6. **AND logic for search** — uses `plainto_tsquery` (all query terms must be present in the same session). Prevents false positives from natural-language queries containing filler words like "user" or "abuses." The tool description guides the agent to search with specific identifiers (e.g., "johnsmith", "coolify") and make separate calls for unrelated entities. AND is the safe default — returning nothing for a too-broad query is better than returning noisy false positives.

_Considered and rejected: Option A (better tool description only — doesn't address the legitimate entity-recall need), Option C (entity-specific memory tier — over-engineers with a parallel storage tier for data already in alert_sessions), Option B-original (session search with ILIKE — false positive problem on common terms like "user" or "centos"), Option B-structured (extracted namespace column — couples schema to alert format structure), returning raw executive summaries (strips entity identifiers, would mislead agent when sessions match on keyword but involve different entities), per-session LLM calls (N calls = slow, single batched call solves this)._

---

## Q5: Similarity threshold for auto-injection (Tier 4)

The same retrieval path (`FindSimilarWithBoosts`) is used for both the `recall_past_investigations` tool and Tier 4 auto-injection at investigation start. Should the threshold apply to both, or only the tool?

**Decision:** Option A — same threshold for both. If a memory isn't similar enough to be useful when explicitly searched, it's not useful when auto-injected either. The agent starting without memory hints is fine — it's been doing that for every investigation before this feature existed. Consistent behavior, saves prompt tokens.

_Considered and rejected: Option B (lower threshold for auto-injection — two thresholds to maintain, low-relevance memories still waste tokens), Option C (no threshold for auto-injection — inconsistent, acknowledges noise is bad for the tool but injects it into the prompt anyway)._

---

## Q6: Reflector extraction selectivity

Better retrieval can't fix a noisy store. A real production example (74-scoring investigation) produced 6 memories, but critical review reveals only 2–3 are genuinely high-quality cross-cutting knowledge. The rest are: overly niche one-off patterns unlikely to recur, speculative findings that weren't verified, or general agent behavior guidance that belongs in skills/instructions rather than memories.

At ~4–6 memories per investigation and ~10–20 investigations/day, the store grows by 30–60 memories/week. If 30–40% are marginal, the store fills with noise that competes with good memories at similar cosine distances — a problem the similarity threshold (Q1) can't solve because marginal memories are semantically relevant, just not useful.

Production data reveals a specific, dominant noise pattern: **tool-bug and tool-workaround memories consume 3–5 of 10 result slots in every query**. Across 4 real recall examples, `vm-ssh` limitations alone produced 3–4 separate memories (blocked commands, grep parsing bug, redirect failures, host key issues) that surface for ANY VM-related query. These memories are correct but **domain-generic** — they apply to every investigation of this type and crowd out pattern-specific knowledge. A query specifically about "Coolify" failed to retrieve the Coolify pattern memory because tool-bug memories occupied the slots instead.

These don't belong in memories. They belong in **skills or custom instructions** — always available without consuming retrieval slots.

**Decision:** Option C — prompt guidance + soft cap communicated to the Reflector. The Reflector prompt gets tighter extraction criteria (Option A guidelines) plus a soft cap: "Aim for 0–3 new memories per investigation. Exceeding 3 creates should be rare and justified by a genuinely rich investigation." No code-level enforcement — the Reflector exercises judgment.

**Important:** The prompt changes must not disrupt the Reflector's JSON output format. The extraction criteria and soft cap are placed in a dedicated `## Extraction Boundaries` section, clearly separated from both the `## Quality Guidelines` (which cover how to judge extraction-worthy learnings) and the output format specification (which lives in the user prompt, not the system prompt). This keeps "what not to extract" distinct from "how to judge quality" and "how to format the response" so the LLM doesn't conflate them.

**Default recall limit stays at 10.** With the similarity threshold (Q1), confidence in ranking (Q2), hybrid search (change #8), and tighter Reflector extraction (this change), the quality of returned results improves enough that artificially capping at 5 would cut off genuinely relevant memories. The threshold is the real quality gate — the limit is just a cap.

_Considered and rejected: Option A (prompt guidance only — no soft cap signal, Reflector may still produce 5-6 memories per investigation), Option B (hard code-level cap — arbitrary truncation, silently drops memories, Reflector can't optimize ordering), lowering default recall limit to 5 (redundant with the similarity threshold as quality gate, would cut off relevant results that pass the threshold)._
