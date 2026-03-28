# Memory Recall Improvements

**Status:** Design complete — all decisions finalized in [memory-recall-improvements-questions.md](memory-recall-improvements-questions.md)  
**Builds on:** [ADR-0014: Investigation Memory](../adr/0014-investigation-memory.md)

## Problem

Memory recall (`recall_past_investigations` tool and Tier 4 auto-injection) returns results that are frequently irrelevant to the agent's actual question. The core symptom: the system **always returns top-k results regardless of similarity**, presenting noise as signal.

Observed in production:

```
recall_past_investigations({
  "query": "suspicious user john smith new account first activation"
})
→ "Found 10 relevant memories" — all generic procedural patterns,
   none about this user or account
```

The agent asked an entity-level question; the system answered with pattern-level noise. This wastes tokens, can mislead reasoning, and dilutes attention.

### Root Causes

1. **No similarity threshold** — always returns `limit` results even when the best match is semantically distant. No way to say "nothing relevant found."
2. **No quality signal in output** — results say "Found N relevant memories" without similarity scores. The agent cannot self-assess relevance.
3. **Confidence not used in ranking** — human-reviewed high-confidence memories rank identically to low-confidence ones at the same cosine distance.
4. **No recency signal** — old memories about deprecated infrastructure compete equally with recent ones.
5. **Stale embeddings on update** — manual content edits via API don't refresh the embedding vector.
6. **Structural entity-recall gap** — memories are generalized patterns (by design); the system cannot answer "have we investigated this user before?"

## Design Principles

1. **Minimal invasive changes.** Improve ranking and filtering within the existing retrieval architecture. No new storage backends, no new services.
2. **Better to return nothing than noise.** An empty result is a clear signal. Ten irrelevant results actively harm investigation quality.
3. **Transparency over magic.** Show the agent similarity scores so it can reason about relevance — don't hide the ranking behind "Found N relevant memories."
4. **Confidence is earned.** Human review and reinforcement should have a visible effect on which memories surface.

## Changes

### 1. Similarity Threshold (filter low-quality matches)

Add a minimum similarity floor to `FindSimilarWithBoosts`. Candidates below the threshold are discarded before the final `LIMIT`.

**Approach:** Fixed constant in code (e.g., `(1 - distance) >= 0.45`), reviewed when the embedding model changes — same lifecycle as pgvector column dimensions. Not configurable — aligns with ADR-0014's "zero manual tuning" principle.

**Where:** `pkg/memory/service.go` — `FindSimilarWithBoosts` SQL, add `WHERE (1 - distance) >= $threshold` in outer query.

**Effect:** `recall_past_investigations` returns "No relevant memories found" when nothing meets the bar instead of always returning top-k noise.

**Applies to both retrieval paths:** The same threshold is used for the `recall_past_investigations` tool AND Tier 4 auto-injection (`pkg/queue/executor_memory.go`). If no memories meet the bar, the agent starts without a "Lessons from Past Investigations" section — this is fine, the agent operated without memories before this feature existed.

Also applies to `FindSimilar` (used by the Reflector for dedup context), though the Reflector threshold may differ — the Reflector benefits from seeing broadly similar memories even at lower relevance.

### 2. Similarity Scores in Tool Output

Include the match score in the recall tool's formatted output so the agent can gauge relevance.

**Current format:**
```
1. [procedural, positive, learned 1 day ago] For repeat investigations...
```

**New format:**
```
1. [procedural, positive, score: 0.82, learned 1 day ago] For repeat investigations...
```

**Where:** `pkg/memory/tool_executor.go` — `executeRecall` formatting. Requires `FindSimilarWithBoosts` to return similarity scores (currently only returned internally in the CTE but not exposed to callers).

**Schema change:** Add a `Score` field to the `Memory` struct, populated during retrieval.

### 3. Confidence Boost in Ranking

Incorporate memory confidence into the re-ranking formula using a **multiplicative factor** so that battle-tested memories rank higher than uncertain ones at similar cosine distances.

**Ranking formula:** `(1 - distance) * (0.7 + 0.3 * confidence)` — confidence impact scales with semantic relevance. Human reviews matter most for highly relevant memories.

**Flat initial confidence:** All auto-extracted memories start at confidence `0.7` regardless of investigation score. The current `initialConfidence(score int)` function is replaced by a constant. The Reflector is trusted as the quality gate at extraction time — it already sees the full quality evaluation when deciding what to extract. Confidence becomes a pure human/reinforcement signal:

| State | Confidence | Multiplier |
|---|---|---|
| Unreviewed (baseline) | 0.70 | 0.91 |
| Reinforced 3x | 0.93 | 0.98 |
| Human: accurate | 0.84 | 0.95 |
| Human: partially_accurate | 0.42 | 0.83 |
| Human: inaccurate | deprecated | gone |
| Feedback-created | 0.90 | 0.97 |

**Code impact:**
- `pkg/memory/service.go`: `initialConfidence(score int)` becomes `const initialConfidence = 0.7`
- `pkg/memory/service.go`: outer `ORDER BY` in `FindSimilarWithBoosts` uses the multiplicative formula
- `pkg/queue/scoring_executor.go`: drop `score` parameter from `ApplyReflectorActions` call

### 4. Recency Signal in Ranking — Temporal Decay

Apply a time-based decay multiplier so memories that haven't been reinforced gradually fade in ranking. Combined with Reflector reinforcement, this creates a self-correcting "use it or lose it" mechanism: relevant patterns get reinforced during new investigations (resetting the decay clock), while stale patterns fade naturally without needing explicit deprecation.

**Formula:** `EXP(-0.0077 * age_in_days)` where `age_in_days` is computed from `updated_at` (not `created_at`). This gives a **90-day half-life** — a memory not reinforced for 90 days has its ranking score halved.

| Days since last reinforcement | Decay multiplier |
|---|---|
| 0 (just created/reinforced) | 1.00 |
| 30 days | 0.79 |
| 60 days | 0.63 |
| 90 days (half-life) | 0.50 |
| 180 days | 0.25 |

**Why `updated_at`:** Reinforcement updates `updated_at`, automatically resetting the decay clock. A memory reinforced yesterday is fresh regardless of when it was created. Human review also updates `updated_at`, so reviewed memories get a reset.

**Why this works as a system:** The Reflector already reinforces memories it agrees with. So a pattern that's still appearing in investigations stays fresh. A pattern that stopped appearing fades. If a rare pattern recurs after decay, the first investigation might not benefit from the old memory, but the Reflector will reinforce it (or create a new one) — the system self-corrects within one investigation cycle.

**Implementation:** One additional multiplier in the ranking `ORDER BY` — no schema changes, no background jobs. The decay constant `0.0077` (= `ln(2) / 90`) is a code constant, same lifecycle as the similarity threshold.

**Where:** `pkg/memory/service.go` — `FindSimilarWithBoosts` and `FindSimilar` `ORDER BY` clauses.

### 5. Embedding Refresh on Content Update

When `Update` changes a memory's `content`, regenerate the embedding so the vector stays consistent with the text.

**Where:** `pkg/memory/service.go` — `Update` method. When `input.Content != nil`, call `s.embedder.Embed(ctx, *input.Content, EmbeddingTaskDocument)` and write the new vector via raw SQL (Ent doesn't manage the embedding column).

This is a correctness fix, not a design question.

### 6. Entity-Level Recall — `search_past_sessions` Tool

Add a new `search_past_sessions` tool that searches `alert_sessions` using PostgreSQL full-text search (`tsvector`). This addresses the structural gap: agents can now ask "have we investigated this user/namespace/workload before?"

**Database migration:**
```sql
ALTER TABLE alert_sessions ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED;
CREATE INDEX idx_sessions_search ON alert_sessions USING gin(search_vector);
```

The `'simple'` configuration is critical — it preserves identifiers without stemming (usernames, namespace names stay exact). The `GENERATED ALWAYS` column auto-syncs with `alert_data` — no application-level maintenance.

**Search scope: `alert_data` only**, not `final_analysis`. Entity identifiers originate from the alert payload — this is where usernames, namespaces, and workload names live. Including `final_analysis` would increase false positive matches (any investigation that mentions a common term in passing). If agents need to search investigation conclusions, the scope can be expanded later by updating the generated column expression.

**Search behavior:** Uses `plainto_tsquery('simple', query)` — **AND logic**: all query terms must be present in the same session's `alert_data`. AND is the safe default because returning nothing for a too-broad query is better than returning noisy false positives that get fed to the summarization LLM and potentially mislead the agent. The tool description (change #7) explicitly instructs the agent to pass short, identifier-focused queries and make separate calls for unrelated entities.

**Tool definition:**
- Name: `search_past_sessions`
- Parameters:
  - `query` (string, required) — specific identifiers to search for. The tool description explicitly tells the agent: short identifier terms, not natural language.
  - `alert_type` (string, optional) — filter by alert type
  - `days_back` (integer, optional, default 30) — how far back to search
  - `limit` (integer, optional, default 5, max 10) — max sessions to return
- Returns: LLM-summarized digest of matching sessions (see below)

**Execution flow:**

1. `tsvector` query finds matching completed sessions (status = `completed`, ordered by `created_at DESC`, limit applied)
2. For each session, collect: `alert_data`, `alert_type`, `final_analysis`, `quality_rating`, `investigation_feedback`, `created_at` (score is intentionally excluded — the number without its eval report is misleading; human feedback is the quality signal)
3. Bundle ALL sessions into a **single LLM call** with the original search query
4. The summarization LLM produces a focused digest in the context of the search query — preserving entity identifiers, conclusions, human corrections, and flagging when a match is about a different entity than the one queried
5. Return the LLM's output directly to the calling agent

**Why LLM summarization instead of raw data:**
Executive summaries strip entity identifiers — a summary like "alert resolved, no action needed" doesn't say which entity it was about. If the session matched on keyword but involved a different entity with a similar pattern, returning the raw executive summary would mislead the agent. The summarization LLM has access to the full `final_analysis` + `alert_data` and can produce an entity-aware, query-relevant response.

**LLM call details:**
- Uses the **same model as the current agent** — same provider, same retry/fallback infrastructure. Follows the existing tool summarization pattern for simplicity.
- **Single call** regardless of how many sessions match (1–10). All sessions are batched into one prompt.
- **No fallback to raw data.** If the summarization LLM call fails after retries/provider fallback, return an error: "Unable to retrieve session history — summarization failed." Returning unsummarized data risks misleading the agent (the exact problem this solves).

**Where:** New tool registration in `pkg/memory/tool_executor.go` (or a new file), SQL query and summarization call in `pkg/memory/service.go`.

### 7. Improved Tool Descriptions

**This is critical for correct tool usage.** The agent has two memory/history tools. If the descriptions are vague or overlapping, the agent will misuse them (the exact problem that motivated this design). Each description must clearly state: what data it searches, what kind of queries it's good for, and what it will NOT find.

**`recall_past_investigations`:**
> "Search distilled knowledge from past investigations — reusable patterns, procedures, environment quirks, and anti-patterns. Use for situational questions like 'what do we know about this type of workload?', 'how should I handle this category of alert?', or 'what are common false positives here?'. Returns generalized learnings, NOT specific investigation history — will not find particular users, namespaces, or session details."

**`search_past_sessions`:**
> "Search past investigation sessions by keywords in alert data. Use to check if a specific entity (user, namespace, workload, IP) was investigated before — critical for escalation and pattern-of-behavior decisions. Pass specific identifiers as the query — one or a few key terms per call. For unrelated identifiers, make separate calls. Do NOT pass natural language sentences. Returns a focused summary of matching investigations including conclusions, quality assessments, and human review corrections.
>
> Good queries: 'john-doe' (single identifier), 'john-doe my-namespace' (both must match — narrows results), 'nginx-proxy', 'coolify'
> Bad queries: 'check if user john-doe was investigated before in my-namespace' (natural language — every word must match, will return nothing)"

**Why the descriptions are this specific:** The `search_past_sessions` tool uses AND keyword matching (change #6) — all query terms must appear in the same session. If the agent passes a natural language query like "user john might be abusing qemu-kvm", the search requires ALL tokens (`user`, `john`, `might`, `be`, `abusing`, `qemu`, `kvm`) present in the same alert, returning nothing. The description must steer the agent toward short, identifier-focused queries ("john" or "qemu-kvm") to get useful results.

### 8. Hybrid Search for Memory Recall

Upgrade `recall_past_investigations` from pure vector search to hybrid search (vector + keyword) using Reciprocal Rank Fusion (RRF). Fixes the production failure where querying "quantiaia coolify VM" failed to find the Coolify memory — extra query terms shifted the embedding away from "coolify", but keyword search catches it.

**Database migration:**
```sql
ALTER TABLE investigation_memories ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED;
CREATE INDEX idx_memories_search ON investigation_memories USING gin(search_vector);
```

**Query change in `FindSimilarWithBoosts`:** Two inner CTEs fetch candidates — one by vector similarity, one by keyword match — then merge using RRF before applying the confidence multiplier (from change #3) and scope boosts in the final `ORDER BY`.

Pseudo-SQL (actual implementation will use `ROW_NUMBER()` for positional ranks):

```sql
WITH vector_candidates AS (
  SELECT id, (1 - embedding <=> $query_vec) AS similarity,
         ROW_NUMBER() OVER (ORDER BY embedding <=> $query_vec) AS pos
  FROM investigation_memories
  WHERE project = $1 AND deprecated = false AND embedding IS NOT NULL
    AND (1 - embedding <=> $query_vec) >= $threshold
  ORDER BY similarity DESC LIMIT $candidate_pool
),
keyword_candidates AS (
  SELECT id, ts_rank(search_vector, plainto_tsquery('simple', $query_terms)) AS kw_rank,
         ROW_NUMBER() OVER (ORDER BY ts_rank(search_vector, plainto_tsquery('simple', $query_terms)) DESC) AS pos
  FROM investigation_memories
  WHERE project = $1 AND deprecated = false
    AND search_vector @@ plainto_tsquery('simple', $query_terms)
  LIMIT $candidate_pool
),
fused AS (
  SELECT COALESCE(v.id, k.id) AS id,
    1.0 / (60 + COALESCE(v.pos, $candidate_pool)) +
    1.0 / (60 + COALESCE(k.pos, $candidate_pool)) AS rrf_score
  FROM vector_candidates v FULL OUTER JOIN keyword_candidates k ON v.id = k.id
)
SELECT m.*, f.rrf_score
FROM fused f JOIN investigation_memories m ON f.id = m.id
ORDER BY f.rrf_score
  * (0.7 + 0.3 * m.confidence)
  * EXP(-0.0077 * EXTRACT(EPOCH FROM (NOW() - m.updated_at)) / 86400.0)
  + CASE WHEN m.alert_type = $alert_type THEN 0.05 ELSE 0 END
  + CASE WHEN m.chain_id = $chain_id THEN 0.03 ELSE 0 END
  DESC
LIMIT $limit
```

**Where:** `pkg/memory/service.go` — `FindSimilarWithBoosts`.

### 9. Reflector Extraction Selectivity

Better retrieval can't fix a noisy store. Production data reveals the dominant noise source: **tool-bug and tool-workaround memories** that surface for every query in the domain, consuming retrieval slots. These belong in skills/instructions, not memories.

**Changes to `reflectorSystemPrompt`** in `pkg/memory/reflector.go`:

**a) Insert new `## Extraction Boundaries` section** between the existing "Memory Valence" and "Quality Guidelines" sections:

```
## Extraction Boundaries

The following do NOT belong in memories and must NOT be extracted:

- **Tool limitations, bugs, and workarounds.** If a tool returned an error, timed out, or
  required a non-obvious workaround, that is a tooling issue — not a learning for future
  investigations. These belong in tool improvement reports, skills, or runbook updates.
- **Domain-generic procedures.** Investigation steps that apply to every alert of a given type
  (e.g., "always check logs first") belong in skills or runbooks, not memories. Memories are
  for specific, surprising, or non-obvious findings.
- **Speculative findings.** Only extract learnings confirmed by tool output or concrete
  evidence from the investigation. Do not extract guesses, hypotheses, or unverified theories.
- **Skill content.** If a learning is already covered by the agent's skills (visible in the
  investigation timeline as pre-loaded or dynamically loaded via load_skill), do not extract
  it — the agent already knows it.

Aim for **0–3 new memories** per investigation. Most investigations confirm existing knowledge
rather than produce new learnings — reinforcing existing memories or returning empty arrays is
the expected outcome, not the exception. Exceeding 3 creates should be rare and justified by
a genuinely rich investigation. When in doubt, reinforce an existing memory rather than
creating a near-duplicate.

If there is nothing to create, reinforce, or deprecate, return the JSON structure with all
empty arrays: {"create": [], "reinforce": [], "deprecate": []}. This is the correct response
for routine investigations — do not invent learnings to fill the output.
```

**b) Remove the last bullet from `## Quality Guidelines`** (the "routine investigation → empty arrays" point is now covered by the soft cap in Extraction Boundaries) and **remove the skill-duplication bullet** (moved to Extraction Boundaries). The remaining Quality Guidelines become:

```
## Quality Guidelines

- Extract only learnings that would **concretely help** a future investigation. Ask: "If an
  agent saw this memory before investigating a similar alert, would it change what it does?"
- Ground every learning in **specific evidence** from the investigation — tool call results,
  agent reasoning, or scoring critique. Do not extract generic SRE knowledge the agent already
  has.
- Prefer **specific and actionable** over vague and general. "Check PgBouncer health before
  blaming the database" is better than "Consider all components in the request path."
- Negative learnings from mistakes are especially valuable — they prevent repeating errors.
```

**Format safety note:** The JSON output schema (`reflectorOutputSchema`) is already in the user prompt, not the system prompt — format interference risk is naturally low. The new "Extraction Boundaries" section is clearly separated from both the category definitions above it and the quality criteria below it.

**Also update `feedbackReflectorSystemPrompt`** with an analogous Extraction Boundaries section before its existing Guidelines. The wording should be slightly softer since human feedback is the strongest signal, but the same three categories (tool bugs, domain-generic procedures, speculative findings) should be excluded. The soft cap can be omitted for the feedback variant — human review typically produces fewer, higher-quality learnings.

## Affected Components

| Component | Change |
|-----------|--------|
| `pkg/memory/service.go` | Threshold filter, confidence boost, flat initial confidence, temporal decay, score in return type, embedding refresh, hybrid search (RRF), session search query |
| `pkg/memory/types.go` | Add `Score` field to `Memory` struct |
| `pkg/memory/tool_executor.go` | Score in output format, updated tool descriptions, new `search_past_sessions` tool |
| `pkg/memory/reflector.go` | Tighter extraction criteria in Reflector prompt |
| `pkg/queue/executor_memory.go` | Pass threshold to auto-injection retrieval |
| `pkg/queue/scoring_executor.go` | Drop `score` parameter from `ApplyReflectorActions` call |
| Database migration | `tsvector` + GIN index on `alert_sessions.alert_data`, `tsvector` + GIN index on `investigation_memories.content` |
| Tests | Update existing tests for new ranking behavior, add threshold tests, hybrid search tests, session search tests |
| `docs/adr/0014-investigation-memory.md` | Update retrieval query shape, confidence model, Reflector guidelines, new tool |

## Implementation Plan

Four phases, each a separate PR. Ordered by dependency chain and risk: lowest-risk/highest-immediate-impact first.

### Phase 1 — Reflector Extraction Selectivity (change 9) - DONE

Prompt-only change. Reduces noise at the source before we improve retrieval.

| File | What changes |
|------|-------------|
| `pkg/memory/reflector.go` | Insert `## Extraction Boundaries` section into `reflectorSystemPrompt`, trim `## Quality Guidelines` (see change #9 for exact text) |
| `pkg/memory/reflector.go` | Analogous Extraction Boundaries for `feedbackReflectorSystemPrompt` |

No code logic changes, no migrations. Prompt assertion tests and e2e golden fixtures updated to match the new prompt text. Verify by running `go test ./pkg/memory/... -run Reflector` and the e2e golden-fixture suite, plus inspecting Reflector output on subsequent investigations.

### Phase 2 — Ranking & Filtering Overhaul (changes 1, 2, 3, 4, 5)

All modifications to `FindSimilarWithBoosts` in one PR — threshold, confidence, decay, score exposure. Also includes the embedding refresh fix and flat initial confidence.

| File | What changes |
|------|-------------|
| `pkg/memory/service.go` | `FindSimilarWithBoosts`: add `WHERE (1 - distance) >= $threshold`, confidence multiplier `(0.7 + 0.3 * confidence)`, temporal decay `EXP(...)` in `ORDER BY`, return similarity score to callers |
| `pkg/memory/service.go` | `FindSimilar`: add temporal decay to `ORDER BY` |
| `pkg/memory/service.go` | `initialConfidence(score int)` → `const initialConfidence = 0.7` |
| `pkg/memory/service.go` | `Update`: regenerate embedding when content changes |
| `pkg/memory/types.go` | Add `Score` field to `Memory` struct |
| `pkg/memory/tool_executor.go` | Include score in `executeRecall` output format, update `recall_past_investigations` description |
| `pkg/queue/executor_memory.go` | Pass similarity threshold to auto-injection retrieval |
| `pkg/queue/scoring_executor.go` | Drop `score` parameter from `ApplyReflectorActions` call |
| Tests | Update existing ranking/recall tests, add threshold edge cases (all above threshold, none above, mixed) |

No migrations. Biggest behavioral change — test with production-like memory sets before merge.

### Phase 3 — Hybrid Search (change 8)

Replaces pure vector search with vector + keyword RRF. Depends on phase 2 (the query it extends).

| File | What changes |
|------|-------------|
| DB migration | `tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED` + GIN index on `investigation_memories` |
| `pkg/memory/service.go` | `FindSimilarWithBoosts`: replace single-CTE query with dual-CTE (vector + keyword) + RRF fusion (see change #8 pseudo-SQL) |
| Tests | Hybrid search tests: keyword-only match, vector-only match, both match (RRF boost), query with extra terms that shift embedding |

### Phase 4 — Entity-Level Recall (changes 6, 7)

New `search_past_sessions` tool with LLM summarization. Finalize both tool descriptions.

| File | What changes |
|------|-------------|
| DB migration | `tsvector GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED` + GIN index on `alert_sessions` |
| `pkg/memory/tool_executor.go` | Register `search_past_sessions` tool, finalize `search_past_sessions` description (see change #7) |
| `pkg/memory/service.go` | Session search query (`plainto_tsquery` + AND logic, completed sessions only, ordered by `created_at DESC`) |
| `pkg/memory/service.go` (or new file) | LLM summarization: build prompt from matched sessions, single call using current agent model, error on failure |
| Tests | Session search tests: single-term match, multi-term AND, no matches, LLM summarization mock, failure behavior |

After phase 4: update `docs/adr/0014-investigation-memory.md` to reflect the new retrieval query shape, confidence model, Reflector guidelines, and both tools.

## Out of Scope

- New storage backends or vector databases
- Memory management UI (dedicated page)
- Bulk memory operations
- Changes to memory categories or valence system
