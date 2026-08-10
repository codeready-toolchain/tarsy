# Session Deep Links — Design Questions

**Status:** All decisions made  
**Related:** [Design document](session-deep-links-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: What should a deep link primarily target?

“Share a chat” is ambiguous in TARSy: there is **one Chat per session**, and UI “follow-up chats” are **stages** (one per user turn). We need a primary anchor granularity.

### Option C: Both — `stage` required, optional `event`

`?stage=…` for the turn; optional `&event=…` for a specific item inside it.

- **Pro:** Covers the common case and the precise case without two systems.
- **Pro:** Stage alone still works when event is omitted.
- **Pro:** Event focus reuses existing search scroll/expand (`data-flow-item-id` + stage collapse overrides) — low incremental cost on top of stage linking.
- **Con:** Slightly more URL/API surface and resolution logic.

**Decision:** Option C — stage is the primary share target; optional `event` for precision. Event-level is cheap because in-session search already expands stages/items and scrolls via `[data-flow-item-id]`.

_Considered and rejected: Option A (stage-only — enough for now but leaves easy precision on the table), Option B (event-only — too granular as the sole model; stage is the natural “chat turn” unit)._

---

## Q2: How should the deep link be encoded in the URL?

### Option A: Query parameters (`?stage=` / optional `&event=`)

```
/sessions/{id}?stage={stageId}
/sessions/{id}?stage={stageId}&event={timelineEventId}
```

- **Pro:** First-class in React Router (`useSearchParams`); easy to build/test.
- **Pro:** Composes cleanly with Q1’s optional `event` param.
- **Pro:** Visible, copy-paste friendly.
- **Con:** Slightly noisier URLs; params linger unless we strip/replace them.

**Decision:** Option A — query parameters for `stage` and optional `event`.

_Considered and rejected: Option B (hash — weaker composition for two params / React Router), Option C (nested path — fights `/trace` and `/scoring`, implies a resource page we don’t have)._

---

## Q3: Where should users copy the link from?

Given Q1 (stage + optional event), this is about **which UI surfaces expose Copy link**, and the matching open/focus behavior.

### Option B′: Stage + event copy controls, with explicit focus rules

**Copy surfaces**
- **Stage link** on the stage separator and/or chat user question → `?stage=` only.
- **Event link** on individual timeline items within the stage → `?stage=&event=`.
- User-question “copy link” uses the **stage** URL (same landing for chat; more resilient than pinning the event id).

**Open / focus behavior**
- **Stage link:** expand timeline + stage; scroll/focus to the **first real timeline event** in the stage (for chat: the user question). Do not embed that event id in the stage URL — resolve client-side.
- **Event link:** expand timeline + stage; expand the target event if collapsible; scroll/focus to that event.
- If `event` is missing/invalid but `stage` is valid: fall back to stage behavior (toast details deferred to Q6).

- **Pro:** Clear mental model for both share granularities from Q1.
- **Pro:** Reuses existing search expand/scroll patterns (`data-flow-item-id`, stage collapse overrides).
- **Pro:** Stage URLs stay stable if in-stage event order shifts.
- **Con:** Need distinct “copy link” vs “copy content” affordances on items that already have CopyButton.

**Decision:** Option B′ — stage and event copy controls with the focus rules above. (Q4 later chose all stages.)

_Considered and rejected: Option A (stage-only copy UI — leaves event param undiscoverable), Option C (separator-only — weak “share this message”), Option D (overflow-only — poor discoverability)._

---

## Q4: Should investigation stages be linkable in the same way as chat?

Related to Q1/Q3 — product asked “any stage?” in parentheses.

### Option B: All stages from day one

Stage and event copy controls (per Q3) on every timeline stage — data gathering, synthesis, chat, etc. Same open/focus rules everywhere.

- **Pro:** One mechanism; answers “any stage?” immediately.
- **Pro:** Stage ids already exist for investigation stages — little extra logic beyond chat.
- **Con:** Slightly broader UX polish (copy control on every separator / eligible items).

**Decision:** Option B — all stages linkable from day one with the same stage/event model.

_Considered and rejected: Option A (chat-only — leaves investigation stages unshareable), Option C (chat-first UI with generic machinery — unnecessary delay given controls are the same)._

---

## Q5: Should the URL update as the user browses the timeline, or only when they copy?

### Option A: Copy-only (URL changes only when Copy link is used / when opening a shared link)

- **Pro:** Simple; doesn’t fight auto-scroll or collapse toggles.
- **Pro:** Browser history stays clean.
- **Con:** Address bar doesn’t always reflect “where I’m looking”.

**Decision:** Option A — copy-only; no live URL sync while browsing. Opening a shared link still applies focus params; Copy link builds the share URL explicitly.

_Considered and rejected: Option B (live sync — scroll-spy complexity and history noise beyond the sharing need)._

---

## Q6: What should happen if the target stage/event is missing?

Deep links can go stale (deleted session content, wrong env, typo). Partial failure also matters: valid `stage`, invalid `event` (already noted in Q3 → fall back to stage focus).

### Option B: Open session + non-blocking toast (“Linked stage not found”)

- **Pro:** Clear feedback without blocking the page.
- **Con:** Minor UX surface to implement/test.

Behavior:
- Unknown stage → toast, show session unfocused.
- Known stage, unknown event → toast, apply stage focus (first event).

**Decision:** Option B — non-blocking toast; never hard-fail the session page for a bad anchor.

_Considered and rejected: Option A (silent ignore — recipient may not notice), Option C (hard error — hostile when the session is still useful)._

---

## Q7: When a deep link opens, should we change layout (e.g. jump past executive summary)?

Terminal sessions can show executive summary / final analysis above a collapsed timeline. Chat turns live inside the timeline. Q3 already defined expand + scroll focus; this question is only whether to alter page layout beyond that.

### Option A: Minimal — expand timeline + target stage, then `scrollIntoView`

- **Pro:** Reuses existing layout; least surprising.
- **Pro:** Matches current search-match scrolling and Q3 focus rules.
- **Con:** User may briefly see summary before scroll settles.

**Decision:** Option A — keep the normal session layout; expand + scroll only. No special deep-link layout mode.

_Considered and rejected: Option B (hide/collapse summary for deep links — two layouts for the same session)._
