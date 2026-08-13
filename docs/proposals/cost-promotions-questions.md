# Cost Promotions — Design Questions

**Status:** All decisions made
**Related:** [Design document](cost-promotions-design.md)

Each question has options with trade-offs and a recommendation. Decisions below; design document updated to match.

---

## Q1: Config shape

Promotions need model, rates, and a time window. That can be a new list under `cost_estimation`, or time fields bolted onto the existing `model_rates` map.

### Option A: Separate `promotions` list

```yaml
cost_estimation:
  model_rates: { … }
  promotions:
    - id: gemini-3.7-flash-intro
      model: gemini-3.7-flash
      start: "…"
      end: "…"
      input_per_million: 0.75
      output_per_million: 3.75
```

- **Pro:** Clear permanent vs temporary semantics; multiple sequential windows per model over time; natural place for `id` / labels.
- **Pro:** Does not complicate the `model_rates` map (exact name → single flat rate).
- **Con:** Second stanza for operators to learn.

**Decision:** Option A — separate `promotions` list under `cost_estimation`. Clear permanent vs temporary semantics; supports sequential windows per model; natural place for `id` / labels.

_Considered and rejected: Option B (window fields on `model_rates` — cannot stack sequential windows, mixes negotiated rates with promos), Option C (same shape as A with stronger docs-only boundary — redundant once A is chosen; docs will still state that time windows belong in `promotions`, not `model_rates`)._

---

## Q2: Priority vs permanent `model_rates`

When both a permanent override and an active promotion exist for the same model, which wins?

### Option B: Active promotions beat `model_rates`

Resolve: active promo → permanent → catalog → snapshot.

- **Pro:** Easy to temporarily force intro pricing even if an override exists.
- **Con:** Surprising if an operator set a permanent rate and a leftover promo silently wins.
- **Con:** Harder to reason about in multi-env GitOps.

**Decision:** Option B — when a promotion matches model + date, it beats everything (including permanent `model_rates`). Outside the window, existing resolve order is unchanged: `model_rates` → catalog → snapshot.

_Considered and rejected: Option A (`model_rates` always beat promotions — blocks temporarily overriding negotiated rates without editing them), Option C (explicit priority/layer — overkill for v1)._

---

## Q3: Evaluation clock

Which timestamp decides whether a promotion is active?

### Option A: Wall clock at `Estimate` / write time (UTC)

- **Pro:** Matches ADR-0020 point-in-time persistence; late chat after promo end uses the new rate.
- **Pro:** Simple; no session join required in the cost package.
- **Con:** A long session spanning a promo boundary can mix rates across interactions (accurate, but not one rate per session).

**Decision:** Option A — evaluate promotions against wall clock UTC at estimate/write time. Long sessions may mix rates across the promo boundary; that is intentional.

_Considered and rejected: Option B (session `created_at` — understates post-promo spend on late tokens), Option C (interaction `created_at` — no practical benefit over A for sync writes)._

---

## Q4: Window format and boundaries

How should operators write windows, and how do we compare `now`?

### Option C: Support both date-only and RFC3339

- **Pro:** Convenience + precision.
- **Con:** More parser/validation surface.

**Decision:** Option C with half-open calendar semantics (variant 1). Accept RFC3339 *or* `YYYY-MM-DD` (UTC). Date-only values mean that day’s `T00:00:00Z`. Window is half-open `[start, end)` for both forms — e.g. `end: "2026-10-01"` runs through 2026-09-30. Docs show date-only as the common case and RFC3339 when operators need sub-day control.

_Considered and rejected: Option A (RFC3339-only — less friendly for “promo through September”), Option B (date-only only — no sub-day control), inclusive-last-day date-only end (variant 2 — different rule for dates vs timestamps)._

---

## Q5: Overlapping promotions

Two promotions for the same model with intersecting windows.

### Option A: Reject at config load / validation

- **Pro:** Fail fast in GitOps; no runtime ambiguity.
- **Pro:** Forces sequential windows to be explicit.
- **Con:** Operator must fix YAML before deploy.

**Decision:** Option A — overlapping windows for the same model are a config validation error.

_Considered and rejected: Option B (lowest price wins — silent policy), Option C (first-listed / highest id — order-dependent footgun)._

---

## Q6: Rate field richness

Permanent overrides today are flat input/output only; catalog supports tiers and reasoning rates.

### Option A: Flat input/output only (parity with `model_rates`)

- **Pro:** Smallest change; thinking tokens fall back to output rate (existing override behavior).
- **Con:** Cannot express promo-specific reasoning discounts or context tiers.

**Decision:** Option A — promotions use flat `input_per_million` / `output_per_million` only; thinking tokens use the output rate.

_Considered and rejected: Option B (optional reasoning rate — defer until needed), Option C (full tiers — out of scope; same as ADR-0020 for overrides)._

---

## Q7: Model matching

Permanent overrides match exact `model_name`. Catalog uses exact then conservative heuristics.

### Option A: Exact `model_name` only

- **Pro:** Same as `model_rates`; no accidental promo on `gemini/…` vs `vertex_ai/…` aliases.
- **Con:** Operator must list each TARSy provider model id they care about.

**Decision:** Option A — promotions match exact `model_name` only (no catalog heuristics).

_Considered and rejected: Option B (catalog heuristics — unsafe accidental matches for money-affecting config)._

---

## Q8: Open-ended promotions

Must every promotion have an `end`? Does `start` need to be required too?

### Option A′: Require `end`; `start` optional (defaults to already-active)

- **Pro:** Hard stop date still prevents forgotten forever-cheap rates.
- **Pro:** Easy to add a current promo mid-window without digging up the real intro date.
- **Pro:** Future scheduling still works when `start` is set.
- **Con:** Omitted `start` means weaker audit of “when did this promo begin.”

**Decision:** Option A′ — `end` is required; `start` is optional and defaults to already-active (`-∞`). When `start` is set, window is half-open `[start, end)` as in Q4. Overlap checks use the effective start.

_Considered and rejected: Option A (require both start and end — unnecessary friction mid-window), Option B (optional end — stale cheap rates), Option C (warn-only open end — not enforced)._

---

## Q9: Snapshot / catalog interplay

Today intro prices were also candidates for `snapshot.json`. With promotions, what is the guidance?

### Option A: Snapshot holds durable/standard rates; promotions hold time-bounded intro rates

- **Pro:** Clear separation; airgap after promo end does not keep intro pricing by accident.
- **Con:** Brand-new models may be unpriced in airgap until snapshot is updated **or** a long-dated promotion covers them.

**Decision:** Option A — snapshot/catalog for durable list rates; promotions for time-bounded intro windows. Escape hatch: if a model is missing from catalog/snapshot, price it via a promotion (or temporary `model_rates`) with a real `end`. **Pragmatic exception:** keep existing `gemini-3.7-flash` intro rates in `snapshot.json` as-is for now; active YAML promotion beats them during the window; expect LiteLLM (then a later snapshot refresh) to carry permanent rates after the promo ends.

_Considered and rejected: Option B (also bake promo into snapshot — no clock, intro rates become permanent), Option C (pragmatic smell allowed as general policy — weak; the 3.7 exception is narrow and already shipped)._

---

## Q10: Config Viewer detail

How much should System → Cost estimation show for promotions?

### Option A: Full list with lifecycle status (`active` / `upcoming` / `expired`), window, rates, optional id

- **Pro:** Operators can verify “are we on intro pricing right now?”
- **Con:** Slightly more API surface on `GET /api/v1/system/config`.

**Decision:** Option A — Config Viewer lists all configured promotions with lifecycle status, window, rates, and optional `id`.

_Considered and rejected: Option B (active-only — harder to debug misses), Option C (flag-only — weak vs showing `model_rates`)._

---

## Q11: Persist promotion on interaction

Should each priced `llm_interaction` record *which* promotion (if any) produced the estimate?

### Option A: No — keep only `estimated_cost_usd` (status quo)

- **Pro:** No migration; provenance stays debug/Viewer-only.
- **Con:** Cannot audit later which promo window applied.

**Decision:** Option A — do not persist promotion identity on `llm_interactions` in v1. Provenance remains in-memory for Config Viewer / resolve debugging only.

_Considered and rejected: Option B (nullable provenance/promotion_id column — schema+API cost beyond “apply promo rates”), Option C (log/metric only — optional later, not required for v1)._

---

## Q12: Is `id` required?

`id` is only a human label for Config Viewer / debug provenance — not used for matching. Requiring it adds YAML noise for a field many operators will not understand.

### Option B: Optional `id`

- **Pro:** Minimal config — `model` + `end` + rates is enough.
- **Pro:** Avoids forcing a confusing label.
- **Con:** Provenance without `id` is `promotion:<model>` and cannot distinguish sequential windows for the same model unless `id` is set.

**Decision:** Option B — `id` is optional. When set, must be unique across the promotions list. Provenance: `promotion:<id>` if set, else `promotion:<model>`.

_Considered and rejected: Option A (require `id` — unnecessary friction; label is not needed for pricing)._

