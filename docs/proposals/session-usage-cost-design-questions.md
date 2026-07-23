# Session Usage Cost — Design Questions

**Status:** All decisions made
**Related:** [Design document](session-usage-cost-design.md) (Final) · [Sketch](session-usage-cost-sketch.md) · [Sketch questions](session-usage-cost-questions.md)

Each question has options with trade-offs and a recommendation. Decisions below were walked through one by one; the design document reflects the locked choices.

---

## Q1: Should estimated cost be computed on read or persisted at write time?

Sessions are often multi-model. Accurate cost needs **tokens × rates per `model_name`**, not `total_tokens × single rate`. Today list/detail only expose **summed** tokens. This decision drives schema, list-query cost, and whether historical $ changes when the price book updates.

### Option B: Persist nullable `estimated_cost_usd` on each `llm_interaction` at write time (minimal)
- On `recordLLMInteraction` (when input/output tokens are known): resolve rates → compute USD → store **only** that nullable value (no per-row rate card / provenance in v1).
- List/detail/execution/usage aggregate with `SUM(estimated_cost_usd)` like tokens; nulls feed partial/unpriced completeness (sketch Q9).
- **Pro:** Point-in-time cost when the call happened — price-book changes do not rewrite history.
- **Pro:** Deprecated/removed catalog models do not erase $ already stored on old rows.
- **Pro:** Session list stays cheap (`SUM` subquery), same pattern as token aggregates.
- **Con:** Migration + write-path changes; pre-feature rows stay null until optional backfill.
- **Con:** Fixing a wrong override does not retroactively change past sessions (deliberate backfill if ever needed).
- **Con:** Catalog cold start needs snapshot/override discipline so writes can still price when possible.

**Decision:** Option B (minimal) — persist nullable `estimated_cost_usd` at write time; aggregate with `SUM` on read. No per-row rates/provenance in v1.

**Existing sessions (v1):** schema-only migration (add nullable column). Pre-feature rows stay `NULL` → unpriced / partial completeness. Only new interactions get estimates. No SQL backfill (rates are not in the DB; embedding a price table in `.up.sql` is brittle).

**Follow-up (not v1):** consider a planned one-shot Go backfill (startup job or CLI) that reuses the write-path estimator (snapshot + overrides, catalog if loaded), batched and multi-replica safe. Document that backfilled `$` uses rates available at backfill time, not true historical list prices when the session ran.

_Considered and rejected: Option A (on-read — history drifts when prices change; deprecated models risk losing estimates), Option C (hybrid — extra mental model without buying v1 simplicity once we accept a column), SQL migration backfill (no in-DB rates; wrong place for catalog/overrides)._

---

## Q2: How should cost fields attach to existing session APIs?

The sketch requires Est. $ on list, detail header, and execution overviews. We need a stable DTO contract the dashboard can consume without a second round-trip per session.

### Option A: Embed cost fields on existing DTOs
- Add `estimated_cost_usd` + completeness on `DashboardSessionItem`, `SessionDetailResponse`, and `ExecutionOverview` (when estimation enabled).
- **Pro:** Zero extra requests; list/detail/execution UIs already have the payload.
- **Pro:** Live refresh via existing WS→REST paths picks up cost automatically.
- **Con:** Slightly wider list payload; must define behavior when estimation is disabled (omit vs null).

**Decision:** Option A — flat cost fields next to existing token fields on list, detail, and `ExecutionOverview`. When estimation is disabled, omit cost fields and expose `cost_estimation_enabled` on the list/detail response root so the UI can hide $.

**Amendment (verify pass):** also embed the same fields on `SessionSummaryResponse` (`GET /sessions/:id/summary`). It carries session-level token totals like `SessionDetailResponse` and is consumed directly by external services (not just the dashboard), so it gets the same cost/completeness treatment for consistency.

_Considered and rejected: Option B (separate usage endpoint — breaks glanceable list cost), Option C (nested envelope — extra mapping for little gain)._

---

## Q3: What shape should the Usage aggregation API have?

The Usage page needs totals, by-model, by alert type/chain, and top sessions for one date window (sketch Q8/Q11).

### Option A: Single `GET /api/v1/usage/summary` returning all sections
- Query params carry the date window (+ optional familiar filters). Server aggregates **only for that window**.
- Response is rollups (totals, GROUP BY model / alert type / chain) plus a **capped** `top_sessions` list — not the full session population.
- **Pro:** One round-trip; matches “snapshot for this window” mental model (similar to Config Viewer).
- **Pro:** Simpler frontend loading/error states; UI does not load-all-then-filter.
- **Con:** Less flexible if one section becomes expensive and needs independent pagination later.

**Decision:** Option A — one `GET /api/v1/usage/summary` for the selected range (and optional filters). All sections are server-aggregated for that window only. UI does **not** fetch every session and filter client-side. Cap `top_sessions` (e.g. 10–25); breakdowns are GROUP BY rows, not full session lists. Split/paginate later only if needed.

_Considered and rejected: Option B (split endpoints — extra boilerplate for v1), Option C (overload `GET /sessions` — poor fit for aggregates)._

---

## Q4: What timestamp defines the Usage date window?

Session list filters on `alert_sessions.created_at`. Usage could instead filter on `llm_interactions.created_at` (when tokens were actually spent).

### Option A: Session `created_at` (same as Alert History)
- **Pro:** Consistent with existing filters; “sessions started in this window.”
- **Pro:** Simpler joins for by-alert-type / by-chain / top sessions.
- **Con:** A long-running session started before the window but still burning tokens mid-window is excluded; conversely, late chat on an old session is included if the session started inside the window.

**Decision:** Option A — Usage date window filters on `alert_sessions.created_at` (same semantics as Alert History). Document the edge case; revisit interaction/`spend-time` basis later if operators need FinOps-style windows.

_Considered and rejected: Option B (interaction `created_at` — better spend accuracy but divergent mental model for v1), Option C (dual mode — extra surface for v1)._

---

## Q5: How should we match `llm_interactions.model_name` to LiteLLM catalog keys?

TARSy stores the provider YAML `model` field (e.g. `gemini-2.5-pro`). LiteLLM keys are often prefixed (`gemini/gemini-2.5-pro`, `openai/gpt-4o`, etc.) and include aliases. Bad matching → many “unpriced” warnings.

### Option A: Exact match, then conservative suffix/prefix heuristics
- Try exact key; then `*/{model}`; then strip known provider prefixes; YAML overrides always win on exact TARSy `model_name`.
- **Pro:** Good OOTB hit rate without maintaining a huge alias table.
- **Pro:** Overrides can always use the exact TARSy `model_name`.
- **Con:** Heuristics can mis-associate similarly named models (rare but possible) — mitigate by treating conflicting candidates as unpriced.

**Decision:** Option A — exact match first, then conservative heuristics; YAML overrides win on exact `model_name`; if multiple heuristic candidates conflict, leave unpriced rather than guessing. Record resolve provenance (`override` | `catalog:<key>` | `snapshot:<key>` | `unpriced`) for Config Viewer / debugging.

_Considered and rejected: Option B (exact-only — weak OOTB without large alias maintenance), Option C (require `provider/model` everywhere — out of scope for this feature)._

---

## Q6: How should the remote catalog be fetched and cached?

Sketch: LiteLLM JSON cached, bundled snapshot fallback. OpenClaw uses ~24h TTL, size limits, and in-memory cache.

### Option A: In-memory cache + periodic refresh (startup kick + TTL)
- **Pro:** Simple; enough for one process; matches no-hot-reload YAML (catalog can still refresh without restart).
- **Pro:** Snapshot covers offline/airgap and fetch failure.
- **Con:** Each replica fetches independently (acceptable for a public JSON).

**Decision:** Option A — in-memory catalog cache with startup fetch + periodic refresh (default 24h TTL). Bundled snapshot as fallback on failure/airgap. Fail soft: writes still price via snapshot/overrides when possible; surface catalog staleness in Config Viewer. On-disk cache left as a possible fast-follow.

_Considered and rejected: Option B (on-disk cache — writable-path complexity for v1), Option C (startup-only fetch — stale prices on long-lived processes)._

---

## Q7: Where does cost config live in YAML, and what is the override unit?

Need a home for `enabled` + rate overrides that fits `tarsy.yaml` and Config Viewer.

### Option A: `system.cost_estimation` with per-million USD rates
```yaml
system:
  cost_estimation:
    enabled: true
    model_rates:
      gemini-2.5-pro:
        input_per_million: 1.25
        output_per_million: 10.0
```
- **Pro:** Matches Slack-style system toggles; per-million is human-friendly (LiteLLM stores per-token).
- **Pro:** Config Viewer System section is a natural home.
- **Con:** Slight unit conversion vs LiteLLM’s per-token fields (document clearly).

**Decision:** Option A — `system.cost_estimation` with per-million override fields (convert to per-token internally). Default `enabled: true` when the block is omitted entirely.

_Considered and rejected: Option B (top-level key — extra Config Viewer root for v1), Option C (rates on `llm_providers` — pricing keys on model id, not provider key)._

---

## Q8: How should incompleteness be represented in API responses?

Sketch Q9: always show partial $ when possible, with an explicit warning when anything is unpriced.

### Option A: Enum + counts
- `cost_completeness`: `complete` | `partial` | `none`
- Optional `unpriced_interaction_count` / `unpriced_models[]`
- **Pro:** Simple for UI (icon/tooltip branching); actionable for operators (“which models?”).
- **Con:** Slightly more fields on list rows (can omit `unpriced_models` on list, keep on detail/usage).

**Decision:** Option A — `estimated_cost_usd` + `cost_completeness` (`complete` | `partial` | `none`) (+ `unpriced_interaction_count` where useful). Usage/detail may include `unpriced_models` for dig-in. When estimation is disabled, omit cost fields. Unpriced includes null persisted costs (no match at write, or pre-feature rows).

_Considered and rejected: Option B (nullable-only — can’t distinguish $0 vs unpriced vs disabled), Option C (two dollar fields — confusing glance UX)._

---

## Q9: How should v1 treat thinking-token and cache-token gaps?

Thinking tokens already flow proto → gRPC → Go/`TokenUsage`/Prometheus for Google native, but are **dropped** at `recordLLMInteraction`. Cache tokens are not tracked at all. LiteLLM has `output_cost_per_reasoning_token` for some models.

### Option B+: Persist `thinking_tokens` in v1; price when present; no LangChain extraction yet
- Add nullable `thinking_tokens` on `llm_interactions`; set from `resp.Usage.ThinkingTokens` on all writes (same path for every backend).
- Google native already populates the field → real counts + estimate uses reasoning rate when catalog has it, else output rate.
- LangChain leaves thinking at `0` today → store null/0; price input/output only. Do **not** teach LangChain usage extraction in v1 (incorrect mapping is the real risk).
- Cache tokens remain a documented limitation (no proto/DB work).
- **Pro:** Low lift (stop dropping an existing field); better Gemini-native estimates; no risky LangChain parsing.
- **Con:** LangChain/thinking coverage incomplete until a follow-up; still no cache; no backfill for history.

**Decision:** Option B+ — persist nullable `thinking_tokens` and include in write-time cost when > 0. Rely on Google-native values in v1; leave LangChain at unknown/0 (no extraction work). Cache gaps stay documented only. Optional UI display of thinking totals is not required for cost.

_Considered and rejected: Option A (input/output only — leaves an easy Google-native win on the table), Option C (block $ UI on full cache+thinking proto work), full LangChain thinking extraction in v1 (risky without careful double-count validation)._

---

## Q10: Which `interaction_type` values count toward usage and cost?

`llm_interactions` includes `iteration`, `final_analysis`, `executive_summary`, `chat_response`, `summarization`, `synthesis`, `forced_conclusion`, `scoring`, `memory_extraction`, etc. Session token totals today sum **all** types.

### Option A: All interaction types (match session token totals)
- **Pro:** Session Est. $ reconciles with session token totals; no “why don’t these match?” surprise.
- **Pro:** Usage reflects real LLM spend including chat/scoring/summarization.
- **Con:** Fleet “investigation cost” includes post-hoc chat/scoring.

**Decision:** Option A — all `interaction_type` values count toward usage and cost, matching existing session token SUMs.

_Considered and rejected: Option B (investigation-only — diverges from token totals), Option C (category filter — extra UI/API for v1)._

---

## Q11: How should “top sessions” be ranked on the Usage page?

Top sessions is a **capped highlight reel** for the selected window (e.g. ~20 rows with tokens + Est. `$`), not a paginated “all sessions in range” table (that would overlap Alert History and needs more API/UI work).

### Option C+: Capped top-N with server-side `rank_by` (default cost when enabled)
- Table columns: tokens + estimated cost (when enabled).
- API supports `rank_by=cost|tokens`; column-header sort **re-fetches** (do not re-sort only the already-returned N rows — wrong set for the other metric).
- Default: `cost` when estimation enabled, else `tokens`.
- **Pro:** Answers “expensive / heavy investigations lately?” without loading the full fleet into the Usage UI.
- **Pro:** Correct ranking for either metric with little extra work over a single default sort.
- **Con:** Not a full session browser; users go to Alert History (or a later follow-up) for exhaustive paging.

**Decision:** Option C+ — capped top-**20** on Usage (hardcoded; no `limit` query param in v1); tokens + Est. `$` columns; server-side `rank_by` with default cost when estimation is on (else tokens). No full paginated session table on Usage in v1. Unpriced sessions: include with `$0` + completeness warning rather than dropping them silently.

_Considered and rejected: Option A (tokens-only ranking — weak when `$` is the point), Option B (single fixed ranking — almost as easy to add `rank_by`), full paginated “all sessions in range” on Usage (duplicates Alert History; more work)._

---

## Q12: How should the dashboard render estimated cost next to tokens?

### Option B: New sibling `EstimatedCostDisplay` used next to token components
- **Pro:** Clean separation; easy to unmount when estimation disabled.
- **Pro:** Shared formatting + tooltip + incomplete warning; `TokenUsageDisplay` stays stable.
- **Con:** Two components to place in every surface.

**Decision:** Option B — shared `EstimatedCostDisplay` (or equivalent) composed next to existing token UI / header footer; no-op / omitted when estimation disabled or cost fields absent.

_Considered and rejected: Option A (extend `TokenUsageDisplay` — mixed responsibilities), Option C (ad-hoc per surface — inconsistent Est. $/warning UX)._

---

## Q13: Should v1 support LiteLLM tiered pricing?

LiteLLM expresses context tiers two ways: `tiered_pricing` arrays (~20 models) and `*_above_{N}k_tokens` fields (~212 models). Checked for current TARSy-relevant Gemini ids:

| Model (catalog) | Context-tiered? |
|-----------------|-----------------|
| `gemini-3.6-flash` | **No** — flat input/output (+ priority/flex/batches only) |
| `gemini-3.1-pro-preview` | **Yes** — `input/output_cost_per_token_above_200k_tokens` |

### Option B-scoped: Threshold + `tiered_pricing` array at write time
- At write time, use that interaction’s `input_tokens` to pick rates:
  - if `*_above_{N}k_tokens` applies and input ≥ threshold → use above rates
  - else if `tiered_pricing` ranges exist → OpenClaw-style single-tier pick
  - else → flat base rates
- Skip priority/flex/batches maze in v1.
- **Pro:** Correct list-price behavior for Gemini 3.1 Pro (and other above-200k models) without much extra code.
- **Con:** Slightly more catalog parsing than flat-only.

**Decision:** Option B-scoped — support `above_{N}k` thresholds and `tiered_pricing` arrays when present; pick tier from the interaction’s input size at write time. Do not implement priority/flex/batches pricing in v1. Driven by Gemini 3.1 Pro being tiered (3.6 Flash is flat).

_Considered and rejected: Option A (flat-only — under-estimates Gemini 3.1 Pro prompts ≥ 200k tokens), full LiteLLM dialect (priority/flex/etc. — out of scope)._
