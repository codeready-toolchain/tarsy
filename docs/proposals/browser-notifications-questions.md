# Browser notifications — Design Questions

**Status:** All decisions made
**Related:** [Design document](browser-notifications-design.md)

Decisions are recorded below. The design document reflects them.

---

## Q1: Can this notify after the dashboard tab is closed?

The tab counter only exists while a tab is open. Desktop toasts are a separate channel. `new Notification()` runs in the page, so it fires for a background tab and stops when the last dashboard tab closes. Reaching a machine with no dashboard tab means Web Push: a service worker, VAPID keys, and stored subscriptions. TARSy has none of that today. Slack already posts a terminal message outside the dashboard.

### Option A: Background tab only

The page calls `new Notification()` while a dashboard tab is open. The favicon and title carry the counter.

- **Pro:** Uses the websocket and list API already in the dashboard. No server changes, no service worker.
- **Pro:** Matches the request for a counter on the tab icon, which requires the tab.
- **Con:** Closing the tab, or quitting the browser, stops both signals until the dashboard is opened again.
- **Con:** Most mobile browsers throw on the `Notification` constructor. The counter still works; the OS toast does not.

**Decision:** Option A — a dashboard tab left open is enough. A service-worker toast only helps after that tab is closed, or on Android, and that is not worth a service worker.

_Considered and rejected: Option B (service-worker toasts while a tab is open — same UX for a tab that stays open, plus a sticky worker), Option C (Web Push — server subscriptions and VAPID for a case Slack already covers)._

---

## Q2: Where do notification settings live?

Someone has to store "enabled" and the label list. The dashboard already stores filters, sort, and the nav rail in `localStorage` (`tarsy-*` keys). There is no user-preference API. Login via oauth2-proxy is optional; without it the server has no user id.

### Option A: This browser only

`localStorage`, same as the other dashboard preferences.

- **Pro:** Works with and without login. No schema, no API.
- **Pro:** A personal choice on a shared wallboard browser stays on that browser.
- **Con:** A second browser, or a cleared profile, starts from the defaults. The user sets the labels again.

**Decision:** Option A — same place as the other dashboard preferences, and it works when login is not configured.

_Considered and rejected: Option B (server-side per user — needs a user id and a new preference API), Option C (deployment YAML — one label list for everyone, and it puts paging policy in TARSy config)._

---

## Q3: Which sessions qualify?

"Completed and not reviewed yet" can be read narrowly or as the whole triage queue. Today, completed, failed, and timed-out sessions all enter `needs_review`. Cancelled sessions are stored as `reviewed` and would not match an unreviewed filter. Claiming a session moves it to `in_progress` until someone completes the review.

### Option A: Completed and still in `needs_review`

`status=completed` and `review_status=needs_review`.

- **Pro:** Matches the request: a finished investigation nobody has claimed.
- **Pro:** One list query already expresses it. Claiming a session drops it from the counter, which is what the on-call wants once a person owns it.
- **Con:** A failed or timed-out investigation sits in Triage and does not change the counter. A claimed-but-unfinished review does not either.

**Decision:** Option A — a finished investigation that nobody has claimed yet. Claiming it drops it from the counter.

_Considered and rejected: Option B (also count `in_progress` — the counter would stay up after someone has taken it), Option C (also failed and timed-out runs — a different signal from a completed investigation)._

---

## Q4: What happens when no labels are selected?

Labels are optional in the catalog. A session may have `null` (parse failed or summary skipped), `[]` (no label applied), or one or more names. The list API ORs a comma-separated `label` parameter and does not match `null` or `[]` when a label is set.

### Option D: One autocomplete, default `ban` and `action`

A single multi-select. No second mode switch.

Suggestions are the catalog (`label_maps` on `GET /api/v1/system/config`) plus names already stored on sessions (`filter-options`). The user can also type a label that is not listed. Typed text must match the existing label token rule, `[A-Za-z][A-Za-z0-9_-]*`. Two pinned rows sit above the names:

- **Any label** — the session has a non-empty `labels` array, whatever the names are.
- **No label** — `labels` is `null` or `[]`.

Specific names are OR. **No label** stacks with them. **Any label** already covers every name, so choosing it clears specific names; adding a specific name clears **Any label**. **Any label** and **No label** together mean every qualifying session. An empty field matches nothing.

Default selection on a fresh browser is the two chips `ban` and `action`, and not **No label**. The builtin catalog is `watch` / `action` / `noise` and does not define `ban`; `ban` is still the default chip because a custom name is allowed. Sessions match `ban` only when that name is actually stored.

`GET /api/v1/sessions?label=` cannot express "these names OR unlabeled" or "any non-empty labels". The list handler gains a small filter for that. The watcher does not page the whole unreviewed backlog down to the browser to apply it locally.

- **Pro:** One field. Defaults to the narrow set, and can be widened to unlabeled sessions or to every session without another control.
- **Pro:** A name can be chosen before any session has carried it, and before it exists in the catalog.
- **Con:** **Any label** and specific names replace each other. The list API needs the extra filter.

**Decision:** Option D — one autocomplete, default chips `ban` and `action`. The user can add catalog names, stored names, or a typed name, and can include unlabeled sessions, every labeled session, or both.

_Considered and rejected: Option A (empty selection means every session — the default would not be `ban` and `action`), Option B (empty selection means off, and unlabeled sessions can never match), Option C (a separate All / Only-these radio — a second control for the same field)._

---

## Q5: What does the tab counter count?

The number on the favicon, in `(N) TARSy`, and on the header button should mean one thing. It has to go down when the queue shrinks, including when a different person reviews the session on another machine.

### Option A: Live backlog

The number is `total_items` for the qualifier. It includes sessions that completed before this tab was opened. It drops when a session is claimed or reviewed (per Q3), and it is unchanged by focusing the tab.

- **Pro:** Glancing at the tab answers "how many are waiting?"
- **Pro:** The list endpoint already returns the number. No extra unread-cursor.
- **Con:** A pinned tab can sit at `(12)` for a shift if nobody reviews. That is the queue, and it can feel sticky.

**Decision:** Option A — the tab shows how many matching sessions are waiting right now, including ones that finished before the tab was opened. `100` and above display as `99+` on the favicon and as the real total in the title and on the header control.

_Considered and rejected: Option B (clear the number on focus — hides a queue that is still waiting), Option C (a dot with no number)._

---

## Q6: Is the tab counter tied to desktop notifications?

The counter needs no browser permission. The OS toast does, and it is the noisier of the two. The label list applies to both either way.

### Option A: Two switches, one label list, plus a header master switch

"Tab counter" and "Desktop notifications" are independent. Either can be on alone. They share the label field from Q4.

Both default to on, and a master switch defaults to on. The master switch is an on/off control in `SharedHeader`, immediately before the light/dark control. It does not edit the label list or the two signal switches. Off suspends both signals and clears the tab counter. On resumes whatever the settings currently say, including a fine-tune that left one signal off.

Fine-tuning (the two switches, the label field, permission state) lives on the Settings page from Q9, not on this header control.

The page load does not call `Notification.requestPermission()`. The counter runs as soon as the master switch is on. Desktop toasts start once the browser permission is `granted`. Turning the master switch from off to on is a user gesture and requests permission when desktop notifications are configured on and the browser has not been asked yet. The settings surface can request it too.

- **Pro:** Day to day is one header control. Settings can still mute toasts and keep the counter, or the reverse.
- **Pro:** Both signals are on for a fresh browser, which matches "enable both by default."
- **Con:** Two switches in settings, plus the header control. The first visit cannot pop a permission prompt by itself, so desktop toasts wait for a click.

**Decision:** Option A — both signals default to on. The header control just before the theme switch suspends or resumes both of them without changing the saved configuration. Fine-tuning stays in settings.

_Considered and rejected: Option B (one switch only — no way to keep the counter while muting desktop toasts)._

---

## Q7: When does the desktop notification fire?

Subscribing to `sessions` replays up to 200 historical events that are indistinguishable from live ones. The watcher avoids that by toasting only ids that show up on a later list fetch and were not in the remembered set. This question is about the remaining product rules: the existing backlog, a focused tab, and a session that is reopened.

### Option A: New matches, and only while the tab is in the background

Toast when a session newly appears in the qualifier after the watcher is armed. Do not toast the backlog that existed at enable time. Do not toast when `document.hidden` is false. Do not toast a reopen of an id already remembered. The counter still updates in all of those cases.

- **Pro:** A focused dashboard is already showing the new row. A reload does not re-toast the queue.
- **Pro:** Browsers often suppress a toast for the focused tab anyway.
- **Con:** A session that completes while the user is looking at another TARSy route in the same tab does not toast. The counter and the list still update.

**Decision:** Option A — toasts are for new matches while the tab is in the background. The counter still tracks the full waiting queue.

_Considered and rejected: Option B (also toast while the tab is focused — stacks a toast on top of a row the user can already see), Option C (one summary toast for the existing backlog when the watcher arms — the counter already shows that number)._

---

## Q8: What does the desktop notification say?

`GET /api/v1/sessions` returns `alert_type`, `labels`, and `executive_summary` on each row. The summary is one to four lines and is already what Slack posts. An OS toast can also appear on a lock screen.

### Option A: One toast per session, with the summary

Title from the alert type and labels. Body is the executive summary, truncated. Click focuses the tab and opens `/sessions/{id}`.

- **Pro:** The toast is enough to decide whether to walk over. Click lands on the session that matched.
- **Con:** The summary is incident text in the OS notification list, visible to anyone who can see the screen.

**Decision:** Option A — one toast per session, with the alert type, labels, and the executive summary truncated to about 180 characters. Click opens that session. The notification tag is the session id, so a duplicate replaces itself.

_Considered and rejected: Option B (title and labels only — the summary is the reason to look), Option C (one coalesced toast for a burst — loses the per-session summary and the direct link)._

---

## Q9: Where does the fine-tuning live?

Q6 puts an on/off control in the header, immediately before the theme switch. That control only suspends or resumes the saved configuration. The two signal switches, the label field, and the permission state need a separate place. It has to work without oauth2-proxy, because the user menu is hidden when login is not configured.

### Option A: A Settings page in the left nav

A nav item, same list as Triage and Usage, opening a page whose content is this form.

- **Pro:** A real settings tab, separate from the header switch. Enough room for the label field. Reachable on every route, with or without login.
- **Con:** A new page and nav item for one form.

**Decision:** Option A — a Settings page in the left nav holds the two signal switches, the label field, and the permission state. The header control stays the master on/off switch.

_Considered and rejected: Option B (a menu on the header control — crowds the switch and cramps the label field), Option C (the user menu — hidden when oauth2-proxy is off)._

---

## Q10: What happens when several dashboard tabs are open?

Each tab has its own favicon and title. OS toasts are per browser, and two open tabs both receive the same websocket wake-up.

### Option A: Each tab draws the counter; one toast per session

Every tab sets its own favicon and title from `total_items`. The toast is claimed once in `localStorage` (the seen-id write) so only one tab constructs `Notification`.

- **Pro:** Every tab shows the queue. The user is not toasted twice for one session.
- **Con:** Two tabs can theoretically both pass the claim check before either writes. A rare double toast is acceptable; a lock around the claim is enough if it shows up in practice.

**Decision:** Option A — every tab draws the counter. One tab claims the session id in `localStorage` before constructing the toast.

_Considered and rejected: Option B (every tab toasts — duplicate OS toasts), Option C (watcher only on the session list and Triage — a session-detail tab goes silent)._
