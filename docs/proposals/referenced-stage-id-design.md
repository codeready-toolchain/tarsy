# Referenced Stage ID

**Status:** Final
**Decisions:** [referenced-stage-id-questions.md](referenced-stage-id-questions.md)
**Origin:** Identified during [stage-types ADR](../adr/0004-stage-types.md) investigation (Q4)

## Overview

Synthesis stages are linked to their parent investigation stage by a **naming convention only**. The executor creates a synthesis stage named `"{ParentStageName} - Synthesis"`, and downstream code (e.g. `buildChatContext`) pairs them by stripping the suffix and scanning backward by name.

This is fragile:

- Relies on a string convention not enforced at the schema level.
- Pairing logic uses backward name scanning — correct today but could break if naming conventions evolve.
- No structural way to query "which investigation stage does this synthesis belong to?"

Adding an optional `referenced_stage_id` FK to the `stages` table replaces this convention with a structural relationship.

## Design Principles

1. **Structural over conventional.** An FK is queryable, enforceable, and self-documenting. String suffix matching is none of these.
2. **Non-breaking.** The column is optional and nullable. Existing stages continue to work. No behavior changes until the FK is populated and consumers switch to it.
3. **Minimal scope.** This is a schema change + two consumer rewrites (synthesis creation, chat context building). No new entities, no new services.
4. **Consistent with existing patterns.** Follow the same ent edge patterns used throughout the `Stage` schema (e.g. `session`, `chat`, `chat_user_message` edges).

## Architecture

### Schema Change

Add an optional `referenced_stage_id` column to the `stages` table as a self-referential FK with ON DELETE SET NULL.

| Stage type | `referenced_stage_id` | Purpose |
|---|---|---|
| `investigation` | NULL | No parent |
| `synthesis` | Points to parent investigation stage | Replaces name-based pairing |
| `chat` | NULL | Chats are session-scoped, not stage-scoped |
| `exec_summary` | NULL | Summarizes entire session |
| `scoring` | NULL | Evaluates entire session |

### Ent Schema

Add a field and self-referential edge to `ent/schema/stage.go`:

```go
// Field
field.String("referenced_stage_id").
    Optional().
    Nillable().
    Comment("FK to another stage in the same session (e.g. synthesis → investigation)"),

// Edges
edge.To("referencing_stages", Stage.Type).
    Annotations(entsql.OnDelete(entsql.SetNull)),
edge.From("referenced_stage", Stage.Type).
    Ref("referencing_stages").
    Field("referenced_stage_id").
    Unique(),
```

This follows the same pattern as every other FK in the Stage schema (`session`, `chat`, `chat_user_message` all use edges). The `referencing_stages` reverse edge is generated but unlikely to be queried — acceptable cost for consistency.

### Same-Session Constraint

The FK enforces referential integrity (referenced stage must exist). Same-session enforcement is application-level: the `StageService.CreateStage` method validates that `referenced_stage_id` belongs to the same `session_id`. This is the pragmatic choice — SQL CHECK constraints cannot reference other rows, and a trigger adds complexity for a constraint that's only set by internal executor code.

### Creation Path Change

In `executor_synthesis.go`, pass the parent investigation stage's ID when creating the synthesis stage:

```go
stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
    SessionID:          input.session.ID,
    StageName:          synthStageName,
    StageIndex:         input.stageIndex + 1,
    ExpectedAgentCount: 1,
    StageType:          string(stage.StageTypeSynthesis),
    ReferencedStageID:  &parallelResult.stageID,  // NEW
})
```

The parent investigation stage ID is already available as `parallelResult.stageID` — no additional queries needed.

### Consumer Change: `buildChatContext`

Replace the name-based backward scan in `chat_executor.go` with a direct FK lookup. No name-based fallback — the migration backfills all existing synthesis stages (see Migration below).

**Current** (name-based):
```go
synthResults := make(map[string]string)
for i, stg := range stages {
    if stg.StageType == stage.StageTypeSynthesis {
        parentName := strings.TrimSuffix(stg.StageName, " - Synthesis")
        for j := i - 1; j >= 0; j-- {
            if stages[j].StageName == parentName && stages[j].StageType == stage.StageTypeInvestigation {
                if fa := e.extractFinalAnalysis(ctx, stg); fa != "" {
                    synthResults[stages[j].ID] = fa
                }
                break
            }
        }
    }
}
```

**Proposed** (FK-based):
```go
synthResults := make(map[string]string)
for _, stg := range stages {
    if stg.StageType == stage.StageTypeSynthesis && stg.ReferencedStageID != nil {
        if fa := e.extractFinalAnalysis(ctx, stg); fa != "" {
            synthResults[*stg.ReferencedStageID] = fa
        }
    }
}
```

This removes the `strings` import dependency for synthesis pairing and eliminates the O(n) backward scan per synthesis stage.

### Migration

The migration has two steps:

1. **Schema change:** Add nullable `referenced_stage_id` column with FK constraint (ON DELETE SET NULL) and self-referential edge.

2. **Backfill:** Populate `referenced_stage_id` for all existing synthesis stages using the name convention:

```sql
UPDATE stages s_synth
SET referenced_stage_id = (
    SELECT s_inv.stage_id
    FROM stages s_inv
    WHERE s_inv.session_id = s_synth.session_id
      AND s_inv.stage_type = 'investigation'
      AND s_inv.stage_name = REPLACE(s_synth.stage_name, ' - Synthesis', '')
      AND s_inv.stage_index < s_synth.stage_index
    ORDER BY s_inv.stage_index DESC
    LIMIT 1
)
WHERE s_synth.stage_type = 'synthesis';
```

Same approach as the `stage_type` backfill in ADR-0004 — embed the backfill in the ent migration. This ensures all existing data has the FK before consumers switch to the FK-only code path.

### Model Changes

Add `ReferencedStageID` to `CreateStageRequest` in `pkg/models/stage.go`:

```go
type CreateStageRequest struct {
    // ... existing fields ...
    ReferencedStageID  *string `json:"referenced_stage_id,omitempty"`
}
```

Wire it in `StageService.CreateStage` with optional validation (referenced stage must exist, must be in same session).

### API / WS Exposure

Add `referenced_stage_id` (nullable) to:

- `StageOverview` in the session detail API response
- `StageStatusPayload` in WebSocket events
- `TraceStageGroup` in the trace API response

This is additive — existing clients ignore unknown fields. Enables the frontend to show stage relationships without a future API change.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Ent schema approach | Self-referential ent edge | Consistent with how every other FK in the Stage schema is modeled. ON DELETE SET NULL. |
| Q2 | Backfill strategy | Backfill in migration SQL | Same pattern as ADR-0004 stage_type backfill. Enables dropping name-based pairing entirely. |
| Q3 | Name-based fallback | FK-only, no fallback | Backfill covers all existing data. Single code path, no dead fallback code. |
| Q4 | API/WS exposure | Expose in responses | Additive field, near-zero cost. Avoids future API change. Same reasoning as `stage_type` in ADR-0004 Q3. |
| Q5 | Chat stage scoping | Not applicable | Chats are session-scoped. `referenced_stage_id` is NULL for chat stages. |

## Implementation Plan

Single PR — the scope is small (schema + 2 consumers + API wiring):

1. **Schema:** Add `referenced_stage_id` field and self-referential edge to `ent/schema/stage.go`. Regenerate ent code. Add backfill SQL to the migration.
2. **Model:** Add `ReferencedStageID` to `CreateStageRequest`. Wire in `StageService.CreateStage` with same-session validation.
3. **Creation path:** Set `ReferencedStageID: &parallelResult.stageID` in `executor_synthesis.go`.
4. **Consumer:** Replace name-based pairing in `buildChatContext` with FK lookup. Remove `strings.TrimSuffix` / backward scan.
5. **API:** Add `ReferencedStageID` to `StageOverview`, `StageStatusPayload`, and `TraceStageGroup`. Populate in handlers.
6. **Tests:** Update `buildChatContext` tests (synthesis pairing via FK instead of name). Add migration backfill test. Update integration tests.
