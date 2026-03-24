# Investigation Memory — Design Questions

**Status:** All questions decided
**Related:** [Design document](investigation-memory-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Embedding model, provider, and configuration

Individual memory entries (sentences to short paragraphs, extracted by the Reflector from full investigations) need to be embedded as vectors for pgvector similarity search. The embedding model only sees these extracted entries and query text — never the raw investigation timeline. TARSy already uses Google Gemini as its default LLM provider with `GOOGLE_API_KEY` configured in every deployment. OpenClaw (a reference project) uses the same Gemini embedding API with direct HTTP calls — the same pattern proposed here.

The design needs to answer three coupled questions: which provider/model to use by default, how to make it configurable, and how the Go backend calls the embedding API.

### Available embedding models

| Provider | Model | Dimensions | Status | Notes |
|----------|-------|-----------|--------|-------|
| Google | `text-embedding-004` | 768 | Stable | Proven, used by OpenClaw, same `GOOGLE_API_KEY` |
| Google | `gemini-embedding-2-preview` | 3072 (configurable: 768/1536/3072) | Preview (Mar 2026) | Latest, multimodal, requires `dimensions` param for smaller vectors |
| OpenAI | `text-embedding-3-small` | 1536 (configurable down to 256) | Stable | High quality, requires separate `OPENAI_API_KEY` |
| OpenAI | `text-embedding-3-large` | 3072 (configurable down to 256) | Stable | Highest quality OpenAI, more expensive |

### Configuration approach

A `memory` block under `defaults` in `tarsy.yaml`, following the same pattern as `defaults.scoring`:

```yaml
# tarsy.yaml — memory configuration (override built-in default)
defaults:
  memory:
    enabled: true                              # false to disable memory entirely
    embedding:
      provider: "google"                       # google | openai (determines API format)
      model: "gemini-embedding-2-preview"      # model name sent to the provider API
      api_key_env: "GOOGLE_API_KEY"            # env var holding the API key
      dimensions: 768                          # output dimensions (must match pgvector column)
      # base_url: ""                           # optional custom endpoint (provider default if omitted)
```

**Built-in default** (used when `defaults.memory.embedding` is not set in `tarsy.yaml`):

```go
// pkg/config/defaults.go
func defaultEmbeddingConfig() EmbeddingConfig {
    return EmbeddingConfig{
        Provider:   EmbeddingProviderGoogle,
        Model:      "gemini-embedding-2-preview",
        APIKeyEnv:  "GOOGLE_API_KEY",
        Dimensions: 768, // reduced from model's 3072 default — right-sized for memory content
    }
}
```

**Zero-config path:** If `GOOGLE_API_KEY` is set (already required for Gemini LLM), memory embedding works out of the box with no additional configuration. The built-in default uses Google `gemini-embedding-2-preview` at 768 dimensions.

**Switching to a different model/provider** — examples:

```yaml
# Example 1: Use Google's previous stable model
defaults:
  memory:
    embedding:
      provider: "google"
      model: "text-embedding-004"
      api_key_env: "GOOGLE_API_KEY"
      dimensions: 768                          # text-embedding-004 native dimension

# Example 2: Use OpenAI embeddings
defaults:
  memory:
    embedding:
      provider: "openai"
      model: "text-embedding-3-small"
      api_key_env: "OPENAI_API_KEY"
      dimensions: 768                          # reduce from 1536 default (must match pgvector column)

# Example 3: Use OpenAI via a proxy/custom endpoint
defaults:
  memory:
    embedding:
      provider: "openai"
      model: "text-embedding-3-small"
      api_key_env: "OPENAI_API_KEY"
      dimensions: 768
      base_url: "https://my-proxy.example.com/v1"

# Example 4: Disable memory entirely
defaults:
  memory:
    enabled: false
```

### Go types

```go
// pkg/config/enums.go
type EmbeddingProviderType string

const (
    EmbeddingProviderGoogle EmbeddingProviderType = "google"
    EmbeddingProviderOpenAI EmbeddingProviderType = "openai"
)

// pkg/config/types.go
type MemoryConfig struct {
    Enabled   bool            `yaml:"enabled"`
    Embedding EmbeddingConfig `yaml:"embedding,omitempty"`
}

type EmbeddingConfig struct {
    Provider   EmbeddingProviderType `yaml:"provider,omitempty"`
    Model      string                `yaml:"model,omitempty"`
    APIKeyEnv  string                `yaml:"api_key_env,omitempty"`
    Dimensions int                   `yaml:"dimensions,omitempty"` // 0 = model default
    BaseURL    string                `yaml:"base_url,omitempty"`
}
```

### Embedding API calls (direct HTTP from Go)

The Go backend calls the provider's embedding API directly — a single HTTP POST per embedding. No Python service involvement. Embedding calls are infrequent: a few per investigation (extraction) and one per investigation (query).

**Resilience:** The `Embedder` implementation wraps each HTTP call with `context.WithTimeout` (30s default — embedding is fast, long waits indicate a problem). On transient failures (5xx, network errors, 429), retry once after a jittered backoff (same pattern as `mcp.Client.CallTool`). On 429, respect the `Retry-After` header if present. No circuit breaker — embedding calls are too infrequent (single-digit per investigation) to trip or benefit from one, and TARSy doesn't use circuit breakers anywhere else. Embedding failures are best-effort: the individual memory is skipped, other memories proceed (see [Observability section](investigation-memory-design.md#observability-tracking-memory-extraction-calls)).

**Google API format** (`text-embedding-004`, `gemini-embedding-2-preview`):

```text
POST https://generativelanguage.googleapis.com/v1beta/models/{model}:embedContent
Header: x-goog-api-key: {api_key}
Body: {
  "content": { "parts": [{ "text": "memory content" }] },
  "taskType": "RETRIEVAL_DOCUMENT"              // or "RETRIEVAL_QUERY" for search queries
}
Response: { "embedding": { "values": [0.12, -0.45, ...] } }
```

The `taskType` parameter tells the model to optimize the embedding for storage (`RETRIEVAL_DOCUMENT`) vs. search (`RETRIEVAL_QUERY`). Google's models use this to produce better search results.

**OpenAI API format** (`text-embedding-3-small`, `text-embedding-3-large`):

```text
POST https://api.openai.com/v1/embeddings
Header: Authorization: Bearer {api_key}
Body: {
  "model": "text-embedding-3-small",
  "input": "memory content",
  "dimensions": 512
}
Response: { "data": [{ "embedding": [0.12, -0.45, ...] }] }
```

The `Embedder` interface in Go dispatches to the correct API format based on `provider`:

```go
// pkg/memory/embedder.go
type Embedder interface {
    Embed(ctx context.Context, text string, task EmbeddingTask) ([]float32, error)
}

type EmbeddingTask string
const (
    EmbeddingTaskDocument EmbeddingTask = "document" // storing a memory
    EmbeddingTaskQuery    EmbeddingTask = "query"    // searching for memories
)
```

### Why direct HTTP and not the Python LLM service

- Embedding is a simple, stateless operation — one HTTP POST, parse JSON, extract float array
- No streaming, no multi-turn, no tool calling — none of the complexity that justifies the gRPC service
- The Python service only has a `Generate` RPC; adding `Embed` would require protobuf changes, Python implementation, and deployment coordination
- OpenClaw uses the same pattern: direct HTTP calls to the embedding API from the application layer

### Decision: Google `gemini-embedding-2-preview` at 768 dimensions

MTEB English #1 (68.32 overall; 67.99 at 768 dims — negligible loss). Better semantic discrimination for TARSy's technical SRE domain than the previous-generation `text-embedding-004`. Same `GOOGLE_API_KEY`, consistent with TARSy already running preview-track LLMs. Zero-config setup; switching to `text-embedding-004` or OpenAI is a single config change.

**Important constraint:** Changing dimensions after memories are stored requires re-embedding all existing memories (pgvector column size is fixed). The system validates dimension consistency at startup.

---

## Q2: What pgvector index strategy?

pgvector supports two approximate nearest-neighbor index types. The choice affects query speed, build time, and memory usage.

### Decision: HNSW (Hierarchical Navigable Small World)

Best recall and query speed for small-to-medium datasets, no training phase (always up-to-date after inserts), pgvector's recommended default. Memory overhead is negligible for TARSy's expected dataset size (low thousands). Including the index from the start avoids a migration under load later.

Migration creates the index with `m = 16, ef_construction = 64` using `vector_cosine_ops`.

---

## Q3: How should Reflector parse failures be handled?

The Reflector's LLM call asks the model to output structured JSON. LLMs sometimes produce malformed JSON, extra text around the JSON, or completely ignore the schema.

### Decision: Lenient parsing + silent fallback (C + A)

First try strict `encoding/json` unmarshal. If that fails, strip markdown fences and extract the first `{`...`}` by bracket depth (same pattern OpenClaw uses in `qmd-query-parser.ts`). If that still fails, log a warning and proceed with "no memories" — memory extraction is best-effort and must never block scoring. No LLM retry. The lenient parser can be shared with any future structured-output parsing.

---

## Q4: Should `recall_past_investigations` support structured filters?

The tool lets the agent search for memories beyond the auto-injected set. The question is whether it accepts just a free-text query or also structured filters.

### Decision: Free-text query only (Option A)

Single `query` parameter + optional `limit` (default 10, max 20). Semantic search handles intent better than the LLM constructing filter parameters. Keeps the tool interface simple — one natural language query, the system does the rest. Structured filters (category, valence, alert_type) can be added as optional parameters later if agents consistently need them.

---

## Q5: How should memory refinement trigger on human review?

When a reviewer completes their review (`quality_rating` + optional `investigation_feedback`), memories from that session need their confidence adjusted. The question is when and how.

### Decision: Hybrid — inline confidence + background feedback Reflector (Option C, revised)

**Two-part refinement on every review completion:**

**Part 1 — Inline (in review handler, synchronous):** Confidence adjustment based on `quality_rating`. Simple SQL, negligible latency.

| `quality_rating` | Existing memories | Mechanism |
|---|---|---|
| `accurate` | `confidence = min(confidence × 1.2, 1.0)` | Multiplicative boost |
| `partially_accurate` | `confidence = confidence × 0.6` | Multiplicative degradation — human says "partly wrong" |
| `inaccurate` | `deprecated = true` | Kill switch — investigation conclusions were wrong |

Human review has higher authority than automated score. Multiplicative adjustment means high-confidence memories get proportionally larger absolute changes.

**Part 2 — Background job (async, when `investigation_feedback` text is non-empty):** Enqueue a refinement job (same queue infrastructure as scoring). The job runs a Reflector variant that sees:
- The `investigation_feedback` text
- The `quality_rating`
- Existing memories from the session (including newly deprecated ones)
- Alert context metadata

The Reflector can create new memories, deprecate specific existing ones, or reinforce ones the human confirmed. **All feedback-derived memories get 0.9 initial confidence** — human-written feedback is the strongest signal, regardless of `quality_rating`.

This is where learning from mistakes happens: `inaccurate` + feedback "TARSy mistook X for Y" → existing wrong memories deprecated (Part 1) + new `negative`/`procedural` memory created at 0.9 (Part 2). Feedback from `partially_accurate` reviews is equally valuable — the human explains what was right vs. wrong, producing the most nuanced memories.

---

## Q6: Should the API include a bulk memory management endpoint?

The design includes per-memory CRUD endpoints. The question is whether to also add bulk operations.

### Decision: No bulk endpoint in v1 (Option A)

Per-memory CRUD only. Memory store will be small early on. Bulk endpoints can be added when there's demonstrated need.

---

## Q7: Should memory decay be implemented in v1?

The sketch describes decay: memories not reinforced within a configurable window lose confidence. The question is whether to build this now.

### Decision: No decay in v1 (Option A)

Memories keep confidence indefinitely. The Reflector handles explicit deprecation of outdated memories. Early on, memories should accumulate. Even OpenClaw ships temporal decay disabled by default.

**Future enhancement:** If the memory store grows large enough to need pruning, add a **query-time decay multiplier** (not stored data mutation). Apply `score × e^(-λ × age_in_days)` at retrieval time with a configurable half-life. This is cleaner than periodic jobs — no data mutation, reversible via config, no infrastructure. OpenClaw implements this pattern (`temporal-decay.ts`) with a 30-day half-life and evergreen exemptions.

---

## Q8: How should injected memory IDs be stored per session?

To show "which memories were injected into this investigation" on the session detail page and to exclude them from recall tool results, we need to record which memory IDs were selected at investigation start.

### Decision: Join table via Ent edge (Option B)

Many-to-many Ent edge between `AlertSession` and `InvestigationMemory`:

```go
// alertsession.go
edge.To("injected_memories", InvestigationMemory.Type)

// investigationmemory.go
edge.From("injected_into_sessions", AlertSession.Type).Ref("injected_memories")
```

Ent auto-generates the join table, queries, and mutations. Both directions are natural queries: `session.QueryInjectedMemories()` and `memory.QueryInjectedIntoSessions()`. Enables memory usage analytics without JSON operators. Minimal schema overhead (~4 lines of edge definitions).
