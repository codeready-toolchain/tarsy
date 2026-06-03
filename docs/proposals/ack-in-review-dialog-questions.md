# Move Acknowledge into Review Dialog — Questions

**Status:** Resolved — all decisions made  
**Related:** [Design document](ack-in-review-dialog-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How should "Acknowledge" be presented in the review dialog?

The modal currently has a `RadioGroup` with three quality ratings. Acknowledge is semantically different — it's not a quality judgment. How it's visually integrated affects whether users perceive it as "a fourth rating" or "a separate action."

### Option A: Fourth radio option in the same RadioGroup

Add "Acknowledge" as a fourth `FormControlLabel` in the existing `RadioGroup`, visually separated (e.g., with a divider above it).

- **Pro:** Simple implementation — just another radio option. Users scan one list of choices.
- **Pro:** Clear that you must pick exactly one outcome before submitting.
- **Con:** May conflate "I'm rating quality" with "I'm choosing not to rate." The grouping implies they're the same category.

### Option B: Separate section below the ratings with its own button

Keep the 3-option `RadioGroup` for quality ratings. Below (or above), add a distinct "Acknowledge" section with its own submit button (e.g., "Just Acknowledge — no quality judgment").

- **Pro:** Visually distinct — makes it clear acknowledge is a different kind of action.
- **Pro:** Can use a secondary/outlined button style to signal it's the lightweight path.
- **Con:** Two submit paths in one dialog can confuse. Users might not notice it.
- **Con:** More complex layout, especially on mobile.

### Option C: Fourth radio option with conditional UI changes

Like Option A (same RadioGroup), but when "Acknowledge" is selected, the feedback fields fade/collapse and the submit button label changes to "Acknowledge" (from "Complete Review").

- **Pro:** Single selection mechanism — no confusion about which button to press.
- **Pro:** Adaptive UI makes it obvious that acknowledge skips the detailed feedback step.
- **Pro:** Maintains the "pick one, then submit" mental model.
- **Con:** Slightly more complex than a plain fourth radio (conditional rendering).

**Decision:** Option C — with a descriptive subtitle on the "Acknowledge" radio option (matching the pattern of the other three ratings) to clarify that it means "no quality judgment." E.g., *"I've reviewed this but won't judge investigation quality"*.

_Considered and rejected: Option A (conflates rating with non-rating in a flat list), Option B (dual-submit paths confuse users)_

---

## Q2: What happens to bulk acknowledge?

Currently, bulk "Ack All" is a toolbar button in `TriageGroupedList` for `needs_review` and `in_progress` groups. Without a standalone ack button, bulk ack needs a new home.

### Option C: Merge "Ack All" and "Complete All" into a single "Review All" button

Single bulk button opens the modal with all options (3 ratings + acknowledge). One dialog, one decision applied to all selected.

- **Pro:** Reduces toolbar buttons. Cleaner UI.
- **Pro:** Consistent with the per-session experience.
- **Con:** Same "extra click" concern as Option B for pure acknowledge.

**Decision:** Option C — merge both bulk buttons into a single "Review All" that opens the modal (with all 4 options including Acknowledge). Consistent with per-session flow. "Complete All" already required a modal, so the only net cost is that bulk-ack gains one extra click.

_Considered and rejected: Option A (inconsistent — keeps a separate ack path for bulk only), Option B (same outcome as C but without consolidating the toolbar buttons)_

---

## Q3: Should ack from the modal still auto-claim unclaimed sessions?

**Decision:** No change needed. The backend already auto-claims on submit (not on modal open). This behavior applies to both `doComplete` and `doAcknowledge` and remains unchanged by this proposal.

---

## Q4: Snackbar and undo behavior after acknowledging via modal

**Decision:** No change needed. Keep existing differentiated snackbars (triage view only) — wire them to the modal outcome instead of the button source. "Acknowledged" + Undo for ack, rating chip + "Add note" + Undo for complete.

---

## Q5: Impact on FinalAnalysisCard inline review shortcuts

`FinalAnalysisCard` on the session detail page has inline thumb up/down icon buttons that open the review modal with a pre-selected rating (`initialRating`). Should it also get an "acknowledge" shortcut?

### Option A: Add an acknowledge icon button to FinalAnalysisCard

Add a neutral icon (e.g., `DoneAll` or `CheckCircle`) alongside the thumbs that opens the review modal with "Acknowledge" pre-selected.

- **Pro:** Consistency — acknowledge is available everywhere review is.
- **Pro:** Quick path for detail-page reviewers who just want to ack.
- **Con:** Adds visual clutter to the analysis card. Three buttons → four.

**Decision:** Option A — add an acknowledge shortcut icon to `FinalAnalysisCard` for consistency. Acknowledge should be available everywhere review is.

_Considered and rejected: Option B (inconsistency between surfaces), Option C (loses quick-rate convenience for power users)_
