# Referenced Stage ID — Proposal

**Status:** Idea — not yet designed
**Origin:** Identified during [stage-types](stage-types-design.md) investigation (Q4)

## Problem

Synthesis stages are linked to their parent investigation stage by a **naming convention only**. The executor creates a synthesis stage named `"{ParentStageName} - Synthesis"`, and downstream code (e.g. `buildChatContext`) pairs them by stripping the suffix and scanning backward by name.

This is fragile:

- Relies on a string convention that's not enforced at the schema level.
- Pairing logic uses backward name scanning — correct today but could break if naming conventions evolve.
- No structural way to query "which investigation stage does this synthesis belong to?"

## Proposed Solution

Add an optional `referenced_stage_id` FK column to the `stages` table, pointing to another stage in the same session.

| Stage type | `referenced_stage_id` | Purpose |
|---|---|---|
| `investigation` | NULL | No parent |
| `synthesis` | Points to parent investigation stage | Replaces name-based pairing |
| `chat` | Optionally points to a specific stage | Scope a follow-up question to one stage |
| `exec_summary` | NULL | Summarizes entire session |
| `scoring` | NULL | Evaluates entire session via stage-type filtering |

## Scope

- Add optional `referenced_stage_id` FK to stages schema
- Set it when creating synthesis stages (executor_synthesis.go)
- Replace name-based pairing in `buildChatContext` with FK lookup
- Optionally allow chat stages to reference a specific stage

## Why Not Now

This is orthogonal to both the stage-types work and the scoring pipeline. The current name-based convention works reliably. This is a cleanup/robustness improvement, not a blocker for any current feature.
