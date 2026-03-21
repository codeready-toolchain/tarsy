# ADR-0005: Referenced Stage ID

**Status:** Implemented  
**Date:** March 4, 2026  
**Origin:** Identified during [stage-types ADR](0004-stage-types.md) investigation (Q4)

## Overview

Synthesis stages are linked to their parent investigation stage by a **naming convention only**. The executor creates a synthesis stage named `"{ParentStageName} - Synthesis"`, and downstream code pairs them by stripping the suffix and scanning backward by name.

This is fragile:

- Relies on a string convention not enforced at the schema level.
- Pairing logic uses backward name scanning — correct today but could break if naming conventions evolve.
- No structural way to query "which investigation stage does this synthesis belong to?"

Adding an optional `referenced_stage_id` FK to the `stages` table replaces this convention with a structural relationship.

## Design Principles

1. **Structural over conventional.** An FK is queryable, enforceable, and self-documenting. String suffix matching is none of these.
2. **Non-breaking.** The column is optional and nullable. Existing stages continue to work. No behavior changes until the FK is populated and consumers switch to it.
3. **Minimal scope.** Schema change plus synthesis creation and chat-context consumers. No new entities, no new services.
4. **Consistent with existing patterns.** Model the FK the same way as other stage foreign keys (self-referential edge, ON DELETE SET NULL).

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

The field is modeled in the ORM with a self-referential relationship consistent with other stage FKs. A reverse “referencing stages” relation may be generated even if rarely queried — acceptable for consistency.

### Same-Session Constraint

The FK enforces referential integrity (referenced stage must exist). **Same-session enforcement is application-level:** stage creation validates that `referenced_stage_id` belongs to the same session as the new stage. SQL `CHECK` constraints cannot reference other rows; a trigger would add complexity for a constraint that internal executor code sets in one place.

### Synthesis creation and consumers

When creating a synthesis stage, the parent investigation stage ID is set on the new row (it is already known from the parallel path; no extra lookup is required). Chat context building switches from name-based backward scans to a direct FK read: for each synthesis stage with `referenced_stage_id` set, map final analysis to that parent. After migration backfill, there is no name-based fallback — a single FK-based code path.

### Migration

1. **Schema:** Add nullable `referenced_stage_id` with FK (ON DELETE SET NULL).
2. **Backfill:** For existing synthesis rows, set `referenced_stage_id` from the legacy name convention (same session, investigation stage whose name matches the synthesis name with the ` - Synthesis` suffix removed, choosing the highest `stage_index` below the synthesis among matches). Embed the backfill in the same migration as the column so data is ready before consumers rely on the FK.

This mirrors the `stage_type` backfill approach in ADR-0004.

### API / WebSocket exposure

Expose `referenced_stage_id` (nullable) on stage summaries in session detail, WebSocket stage status payloads, and trace responses. This is additive for clients and supports showing stage relationships without a later breaking API change.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | ORM / schema approach | Self-referential FK with edge | Consistent with how other stage foreign keys are modeled. ON DELETE SET NULL. |
| Q2 | Backfill strategy | Backfill in the same migration as the column | Same pattern as ADR-0004 `stage_type` backfill. Enables dropping name-based pairing entirely. |
| Q3 | Name-based fallback | FK-only, no fallback | Backfill covers existing data. Single code path, no dead fallback code. |
| Q4 | API/WS exposure | Expose in responses | Additive field, low cost. Avoids a future API change. Same reasoning as `stage_type` in ADR-0004 Q3. |
| Q5 | Chat stage scoping | Not applicable | Chats are session-scoped. `referenced_stage_id` is NULL for chat stages. |
