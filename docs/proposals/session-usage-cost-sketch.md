# Session Usage Cost — Estimated Spend & Usage Dashboard

**Status:** Sketch complete — ready for detailed design

**Related:** [Questions document](session-usage-cost-questions.md) (all decisions made)

## Problem

TARSy already shows **token usage** on sessions (list column, session header, timeline, trace). Operators can see *how much* an investigation consumed, but not *what it roughly cost*, and there is no place to explore **fleet usage over a date window** (tokens or dollars).

That gap matters for everyday SRE judgment:

- “About how much did this investigation cost?” (session glance)
- “Was this alert type expensive?” / “Did fallback blow up spend?”
- “What did we use in the last 30 days / MTD?” (fleet dig-in)

Tokens alone answer volume; **dollars (even estimated)** answer trade-offs. Today there is no cost display, no usage dashboard, and no pricing configuration surface.

## Goal

Ship **both** of these in the same feature:

1. **Session estimated cost** — next to existing token usage on the **session detail** view, the **main session list** (Alert History), and **per-execution** surfaces for parallel agents and sub-agents (so multi-model branches can be compared).
2. **Usage dashboard** — a dedicated page to dig into **token counts and estimated cost**, with easy date windows (last 30d, MTD, custom ranges, etc.).

Additional product rules:

- **Cost estimation is configurable and enabled by default.** When disabled, TARSy shows **token usage only** (no $ anywhere). The usage dashboard remains useful for tokens even when $ is off.
- Cost is labeled **“Est. $X”** with tooltip caveats — not invoice truth.
- Invoice-grade FinOps reconciliation stays out of scope.

**Audience (Q1):** Investigating SREs *and* leads who need date-window fleet views — not finance chargeback.

## How It Relates to the Existing System

| Surface | Today | This feature |
|---------|--------|----------------|
| Alert History **Tokens** column | `TokenUsageDisplay` | Est. cost beside/under tokens when enabled (Q4) |
| Session header footer | “Used tokens” | “Est. $X” in the same footer when enabled (Q4) |
| Parallel agent tabs/cards | Per-execution tokens via `ExecutionOverview` | Est. $ next to those tokens when enabled (Q4) |
| Sub-agent cards/tabs | Same token path as parallel executions | Est. $ next to those tokens when enabled (Q4) |
| Dashboard hamburger | Manual Alert Submission, System Status | Add **Usage** → dedicated page (Q3) |
| Session list date filters | `TimeRangeModal` presets + custom | Pattern reused on Usage; Usage-oriented presets (Q11) |
| Config Viewer (`/system`) | Read-only effective config | Show cost-estimation flag + effective rates (Q6, Q10) |
| Prometheus | Fleet tokens by provider/model | Ops-facing; not a substitute for the Usage page |

### Data already available

- Per-interaction: `model_name`, `input_tokens`, `output_tokens`, `total_tokens`, `duration_ms`
- Session aggregates: SUM of those tokens (list, detail, summary APIs)
- Per-execution (`ExecutionOverview`): token aggregates + `llm_provider` / `llm_backend` (already used for parallel/sub-agent token chips)

### Known gaps (estimate quality — design can improve later)

- No cache-read / cache-write token fields in proto or DB
- Thinking tokens exist at runtime / Prometheus but are not persisted on interactions
- No provider on `llm_interactions` (provider lives on executions)
- No pricing catalog / overrides / cost-estimation toggle today

These do not block shipping estimated cost; UX stays honest about limits (Q5, Q9).

## Key Concepts

### Estimated cost (not invoice cost) — Q2

**Estimated cost** = token usage × rates from the price book for the model(s) used.

v1 shows **estimated** cost. UX/copy should allow a future **provider-reported** source without redesign. Not for invoice reconciliation, cloud CUR join, or automatic enterprise-discount detection.

### Cost estimation toggle — Q10

Cluster-wide YAML flag, **default on**, visible read-only in Config Viewer:

| Setting | Session list / detail / parallel & sub-agent executions | Usage dashboard |
|---------|------------------------------------------------------|-----------------|
| **On** (default) | Tokens + estimated cost | Tokens + estimated cost |
| **Off** | Tokens only | Tokens only (dashboard still useful) |

### Price book — Q6

```
YAML overrides          (highest — enterprise / private / corrections)
       ↓
Remote catalog          (LiteLLM public JSON, cached; primary defaults)
       ↓
Bundled snapshot        (fallback only — offline / fetch failure)
```

- TARSy does **not** hand-maintain a full public price table in git as the primary source.
- Config Viewer shows effective rates and source (catalog vs override).
- No Python pricing microservice in v1; Go can fetch/cache (OpenClaw-style).
- List prices ≠ negotiated Vertex/Azure rates — overrides remain essential for enterprise accuracy.

### Two UX products (both in scope) — Q1, Q3, Q4, Q8

```
┌─────────────────────────────────────────────────────────────┐
│ Session UX                                                  │
│  List + Detail header + parallel / sub-agent executions     │
│  Tokens always · Est. $ when cost estimation enabled        │
│  Live updates while session runs (same cadence as tokens)   │
│  Compare Est. $ across parallel agents (often different     │
│  models) and sub-agents within one investigation            │
└─────────────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────────────┐
│ Usage dashboard  (hamburger → Usage → e.g. /usage)          │
│  Date windows · dig into tokens and/or $                    │
│  Totals · by model · by alert type/chain · top sessions     │
└─────────────────────────────────────────────────────────────┘
```

## Behavior (feature / UX)

### Sessions (list, detail, parallel & sub-agents) — Q4, Q5, Q7, Q9

- Show **Est. $X** next to tokens on:
  - Session detail header
  - Alert History Tokens column
  - **Parallel agent** surfaces that already show per-execution tokens
  - **Sub-agent** surfaces that already show per-execution tokens  
  (when cost estimation is enabled)
- Soft **“Est.”** label + tooltip for caveats; escalate warning when estimate is partial/unpriced.
- Updates **live** as token aggregates refresh during a running session.
- Partial estimate + explicit warning when some interactions are unpriced — never silent undercount, don’t hide $ entirely just because pricing is incomplete.
- Session total should read as the rollup of underlying executions (same honesty rules).
- Per-LLM-interaction cost rows in Trace detail are **follow-up**, not required for v1 once execution-level cost exists.

### Usage dashboard — Q3, Q8, Q11

- Entry: hamburger menu **Usage** (next to Manual Alert Submission and System Status) → dedicated page (e.g. `/usage`).
- **Date windows:** presets + custom range. Presets include at least Last 7 days, Last 30 days, MTD, Last calendar month. **Default: Last 30 days.**
- **Content:** totals (tokens + est. cost when enabled); breakdown by model; breakdown by alert type and/or chain; top sessions (deep-link to detail); familiar session-style filters.
- When cost estimation is off: same page, tokens only.

## Out of Scope

- Invoice-accurate FinOps reconciliation (provider Admin Cost APIs, CUR, Vertex billing exports)
- Budget alerts, hard spend caps, or auto-stopping sessions on spend
- Charging end-users / multi-tenant showback (may interact with [session authorization](session-authorization-sketch.md) later)
- Non-LLM costs (MCP infra, embeddings for memory)
- Replacing Prometheus / Grafana for ops monitoring
- Per-LLM-interaction Trace detail cost rows in v1 (execution-level parallel/sub-agent cost is in scope)
- Per-user cost-display preference
- Python pricing microservice
- Detailed technical design (pricing algorithm, schema, APIs) — `/design-with-questions`

## Decision summary

| Q | Decision |
|---|----------|
| Q1 | Both session cost + usage dashboard in this sketch |
| Q2 | Estimate now; UX ready for future provider-reported cost |
| Q3 | Hamburger **Usage** → dedicated page |
| Q4 | Session detail + Alert History list + parallel & sub-agent execution surfaces (amended) |
| Q5 | Soft “Est. $X” + tooltip |
| Q6 | LiteLLM catalog (cached) + YAML overrides + Config Viewer; snapshot fallback |
| Q7 | Live cost updates with tokens |
| Q8 | Totals + model + alert type/chain + top sessions + filters |
| Q9 | Partial estimate + explicit warning when incomplete |
| Q10 | YAML toggle (default on) + Config Viewer |
| Q11 | Presets + custom; default Last 30 days |

## Next step

Detailed technical design via `/design-with-questions` — see [session-usage-cost-design.md](session-usage-cost-design.md) and [session-usage-cost-design-questions.md](session-usage-cost-design-questions.md).
)
