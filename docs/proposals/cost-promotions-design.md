# Cost Promotions — Design

**Status:** Final — decisions recorded in [cost-promotions-questions.md](cost-promotions-questions.md)

## Overview

TARSy estimates LLM spend from a price book (today: YAML `model_rates` → LiteLLM catalog → bundled snapshot). Provider intro pricing is often **time-bounded** (for example Gemini 3.7 Flash introductory rates that later rise to a standard list price). LiteLLM and the bundled snapshot cannot express “cheap until date X”; today operators either bake promo rates into the snapshot (wrong after the window) or permanent `model_rates` (wrong outside the window, or always require a restart/edit when the promo ends).

**Cost promotions** add GitOps-configured, time-sensitive rate windows for exact `model_name`s. While a promotion is active (model + date), its rates **beat every other price-book source**, including permanent `model_rates`, for new interaction writes. When cost estimation is **disabled**, promotions are ignored like other rates (`estimated_cost_usd` stays null). Historical `estimated_cost_usd` values remain point-in-time (ADR-0020) — promotions never rewrite past sessions.

## Design Principles

1. **Point-in-time stays sacred.** Evaluate promotions at estimate/write time; never reprice stored rows.
2. **GitOps-first.** Promotions live in `tarsy.yaml` next to existing cost estimation config; restart loads definitions.
3. **Time activates without restart.** Once loaded, crossing a window boundary changes which rate applies without a redeploy.
4. **Active promo beats everything.** When model + date match, promotion rates outrank `model_rates`, catalog, and snapshot.
5. **Fail closed on ambiguity.** Overlapping windows for the same model are a config error, not a runtime guess.
6. **Minimal surface.** Flat per-million input/output only in v1 — parity with existing overrides; no new DB columns.

## Architecture / How It Works

### Resolve order

```
Resolve(model_name, input_tokens, now)
  1. Active promotion for exact model_name where effectiveStart <= now < end
  2. YAML model_rates (permanent, exact name)
  3. Remote LiteLLM catalog (tiered / above_Nk as today)
  4. Bundled snapshot
  → else unpriced
```

`now` is wall-clock UTC at `Estimate` / write time.

### Write path (unchanged shape)

```
LLM interaction create
  → CostBook.Estimate(model, tokens…)   # uses wall clock for promo windows
  → persist estimated_cost_usd (nullable)
```

No schema change. Provenance may be `promotion:<id>` for Config Viewer / resolve debugging; it is **not** persisted on `llm_interactions`.

### Config load vs time activation

| Concern | Behavior |
|---------|----------|
| Add/edit/remove promotion YAML | Requires process restart (same as `model_rates`) |
| Promo start/end crosses “now” | Takes effect on next `Estimate` without restart |
| Catalog TTL refresh | Unaffected; active promotions sit above catalog |

### Relationship to snapshot / LiteLLM

| Source | Role |
|--------|------|
| **Promotions** | Time-bounded rates (intro pricing); beat all other sources while active |
| **`model_rates`** | Permanent negotiated / private rates (when no active promo) |
| **Catalog** | Current public list when reachable |
| **Snapshot** | Airgap / fetch-failure baseline of **durable** rates |

Do not bake *new* time-bounded intro prices into `snapshot.json` when a promotion can express the window. Snapshot remains the airgap/fetch-failure baseline. **Exception already in tree:** `gemini-3.7-flash` intro rates stay in the snapshot as-is for now; a YAML promotion covers the window while active (and beats the snapshot). After the intro period, rely on LiteLLM catalog refresh (and a later snapshot update) for durable rates rather than rewriting the snapshot as part of shipping promotions.

## Core Concepts

### Promotion

A temporary flat rate for one exact TARSy `model_name`, configured as a list under `cost_estimation` (not as fields on `model_rates`):

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

`id` is **optional** — an operator-friendly label only. Matching and pricing use `model` + window. If set, `id` must be unique across the promotions list. Time windows live only in `promotions`, never on `model_rates`.

### Window format

- Accept **RFC3339** (any offset; normalized to UTC) or **`YYYY-MM-DD`** (interpreted as UTC midnight).
- Date-only values mean that day’s `T00:00:00Z`.
- Interval is half-open **`[start, end)`** for both forms.
- Example: `end: "2026-10-01"` → promo runs through 2026-09-30 UTC.
- **`end` is required.** **`start` is optional**; omitted start means already active (effective start = `-∞`).
- Overlap checks use the effective start.
- **Expired and upcoming entries are valid config** — they remain loadable so Config Viewer can show `expired` / `upcoming`. Do not reject solely because `end <= now` or `start > now`.
- Lifecycle: `upcoming` only when `start` is set and `now < start`; `active` when in window; `expired` when `now >= end`. Omitted `start` is never `upcoming`.

### Active window

At estimate time `now`, a promotion applies iff:

- `model` equals the interaction’s `model_name` exactly (no catalog heuristics), and
- `effectiveStart <= now < end`.

A long session that spans a promo boundary may mix rates across interactions; that is intentional (per-interaction point-in-time estimates).

### Overlaps

Two promotions for the same `model` with intersecting windows are a **config validation error** at load. Sequential non-overlapping windows are allowed.

### Rates

Same flat fields as `model_rates`: `input_per_million`, `output_per_million`. Thinking tokens use the output rate. No promo-specific reasoning rates or tiers in v1.

### Provenance

Returned by `Book.Estimate` for tests/debugging (not stored on `llm_interactions`):

| Provenance | Meaning |
|------------|---------|
| `promotion:<id>` | Active promotion when `id` is set |
| `promotion:<model>` | Active promotion when `id` is omitted |
| `override` | Permanent `model_rates` |
| `catalog:<key>` | Remote catalog |
| `snapshot:<key>` | Bundled snapshot |
| `unpriced` | No rate |

(If multiple sequential windows share a model and omit `id`, provenance collapses to the same `promotion:<model>` string — acceptable for v1 debug; use `id` only when you care to distinguish them.)

### Config Viewer

Extend existing surfaces (today: `cost.Status` → `CostEstimationView` → dashboard `ConfigViewer` / `system.ts`):

Under System → Cost estimation, list **all** configured promotions with:

- `id` (optional), model, rates, window (`start` may be null)
- lifecycle status from wall clock: `active` | `upcoming` | `expired`

Book `Status()` is the source of truth when the book is wired (same pattern as `model_rates` + catalog today); config-only fallback includes promotions from YAML without live lifecycle if the book is nil.

## Implementation Plan

Single PR. Touch points:

1. **Config** — Extend `CostEstimationYAMLConfig` / `CostEstimationConfig` / `cost.Config` with a `promotions` list (resolved structs hold parsed `time.Time` windows). Wire through `resolveCostEstimationConfig` and `costConfigFrom` in `cmd/tarsy`.
2. **Validation** — In `Validator.validateCostEstimation` (alongside `model_rates`):
   - required non-empty `model`, required `end`, `input_per_million` / `output_per_million` `>= 0` (same as overrides)
   - parse start/end (RFC3339 or date); `end > effectiveStart`
   - no overlapping windows per model; if `id` is set, it must be unique across the list
3. **Price book** — Teach `Book.resolveLocked` / `Estimate` to check active promotions **first** using `time.Now().UTC()` (injectable clock for tests). Add `ProvenancePromotion` and extend `cost.Status`.
4. **Config Viewer** — Extend `CostEstimationView` / `buildCostEstimationView`, dashboard `system.ts`, and `ConfigViewer` Cost estimation section (full list + lifecycle status).
5. **Docs / example** — Update `docs/session-usage-cost.md` and `deploy/config/tarsy.yaml.example` (include Gemini 3.7 Flash intro promotion example).
6. **Tests** — active / before start / after end / omitted start / date-only vs RFC3339 / overlap reject / promo beats `model_rates` / exact-name miss / disabled estimation ignores promos / expired still loads; Viewer/API coverage as needed.

**Snapshot:** Leave `gemini-3.7-flash` intro rates in `pkg/cost/snapshot.json` unchanged. Active YAML promotion beats them during the window; expect LiteLLM (then a later snapshot refresh) for durable post-promo rates.

**Follow-up (separate PR):** New ADR documenting promotions and resolve-order change — do not amend historical ADRs.

### Out of scope

- Persisting promotion id / provenance on `llm_interactions`
- Hot-reload of YAML without restart
- Promo tiers / reasoning rates / priority / flex / batch
- Dashboard Usage breakdown “cost under promotion”
- Auto-sync of provider promo calendars
- Amending historical ADRs / rewriting snapshot 3.7 rates in this PR

## Decisions

| # | Decision |
|---|----------|
| Q1 | Separate `promotions` list under `cost_estimation` |
| Q2 | Active promo beats everything, including `model_rates` |
| Q3 | Wall clock UTC at write / `Estimate` time |
| Q4 | RFC3339 or `YYYY-MM-DD` (UTC); half-open `[start, end)` |
| Q5 | Reject overlapping windows at config load |
| Q6 | Flat input/output only |
| Q7 | Exact `model_name` match only |
| Q8 | `end` required; `start` optional (defaults to already-active) |
| Q9 | Snapshot holds durable rates; promotions hold time windows (keep existing 3.7 snapshot intro rates as-is) |
| Q10 | Config Viewer: full list + lifecycle status |
| Q11 | Do not persist promotion identity on interactions |
| Q12 | `id` optional (label only); provenance falls back to `promotion:<model>` |

## References

- [Cost Promotions — Questions](cost-promotions-questions.md)
- [ADR-0020: Session Usage Cost](../adr/0020-session-usage-cost.md)
- [Session Usage Cost Estimation](../session-usage-cost.md)
- `pkg/cost/` — `Book`, resolve order, overrides, snapshot
