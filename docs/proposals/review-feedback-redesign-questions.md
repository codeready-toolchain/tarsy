# Review Workflow Feedback Redesign — Questions

**Status:** Complete — all decisions made
**Related:** [Design document](review-feedback-redesign-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Should `resolution_reason` (actioned/dismissed) be kept alongside the new fields?

The current `resolution_reason` describes what the human did about the **alert** (actioned vs. dismissed). The new `quality_rating` describes how well TARSy **investigated**. These are orthogonal: a dismissed alert can have an accurate investigation (TARSy correctly identified a false positive). The question is whether the alert-lifecycle signal is still worth keeping as a structured enum.

### Option A: Drop `resolution_reason` entirely, replace with `action_taken` free text

The [memory sketch](investigation-memory-sketch.md) proposed this approach. What the human did about the alert is captured in `action_taken` as free text.

- **Pro:** Cleaner schema — three new fields with clear roles, no leftover from old design.
- **Pro:** `action_taken` is more expressive than a binary enum — "scaled pod to 5 replicas, ticket INFRA-1234" beats just "actioned."
- **Con:** Loses the structured `actioned`/`dismissed` enum used for dashboard filtering (`DashboardListParams.ResolutionReason`).
- **Con:** Breaking change for any external integrations consuming the API (Slack notifications, Grafana dashboards that reference `resolution_reason`).

**Decision:** Option A — drop `resolution_reason` entirely. The binary enum adds no value when `action_taken` is more expressive, and `quality_rating` replaces it as the structured enum for filtering.

_Considered and rejected: Option B (keep `resolution_reason` alongside new fields — overlapping fields, heavy resolve form), Option C (replace enum-for-enum — still a breaking change anyway, and `quality_rating` + `action_taken` covers both signals cleanly)._

---

## Q2: How should existing reviewed sessions be migrated?

There are existing reviewed sessions with `resolution_reason` and `resolution_note` data. The new schema removes these columns and adds `quality_rating`, `action_taken`, and `investigation_feedback`. The `resolved` status is renamed to `reviewed`.

### Option B: Data-preserving migration — copy `resolution_note` → `action_taken`, rename enums in-place, then drop old columns

Migration that: (1) renames enum values (`resolved`→`reviewed`, `resolve`→`complete`, `update_note`→`update_feedback`), (2) adds new columns, (3) copies `resolution_note` into `action_taken`, (4) sets `quality_rating=accurate` for human-reviewed sessions (`assignee IS NOT NULL` — system-auto-completed cancelled sessions have no assignee and stay NULL), (5) renames `resolved_at`→`reviewed_at`, (6) drops old columns. Unreviewed sessions keep `quality_rating` NULL.

- **Pro:** Preserves the free-text notes humans wrote.
- **Pro:** Human-reviewed sessions get `quality_rating=accurate` as a reasonable default; system-auto-completed and unreviewed sessions stay NULL.
- **Con:** More complex migration (enum renames + data copy + column rename).

**Decision:** Option B — data-preserving migration. Enum values renamed in-place, `resolution_note` copied to `action_taken`, `quality_rating` set to `accurate` for human-reviewed sessions (`assignee IS NOT NULL`), old columns dropped. System-auto-completed cancelled sessions and unreviewed sessions keep `quality_rating` NULL.

_Considered and rejected: Option A (destructive — loses human-written resolution notes), Option C (keep old columns deprecated — schema bloat, dual-schema complexity, "eventually" cleanup)._

---

## Q3: Should `quality_rating` be required when completing a review?

The old `resolution_reason` was required to resolve. Should the new `quality_rating` follow the same pattern?

### Option A: Required when completing a review

Every `complete` action must include a `quality_rating`. Matches the old behavior where `resolution_reason` was mandatory.

- **Pro:** Guarantees every human-reviewed session has a quality signal — critical for the memory feature.
- **Pro:** Simple validation rule — same as today.
- **Pro:** Forces reviewers to think about investigation quality, not just close the ticket.
- **Con:** Reviewers who just want to close a session quickly must pick a rating, potentially leading to noisy data.

**Decision:** Option A — required when completing a review. Same friction as today (one mandatory choice), but the signal is far more useful. If reviewer fatigue becomes an issue, `not_assessed` can be added later as a backward-compatible enum extension.

_Considered and rejected: Option B (optional — undermines the whole point), Option C (`not_assessed` escape hatch — risks becoming default click-through, can be added later if needed)._

---

## Q4: How should post-review editing work?

Currently, `update_note` allows editing `resolution_note` after a session is resolved. With the new schema, there are three fields that might need post-review editing.

### Option C: Rename `update_note` to `update_feedback`, allow updating all three fields

The action name changes from `update_note` to `update_feedback`.

- **Pro:** Clear rename — `update_feedback` better describes the broader scope.
- **Pro:** Backward incompatible anyway (fields changed), so renaming the action is free.
- **Con:** Any code referencing `update_note` string literal needs updating.

**Decision:** Option C — rename `update_note` to `update_feedback`. Already a breaking API change, so the rename is free. Single action for all three fields keeps the API simple.

_Considered and rejected: Option A (same behavior, just didn't rename — miss the chance to clarify semantics), Option B (two separate actions — over-granular, reviewers often update rating and feedback together)._

---

## Q5: Should the frontend require all three fields or allow partial submission?

When completing a review, the UI presents the three new fields. The question is which are required vs. optional in the form UX.

### Option A: Only `quality_rating` required, both text fields optional

Radio group for rating is mandatory. Both text fields (`action_taken`, `investigation_feedback`) are optional with inline placeholder text (grey text inside the field, replaced when the user starts typing).

- **Pro:** Low friction — one mandatory click plus optional context.
- **Pro:** Matches the backend requirement (Q3 Option A) exactly.
- **Pro:** Reviewers who have something to say will say it; those who don't aren't forced to write filler.

**Decision:** Option A — only `quality_rating` required. One mandatory click, two optional text fields with inline placeholder text. Voluntary text is higher quality than forced text.

_Considered and rejected: Option B (require `investigation_feedback` — leads to low-quality "ok" filler), Option C (all required — significant friction, guaranteed noise)._
