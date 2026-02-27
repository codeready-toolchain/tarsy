# Stage Types — Design Document

**Status:** Final
**Related:** [Design questions](stage-types-questions.md)

## Overview

TARSy alert sessions are composed of stages, each representing a set of LLM interactions with some outcome. Today there are already multiple kinds of stages — investigation, synthesis, chat — but the schema has no explicit field to distinguish them. The kinds are identified through fragile heuristics: nullable FK checks (`chat_id IS NOT NULL`) and string suffix matching (`" - Synthesis"`).

This proposal adds a `stage_type` enum field to the Stage schema so every stage carries an explicit type. This removes heuristic-based classification from the codebase and enables direct filtering, simpler context building, and richer API/WS responses.

**In scope:** data model, API, WS event changes, creation-path wiring, chat context builder simplification.
**Out of scope:** UI changes, scoring-as-a-stage migration (this only reserves the enum value).

The investigation uncovered 1 follow-up issue to decide on: Synthesis stages reference their "parent" stages
by name (e.g. "my stage - Synthesis" is related to "my stage" stage). This is fragile and would benefit from
a more formal approach. If there was "referenced_stage_id" column, it could be for example reused by the
chats to focus the chat to a single investigation stage or score.

This proposal doesn't address that issue and keeps the matching of the "parent" stage based on string matching.

## Design Principles

1. **Additive change.** No existing behavior changes — only new information surfaced.
2. **Explicit over implicit.** Replace heuristic classification with a declarative field.
3. **Single source of truth.** The `stage_type` column is the canonical way to identify stage kind — no more suffix checks or FK inference.
4. **Consistent with existing patterns.** Follow the same ent enum pattern used by `stage.Status`, `stage.ParallelType`, and `stage.SuccessPolicy`.

## Architecture

### Stage Type Enum

Defined as an ent schema enum field. No separate Go type — use ent-generated constants (`stage.StageTypeInvestigation`, `stage.StageTypeSynthesis`, etc.), consistent with how `stage.Status`, `stage.ParallelType`, and `stage.SuccessPolicy` work throughout the codebase.

Four values:

| Value | Description | Created by |
|-------|-------------|------------|
| `investigation` | From chain config stage | `executor.go` (default) |
| `synthesis` | Auto-generated after multi-agent investigation | `executor_synthesis.go` |
| `chat` | User follow-up chat message | `chat_executor.go` |
| `scoring` | Quality evaluation (reserved, not wired up) | Future |

### DB Schema Change

Add one field to `ent/schema/stage.go`:

```go
field.Enum("stage_type").
    Values("investigation", "synthesis", "chat", "scoring").
    Default("investigation").
    Comment("Kind of stage: investigation (from chain), synthesis (auto-generated), chat (user message), scoring (quality evaluation)"),
```

The field is **required with a default** (`investigation`), not optional/nillable. Every stage has exactly one type. The default covers the most common case (investigation) and means the investigation creation path needs no code change.

No index on `stage_type` — all current queries load stages per-session (typically 1-5 stages), filtering in-memory is trivial. An index can be added later if a cross-session filtering use case arises.

### CreateStageRequest Change

```go
type CreateStageRequest struct {
    // ... existing fields ...
    StageType string `json:"stage_type,omitempty"` // defaults to "investigation" if empty
}
```

### StageService.CreateStage Change

```go
stageType := stage.StageTypeInvestigation // default
if req.StageType != "" {
    stageType = stage.StageType(req.StageType)
}

builder := s.client.Stage.Create().
    // ... existing fields ...
    SetStageType(stageType)
```

Validation: ent enum validation rejects unknown values automatically.

### Creation Path Changes

**Investigation** (`executor.go:330`) — no change needed. Omitting `StageType` falls through to the `"investigation"` default.

**Synthesis** (`executor_synthesis.go:40`) — set `StageType: "synthesis"`.

**Chat** (`chat_executor.go:172`) — set `StageType: "chat"`.

**Scoring** — not wired up in this proposal. Reserved for future use.

### API Response Changes

**StageOverview** (`pkg/models/session.go`) — add `StageType string`:

```go
type StageOverview struct {
    // ... existing fields ...
    StageType string `json:"stage_type"`
}
```

Populated directly from `stg.StageType` in `GetSessionDetail`.

**TraceStageGroup** (`pkg/models/interaction.go`) — add `StageType string`:

```go
type TraceStageGroup struct {
    StageID   string `json:"stage_id"`
    StageName string `json:"stage_name"`
    StageType string `json:"stage_type"`
    // ...
}
```

Populated from the DB field in `handler_trace.go`.

### WS Event Payload Change

Add `stage_type` to `StageStatusPayload` (`pkg/events/payloads.go`):

```go
type StageStatusPayload struct {
    BasePayload
    StageID    string `json:"stage_id,omitempty"`
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"`
    StageType  string `json:"stage_type"`
    Status     string `json:"status"`
}
```

The `publishStageStatus` helper (`pkg/queue/executor_helpers.go`) gains a `stageType` parameter. All 6 call sites are updated to pass the type:

- `executor.go` — `"investigation"` for investigation stages, `"synthesis"` for synthesis terminal status
- `executor_synthesis.go` — `"synthesis"`
- `chat_executor.go` — `"chat"`

### Chat Context Builder Simplification

With an explicit type field, the heuristic-based filtering in `chat_executor.go:452-485` becomes direct:

```go
for _, stg := range stages {
    switch stg.StageType {
    case stage.StageTypeSynthesis:
        // Already paired via synthResults map — skip
        continue
    case stage.StageTypeChat:
        // Previous Q&A
        isCurrentChat := stg.ChatUserMessageID != nil && *stg.ChatUserMessageID == input.Message.ID
        if !isCurrentChat {
            if qa := e.buildChatQA(ctx, stg); qa.Question != "" {
                previousChats = append(previousChats, qa)
            }
        }
        continue
    default:
        // Investigation stage — build per-agent timelines
    }
}
```

No more `strings.HasSuffix` or `chat_id != nil` checks for stage identification.

The synthesis stage _pairing_ logic (finding which investigation stage a synthesis belongs to) still uses name-based backward scanning. This is correct — the synthesis stage name is always derived from the parent in `executor_synthesis.go`. A structural `parent_stage_id` FK could replace this in a future proposal but is orthogonal to stage types.

## DB Schema Impact

One new column on the `stages` table:

| Column | Type | Nullable | Default | Index |
|--------|------|----------|---------|-------|
| `stage_type` | `enum('investigation','synthesis','chat','scoring')` | NOT NULL | `investigation` | None |

### Migration

The column is added with `DEFAULT 'investigation'`, so existing rows are backfilled automatically. Existing synthesis and chat stages need explicit backfill, embedded in the same ent migration:

```sql
-- Backfill synthesis stages (identified by name suffix)
UPDATE stages SET stage_type = 'synthesis' WHERE stage_name LIKE '% - Synthesis';

-- Backfill chat stages (identified by non-null chat_id)
UPDATE stages SET stage_type = 'chat' WHERE chat_id IS NOT NULL;

-- Investigation stages already have the correct default
```

This migration is safe and idempotent. The heuristics match exactly the stages they need to update.

## Impact Analysis

### Files Affected

| Area | Files | Change scope |
|------|-------|-------------|
| DB schema | `ent/schema/stage.go` | Add `stage_type` enum field |
| Generated code | `ent/` (codegen) | Regenerate ent code |
| Stage model | `pkg/models/stage.go` | Add `StageType` to `CreateStageRequest` |
| Stage service | `pkg/services/stage_service.go` | Wire `StageType` in `CreateStage` |
| Investigation executor | `pkg/queue/executor.go` | Update `publishStageStatus` calls to pass type |
| Synthesis executor | `pkg/queue/executor_synthesis.go` | Set `StageType: "synthesis"` in `CreateStageRequest`; update `publishStageStatus` call |
| Chat executor | `pkg/queue/chat_executor.go` | Set `StageType: "chat"` in `CreateStageRequest`; simplify `buildChatContext`; update `publishStageStatus` calls |
| Event helpers | `pkg/queue/executor_helpers.go` | Add `stageType` parameter to `publishStageStatus` |
| API DTOs | `pkg/models/session.go` | Add `StageType` to `StageOverview` |
| API DTOs | `pkg/models/interaction.go` | Add `StageType` to `TraceStageGroup` |
| API handler | `pkg/api/handler_trace.go` | Populate `StageType` from DB field |
| Session service | `pkg/services/session_service.go` | Populate `StageType` in `StageOverview` |
| WS payloads | `pkg/events/payloads.go` | Add `StageType` to `StageStatusPayload` |
| DB migration | Generated ent migration + backfill | Add column + backfill SQL |
| Tests | Various `_test.go` files | Update test cases to include `StageType` |

### Risk

- **Low**: Additive field with a default value. No existing behavior changes.
- **Migration**: Backfill is a safe `UPDATE` on existing rows.
- **API compatibility**: New JSON field is additive — existing clients ignore unknown fields.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Where to define `StageType` | Ent-generated constants only | Consistent with `stage.Status`, `stage.ParallelType`, `stage.SuccessPolicy`. No duplication. |
| Q2 | DB index on `stage_type` | No index | Low cardinality, no cross-session queries. Can add later if needed. |
| Q3 | `stage_type` in `StageStatusPayload` | Yes | WS payload should be self-describing. Mechanical change to `publishStageStatus`. |
| Q4 | Synthesis pairing in `buildChatContext` | Replace identification only, keep name-based pairing | Name convention is reliable. `parent_stage_id` FK is a separate future concern. |
| Q5 | Backfill migration | Embed in ent migration | Single step, standard pattern for schema + data changes. |
| Q6 | PR granularity | Single PR | Change is small (~15 files) and tightly coupled. |

## Implementation Plan

Single PR with the following changes:

1. **Schema:** Add `stage_type` enum field to `ent/schema/stage.go`. Regenerate ent code. Add backfill SQL to the migration.
2. **Service:** Add `StageType` to `CreateStageRequest`. Wire in `StageService.CreateStage` with `"investigation"` default.
3. **Creation paths:** Set `StageType: "synthesis"` in `executor_synthesis.go`. Set `StageType: "chat"` in `chat_executor.go`. Investigation path needs no change (default).
4. **API:** Add `StageType` to `StageOverview` and `TraceStageGroup`. Populate in `GetSessionDetail` and `handler_trace.go`.
5. **WS:** Add `StageType` to `StageStatusPayload`. Add `stageType` parameter to `publishStageStatus`. Update all 6 call sites.
6. **Chat context:** Replace `strings.HasSuffix` and `chat_id != nil` identification checks with `stg.StageType` switch in `buildChatContext`.
7. **Tests:** Update existing tests, add targeted tests for type assignment and backfill.
