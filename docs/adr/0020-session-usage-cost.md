# ADR-0020: Session Usage Cost

**Status:** Implemented
**Date:** 2026-07-23

## Overview

TARSy already surfaces **token usage** on sessions (Alert History, session header, parallel/sub-agent execution surfaces). Operators can see volume but not approximate spend, and there was no place to explore **fleet usage over a date window**.

This decision adds:

1. **Session estimated cost** — soft “Est. $X” next to tokens on Alert History, session detail, and parallel / sub-agent execution surfaces (when cost estimation is enabled).
2. **Usage page** (`/usage`) — date-window dig-in for token totals and estimated cost (when enabled), with breakdowns by model / alert type / chain and a capped top-sessions list.
3. **Price book** — cached LiteLLM public catalog + YAML overrides + bundled snapshot fallback.
4. **Cluster toggle** — cost estimation **on by default**; when off, tokens-only everywhere (Usage page remains for tokens).

Cost is always **estimated** list-price math — not invoice truth. UX copy stays ready for a future provider-reported source. Invoice / CUR reconciliation stays out of scope.

**Operator guide:** [Session Usage Cost Estimation](../session-usage-cost.md)

## Design Principles

1. **Honest estimates.** Soft “Est. $X” + warnings when incomplete; never silent undercount.
2. **Tokens remain primary.** Cost is an overlay; disabling estimation removes $ only.
3. **Point-in-time estimates.** Persist nullable `estimated_cost_usd` at write so history does not drift when the price book changes; pre-feature rows stay null (no backfill in v1).
4. **GitOps config.** YAML toggle + rate overrides; Config Viewer shows effective rates (read-only). Restart-to-apply for YAML (catalog can still refresh on TTL).
5. **Server owns pricing.** Catalog fetch, matching, and USD math live in the Go cost package; the dashboard consumes cost fields.
6. **Same live cadence as tokens.** Cost follows existing REST refresh after WebSocket notifications — no new token/cost WebSocket payloads in v1.
7. **Server-side Usage aggregates.** One summary call for the selected window; UI does not load-all-then-filter.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Persist vs compute on read | Persist nullable `estimated_cost_usd` at write; `SUM` on read | Point-in-time cost; price-book changes do not rewrite history; list stays cheap. Rejected: on-read (history drifts / deprecated models lose $), hybrid (extra mental model). |
| Q2 | Session API shape | Embed cost fields on existing list / detail / summary / `ExecutionOverview` DTOs + `cost_estimation_enabled` | Zero extra round-trips; live refresh picks up cost automatically. Includes `SessionSummaryResponse` for external consumers. Rejected: separate usage endpoint per session, nested envelope. |
| Q3 | Usage API shape | Single `GET /api/v1/usage/summary` | One round-trip; matches “snapshot for this window.” Rejected: split endpoints / overload `GET /sessions`. |
| Q4 | Usage date window | Filter on session `created_at` (same as Alert History) | Consistent mental model; simpler joins. Document edge case (long-running sessions spanning windows). |
| Q5 | Model ↔ catalog matching | Exact `model_name`, then conservative heuristics; conflict → unpriced | Good OOTB hit rate; YAML overrides always win on exact name; never guess when heuristics disagree. |
| Q6 | Catalog cache | In-memory + 24h TTL; bundled snapshot fallback | Simple per-process; covers airgap / fetch failure. Rejected: on-disk cache and startup-only fetch for v1. |
| Q7 | Config home | `system.cost_estimation` with flat per-million USD overrides; default enabled | Matches Slack-style system toggles; Config Viewer System section is a natural home. |
| Q8 | Completeness | `cost_completeness`: `complete` \| `partial` \| `none` (+ counts) | Distinguishes true `$0` from unpriced / disabled; actionable for UI. Do **not** infer from `COALESCE(SUM, 0)`. |
| Q9 | Thinking / cache tokens | Persist `thinking_tokens`; price when > 0; no LangChain extraction; no cache tokens | Stops dropping Google-native thinking already in the pipeline; avoids risky LangChain mapping in v1. Cache gaps documented only. |
| Q10 | Which interactions count | All `interaction_type` values | Matches existing session token SUMs (chat/scoring/summarization included). |
| Q11 | Top sessions on Usage | Hardcoded top-20 + server `rank_by=cost\|tokens` | Answers “expensive / heavy lately?” without a full session browser. Column sort re-fetches. |
| Q12 | Dashboard cost UI | Sibling `EstimatedCostDisplay` next to token components | Clean separation; easy to omit when estimation disabled. |
| Q13 | Tiered pricing | B-scoped: `above_Nk` thresholds + `tiered_pricing` arrays at write time | Correct list price for Gemini 3.1 Pro (≥200k). Skip priority/flex/batches in v1. |

### Product decisions (from sketch)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| S1 | Scope | Session Est. $ **and** Usage dashboard in the same feature | Operators need glanceable session spend *and* date-window fleet dig-in. |
| S2 | Fidelity story | Estimate now; UX ready for future provider-reported cost | Achievable with tokens + price book; avoids a later UX rewrite. |
| S3 | Usage discovery | Hamburger **Usage** → `/usage` | Same path as Manual Alert Submission / System Status. |
| S4 | Session surfaces | List + detail header + parallel/sub-agent execution surfaces | Multi-model branches need per-execution comparison. |
| S5 | Estimate labeling | Soft “Est. $X” + tooltip; escalate when incomplete | Compact; does not overclaim invoice accuracy. |
| S6 | Live updates | Same cadence as token aggregates while session runs | Consistent with existing live token growth. |
| S7 | Toggle placement | YAML flag (default on) + read-only Config Viewer | GitOps-friendly; Usage page stays for tokens when $ is off. |
| S8 | Usage date controls | Presets (7d / 30d / MTD / last calendar month) + custom; default 30d | Aggregate-oriented windows; short session-list presets (10m/1h) not required. |

## Architecture

```
Price book (process memory)
  YAML overrides > cached LiteLLM catalog > bundled snapshot
           |
           v  resolve rates(model_name, input_tokens) at write time
LLM Usage (incl. thinking_tokens when Google native)
  --> persist thinking_tokens
  --> CostEstimator --> llm_interactions.estimated_cost_usd (nullable)
           |
           +--> Session list/detail/summary + ExecutionOverview (SUM cost + tokens)
           +--> GET /api/v1/usage/summary (fleet aggregates for window)
           +--> Config Viewer (enabled flag, overrides, catalog status)
           |
           v
Dashboard: EstimatedCostDisplay · Usage page · hamburger → Usage
```

### Write path

On each LLM interaction create (investigation, chat, scoring, memory, summarization, etc.):

1. Persist nullable `thinking_tokens` from provider usage (Google native populates; LangChain typically 0).
2. If cost estimation **enabled**: resolve rates → compute → persist nullable `estimated_cost_usd` (null if unpriced).
3. If cost estimation **disabled**: leave `estimated_cost_usd` null. Thinking tokens still persist.

Session / execution aggregates use `SUM(estimated_cost_usd)` alongside existing token SUMs. Parent `ExecutionOverview` rolls up nested sub-agent cost the same way as tokens. Completeness uses **counts** of priced vs token-bearing rows.

**Existing rows (v1):** schema-only migration — nullable columns; pre-feature rows stay `NULL` (unpriced). No SQL backfill.

### Usage dashboard data flow

1. Client calls `GET /api/v1/usage/summary` with `start_date` / `end_date` (RFC3339), optional `alert_type` / `chain_id`, and `rank_by=cost|tokens`.
2. Window is keyed on session `created_at`; soft-deleted sessions excluded (same as Alert History).
3. Server returns rollups for that window only: totals, by-model, by alert type, by chain, and capped top-20 sessions.

### Price book lifecycle

```
Startup
  ├─ Load YAML system.cost_estimation (enabled + overrides)
  ├─ Load bundled snapshot into memory
  └─ Kick async fetch of LiteLLM JSON (timeout + max body size)
Refresh (periodic TTL, default 24h)
  └─ Replace in-memory catalog; overrides always win on resolve
Resolve(model_name, input_tokens)
  └─ override (flat) → catalog match (tiered if applicable) → snapshot → unpriced
```

Catalog URL: `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`

Resolve provenance (`override` | `catalog:<key>` | `snapshot:<key>` | `unpriced`) is recorded for Config Viewer / debugging.

## Core Concepts

### Schema additions (`llm_interactions`)

| Column | Type | Notes |
|--------|------|--------|
| `thinking_tokens` | nullable int | Same pattern as other token columns |
| `estimated_cost_usd` | nullable float | Point-in-time estimate; null = unpriced or estimation disabled at write |

### Estimated cost (write-time)

When estimation is enabled and rates resolve:

```
rates = resolveRates(model, input_tokens)   # may apply above_Nk / tiered_pricing
cost_usd =
    input_tokens    * rates.input
  + output_tokens   * rates.output
  + thinking_tokens * rates.reasoning     # if thinking > 0; else 0
                  # reasoning rate = output_cost_per_reasoning_token if present, else output rate
```

**Tier selection** using that interaction’s `input_tokens`:

1. If catalog has `*_above_{N}k_tokens` and input ≥ threshold → use above rates.
2. Else if catalog has `tiered_pricing` ranges → pick one matching tier (no blending).
3. Else → flat base rates.

YAML overrides are **flat** per-million input/output only in v1. Out of scope: priority / flex / batches, cache token rates, override tiers / reasoning rate fields.

### Completeness

**Token-bearing row:** any of `input_tokens`, `output_tokens`, or `thinking_tokens` > 0.

| `cost_completeness` | Meaning | UX |
|---------------------|---------|-----|
| `complete` | All token-bearing interactions have non-null `estimated_cost_usd` | Soft “Est. $X” |
| `partial` | Some token-bearing rows have null cost | Show `SUM` of priced rows + warning |
| `none` | No priced token-bearing rows (or no token-bearing rows) | Hide $ or warning only |

### Cost estimation toggle

`system.cost_estimation.enabled` (default **true** if omitted), read-only in Config Viewer. When off: leave cost null at write; hide `$` in APIs/UI; Usage page remains for tokens.

### Price book sources

| Source | Role |
|--------|------|
| YAML overrides | Highest — flat enterprise/private rates by exact `model_name` |
| Remote LiteLLM catalog | Primary OOTB defaults (in-memory, 24h TTL refresh) |
| Bundled snapshot | Fallback when remote fetch fails / airgap |

## Configuration

```yaml
system:
  cost_estimation:
    enabled: true   # default true if block omitted
    model_rates:    # overrides win over catalog; per-million USD (flat)
      gemini-3.1-pro-preview:
        input_per_million: 2.0
        output_per_million: 12.0
```

Exposed via `GET /api/v1/system/config` (System section) with catalog metadata: last fetch, source, entry count — not the full remote dump. See [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md).

## APIs

### Session / execution enrichment

When cost estimation is enabled:

| Response | Fields |
|----------|--------|
| Dashboard session list | `cost_estimation_enabled` at root; each item: `estimated_cost_usd`, `cost_completeness` |
| Session detail | `cost_estimation_enabled` + session-level cost fields |
| `ExecutionOverview` | per execution / sub-agent cost fields (rolled up like tokens) |
| Session summary (`GET /sessions/:id/summary`) | same session-level cost treatment as detail (external consumers) |

When disabled: omit cost fields; `cost_estimation_enabled: false`.

### Usage aggregation

```
GET /api/v1/usage/summary?start_date=&end_date=&alert_type=&chain_id=&rank_by=cost|tokens
```

- Date window: session `created_at`; exclude soft-deleted sessions.
- All sections are server aggregates for that window only.
- All `interaction_type` values count.
- `top_sessions`: hardcoded cap of **20**; `rank_by` defaults to `cost` when estimation enabled else `tokens`.
- Unpriced top sessions: include with `$0` + completeness warning.

Illustrative response shape:

```json
{
  "cost_estimation_enabled": true,
  "window": { "start": "...", "end": "..." },
  "rank_by": "cost",
  "totals": {
    "input_tokens": 0,
    "output_tokens": 0,
    "total_tokens": 0,
    "estimated_cost_usd": 0.0,
    "cost_completeness": "partial",
    "unpriced_interaction_count": 12
  },
  "by_model": [],
  "by_alert_type": [],
  "by_chain": [],
  "top_sessions": []
}
```

## Frontend

| Area | Behavior |
|------|----------|
| Routes | `/usage` |
| Hamburger | **Usage** next to Manual Alert Submission / System Status |
| Usage page | Date presets (7d / 30d / MTD / last calendar month; default 30d) + custom range; optional `alert_type` / `chain_id` filters; totals / breakdowns / top-20 with server-side `rank_by` |
| Session / execution surfaces | `EstimatedCostDisplay` beside existing token UI |
| Config Viewer | System / cost-estimation section |

Live updates: existing WS → REST refresh; cost fields arrive with session payloads.

## Out of Scope

- Invoice / Admin Cost API / CUR reconciliation
- Budget alerts or spend caps
- Cache token persistence / pricing
- LangChain thinking-token extraction
- Priority / flex / batches LiteLLM rates
- YAML override tiers / reasoning rates (flat input/output only)
- Per-LLM-interaction Trace cost rows
- Full paginated “all sessions in range” table on Usage
- Client-side price book / Python pricing service
- Hot-reload of YAML rates without process restart
- Backfill of pre-feature `estimated_cost_usd`

## Future Considerations

- One-shot Go backfill for pre-feature rows (reuse write-path estimator; document that backfilled `$` uses rates at backfill time)
- LangChain thinking usage extraction after careful double-count validation
- Optional `sort_by=total_tokens|estimated_cost_usd` on session list, or a paginated Usage session table
- Provider-reported cost source when gateways expose it

## References

- [Session Usage Cost Estimation](../session-usage-cost.md) — operator-facing guide
- [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md) — Config Viewer surface for cost settings
