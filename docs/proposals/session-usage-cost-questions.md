# Session Usage Cost — Sketch Questions

**Status:** All decisions made
**Related:** [Sketch document](session-usage-cost-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: Who is the primary audience, and what decision should this unlock first?

This sets the product north star. Session cost and a fleet usage dashboard serve overlapping but different jobs. The primary audience determines what we optimize for in v1 (glanceable session UX vs analytics depth vs finance-grade accuracy).

### Option C: Both — session cost in everyday UX + usage dashboard in the same sketch
- **Pro:** Session cost lands where attention already is (list + detail next to tokens).
- **Pro:** Usage dashboard answers fleet questions with date windows (last 30d, MTD, etc.) for both tokens and estimated cost.
- **Pro:** Matches how operators described the need: glanceable session spend *and* a place to dig deeper.
- **Pro:** Leaves FinOps/invoice reconciliation out without shrinking the product to “tokens only.”
- **Con:** Broader than session-only; needs clear bounds on dashboard depth (see Q8, Q11).

**Decision:** Option C — both surfaces are in scope for this sketch. Estimated cost next to token usage on sessions (detail + list), plus a usage dashboard for deeper filtering/exploration by date window. Not FinOps-grade invoice truth.

_Considered and rejected: Option A (session-only — leaves date-window fleet questions unanswered), Option B (fleet-first — underweights the everyday session glance), Option D (FinOps — wrong fidelity bar for v1)._

---

## Q2: What cost fidelity should v1 commit to in the product story?

Research showed four tiers (list-price estimate → gateway-reported cost → admin billing APIs → cloud CUR). The sketch needs a clear product promise so the UI does not overclaim.

### Option B: Estimate now, UX designed for a future “provider-reported” source
- **Pro:** Achievable with current token data + a price book; useful for session and fleet comparisons.
- **Pro:** Labels/fields can grow later (`estimated` vs `reported`) without a UX rewrite.
- **Pro:** Aligns with OpenRouter/LiteLLM/Langfuse patterns if those enter the path later.
- **Con:** Slightly more conceptual surface area in copy and tooltips than a pure estimate-only story.

**Decision:** Option B — v1 shows estimated cost from rates; UX/copy stay ready for a stronger provider-reported source later. Invoice reconciliation stays out of scope.

_Considered and rejected: Option A (estimate-only with no room for a future reported source — would force a UX rewrite later), Option C (require real/provider cost in v1 — blocks the feature for most direct providers)._

---

## Q3: Where should the usage dashboard live?

There is no analytics route today. Discovery today is via the dashboard hamburger menu (Manual Alert Submission, System Status).

### Option D: Hamburger menu entry “Usage” → dedicated page (e.g. `/usage`)
- **Pro:** Same discovery path as Manual Alert Submission and System Status — familiar, no new header chrome.
- **Pro:** Treats Usage as its own destination, not buried under System Status (health/config).
- **Pro:** First-class enough for date-window dig-in without always-visible top nav.
- **Con:** One more menu item; slightly less “ops clustered” than a `/system` tab.

**Decision:** Option D — add **Usage** next to Manual Alert Submission and System Status in the hamburger menu; open a dedicated Usage page (route such as `/usage`).

_Considered and rejected: Option A (`/system` tab — Usage is not system health/config), Option B (always-visible top-level nav — heavier IA than needed), Option C (defer dashboard — rejected by Q1)._

---

## Q4: Which session surfaces show estimated cost in v1?

Tokens appear in several places. Showing cost everywhere at once may clutter; too few places and the feature is easy to miss. Parallel agents often use different models, so per-execution cost aids comparison within one investigation.

### Option B+: Session list + detail header + parallel / sub-agent execution surfaces (where tokens already show)
- **Pro:** Glanceable comparison across sessions (list) and within a session (header).
- **Pro:** Per-execution Est. $ next to existing `TokenUsageDisplay` on parallel agent tabs/cards and sub-agent cards — natural place to compare models in one investigation.
- **Pro:** Reuses `ExecutionOverview` token aggregates already shown in timeline/trace; little new UX invention.
- **Con:** Slightly broader than list+header alone; list density still needs a compact format.

**Decision:** Option B+ — estimated cost (when enabled) on:
1. Session detail header
2. Alert History Tokens column
3. **Parallel agent** execution surfaces that already show tokens
4. **Sub-agent** execution surfaces that already show tokens (same pattern; include in v1 since the data/UI path matches parallel agents)

Full per-LLM-interaction cost rows in Trace detail remain optional follow-up if still useful after execution-level cost ships.

_Originally decided as Option B (list + detail only). Amended after sketch completion to include parallel/sub-agent execution surfaces for multi-model comparison within a session._

_Considered and rejected: Option A (detail only), original Option B without execution-level cost (misses parallel model comparison), Option C as “every trace interaction row” (heavier than needed if execution cards already compare agents)._

---

## Q5: How should we communicate that the number is an estimate?

Trust depends on not sounding like a bill.

### Option A: Soft label (“Est. $X”) + tooltip with caveats
- **Pro:** Compact; tooltip can explain rates, unpriced interactions, cache limitations.
- **Pro:** Fits dense list/header layouts.
- **Con:** Easy to ignore the “Est.” prefix if users skim.

**Decision:** Option A — show “Est. $X” with a tooltip for caveats. Escalate to a stronger warning only when interactions are unpriced or the estimate is known-incomplete (see also Q9).

_Considered and rejected: Option B (always-on chip — too noisy in the list), Option C (no labeling — overclaims accuracy)._

---

## Q6: How should operators configure model rates?

Without a price book, estimates are impossible (or silently wrong). Hand-maintaining a full default table in TARSy git is easy to let rot when providers change prices. Config today is YAML; Config Viewer is read-only.

### Option B2: Remote catalog defaults (LiteLLM, cached) + YAML overrides + Config Viewer
- **Pro:** OOTB list prices without TARSy maintainers tracking every provider change.
- **Pro:** LiteLLM’s public JSON is the de facto catalog (also used by OpenClaw); no auth required.
- **Pro:** YAML overrides win for enterprise discounts, private/self-hosted, and corrections.
- **Pro:** Config Viewer shows effective rates and source (catalog vs override).
- **Pro:** Bundled snapshot is **fallback only** (offline/airgap), not the primary maintenance path.
- **Con:** Remote fetch can fail — need cache + snapshot fallback and clear “stale/unpriced” UX.
- **Con:** List prices ≠ negotiated cloud rates — overrides still matter.
- **Con:** Model-id normalization required (provider prefixes, aliases).

**Decision:** Option B2 — defaults from a **cached remote catalog** (LiteLLM JSON primary); optional OpenRouter supplement later if useful; **YAML overrides win**; Config Viewer shows effective rates; **bundled snapshot as fallback only**. No Python pricing microservice in v1 (Go can fetch/cache like OpenClaw). No hand-maintained full price table as the primary source.

_Considered and rejected: Option A (YAML-only — no OOTB defaults), Option B without remote catalog (implies hand-maintained defaults in git — easy to miss price changes), Option C (dashboard-editable rates — write-path clash with YAML-as-source-of-truth), Langfuse-as-feed (their curated JSON is not a supported third-party API; same manual lag), Python LLM sub-service for prices (unnecessary hop for static JSON)._

---

## Q7: Should estimated cost update while a session is still running?

Sessions already accumulate tokens as interactions complete.

### Option A: Live — cost updates as tokens arrive
- **Pro:** Consistent with live token growth; useful for long investigations.
- **Con:** Number will jump; may feel noisy.

**Decision:** Option A — estimated cost updates on the same cadence as token aggregates while a session is running (detail and list as data refreshes). No special “wait until terminal” rule unless UX proves too noisy later.

_Considered and rejected: Option B (terminal only — loses mid-flight awareness), Option C (split detail/list behavior — extra mental model)._

---

## Q8: What must the v1 usage dashboard include?

The usage dashboard is **in scope** (Q1). It should support digging into **both token counts and estimated cost** (when cost estimation is enabled — see Q10), especially via date windows (see Q11). This question bounds *what content* ships so the page stays useful without becoming FinOps.

### Option C: Totals + model breakdown + alert type and/or chain breakdown + top sessions + familiar filters
- **Pro:** Matches how TARSy work is organized (alert types, chains).
- **Pro:** Top sessions deep-link into existing session UX.
- **Pro:** Still bounded — no user chargeback, no anomaly ML, no budget enforcement.
- **Con:** More charts/tables than a totals-only or model-only page.

**Decision:** Option C — v1 Usage page includes: totals (tokens + est. cost when enabled), breakdown by model, breakdown by alert type and/or chain, top sessions, and familiar session-style filters, driven by date windows (Q11).

_Considered and rejected: Option A (totals only — too thin), Option B (no alert-type/chain view), Option D (full analytics — too large for this sketch)._

---

## Q9: How should incomplete pricing or accuracy gaps surface in UX?

Even with a price book, some interactions may be unpriced, and cache/thinking gaps may skew estimates.

### Option B: Partial cost + explicit warning when anything is unpriced or known-incomplete
- **Pro:** Preserves a useful number while preserving trust.
- **Pro:** Matches OpenClaw / token-ledger “unpriced” patterns.
- **Con:** Needs clear, short copy.

**Decision:** Option B — always show a partial estimate when possible, with an explicit caveat when anything is unpriced or known-incomplete (pairs with Q5 soft “Est.” labeling; escalate warning when incomplete).

_Considered and rejected: Option A (silent best-effort — quiet undercounting), Option C (hide until fully priced — feature often disappears)._

---

## Q10: Cost estimation is toggleable (on by default) — where does that switch live?

Product intent is clear: **estimated $ is configurable, enabled by default; when disabled, UI shows token usage only (no cost).** This question is only *where operators flip that switch* (and related: whether the usage dashboard still exists for tokens-only).

### Option B: YAML flag + visible in Config Viewer (read-only)
- **Pro:** Matches TARSy’s config model; GitOps-friendly; no dashboard write path.
- **Pro:** Operators can confirm the effective setting in the UI (ADR-0019).
- **Con:** Still YAML to change (reload/restart depends on how config is loaded today).

**Decision:** Option B — cluster-wide YAML flag (default **on**), shown read-only in Config Viewer. When off: hide estimated cost everywhere (session list, session detail, usage dashboard cost columns/totals) but **keep** the usage dashboard for token exploration. Per-user toggle is not v1.

_Considered and rejected: Option A (YAML only — no UI visibility of effective setting), Option C (per-user preference — weak for a shared ops tool)._

---

## Q11: Which date-window controls must the usage dashboard offer?

Date filtering is a primary reason for the usage dashboard (last 30d, MTD, etc.). The session list already has date filters/presets (`TimeRangeModal`: 10m…30d + custom) — the usage page should feel familiar but oriented around aggregate windows (MTD / last month matter more than “last 10 minutes”).

### Option B: Presets + custom start/end date range
- **Pro:** Covers quick windows and ad-hoc ranges.
- **Pro:** Aligns with existing session list date UX patterns.
- **Con:** Slightly more UI than presets-only.

**Decision:** Option B — named presets **plus** custom start/end. Preset set should include at least **Last 7 days**, **Last 30 days**, **MTD**, and **Last calendar month**. Default preset: **Last 30 days**. Short session-list presets (10m / 1h) are not required on the Usage page.

_Considered and rejected: Option A (presets only — awkward for custom ranges), Option C (custom only — worse for the common last-30d / MTD job)._
