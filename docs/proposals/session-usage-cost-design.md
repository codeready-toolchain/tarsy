# Session Usage Cost — Technical Design

**Status:** Final

**Related:** [Design questions](session-usage-cost-design-questions.md) (all decisions made) · [Sketch](session-usage-cost-sketch.md)

## Overview

Add **estimated USD cost** next to existing token usage, and a dedicated **Usage** dashboard for fleet dig-in over date windows.

v1 delivers:

1. **Session estimated cost** on Alert History, session detail header, and parallel / sub-agent execution surfaces (when cost estimation is enabled).
2. **Usage page** (`/usage`) with date presets/custom range, token totals, estimated cost (when enabled), breakdowns by model / alert type / chain, and capped top sessions.
3. **Price book**: cached LiteLLM public catalog + YAML overrides + bundled snapshot fallback.
4. **Cluster toggle**: cost estimation **on by default**; when off, tokens-only everywhere (Usage page remains).

Cost is always **estimated** list-price math — not invoice truth. UX copy stays ready for a future `provider-reported` source (sketch Q2).

## Design Principles

1. **Honest estimates.** Soft “Est. $X” + warnings when incomplete; never silent undercount.
2. **Tokens remain primary.** Cost is an overlay; disabling estimation removes $ only.
3. **Point-in-time estimates.** Persist nullable `estimated_cost_usd` at write so history does not drift when the price book changes; pre-feature rows stay null in v1 (no backfill).
4. **GitOps config.** YAML toggle + rate overrides; Config Viewer shows effective rates (read-only). Restart-to-apply for YAML (catalog can still refresh on TTL).
5. **Server owns pricing.** Catalog fetch, matching, and USD math live in Go (`pkg/cost`); the dashboard consumes cost fields.
6. **Same live cadence as tokens.** Cost follows existing REST refresh after WS notifications — no new token/cost WebSocket payloads in v1.
7. **Server-side Usage aggregates.** One summary call for the selected window; UI does not load-all-then-filter.

## Architecture / How It Works

```
Price book (process memory) — pkg/cost
  YAML overrides > cached LiteLLM catalog > bundled snapshot
           |
           v  resolve rates(model_name, input_tokens) at write time
LLM Usage (incl. thinking_tokens when Google native)
  --> persist thinking_tokens
  --> CostEstimator --> llm_interactions.estimated_cost_usd (nullable)
           |
           +--> Session list/detail + ExecutionOverview (SUM cost + tokens)
           +--> GET /api/v1/usage/summary (fleet aggregates for window)
           +--> Config Viewer (enabled flag, overrides, catalog status)
           |
           v
Dashboard: EstimatedCostDisplay next to tokens · Usage page · hamburger → Usage
```

### Write-path inventory

Only **two** production Create sites need cost/thinking fields:

| Site | Notes |
|------|--------|
| `pkg/agent/controller/helpers.go` → `recordLLMInteraction` | Used by iterating (investigation + chat), single-shot (synthesis, exec summary, memory), scoring |
| `pkg/agent/controller/summarize.go` | Direct `CreateLLMInteraction` for summarization |

Chat/scoring/memory are **not** separate Create sites — they already go through `recordLLMInteraction`. (Enum value `chat_response` is unused in writers today; chat rows use `iteration`.)

Also extend: `CreateLLMInteractionRequest`, `InteractionService.CreateLLMInteraction`, Ent schema.

### Data flow — session / execution cost

1. On each write site above:
   - Persist nullable `thinking_tokens` from `resp.Usage.ThinkingTokens` (Google native populates; LangChain stays 0 — store 0/null; no LangChain extraction in v1).
   - If cost estimation **enabled**: resolve rates → compute → persist nullable `estimated_cost_usd` (null if model unpriced).
   - If cost estimation **disabled**: leave `estimated_cost_usd` null (do not price). Thinking tokens still persisted.
2. Session list / detail / `ExecutionOverview` / `SessionSummaryResponse` aggregate with `SUM(estimated_cost_usd)` alongside existing token SUMs (session-level SUM is the same query/helper reused by both `SessionDetailResponse` and `SessionSummaryResponse`).
3. Completeness is derived from **counts of priced vs unpriced token-bearing rows**, not from `SUM` alone (`COALESCE(SUM, 0)` cannot distinguish “truly $0” from “all null”).
4. Dashboard shows “Est. $X” via `EstimatedCostDisplay`; incomplete → warning.

**Existing sessions (v1):** schema-only migration — add nullable columns; pre-feature rows stay `NULL` (unpriced). No SQL backfill.

### Data flow — Usage dashboard

1. Client calls `GET /api/v1/usage/summary` with `start_date` / `end_date` (RFC3339), optional filters (`alert_type`, `chain_id`, …), and `rank_by=cost|tokens`.
2. Window is keyed on **`alert_sessions.created_at`** (same as Alert History). Soft-deleted sessions excluded (`DeletedAtIsNil`), matching the session list.
3. Server returns rollups for that window only: totals, by-model, by alert type, by chain, and capped top sessions — not the full session population.

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

Catalog URL:
`https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`

**Model matching (Q5):** exact `model_name` first, then conservative `*/{model}` / prefix-strip heuristics; YAML overrides win on exact name; conflicting heuristic candidates → unpriced. Record provenance (`override` | `catalog:<key>` | `snapshot:<key>` | `unpriced`) for Config Viewer / debugging.

**Bundled snapshot:** checked-in JSON (full or curated subset) used when fetch fails / airgap; maintainers refresh periodically (not the primary price source).

## Core Concepts

### Schema additions (`llm_interactions`)

| Column | Type | Notes |
|--------|------|--------|
| `thinking_tokens` | nullable int | Same pattern as other token columns |
| `estimated_cost_usd` | nullable float | Prefer Ent `field.Float` (see `investigationmemory.confidence`); aggregates use NullFloat64-style scan |

No new indexes required for v1 session SUMs (existing `(session_id, created_at)`). Usage `GROUP BY model_name` may warrant an index later if slow.

### Estimated cost (write-time)

Per interaction, when estimation is enabled and rates resolve:

```
rates = resolveRates(model, input_tokens)   # may apply above_Nk / tiered_pricing
cost_usd =
    input_tokens    * rates.input
  + output_tokens   * rates.output
  + thinking_tokens * rates.reasoning     # if thinking > 0; else 0
                  # reasoning rate = output_cost_per_reasoning_token if present, else output rate
```

Persist `estimated_cost_usd` (null if unpriced or estimation disabled). Aggregates are `SUM` of stored (non-null) values.

**Tier selection (Q13, B-scoped)** using that interaction’s `input_tokens`:

1. If catalog has `*_above_{N}k_tokens` and input ≥ threshold → use above rates (e.g. Gemini 3.1 Pro at 200k).
2. Else if catalog has `tiered_pricing` ranges → pick one matching tier (OpenClaw-style; no blending).
3. Else → flat base rates.

YAML overrides are **flat** per-million input/output only in v1 (no override tiers / reasoning rate fields). Out of scope: priority / flex / batches, cache token rates.

**Cache tokens:** not persisted — documented limitation.

**Thinking tokens (Q9):** persist column; include in cost when > 0. Google native provides values; LangChain left at unknown/0. Optional UI display of thinking totals is not required for cost.

### Completeness (Q8)

**Token-bearing row:** interaction with any of `input_tokens`, `output_tokens`, or `thinking_tokens` > 0 (failed/empty calls with all-null/zero tokens are excluded from the completeness denominator).

| `cost_completeness` | Meaning | UX |
|---------------------|---------|-----|
| `complete` | All token-bearing interactions have non-null `estimated_cost_usd` | Soft “Est. $X” |
| `partial` | Some token-bearing rows have null cost | Show `SUM` of priced rows + warning |
| `none` | No priced token-bearing rows (or no token-bearing rows) | Hide $ or warning only |

Compute via counts (e.g. `COUNT(*)` token-bearing, `COUNT(estimated_cost_usd)` priced) — **do not** infer completeness from `COALESCE(SUM(estimated_cost_usd), 0)`.

API: `estimated_cost_usd` + `cost_completeness` (+ `unpriced_interaction_count` where useful). Usage/detail may include `unpriced_models`. When estimation disabled, omit cost fields.

### Cost estimation toggle

`system.cost_estimation.enabled` (default **true** if omitted), read-only in Config Viewer. When off: leave cost null at write; hide `$` in APIs/UI; Usage page remains for tokens.

### Execution rollup

Parent `ExecutionOverview` token totals include nested sub-agents today (`session_service.go`). Estimated cost follows the **same rollup** (add sub-agent cost onto parent overview). Session-level SUM counts each interaction once (includes null-`execution_id` rows; those are skipped in per-execution maps — same as tokens today).

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

Wire through: `SystemYAMLConfig` → `config.Config` → `SystemView` / `GET /api/v1/system/config` → Config Viewer System section (+ catalog metadata: last fetch, source, entry count — not the full remote dump).

## APIs

### Enrich existing session responses (Q2)

When cost estimation is enabled:

| Response | Fields |
|----------|--------|
| `DashboardListResponse` | `cost_estimation_enabled` at root; each `DashboardSessionItem` gets `estimated_cost_usd`, `cost_completeness` |
| `SessionDetailResponse` | `cost_estimation_enabled` + session-level cost fields |
| `ExecutionOverview` | per execution / sub-agent cost fields (rolled up like tokens) |
| `SessionSummaryResponse` (`GET /sessions/:id/summary`) | `cost_estimation_enabled` + session-level cost fields — external services consume this endpoint directly, so it gets the same treatment as `SessionDetailResponse` |

When disabled: omit cost fields; `cost_estimation_enabled: false`.

### Usage aggregation (Q3, Q4, Q10, Q11)

```
GET /api/v1/usage/summary?start_date=&end_date=&alert_type=&chain_id=&rank_by=cost|tokens
```

- Date window: session `created_at`; exclude soft-deleted sessions.
- All sections are **server aggregates for that window only**.
- All `interaction_type` values count (match session token SUMs).
- `top_sessions`: hardcoded cap of **20** (no `limit` query param in v1); tokens + Est. `$`; `rank_by` defaults to `cost` when estimation enabled else `tokens`; column sort **re-fetches**. No full paginated session table on Usage in v1.
- Unpriced top sessions: include with `$0` + completeness warning.

Draft response shape:

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
  "by_model": [
    {
      "model_name": "gemini-3.6-flash",
      "input_tokens": 0,
      "output_tokens": 0,
      "total_tokens": 0,
      "estimated_cost_usd": 0.0,
      "priced": true
    }
  ],
  "by_alert_type": [{ "alert_type": "...", "total_tokens": 0, "estimated_cost_usd": 0.0 }],
  "by_chain": [{ "chain_id": "...", "total_tokens": 0, "estimated_cost_usd": 0.0 }],
  "top_sessions": [
    {
      "session_id": "...",
      "alert_type": "...",
      "chain_id": "...",
      "total_tokens": 0,
      "estimated_cost_usd": 0.0,
      "cost_completeness": "complete",
      "created_at": "..."
    }
  ]
}
```

### Config Viewer

Extend `SystemConfigResponse.system` / `SystemSettingsView` with cost-estimation settings + catalog status.

## Frontend

| Area | Change |
|------|--------|
| Routes | `USAGE: '/usage'` in `routes.ts` + `App.tsx` |
| Hamburger (`DashboardView`) | **Usage** next to Manual Alert Submission / System Status (`window.open('/usage', …)`, same pattern) |
| Usage page | Date presets **Last 7d / 30d / MTD / Last calendar month** (default 30d) + custom range. Stock `TimeRangeModal` presets (10m…30d) are insufficient — extend or add a Usage-specific range picker. Optional familiar filters (`alert_type`, `chain_id`). Totals; breakdown tables; capped top sessions with server-side `rank_by`. |
| Session / execution surfaces | `EstimatedCostDisplay` beside existing token UI (list, `SessionHeader`, parallel/sub-agent surfaces) |
| Config Viewer | System / cost estimation section |

Live updates: existing WS → REST refresh; cost fields arrive with session payloads.

## Implementation Plan

One phase = one PR, split further where a phase mixes genuinely different review concerns (pricing math vs. SQL aggregation vs. a new endpoint). Not split down to trivial/single-concern PRs — related code that must be reviewed together (e.g. the estimator and the write path that calls it) stays in one PR. Every PR below is purely additive (new nullable columns, new optional response fields, new endpoint) — `tarsy` keeps working end-to-end after each merge, with no PR left in a broken or half-wired state. Docs/`tarsy.yaml.example` ship in the PR that introduces the corresponding behavior (not a standalone docs PR).

### PR 1 — Price book + write-path persist ✅ Done

Cost is computed and stored, but not yet exposed by any API — a safe, invisible slice.

1. **`pkg/cost`:** catalog fetcher (memory + 24h TTL, timeout/size limits), bundled snapshot, model matching, estimator (flat + B-scoped tiers + thinking/reasoning rate); unit tests (overrides, unpriced, Gemini 3.1 Pro above-200k, disabled toggle).
2. **Config:** `system.cost_estimation` load/validate (enabled default true, flat per-million overrides); expose via `GET /api/v1/system/config` (`SystemView`); update `tarsy.yaml.example`.
3. **Schema:** nullable `thinking_tokens` (int), nullable `estimated_cost_usd` (float); no data backfill. Flow: edit `ent/schema/llminteraction.go` → `make ent-generate` → `make migrate-create NAME=add_llm_interaction_cost_fields` → run `db-migration-review` on the `.up.sql` before commit.
4. **Write path:** extend `CreateLLMInteractionRequest` + `InteractionService`; set thinking + cost in `recordLLMInteraction` and `summarize.go` (skip cost when estimation disabled).
5. **Operator docs:** estimate caveats, tiers, thinking/LangChain/cache gaps, airgap/snapshot fallback, completeness semantics — the backend behavior this PR introduces.

### PR 2 — Session & execution API enrichment

Depends on PR 1's columns. Mechanical extension of existing aggregation code to add cost alongside tokens — reviewed independently of the estimator internals.

1. Cost SUM + completeness counts on list / detail / summary / `ExecutionOverview` (parent rollup like tokens); `cost_estimation_enabled` on list/detail/summary roots. Includes `SessionSummaryResponse` for external-service consumers.
2. Service tests (multi-model, null pre-feature rows, `$0` vs unpriced).

New fields are additive JSON; the dashboard doesn't consume them yet, so nothing changes visibly.

### PR 3 — Usage API

Depends on PR 1's columns (independent of PR 2). A new endpoint with its own aggregation/ranking logic, not an extension of existing code — different review lens from PR 2.

1. `GET /api/v1/usage/summary` (session `created_at` window, soft-delete exclusion, GROUP BYs, top-20 + `rank_by`).
2. Service/integration tests (windows, multi-model, null costs, `rank_by`).

### PR 4 — Dashboard: Est. $ surfaces, Usage page, Config Viewer

Depends on PR 2 + PR 3 response fields.

1. **`EstimatedCostDisplay`** on Alert History list, session detail header, parallel/sub-agent surfaces; hide when estimation disabled / fields absent.
2. **Usage page** (`/usage`): hamburger entry, Usage-oriented date presets (7d / 30d / MTD / last calendar month; default 30d) + custom range, optional familiar filters, totals / breakdowns / top-20 with server-side `rank_by`.
3. **Config Viewer:** System / cost-estimation section (enabled flag, overrides, catalog status).
4. **Frontend tests:** `EstimatedCostDisplay` unit tests, Usage page component tests (`make test-dashboard`).

## Decision summary

| Q | Decision |
|---|----------|
| Q1 | Persist nullable `estimated_cost_usd` at write; schema-only for existing rows; Go backfill as follow-up |
| Q2 | Embed cost fields on existing session/execution DTOs (incl. `SessionSummaryResponse`, used by external services) + `cost_estimation_enabled` |
| Q3 | Single `GET /api/v1/usage/summary` for the selected window (server aggregates only) |
| Q4 | Window on session `created_at` |
| Q5 | Exact match + conservative heuristics; conflict → unpriced |
| Q6 | In-memory catalog + 24h TTL; snapshot fallback |
| Q7 | `system.cost_estimation` with flat per-million overrides |
| Q8 | `cost_completeness` enum + counts (not inferred from SUM) |
| Q9 | Persist `thinking_tokens`; price when > 0; Google native in practice; no LangChain extraction; no cache tokens |
| Q10 | All interaction types count |
| Q11 | Capped top-20 + server `rank_by` (default cost when enabled; no `limit` param) |
| Q12 | Sibling `EstimatedCostDisplay` |
| Q13 | B-scoped tiers (`above_Nk` + `tiered_pricing` array); no priority/flex |

## Out of Scope (design v1)

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
- Backfill of pre-feature `estimated_cost_usd` (see Follow-ups)

## Follow-ups

### One-shot Go backfill for pre-feature rows

Not in v1. Consider a planned one-shot Go backfill (startup job or CLI) that reuses the write-path estimator, batches updates where cost IS NULL, is multi-replica safe, and documents that backfilled `$` uses rates at **backfill time**. Pure SQL migration backfill is not viable.

### LangChain thinking usage extraction

After careful double-count validation, populate `thinking_tokens` from LangChain usage metadata where providers expose it.

### Usage / Alert History sort by tokens or cost

Optional later: `sort_by=total_tokens|estimated_cost_usd` on `GET /sessions`, or a paginated Usage session table.
