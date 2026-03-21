# ADR-0013: Review Workflow Feedback Redesign

**Status:** Implemented
**Date:** 2026-03-20

## Overview

The current review workflow captures `resolution_reason` (`actioned`/`dismissed`) and an optional `resolution_note` when a human resolves a session. These fields describe **what the human did about the alert**, but say nothing about **whether TARSy's investigation was accurate**.

This redesign replaces those fields with three orthogonal signals:

| Field | Type | Purpose |
|-------|------|---------|
| `quality_rating` | enum: `accurate` / `partially_accurate` / `inaccurate` | Was the investigation correct? **Required** at review completion. |
| `action_taken` | optional text | What the human did about the alert |
| `investigation_feedback` | optional text | Why the investigation was good or bad |

Additionally, the terminal review status is renamed from `resolved` to `reviewed` to reflect the shifted purpose — the reviewer is assessing investigation quality, not resolving an incident.

This is a prerequisite for the [Investigation Memory](../proposals/investigation-memory-sketch.md) feature, which needs an unambiguous signal of investigation quality to determine whether extracted memories represent patterns to repeat or patterns to avoid.

## Design Principles

1. **Investigation quality is the primary signal.** The main purpose of human review is to assess TARSy's investigation accuracy — not to track the alert's lifecycle.
2. **Orthogonal fields for orthogonal concepts.** Alert resolution (what the human did) and investigation quality (how well TARSy performed) are independent axes.
3. **Terminology matches purpose.** The status `reviewed` and action `complete` reflect that the reviewer is completing a quality assessment, not resolving an incident.
4. **Safe migration.** Existing data preserved via `resolution_note` → `action_taken` copy. Enum values renamed in-place. `quality_rating` = `accurate` for human-reviewed sessions (reasonable default — if a human reviewed it under the old workflow, the investigation was implicitly accepted). System-auto-completed cancelled sessions and unreviewed sessions stay NULL.

## Terminology Changes

| Concept | Old | New | Rationale |
|---------|-----|-----|-----------|
| Terminal review status | `resolved` | `reviewed` | The reviewer assessed the investigation, not resolved the alert |
| Action to finish review | `resolve` | `complete` | "Complete the review" → status `reviewed`. Using `review` would collide with the workflow name |
| Timestamp | `resolved_at` | `reviewed_at` | Follows status rename |
| Action to edit after completion | `update_note` | `update_feedback` | Broader scope: rating + summary + feedback, not just a note |
| Quality signal (enum) | `resolution_reason` (actioned/dismissed) | `quality_rating` (accurate/partially_accurate/inaccurate) | Investigation quality, not alert lifecycle |
| Alert outcome (text) | `resolution_note` | `action_taken` | What the human did about the alert — no "resolution" terminology |
| Investigation feedback (text) | *(new)* | `investigation_feedback` | Why the investigation was good or bad |

## What Does NOT Change

The claim/assign mechanism is untouched. `claim`, `unclaim`, and reassignment actions remain as-is — including the `assignee`, `assigned_at` fields, the `needs_review` / `in_progress` status values, and the `investigating` triage group. Only the terminal review actions change (`resolve` → `complete`, `update_note` → `update_feedback`) along with the fields they write.

## What Changes

### Database schema

**AlertSession:**

| Remove | Add |
|--------|-----|
| `resolution_reason` (enum: actioned/dismissed) | `quality_rating` (enum: accurate/partially_accurate/inaccurate, optional, nillable) |
| `resolution_note` (text, optional, nillable) | `action_taken` (text, optional, nillable) |
| `resolved_at` (time, optional, nillable) | `reviewed_at` (time, optional, nillable) |
| `review_status` enum value `resolved` | `review_status` enum value `reviewed` |
| | `investigation_feedback` (text, optional, nillable) |

**SessionReviewActivity:**

| Remove | Add |
|--------|-----|
| `resolution_reason` (enum: actioned/dismissed) | `quality_rating` (enum: accurate/partially_accurate/inaccurate, optional, nillable) |
| `action` enum value `resolve` | `action` enum value `complete` |
| `action` enum value `update_note` | `action` enum value `update_feedback` |
| `from_status` / `to_status` enum value `resolved` | `from_status` / `to_status` enum value `reviewed` |
| | `investigation_feedback` (text, optional, nillable) |

The activity log's `note` field is unchanged — it continues to capture the `action_taken` text.

### Migration

Data-preserving migration. Enum renames are done in-place (add new value, update rows, drop old). **`alert_sessions`:** rename `reviewed` status value; add new nullable columns; copy `resolution_note` → `action_taken`; set `quality_rating` = `accurate` where status is reviewed and an assignee exists (human-reviewed); rename `resolved_at` → `reviewed_at`; drop old resolution columns. **`session_review_activities`:** rename action and status enum values as above; add `quality_rating` and `investigation_feedback`; drop `resolution_reason`.

Human-reviewed sessions with an assignee get `quality_rating` = `accurate` as a backward-compatible default. System-auto-completed cancelled sessions (no assignee) and never-reviewed rows keep `quality_rating` NULL — consistent with how the worker sets the field going forward.

### API layer

**Update review request** — session IDs, action, optional `quality_rating`, `action_taken`, and `investigation_feedback` (actor supplied server-side). Validation: for `complete`, `quality_rating` is required and must be a valid enum value; text fields optional. For `update_feedback`, at least one of the three fields must be present; if `quality_rating` is sent, it must be valid.

Review actions are `claim`, `unclaim`, `complete`, `reopen`, `update_feedback`. **ReviewActivityItem** swaps `resolution_reason` for `quality_rating`, adds `investigation_feedback`, keeps `note` as the action summary snapshot. **Dashboard and triage DTOs** expose `QualityRating`, `ActionTaken`, and `InvestigationFeedback` instead of resolution reason/note. **Dashboard list filters** use `quality_rating` instead of `resolution_reason`. **WebSocket review status payloads** carry the new fields so the UI can update triage rows without refetch. **Triage group keys** use `reviewed` instead of `resolved`.

### Service layer

Complete-review handling sets `quality_rating` (required), optional `action_taken` and `investigation_feedback`, status `reviewed`, and `reviewed_at`. Reopen clears quality fields and timestamp and returns the session to `needs_review`. `update_feedback` updates any subset of the three fields on already-reviewed sessions without touching omitted fields. Activity logging records `complete` with quality and feedback as appropriate. Triage and dashboard queries select and filter on the new columns and reviewed status.

### Worker: auto-completed cancelled sessions

Cancelled sessions are still auto-marked reviewed to keep the triage queue clean, but with **no human quality judgment**: `quality_rating`, `action_taken`, and `investigation_feedback` stay NULL; `reviewed_at` is set. Published review events use `reviewed` without a quality rating.

### Frontend

**UX goal: low-friction rating.** The old flow (click Resolve → modal → pick radio → submit) should be replaced with something faster — e.g. three inline rating controls per row (accurate / partially accurate / inaccurate, color-coded) plus an easy path for optional text. Exact interaction (popover vs lightweight modal) is implementation detail.

**Components** — the complete-review flow presents required `quality_rating` and optional `action_taken` / `investigation_feedback`. Triage rows show quality badges instead of actioned/dismissed; group and action constants move from `resolved` / resolve to `reviewed` / complete. The edit flow renames to feedback editing and passes all three current values when opening the modal. Dashboard triage grouping, actions, and WebSocket handling follow the same terminology and payload shape. TypeScript types align with the API.

## Decisions Summary

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Keep `resolution_reason`? | **Drop entirely** — `quality_rating` + `action_taken` cover both signals | `action_taken` free text is more expressive than a binary enum; `quality_rating` replaces it as the structured filter |
| Q2 | Migration strategy | **Data-preserving** — copy `resolution_note` → `action_taken`, set `quality_rating=accurate` for human-reviewed sessions (`assignee IS NOT NULL`), drop old columns | Preserves human-written notes; reasonable default for existing reviews; system-auto-completed and unreviewed sessions stay NULL |
| Q3 | `quality_rating` required? | **Yes** — same friction as today, far more useful signal | Guarantees every human-reviewed session has a quality signal for the memory feature; `not_assessed` escape hatch can be added later if needed |
| Q4 | Post-resolve editing | **Rename `update_note` → `update_feedback`** — single action for all three fields | Already a breaking API change, so rename is free; single action keeps the API simple |
| Q5 | Frontend field requirements | **Only `quality_rating` required** — optional text fields with guiding placeholders | Low friction (one mandatory click); voluntary text is higher quality than forced text |
| — | Status rename | **`resolved` → `reviewed`** — terminology matches the shifted purpose | |
| — | Action rename | **`resolve` → `complete`** — "complete the review" produces `reviewed` status | |
