# Review Workflow Feedback Redesign

**Status:** Final — all decisions made, see [review-feedback-redesign-questions.md](review-feedback-redesign-questions.md)

## Overview

The current review workflow captures `resolution_reason` (`actioned`/`dismissed`) and an optional `resolution_note` when a human resolves a session. These fields describe **what the human did about the alert**, but say nothing about **whether TARSy's investigation was accurate**.

This redesign replaces those fields with three orthogonal signals:

| Field | Type | Purpose |
|-------|------|---------|
| `quality_rating` | enum: `accurate` / `partially_accurate` / `inaccurate` | Was the investigation correct? **Required** at review completion. |
| `action_taken` | optional text | What the human did about the alert |
| `investigation_feedback` | optional text | Why the investigation was good or bad |

Additionally, the terminal review status is renamed from `resolved` to `reviewed` to reflect the shifted purpose — the reviewer is assessing investigation quality, not resolving an incident.

This is a prerequisite for the [Investigation Memory](investigation-memory-sketch.md) feature, which needs an unambiguous signal of investigation quality to determine whether extracted memories represent patterns to repeat or patterns to avoid.

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

**`AlertSession`** (`ent/schema/alertsession.go`):

| Remove | Add |
|--------|-----|
| `resolution_reason` (enum: actioned/dismissed) | `quality_rating` (enum: accurate/partially_accurate/inaccurate, optional, nillable) |
| `resolution_note` (text, optional, nillable) | `action_taken` (text, optional, nillable) |
| `resolved_at` (time, optional, nillable) | `reviewed_at` (time, optional, nillable) |
| `review_status` enum value `resolved` | `review_status` enum value `reviewed` |
| | `investigation_feedback` (text, optional, nillable) |

**`SessionReviewActivity`** (`ent/schema/sessionreviewactivity.go`):

| Remove | Add |
|--------|-----|
| `resolution_reason` (enum: actioned/dismissed) | `quality_rating` (enum: accurate/partially_accurate/inaccurate, optional, nillable) |
| `action` enum value `resolve` | `action` enum value `complete` |
| `action` enum value `update_note` | `action` enum value `update_feedback` |
| `from_status` / `to_status` enum value `resolved` | `from_status` / `to_status` enum value `reviewed` |
| | `investigation_feedback` (text, optional, nillable) |

The activity log's `note` field is unchanged — it continues to capture the `action_taken` text.

### Migration

Data-preserving migration. All enum renames are done in-place (add new value, update rows, drop old value).

**`alert_sessions`:**
1. Rename `review_status` enum value `resolved` → `reviewed` (add value, update rows, drop old)
2. Add `quality_rating`, `action_taken`, `investigation_feedback` columns (nullable)
3. Copy `resolution_note` → `action_taken` for existing rows
4. Set `quality_rating` = `accurate` for rows where `review_status` = `reviewed` AND `assignee IS NOT NULL` (human-reviewed sessions get a reasonable default; system-auto-completed cancelled sessions have no assignee and stay NULL)
5. Rename column `resolved_at` → `reviewed_at`
6. Drop columns `resolution_reason`, `resolution_note`

**`session_review_activities`:**
7. Rename `action` enum value `resolve` → `complete` (add value, update rows, drop old)
8. Rename `action` enum value `update_note` → `update_feedback` (add value, update rows, drop old)
9. Rename `from_status` / `to_status` enum value `resolved` → `reviewed` (add value, update rows, drop old)
10. Add `quality_rating`, `investigation_feedback` columns (nullable)
11. Drop column `resolution_reason`

`quality_rating` = `accurate` for human-reviewed sessions (`assignee IS NOT NULL` — if a human reviewed it, the investigation was implicitly accepted). System-auto-completed cancelled sessions (`assignee IS NULL`) and unreviewed sessions stay NULL — consistent with how the worker sets `quality_rating = NULL` going forward.

### API layer

**`UpdateReviewRequest`** (`pkg/models/session.go`):

```go
type UpdateReviewRequest struct {
    SessionIDs            []string `json:"session_ids"`
    Action                string   `json:"action"`
    Actor                 string   `json:"-"`
    QualityRating         *string  `json:"quality_rating,omitempty"`
    ActionTaken           *string  `json:"action_taken,omitempty"`
    InvestigationFeedback *string  `json:"investigation_feedback,omitempty"`
}
```

Validation:
- `complete` action: `quality_rating` is **required**, must be one of `accurate`, `partially_accurate`, `inaccurate`. Text fields are optional.
- `update_feedback` action: at least one field must be non-nil. `quality_rating`, if provided, must be a valid value.

**Review actions** (`pkg/models/session.go`): Full set: `claim`, `unclaim`, `complete`, `reopen`, `update_feedback`.

**`ReviewActivityItem`**: Replace `resolution_reason` with `quality_rating`. Add `investigation_feedback`. `note` remains as the resolution summary snapshot.

**`DashboardSessionItem`** and **`triageRow`**: Replace `ResolutionReason`/`ResolutionNote` with `QualityRating`, `ActionTaken`, `InvestigationFeedback`.

**`DashboardListParams`**: Replace `ResolutionReason` filter with `QualityRating` filter (enum: accurate/partially_accurate/inaccurate).

**`ReviewStatusPayload`** (WebSocket event, Go + TS): Replace `ResolutionReason` with `QualityRating`. Add `ActionTaken` and `InvestigationFeedback` so the frontend can live-update triage rows without a refetch. Comment updates for `reviewed` status.

**Triage group key**: `resolved` → `reviewed` in `TriageGroupKey` constants and `ParseTriageGroupKey`.

### Service layer

**`doResolve` → `doComplete`**: Set `quality_rating` (required), `action_taken`, `investigation_feedback`. Set `review_status=reviewed`, `reviewed_at=now`.

**`doReopen`** (name unchanged): Clear `quality_rating`, `action_taken`, `investigation_feedback`, `reviewed_at`. Transition from `reviewed` → `needs_review`.

**`validateReviewRequest`**: For `complete`, validate `quality_rating` is present and a valid enum value. For `update_feedback`, validate at least one field is non-nil and `quality_rating` (if present) is valid.

**`update_feedback` action** (replaces `update_note`): Can update any combination of `quality_rating`, `action_taken`, `investigation_feedback` on already-reviewed sessions. Omitted fields are not changed.

**`insertActivity`**: Signature changes — `resolutionReason` parameter replaced with `qualityRating` and `investigationFeedback`. Action value is `complete` (was `resolve`).

**Triage query** (`queryTriageGroup`): Select `quality_rating`, `action_taken`, `investigation_feedback` instead of `resolution_reason`, `resolution_note`. Group predicate uses `ReviewStatusReviewed`.

**Dashboard list query** (`ListSessionsForDashboard`): Replace `resolution_reason` filter and select with `quality_rating`. Note: the `dashboardRow` scan struct currently only has `ResolutionReason` (not `ResolutionNote`); add `QualityRating` as its replacement. The triage query (`triageRow`) has the full set — `QualityRating`, `ActionTaken`, `InvestigationFeedback` — since triage rows display all three fields.

### Worker: auto-completed cancelled sessions

Currently, cancelled sessions are auto-resolved with `resolution_reason=dismissed`. With the new schema:
- `review_status` = `reviewed` (still auto-completed to keep triage queue clean)
- `quality_rating` = NULL (system-completed, no human judgment)
- `action_taken` = NULL
- `investigation_feedback` = NULL
- `reviewed_at` = now

The worker no longer needs to set a resolution reason. The `publishReviewStatus` payload uses `reviewed` status with no `quality_rating`.

### Frontend

**UX goal: low-friction rating.** The old flow (click Resolve → modal → pick radio → submit) should be replaced with something faster. The current direction is three inline rating icons per row (accurate/partially accurate/inaccurate, color-coded green/yellow/red) that let the reviewer express the quality signal with minimal clicks and mouse travel, plus an easy path to add optional text feedback. Exact interaction pattern (popover vs. lightweight modal vs. other) to be finalized during frontend implementation.

**`ResolveModal` → `CompleteReviewModal`**: Redesigned to present `quality_rating` (required), `action_taken` (optional text with inline placeholder), and `investigation_feedback` (optional text with inline placeholder). May be replaced or supplemented by a lighter inline interaction — see UX goal above.

**`TriageSessionRow`**: Replace `actioned`/`dismissed` badges with quality rating badges (color-coded: green/yellow/red) and inline rating icons for unreviewed rows. Group key `'resolved'` → `'reviewed'`. `resolution_note` → `action_taken`.

**`EditNoteModal` → `EditFeedbackModal`**: Supports editing `quality_rating`, `action_taken`, and `investigation_feedback` for already-reviewed sessions. Action changes to `update_feedback`. Props change from `initialNote: string` to `initialQualityRating`, `initialActionTaken`, `initialInvestigationFeedback` — parent components (`TriageSessionRow`, `TriageGroupedList`, `DashboardView`) must pass all three current values when opening the modal.

**`TriageGroupedList`**: Rename `resolved` group to `reviewed` in group config, `SELECTABLE_GROUPS` set, and empty-state initializer.

**`TriageView`**: `resolveSessionIds` → `completeSessionIds`. Opens `CompleteReviewModal` instead of `ResolveModal`.

**`DashboardView`**: `TRIAGE_GROUPS` key `'resolved'` → `'reviewed'`. Action `REVIEW_ACTION.RESOLVE` → `REVIEW_ACTION.COMPLETE`. WebSocket handler updates for new payload shape.

**TypeScript types**: Update `UpdateReviewRequest`, `ReviewActivityItem`, `DashboardSessionItem`, `ReviewStatusPayload` to match new API contract. `TriageGroupKey` includes `'reviewed'` instead of `'resolved'`. `REVIEW_ACTION` adds `COMPLETE` and `UPDATE_FEEDBACK`, removes `RESOLVE` and `UPDATE_NOTE`.

## Affected Files — Comprehensive List

### Ent Schemas (manual edits, then `go generate`)

| File | Changes |
|------|---------|
| `ent/schema/alertsession.go` | `review_status` enum: `resolved`→`reviewed`; rename `resolved_at`→`reviewed_at`; drop `resolution_reason`, `resolution_note`; add `quality_rating`, `action_taken`, `investigation_feedback` |
| `ent/schema/sessionreviewactivity.go` | `action` enum: `resolve`→`complete`, `update_note`→`update_feedback`; `from_status`/`to_status` enum: `resolved`→`reviewed`; drop `resolution_reason`; add `quality_rating`, `investigation_feedback` |

### Ent Generated Code (auto-regenerated via `go generate`)

All files regenerated automatically — no manual edits:

`ent/alertsession/alertsession.go`, `ent/alertsession/where.go`, `ent/alertsession.go`, `ent/alertsession_create.go`, `ent/alertsession_update.go`, `ent/sessionreviewactivity/sessionreviewactivity.go`, `ent/sessionreviewactivity/where.go`, `ent/sessionreviewactivity.go`, `ent/sessionreviewactivity_create.go`, `ent/sessionreviewactivity_update.go`, `ent/mutation.go`, `ent/migrate/schema.go`

### Migration

| File | Changes |
|------|---------|
| New migration `.up.sql` | All enum renames, column adds/drops, data copy (see Migration section above) |

### API Models

| File | Changes |
|------|---------|
| `pkg/models/session.go` | `ReviewActionResolve`→`ReviewActionComplete`; `ReviewActionUpdateNote`→`ReviewActionUpdateFeedback`; `ValidReviewAction` switch; `UpdateReviewRequest` fields; `DashboardSessionItem` fields; `DashboardListParams.ResolutionReason`→`QualityRating`; `DashboardListParams.ReviewStatus` comment (`resolved`→`reviewed`); `ReviewActivityItem` fields; `TriageGroupResolved`→`TriageGroupReviewed`; `ParseTriageGroupKey` switch |
| `pkg/models/session_test.go` | Update `TestValidReviewAction` table |

### Service Layer

| File | Changes |
|------|---------|
| `pkg/services/session_service_review.go` | `validateReviewRequest`; `doResolve`→`doComplete`; action switch cases; all `ReviewStatusResolved`→`ReviewStatusReviewed`; `SetResolvedAt`→`SetReviewedAt`; `ClearResolvedAt`→`ClearReviewedAt`; drop `Resolution{Reason,Note}` setters/clearers; add `QualityRating`/`ActionTaken`/`InvestigationFeedback` setters/clearers; `insertActivity` signature; `triageGroupPredicates`; `triageRow` struct; `queryTriageGroup` SELECT columns |
| `pkg/services/session_service.go` | `dashboardRow` struct; `ListSessionsForDashboard` filter + SELECT; mapping to `DashboardSessionItem` |
| `pkg/services/session_service_review_test.go` | All review test assertions, enum values, action strings, triage group keys |
| `pkg/services/session_service_test.go` | Review-related assertions in dashboard list tests |

### API Handlers

| File | Changes |
|------|---------|
| `pkg/api/handler_review.go` | Validation for `complete` action (`quality_rating` required); event payload construction (`QualityRating` instead of `ResolutionReason`); activity item mapping |
| `pkg/api/handler_session.go` | Query param `resolution_reason`→`quality_rating` with updated validator |
| `pkg/api/handler_review_test.go` | Action strings, validation tests, triage path `resolved`→`reviewed` |
| `pkg/api/handler_session_test.go` | Invalid `quality_rating` test (was `resolution_reason`) |

### Events

| File | Changes |
|------|---------|
| `pkg/events/payloads.go` | `ReviewStatusPayload`: `ResolutionReason`→`QualityRating`; add `ActionTaken`, `InvestigationFeedback`; comment updates for `reviewed` |
| `pkg/events/publisher_test.go` | Payload JSON assertions: `resolved`→`reviewed`, `resolution_reason`→`quality_rating`; new field assertions |

### Worker

| File | Changes |
|------|---------|
| `pkg/queue/worker.go` | Cancelled sessions: `ReviewStatusResolved`→`ReviewStatusReviewed`; `SetResolvedAt`→`SetReviewedAt`; remove `SetResolutionReason(Dismissed)`; `publishReviewStatus` payload updates |
| `pkg/queue/worker_test.go` | Assertions: `resolved`→`reviewed`, remove `dismissed` assertion |
| `pkg/queue/integration_test.go` | `ReviewStatusResolved`→`ReviewStatusReviewed`; `ResolvedAt`→`ReviewedAt` |

### Agent (interfaces + test stubs)

| File | Changes |
|------|---------|
| `pkg/agent/context.go` | `PublishReviewStatus` interface — signature unchanged, but docstring if it mentions `resolved` |
| `pkg/agent/controller/streaming_test.go` | Noop stub — no functional change |
| `pkg/agent/orchestrator/runner_test.go` | Noop stub — no functional change |
| `pkg/queue/executor_integration_test.go` | Noop stub — no functional change |

### Frontend Types

| File | Changes |
|------|---------|
| `web/dashboard/src/types/session.ts` | `DashboardSessionItem`: `resolution_reason`/`resolution_note` → `quality_rating`/`action_taken`/`investigation_feedback` |
| `web/dashboard/src/types/api.ts` | `TriageGroupKey`: `'resolved'`→`'reviewed'`; `REVIEW_ACTION`: `RESOLVE`→`COMPLETE`, `UPDATE_NOTE`→`UPDATE_FEEDBACK`; `UpdateReviewRequest`: `resolution_reason`→`quality_rating`, add `action_taken`, `investigation_feedback`; `ReviewActivityItem`: `resolution_reason`→`quality_rating`, add `investigation_feedback` |
| `web/dashboard/src/types/events.ts` | `ReviewStatusPayload`: `resolution_reason`→`quality_rating`; add `action_taken`, `investigation_feedback` |

### Frontend Components

| File | Changes |
|------|---------|
| `web/dashboard/src/components/dashboard/ResolveModal.tsx` | Rename file → `CompleteReviewModal.tsx`; redesign form: quality rating radio + two optional text fields; props rename; action name in callback |
| `web/dashboard/src/components/dashboard/EditNoteModal.tsx` | Rename file → `EditFeedbackModal.tsx`; support editing all three fields; action `update_feedback` |
| `web/dashboard/src/components/dashboard/DashboardView.tsx` | `TRIAGE_GROUPS` key `'resolved'`→`'reviewed'`; `REVIEW_ACTION.RESOLVE`→`REVIEW_ACTION.COMPLETE`; `resolution_reason`→`quality_rating` in complete handler; WS `review.status` handler |
| `web/dashboard/src/components/dashboard/TriageGroupedList.tsx` | Group key `'resolved'`→`'reviewed'`; display label |
| `web/dashboard/src/components/dashboard/TriageSessionRow.tsx` | Group `'resolved'`→`'reviewed'`; `resolutionReasonConfig`→quality rating badge config (green/yellow/red); `resolution_note`→`action_taken`; `onEditNote`→`onEditFeedback` |
| `web/dashboard/src/components/dashboard/TriageView.tsx` | `resolveSessionIds`→`completeSessionIds`; import `CompleteReviewModal`; title updates |

### E2E Tests

| File | Changes |
|------|---------|
| `test/e2e/review_workflow_test.go` | WS assertions `resolved`→`reviewed`; action `resolve`→`complete`; `resolution_reason`→`quality_rating`; triage group `resolved`→`reviewed`; auto-reviewed cancelled path |
| `test/e2e/helpers.go` | `PatchReview` body updates; `GetTriageGroup` group name |
| `test/e2e/testdata/expected_events.go` | WS event field expectations |

### Documentation

| File | Changes |
|------|---------|
| `docs/functional-areas-design.md` | Schema table, workflow description |
| `docs/architecture-overview.md` | Triage workflow description |
| `README.md` | API action list (`resolve`→`complete`, `update_note`→`update_feedback`) |

## Implementation Plan

### Phase 1: Backend schema + migration - DONE

1. Update Ent schemas (`alertsession.go`, `sessionreviewactivity.go`) — all field/enum changes per schema tables above
2. Generate Ent code (`go generate`)
3. Create migration (`make migrate-create`) and review per db-migration-review skill
4. Edit migration to include: enum value renames (add new → update rows → drop old), column rename (`resolved_at`→`reviewed_at`), data-copy (`resolution_note`→`action_taken`), `quality_rating=accurate` backfill for human-reviewed sessions (`assignee IS NOT NULL`), column drops
5. Update API models (`pkg/models/session.go`) — all constants, request/response types, triage group key
6. Update service layer (`session_service_review.go`, `session_service.go`) — all function renames, field references, query changes
7. Update handlers (`handler_review.go`, `handler_session.go`) — validation, event publishing, query params
8. Update event payloads (`pkg/events/payloads.go`)
9. Update worker (`pkg/queue/worker.go`) — auto-complete for cancelled sessions
10. Update all backend tests (service, handler, worker, e2e, event)
11. Update documentation (ADR, functional areas, architecture, README)

### Phase 2: Frontend - DONE

1. Update TypeScript types (`types/api.ts`, `types/session.ts`, `types/events.ts`)
2. Rename `ResolveModal` → `CompleteReviewModal` with new three-field layout
3. Rename `EditNoteModal` → `EditFeedbackModal` with all three fields
4. Update `TriageSessionRow` — quality rating badges, group key
5. Update `TriageGroupedList` — group key and label
6. Update `TriageView` — state names, modal imports
7. Update `DashboardView` — triage groups, action handling, WebSocket handler

## Decisions Summary

| # | Question | Decision |
|---|----------|----------|
| Q1 | Keep `resolution_reason`? | **Drop entirely** — `quality_rating` + `action_taken` cover both signals |
| Q2 | Migration strategy | **Data-preserving** — copy `resolution_note` → `action_taken`, set `quality_rating=accurate` for human-reviewed sessions (`assignee IS NOT NULL`), drop old columns |
| Q3 | `quality_rating` required? | **Yes** — same friction as today, far more useful signal |
| Q4 | Post-resolve editing | **Rename `update_note` → `update_feedback`** — single action for all three fields |
| Q5 | Frontend field requirements | **Only `quality_rating` required** — optional text fields with guiding placeholders |
| — | Status rename | **`resolved` → `reviewed`** — terminology matches the shifted purpose |
| — | Action rename | **`resolve` → `complete`** — "complete the review" produces `reviewed` status |
