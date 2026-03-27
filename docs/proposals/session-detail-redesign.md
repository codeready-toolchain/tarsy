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

### A. Compress SessionHeader into a lean banner ✅ DONE

**Final layout:**

```
┌──────────────────────────────────────────────────────────────┐
│ SecurityInvestigation  ● Completed · 4m 40s        ↻  📋  ★ │
│ Mar 26, 7:09 AM · by guardian-cockpit-sa                     │
│──────────────────────────────────────────────────────────────│
│ USED TOKENS  785,935 total  759,945 in  19,877 out | 📄 ALERT DATA ▾ 📋│
│ (expanded: rich field rendering with 2-col grid, chips)      │
└──────────────────────────────────────────────────────────────┘
```

Changes made:

1. **Removed stats pills row entirely** — all colored badges and "Session Summary" header deleted.
2. **Removed Parallel Agents badge** — implementation detail, visible from the timeline.
3. **Single-flow layout** — eliminated the two-column layout (left metadata / right actions) that created vertical gaps. Title, status, duration, and actions now share one row.
4. **Duration moved inline** — added `variant="inline"` to `ProgressIndicator`; duration renders as compact text next to the status badge instead of a separate "DURATION" header block.
5. **Action buttons replaced with icon buttons** — Re-submit (↻), Cancel (✕), Score (★) are now small icon buttons with tooltips, eliminating large outlined buttons.
6. **Token usage + alert data in a unified footer bar** — tokens on the left, alert data toggle on the right, separated by a subtle vertical divider.
7. **Alert data merged into header card** — no longer a separate `Paper` card. Collapsed by default; expands inline with rich field rendering (`AlertDataContent` extracted as reusable component).
8. **Removed duplicate stage/interaction counts from AppBar**.
9. **Truncated author** — K8s service account paths show last segment; full path in tooltip.
10. **Removed session UUID** — available from URL.
11. **Removed chain ID** — alert type in title is sufficient.
12. **Interactive JSON rendering** — nested JSON fields now use `JsonDisplay` (react-json-view-lite) instead of plain `<pre>` dumps.

### B. Alert Data merged into header ✅ DONE

Alert data is no longer a separate card. It lives inside the `SessionHeader` footer bar:

1. **Always collapsed by default** — users click "ALERT DATA" in the footer to expand.
2. **Rich field rendering on expand** — `AlertDataContent` extracted as a reusable component from `OriginalAlertCard`. Renders severity/environment chips, 2-column grid for simple fields, full-width for complex fields, importance-based ordering.
3. **Copy button** inline in the footer bar for quick raw data copy.
4. **`OriginalAlertCard` preserved** as a standalone component (still available for other uses) but no longer rendered on the session detail page.

### C. Prioritize field ordering ✅ DONE

Implemented importance-based field ordering (was alphabetical):

1. Cluster, environment, severity (tier 1)
2. Alert type, namespace (tier 2)
3. Workload name, workload CID, node (tier 3)
4. Timestamps (tier 4)
5. User info (tier 5)
6. Everything else alphabetically (tier 99)

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

## E. Timeline & Below-the-Header Improvements

### Problems

Between the header and the first tool call, there are **4 layers of navigation/meta** (~150px of chrome):

1. "Jump to Summary" button
2. Search bar
3. Chain header with 3 action buttons + 5 stat chips
4. "AI Reasoning Flow" tab

This undoes the header compression — the user still can't see content without scrolling.

### E1. Compress pre-timeline chrome ✅ (partial)

- ✅ **Move search into the chain header bar** — combined into one row with expand/collapse + copy buttons.
- **Remove "Jump to Summary" button** — replace with a keyboard shortcut, a sticky FAB, or integrate into a mini-nav.
- ✅ **Remove stat chips entirely** — removed all stat chips (stages, completed, failed, tool calls, thoughts, analyses).
- ✅ **Remove chain name** — alert type in the session header is sufficient; no need for "Chain: xxx" duplication.

### E2. Compact parallel execution agent cards ✅

- ✅ **Removed "PARALLEL EXECUTION" header** — the agent cards themselves make the multi-agent layout obvious.
- ✅ **Compacted cards to 2 rows** — Row 1: name + status chip; Row 2: model info + tokens inline. Reduced vertical padding.

### E3. Tighter tool call rendering (partial ✅)

- ✅ **Tighter vertical gaps** — reduced `my`/`mb` on tool calls (`1` → `0.5`), thoughts/summaries (`1.5` → `1`), fallback items (`2` → `1`).
- _Skipped_: Collapse thought indicators (conflicts with streaming UX).
- _Deferred_: Failed tool call prominence, compact mode (needs more design work).

### E4. Stronger stage dividers

"INVESTIGATION", "SYNTHESIS", "VM-REMEDIATION" etc. appear as centered faded text — the primary navigation landmarks in the timeline, yet they barely stand out from tool call bars.

**Suggestion:** Make stages proper section headers — left-aligned, bolder, with a subtle background strip or left border accent. Add expand/collapse per stage to skip entire sections.

### E5. Surface executive summary near the top ✅

- ✅ **Summary-first layout for completed sessions** — when navigating to an already-terminal session, `FinalAnalysisCard` + `ExtractedLearningsCard` render _above_ the timeline. The user sees the conclusion immediately without scrolling.
- ✅ **No layout shift for live sessions** — tracked via `wasTerminalOnMount` ref. If you open a live session that then completes, the order stays timeline-first (no jarring restructure). Refresh after completion gives the summary-first view.
- ✅ **"Jump to Summary" button in timeline header** — present only in the normal (timeline-first) layout; redundant in summary-first layout so omitted there.

### E6. Compact "Have follow-up questions?"

A full-width card with subtitle for an occasionally-used feature. Could be a compact inline element or a floating action button.

### Priority

| # | Change | Impact |
|---|--------|--------|
| 1 | Surface executive summary near top | Most users want the conclusion, not the raw timeline |
| 2 | Compress pre-timeline chrome | Reduces scroll-to-content distance |
| 3 | Compact parallel execution cards | Saves vertical space |
| 4 | Stronger stage dividers | Better timeline navigation |
| 5 | Tighter tool call rendering | Less visual monotony |
| 6 | Compact "follow-up questions" | Minor space savings |

---

## Guiding Principle

> For completed sessions, the user opened this page to see **what the AI did**, not to re-read the raw alert input. The current layout gives equal or greater weight to metadata and input data over the actual AI reasoning — which should be the star of the show.
