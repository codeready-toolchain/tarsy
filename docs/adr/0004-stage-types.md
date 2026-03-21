# ADR-0004: Stage Types

**Status:** Implemented  
**Date:** 2026-03-04

## Overview

TARSy alert sessions are composed of stages, each representing a set of LLM interactions with some outcome. Today there are already multiple kinds of stages — investigation, synthesis, chat — but the schema has no explicit field to distinguish them. The kinds are identified through fragile heuristics: nullable FK checks (`chat_id IS NOT NULL`) and string suffix matching (`" - Synthesis"`).

This proposal adds a `stage_type` enum field to the Stage schema so every stage carries an explicit type. This removes heuristic-based classification from the codebase and enables direct filtering, simpler context building, and richer API and WebSocket responses.

Additionally, executive summary generation is refactored from a special-cased direct LLM call into a typed stage, unifying all LLM-driven activities under the stage model.

**In scope:** data model and API/WS shape for stage type; wiring on creation paths; chat context building using explicit types; executive summary as a first-class `exec_summary` stage.

**Out of scope:** UI changes, scoring pipeline (see [ADR-0008: Session Scoring](0008-session-scoring.md)).

**Follow-up:** Synthesis stages reference their "parent" stages by name. That remains fragile and would benefit from a `referenced_stage_id` FK. See [ADR-0005](0005-referenced-stage-id.md). This proposal keeps name-based pairing for synthesis adjacency.

## Design Principles

1. **Explicit over implicit.** Replace heuristic classification with a declarative field.
2. **Single source of truth.** The `stage_type` column is the canonical way to identify stage kind — no more suffix checks or FK inference.
3. **Consistent with existing patterns.** Follow the same schema enum pattern used by stage status, parallel type, and success policy.
4. **Composable context filtering.** Stage types enable query-level filtering for building different contexts (investigation, chat, scoring).

## Architecture

### Stage Type Enum

Five values:

| Value | Description | Created by |
|-------|-------------|------------|
| `investigation` | From chain config stage | Session executor (default) |
| `synthesis` | Auto-generated after multi-agent investigation | Synthesis path |
| `chat` | User follow-up chat message | Chat path |
| `exec_summary` | Executive summary of the investigation | Session executor (after refactor) |
| `scoring` | Quality evaluation | Scoring path (reserved) |

All values exist in the schema from the initial rollout; `exec_summary` and `scoring` gain creation paths when their features are wired. Stage creation remains internal — executors call the stage service; the enum provides schema-level validation.

Stage types enable composable context filtering:

| Need | Stage types included |
|------|---------------------|
| Build next-stage context | `investigation`, `synthesis` |
| Build chat context | `investigation`, `synthesis`, `chat` |
| Build scoring context | `investigation`, `synthesis`, `exec_summary` |
| Main timeline view | `investigation`, `synthesis` |
| Full session view | all |

### DB Schema

Add a **required `stage_type` enum column** on stages with default `investigation`. Every row has exactly one type; the default covers the common investigation path without special-casing that creator.

No index on `stage_type` for the initial design — sessions typically have few stages and load by session; an index can be added later if cross-session filtering appears.

### Migration (high level)

Add the column with default `investigation` so existing rows classify correctly immediately. **Backfill** existing synthesis stages (identified by the established name convention) and chat stages (identified by non-null chat linkage) so their `stage_type` matches reality before relying on the field everywhere. Investigation rows need no manual backfill beyond the default.

### Chat context builder

With an explicit type field, chat context construction uses a **type switch** instead of suffix checks and chat-id inference for "what kind of stage is this." Synthesis **pairing** (which investigation a synthesis belongs to) still uses the existing name-derived relationship until a structural FK exists ([ADR-0005](0005-referenced-stage-id.md)).

### Executive Summary Refactoring

The prior executive summary path was a **one-off**: direct LLM call outside the agent/controller framework, custom timeline sequencing, manual progress publishing, inline provider and fallback handling, and results denormalized on the session.

Refactoring moves it into a **typed `exec_summary` stage** with a normal stage record and agent execution, so ordering, status, and interactions align with the rest of the product.

**Why `SingleShotController` fits (no new controller category):** Executive summary is one system + user message pair, one LLM call, one text result — the same shape as other single-shot work, with standard message storage, timeline, and interaction recording. Differences vs synthesis (static template prompts vs config-heavy synthesis assembly, no thinking fallback, distinct interaction labeling) are **configuration** on the single-shot pattern, not a different control-flow structure. A dedicated controller would only be justified if requirements later add multi-turn behavior, custom retry/extraction, or lifecycle hooks the single-shot config cannot express.

**Compatibility and behavior:** Sentinel sequence numbers and hand-rolled timeline types for new executive summary work go away in favor of stage indexing and normal events. **Existing** enum values for legacy rows (e.g. historical interaction or timeline types) remain valid for backward compatibility. The session-level `executive_summary` field stays as a denormalized copy for list views and notifications. **Fail-open** behavior is preserved: if the exec summary stage fails, the session still completes with error surfaced on the session fields as today. Chain-level `executive_summary_provider` resolution continues to apply via the resolved agent config for that stage. Context builders that should only see investigation + synthesis for "main narrative" filtering gain **explicit** type guards so `exec_summary` and `scoring` cannot leak in accidentally if call sites change.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Where to define `StageType` | Schema-generated constants only | Consistent with other stage enums. No duplication. Not a user-facing config concept — duplicating in application config would wrongly imply configurability. |
| Q2 | DB index on `stage_type` | No index | Low cardinality, no cross-session queries in the first version. Stages load per-session. Can add later if needed. |
| Q3 | `stage_type` in stage status WS payload | Yes | Payload should be self-describing. Consistent with REST API. |
| Q4 | Synthesis pairing in chat context | Replace identification-only heuristics with `stage_type`; keep name-based pairing | Name convention is reliable today. Structural FK is [ADR-0005](0005-referenced-stage-id.md). Adjacency-only pairing would be fragile. |
| Q5 | Backfill migration | Single schema migration with embedded data backfill | One step, standard pattern. Avoids permanent startup backfill code. |
| Q6 | Delivery granularity | Split additive model/API work from executive-summary behavioral refactor | Separates low-risk schema exposure from the path change for exec summary. |
