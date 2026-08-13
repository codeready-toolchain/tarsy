# ADR-0023: Cost Promotions

**Status:** Implemented
**Date:** 2026-08-13

## Overview

TARSy estimates LLM spend from a price book (YAML `model_rates` → LiteLLM catalog → bundled snapshot). Provider intro pricing is often **time-bounded** (for example Gemini 3.7 Flash introductory rates that later rise to a standard list price). Neither the remote catalog nor the bundled snapshot can express “cheap until date X”; operators previously either baked promo rates into the snapshot (wrong after the window) or permanent `model_rates` (wrong outside the window, or always require a restart/edit when the promo ends).

**Cost promotions** add GitOps-configured, time-sensitive rate windows for exact `model_name`s. While a promotion is active (model + date), its rates **beat every other price-book source**, including permanent `model_rates`, for new interaction writes. When cost estimation is **disabled**, promotions are ignored like other rates (`estimated_cost_usd` stays null). Historical `estimated_cost_usd` values remain point-in-time ([ADR-0020](0020-session-usage-cost.md)) — promotions never rewrite past sessions.

**Operator guide:** [Session Usage Cost Estimation](../session-usage-cost.md)

## Design Principles

1. **Point-in-time stays sacred.** Evaluate promotions at estimate/write time; never reprice stored rows.
2. **GitOps-first.** Promotions live in `tarsy.yaml` next to existing cost estimation config; restart loads definitions.
3. **Time activates without restart.** Once loaded, crossing a window boundary changes which rate applies without a redeploy.
4. **Active promo beats everything.** When model + date match, promotion rates outrank `model_rates`, catalog, and snapshot.
5. **Fail closed on ambiguity.** Overlapping windows for the same model are a config error, not a runtime guess.
6. **Minimal surface.** Flat per-million input/output only in v1 — parity with existing overrides; no new DB columns.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Config shape | Separate `promotions` list under `cost_estimation` | Clear permanent vs temporary semantics; supports sequential windows per model; natural place for optional `id`. Rejected: window fields on `model_rates` (cannot stack windows; mixes negotiated rates with promos). |
| Q2 | Priority vs `model_rates` | Active promo beats everything, including permanent overrides | Lets operators temporarily force intro pricing without editing negotiated rates. Outside the window, resolve order is unchanged. Rejected: overrides always win (blocks temporary override); explicit priority layer (overkill for v1). |
| Q3 | Evaluation clock | Wall clock UTC at write / `Estimate` time | Matches ADR-0020 point-in-time persistence; late chat after promo end uses the new rate; no session join in the cost package. Long sessions may mix rates across the promo boundary — intentional. Rejected: session or interaction `created_at`. |
| Q4 | Window format | RFC3339 or `YYYY-MM-DD` (UTC); half-open `[start, end)` | Date-only convenience plus sub-day precision when needed. Date-only means that day’s `T00:00:00Z`. Rejected: RFC3339-only, date-only-only, inclusive-last-day date ends. |
| Q5 | Overlapping windows | Reject at config load | Fail fast in GitOps; no runtime ambiguity; sequential non-overlapping windows remain allowed. Rejected: lowest price wins; first-listed / highest id. |
| Q6 | Rate richness | Flat `input_per_million` / `output_per_million` only | Parity with `model_rates`; thinking tokens use the output rate. Rejected: promo-specific reasoning rates or tiers (defer). |
| Q7 | Model matching | Exact `model_name` only | Same as overrides; avoids accidental promo on catalog aliases (`gemini/…` vs `vertex_ai/…`). Rejected: catalog heuristics (unsafe for money-affecting config). |
| Q8 | Open-ended promotions | `end` required; `start` optional (already-active / `-∞`) | Hard stop prevents forgotten forever-cheap rates; easy to add a current promo mid-window; scheduling still works when `start` is set. Rejected: require both; optional end; warn-only open end. |
| Q9 | Snapshot / catalog | Snapshot holds durable rates; promotions hold time windows | Clear separation so airgap after promo end does not keep intro pricing by policy. Escape hatch: price missing models via a dated promotion or temporary override. **Narrow exception:** existing `gemini-3.7-flash` intro rates remain in the bundled snapshot as shipped; an active YAML promotion beats them during the window; expect LiteLLM (then a later snapshot refresh) for durable post-promo rates. Rejected: bake promos into snapshot as general policy. |
| Q10 | Config Viewer | Full list + lifecycle (`active` / `upcoming` / `expired`) | Operators can verify “are we on intro pricing right now?” Rejected: active-only; flag-only. |
| Q11 | Persist promotion on interaction | Do not persist promotion identity | No migration; provenance stays debug / Viewer-only (`estimated_cost_usd` only). Rejected: nullable provenance column; log/metric-only as a requirement. |
| Q12 | Promotion `id` | Optional label | Matching uses `model` + window; provenance is `promotion:<id>` when set, else `promotion:<model>`. When set, `id` must be unique across the list. Rejected: required `id`. |

## Architecture

### Resolve order

```
Resolve(model_name, input_tokens, now)
  1. Active promotion for exact model_name where effectiveStart <= now < end
  2. YAML model_rates (permanent, exact name)
  3. Remote LiteLLM catalog (tiered / above_Nk as today)
  4. Bundled snapshot
    → else unpriced
```

`now` is wall-clock UTC at estimate / write time.

### Write path

```
LLM interaction create
  → price book Estimate(model, tokens…)   # wall clock for promo windows
  → persist estimated_cost_usd (nullable)
```

No schema change. Provenance may be `promotion:<id|model>` for Config Viewer / resolve debugging; it is **not** persisted on interactions.

### Config load vs time activation

| Concern | Behavior |
|---------|----------|
| Add/edit/remove promotion YAML | Requires process restart (same as `model_rates`) |
| Promo start/end crosses “now” | Takes effect on next estimate without restart |
| Catalog TTL refresh | Unaffected; active promotions sit above catalog |

### Price-book source roles

| Source | Role |
|--------|------|
| **Promotions** | Time-bounded rates (intro pricing); beat all other sources while active |
| **`model_rates`** | Permanent negotiated / private rates (when no active promo) |
| **Catalog** | Current public list when reachable |
| **Snapshot** | Airgap / fetch-failure baseline of **durable** rates |

Do not bake *new* time-bounded intro prices into the bundled snapshot when a promotion can express the window.

## Configuration

```yaml
system:
  cost_estimation:
    enabled: true
    model_rates: { … }          # permanent; loses to an active promotion
    promotions:
      - model: gemini-3.7-flash
        start: "2026-08-01"       # optional; omit = already active
        end: "2026-10-01"         # required; half-open exclusive bound
        input_per_million: 0.75
        output_per_million: 3.75
      # Optional label for Viewer / debug provenance only:
      # - id: gemini-3.7-flash-intro
      #   model: …
```

### Window format

- Accept **RFC3339** (any offset; normalized to UTC) or **`YYYY-MM-DD`** (UTC midnight).
- Interval is half-open **`[start, end)`** for both forms (e.g. `end: "2026-10-01"` runs through 2026-09-30 UTC).
- **`end` is required.** **`start` is optional** (effective start = `-∞`).
- Expired and upcoming entries are valid config so Config Viewer can show lifecycle.
- Lifecycle: `upcoming` only when `start` is set and `now < start`; `active` when in window; `expired` when `now >= end`. Omitted `start` is never `upcoming`.

### Active window

A promotion applies at estimate time iff:

- `model` equals the interaction’s `model_name` exactly, and
- `effectiveStart <= now < end`.

A long session that spans a promo boundary may mix rates across interactions (per-interaction point-in-time estimates).

### Rates and provenance

Same flat fields as `model_rates`. Thinking tokens use the output rate.

| Provenance | Meaning |
|------------|---------|
| `promotion:<id>` | Active promotion when `id` is set |
| `promotion:<model>` | Active promotion when `id` is omitted |
| `override` | Permanent `model_rates` |
| `catalog:<key>` | Remote catalog |
| `snapshot:<key>` | Bundled snapshot |
| `unpriced` | No rate |

### Config Viewer

Under System → Cost estimation, list **all** configured promotions with optional `id`, model, rates, window (`start` may be null), and lifecycle status from wall clock: `active` | `upcoming` | `expired`. The live price book status is the source of truth when wired; config-only fallback still lists promotions from YAML.

## Out of scope / future

- Persisting promotion id / provenance on interactions
- Hot-reload of YAML without restart
- Promo tiers / reasoning rates / priority / flex / batch
- Dashboard Usage breakdown “cost under promotion”
- Auto-sync of provider promo calendars
- Rewriting bundled snapshot intro rates for Gemini 3.7 Flash (follow-up when durable list prices are known)

## References

- [ADR-0020: Session Usage Cost](0020-session-usage-cost.md)
- [Session Usage Cost Estimation](../session-usage-cost.md)
- [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md)
