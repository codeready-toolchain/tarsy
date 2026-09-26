# Usage Charts — Design Questions

**Status:** Decided
**Related:** [Design document](usage-charts-design.md)

Decisions are recorded below. The Material UI v9 migration has landed (`@mui/material` 9.4.0, `@mui/x-date-pickers` 9.14.0), so Q8 is `@mui/x-charts` v9.

---

## Q1: Which timestamp fills the buckets?

The Usage summary assigns a session's whole cost to the window that contains `alert_sessions.created_at`. A chart has to pick the same rule or a different one. Long investigations and later chat on an old session land on different days depending on this choice. The stat cards will match the charts only when the chart uses the same timestamp as the cards.

### Option A: Session `created_at`

Every interaction on a session is summed into the bucket of the session's start. The same session filter as `GET /api/v1/usage/summary`.

- **Pro:** Sum of the buckets equals **Est. cost**. Sum of session counts equals the Sessions card. The documented window edge case (a session started before the range is absent; later chat on an in-range session is included) stays one rule.
- **Pro:** The query is the summary population grouped by time. No new notion of "when spend happened."
- **Con:** A session that starts Monday and keeps chatting on Wednesday shows all of its cost on Monday. The Wednesday bucket does not move.

**Decision:** Option A — bucket by session `created_at`, so chart totals match the existing Usage cards.

_Considered and rejected: Option B (interaction `created_at` — spend follows each LLM call, but buckets stop matching the cards and the average line needs a different session denominator)._

---

## Q2: How should estimated total cost be drawn?

"Total cost, with a model breakdown" can be a running total (the Cursor chart) or the spend inside each bucket. The API returns per-bucket deltas either way; this question is only the drawing.

### Option A: Cumulative stacked area

Each model's band is the running sum of its bucket costs. The top of the stack at the right edge equals **Est. cost** for the window (given Q1 Option A). Hovering a point shows that bucket's model split, the bucket total, and the cumulative total.

- **Pro:** Answers "where did this window's spend come from, and how did it add up?" The end of the chart is the number on the card.
- **Pro:** Matches the inspiration screenshot without copying its Group-by / Metric controls.
- **Con:** A single expensive day lifts every later point. Spikes are easier to see in the tooltip than in the slope.

**Decision:** Option A — cumulative stacked area by model, with the bucket split, bucket total, and cumulative total in the tooltip.

_Considered and rejected: Option B (per-bucket stacked bars — spikes are obvious, but no point equals the card total), Option C (one cumulative line with models only in the tooltip — the breakdown is hidden until hover)._

---

## Q3: What should the average chart plot?

**Avg. cost / session** on the Totals card is `estimated_cost_usd / session_count` for the whole window. `session_count` includes sessions with no LLM rows. The chart can repeat that formula per bucket, or draw a running average that finishes on the card.

Per-model average lines are not an option here. A session that calls two models counts in both, so those lines do not add up. The by-model table already has **Avg. / session**.

### Option A: Per-bucket average

One line. Point = bucket cost / bucket session count, using every non-deleted session in the bucket. Buckets with no sessions are gaps (null), not $0.

A bucket with exactly one session is one observation, so the point is drawn as a hollow mark. Every tooltip includes the session count. The single-session tooltip states that the figure is that session's cost. Buckets with two or more sessions keep the normal filled mark. No confidence band and no fading scale for "small" counts.

- **Pro:** Shows whether new sessions got more expensive. A quiet day does not look like a free session.
- **Pro:** With Q1 Option A, the formula is the card's formula, applied inside each bucket. A session-weighted combination of the points equals the card.
- **Con:** A day with one expensive session still swings the line. The hollow mark and tooltip flag that point; they do not remove it. The window average stays on the card, not on the chart.

**Decision:** Option A — per-bucket average, gaps when a bucket has no sessions. Tooltip always shows the session count. A bucket with exactly one session uses a hollow mark and tooltip copy that the value is that session's cost.

_Considered and rejected: Option B (running average — lands on the card, and dilutes spikes), Option C (both lines — two ratios on one axis). Also skipped: confidence intervals, opacity by sample size, and a "fewer than N" threshold beyond a single session. Those need a chosen cutoff and more chart machinery. One session is unambiguous, and a hollow mark is a stock "this point is one observation" treatment._

---

## Q4: How long is a bucket?

Usage presets include Last day (a rolling 24 hours) and windows up to 365 days. One fixed size is a poor fit for both.

### Option B: Always calendar days

- **Pro:** One rule. Tick labels are always dates.
- **Con:** Last day is one partial point, sometimes two. The chart does not show shape inside a day.

**Decision:** Option B — every bucket is a calendar day, including short windows. Hourly resolution can wait.

_Considered and rejected: Option A (adaptive hours below 48h — more resolution, and a second bucket size to test), Option C (operator picks hour or day — another control)._

---

## Q5: Whose calendar is a "day"?

Month-to-date and Last calendar month on the Usage page are computed in the browser's local timezone, then sent as RFC3339. Truncating in UTC would put an evening session on the next calendar date for anyone not on UTC.

### Option A: Browser IANA timezone, UTC if it cannot be used

The dashboard sends `timezone` (for example `America/Los_Angeles`). A known IANA name is used for `AT TIME ZONE`. If the parameter is missing or not a known zone, the server buckets in UTC and does not return an error. The response `timezone` field is the zone actually used, so a fallback is visible.

- **Pro:** Tick labels match the operator's calendar and the MTD preset.
- **Pro:** Two operators in different zones can still share an API call explicitly; the zone is part of the query, not an implicit server default.
- **Pro:** A bad or absent zone still returns a chart.
- **Con:** Tests have to cover a non-UTC boundary and the UTC fallback. A caller who ignores the echoed `timezone` can misread which calendar the buckets used.

**Decision:** Option A — bucket in the requested IANA timezone. Missing or unknown names fall back to UTC with no error. The response echoes the timezone that was applied.

_Considered and rejected: Option B (UTC always — no parameter, and a late local evening lands on the next day's tick)._

---

## Q6: How many models stay visible?

A stacked area gets hard to read, and hard to color, once there are many models. The by-model table still lists every model with full precision.

### Option A: Top 6 by window cost, rest as Other

The six models with the largest `SUM(estimated_cost_usd)` in the window each get a band. Everything else is one **Other** band. The API does not use the model name `"other"` for that band. Each point carries the collapsed models and each one's cost for that bucket; the band is their sum.

The tooltip's **Other** row shows that total, then every collapsed model that has a non-zero cost in the bucket, with that model's Est. cost. Models at $0 for the bucket are omitted, so the list is exactly what makes up that bucket's Other total. The stack itself stays one band.

- **Pro:** The bands that matter stay visible. Other keeps the stack equal to the total.
- **Pro:** Stable for a deployment that experiments with many model names.
- **Pro:** Hovering Other still names the models inside it, with their bucket cost.
- **Con:** A model just outside the top 6 has no band of its own. The table and the Other tooltip still name it.

**Decision:** Option A — top 6 models by window cost as bands, the rest as one Other band. The Other tooltip lists each collapsed model with a non-zero cost in that bucket and that model's Est. cost.

_Considered and rejected: Option B (every model as its own band — the chart and the table match, and the stack gets crowded), Option C (top 6 plus a control to expand all — the table already lists every model)._

---

## Q7: Where does the time series live?

ADR-0020 kept fleet aggregates on one summary call. A time series is a different shape (buckets, timezone) and a different query.

### Option A: `GET /api/v1/usage/series`

Same window and `alert_type` / `chain_id` as the summary, plus `timezone`. The Usage page requests it in parallel. `rank_by` stays on the summary only.

- **Pro:** The summary JSON and its tests stay put. Re-ranking top sessions does not recompute buckets.
- **Pro:** A series failure leaves the tables up.
- **Con:** Two queries for one page. A session that finishes between the two calls can make the chart and the card disagree by one session until the next refresh. The existing live refresh pulls both.

**Decision:** Option A — a sibling `GET /api/v1/usage/series`. The summary response stays unchanged.

_Considered and rejected: Option B (embed the series in the summary — one snapshot, and every summary caller pays for the bucket query, including a `rank_by` change)._

---

## Q8: Which chart library?

The dashboard has no chart dependency today. Material UI and the date pickers are both on v9 (`@mui/material` 9.4.0, `@mui/x-date-pickers` 9.14.0), which uses `@mui/utils` v9.

### Option A: `@mui/x-charts` v9 (community)

Stacked area is `LineChart` with `area` and a shared `stack`. Custom tooltips are a slot. MIT community package. The same 9.x line as the date pickers, so `@mui/utils` and `@mui/x-internals` stay aligned.

v9 turns marks off unless `showMark` is set, so the average line sets it and the cost area leaves it off. The tooltip hook is `useAxesTooltip`, and the tooltip renders inside the chart container. The line axis domain is strict by default.

- **Pro:** Theme, CSS variables, and dark mode come from the existing MUI setup. No second chart vocabulary.
- **Pro:** Stacked area and a null-gapped line are both in the community package. Pro is not required.
- **Con:** A new dependency and a custom tooltip component.

**Decision:** Option A — `@mui/x-charts` v9. The v7 → v9 migration landed, so the charts package matches Material UI 9 and `@mui/x-date-pickers` 9.14.0.

_Considered and rejected: Option B (Recharts — a second styling system next to MUI), Option C (hand-rolled SVG — axes, hover, and focus become the project). `@mui/x-charts` v8 was the package only while the dashboard stayed on Material UI 7; it depends on `@mui/utils` v7 and does not belong next to the v9 date pickers._
