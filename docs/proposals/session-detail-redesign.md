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

**Implemented layout:**

```
┌─────────────────────────────────────────────────────────────┐
│ SecurityInvestigation                [Completed]   DURATION  │
│ Started at Mar 26, 7:09 AM · by guardian-cockpit-sa    4m 40s│
│                                          [RE-SUBMIT ALERT]   │
│─────────────────────────────────────────────────────────────│
│ USED TOKENS    785,935 total    759,945 in    19,877 out     │
└─────────────────────────────────────────────────────────────┘
```

Changes made:

1. **Removed the stats pills row entirely** — all 6–7 colored badges (total, LLM, MCP, stages, tokens, score, errors) and the "Session Summary" section header deleted.
2. **Removed the Parallel Agents badge** — implementation detail, visible from the timeline itself.
3. **Token usage shown once at card bottom** — separated by a divider, displayed as a compact stat row with color-coded full numbers (`785,935 total  759,945 in  19,877 out`). Both the old pill variant and `variant="detailed"` block removed.
4. **Removed duplicate stage/interaction counts from AppBar** — the header title is sufficient.
5. **Truncated author** — K8s service account paths like `system:serviceaccount:ns:name` display just the last segment; full path in a tooltip.
6. **Removed session UUID entirely** — available from the URL, no need to display it.
7. **Removed chain ID** — alert type in the title is sufficient.
8. **Moved scoring trigger button** to the right-side actions column alongside Cancel/Re-submit.

### B. Collapse Original Alert Data by default (completed sessions) ✅ DONE

Changes made:

1. **Collapsed by default for terminal sessions** — new `sessionStatus` prop; card starts collapsed when status is completed/failed/cancelled/timed_out, expanded for active sessions.
2. **Summary preview when collapsed** — shows top 2–3 fields by priority as a single-line preview (e.g., `Cluster: api1.r83... · Namespace: babyzalokvich-dev · Node: ip-10-8-16-49`), truncated with ellipsis.
3. **2-column grid for simple fields** — short string/number values render in a responsive 2-column CSS grid. Complex fields (URLs, JSON objects, multi-line text, timestamps with icons) still get full width.
4. **Clickable header row** — the entire header is now clickable to toggle expand/collapse, not just the chevron icon.

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

## Implementation Plan

### Phase 1: SessionHeader cleanup ✅ DONE

| Change | Status |
|--------|--------|
| Remove stats pills row + "Session Summary" section entirely | ✅ |
| Remove Parallel Agents badge | ✅ |
| Remove duplicate token displays; add single compact row at card bottom | ✅ |
| Remove stage/interaction counts from SharedHeader children | ✅ |
| Remove chain ID from title | ✅ |
| Remove session UUID (available from URL) | ✅ |
| Truncate K8s service account author string (tooltip for full path) | ✅ |
| Move scoring trigger to actions column | ✅ |

Net result: `SessionHeader.tsx` reduced from 790 → ~498 lines.

### Phase 2: OriginalAlertCard improvements ✅ DONE

| Change | Status |
|--------|--------|
| Default collapsed for terminal sessions | ✅ |
| Two-column grid for simple fields | ✅ |
| Summary preview line when collapsed | ✅ |
| Field importance ordering | ✅ |
| Clickable header row | ✅ |

### Phase 3: Larger redesign

| Change | Impact | Est. LOC |
|--------|--------|----------|
| Sticky session header on scroll | Persistent context while browsing timeline | ~80 |
| Tab or accordion section navigation | Direct access to Timeline/Summary/Alert Data | ~200 |

---

## Guiding Principle

> For completed sessions, the user opened this page to see **what the AI did**, not to re-read the raw alert input. The current layout gives equal or greater weight to metadata and input data over the actual AI reasoning — which should be the star of the show.
