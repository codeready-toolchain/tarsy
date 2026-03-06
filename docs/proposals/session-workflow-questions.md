# Session Workflow — Sketch Questions

**Status:** Open — decisions pending
**Related:** [Sketch document](session-workflow-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: Where does the review lifecycle live?

The existing `status` field tracks the automated investigation lifecycle (`pending` → `in_progress` → `completed`). The new review workflow tracks what happens after. These are fundamentally different concerns, but there's a question of whether they should be modeled separately or merged.

### Option A': Hybrid — fields on session + activity table (chosen)

Add `review_status` and `assignee` as indexed fields directly on `alert_sessions` for fast filtered queries (the workflow view). Add a separate `session_review_activity` table for transition history, resolution notes, and human feedback.

- **Pro:** No risk of breaking existing status logic — `status` and `review_status` are independent dimensions.
- **Pro:** Fast list queries — no JOIN needed for the workflow view (filter/group by `review_status`, `assignee` directly).
- **Pro:** History and comments have a natural home in the activity table without cluttering the session schema.
- **Pro:** Follows the scoring pattern: denormalized "current state" on the session (like `executive_summary`), full data in a related table (like `session_scores`).
- **Pro:** Foundation for future enhancements — resolution notes, human feedback, re-open history, assignment changes all log to the activity table.

**Decision:** Option A' (hybrid). Fields on session for current state + `session_review_activity` table for history and human feedback.

_Considered and rejected: Option B — extending `status` enum (conflates automated and human lifecycles, breaks executor logic). Option C — separate review entity only (mandatory JOIN for every list query, no denormalized current state for fast filtering). Option A — fields only (no natural place for history, resolution notes, or future human feedback)._

---

## Q2: What view paradigm for the workflow dashboard?

The current dashboard is a flat session list. The workflow needs a way to show "what needs attention" at a glance. But TARSy may also be used purely as an investigation engine with an external ticketing system — the current list view must remain a first-class citizen, not degraded by workflow features.

### Option D': Additive hybrid — keep current list + new workflow tab (chosen)

Two top-level tabs on the dashboard:

1. **Sessions** — the current flat list, unchanged. Sortable, filterable, no workflow concepts forced on it. The "catalog" / history view for teams that use external ticketing or don't need review workflow.
2. **Triage** — the action-oriented view with two sub-layouts (grouped list and Kanban), both shipping from day one.

- **Pro:** Purely additive — existing list view doesn't change. Teams that ignore workflow features see no difference.
- **Pro:** Clean separation of concerns — "what has the system done" (Sessions) vs. "what do I need to do" (Triage).
- **Pro:** `review_status` and `assignee` can optionally appear as columns/filters in the Sessions list too, but aren't prominent.

All three layouts ship together:
1. **Sessions tab** — current flat list (unchanged).
2. **Workflow tab, grouped list layout** — table grouped by review status, collapsible sections with counts.
3. **Workflow tab, Kanban layout** — columns per review state, session cards, drag-and-drop or quick actions.

A sub-toggle within the Workflow tab switches between grouped list and Kanban. User preference persisted in `localStorage`.

**Decision:** Option D' (additive hybrid). Current list preserved as "Sessions" tab. New "Workflow" tab ships with both grouped list and Kanban layouts from day one.

_Considered and rejected: Option A — Kanban only (less dense, doesn't replace the list for catalog use). Option B — inbox only (no visual state distribution). Option C — grouped list only (no Kanban path). Option D — hybrid without preserving the current list (forces workflow concepts on all users)._

---

## Q3: How does assignment work?

When a session needs review, someone needs to own it. The question is whether assignment is push (someone assigns you) or pull (you grab it yourself), or both.

### Option A: Self-claim only (chosen)

SREs pick sessions from the unassigned pool by clicking "Claim." No one can assign sessions to others.

- **Pro:** Simple model. No need for user management, team rosters, or assignment permissions.
- **Pro:** Respects autonomy — people choose their own work.
- **Pro:** Works well for small teams where everyone is equally capable.
- **Pro:** Matches current reality — TARSy has no user registry, so there's nobody to assign to.
- **Con:** Doesn't work for team leads who need to distribute work.
- **Con:** Risk of "bystander effect" — everyone waits for someone else to claim.

Claiming sets `assignee` to the current user's identity (from `X-Forwarded-User`). Unclaim resets it to null.

**Decision:** Option A — self-claim only. TARSy currently has no user registry or awareness of other users, so assigning to others isn't feasible. Extend to Option C (both claim and assign) when a user registry or session authorization with OIDC groups lands.

_Considered and rejected: Option B — assign to others only (bottleneck, requires user registry). Option C — both claim and assign (requires user registry that doesn't exist yet; natural evolution once users are modeled)._

---

## Q4: What are the review workflow states?

The review states define the columns/groups in the workflow view. Too few and the workflow is too coarse; too many and it becomes ceremony.

### Option A': Minimal states + resolution reason (chosen)

Three workflow states: `needs_review` → `in_progress` → `resolved`

When resolving, a `resolution_reason` captures the outcome: `actioned`, `dismissed`, `false_positive`, `duplicate`, `not_applicable`, etc. This follows the Jira model — status is the workflow position, resolution is the outcome type.

- **Pro:** Dead simple workflow — 3 Kanban columns, clean state machine.
- **Pro:** "Dismissed" is a *reason* for resolving, not a separate workflow state. Both "I acted on this" and "this was noise" end up in the same terminal state.
- **Pro:** New resolution reasons can be added without changing the workflow or Kanban layout.
- **Pro:** The "resolved" column/section can be filtered by reason if needed (e.g., show only actioned items).
- **Pro:** Resolution reason naturally lives alongside the resolution note in the `session_review_activity` table.

**Decision:** Option A' — three states (`needs_review`, `in_progress`, `resolved`) plus a `resolution_reason` field when resolving. The resolution reason and an optional resolution note are recorded in the review activity log.

_Considered and rejected: Option B — PagerDuty-style "acknowledged" (wrong semantics for reviewing AI findings). Option C — `dismissed` as a separate workflow state (dismissed is a sub-type of resolved, not a distinct workflow position). Option D — 5+ states (overkill, the AI already did the investigation)._

---

## Q5: Which sessions enter the workflow?

Not every session necessarily needs human review. The question is which sessions get a review status and appear in the workflow view — and what about sessions still being investigated.

### Option A+: All terminal sessions automatically + virtual "Investigating" column (chosen)

Two layers:

1. **Active investigations** (`status IN (pending, in_progress)`, `review_status IS NULL`): shown in the workflow view as a lightweight virtual "Investigating" column/section. Read-only — no review actions, just awareness of incoming work. Sessions move out of this column automatically when the investigation completes.

2. **Terminal sessions** (`status IN (completed, failed, timed_out)`): automatically get `review_status = needs_review`. These are the actionable items in the workflow.

The Kanban board has 4 visual columns, but only 3 are review states:

| Column | Source | Actionable? |
|---|---|---|
| **Investigating** | `status IN (pending, in_progress)`, `review_status IS NULL` | No — virtual, read-only |
| **Needs Review** | `review_status = needs_review` | Yes — claim, start review |
| **In Progress** | `review_status = in_progress` | Yes — resolve |
| **Resolved** | `review_status = resolved` | No — done |

- **Pro:** Nothing falls through the cracks. Every terminal investigation enters the review queue.
- **Pro:** SREs see the full picture — "2 running, 5 need my attention, 1 being worked on, 12 resolved."
- **Pro:** Clean separation maintained — `status` drives the virtual column, `review_status` drives the rest.
- **Pro:** The `dismissed` resolution reason (Q4) is the escape valve for noise.

**Decision:** Option A+ — all terminal sessions enter as `needs_review` automatically. Active investigations appear in a virtual read-only "Investigating" column derived from session `status`. Rule-based filtering (auto-triage) can be layered in later.

_Considered and rejected: Option B — opt-in (things get missed). Option C — rule-based entry (premature, we don't know what rules teams want yet). Option D — variable defaults (same as A without rules)._

---

## Q6: Where does the workflow view live in the dashboard?

The current dashboard has one main page (session list). The workflow view needs a home.

### Option C: Tabs on the existing dashboard (chosen)

The dashboard page gets a tab bar: **"Sessions"** | **"Triage"**. Same page, different perspective. The Sessions tab (current flat list) remains the default. User's last-used tab is persisted in `localStorage`.

- **Pro:** Everything in one place. Users switch context without navigating away.
- **Pro:** Shared components (filters, session cards) reduce duplication.
- **Pro:** Natural evolution of the current dashboard — additive, not disruptive.
- **Pro:** Default is the current view — no surprise for existing users.

**Decision:** Option C — tabs on the existing dashboard. Sessions tab is the default; last-used tab persisted in `localStorage`.

_Considered and rejected: Option A — replace default (breaks existing users). Option B — separate route (splits attention, navigation overhead)._

---

## Q7: How do we identify users for assignment?

Assignment requires knowing who the users are. TARSy currently gets usernames from `X-Forwarded-User` (oauth2-proxy) but has no user registry. Given Q3 (self-claim only), there's no "assign to others" UI to populate.

### Option A: Use `X-Forwarded-User` header value directly (chosen)

Store the assignee as the raw string from the auth proxy header. No user table. Assignee is just a text field. When a user clicks "Claim," TARSy stores their `X-Forwarded-User` value.

- **Pro:** Zero infrastructure. Works with existing auth setup.
- **Pro:** Consistent with how `author` is already stored on sessions.
- **Pro:** Sufficient for self-claim — no dropdown or autocomplete needed.

**Decision:** Option A — raw `X-Forwarded-User` header value as the `assignee` text field. Sufficient for self-claim. Revisit when "assign to others" is added (Q3 evolution).

_Considered and rejected: Option B — user registry (premature, significant effort). Option C — observed users list (unnecessary for self-claim, useful later for "assign to others")._
