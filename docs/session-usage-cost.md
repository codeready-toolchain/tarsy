# Session Usage Cost Estimation

TARSy can attach an **estimated USD cost** to each LLM interaction at write time, using list prices from a price book. Estimates are for operator judgment — they are **not** invoice truth.

Cost is persisted on each `llm_interaction` at write time. Session list, detail, summary, and `ExecutionOverview` APIs expose estimated cost + completeness when estimation is enabled. The dashboard shows soft **Est. $** next to tokens on Alert History, session detail, and parallel/sub-agent surfaces when estimation is enabled. Fleet dig-in is available on the **Usage** page (`/usage`, hamburger → Usage) via `GET /api/v1/usage/summary`. Config Viewer exposes the effective toggle, overrides, and catalog status under System → Cost estimation (`GET /api/v1/system/config`).

## Table of Contents

- [Overview](#overview)
- [Configuration](#configuration)
- [Price book](#price-book)
- [How estimates are computed](#how-estimates-are-computed)
- [Session APIs](#session-apis)
- [Usage API](#usage-api)
- [Thinking tokens](#thinking-tokens)
- [Known gaps](#known-gaps)
- [Completeness](#completeness)

## Overview

When cost estimation is **enabled** (default):

1. On each LLM interaction write, TARSy resolves rates for `model_name`.
2. It computes `estimated_cost_usd` from input / output / thinking tokens.
3. The value is stored on the row (point-in-time). Later price-book changes do not rewrite history.

When cost estimation is **disabled**:

- Token usage is still persisted (including thinking tokens when reported).
- `estimated_cost_usd` is left null (no pricing math).

## Configuration

```yaml
system:
  cost_estimation:
    enabled: true   # default true if the whole block is omitted
    model_rates:    # optional flat overrides; exact TARSy model_name
      gemini-3.1-pro-preview:
        input_per_million: 2.0
        output_per_million: 12.0
```

- Overrides are **per-million USD** (converted to per-token internally).
- Overrides win over the remote catalog and the bundled snapshot.
- YAML changes require a process restart (catalog TTL refresh does not reload YAML).

See also [`deploy/config/tarsy.yaml.example`](../deploy/config/tarsy.yaml.example).

## Price book

Resolve order:

1. **YAML overrides** — exact `model_name`
2. **Remote LiteLLM catalog** — fetched asynchronously at startup, refreshed every 24h
3. **Bundled snapshot** — curated JSON in `pkg/cost/snapshot.json` for airgap / fetch failure

Catalog URL:

`https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`

Model matching: exact key first, then conservative `*/{model}` / provider-prefix heuristics. If multiple heuristic candidates disagree on rates, the model stays **unpriced** rather than guessing.

Config Viewer (`system.cost_estimation`) shows `enabled`, override rates, and catalog status (`source`, `entry_count`, `last_fetch`, `last_error`).

## How estimates are computed

```
cost_usd =
    input_tokens    * rates.input
  + output_tokens   * rates.output
  + thinking_tokens * rates.reasoning   # only when thinking_tokens > 0
```

Reasoning rate = LiteLLM `output_cost_per_reasoning_token` when present, otherwise the output rate.

**Context tiers (B-scoped):**

1. If the catalog has `*_above_{N}k_tokens` and `input_tokens` ≥ threshold → use those rates (e.g. Gemini 3.1 Pro at 200k).
2. Else if `tiered_pricing` ranges exist → pick the single matching tier (no blending).
3. Else → flat base rates.

Priority / flex / batch rates and YAML override tiers are out of scope for v1.

## Session APIs

When cost estimation is **enabled**, these responses include cost fields next to existing token totals:

| Response | Fields |
|----------|--------|
| `GET /api/v1/sessions` (`DashboardListResponse`) | root `cost_estimation_enabled`; each item: `estimated_cost_usd`, `cost_completeness` |
| `GET /api/v1/sessions/:id` (`SessionDetailResponse`) | root `cost_estimation_enabled` + session-level `estimated_cost_usd`, `cost_completeness`, `unpriced_interaction_count`; same on each `ExecutionOverview` (parent rollup includes nested sub-agents) |
| `GET /api/v1/sessions/:id/summary` (`SessionSummaryResponse`) | same session-level cost fields as detail |

When estimation is **disabled**: responses set `cost_estimation_enabled: false` and omit the other cost keys. Aggregates use `SUM(estimated_cost_usd)` of non-null values; completeness uses priced vs token-bearing interaction counts (see [Completeness](#completeness)).

## Usage API

```text
GET /api/v1/usage/summary?start_date=&end_date=&alert_type=&chain_id=&rank_by=cost|tokens
```

Server-side fleet aggregates for one date window. The dashboard **Usage** page (`/usage`) calls this endpoint for the selected window (presets: Last 7d / 30d / MTD / last calendar month; default 30d) with optional `alert_type` / `chain_id` filters and server-side `rank_by` for the top-20 table — it does not load-all-then-filter.

| Param | Required | Notes |
|-------|----------|--------|
| `start_date` | yes | RFC3339; session `created_at >= start_date` |
| `end_date` | yes | RFC3339; session `created_at < end_date` (half-open, same as Alert History). Window length (`end - start`) must not exceed 365 days. |
| `alert_type` | no | Exact match filter |
| `chain_id` | no | Exact match filter |
| `rank_by` | no | `cost` or `tokens`. Default: `cost` when estimation enabled, else `tokens`. `rank_by=cost` is rejected when estimation is disabled. |

Rules:

- Soft-deleted sessions are always excluded.
- All `interaction_type` values count (same as session token SUMs).
- Response sections: `totals`, `by_model`, `by_alert_type`, `by_chain`, and capped `top_sessions` (hardcoded top **20**; no `limit` param).
- Unpriced top sessions are included with `$0` + `cost_completeness` (not dropped).
- When estimation is disabled: `cost_estimation_enabled: false` and cost fields are omitted; token rollups remain.

Window edge case: a long-running session started before the window is excluded even if it burns tokens inside the window (and late chat on an in-window session is included). Same mental model as Alert History.

## Thinking tokens

- Column: `llm_interactions.thinking_tokens` (nullable).
- Populated from provider usage when available (Google native maps `thoughts_token_count`).
- LangChain backends typically report `0` today — no LangChain thinking extraction in v1.
- Thinking tokens are included in cost when > 0.

## Known gaps

| Gap | Impact |
|-----|--------|
| Cache read/write tokens | Not persisted; estimates may undercount models that discount cached input |
| LangChain thinking usage | Often stored as 0; cost uses input/output only |
| Negotiated enterprise rates | Catalog is public list price — use YAML overrides |
| Historical sessions | Pre-feature rows stay null until a future backfill |

## Completeness

Session list/detail/summary, `ExecutionOverview`, and `GET /api/v1/usage/summary` expose `cost_completeness`. Semantics:

| Completeness | Meaning |
|--------------|---------|
| `complete` | All token-bearing interactions have non-null `estimated_cost_usd` |
| `partial` | Some token-bearing rows are unpriced (null cost) |
| `none` | No priced token-bearing rows |

A **token-bearing** interaction has any of `input_tokens`, `output_tokens`, or `thinking_tokens` > 0. Explicit `$0` is priced (complete); SQL `NULL` is unpriced. Unpriced includes: unknown models, heuristic conflicts, estimation disabled at write time, and pre-feature rows. Completeness is derived from **counts**, not from `COALESCE(SUM(estimated_cost_usd), 0)`.
