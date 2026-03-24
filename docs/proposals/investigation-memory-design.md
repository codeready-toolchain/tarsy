# Investigation Memory — Detailed Design

**Status:** All design questions decided — see [investigation-memory-design-questions.md](investigation-memory-design-questions.md)
**Sketch:** [investigation-memory-sketch.md](investigation-memory-sketch.md) (all sketch-level decisions finalized)
**Prerequisites:** [ADR-0013: Review Feedback Redesign](../adr/0013-review-feedback-redesign.md) (implemented)

## Overview

This document turns the [investigation memory sketch](investigation-memory-sketch.md) into an implementation-ready design. The sketch established *what* to build and key architectural decisions (Q1-Q9). This document covers *how* to build it: exact schemas, package structure, prompt templates, API contracts, and implementation phases.

## Design Principles

1. **Memory is a light touch.** Complementary hints for specific/repeating situations, not a playbook. The LLM's investigative creativity is preserved.
2. **Semantic-first retrieval.** pgvector cosine similarity drives ranking. Investigation scope metadata (`alert_type`, `chain_id`) is a soft boost, never a hard filter. Infrastructure-specific context (service names, clusters, regions) lives in the memory content itself and is matched via semantic search. Project is the only hard filter (security boundary).
3. **Zero manual tuning.** No thresholds, no fallback levels, no scoring weights to calibrate. The 3-5 auto-inject cap is the noise control.
4. **Embedded extraction.** Memory extraction runs as a separate LLM call within the scoring stage — triggered by the `ScoringExecutor` after the scoring controller completes. No new infrastructure, near-zero marginal cost.
5. **In-prompt dedup.** The Reflector sees existing memories and decides what to create, reinforce, or deprecate in one pass.
6. **Future-proof for multi-tenancy.** `project` field from day one, hard-filtered on every query.

## Architecture

### New Packages

```text
pkg/memory/                     # Memory domain logic
├── service.go                  # MemoryService — CRUD, retrieval, refinement
├── embedder.go                 # Embedding generation (calls embedding API)
├── retriever.go                # Semantic-first retrieval (pgvector queries)
├── reflector.go                # Reflector prompt builder + response parser (extraction + feedback variants)
├── parser.go                   # Lenient JSON parser (shared with future structured-output parsing)
└── tool_executor.go            # MemoryToolExecutor — recall_past_investigations tool

pkg/queue/
└── feedback_executor.go        # Background job: feedback Reflector (triggered on review with feedback text)

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

// Scope metadata (soft signals, not hard filters — TARSy-native fields only)
field.String("alert_type").Optional().Nillable()
field.String("chain_id").Optional().Nillable()

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
edge.From("injected_into_sessions", AlertSession.Type).
    Ref("injected_memories")

// Indexes
index.Fields("project")                          // Security boundary
index.Fields("project", "deprecated")            // Active memories per project
index.Fields("source_session_id")                // Memories from a session
index.Fields("category")                         // For dashboard grouping
```

The `embedding` column is a pgvector `vector(N)` type added via a raw SQL migration (Ent doesn't support custom column types). An HNSW index on the embedding column enables approximate nearest-neighbor search.

### Embedding Configuration

Embedding is configurable via `defaults.memory` in `tarsy.yaml`. The built-in default uses Google `gemini-embedding-2-preview` at 768 dimensions with `GOOGLE_API_KEY` — zero additional configuration required since every TARSy deployment already has this key.

**Default model: `gemini-embedding-2-preview` at 768 dimensions.** This is Google's top embedding model (#1 on MTEB English leaderboard as of March 2026). At 768 dims it retains near-full quality (67.99 vs 68.32 at full 3072 dims) while keeping storage practical. The model's native 3072-dim output is reduced via the `dimensions` parameter, which is set in the built-in default. Consistent with TARSy already running preview-track LLM models.

**Config structure:**

```yaml
# tarsy.yaml
defaults:
  memory:
    enabled: true
    embedding:
      provider: "google"                       # google | openai
      model: "gemini-embedding-2-preview"      # model name
      api_key_env: "GOOGLE_API_KEY"            # env var for API key
      dimensions: 768                          # output dimensions (must match pgvector column)
      # base_url: ""                           # optional custom endpoint
```

**Go types:**

```go
type MemoryConfig struct {
    Enabled   bool            `yaml:"enabled"`
    Embedding EmbeddingConfig `yaml:"embedding,omitempty"`
}

type EmbeddingConfig struct {
    Provider   EmbeddingProviderType `yaml:"provider,omitempty"`
    Model      string                `yaml:"model,omitempty"`
    APIKeyEnv  string                `yaml:"api_key_env,omitempty"`
    Dimensions int                   `yaml:"dimensions,omitempty"`
    BaseURL    string                `yaml:"base_url,omitempty"`
}
```

**Built-in default** (when `defaults.memory.embedding` is not set):

```go
func defaultEmbeddingConfig() EmbeddingConfig {
    return EmbeddingConfig{
        Provider:   EmbeddingProviderGoogle,
        Model:      "gemini-embedding-2-preview",
        APIKeyEnv:  "GOOGLE_API_KEY",
        Dimensions: 768,
    }
}
```

**Embedding pipeline:** The Go backend calls the provider's embedding API directly via HTTP — one POST per embedding. No Python service involvement (embedding is a simple stateless operation, unlike the multi-turn LLM calls that justify the gRPC service). The `Embedder` interface dispatches to the correct API format based on `provider`:

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

Google's API maps `EmbeddingTaskDocument` → `taskType: "RETRIEVAL_DOCUMENT"` and `EmbeddingTaskQuery` → `taskType: "RETRIEVAL_QUERY"`, optimizing embeddings for storage vs. search. OpenAI's API doesn't have an equivalent parameter.

**Switching models:** Change the `embedding` block in `tarsy.yaml`. Dimensions must match the existing pgvector column — changing dimensions requires re-embedding all stored memories. The system validates dimension consistency at startup (compares configured dimensions against the pgvector column size and fails with a clear error if mismatched).

### pgvector Setup

A migration enables the pgvector extension and creates the embedding column + index:

```sql
-- Enable pgvector
CREATE EXTENSION IF NOT EXISTS vector;

-- Add embedding column (after Ent creates the table)
-- 768 = gemini-embedding-2-preview reduced from 3072 default
ALTER TABLE investigation_memories
    ADD COLUMN embedding vector(768);

-- HNSW index for approximate nearest-neighbor search
CREATE INDEX idx_investigation_memories_embedding
    ON investigation_memories
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
```

**Index strategy:** HNSW with `m = 16, ef_construction = 64` using `vector_cosine_ops`. Always up-to-date on insert, best recall for TARSy's expected dataset size. See [Q2 decision](investigation-memory-design-questions.md#q2-what-pgvector-index-strategy).

### Prerequisite: Required Skills in Timeline

Currently, required skills (`required_skills` in agent config) are injected into the agent's system prompt at Tier 2.5 but are invisible to anything that reads the investigation timeline — the scoring evaluator, the Reflector, and the UI.

**Change:** At agent execution start (before the first LLM call), emit a `skill_loaded` timeline event for each required skill. The event content includes the full skill text (name + body). On-demand skills loaded via `load_skill` already appear as tool call events.

**Benefits beyond memory:**

| Consumer | Before | After |
|---|---|---|
| **UI timeline** | No visibility into agent's domain knowledge | Skills shown at the start of each agent's timeline |
| **Scoring evaluator** | Can't assess whether agent properly applied skill knowledge (e.g., classification criteria) | Sees exactly what domain knowledge was provided — can evaluate skill application quality |
| **Reflector** | Needs special handling to avoid duplicating skill content | Skills flow through `buildScoringContext()` automatically — no extra code |

**Implementation:** New `timelineevent.EventTypeSkillLoaded` enum value. `formatTimelineEvents()` in `investigation_formatter.go` formats it as `**Pre-loaded Skill: {name}**` followed by the skill content. Emitted by the iterating/single-shot controllers at execution start.

**Token cost:** Negligible — skills are already in the system prompt consuming tokens. This just makes the same content visible in the timeline. For the scoring context specifically, skills add ~1000-2000 tokens to a timeline that's typically 10,000-50,000+ tokens.

### Scoring Stage: Reflector (Separate LLM Call)

The existing `ScoringController` runs two turns (score + tool report). After scoring completes, the `ScoringExecutor` runs a **separate** Reflector LLM conversation for memory extraction.

**Why a separate conversation (not a third turn in scoring):**

The scoring system prompt defines the role as "You are an expert investigation quality evaluator." The Reflector needs a fundamentally different role — a memory extraction specialist. Continuing the scoring conversation would create a role conflict in the system prompt and pollute the context with scoring-specific instructions. A fresh conversation gives the Reflector a clean, purpose-built prompt.

**How the scoring conversation works today (code reference: `ScoringController.Run()`):**

The scoring controller manages a single `messages []ConversationMessage` array. Each turn appends to it. The conversation starts fresh — not a continuation of the investigation LLM conversation — but `ScoringExecutor.buildScoringContext()` reconstructs the full investigation from timeline events in the database.

```text
Scoring conversation (unchanged)
════════════════════════════════
messages[0] = System:    BuildScoringSystemPrompt()
                         → "You are an expert investigation quality evaluator for TARSy..."

messages[1] = User:      BuildScoringInitialPrompt(investigationContext, scoringOutputSchema)
                         where investigationContext = ScoringExecutor.buildScoringContext():
                           § ORIGINAL ALERT         — session.AlertData (raw alert payload)
                           § RUNBOOK                — resolved runbook content (if configured)
                           § AVAILABLE TOOLS/AGENT  — MCP tool lists per agent execution
                           § INVESTIGATION TIMELINE — full timeline from DB, formatted by
                             FormatStructuredInvestigation(), which includes per-agent:
                               • Pre-loaded Skills    (SkillLoaded events — domain knowledge)
                               • Internal Reasoning   (LlmThinking events — the "thoughts")
                               • Agent Response       (LlmResponse events)
                               • Tool Call + Result   (LlmToolCall events with args & output)
                               • Summarized Results   (McpToolSummary events)
                               • Final Analysis       (FinalAnalysis events)
                               • Synthesis results (if parallel stage)
                               • Executive summary (if present)

→ Turn 1: LLM responds with score analysis + numeric score
  (if score extraction fails, retry messages are appended — up to 5 retries)

→ Turn 2: Tool improvement report prompt appended, LLM responds with report

→ ScoringController.Run() returns ScoringResult (score, analysis, tool report, failure tags)
```

**Reflector conversation (new, separate LLM call):**

After `ScoringController.Run()` returns, the `ScoringExecutor` runs the Reflector as a new conversation. The executor already has all the data — it threads the context explicitly into the Reflector's prompt.

```text
Reflector conversation (new, separate)
═══════════════════════════════════════
messages[0] = System:  BuildReflectorSystemPrompt()
                       → "You are a memory extraction specialist..."
                       (dedicated role — focused entirely on identifying learnings)

messages[1] = User:    BuildReflectorPrompt() assembles all context explicitly:

  ┌────────────────────────────────────────────────────────────────────────┐
  │ § INVESTIGATION CONTEXT                                                │
  │   The same investigationContext string from buildScoringContext().     │
  │   Already available in the executor — no redundant DB queries.         │
  │   Contains: raw alert, runbook, tools per agent, full timeline         │
  │   (thoughts, tool calls, results, final analysis, synthesis,           │
  │   executive summary).                                                  │
  │                                                                        │
  │ § SCORING RESULTS (from ScoringResult returned by the controller)      │
  │   - Score (0-100)                                                      │
  │   - Score analysis (what went well, what went wrong)                   │
  │   - Failure tags                                                       │
  │   - Tool improvement report                                            │
  │                                                                        │
  │ § EXISTING MEMORIES (fetched before this call)                         │
  │   pgvector similarity search filtered by project, ranked by cosine     │
  │   similarity to the investigation's final analysis.                    │
  │   JSON array: [{id, content, category, valence, confidence,            │
  │   seen_count}]                                                         │
  │                                                                        │
  │ § ALERT METADATA                                                       │
  │   - Alert type                                                         │
  │   - Chain ID                                                           │
  │                                                                        │
  │ § EXTRACTION INSTRUCTIONS + OUTPUT SCHEMA                              │
  │   (create / reinforce / deprecate actions)                             │
  └────────────────────────────────────────────────────────────────────────┘

→ LLM responds with: structured JSON (memory create/reinforce/deprecate actions)
```

**Data flow through the executor:**

```text
ScoringExecutor.runScoring()
│
├─ investigationContext := buildScoringContext(ctx, session)   ← DB queries happen here
│
├─ scoringResult := scoringAgent.Execute(ctx, execCtx, investigationContext)
│     └─ ScoringController.Run() — turns 1 & 2, returns ScoringResult
│
├─ existingMemories := memoryService.FindSimilar(ctx, project, finalAnalysis, limit)
│     └─ pgvector query filtered by project
│
└─ reflectorResult := reflector.Run(ctx, ReflectorInput{
│     InvestigationContext: investigationContext,   // reused, no extra DB call
│     ScoringResult:       scoringResult,           // score, analysis, tool report
│     ExistingMemories:    existingMemories,        // for dedup
│     AlertType:           session.AlertType,
│     ChainID:             session.ChainID,
│  })
│     └─ Builds fresh messages[0..1], calls LLM, parses JSON response
│
└─ memoryService.ApplyReflectorActions(ctx, reflectorResult)
      └─ Creates / reinforces / deprecates memories in DB
```

**Reflector system prompt (message 0):**

```text
You are a memory extraction specialist for TARSy, an automated incident investigation platform.

TARSy uses agent chains — multi-stage pipelines where AI agents investigate incidents by
calling external tools (MCP tools), analyzing evidence, and producing findings. Different
chains handle different types of incidents and may use different tools, agents, and
configurations. Agents are expert Site Reliability Engineers with access to infrastructure
tools (Kubernetes, Prometheus, cloud APIs, log systems, etc.).

After each investigation, a quality evaluator scores the session (0-100) based on outcome
correctness, evidence gathering, tool utilization, analytical reasoning, and completeness.
It also produces failure tags and a tool improvement report.

Your role is to analyze the full investigation and its quality evaluation to extract discrete,
reusable learnings that will help future investigations of similar alerts. You receive:
- The original alert and runbook
- The full investigation timeline, which includes:
  - Pre-loaded skills (domain knowledge injected into the agent's prompt before investigation)
  - Agent reasoning, tool calls with arguments and results, final analysis
- The quality score, analysis, failure tags, and tool improvement report
- Existing memories from past investigations (for deduplication)

## Memory Categories

Each learning falls into one category:

- **semantic** — Facts about infrastructure, services, alert patterns, or environment behavior.
  Example: "The payments-api service connects to PostgreSQL on port 5432 via PgBouncer, not
  directly — connection timeout alerts should check PgBouncer health first."

- **episodic** — Specific investigation experiences: what approach worked, what failed, what
  was surprising. Tied to a concrete event.
  Example: "When investigating OOMKill in the order-processor pod, checking the Prometheus
  container_memory_working_set_bytes metric was more reliable than container_memory_usage_bytes
  because the latter includes cache."

- **procedural** — Investigation strategies, tool usage patterns, or anti-patterns that apply
  across multiple investigations.
  Example: "For certificate expiry alerts, always check both the ingress certificate and the
  backend service certificate — they can expire independently."

## Memory Valence

- **positive** — A pattern that worked well and should be repeated.
- **negative** — A mistake, dead end, or anti-pattern to avoid in the future.
- **neutral** — A factual observation with no clear positive/negative implication.

## Quality Guidelines

- Extract only learnings that would **concretely help** a future investigation. Ask: "If an
  agent saw this memory before investigating a similar alert, would it change what it does?"
- Ground every learning in **specific evidence** from the investigation — tool call results,
  agent reasoning, or scoring critique. Do not extract generic SRE knowledge the agent already
  has.
- **Do not duplicate skill content.** The investigation timeline includes the agent's
  pre-loaded skills. If a learning is already covered by a skill (e.g., classification
  criteria, report format, environment facts), do not extract it — the agent already knows it.
- Prefer **specific and actionable** over vague and general. "Check PgBouncer health before
  blaming the database" is better than "Consider all components in the request path."
- Negative learnings from mistakes are especially valuable — they prevent repeating errors.
- If the investigation was routine and existing memories already cover the lessons, return
  empty arrays. Not every investigation produces new learnings.
```

**Reflector user prompt (message 1):**

```text
Below is a completed TARSy investigation and its quality evaluation. Extract discrete
learnings for future investigations.

## Investigation

<investigation_context>
{investigationContext — from buildScoringContext(): alert, runbook, tools, full timeline}
</investigation_context>

## Quality Evaluation

Score: {score}/100
Failure tags: {failureTags — comma-separated, or "none"}

<score_analysis>
{scoreAnalysis}
</score_analysis>

<tool_improvement_report>
{toolImprovementReport}
</tool_improvement_report>

## Existing Memories

These memories were previously extracted from past investigations in this project. Use them
to avoid creating duplicates and to decide what to reinforce or deprecate.

<existing_memories>
{JSON array: [{id, content, category, valence, confidence, seen_count}]}
</existing_memories>

## Your Task

For each learning you identify, choose an action:
- **CREATE**: Genuinely new knowledge not covered by existing memories.
- **REINFORCE**: An existing memory is confirmed by this investigation — return its ID.
- **DEPRECATE**: An existing memory is contradicted or proven outdated — return its ID with
  a reason.

Alert context for scoping:
- Alert type: {alert_type}
- Chain: {chain_id}

Respond with a JSON object (and nothing else):
{reflector_output_schema}
```

**Reflector output schema:**

```json
{
  "create": [
    {
      "content": "string — the learning (a sentence to a short paragraph)",
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

**Parse strategy:** Lenient parsing with silent fallback. Try strict JSON first, then strip markdown fences and extract JSON by bracket depth. If both fail, log a warning and proceed with "no memories" — extraction never blocks scoring. See [Q3 decision](investigation-memory-design-questions.md#q3-how-should-reflector-parse-failures-be-handled).

### Changes to `ScoringExecutor`

Memory extraction runs in the `ScoringExecutor` (not the `ScoringController`) — after the scoring controller returns, the executor triggers the Reflector as a separate LLM call.

```go
// In ScoringExecutor.runScoring(), after scoringAgent.Execute() returns:

// investigationContext is already available — built earlier for the scoring call
// scoringResult is the ScoringResult returned by ScoringController.Run()

// 1. Fetch existing memories for dedup context
existingMemories, err := memoryService.FindSimilar(ctx, projectID, finalAnalysis, reflectorMemoryLimit)
if err != nil {
    logger.Warn("failed to fetch existing memories for reflector", "error", err)
    existingMemories = nil // proceed without dedup context
}

// 2. Run Reflector as a separate LLM conversation
//    Skills are visible in the investigationContext because they're stored as
//    timeline events — no special skill resolution needed here.
reflectorResult, err := reflector.Run(ctx, reflector.Input{
    InvestigationContext: investigationContext,  // reused from buildScoringContext()
    ScoringResult:       scoringResult,          // score, analysis, tool report, failure tags
    ExistingMemories:    existingMemories,
    AlertType:           session.Edges.Alert.AlertType,
    ChainID:             session.ChainID,
    LLMClient:           e.llmClient,
    Config:              resolvedConfig,
})
if err != nil {
    logger.Warn("reflector failed", "error", err)
} else {
    memoryService.ApplyReflectorActions(ctx, projectID, sessionID, reflectorResult)
}
```

The `reflector.Run()` function builds a fresh 2-message conversation (system + user), calls the LLM, and parses the JSON response. It is completely independent of the scoring LLM conversation.

**Progress status:** The Reflector call emits a **"Memorizing..."** progress status visible in the UI/streaming output, distinguishing it from the scoring turns. Code-level names (`ScoringController`, `defaults.scoring`, etc.) remain unchanged — memory extraction is a natural extension of the scoring stage, not a separate stage.

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

**Tool parameters:** Free-text `query` only (+ optional `limit`). Semantic search handles intent; no structured filters in v1. See [Q4 decision](investigation-memory-design-questions.md#q4-should-recall_past_investigations-support-structured-filters).

**Chat sessions:** The `recall_past_investigations` tool is registered for chat sessions (follow-up conversations after an investigation). Chat sessions do **not** get Tier 4 auto-injection — the user/agent explicitly decides when to recall past investigations. Memory *extraction* does not happen for chat sessions (extraction is scoring-only).

### Human Review Refinement

When a human completes their review (via `PATCH /api/v1/sessions/review` with `action: complete`), two refinement paths trigger. See [Q5 decision](investigation-memory-design-questions.md#q5-how-should-memory-refinement-trigger-on-human-review).

**Part 1 — Inline confidence adjustment** (synchronous, in review handler):

| `quality_rating` | Existing memories | Mechanism |
|---|---|---|
| `accurate` | `confidence = min(confidence × 1.2, 1.0)` | Multiplicative boost |
| `partially_accurate` | `confidence = confidence × 0.6` | Proportional degradation |
| `inaccurate` | `deprecated = true` | Kill switch |

Human review has higher authority than automated score. Multiplicative adjustment ensures high-confidence memories from overscored investigations get proportionally larger corrections.

**Part 2 — Background feedback Reflector** (async, when `investigation_feedback` text is non-empty):

Enqueues a refinement job (same queue infrastructure as scoring). A Reflector variant sees the feedback text, `quality_rating`, existing session memories, and alert context. It can create new memories, deprecate specific existing ones, or reinforce confirmed ones. All feedback-derived memories get **0.9 initial confidence** — human-written feedback is the strongest signal.

This is the primary mechanism for learning from mistakes: `inaccurate` reviews deprecate wrong memories (Part 1) while feedback text produces new `negative`/`procedural` memories (Part 2). `partially_accurate` feedback is equally valuable — the human explains what was right vs. wrong, producing nuanced corrections.

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

**Bulk operations:** Not in v1 — per-memory CRUD suffices for expected scale. See [Q6 decision](investigation-memory-design-questions.md#q6-should-the-api-include-a-bulk-memory-management-endpoint).

### Frontend: Session Detail

Two new sections on the session detail page:

1. **"Injected Memories" card** — shown above the timeline, after the alert card. Lists the 3-5 memories that were auto-injected into this investigation's prompt. Only visible for sessions where memory was active.

2. **"Extracted Learnings" card** — shown near the final analysis card (after scoring). Lists memories that the Reflector extracted from this investigation, with category/valence badges. Shows "No new learnings" if the Reflector found nothing novel.

Both are read-only in v1. Editing/deletion is API-only.

### Confidence Model

**Initial confidence** — derived from the investigation's score:

| Score range | Initial confidence | Rationale |
|-------------|-------------------|-----------|
| 80-100 | 0.8 | High-quality investigation — high trust in extracted patterns |
| 60-79 | 0.6 | Good investigation — moderate trust |
| 40-59 | 0.4 | Average — lower trust, may need reinforcement |
| 0-39 | 0.3 | Poor investigation — extracted anti-patterns at low confidence |

**Human review adjustments** (multiplicative, human authority > automated score):

| `quality_rating` | Action | Example: initial 0.8 |
|---|---|---|
| `accurate` | `min(confidence × 1.2, 1.0)` | → 0.96 |
| `partially_accurate` | `confidence × 0.6` | → 0.48 |
| `inaccurate` | `deprecated = true` | → excluded from retrieval |

**Feedback-derived memories:** 0.9 initial confidence (human-written text is strongest signal).

**Reinforcement** (when Reflector outputs `reinforce`): `min(confidence × 1.1, 1.0)`.

**Decay:** Not in v1. Memories keep confidence indefinitely; the Reflector handles explicit deprecation. See [Q7 decision](investigation-memory-design-questions.md#q7-should-memory-decay-be-implemented-in-v1).

**Future:** If the store grows large enough to need pruning, add a **query-time decay multiplier** — `score × e^(-λ × age_in_days)` with a configurable half-life (e.g., 30 days). Applied at retrieval, not stored — no data mutation, reversible via config, no periodic jobs. Pattern proven by OpenClaw (`temporal-decay.ts`).

### Tracking Injected Memories

To show "which memories were injected" on the session detail page and to exclude them from tool results, injected memories are recorded via a many-to-many Ent edge. See [Q8 decision](investigation-memory-design-questions.md#q8-how-should-injected-memory-ids-be-stored-per-session).

```go
// alertsession.go
edge.To("injected_memories", InvestigationMemory.Type)

// investigationmemory.go
edge.From("injected_into_sessions", AlertSession.Type).Ref("injected_memories")
```

Ent auto-generates the join table. Both directions are natural queries: `session.QueryInjectedMemories()` (session detail page) and `memory.QueryInjectedIntoSessions()` (memory usage analytics).

## Implementation Phases

### Phase 0: Required Skills in Timeline (prerequisite)

1. Add `EventTypeSkillLoaded` to the `timelineevent` enum
2. Emit `skill_loaded` events at agent execution start (before first LLM call) for each required skill
3. Add formatting case in `investigation_formatter.go` → `formatTimelineEvents()`

**Result:** Skills are visible in the UI timeline, scoring evaluator, and investigation context used by the Reflector. No changes to the scoring or memory code needed — skills flow through existing `buildScoringContext()` automatically.

### Phase 1: Schema + Extraction (core pipeline)

1. Enable pgvector extension (migration)
2. `InvestigationMemory` Ent schema + migration (including raw SQL for embedding column + HNSW index)
3. `pkg/memory/` package: `MemoryService` with Create, Retrieve, Update
4. `Embedder` interface + Google/OpenAI provider implementations (direct HTTP)
5. Startup validation: verify configured dimensions match pgvector column size
6. Reflector prompt builder + lenient JSON parser in `pkg/memory/reflector.go` and `pkg/memory/parser.go`
7. Reflector LLM call in `ScoringExecutor` (after scoring controller returns) with "Memorizing..." progress status
8. `ScoringResult` extended with memory extraction output

**Result:** Memories are extracted and stored after every scored investigation.

### Phase 2: Retrieval + Injection (agent uses memory)

1. `MemoryRetriever` — pgvector similarity search within project
2. `MemoryBriefing` on `ExecutionContext`
3. Tier 4 section in `ComposeInstructions` (investigation sessions only)
4. `MemoryToolExecutor` wrapping inner executor (`pkg/memory/tool_executor.go`)
5. `recall_past_investigations` tool registration (investigation + chat sessions)
6. Record injected memory IDs via Ent edge (join table)

**Result:** Investigations benefit from past learnings. Chat sessions can recall past investigations via tool.

### Phase 3: Human Refinement + API + Dashboard

1. Inline confidence adjustment on review completion (multiplicative, Q5 Part 1)
2. Background feedback Reflector job — enqueue when `investigation_feedback` is non-empty, runs Reflector variant to create/deprecate/reinforce memories from human feedback (Q5 Part 2)
3. Memory CRUD API endpoints
4. Frontend: "Injected Memories" card on session detail
5. Frontend: "Extracted Learnings" card on session detail
6. Memory list/detail in API (admin usage)

**Result:** Full feedback loop — extract → inject → refine from human review. Learning from mistakes via feedback-derived memories. Visible in dashboard.

## Open Questions Summary

| # | Question | Section affected |
|---|----------|-----------------|
| ~~Q1~~ | ~~Embedding model, provider, and configuration~~ | **Decided:** `gemini-embedding-2-preview` at 768 dims, configurable via `defaults.memory.embedding` |
| ~~Q2~~ | ~~pgvector index strategy~~ | **Decided:** HNSW (`m=16, ef_construction=64, vector_cosine_ops`) |
| ~~Q3~~ | ~~Reflector parse failure handling~~ | **Decided:** Lenient parse + silent fallback (never blocks scoring) |
| ~~Q4~~ | ~~recall_past_investigations tool parameters~~ | **Decided:** Free-text `query` only (+ optional `limit`), no structured filters in v1 |
| ~~Q5~~ | ~~Review refinement trigger mechanism~~ | **Decided:** Inline multiplicative confidence + background Reflector for feedback text |
| ~~Q6~~ | ~~Bulk memory management API~~ | **Decided:** No bulk endpoint in v1, per-memory CRUD only |
| ~~Q7~~ | ~~Memory decay in v1~~ | **Decided:** No decay in v1; future: query-time multiplier (not stored data) |
| ~~Q8~~ | ~~Storing injected memory IDs~~ | **Decided:** Many-to-many Ent edge (join table, auto-generated) |
