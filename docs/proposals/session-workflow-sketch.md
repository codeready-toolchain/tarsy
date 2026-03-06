# Session Workflow — Action-Oriented Alert Management

**Status:** Sketch complete — ready for detailed design
**Decisions:** [session-workflow-questions.md](session-workflow-questions.md)

## Problem

TARSy automates incident investigation: alerts arrive, AI agent chains run, findings are produced. But today, the dashboard is a **monitoring view** — it shows what the system is doing, not what the human needs to do. Once the AI finishes, the session sits in a "completed" list with no further lifecycle.

For an SRE team using TARSy in production, this creates a gap:

- **No triage signal.** There's no way to see which completed investigations need human attention versus which are informational noise.
- **No ownership.** If two SREs are on-call, there's no way to claim a session so others know it's handled.
- **No resolution tracking.** After reviewing findings and taking action (applying a fix, updating a runbook, filing a ticket), there's no way to mark the work as done.
- **No workflow visibility.** A team lead can't glance at a board and see: 2 investigating, 5 awaiting review, 1 being worked on, 12 resolved today.

The investigation lifecycle ends at "completed." The human response lifecycle doesn't exist.

## Goal

Add a lightweight human workflow layer on top of TARSy's existing automated investigation lifecycle. This gives SRE teams an action-oriented view: **what needs my attention, who's handling what, and what's done.**

This is not an incident management system (PagerDuty, incident.io already do that). It's scoped to TARSy's unique value: AI produces investigation findings → human reviews and acts on them. Teams that use TARSy purely as an investigation engine with external ticketing can ignore the workflow entirely — the existing session list remains unchanged.

## How It Relates to Existing Concepts

| Existing concept | Relationship |
|---|---|
| **Session status** (`pending`, `in_progress`, `completed`, `failed`, …) | Untouched. This is the *investigation* lifecycle. The new workflow tracks the *human response* after investigation completes. Active investigations (`pending`, `in_progress`) appear in the Triage view as a virtual read-only column. |
| **Session authorization** (projects, RBAC) | Workflow respects project boundaries. You can only claim sessions in projects you have access to. |
| **Session scoring** | Scoring evaluates investigation quality. Workflow tracks whether a human has reviewed and acted on findings. Orthogonal but complementary — a low score might signal "needs attention." |
| **Author** | The person who submitted the alert. Distinct from **assignee** (the person reviewing the findings). |

## Key Concepts

### Review Status (the human workflow)

A `review_status` field on `alert_sessions`, independent of the existing `status` field. Two separate dimensions: `status` tracks the automated investigation lifecycle, `review_status` tracks the human response.

- `NULL` — session is still being investigated (`status IN (pending, in_progress)`). Not in the review workflow yet.
- `needs_review` — investigation reached a terminal state. Awaiting human attention.
- `in_progress` — someone claimed it and is actively reviewing/acting on findings.
- `resolved` — done. The SRE reviewed the findings and either took action or dismissed it.

All sessions reaching a terminal investigation status (`completed`, `failed`, `timed_out`) automatically get `review_status = needs_review`. Cancelled sessions skip directly to `review_status = resolved` with `resolution_reason = dismissed`.

### Resolution Reason

When resolving a session, a `resolution_reason` captures the outcome — separating "why it's done" from the workflow state itself:

- `actioned` — findings reviewed, action taken (fix applied, ticket filed, runbook updated).
- `dismissed` — not actionable (false positive, noise, duplicate, already handled elsewhere).

An optional resolution note provides free-text context. Both are recorded in the review activity log.

### Assignee

The person who claimed the session for review. Set via self-claim ("Claim" button) using the `X-Forwarded-User` identity from oauth2-proxy. Stored as a text field on the session.

Self-claim only for now — no assigning to others (TARSy has no user registry). Extend to mutual assignment when a user model is introduced.

### Review Activity Log

A `session_review_activity` table logs all workflow transitions and human feedback:

- State transitions (who, when, from-state → to-state)
- Resolution reason and note
- Claim/unclaim events
- Foundation for future: re-open history, comments, human feedback on investigation quality

This follows the scoring pattern: denormalized current state on the session (fast queries), full history in a related table.

## Dashboard Integration

### Tab structure

Two tabs on the main dashboard page: **Sessions** | **Triage**

- **Sessions** — the current flat session list, unchanged. Sortable, filterable, catalog/history view. Default tab. `review_status` and `assignee` available as optional filter/column but not prominent.
- **Triage** — the action-oriented workflow view. Two sub-layouts switchable via toggle:
  - **Grouped list** — existing table style grouped by review status, collapsible sections with counts.
  - **Kanban board** — columns per review state, session cards with quick actions.

Last-used tab and layout preference persisted in `localStorage`.

### Triage view columns

| Column | Source | Actionable? |
|---|---|---|
| **Investigating** | `status IN (pending, in_progress)`, `review_status IS NULL` | No — virtual, read-only. Shows active AI investigations. |
| **Needs Review** | `review_status = needs_review` | Yes — claim, start review. |
| **In Progress** | `review_status = in_progress` | Yes — resolve. |
| **Resolved** | `review_status = resolved` | No — done. Filterable by resolution reason. |

## Scope

### In scope

- `review_status`, `assignee`, `resolution_reason`, `resolution_note` fields on `alert_sessions`
- `session_review_activity` table for transition history and human feedback
- API endpoints for workflow transitions (claim, unclaim, update review status, resolve with reason)
- Extended query params for filtering by `review_status`, `assignee`
- Triage tab with both grouped list and Kanban layouts
- WebSocket events for real-time workflow updates

### Out of scope

- Notifications (Slack, email, push) — valuable but separate feature
- Escalation policies (auto-reassign after timeout) — future enhancement
- SLA tracking (time-to-acknowledge, time-to-resolve) — future enhancement
- Integration with external incident management tools — future enhancement
- Auto-triage rules (automatically set review status based on scoring, alert type, etc.) — future enhancement
- Assign to others (requires user registry) — extend when user model exists

## Behavior

1. Alert arrives → AI investigation runs (session visible in Triage "Investigating" column)
2. Session reaches terminal state (`completed`, `failed`, `timed_out`) → `review_status` automatically set to `needs_review`. Cancelled sessions skip to `resolved` (reason: `dismissed`) automatically.
3. SRE claims the session → `assignee` set, `review_status` moves to `in_progress`
4. SRE reviews findings, executive summary, and scoring
5. SRE takes action outside TARSy (applies fix, files ticket, updates runbook)
6. SRE resolves the session with a resolution reason and optional note → `review_status = resolved`
7. Session moves to the "Resolved" column/section

### Edge cases

- **Unclaim:** SRE can unclaim a session, returning it to `needs_review` with no assignee.
- **Failed/timed-out sessions:** Enter the workflow as `needs_review` — someone should review what went wrong. A **Re-submit** quick action is available on these cards (same as the existing re-submit on the session detail page — navigates to the submit form with pre-filled alert data).
- **Cancelled sessions:** Auto-resolved with reason `dismissed` — someone already decided to stop the investigation.
- **Re-open:** A resolved session can be moved back to `needs_review` (logged in activity table).

## Technical Notes

### Database Changes

- New fields on `alert_sessions`: `review_status` (enum, nullable), `assignee` (string, nullable), `assigned_at` (time, nullable), `resolved_at` (time, nullable), `resolution_reason` (enum, nullable), `resolution_note` (text, nullable)
- New `session_review_activity` table: `id`, `session_id`, `actor`, `action` (enum), `from_status`, `to_status`, `resolution_reason`, `note`, `created_at`
- Indexes: `review_status`, `assignee`, `(review_status, assignee)`

### API Changes

- `PATCH /api/v1/sessions/:id/review` — update review status (claim, unclaim, resolve with reason/note, re-open)
- Extended query params on `GET /api/v1/sessions`: `review_status`, `assignee`
- `GET /api/v1/sessions/:id/review-activity` — activity log for a session
- WebSocket events for review status changes

### Frontend Changes

- Tab bar on dashboard: Sessions | Triage
- Triage tab with layout toggle (grouped list / Kanban)
- Assignee badge on session items
- Virtual "Investigating" column from active sessions

#### Interaction model: drag-and-drop + contextual action buttons

Two complementary interaction patterns — buttons are always available, drag-and-drop adds speed in the Kanban layout.

**Action buttons on cards/rows:**

| Action | Type | Behavior |
|---|---|---|
| **Claim** | Primary button, Needs Review cards | Single click, no confirmation. Sets assignee and moves to In Progress. |
| **Resolve** | Primary button, In Progress cards | Opens compact modal/popover: resolution reason picker (`actioned` / `dismissed`) + optional note field. |
| **Re-submit** | Secondary button, failed/timed-out cards | Navigates to submit form with pre-filled alert data (existing mechanism). |
| **Unclaim** | Context menu (three-dot) | Returns to Needs Review, clears assignee. |
| **Re-open** | Context menu (three-dot), Resolved cards | Moves back to Needs Review. |

**Drag-and-drop (Kanban view only):**

- Drag between columns to trigger the same transitions as buttons.
- Constrained to horizontal movement between columns.
- When dropping into "Resolved," the resolve modal pops for reason + note.
- Investigating column is read-only — no drag out.
- Optimistic UI update with backend sync.

**Keyboard shortcuts (power users):**

- `C` — claim focused card.
- `R` — resolve focused card (opens modal).
- Arrow keys to navigate between cards.
