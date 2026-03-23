# Investigation Memory — Design Questions

**Status:** Open — decisions pending
**Related:** [Design document](investigation-memory-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Which embedding model and dimensions?

Memory content (1-2 sentence learnings) needs to be embedded as vectors for pgvector similarity search. The model choice affects embedding quality, dimensions (storage cost + index performance), and operational complexity.

### Option A: OpenAI text-embedding-3-small (1536 dimensions)

Use OpenAI's latest small embedding model via direct API call from Go.

- **Pro:** High-quality embeddings, well-tested for short text similarity.
- **Pro:** 1536 dimensions is a standard size — good balance of quality and storage.
- **Pro:** OpenAI API is straightforward to call from Go (simple HTTP POST).
- **Con:** Requires OpenAI API key and network access from the Go backend.
- **Con:** External dependency — adds a failure point for memory extraction.
- **Con:** Cost per embedding call (though minimal for short texts).

### Option B: OpenAI text-embedding-3-small with reduced dimensions (512 or 768)

Same model but use OpenAI's native dimension reduction (`dimensions` parameter).

- **Pro:** Smaller vectors → less storage, faster index operations, lower memory.
- **Pro:** OpenAI's models support this natively — no quality degradation for reduced dims.
- **Pro:** For short 1-2 sentence texts, 512 dimensions captures sufficient semantics.
- **Con:** Same external dependency concerns as Option A.
- **Con:** Slightly lower recall for edge cases compared to full 1536.

### Option C: Use the same LLM provider configured for scoring

Extend the Python LLM service with an `Embed` RPC, using whatever model backend is configured for scoring (e.g., if scoring uses Anthropic, use Anthropic's embedding model; if OpenAI, use OpenAI's).

- **Pro:** No new API keys or configuration — reuses existing LLM infrastructure.
- **Pro:** Embedding provider follows the deployment's LLM choice.
- **Con:** Requires extending the Python gRPC service (new protobuf RPC, new Python code).
- **Con:** Not all LLM backends have embedding models (Anthropic doesn't offer embeddings directly).
- **Con:** Couples embedding quality to the LLM provider choice, which may change.

### Option D: Lightweight open-source model via Python service

Add an embedding endpoint to the Python LLM service using a small open-source model (e.g., `all-MiniLM-L6-v2`, 384 dimensions) running locally.

- **Pro:** No external API calls — fully self-contained, no API keys.
- **Pro:** Very fast inference for short texts.
- **Pro:** 384 dimensions — minimal storage and index overhead.
- **Con:** Lower embedding quality than OpenAI's models (may affect retrieval relevance).
- **Con:** Adds model weight loading to the Python service (memory footprint).
- **Con:** Need to maintain the model dependency.

**Recommendation:** Option B. OpenAI text-embedding-3-small at 512 dimensions. Short memory texts (1-2 sentences) don't need 1536 dimensions — 512 captures sufficient semantics at lower storage cost. Direct API call from Go avoids extending the Python service. The OpenAI dependency is acceptable since TARSy already relies on external LLM APIs.

---

## Q2: How should the Go backend generate embeddings?

The embedding pipeline needs to convert memory content strings into float vectors. This affects where the code lives and how it's called.

### Option A: Direct HTTP call from Go to OpenAI API

A thin Go client in `pkg/memory/embedder.go` that calls the OpenAI embeddings endpoint directly. No Python service involvement.

- **Pro:** Simplest implementation — one HTTP POST, parse JSON response, extract vector.
- **Pro:** No changes to the Python LLM service or protobuf definitions.
- **Pro:** Embedding calls are infrequent (only at memory creation and query time) — no need for batching or streaming.
- **Con:** New API key configuration (`OPENAI_API_KEY` or `EMBEDDING_API_KEY`).
- **Con:** Doesn't go through TARSy's existing LLM abstraction layer.

### Option B: New `Embed` RPC on the Python LLM service

Add an `Embed` RPC to the protobuf definition. The Python service handles the embedding model call.

- **Pro:** Consistent with TARSy's architecture — all LLM/AI calls go through the Python service.
- **Pro:** Can swap embedding models by changing Python config without touching Go.
- **Con:** Protobuf changes, Python implementation, deployment coordination.
- **Con:** More moving parts for a simple HTTP call.

### Option C: Configurable — support both direct API and gRPC

The `Embedder` interface in Go accepts a backend config. Initially implement direct HTTP; add gRPC backend later if needed.

- **Pro:** Maximum flexibility.
- **Con:** Over-engineered for v1 when one approach suffices.

**Recommendation:** Option A. Direct HTTP from Go. Embedding is a simple, infrequent operation (a few calls per investigation for extraction, one call per investigation for query embedding). A thin HTTP client is simpler than extending the gRPC service. The `Embedder` can be an interface for future flexibility without building multiple backends now.

---

## Q3: What pgvector index strategy?

pgvector supports two approximate nearest-neighbor index types. The choice affects query speed, build time, and memory usage.

### Option A: HNSW (Hierarchical Navigable Small World)

- **Pro:** Better recall and query speed for small-to-medium datasets (up to ~1M rows).
- **Pro:** No training phase — index is always up-to-date after inserts.
- **Pro:** pgvector's recommended default for most use cases.
- **Con:** Higher memory usage than IVFFlat.
- **Con:** Slower inserts than IVFFlat (each insert updates the graph).

### Option B: IVFFlat (Inverted File with Flat Compression)

- **Pro:** Lower memory footprint.
- **Pro:** Faster bulk inserts.
- **Con:** Requires periodic reindexing (`REINDEX`) after significant data changes for optimal recall.
- **Con:** Lower recall than HNSW for the same query speed.
- **Con:** Needs tuning (`lists` parameter based on dataset size).

### Option C: No index initially (exact search)

Start without an ANN index. Use exact `ORDER BY embedding <=> query_embedding` with a `WHERE project = $1` filter.

- **Pro:** Zero index maintenance. Perfect recall.
- **Pro:** For small memory stores (hundreds to low thousands), exact search is fast enough.
- **Con:** Doesn't scale — becomes slow at tens of thousands of memories.
- **Con:** Needs to add an index later when the store grows.

**Recommendation:** Option A (HNSW). TARSy's memory store will grow gradually (a few memories per investigation). HNSW handles this well with no tuning, always-current indexing, and good recall. The memory overhead is negligible for the expected dataset size. If performance is fine without an index at first, HNSW can be added in a later migration — but including it from the start avoids a migration under load.

---

## Q4: How should Reflector parse failures be handled?

The Reflector's third turn asks the LLM to output structured JSON. LLMs sometimes produce malformed JSON, extra text around the JSON, or completely ignore the schema.

### Option A: Best-effort parsing with fallback to "no memories"

Try to parse JSON. If it fails, log a warning and treat as "no new learnings." Scoring completes successfully either way.

- **Pro:** Memory extraction never blocks or fails scoring.
- **Pro:** Simplest error handling.
- **Con:** Silent data loss — if the LLM consistently outputs bad JSON, no memories are ever created.
- **Con:** No retry to fix the issue.

### Option B: JSON extraction with retry (same pattern as score extraction)

Try to parse. If it fails, append the malformed output as assistant message plus a "please output valid JSON" user message and retry once. If still fails, fall back to "no memories."

- **Pro:** Recovers from common formatting errors (markdown fences, trailing text).
- **Pro:** Consistent with how `ScoringController` already handles score extraction retries.
- **Con:** One additional LLM call on failure (extra tokens, latency).
- **Con:** More complex control flow.

### Option C: Lenient JSON parsing (strip markdown fences, find JSON in text)

Before strict parsing, apply heuristics: strip ```json fences, find the first `{` and last `}`, try parsing that substring. Only if that fails, treat as "no memories."

- **Pro:** Handles the most common LLM formatting issues without a retry call.
- **Pro:** No extra LLM tokens.
- **Con:** Heuristic parsing can be fragile.
- **Con:** Doesn't recover from genuinely malformed JSON (missing commas, etc.).

**Recommendation:** Option C + A combined. First try lenient parsing (strip fences, find JSON object). If that fails, log a warning and proceed with "no memories." No retry — memory extraction is best-effort and the scoring call is already long enough. The lenient parser can be shared with any future structured-output parsing.

---

## Q5: Should `recall_past_investigations` support structured filters?

The tool lets the agent search for memories beyond the auto-injected set. The question is whether it accepts just a free-text query or also structured filters.

### Option A: Free-text query only

Single `query` parameter. The tool embeds it and runs pgvector similarity search.

- **Pro:** Simplest tool definition — the agent just describes what it wants.
- **Pro:** Semantic search handles intent well ("what went wrong with database connections?").
- **Pro:** Fewer parameters = less for the LLM to get wrong.
- **Con:** Can't explicitly filter by category or valence.

### Option B: Free-text query + optional category/valence filters

`query` (required) + optional `category` and `valence` parameters. Filters are applied as WHERE clauses before similarity ranking.

- **Pro:** Agent can be specific ("show me procedural tips for this alert type").
- **Pro:** Structured filters are cheap to implement (simple WHERE clauses).
- **Con:** More parameters for the LLM to fill — may produce inconsistent results.
- **Con:** Marginal value — semantic search already ranks relevant memories highly.

### Option C: Free-text query + optional filters + alert-type scoping

All of Option B plus an `alert_type` parameter to scope by alert type.

- **Pro:** Most expressive — agent has full control.
- **Con:** Over-parameterized. The agent shouldn't need to know metadata field names.
- **Con:** Conflicts with the "semantic-first, no hard filters" philosophy.

**Recommendation:** Option A. Free-text query only. The semantic search handles intent better than the LLM constructing filter parameters. Keep the tool interface simple — one natural language query, the system does the rest. If agents consistently need structured filtering, it can be added as optional parameters later.

---

## Q6: How should memory refinement trigger on human review?

When a reviewer completes their review (`quality_rating` + optional `investigation_feedback`), memories from that session need their confidence adjusted. The question is when and how.

### Option A: Inline in the review handler

`SessionService.UpdateReviewStatus` → after persisting the review, synchronously calls `MemoryService.RefineFromReview(sessionID, qualityRating)`.

- **Pro:** Immediate — confidence adjustments happen in the same request.
- **Pro:** Simple — no background infrastructure.
- **Pro:** Transactional — review + memory update succeed or fail together.
- **Con:** Adds latency to the review API response (memory queries + updates).
- **Con:** Couples review service to memory service.

### Option B: Background job triggered by review event

The review handler publishes a `ReviewCompleted` event. A listener in the memory package processes it asynchronously.

- **Pro:** Review handler stays fast — no memory work in the hot path.
- **Pro:** Decoupled — review and memory services don't know about each other.
- **Con:** Eventual consistency — confidence adjustments are delayed.
- **Con:** More infrastructure (event listener, error handling, retry).

### Option C: Inline for simple adjustments, background for feedback processing

Confidence adjustments (based on `quality_rating` enum) are inline — they're just numeric updates. If `investigation_feedback` text is present and we want to re-run the Reflector to extract additional memories from the feedback, that goes to a background job.

- **Pro:** Fast path (confidence) is immediate; slow path (feedback analysis) is async.
- **Pro:** Most operations are simple confidence bumps — no latency hit.
- **Con:** Two codepaths for the same trigger.

**Recommendation:** Option A for v1. Confidence adjustments are simple SQL updates (`UPDATE investigation_memories SET confidence = LEAST(confidence + 0.15, 1.0) WHERE source_session_id = $1`). This adds negligible latency to the review request. `investigation_feedback` is stored but not automatically processed in v1 — future enhancement. If performance becomes an issue, migrate to Option B.

---

## Q7: Should the API include a bulk memory management endpoint?

The design includes per-memory CRUD endpoints. The question is whether to also add bulk operations.

### Option A: No bulk endpoint in v1

Per-memory CRUD only. Admin scripts use the list endpoint + per-memory PATCH/DELETE in a loop.

- **Pro:** Simpler API surface.
- **Pro:** Sufficient for the expected scale (tens to hundreds of memories managed at a time).
- **Con:** Tedious for large-scale cleanup.

### Option B: Add `PATCH/DELETE /api/v1/memories` (bulk)

Accept an array of memory IDs with a shared action (e.g., deprecate all, delete all).

- **Pro:** Efficient for bulk operations.
- **Con:** More complex request/response handling, validation.
- **Con:** Low demand in v1 — memory stores won't be large enough to need this.

**Recommendation:** Option A. No bulk endpoint in v1. The memory store will be small early on, and per-memory operations suffice. Bulk endpoints can be added when there's demonstrated need.

---

## Q8: Should memory decay be implemented in v1?

The sketch describes decay: memories not reinforced within a configurable window lose confidence. The question is whether to build this now.

### Option A: No decay in v1

Memories keep their confidence indefinitely. Deprecated memories are hidden from retrieval but not deleted.

- **Pro:** Simpler — no periodic jobs, no decay configuration.
- **Pro:** Early on, you want memories to accumulate, not disappear.
- **Pro:** The Reflector already deprecates outdated memories explicitly (Q7 sketch decision).
- **Con:** Memory store grows without automatic pruning.

### Option B: Simple time-based decay

A periodic job (e.g., daily) reduces confidence of memories not seen in N days.

- **Pro:** Automatic pruning of stale memories.
- **Con:** Needs a periodic job infrastructure.
- **Con:** Risk of decaying valid memories that just haven't been triggered recently.

**Recommendation:** Option A. No decay in v1. The Reflector's in-prompt dedup (sketch Q7) already handles the "outdated memory" case — it can explicitly deprecate memories that contradict new evidence. Automatic time-based decay risks removing valid-but-infrequent knowledge (e.g., "this metric is stale on Mondays" only triggers on Mondays). Decay can be added later if the store grows unmanageably.

---

## Q9: How should injected memory IDs be stored per session?

To show "which memories were injected into this investigation" on the session detail page and to exclude them from recall tool results, we need to record which memory IDs were selected at investigation start.

### Option A: JSON field on AlertSession

Add an `injected_memory_ids` JSON field (string array) to the AlertSession schema.

- **Pro:** Simple — one field, read alongside other session data.
- **Pro:** No new tables or relationships.
- **Con:** JSON field — not queryable for "which sessions used this memory?" without JSON operators.
- **Con:** Adds a field to an already large schema.

### Option B: Join table (session ↔ memory)

A many-to-many relationship table `session_injected_memories` with `session_id` + `memory_id` + `injected_at`.

- **Pro:** Relational — easy to query both directions (memories per session, sessions per memory).
- **Pro:** Supports future features like "memory usage analytics."
- **Con:** More schema complexity (new table, edges in Ent).
- **Con:** Over-engineered for v1 — we mostly read "which memories for this session?"

### Option C: Store in session metadata JSON

Use the existing `session_metadata` JSON field to stash injected memory IDs.

- **Pro:** No schema changes at all.
- **Con:** `session_metadata` is for alert-submission-time metadata — mixing in runtime injection data is a semantic mismatch.
- **Con:** Fragile — metadata field is not typed or validated.

**Recommendation:** Option A. JSON field on AlertSession. The primary use case is "show injected memories on session detail" — a simple JSON array read. "Which sessions used this memory?" is a secondary concern that can use JSON operators or be solved by a join table later if analytics demand it.
