# Investigation Memory — Sketch Questions

**Status:** Complete — all decisions made
**Related:** [Sketch document](investigation-memory-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: What storage backend should memories use?

TARSy already uses PostgreSQL (via Ent ORM). Memories need to be stored, scoped, filtered, and possibly searched by semantic similarity. The storage choice affects retrieval quality, complexity, and operational overhead.

### Option B: PostgreSQL with pgvector extension

Add embeddings to memory rows using pgvector. Retrieval combines scope filtering with cosine similarity search on embeddings.

- **Pro:** Semantic search finds relevant memories even when wording differs.
- **Pro:** Still PostgreSQL — no separate vector DB to operate.
- **Pro:** Hybrid approach: scope filtering narrows candidates, vector search ranks by relevance.
- **Con:** Requires pgvector extension (may need DBA approval in production).
- **Con:** Needs an embedding pipeline (call LLM or embedding API for each memory).
- **Con:** More complex retrieval logic — tuning similarity thresholds, balancing scope vs semantic scores.

**Decision:** Option B — pgvector from the start. Memory entries are short and discrete (unlike OpenClaw's full-document chunking), so the embedding pipeline is lightweight. Semantic-first retrieval (Q6) uses cosine similarity as the primary ranking, with scope metadata as a soft signal. pgvector is a well-established PostgreSQL extension that avoids introducing a separate vector DB.

_Considered and rejected: Option A (plain PostgreSQL with structured filtering only — no semantic search, relies entirely on scope metadata quality), Option C (plain PostgreSQL now, pgvector later — risks design choices that don't suit vector search, and retrofitting embeddings is more work than building them in)._

---

## Q2: Should memory categories (semantic/episodic/procedural) be explicit in the schema?

The research identifies three memory types: semantic (facts), episodic (past incidents), procedural (strategies). These could be explicit schema fields or implicit in the memory content.

### Option A: Explicit category enum in the schema

Add a `category` field with values `semantic`, `episodic`, `procedural`. The Reflector assigns categories during extraction. Retrieval can filter by category.

- **Pro:** Enables category-aware retrieval — inject procedural memories first (most actionable), semantic second, episodic sparingly.
- **Pro:** Dashboard can group and display memories by type.
- **Pro:** Different categories can have different lifecycle rules (procedural memories are more durable than episodic).
- **Con:** Category boundaries are fuzzy — "check DB connection pool first" is both procedural and episodic.
- **Con:** The Reflector (LLM) may not categorize consistently.

**Decision:** Option A — explicit category enum. The categories are well-established in memory research, map cleanly to distinct TARSy use cases (procedural = investigation strategies, semantic = infrastructure facts, episodic = incident history), and enable differentiated display, lifecycle rules, and context for the LLM. Retrieval is semantic-first (Q6) — categories don't hard-filter, they provide structured context alongside the memory content.

_Considered and rejected: Option B (no categories, flat entries with tags — loses ability to prioritize procedural over episodic in prompt injection), Option C (soft enforcement — marginal benefit over A for added ambiguity in retrieval logic)._

---

## Q3: How should memories be injected into investigation prompts?

Retrieved memories need to reach the investigating agent. This could happen via the system prompt, the user message, or as a tool the agent can call.

### Option D: Hybrid — auto-inject a small set + provide a tool for deeper search

Auto-inject the top 3-5 most relevant memories (by pgvector cosine similarity, with scope as a soft boost) into the system prompt as a new Tier 4. Also provide a `recall_past_investigations` tool for the agent to search for more if needed. The injected set acts as a "briefing" and the tool enables deeper recall.

- **Pro:** Solves the chicken-and-egg problem — the agent always gets the most critical context without needing to know memory exists or what to search for.
- **Pro:** The injected briefing naturally hints to the agent that more memory exists and is worth searching.
- **Pro:** Matches the pattern used by the most successful SRE-specific systems (ACE, PagerDuty auto-inject critical context; RubixKube adds interactive query on top).
- **Pro:** Scales — auto-injection handles the 80% case, tool handles the 20% where deeper exploration is needed.
- **Con:** Two retrieval paths to implement and maintain.
- **Con:** Potential for redundancy between injected and tool-retrieved memories (manageable by excluding already-injected IDs from tool results).

**Decision:** Option D — hybrid injection + tool. TARSy's memory is fundamentally different from OpenClaw's (agent never wrote the memories, doesn't know what's there), making auto-injection essential to solve the cold-start problem. The tool adds value for deeper exploration once the auto-injected context triggers the agent's curiosity. Auto-inject top N via system prompt Tier 4 in `ComposeInstructions`; expose `recall_past_investigations` as a pseudo-MCP tool (similar to `load_skill`).

_Considered and rejected: Option A (system prompt injection only — no agency for deeper exploration, sufficient for v1 but limits learning potential), Option B (user message injection — memories confused with current investigation data), Option C (tool-only — chicken-and-egg problem is real for TARSy since the agent never interacted with the memory store and doesn't know what's there or what queries would be useful)._

---

## Q4: How should scoring and human review combine to determine memory quality?

The Reflector needs to know whether an investigation was "good" (extract positive patterns to repeat) or "bad" (extract anti-patterns to avoid). TARSy has automated scoring and human review, but they arrive at different times.

**Prerequisite: review workflow redesign.** The current `actioned`/`dismissed` resolution reasons are ambiguous — they describe what the human did about the alert, not whether the investigation was good. A `dismissed` investigation that correctly identifies a false positive is actually high-quality. This sketch assumes the review workflow is redesigned with three orthogonal fields:

- **`quality_rating`** (enum: `accurate` / `partially_accurate` / `inaccurate`) — explicit, unambiguous investigation quality signal
- **`resolution_summary`** (optional text) — what the human did about the alert/event (historical record, not about investigation quality)
- **`investigation_feedback`** (optional text) — why the investigation was good or bad (the richest signal for memory refinement)

This replaces the current `resolution_reason` (`actioned`/`dismissed`) and `resolution_note` fields.

### Option C: Score-triggered extraction with human review as refinement

Run the Reflector immediately after scoring. Score (0-100) + failure tags + tool improvement report determine initial memory valence and confidence. When a human later reviews the session, their `quality_rating` and `investigation_feedback` refine the memories:

- `accurate` → boost confidence of memories from this session
- `inaccurate` → reduce confidence, potentially flip valence to negative (pattern-to-avoid)
- `partially_accurate` → moderate adjustment
- `investigation_feedback` text → optionally fed into a refinement pass to update or add memories

- **Pro:** Gets memories flowing immediately (automated) while human feedback improves quality over time.
- **Pro:** Works for both reviewed and unreviewed investigations — degrades gracefully.
- **Pro:** The new `quality_rating` provides an unambiguous quality signal, unlike the old `actioned`/`dismissed`.
- **Pro:** Matches the ACE pattern (automated reflection + human corrections) and PagerDuty's approach (memory that compounds with feedback).
- **Con:** Two-phase memory quality assessment is more complex than single-phase.
- **Con:** Human review may contradict the Reflector's initial assessment, requiring memory updates.

**Decision:** Option C — score-triggered extraction with human review refinement. The automated score drives initial memory extraction (no human bottleneck). The redesigned review workflow with `quality_rating` provides an unambiguous refinement signal when humans review. `investigation_feedback` is the richest signal — it explains *why* the investigation was right or wrong, which can feed into targeted memory updates.

_Considered and rejected: Option A (score-only — misses human judgment, scoring LLM can misjudge), Option B (wait for human review — creates bottleneck, many sessions never reviewed, memory store stays empty), Option D (per-memory human feedback — too granular for v1, can be added later if session-level signals prove too coarse)._

---

## Q5: When and how should memory extraction run?

The Reflector needs to analyze completed investigations and produce memory entries. This can be a formal stage in the investigation chain or an async background process.

### Option C: Embedded in the scoring stage (scoring + memory extraction in one pass)

Extend the existing `ScoringController` to also produce memory entries as part of its analysis. The scoring LLM already has the full investigation context — add a third turn asking it to extract learnings.

- **Pro:** No additional LLM call — piggybacks on the scoring context window that's already loaded.
- **Pro:** Zero new infrastructure — extends existing scoring pipeline.
- **Pro:** Scoring analysis and memory extraction are naturally related — the scorer already identifies what went well and poorly.
- **Con:** Couples memory extraction to scoring — can't run memory extraction without scoring, or change one without affecting the other.
- **Con:** Makes the scoring controller more complex (currently 2 turns → 3 turns).
- **Con:** Scoring and memory extraction may benefit from different prompt strategies.

**Decision:** Option C — embedded in the scoring stage. The scoring controller already loads the full investigation timeline and produces exactly the critique (score + failure tags + tool improvement report) that memory extraction needs. Adding a third turn ("Based on your analysis, what discrete learnings should be remembered for future investigations?") is the most token-efficient approach — zero additional LLM cold-start, zero new infrastructure. The coupling is actually a feature: the scorer's own critique is the ideal input for extraction. If they need to diverge later, the third turn can be factored out into its own stage.

_Considered and rejected: Option A (new stage type — adds latency, extraction failures could affect session status, visible but heavy), Option B (async background process — less visible, separate infrastructure, but non-blocking; a reasonable alternative if the coupling in C becomes problematic)._

---

## Q6: How should memory retrieval work?

Memory retrieval serves two purposes: auto-injecting the top 3-5 memories into the system prompt (Q3 hybrid approach), and serving the `recall_past_investigations` tool for deeper search. The central design tension is: **memories should be complementary hints that help in specific or repeating situations — not a rigid playbook that constrains the LLM's natural investigative ability.** LLMs are already good investigators; memory adds value when it surfaces a relevant past lesson at the right moment, not when it floods the context with marginally related knowledge.

This means the retrieval approach must:
- Avoid injecting so much that it reduces creativity or biases the investigation
- Avoid injecting irrelevant noise that distracts from the current alert
- Require minimal manual tuning — the more automated, the better
- Let the bounded auto-inject cap (3-5 memories, per Q3) be the primary noise control

### Option A: Semantic-first retrieval (pgvector-driven)

Embed the current alert context (alert type, description, service, environment) into a query vector. Retrieve by cosine similarity against all memory embeddings. Scope metadata (chain_id, service) provides a soft boost (e.g., same-service memories rank slightly higher) but never hard-filters. Return top N for auto-injection; the tool exposes the full ranked list.

- **Pro:** Zero manual tuning — cosine similarity is the only ranking function. No thresholds, no fallback levels, no scope hierarchy to configure.
- **Pro:** Cross-cutting knowledge surfaces naturally — a memory about "Prometheus metrics lag 5 min on Mondays" appears for any alert where it's semantically relevant, regardless of which service it was learned from.
- **Pro:** Cold start handled gracefully — new services benefit from semantically similar memories from other services.
- **Pro:** pgvector (Q1) earns its keep — this is the primary use case, not just a storage optimization.
- **Pro:** Noise bounded by design — the 3-5 auto-inject cap (Q3) means only the most similar memories appear. The LLM can ignore hints that don't apply.
- **Con:** Requires quality embeddings — if embedding quality is poor (e.g., overly generic memory content), retrieval quality degrades.
- **Con:** May occasionally surface a memory that is textually similar but contextually irrelevant. Mitigated by the small injection count and prompt framing ("consider if relevant").

**Decision:** Option A — semantic-first retrieval. Let pgvector do the heavy lifting. Memory is a light touch — complementary hints for specific and repeating situations, not a comprehensive briefing. Semantic similarity naturally surfaces the most relevant memories without requiring scope hierarchies or tunable weights. The 3-5 auto-inject cap (Q3) is the noise control, not complex filtering logic. Same-service memories can get a minor boost via a simple `ORDER BY cosine_similarity + CASE WHEN service = $1 THEN 0.05 ELSE 0 END DESC` without requiring a full scoring framework. The `recall_past_investigations` tool provides depth when the agent wants it.

_Considered and rejected: Option B (metadata-scoped with semantic ranking — hard-filtering by alert_type blocks cross-cutting knowledge, fallback thresholds are manual tuning), Option C (composite scoring with tunable weights — four weights to calibrate is exactly the tuning treadmill to avoid, over-engineered when the auto-inject cap already bounds noise)._

---

## Q7: How should memory deduplication work?

When the Reflector (third scoring turn, per Q5) extracts memories from a new investigation, some may overlap with existing memories ("check DB connection pool first" extracted from two different investigations). The system needs to handle this — storing duplicates wastes context window space and creates noise. pgvector (Q1) can help identify semantically similar memories even when the text differs.

### Option C: Include existing memories in the Reflector prompt

When the Reflector extracts memories (the third turn in the scoring stage, per Q5), include the current relevant memories in its context. Instruct it to: update existing memories if new evidence reinforces them, add new memories only if they're genuinely novel, and note any memories that should be deprecated.

- **Pro:** No additional LLM call — extraction + dedup happen in the same third scoring turn (Q5). Existing memories are just added to the prompt context.
- **Pro:** The Reflector can make nuanced decisions (strengthen, weaken, merge, deprecate) — exactly what ACE's Curator does.
- **Pro:** Avoids separate dedup logic — it's part of the extraction prompt.
- **Con:** Context window grows as memory store grows (existing memories in prompt). Manageable since memories are scoped and capped, and the scoring context window is already large.
- **Con:** Couples extraction and deduplication logic.

**Decision:** Option C — include existing memories in the Reflector prompt. With extraction embedded in the scoring stage (Q5), including existing memories in the same turn is essentially free. The Reflector makes one pass: "here's what we learned before, here's what just happened — update the knowledge base." Consistent with the semantic-first retrieval approach (Q6), existing memories to include in the Reflector prompt are found by pgvector similarity — not just strict scope matching — so cross-cutting memories can be recognized as duplicates even when they originated from different services.

_Considered and rejected: Option A (exact text dedup only — LLM output is never identical, effectively no dedup, memory store grows with near-duplicates), Option B (embedding-based similarity + LLM merge — two-step process with a similarity threshold to tune, when the Reflector can handle it in one pass for free)._

---

## Q8: Should memory extraction run for every investigation or be selective?

Since extraction is a third turn in the scoring stage (Q5) — not a separate LLM call — the cost argument is weaker than it would be otherwise. Still, every turn adds tokens, and the Reflector needs existing memories in context (Q7 Option C). The question is whether to always run this turn or skip it selectively.

### Option A: Extract from every completed investigation

Run the Reflector (third scoring turn, per Q5) after every investigation that gets scored, regardless of score or novelty.

- **Pro:** No learning gaps — every investigation contributes.
- **Pro:** Simple logic — no selectivity to tune.
- **Pro:** Near-zero marginal cost — extraction is a third turn in an already-running scoring call (Q5), not a separate LLM invocation.
- **Con:** Most extractions for routine investigations will produce nothing new (but the Reflector can simply return "no new learnings" when existing memories cover the case, per Q7).

**Decision:** Option A — always extract. The cost argument for being selective evaporated with Q5 (third scoring turn, not a separate call). The Reflector (Q7) gracefully handles "nothing new" cases in-prompt. No thresholds to tune, no pre-checks, no config surface area. Per-chain enable/disable (Option D) can be added trivially later if needed.

_Considered and rejected: Option B (score-based filtering — arbitrary thresholds, minimal cost savings given Q5), Option C (novelty detection — unnecessary complexity when Q7's in-prompt dedup handles the "nothing new" case), Option D (configurable per chain — not worth the config surface area for v1 when "always" is the right default)._

---

## Q9: Should memory management be exposed in the dashboard from day one?

Users need some way to see what TARSy has learned and potentially curate it. The dashboard already needs changes for the redesigned review workflow (Q4: `quality_rating`, `resolution_summary`, `investigation_feedback`), so there's an opportunity to add memory visibility alongside those changes.

### Option B: Memory visible in session detail + API only

Show extracted memories on the session detail page (what was learned from this investigation). Also show which memories were auto-injected (Q3 hybrid approach) into the investigation prompt. Provide API endpoints for listing, editing, and deleting memories. No dedicated Memory page.

- **Pro:** Lower frontend effort — leverages existing session detail page.
- **Pro:** Memories are shown in context (alongside the investigation that produced them).
- **Pro:** Can co-ship with the review workflow redesign (Q4) — the session detail page is already being updated to show `quality_rating` and `investigation_feedback`.
- **Con:** No aggregate view — hard to see all memories across investigations.
- **Con:** API-only management requires curl or scripts for bulk operations.

**Decision:** Option B — memory visible in session detail + API only. The session detail page is already being updated for the redesigned review workflow (Q4), so adding memory visibility there is incremental. Showing "what was injected" and "what was learned" per session gives concrete context. API endpoints enable power users and automation. A dedicated Memory page (Option A) can be added when the feature matures and there's demand for aggregate views.

_Considered and rejected: Option A (full memory management UI — significant frontend effort for v1, can be added later), Option C (minimal read-only list — misses the "in context" value of showing memories alongside the investigation that produced them)._
