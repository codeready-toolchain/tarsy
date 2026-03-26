# Session Detail View — Noise Reduction & Redesign

> Analysis of the current session detail page and actionable recommendations to improve signal-to-noise ratio.

## Current State

The session detail page (`SessionDetailPage.tsx`, ~1800 lines) is the primary view for inspecting a single AI reasoning session. The top portion alone — before any AI reasoning content is visible — consumes the entire viewport with metadata and raw input data.

### Component Inventory (top to bottom)

| Zone | Component | File | Lines |
|------|-----------|------|------:|
| Blue AppBar | Shared header — title, Reasoning/Trace toggle, stage/interaction counts, Live indicator, user avatar | `SharedHeader.tsx` | 127 |
| White card | Session metadata — title, status, chain ID, timestamps, UUID, author, stats pills, token usage (shown twice), actions | `SessionHeader.tsx` | 790 |
| Second card | Original alert data — all fields listed vertically, fully expanded by default | `OriginalAlertCard.tsx` | 430 |
| Divider | "Jump to Summary" button, in-session search bar | `SessionDetailPage.tsx` | — |
| Timeline | Conversation timeline with stages, tool calls, responses | `ConversationTimeline.tsx` | 555 |
| Bottom | Final analysis, extracted learnings, chat panel | Various | — |

---

## Problems Identified

### 1. SessionHeader crams 7 concerns into one card

The header simultaneously displays: identity, status, timing, authorship, stats, token usage, and actions — all at the same visual weight.

**Duplicate token display** — Token usage appears twice:
- As a pill badge in the stats row (inline variant: `768.0k · 19.9k · 786.9k`)
- As a full detailed breakdown below (`Total: 786,835  Input: 759,345  Output: 19,877`)

**Duplicate stage/interaction counts** — The AppBar shows "5 stages · 68 interactions" on the right, then the pills repeat "86 total", "34 LLM", "52 MCP", "5/5 stages".

**Noisy technical metadata displayed prominently:**
- Full session UUID in monospace (`56f48046-036f-4976-baf7-...`)
- Full K8s service account path (`system:serviceaccount:guardian-cockpit:guardian-cockpit-sa`)
- Verbose chain ID appended to title (`security-investigation-orchestrated`)

**Visual clutter** — 6–7 colored pill badges (grey, blue, orange, cyan, green, red) all competing for attention in a single row. The "Parallel Agents" badge adds yet another element that's an implementation detail, not actionable info.

### 2. Original Alert Data dominates the viewport

- **Expanded by default** (`useState(true)`) regardless of session status
- For completed sessions, raw input data is far less important than the AI's output
- Each field gets a full-width row — even single-word values like "babyzalokvich"
- Fields sorted alphabetically, not by importance
- Occupies 50%+ of the visible viewport, pushing the AI reasoning timeline off-screen

### 3. Flat information hierarchy

- Session header card and alert data card have identical visual weight (both `Paper` with `p: 3`)
- No progressive disclosure — everything is shown upfront
- No visual flow guiding the eye from "what is this?" to "what happened?"
- The most important content (AI reasoning, conclusions) requires scrolling past all metadata

---

## Recommendations

### A. Compress SessionHeader into a lean banner

**Target layout:**

```
┌─────────────────────────────────────────────────────────────┐
│ SecurityInvestigation                        [Completed]    │
│ 📅 Mar 26, 7:09 AM · ⏱ 4m 40s · by guardian-cockpit-sa      │
│ 🪙 786k tokens (in: 759k · out: 19k)    [RE-SUBMIT ALERT]   │
└─────────────────────────────────────────────────────────────┘
```

Specific changes:

1. **Remove the stats pills row entirely** — the 6–7 colored badges (total, LLM, MCP, stages, tokens, score) are redundant. Stage/interaction counts already appear in the AppBar. Score has its own section. Remove the entire "Session Summary" section and all pills.
2. **Remove the Parallel Agents badge** — this is an internal implementation detail. Whether stages ran in parallel is visible from the timeline itself. No need for a prominent badge.
3. **Keep token usage, but show it once** — keep a single compact line showing total tokens with input/output breakdown inline. Remove both the pill variant and the `variant="detailed"` block. Replace with one clean line in the metadata row (e.g., `🪙 786k tokens (in: 759k · out: 19k)`).
4. **Remove duplicate counts from AppBar** — the AppBar title "AI Reasoning View - 72424927" is sufficient.
5. **Truncate author** — extract just the service account name ("guardian-cockpit-sa"). Show full path in a tooltip.
6. **Hide UUID by default** — replace with a small copy-to-clipboard icon. Almost nobody reads the full UUID on first glance.
7. **Move chain ID out of the title** — show as a small subtitle, chip, or tooltip. Don't append it to the main heading.

### B. Collapse Original Alert Data by default (completed sessions)

- Change default state: `useState(isTerminal ? false : true)` — pass session status as a prop.
- When collapsed, show a **summary line** with 2–3 key fields extracted from the data (e.g., Cluster, Namespace, WorkloadName).
- When expanded, use a **2-column grid** for short fields instead of stacking everything vertically.

### C. Prioritize field ordering

Replace alphabetical sort with importance-based ordering:

1. Cluster / environment-related fields
2. Severity, alert type
3. Namespace, workload
4. Timestamps
5. IDs and hashes (lowest priority)

Or group into "Key Info" (always visible in collapsed summary) and "Details" (shown on expand).

### D. Bigger idea — Sticky header + section navigation

```
┌────────────────────────────────────────────────────────┐
│ [STICKY] SecurityInvestigation · Completed · 4m 40s    │
├────────────────────────────────────────────────────────┤
│ [Tab: Alert Data] [Tab: Timeline] [Tab: Summary]       │
│                                                        │
│ (selected tab content here)                            │
└────────────────────────────────────────────────────────┘
```

Benefits:
- Session identity stays visible while scrolling
- Users navigate directly to what they care about
- Eliminates scrolling past the alert card to reach the timeline
- Most users want Timeline or Summary, not raw alert data

Even without tabs, a sticky header + collapsed-by-default alert card is a major improvement.

---

## Implementation Plan

### Phase 1: Quick wins (low effort, high impact)

| Change | Impact | Est. LOC |
|--------|--------|----------|
| Remove stats pills row + "Session Summary" section entirely | Eliminates biggest source of visual clutter | ~100 (delete) |
| Remove Parallel Agents badge | One less noisy element in the title row | ~50 (delete) |
| Remove detailed token block (SessionHeader lines 715–730) | Eliminates visual duplication | ~15 (delete) |
| Remove stage/interaction counts from SharedHeader children | Reduces AppBar clutter | ~5 |
| Default OriginalAlertCard to collapsed for terminal sessions | Saves 60%+ viewport on completed sessions | ~5 |
| Truncate K8s service account author string | Cleaner metadata line | ~10 |
| Hide UUID behind a copy-to-clipboard button | Less visual noise | ~15 |

### Phase 2: Moderate effort

| Change | Impact | Est. LOC |
|--------|--------|----------|
| Two-column grid for alert data fields | Better space utilization | ~20 |
| Move chain ID to subtitle/chip instead of title suffix | Shorter, scannable title | ~10 |
| Add collapsed summary line to OriginalAlertCard | Context without expansion | ~30 |
| Field importance ordering in OriginalAlertCard | Key info surfaces first | ~20 |

### Phase 3: Larger redesign

| Change | Impact | Est. LOC |
|--------|--------|----------|
| Sticky session header on scroll | Persistent context while browsing timeline | ~80 |
| Tab or accordion section navigation | Direct access to Timeline/Summary/Alert Data | ~200 |

---

## Guiding Principle

> For completed sessions, the user opened this page to see **what the AI did**, not to re-read the raw alert input. The current layout gives equal or greater weight to metadata and input data over the actual AI reasoning — which should be the star of the show.
