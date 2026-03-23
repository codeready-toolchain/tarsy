# Investigation Memory — Detailed Design

**Status:** Draft — pending decisions from [investigation-memory-design-questions.md](investigation-memory-design-questions.md)
**Sketch:** [investigation-memory-sketch.md](investigation-memory-sketch.md) (all sketch-level decisions finalized)
**Prerequisites:** [ADR-0013: Review Feedback Redesign](../adr/0013-review-feedback-redesign.md) (implemented)

## Overview

This document turns the [investigation memory sketch](investigation-memory-sketch.md) into an implementation-ready design. The sketch established *what* to build and key architectural decisions (Q1-Q9). This document covers *how* to build it: exact schemas, package structure, prompt templates, API contracts, and implementation phases.

## Design Principles

1. **Memory is a light touch.** Complementary hints for specific/repeating situations, not a playbook. The LLM's investigative creativity is preserved.
2. **Semantic-first retrieval.** pgvector cosine similarity drives ranking. Investigation scope metadata (alert_type, service) is a soft boost, never a hard filter. Project is the only hard filter (security boundary).
3. **Zero manual tuning.** No thresholds, no fallback levels, no scoring weights to calibrate. The 3-5 auto-inject cap is the noise control.
4. **Embedded extraction.** Memory extraction is a third turn in the existing scoring stage — no new infrastructure, near-zero marginal cost.
5. **In-prompt dedup.** The Reflector sees existing memories and decides what to create, reinforce, or deprecate in one pass.
6. **Future-proof for multi-tenancy.** `project` field from day one, hard-filtered on every query.

## Architecture

### New Packages

```text
pkg/memory/                     # Memory domain logic
├── service.go                  # MemoryService — CRUD, retrieval, refinement
├── embedder.go                 # Embedding generation (calls embedding API)
├── retriever.go                # Semantic-first retrieval (pgvector queries)
└── reflector.go                # Reflector prompt builder + response parser

ent/schema/
└── investigationmemory.go      # New Ent schema
```

### Ent Schema: `InvestigationMemory`

```go
// Fields
field.String("id").StorageKey("memory_id").Unique().Immutable()
field.String("project").NotEmpty().Default("default")
field.Text("content").NotEmpty()
field.Enum("category").Values("semantic", "episodic", "procedural")
field.Enum("valence").Values("positive", "negative", "neutral")
field.Float("confidence").Default(0.5).Min(0).Max(1)
field.Int("seen_count").Default(1).NonNegative()
field.String("source_session_id").NotEmpty()

// Scope metadata (soft signals, not hard filters)
field.String("alert_type").Optional().Nillable()
field.String("chain_id").Optional().Nillable()
field.String("service").Optional().Nillable()
field.String("cluster").Optional().Nillable()

// Embedding — stored as pgvector vector type
// Requires custom SQL migration (Ent doesn't natively support pgvector)
// Column: embedding vector(DIMENSIONS)    ← dimension depends on model

// Lifecycle
field.Time("created_at").Default(time.Now).Immutable()
field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now)
field.Time("last_seen_at").Default(time.Now)
field.Bool("deprecated").Default(false)

// Edges
edge.From("source_session", AlertSession.Type).
    Ref("memories").Field("source_session_id").Unique().Required()

// Indexes
index.Fields("project")                          // Security boundary
index.Fields("project", "deprecated")            // Active memories per project
index.Fields("source_session_id")                // Memories from a session
index.Fields("category")                         // For dashboard grouping
```

The `embedding` column is a pgvector `vector(N)` type added via a raw SQL migration (Ent doesn't support custom column types). An HNSW index on the embedding column enables approximate nearest-neighbor search.

> **Open question:** Embedding model and dimensions — see [questions document](investigation-memory-design-questions.md), Q1.

### Embedding Pipeline

Memory content needs to be converted to vector embeddings for pgvector similarity search. This requires an embedding model accessible from the Go backend.

> **Open question:** How to generate embeddings — see [questions document](investigation-memory-design-questions.md), Q2.

### pgvector Setup

A migration enables the pgvector extension and creates the embedding column + index:

```sql
-- Enable pgvector
CREATE EXTENSION IF NOT EXISTS vector;

-- Add embedding column (after Ent creates the table)
ALTER TABLE investigation_memories
    ADD COLUMN embedding vector(DIMENSIONS);

-- HNSW index for approximate nearest-neighbor search
CREATE INDEX idx_investigation_memories_embedding
    ON investigation_memories
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
```

> **Open question:** pgvector index strategy — see [questions document](investigation-memory-design-questions.md), Q3.

### Scoring Stage: Third Turn (Reflector)

The existing `ScoringController` (2 turns: score + tool report) gains a third turn for memory extraction.

**Flow:**

```text
Turn 1: Score evaluation → score (0-100) + analysis + failure_tags
Turn 2: Tool improvement report → tool_improvement_report
Turn 3: Memory extraction (Reflector) → structured memory entries
```

**Reflector prompt context:**
- The scoring analysis and failure tags (from turn 1)
- The tool improvement report (from turn 2)
- Existing relevant memories from the same project (fetched by pgvector similarity before the turn)
- Alert metadata (type, service, chain, cluster)

**Reflector prompt structure:**

```text
Based on your scoring analysis above, extract discrete learnings that should be
remembered for future investigations of similar alerts.

You have access to existing memories that were previously extracted:

<existing_memories>
{JSON array of existing memories with id, content, category, valence, confidence, seen_count}
</existing_memories>

For each learning, decide:
- CREATE: genuinely new knowledge not covered by existing memories
- REINFORCE: an existing memory is confirmed — return its ID with updated seen_count
- DEPRECATE: an existing memory is contradicted or outdated — return its ID

Alert context:
- Alert type: {alert_type}
- Service: {service}
- Chain: {chain_id}
- Cluster: {cluster}

Respond with a JSON object:
{reflector_output_schema}
```

**Reflector output schema:**

```json
{
  "create": [
    {
      "content": "string — the learning in 1-2 sentences",
      "category": "semantic | episodic | procedural",
      "valence": "positive | negative | neutral"
    }
  ],
  "reinforce": [
    {
      "memory_id": "string — ID of existing memory to reinforce"
    }
  ],
  "deprecate": [
    {
      "memory_id": "string — ID of existing memory to deprecate",
      "reason": "string — why this memory is no longer valid"
    }
  ]
}
```

> **Open question:** How to handle Reflector parse failures — see [questions document](investigation-memory-design-questions.md), Q4.

### Changes to `ScoringController`

```go
// After turn 2 (tool improvement report):

// Fetch existing memories for dedup context
existingMemories, err := memoryService.RetrieveForReflector(ctx, projectID, alertContext)

// Turn 3: Memory extraction
reflectorPrompt := execCtx.PromptBuilder.BuildScoringMemoryExtractionPrompt(
    existingMemories, alertMetadata,
)
messages = append(messages,
    agent.ConversationMessage{Role: agent.RoleAssistant, Content: toolReportResp.Text},
    agent.ConversationMessage{Role: agent.RoleUser, Content: reflectorPrompt},
)
reflectorResp, err := c.scoringCallLLM(ctx, execCtx, messages, "memory_extraction")

// Parse and persist
memoryOps, err := memory.ParseReflectorOutput(reflectorResp.Text)
if err != nil {
    // Log warning, don't fail scoring — memory extraction is best-effort
    logger.Warn("failed to parse memory extraction", "error", err)
} else {
    memoryService.ApplyReflectorOps(ctx, projectID, sessionID, alertMetadata, memoryOps)
}
```

The `ScoringResult` struct gains a `MemoryExtractionRaw` field to store the raw Reflector output for debugging/audit.

### Memory Retrieval (Auto-Injection)

At investigation start, the session executor retrieves the top N memories and passes them to the prompt builder.

**Changes to `ExecutionContext`:**

```go
type ExecutionContext struct {
    // ... existing fields ...

    // MemoryBriefing: pre-rendered memory hints for Tier 4 injection.
    // nil when no relevant memories exist or memory is disabled.
    MemoryBriefing *MemoryBriefing
}

type MemoryBriefing struct {
    Memories    []MemoryHint  // Top N memories for auto-injection
    InjectedIDs []string      // IDs of injected memories (excluded from tool results)
}

type MemoryHint struct {
    ID       string
    Content  string
    Category string
    Valence  string
}
```

**Changes to `PromptBuilder.ComposeInstructions`:**

After Tier 3 (custom instructions), append Tier 4 if `MemoryBriefing` is non-nil:

```text
## Lessons from Past Investigations

The following are learnings from previous investigations of similar alerts.
Consider them as hints — they may or may not apply to your current investigation.
Do not treat them as rules.

- [procedural, positive] For API latency alerts on this service, check DB
  connection pool before upstream dependencies
- [semantic, neutral] Normal error rate for batch-processor during 2-4am is ~200/hr
- [procedural, negative] AVOID: querying metric container_memory_rss — doesn't
  exist in this monitoring setup, use container_memory_working_set_bytes
```

### `recall_past_investigations` Tool

A pseudo-MCP tool (like `load_skill`) that wraps `MemoryService.Search`.

**Tool definition:**

```json
{
  "name": "recall_past_investigations",
  "description": "Search memories from past investigations. Use when the auto-injected hints suggest there might be more relevant context, or when investigating a pattern you've seen before.",
  "parameters": {
    "query": {
      "type": "string",
      "description": "What you want to recall — describe the situation, pattern, or question"
    },
    "limit": {
      "type": "integer",
      "description": "Max results to return (default: 10, max: 20)",
      "default": 10
    }
  }
}
```

**Implementation:** New `MemoryToolExecutor` wrapping the inner executor (same pattern as `SkillToolExecutor`). On `recall_past_investigations` calls, it embeds the query, runs pgvector similarity search within the project boundary (excluding already-injected IDs), and returns formatted results.

> **Open question:** Should the tool support structured filters (category, valence) or just free-text query? — see [questions document](investigation-memory-design-questions.md), Q5.

### Human Review Refinement

When a human completes their review (via `PATCH /api/v1/sessions/review` with `action: complete`), the review handler triggers memory refinement based on `quality_rating`:

- **`accurate`** → boost confidence of all memories from this session by a fixed delta (e.g., +0.15, capped at 1.0)
- **`inaccurate`** → reduce confidence by a fixed delta (e.g., -0.3). If confidence drops below a threshold (e.g., 0.1), mark as deprecated. Memories with `positive` valence may be flipped to `negative`.
- **`partially_accurate`** → minor adjustment (e.g., +0.05)
- **`investigation_feedback`** text → stored as context but not automatically processed in v1. Future: re-run Reflector with feedback for targeted updates.

> **Open question:** How to trigger refinement — inline in review handler vs background job — see [questions document](investigation-memory-design-questions.md), Q6.

### Memory API

New endpoints for memory management:

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/sessions/:id/memories` | Memories extracted from this session |
| `GET` | `/api/v1/sessions/:id/injected-memories` | Memories that were auto-injected into this session |
| `GET` | `/api/v1/memories` | List all memories (paginated, filterable) |
| `GET` | `/api/v1/memories/:id` | Single memory detail |
| `PATCH` | `/api/v1/memories/:id` | Edit memory (content, category, valence, deprecated) |
| `DELETE` | `/api/v1/memories/:id` | Delete memory |

Responses include all memory fields except the raw embedding vector. List endpoints support filtering by `category`, `valence`, `deprecated`, `source_session_id`.

> **Open question:** Whether to include a bulk management endpoint — see [questions document](investigation-memory-design-questions.md), Q7.

### Frontend: Session Detail

Two new sections on the session detail page:

1. **"Injected Memories" card** — shown above the timeline, after the alert card. Lists the 3-5 memories that were auto-injected into this investigation's prompt. Only visible for sessions where memory was active.

2. **"Extracted Learnings" card** — shown near the final analysis card (after scoring). Lists memories that the Reflector extracted from this investigation, with category/valence badges. Shows "No new learnings" if the Reflector found nothing novel.

Both are read-only in v1. Editing/deletion is API-only.

### Confidence Model

Initial confidence is derived from the investigation's score:

| Score range | Initial confidence | Rationale |
|-------------|-------------------|-----------|
| 80-100 | 0.8 | High-quality investigation — high trust in extracted patterns |
| 60-79 | 0.6 | Good investigation — moderate trust |
| 40-59 | 0.4 | Average — lower trust, may need reinforcement |
| 0-39 | 0.3 | Poor investigation — extracted anti-patterns at low confidence |

Human review adjustments (additive, clamped to [0, 1]):
- `accurate`: +0.15
- `partially_accurate`: +0.05
- `inaccurate`: -0.3

Reinforcement (when Reflector outputs `reinforce`): +0.1, capped at 1.0.

**Decay:** Not implemented in v1. Memories stay at their current confidence indefinitely. Decay can be added later as a periodic job if the memory store grows too large.

> **Open question:** Whether to implement decay in v1 or defer — see [questions document](investigation-memory-design-questions.md), Q8.

### Tracking Injected Memories

To show "which memories were injected" on the session detail page and to exclude them from tool results, we need to record which memory IDs were injected at investigation start.

> **Open question:** How to store injected memory IDs — see [questions document](investigation-memory-design-questions.md), Q9.

## Implementation Phases

### Phase 1: Schema + Extraction (core pipeline)

1. Enable pgvector extension (migration)
2. `InvestigationMemory` Ent schema + migration (including raw SQL for embedding column + HNSW index)
3. `pkg/memory/` package: `MemoryService` with Create, Retrieve, Update
4. Embedding pipeline (generate embeddings for memory content)
5. Reflector prompt + parser in `pkg/memory/reflector.go`
6. Third turn in `ScoringController`
7. `ScoringResult` extended with memory extraction output

**Result:** Memories are extracted and stored after every scored investigation.

### Phase 2: Retrieval + Injection (agent uses memory)

1. `MemoryRetriever` — pgvector similarity search within project
2. `MemoryBriefing` on `ExecutionContext`
3. Tier 4 section in `ComposeInstructions`
4. `MemoryToolExecutor` wrapping inner executor
5. `recall_past_investigations` tool registration
6. Record injected memory IDs per session

**Result:** Investigations benefit from past learnings. Chat sessions can recall past investigations.

### Phase 3: Human Refinement + API + Dashboard

1. Memory confidence adjustment on review completion
2. Memory CRUD API endpoints
3. Frontend: "Injected Memories" card on session detail
4. Frontend: "Extracted Learnings" card on session detail
5. Memory list/detail in API (admin usage)

**Result:** Full feedback loop — extract → inject → refine from human review. Visible in dashboard.

## Open Questions Summary

| # | Question | Section affected |
|---|----------|-----------------|
| Q1 | Embedding model and dimensions | Schema, embedding pipeline |
| Q2 | How to generate embeddings | Embedding pipeline, architecture |
| Q3 | pgvector index strategy | Migration, retrieval performance |
| Q4 | Reflector parse failure handling | Scoring controller |
| Q5 | recall_past_investigations tool parameters | Tool definition |
| Q6 | Review refinement trigger mechanism | Review handler, memory service |
| Q7 | Bulk memory management API | API design |
| Q8 | Memory decay in v1 | Confidence model, memory lifecycle |
| Q9 | Storing injected memory IDs | Schema, session detail |
