# ADR-0014: Investigation Memory

**Status:** Implemented
**Date:** 2026-03-25
**Prerequisites:** [ADR-0013: Review Feedback Redesign](0013-review-feedback-redesign.md)

## Overview

TARSy investigations are stateless — each investigation starts from scratch with no knowledge of past investigations. This manifests as:

- **Repeated dead ends** — the agent queries a metric that doesn't exist under that name in this environment, every time
- **Missed shortcuts** — a senior SRE would check the database connection pool first for this type of alert, but the agent doesn't know that
- **Lost baselines** — "200 errors/hr during batch processing is normal for this service" is discovered and forgotten
- **No learning from quality feedback** — investigations scored poorly (or reviewed by humans) don't improve future investigations

Investigation Memory adds a learning loop: after each investigation, a Reflector extracts discrete learnings; before each investigation, relevant memories are injected as hints. Human review feedback refines memory quality over time. The goal is to accumulate institutional knowledge so that each investigation benefits from what was learned before — both **what works** (effective strategies, environment facts) and **what doesn't** (dead ends, mistakes, misleading patterns).

### Existing Signals

TARSy already produces the raw material for memory extraction:

| Signal | Source | What it tells us |
|--------|--------|------------------|
| Score (0-100) | `SessionScore.total_score` | Overall investigation quality |
| Score analysis | `SessionScore.score_analysis` | Detailed critique of what went well/poorly |
| Failure tags | `SessionScore.failure_tags` | Standardized failure patterns (`premature_conclusion`, `missed_available_tool`, etc.) |
| Tool improvement report | `SessionScore.tool_improvement_report` | Tool misuse, missed tools, or improvement needs |
| Human quality rating | `AlertSession.quality_rating` | `accurate` / `partially_accurate` / `inaccurate` — explicit investigation quality ([ADR-0013](0013-review-feedback-redesign.md)) |
| Investigation feedback | `AlertSession.investigation_feedback` | Free-text explaining why the investigation was good or bad |
| Final analysis | `AlertSession.final_analysis` | The investigation's conclusion |
| Investigation timeline | `TimelineEvent` rows | Full tool calls, reasoning, findings |
| Alert metadata | `AlertSession.alert_type`, `chain_id` | Categorization and context |

## Design Principles

1. **Memory is a light touch.** Complementary hints for specific/repeating situations, not a playbook. The LLM's investigative creativity is preserved.
2. **Semantic-first retrieval.** pgvector cosine similarity drives ranking. Investigation scope metadata (`alert_type`, `chain_id`) is a soft boost, never a hard filter. Infrastructure-specific context (service names, clusters, regions) lives in the memory content itself and is matched via semantic search. Project is the only hard filter (security boundary).
3. **Zero manual tuning.** No thresholds, no fallback levels, no scoring weights to calibrate. The `max_inject` cap (default: 5) is the noise control. Scope metadata soft boosts (`+0.05` for alert_type, `+0.03` for chain_id) are fixed implementation constants — negligible tiebreakers, not weights to tune.
4. **Embedded extraction.** Memory extraction runs as a separate LLM call within the scoring stage — triggered by the `ScoringExecutor` after the scoring controller completes. No new infrastructure, near-zero marginal cost.
5. **In-prompt dedup.** The Reflector sees existing memories and decides what to create, reinforce, or deprecate in one pass.
6. **Future-proof for multi-tenancy.** `project` field from day one, hard-filtered on every query.

## Decisions

### Sketch-Level Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| S1 | Storage backend | PostgreSQL with pgvector | Semantic-first retrieval without a separate vector DB. Memory entries are short and discrete, so the embedding pipeline is lightweight. |
| S2 | Explicit categories | Enum: `semantic`, `episodic`, `procedural` | Well-established in memory research, maps to distinct TARSy use cases. Enables differentiated display and lifecycle rules. Categories don't hard-filter — they provide structured context. |
| S3 | Prompt injection method | Hybrid: auto-inject top N + `recall_past_investigations` tool + `search_past_sessions` tool | Auto-injection solves the cold-start problem. `recall_past_investigations` enables deeper exploration of distilled learnings. `search_past_sessions` provides entity-level recall from raw investigation history. |
| S4 | Quality signal source | Score-triggered extraction + human review refinement | Automated score drives initial extraction (no human bottleneck). `quality_rating` from [ADR-0013](0013-review-feedback-redesign.md) provides unambiguous refinement signal. |
| S5 | Extraction timing | Embedded in scoring stage (separate LLM call) | Reuses investigation context already built for scoring. Separate conversation gives the Reflector its own system prompt. Near-zero new infrastructure. |
| S6 | Retrieval strategy | Semantic-first (pgvector cosine similarity) | Zero manual tuning. Cross-cutting knowledge surfaces naturally. Cold start handled gracefully. Auto-inject cap is the noise control. |
| S7 | Deduplication | In-prompt: existing memories included in Reflector context | One-pass extraction + dedup. Reflector decides what to create, reinforce, or deprecate. No separate dedup logic. |
| S8 | Extraction selectivity | Every scored investigation | Low marginal cost (context already available). Reflector returns "no new learnings" for routine investigations. No thresholds to tune. |
| S9 | Dashboard exposure | Session detail + API only (no dedicated page in v1) | Incremental frontend effort. Memories shown in context alongside the investigation. API enables power users. |

### Design-Level Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| D1 | Embedding model | Google `gemini-embedding-2-preview` at 768 dims | MTEB English #1 (67.99 at 768 dims — negligible loss from full 3072). Same `GOOGLE_API_KEY` already configured. Zero-config path. Configurable via `defaults.memory.embedding`. |
| D2 | pgvector index | HNSW (`m=16, ef_construction=64, vector_cosine_ops`) | Best recall for small-to-medium datasets, no training phase, always up-to-date after inserts. |
| D3 | Reflector parse failures | Lenient JSON parsing + silent fallback | Strict parse → strip markdown fences → bracket-depth extraction. On failure, log warning and proceed with "no memories." Extraction never blocks scoring. |
| D4 | Memory tool parameters | `recall_past_investigations`: free-text `query` + `limit`. `search_past_sessions`: keyword `query` + `alert_type` + `days_back` + `limit` | Semantic search for distilled learnings; keyword search for entity-level session lookup. Different retrieval methods for different use cases. |
| D5 | Review refinement trigger | Inline multiplicative confidence + background Reflector for feedback text | Part 1 (sync): `quality_rating` adjusts confidence. Part 2 (async): `investigation_feedback` text triggers a Reflector variant that creates/deprecates/reinforces memories. |
| D6 | Bulk memory API | Not in v1 | Per-memory CRUD suffices for expected scale. |
| D7 | Memory decay | Query-time temporal decay | `EXP(-0.0077 × age_in_days)` multiplier (90-day half-life) based on `updated_at`. Reinforcement resets the clock. No data mutation. |
| D8 | Storing injected memory IDs | Many-to-many Ent edge (join table) | Both query directions natural: `session.QueryInjectedMemories()` and `memory.QueryInjectedIntoSessions()`. |

## Architecture

```text
┌──────────────────────────────────────────────────────────┐
│                   Investigation Flow                     │
│                                                          │
│  Alert → Chain Stages → Final Analysis → Exec Summary    │
│                                                    │     │
│                                              ┌─────▼──┐  │
│                                              │Scoring │  │
│                                              └────┬───┘  │
│                                                   │      │
│                                      ┌────────────▼───┐  │
│                                      │  Reflector     │  │
│                                      │  (extract      │  │
│                                      │   memories)    │  │
│                                      └───────┬────────┘  │
└──────────────────────────────────────────────┼───────────┘
                                               │
                                               ▼
                                    ┌─────────────────────┐
                                    │   Memory Store      │
                                    │   (PostgreSQL +     │
                                    │    pgvector)        │
                                    └─────────┬───────────┘
                                              │
                      ┌───────────────┬───────┼───────────┬───────────┐
                      │               │       │           │           │
                      ▼               ▼       ▼           ▼           ▼
              ┌──────────┐   ┌────────────┐  ┌───────────┐  ┌──────────┐
              │ Prompt   │   │ recall_past│  │search_past│  │ Human    │
              │ Injection│   │ investiga- │  │ _sessions │  │ Review   │
              │ (Tier 4) │   │ tions tool │  │ tool      │  │ Feedback │
              └──────────┘   └────────────┘  └─────┬─────┘  └──────────┘
                                                   │
                                          ┌────────▼──────────┐
                                          │ alert_sessions    │
                                          │ (FTS: tsvector)   │
                                          │        +          │
                                          │ LLM summarization │
                                          └───────────────────┘
```

### Data Model: InvestigationMemory

Each memory is a discrete learning — a sentence to a short paragraph — with metadata for retrieval and lifecycle management.

**Fields:**

| Field | Type | Purpose |
|-------|------|---------|
| `memory_id` | string | Unique identifier |
| `project` | string | Security boundary (hard filter on every query) |
| `content` | text | The learning itself |
| `category` | enum: `semantic`, `episodic`, `procedural` | Memory type |
| `valence` | enum: `positive`, `negative`, `neutral` | Pattern quality signal |
| `confidence` | float [0, 1] | Trust score (derived from investigation score, refined by review) |
| `seen_count` | int | Reinforcement counter |
| `source_session_id` | FK | Investigation that produced this memory |
| `alert_type` | string (optional) | Soft boost signal for retrieval |
| `chain_id` | string (optional) | Soft boost signal for retrieval |
| `embedding` | vector(768) | pgvector column for cosine similarity search |
| `search_vector` | tsvector (generated) | Full-text search column for hybrid keyword matching (`'simple'` config) |
| `deprecated` | bool | Excluded from retrieval when true |
| `last_seen_at` | timestamp | Last reinforcement time |

**Indexes:** project (security boundary), project + deprecated (active memories), source_session_id, category, HNSW on embedding column, GIN on search_vector.

**Edges:** Source session (one-to-many), injected-into sessions (many-to-many via join table).

### Memory Categories

- **semantic** — Facts about infrastructure, services, alert patterns, or environment behavior. Example: "The payments-api service connects to PostgreSQL via PgBouncer — connection timeout alerts should check PgBouncer health first."
- **episodic** — Specific investigation experiences: what worked, what failed, what was surprising. Example: "For OOMKill in order-processor, `container_memory_working_set_bytes` was more reliable than `container_memory_usage_bytes` because the latter includes cache."
- **procedural** — Investigation strategies or anti-patterns applicable across investigations. Example: "For certificate expiry alerts, always check both the ingress certificate and the backend service certificate."

### Memory Valence

- **positive** — Pattern that worked well, should be repeated
- **negative** — Mistake or dead end to avoid
- **neutral** — Factual observation with no clear positive/negative implication

### Extraction Pipeline (Reflector)

After the ScoringController completes its 2-turn conversation, the ScoringExecutor runs a separate Reflector LLM conversation for memory extraction.

```text
ScoringExecutor.runScoring()
│
├─ investigationContext := buildScoringContext()     ← reused, no redundant DB queries
├─ scoringResult := scoringController.Run()         ← 2-turn scoring conversation
│
├─ existingMemories := memoryService.FindSimilar()  ← pgvector search for dedup context
│
├─ reflectorResult := reflector.Run()               ← separate LLM conversation
│     System: "memory extraction specialist" role
│     User: investigation context + scoring results + existing memories + output schema
│     Response: JSON with create/reinforce/deprecate actions
│
└─ memoryService.ApplyReflectorActions()            ← persist to DB with embeddings
```

**Why a separate conversation:** The scoring system prompt defines a "quality evaluator" role. The Reflector needs a "memory extraction specialist" role. A fresh conversation gives each a clean, purpose-built prompt without role conflict.

**Reflector output actions:**
- **CREATE** — genuinely new knowledge; executor generates embedding and assigns initial confidence of 0.7
- **REINFORCE** — existing memory confirmed; `confidence = min(confidence × 1.1, 1.0)`, `seen_count++`
- **DEPRECATE** — existing memory contradicted; sets `deprecated = true`

**Extraction boundaries in the Reflector prompt:**
- Tool limitations, bugs, and workarounds do NOT belong in memories (belong in tool reports/skills)
- Domain-generic procedures do NOT belong in memories (belong in skills/runbooks)
- Speculative findings must not be extracted (only confirmed learnings)
- Skill content already loaded by the agent must not be duplicated
- Aim for 0-3 new memories per investigation; empty arrays are the expected outcome for routine investigations

**Quality guidelines:**
- Extract only learnings that would concretely help a future investigation
- Ground every learning in specific evidence from the investigation
- Prefer specific and actionable over vague and general
- Negative learnings from mistakes are especially valuable

### Prerequisite: Required Skills in Timeline

At agent execution start, a `skill_loaded` timeline event is emitted for each required skill. This makes skills visible to the scoring evaluator, the Reflector, and the UI — flowing through `buildScoringContext()` automatically without special handling.

### Embedding Configuration

Configurable via `defaults.memory` in `tarsy.yaml`. Built-in default uses Google `gemini-embedding-2-preview` at 768 dimensions with `GOOGLE_API_KEY`.

```yaml
defaults:
  memory:
    enabled: true
    max_inject: 5                              # max memories auto-injected (default: 5)
    reflector_memory_limit: 20                 # max existing memories shown to Reflector (default: 20)
    embedding:
      provider: "google"                       # google | openai
      model: "gemini-embedding-2-preview"
      api_key_env: "GOOGLE_API_KEY"
      dimensions: 768                          # must match pgvector column
```

Embedding calls are direct HTTP from Go (not via the Python LLM service) — embedding is a simple stateless operation. The `Embedder` interface dispatches to the correct API format based on `provider`. Google's API supports `taskType` (RETRIEVAL_DOCUMENT vs RETRIEVAL_QUERY) for optimized embeddings; OpenAI does not have an equivalent.

**Dimension consistency:** The system validates at startup that configured dimensions match the pgvector column size. Changing dimensions requires re-embedding all stored memories.

### Memory Scoping

Memory scoping has two distinct layers:

- **Security scope (hard filter — always enforced):** `project` is inherited from the source session. Every query includes `WHERE project = $current_project`. This is a tenant isolation boundary, not an investigation concern. Pre-authorization, all memories use `"default"`. When session authorization lands, memories inherit the real project — designed from the start to avoid schema migration later.
- **Investigation scope (soft boost — never hard-filters):** `alert_type` and `chain_id` provide minor ranking boosts. Infrastructure-specific context (service names, clusters, regions) lives in the memory *content* and is matched via semantic search. This keeps the schema infrastructure-agnostic.

### Memory Retrieval (Auto-Injection)

At investigation start, the session executor retrieves the top `max_inject` memories using hybrid search (vector + keyword) with Reciprocal Rank Fusion (RRF).

**Retrieval query shape (hybrid search):**

The query uses four CTEs: (0) convert query terms to an OR-joined tsquery, (1) vector candidates via pgvector HNSW with a similarity threshold `(1 - distance) >= 0.45`, (2) keyword candidates via tsvector/GIN with `'simple'` config, (3) RRF fusion (k=60). The outer query applies confidence weighting, temporal decay, and scope boosts:

```text
final_score = rrf_score
  × (0.7 + 0.3 × confidence)
  × EXP(-0.0077 × age_in_days)
  + CASE WHEN alert_type = $alert_type THEN 0.05 ELSE 0 END
  + CASE WHEN chain_id  = $chain_id  THEN 0.03 ELSE 0 END
```

- **Similarity threshold (0.45):** Candidates below the floor are discarded. Better to return nothing than noise.
- **Confidence weighting:** Human-reviewed memories rank higher at similar semantic distances.
- **Temporal decay (90-day half-life):** Unreinforced memories fade naturally. Reinforcement via the Reflector resets the clock (`updated_at`).
- **Hybrid search (OR keywords):** Any individual query term can surface a keyword match, preventing extra terms from shifting the embedding away from a valid match.

Project is the hard security filter. `alert_type` and `chain_id` are minor soft boosts.

**Prompt injection (Tier 4):** After Tier 3 (custom instructions), a "Lessons from Past Investigations" section is appended with category/valence tags:

```text
## Lessons from Past Investigations

- [procedural, positive] For API latency alerts on this service, check DB
  connection pool before upstream dependencies
- [semantic, neutral] Normal error rate for batch-processor during 2-4am is ~200/hr
- [procedural, negative] AVOID: querying metric container_memory_rss — doesn't
  exist in this monitoring setup, use container_memory_working_set_bytes
```

### `recall_past_investigations` Tool

A pseudo-MCP tool that wraps hybrid memory search. Accepts a free-text `query` and optional `limit` (default: 10, max: 20). Uses the same retrieval query shape as auto-injection, excluding already-injected memory IDs. Results include similarity scores so the agent can gauge relevance.

**Description:** "Search distilled knowledge from past investigations — reusable patterns, procedures, environment quirks, and anti-patterns. Use for situational questions like 'what do we know about this type of workload?', 'how should I handle this category of alert?', or 'what are common false positives here?'. Returns generalized learnings, NOT specific investigation history — will not find particular users, namespaces, or session details."

### `search_past_sessions` Tool

A pseudo-MCP tool for entity-level recall: searches `alert_sessions` by keywords in `alert_data` using PostgreSQL full-text search with AND logic (all query terms must be present). Addresses the structural entity-recall gap — agents can now check "have we investigated this user/namespace/workload before?"

**Parameters:** `query` (string, required), `alert_type` (string, optional), `days_back` (integer, optional, default 30), `limit` (integer, optional, default 5, max 10).

**Execution flow:** tsvector query finds matching completed sessions → session data (alert_data, final_analysis, quality_rating, investigation_feedback) bundled into a single LLM summarization call → focused digest returned to the agent. The summarization LLM uses the same model as the current agent. On LLM failure, returns an error (no fallback to raw data).

**Database:** `search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED` with GIN index. The `'simple'` config preserves identifiers without stemming.

**Description:** "Search past investigation sessions by keywords in alert data. Use to check if a specific entity (e.g. user, namespace, workload, IP, service) was investigated before — critical for escalation and pattern-of-behavior decisions. Pass specific identifiers as the query — one or a few key terms per call. For unrelated identifiers, make separate calls. Do NOT pass natural language sentences."

Both tools are registered for investigation and chat sessions. Chat sessions do not get Tier 4 auto-injection — the agent explicitly decides when to use memory/session search tools. Memory extraction does not happen for chat sessions.

### Human Review Refinement

When a reviewer completes their review (`PATCH /api/v1/sessions/review` with `action: complete`), two refinement paths trigger:

**Part 1 — Inline confidence adjustment (synchronous):**

| `quality_rating` | Action | Example: initial 0.8 |
|---|---|---|
| `accurate` | `min(confidence × 1.2, 1.0)` | → 0.96 |
| `partially_accurate` | `confidence × 0.6` | → 0.48 |
| `inaccurate` | `deprecated = true` | → excluded from retrieval |

**Part 2 — Background feedback Reflector (async, when `investigation_feedback` is non-empty):**

A Reflector variant sees the feedback text, `quality_rating`, existing session memories, and alert context. It can create, deprecate, or reinforce memories. All feedback-derived memories get **0.9 initial confidence** — human-written feedback is the strongest signal.

This is the primary mechanism for learning from mistakes: `inaccurate` reviews deprecate wrong memories (Part 1) while feedback text produces new corrective memories (Part 2).

### Confidence Model

Confidence is a pure human/reinforcement signal. All auto-extracted memories start at a flat `0.7` regardless of investigation score — the Reflector is the quality gate at extraction time. Valence tags each memory as a pattern to repeat (`positive`), avoid (`negative`), or note (`neutral`).

**Initial confidence:** `0.7` (flat — the Reflector already filters low-quality findings).
**Reinforcement:** `min(confidence × 1.1, 1.0)`.
**Feedback-derived memories:** 0.9 initial confidence.
**Ranking multiplier:** `(0.7 + 0.3 × confidence)` — confidence impact scales with semantic relevance.
**Temporal decay:** `EXP(-0.0077 × age_in_days)` based on `updated_at` (90-day half-life). Reinforcement resets the clock. Applied at retrieval, not stored.

### Observability

- **Reflector LLM call:** Tracked via `LLMInteraction` record with `InteractionTypeMemoryExtraction`. Standard timeline events (thinking, response) produced automatically via the streaming pipeline. Errors logged as warnings — extraction never blocks scoring.
- **Embedding API calls:** Structured logging with model, dimensions, latency, error status. Embedding failures skip the individual memory; others proceed.
- **Progress status:** "Memorizing..." stage event via WebSocket before the Reflector call.
- **Feedback Reflector:** Same tracking pattern — `LLMInteraction` record + streaming timeline events on the original session's timeline.

### Memory API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/sessions/:id/memories` | Memories extracted from this session |
| `GET` | `/api/v1/sessions/:id/injected-memories` | Memories auto-injected into this session |
| `GET` | `/api/v1/memories` | List all memories (paginated, filterable) |
| `GET` | `/api/v1/memories/:id` | Single memory detail |
| `PATCH` | `/api/v1/memories/:id` | Edit memory (content, category, valence, deprecated) |
| `DELETE` | `/api/v1/memories/:id` | Delete memory |

Responses include all memory fields except the raw embedding vector. List endpoints support filtering by `category`, `valence`, `deprecated`, `source_session_id`.

### Frontend: Session Detail

Two new sections on the session detail page:

1. **"Injected Memories" card** — shown above the timeline, lists memories auto-injected into this investigation's prompt. Only visible when memory was active.
2. **"Extracted Learnings" card** — shown near the final analysis, lists memories the Reflector extracted with category/valence badges. Shows "No new learnings" if nothing novel was found.

Both are read-only in v1.

## Future Considerations

- **Dedicated memory management page** — aggregate view of all memories across investigations, with search, filtering, and bulk operations
- **Batch embedding** — Google's `batchEmbedContents` endpoint for reducing round trips when extraction volume grows
- **Per-chain memory enable/disable** — trivial to add if some chains shouldn't participate in memory
- **Per-memory human feedback** — more granular than session-level signals, if needed
- **Memory promotion** — promoting high-confidence memories to permanent skills or custom_instructions
- **`search_past_sessions` scope expansion** — include `final_analysis` in the tsvector column if agents need to search investigation conclusions
