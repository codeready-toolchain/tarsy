# Stage Types

**Status:** Proposed
**Date:** 2026-02-27

## Problem Statement

A TARSy alert session is composed of stages, each representing a set of LLM interactions with some outcome. Today there are already multiple kinds of stages, but the schema has no explicit field to distinguish them. The kinds are identified through fragile heuristics:

| Kind | How identified today | Created by |
|------|---------------------|------------|
| Investigation | `chat_id IS NULL` AND name doesn't end with `" - Synthesis"` | `executor.go:330` — from chain config |
| Synthesis | `chat_id IS NULL` AND `stage_name` ends with `" - Synthesis"` | `executor_synthesis.go:40` — auto-generated after multi-agent investigation |
| Chat | `chat_id IS NOT NULL` | `chat_executor.go:172` — from user chat message |

Scoring could become a fourth kind. The infrastructure exists (`ScoringAgent`, `ScoringController`, `ResolveScoringConfig`, `session_scores` table) but scoring currently runs outside the stage framework entirely.

Problems with the current state:

1. **No explicit type field.** Code that needs to distinguish stage kinds relies on nullable FK checks (`chat_id IS NOT NULL`) or string suffix matching (`" - Synthesis"`). Both are fragile and spread implicit knowledge across the codebase.

2. **Filtering is expensive.** The presentation layer cannot filter stages by kind with a simple `WHERE stage_type = 'chat'` — it must load stages and inspect FKs or name patterns.

3. **Chat context builder uses heuristics.** `chat_executor.go:452-485` identifies synthesis stages by suffix and chat stages by `chat_id != nil`. An explicit type would make this logic direct and self-documenting.

4. **API responses lack type information.** `StageOverview`, `TraceStageGroup`, and `StageStatusPayload` don't include a stage type — consumers must reverse-engineer it from other fields.

5. **Scoring has no stage representation.** If scoring becomes a stage, there's no type to assign it.

## Goal

Add a `stage_type` enum field to the Stage schema that explicitly identifies each stage's kind. Propagate this through the data model, API responses, and WebSocket events so consumers can filter and present stages by type without heuristics.

**In scope:** data model, API, WS event changes, creation-path wiring.
**Out of scope:** UI changes, scoring-as-a-stage migration (separate proposal — this one only reserves the enum value).

## Current Architecture

### Stage schema (no type field)

```go
// ent/schema/stage.go
func (Stage) Fields() []ent.Field {
    return []ent.Field{
        field.String("id").StorageKey("stage_id").Unique().Immutable(),
        field.String("session_id").Immutable(),
        field.String("stage_name"),
        field.Int("stage_index"),
        field.Int("expected_agent_count"),
        field.Enum("parallel_type").Values("multi_agent", "replica").Optional().Nillable(),
        field.Enum("success_policy").Values("all", "any").Optional().Nillable(),
        field.Enum("status").Values("pending", "active", "completed", "failed", "timed_out", "cancelled").Default("pending"),
        field.Time("started_at").Optional().Nillable(),
        field.Time("completed_at").Optional().Nillable(),
        field.Int("duration_ms").Optional().Nillable(),
        field.String("error_message").Optional().Nillable(),
        // Chat context — only way to distinguish chat stages today
        field.String("chat_id").Optional().Nillable(),
        field.String("chat_user_message_id").Optional().Nillable(),
    }
}
```

### Investigation stage creation (`executor.go:330`)

```go
stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
    SessionID:          input.session.ID,
    StageName:          input.stageConfig.Name,
    StageIndex:         input.stageIndex + 1,
    ExpectedAgentCount: len(configs),
    ParallelType:       parallelTypePtr(input.stageConfig),
    SuccessPolicy:      successPolicyPtr(input.stageConfig, policy),
    // No type field — investigation is implicit (chat_id = nil, no suffix)
})
```

### Synthesis stage creation (`executor_synthesis.go:40`)

```go
stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
    SessionID:          input.session.ID,
    StageName:          parallelResult.stageName + " - Synthesis",  // <-- suffix-based identification
    StageIndex:         input.stageIndex + 1,
    ExpectedAgentCount: 1,
    // No type field — synthesis identified by name suffix only
})
```

### Chat stage creation (`chat_executor.go:172`)

```go
stg, err := e.stageService.CreateStage(ctx, models.CreateStageRequest{
    SessionID:          input.Session.ID,
    StageName:          "Chat Response",
    StageIndex:         stageIndex,
    ExpectedAgentCount: 1,
    ChatID:             &chatID,            // <-- FK-based identification
    ChatUserMessageID:  &messageID,
})
```

### Chat context builder heuristics (`chat_executor.go:452-485`)

```go
for _, stg := range stages {
    // Heuristic 1: suffix match
    if strings.HasSuffix(stg.StageName, " - Synthesis") {
        continue
    }
    // Heuristic 2: FK presence
    if stg.ChatID != nil && *stg.ChatID != "" {
        // treat as chat stage
        continue
    }
    // else: investigation stage
}
```

### API response types (no stage type)

```go
// pkg/models/session.go
type StageOverview struct {
    ID                 string              `json:"id"`
    StageName          string              `json:"stage_name"`
    StageIndex         int                 `json:"stage_index"`
    Status             string              `json:"status"`
    // ... no stage_type field
}

// pkg/models/interaction.go
type TraceStageGroup struct {
    StageID    string                `json:"stage_id"`
    StageName  string                `json:"stage_name"`
    // ... no stage_type field
}
```

### WS event payload (no stage type)

```go
// pkg/events/payloads.go
type StageStatusPayload struct {
    BasePayload
    StageID    string `json:"stage_id,omitempty"`
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"`
    Status     string `json:"status"`
    // ... no stage_type field
}
```

## Proposed Architecture

### Stage type enum

```go
// pkg/config/enums.go (or a new file — follows existing pattern for AgentType, LLMBackend)
type StageType string

const (
    StageTypeInvestigation StageType = "investigation"
    StageTypeSynthesis     StageType = "synthesis"
    StageTypeChat          StageType = "chat"
    StageTypeScoring       StageType = "scoring"  // reserved for future use
)
```

The enum is defined in `pkg/config/enums.go` alongside `AgentType` and `LLMBackend` for consistency. `StageType` is a domain concept that flows from config → execution → DB → API, same as those types.

### DB schema change

```go
// ent/schema/stage.go — add one field
field.Enum("stage_type").
    Values("investigation", "synthesis", "chat", "scoring").
    Default("investigation").
    Comment("Kind of stage: investigation (from chain), synthesis (auto-generated), chat (user message), scoring (quality evaluation)"),
```

The field is **required with a default** (`investigation`), not optional/nillable. Every stage has exactly one type. The default covers the most common case and simplifies the investigation creation path.

### CreateStageRequest change

```go
// pkg/models/stage.go
type CreateStageRequest struct {
    SessionID          string  `json:"session_id"`
    StageName          string  `json:"stage_name"`
    StageIndex         int     `json:"stage_index"`
    ExpectedAgentCount int     `json:"expected_agent_count"`
    ParallelType       *string `json:"parallel_type,omitempty"`
    SuccessPolicy      *string `json:"success_policy,omitempty"`
    ChatID             *string `json:"chat_id,omitempty"`
    ChatUserMessageID  *string `json:"chat_user_message_id,omitempty"`
    StageType          string  `json:"stage_type,omitempty"`  // NEW — defaults to "investigation" if empty
}
```

### StageService.CreateStage change

```go
// pkg/services/stage_service.go — in CreateStage
stageType := stage.StageTypeInvestigation // default
if req.StageType != "" {
    stageType = stage.StageType(req.StageType)
}

builder := s.client.Stage.Create().
    // ... existing fields ...
    SetStageType(stageType)
```

Validation: reject unknown `StageType` values (ent enum validation handles this automatically).

### Creation path changes

**Investigation** (`executor.go:330`) — no change needed. Omitting `StageType` falls through to the `"investigation"` default.

**Synthesis** (`executor_synthesis.go:40`):

```go
stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
    // ... existing fields ...
    StageType: "synthesis",  // NEW
})
```

**Chat** (`chat_executor.go:172`):

```go
stg, err := e.stageService.CreateStage(ctx, models.CreateStageRequest{
    // ... existing fields ...
    StageType: "chat",  // NEW
})
```

**Scoring** — not wired up in this proposal. When scoring becomes a stage, it will pass `StageType: "scoring"`.

### API response changes

**StageOverview** (`pkg/models/session.go`):

```go
type StageOverview struct {
    ID                 string              `json:"id"`
    StageName          string              `json:"stage_name"`
    StageIndex         int                 `json:"stage_index"`
    StageType          string              `json:"stage_type"`   // NEW
    Status             string              `json:"status"`
    ParallelType       *string             `json:"parallel_type"`
    ExpectedAgentCount int                 `json:"expected_agent_count"`
    StartedAt          *time.Time          `json:"started_at"`
    CompletedAt        *time.Time          `json:"completed_at"`
    Executions         []ExecutionOverview `json:"executions,omitempty"`
}
```

**TraceStageGroup** (`pkg/models/interaction.go`):

```go
type TraceStageGroup struct {
    StageID    string                `json:"stage_id"`
    StageName  string                `json:"stage_name"`
    StageType  string                `json:"stage_type"`  // NEW
    Executions []TraceExecutionGroup `json:"executions"`
}
```

Both are populated directly from the `Stage.StageType` DB field — no mapping logic needed.

### WS event payload change

**StageStatusPayload** (`pkg/events/payloads.go`):

```go
type StageStatusPayload struct {
    BasePayload
    StageID    string `json:"stage_id,omitempty"`
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"`
    StageType  string `json:"stage_type"`   // NEW
    Status     string `json:"status"`
}
```

**publishStageStatus helper** (`executor.go` / `chat_executor.go`) gains a `stageType` parameter that flows through to the payload. For chat stages, the type is known at the call site. For investigation/synthesis stages, it's derived from the creation path.

### Chat context builder simplification

With an explicit type field, the heuristic-based filtering in `chat_executor.go:452-485` becomes direct:

```go
for _, stg := range stages {
    switch stage.StageType(stg.StageType) {
    case stage.StageTypeSynthesis:
        // Already paired via synthResults map — skip
        continue
    case stage.StageTypeChat:
        // Previous Q&A — skip current message's stage
        isCurrentChat := stg.ChatUserMessageID != nil && *stg.ChatUserMessageID == input.Message.ID
        if !isCurrentChat {
            if qa := e.buildChatQA(ctx, stg); qa.Question != "" {
                previousChats = append(previousChats, qa)
            }
        }
        continue
    default:
        // Investigation stage — build per-agent timelines
        // ...
    }
}
```

No more `strings.HasSuffix` or `chat_id != nil` checks.

## DB Schema Impact

One new column on the `stages` table:

| Column | Type | Nullable | Default | Index |
|--------|------|----------|---------|-------|
| `stage_type` | `enum('investigation','synthesis','chat','scoring')` | NOT NULL | `investigation` | Yes (for filtering) |

### Migration

The column is added with `DEFAULT 'investigation'`, so existing rows are backfilled automatically. However, existing synthesis and chat stages need a data migration to set the correct type:

```sql
-- Backfill synthesis stages (identified by name suffix)
UPDATE stages SET stage_type = 'synthesis' WHERE stage_name LIKE '% - Synthesis';

-- Backfill chat stages (identified by non-null chat_id)
UPDATE stages SET stage_type = 'chat' WHERE chat_id IS NOT NULL;

-- Investigation stages already have the correct default
```

This migration is safe and idempotent. After backfill, the heuristics are no longer needed for new stages but remain valid for verification.

### Index

A single-column index on `stage_type` enables efficient filtering:

```go
// ent/schema/stage.go — add to Indexes()
index.Fields("stage_type"),
```

A composite `(session_id, stage_type)` index is not needed — the existing `(session_id, stage_index)` unique index already covers session-scoped queries, and `stage_type` filtering is typically applied after session scoping.

## Impact Analysis

### Files affected

| Area | Files | Change scope |
|------|-------|-------------|
| Enum definition | `pkg/config/enums.go` | Add `StageType` type and constants |
| DB schema | `ent/schema/stage.go` | Add `stage_type` field + index |
| Stage model | `pkg/models/stage.go` | Add `StageType` to `CreateStageRequest` |
| Stage service | `pkg/services/stage_service.go` | Wire `StageType` in `CreateStage` |
| Investigation executor | `pkg/queue/executor.go` | No code change (default covers it); update `publishStageStatus` calls to pass type |
| Synthesis executor | `pkg/queue/executor_synthesis.go` | Set `StageType: "synthesis"` in `CreateStageRequest` |
| Chat executor | `pkg/queue/chat_executor.go` | Set `StageType: "chat"` in `CreateStageRequest`; simplify `buildChatContext` |
| Event helpers | `pkg/queue/executor.go` (publishStageStatus) | Add `stageType` parameter |
| API DTOs | `pkg/models/session.go` | Add `StageType` to `StageOverview` |
| API DTOs | `pkg/models/interaction.go` | Add `StageType` to `TraceStageGroup` |
| API handler | `pkg/api/handler_trace.go` | Populate `StageType` from DB field |
| Session detail handler | `pkg/api/handler_session.go` (or equivalent) | Populate `StageType` in `StageOverview` |
| WS payloads | `pkg/events/payloads.go` | Add `StageType` to `StageStatusPayload` |
| DB migration | Generated ent migration | Add column + backfill |
| Tests | Various `_test.go` files | Update test cases to include `StageType` |

### Risk

- **Low**: Additive field with a default value. No existing behavior changes — only new information is surfaced.
- **Migration**: Backfill is a safe `UPDATE` on existing rows. The suffix/FK heuristics match exactly the stages they need to update.
- **API compatibility**: New JSON field `stage_type` is additive — existing clients ignore unknown fields.

## Backward Compatibility

**Additive change — no breaking changes.** The `stage_type` field is added with a default value, so:

- Existing DB rows get `investigation` by default + data migration corrects synthesis/chat rows.
- API responses gain a new field — existing clients that don't use it are unaffected.
- WS events gain a new field — existing listeners ignore unknown fields.
- `CreateStageRequest` with empty `StageType` defaults to `investigation` — no change to callers that don't set it.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Should synthesis be a separate type? | Yes — `synthesis` is a distinct type | Currently identified by fragile name suffix. An explicit type makes filtering direct and eliminates string-matching heuristics. |
| Q2 | Should scoring become a stage? | Reserve the enum value, but don't wire it up | Scoring infrastructure exists but runs outside the stage framework. This proposal reserves `scoring` so a future proposal can wire it up without another schema migration. |
| Q3 | Where should `StageType` be defined? | `pkg/config/enums.go` | Follows the pattern of `AgentType` and `LLMBackend` — domain enums that flow from config to DB to API. |
| Q4 | Should the field be nullable? | No — required with default `investigation` | Every stage has exactly one type. A default simplifies the investigation path (most common case). |
| Q5 | Should `chat_id`/`chat_user_message_id` be removed? | No — keep them | They serve a relational purpose (linking stages to chats/messages) independent of the type enum. The type field is for classification; the FKs are for data relationships. |
| Q6 | Should `publishStageStatus` signature change? | Yes — add `stageType` parameter | The WS payload needs the type. Passing it explicitly is cleaner than re-querying the DB. |
| Q7 | Data migration strategy? | Backfill with `UPDATE` using existing heuristics | The same suffix/FK logic that code uses today is applied once in SQL to set the correct type on historical rows. After migration, new stages set the type explicitly at creation. |

## Implementation Phases

1. **Schema + enum + service:** Add `StageType` to `enums.go`, `stage.go` schema, `CreateStageRequest`, and `StageService.CreateStage`. Generate ent code. Write migration with backfill SQL.
2. **Creation paths:** Set `StageType` in synthesis executor and chat executor. Investigation path needs no change (default).
3. **API + WS:** Add `StageType` to `StageOverview`, `TraceStageGroup`, `StageStatusPayload`. Update handlers and `publishStageStatus` to populate the field.
4. **Chat context simplification:** Replace suffix/FK heuristics in `buildChatContext` with `switch stg.StageType`. Remove `strings.HasSuffix` check.
5. **Tests:** Update existing tests, add targeted tests for type filtering and backfill verification.

Each phase is a reviewable PR. Phases 1-2 can be a single PR if preferred (schema + creation are tightly coupled). Phase 4 is optional cleanup that can be deferred.