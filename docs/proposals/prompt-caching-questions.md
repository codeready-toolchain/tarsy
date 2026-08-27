# Prompt Caching — Design Questions

**Status:** Decided — design is [prompt-caching-design.md](prompt-caching-design.md)
**Related:** [Design document](prompt-caching-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: What is in v1?

Phase A (persist/extract cache usage) is cheap and unblocks every later decision with production evidence. Phase B (Claude `cache_control` plus GPT-5.6+ explicit OpenAI caching, Q11) is where looping agents actually get cheaper. Phase C (price cache tokens) keeps Est. $ honest after B. Phase D (Q10 scoped UI) is the same v1 so a miss is visible without SQL. Prompt reorder, Gemini explicit caches, and Usage-page *charts* can wait.

Shipping B without A means we cannot prove hits. Shipping A without B leaves Claude iterating agents paying full input on every turn. Shipping A+B without C undercounts Est. $ once `input_tokens` is normalized to uncached.

### Option C: Observe + looping-call caching + cost formula + scoped UI (Phases A–D)

- **Pro:** Behavior, telemetry, Est. $, and per-call hit/miss land together; ADR-0020’s documented gap actually closes.
- **Pro:** Operators will not see Est. $ *rise* or stay inflated after we start discounting billed input.
- **Con:** Largest v1; cost catalog parsing and estimate tests grow.
- **Con:** YAML overrides/promotions still lack cache rates (same v1 limit as ADR-0020 flat overrides).

**Decision:** Option C — persist cache usage, enable Claude `cache_control` and GPT-5.6+ explicit OpenAI caching on looping calls, price cache tokens, and ship Q10 scoped UI in the same v1. Prompt layout, Gemini explicit caches, Usage-page charts, and session-list cache SUMs stay follow-ups.

_Considered and rejected: Option A (observe only — Claude iterating agents keep paying full input), Option B (observe + Claude caching without cost formula — Est. $ stays wrong, and with normalized input it would undercount)._

---

## Q2: How is caching turned on and off?

Vertex can disable prompt caching per GCP project (requests with `cache_control` then 400). Some operators may want a kill switch. Others will not want YAML for something that should just work on looping Claude calls.

### Option B: `system.prompt_caching.enabled` (default true)

- **Pro:** GitOps kill switch, same pattern as `system.cost_estimation.enabled`.
- **Pro:** Default-on preserves the “just works” path; operators need not set anything.
- **Con:** Another system knob and Config Viewer field.
- **Con:** Easy to leave false in a copied config and never get savings.

**Decision:** Option B — cluster toggle, default on (`*bool`, omit-means-true, same as cost estimation). Python has no `tarsy.yaml`: Go ANDs the toggle onto `GenerateRequest.prompt_cache` in `callLLMWithStreaming`. Pair with a Python 400-retry that strips Claude `cache_control` / OpenAI `prompt_cache_options` so a Vertex project with caching disabled (or an older OpenAI model that rejects 5.6 fields) degrades instead of failing. The toggle does **not** disable Gemini implicit caching. No per-agent YAML.

_Considered and rejected: Option A (no YAML — no kill switch; Vertex 400s until a code change), Option C (per-provider/per-agent YAML — duplicates per-call eligibility and copies into every chain)._

---

## Q3: Who decides that a Generate call is eligible?

Python could guess from `len(messages)` / tools. Go controllers already know whether they will call `Generate` again with the same prefix.

### Option B: Go `GenerateInput.PromptCache` set by controllers

- **Pro:** Eligibility is an explicit property of the control flow (investigation-style iterating loop vs action / scoring / single-shot / summarize).
- **Pro:** Matches how `ClearCache` and `ExecutionID + ":summarization"` already pass intent over proto.
- **Con:** `IteratingController` is shared: loop on for `AgentTypeDefault`, off for `AgentTypeAction` and forced conclusion. Easy to mark the whole controller on by accident.
- **Con:** Proto + `GenerateInput` grow by one bool.

**Decision:** Option B — controllers set `PromptCache` from eligibility only (`AgentTypeDefault` iterating **loop**). Action, forced conclusion, scoring, `SingleShotController`, and summarization stay off. Action is usually one “no action” Generate (safety prompt; Sandbox remediation is Sonnet + small tools); Claude 1h 2× write is then never read. Scoring is two turns with the same last-write problem. Python applies Claude `cache_control` / OpenAI 5.6+ explicit options only when the proto field is true (Go already AND-ed the cluster toggle).

_Considered and rejected: Option A (Python heuristic — scoring/summarization edges; a one-shot with tools would pay write tax), Option C (all Generate calls — synthesis/summarization pay 1.25×–2× for a cache that is never read)._

---

## Q4: Where do we place Claude cache breakpoints?

Anthropic caches everything up to a breakpoint. Up to four breakpoints per request. Tools + system are stable for the whole execution. Conversation grows each turn.

### Option C: Last tool + system + last message (growing prefix)

- **Pro:** Each turn writes the new suffix and reads the entire prior conversation. That is the agent-loop pattern Anthropic documents.
- **Pro:** Still ≤4 breakpoints.
- **Con:** More moving parts; last-message breakpoint must be re-placed every turn (standard).
- **Con:** Slightly more ways to mismatch (e.g. thought-signature / tool_call_id encoding differences already have to be byte-identical anyway).

**Decision:** Option C — last tool, system prompt, and last message. If the last-message breakpoint is flaky on Vertex, drop it without a proto change.

_Considered and rejected: Option A (last tool only — skills and history stay full-price), Option B (tools + system — growing history, the bulk of late-session input, stays uncached)._

---

## Q5: What TTL do we send on Claude `cache_control`?

Default provider TTL is 5 minutes, refreshed on hit. TARSy defaults: LLM call 5m, tool 1m, iteration 6m. Orchestrators pause while sub-agents run — often longer than 5 minutes. 1h writes cost 2× input vs 1.25× for 5m. Vertex documents 1h as unsupported on some old Claude 3.x models; current Claude 5 models support it.

### Option B: 1 hour (hardcoded)

- **Pro:** Covers sub-agent waits and typical session timeouts; TTL refreshes on hits.
- **Pro:** No YAML.
- **Con:** Cold writes cost 2× instead of 1.25×. Fine when turn 2+ reads; painful if we accidentally mark a one-shot (Q3 should prevent that).
- **Con:** Old Vertex Claude 3.x may 400 — needs strip-TTL retry.

**Decision:** Option B — hardcoded 1h on eligible calls. Retry without `ttl` on 400 so old Vertex Claude 3.x degrades to 5m instead of failing.

_Considered and rejected: Option A (5m — orchestrator and slow MCP sequences miss the cache), Option C (YAML `5m`|`1h` — extra knob; easy to pick 5m and lose orchestrator hits)._

---

## Q6: Do we implement Gemini explicit context caches in v1?

Implicit caching is already on for Gemini 2.5+. Explicit `CachedContent` gives a guaranteed TTL and a named handle, but TARSy’s Python service is designed to be stateless and typically runs as a sidecar per replica.

### Option C: Implicit now; explicit only if Phase A shows poor hit rates on looping Gemini executions

- **Pro:** Evidence-driven; Gemini looping agents may already be getting implicit hits.
- **Con:** Leaves a possible follow-up instead of closing Gemini forever.

**Decision:** Option C — **v1 implementation is the same as Option A:** implicit only. Extract `cached_content_token_count`, persist it, and include it in estimated cost. Do **not** create Gemini `CachedContent` objects. Explicit caches are a possible later follow-up if looping sessions show poor implicit hit rates, not part of this design’s implementation.

_Considered and rejected: Option A as a permanent “never explicit” product stance (same code in v1), Option B (explicit `CachedContent` in v1 — named objects and replica state; not required to price implicit hits)._

---

## Q7: How do we persist cache tokens, and what does `input_tokens` mean?

Claude reports uncached input separately from cache read/create. Gemini’s `prompt_token_count` **includes** cached tokens. If we store raw provider `input_tokens`, session SUMs and cost math mean different things per backend.

### Option B: Normalize at the Python boundary

`input_tokens` = uncached input; `cache_read_tokens` / `cache_creation_tokens` extra. Gemini: subtract cached from prompt before sending proto.

- **Pro:** One cost formula, one dashboard meaning: `input_tokens` is full-price input.
- **Pro:** Matches how we already treat thinking as a separate column rather than stuffing it into output.
- **Con:** TARSy `input_tokens` will no longer match Gemini’s raw `prompt_token_count` (document it).
- **Con:** Historical rows stay in the old meaning (same as thinking_tokens rollout).

**Decision:** Option B — Python normalizes so `input_tokens` is uncached (full-price) input. Persist `cache_read_tokens` and `cache_creation_tokens` when > 0. Go/cost do not branch on provider. Document that TARSy `input_tokens` is not Gemini’s raw `prompt_token_count`.

Implementation notes (not a new decision): LangChain `usage_metadata.input_tokens` is inclusive (sum of all input types). Anthropic **raw** `usage.input_tokens` is already uncached — prefer `response_metadata["usage"]` when present, and treat `input_token_details.cache_creation` as unreliable (use `cache_creation_input_tokens` or sum `ephemeral_5m` + `ephemeral_1h`). OpenAI `input_tokens` includes both cached reads and cache writes: `uncached = input - cache_read - cache_creation`.

_Considered and rejected: Option A (leave provider-reported input — cost math must branch; easy to double-count Gemini), Option C (no columns — cannot price per interaction)._

---

## Q8: Should v1 estimated cost use cache rates?

ADR-0020 left cache tokens out on purpose. After Q7, we *have* the counts. LiteLLM’s public JSON already has `cache_read_input_token_cost` / `cache_creation_input_token_cost` (and a 1h variant); TARSy’s parser and bundled snapshot do not yet. YAML overrides remain flat.

### Option B: Price cache tokens when catalog/snapshot has rates; otherwise apply 0.1× / 1.25× or 2× defaults

- **Pro:** Closes the known gap; honest with Phase B.
- **Pro:** Overrides/promotions stay flat; missing cache rates can use published multipliers as fallback so cache-read is never $0.
- **Con:** Estimate signature grows; tests and docs update.

**Decision:** Option B — price cache-read and cache-creation tokens in v1.

- Parse LiteLLM `cache_read_input_token_cost` / `cache_creation_input_token_cost` / `cache_creation_input_token_cost_above_1hr` into the catalog and bundled snapshot. Refresh the snapshot so models TARSy actually ships (Claude, Gemini 2.5/3.x including `gemini-3.7-flash`, GPT-5.x) have cache rates, not only input/output. Today’s snapshot has none.
- Resolve order stays promotion → `model_rates` → catalog → snapshot. Flat YAML promotions/overrides do not add cache fields in v1. When they win, **derive** from the resolved input rate: read = 0.1×; create = **2×** if the model name contains `claude` (the 1h TTL we send), else **1.25×**. Intro-priced input still discounts cache reads.
- If catalog/snapshot has explicit cache rates and no overlay won: use catalog read; for Claude 1h writes use the `above_1hr` field when present, otherwise derive 2× from input — **do not** bill 1h writes at the 5m catalog create rate.
- Select `above_Nk` / tiered catalog rates using prompt size (`uncached + cache_read + cache_creation`), not uncached `input_tokens` alone (otherwise Gemini 200k tiers miss on cache-heavy calls).
- Missing cache rates after that still use the same multipliers on `rates.input` so a row is not silently undercounted.

_Considered and rejected: Option A (keep the gap — with Q7 this undercounts cache-read spend), Option C (defer pricing — same undercount in the first release)._

---

## Q9: Do we change system-prompt layout in v1 for better prefix sharing?

Tier 0 RFC3339 time is the first system section. That makes the whole system prompt unique per session, so cross-session implicit/Claude cache of skills+instructions will not hit. Intra-session looping does not care.

### Option C: Leave layout; intra-session looping is the v1 win

- **Pro:** No golden churn; no risk of changing agent behavior via prompt order.
- **Pro:** Q4 last-message breakpoint already caches growing history inside a session.
- **Con:** No cross-session cache of required skills. Acceptable: alerts differ in the user message anyway.

**Decision:** Option C — do not change prompt layout in v1. Real savings are intra-session growing history (and tool schemas, already on a Q4 breakpoint). System-prompt reorder/split is not worth golden churn.

_Considered and rejected: Option A (move Tier 0 — timestamp still in the same cached system block), Option B (split system blocks — real cross-session system-text win, extra prompt-builder work for little token volume)._

---

## Q10: What operator surfaces ship in v1?

Cache tokens can live only in the DB, or also in Prometheus, session detail, Usage aggregates, and Config Viewer. Session-list and execution-overview SUMs already exist for input/output/total; adding cache there does not show *which* `Generate` missed.

### Option C (scoped): DB + Prometheus + Config Viewer + trace per-interaction + Usage totals / by-model

- **Pro:** Per-call hit/miss on the trace (the diagnostic that matters); fleet hit rate on Usage totals and the by-model table.
- **Pro:** Config Viewer shows `system.prompt_caching.enabled` next to cost estimation (Q2).
- **Con:** Trace DTOs, `TokenUsageDisplay`, Usage aggregate SQL, and Usage-page tests grow in the same v1 as proto + Python.
- **Con:** Session list, session header, and execution-overview chips stay input/output/total only (same as thinking tokens today).

**Decision:** Option C, scoped — persist + Prometheus `cache_read` / `cache_creation` directions + Config Viewer toggle + **trace LLM interaction** list/detail fields + **Usage totals and by-model** columns. Do **not** add cache SUMs to session list, session header, `ExecutionOverview`, Usage by-alert-type / by-chain / top-sessions, or Usage charts in v1. `TokenUsageDisplay` renders cache only when the DTO has the fields (session surfaces omit them). Thinking tokens never landed on trace DTOs; cache does, so a miss is visible per call.

_Considered and rejected: Option A (DB + Prometheus only — cannot see a miss without SQL), full Option C / Option B with session-list rollups (DTO churn on Alert History and parallel tabs; a session total does not locate the miss)._

---

## Q11: OpenAI prompt caching in v1?

Built-in `openai-default` is GPT-5.2 (automatic prefix cache; extract-only is enough). GPT-5.6+ places an implicit breakpoint on the latest user/tool message, does **not** fall back to a partial prefix, and bills cache **writes** at 1.25×. On a TARSy iterating turn the last message is always new, so implicit 5.6 is write-the-whole-prompt and read ~0. OpenAI TTL is `30m` only. Q11 still walks back past tool-result messages so we do not rely on a `function_call_output` breakpoint writing; Phase B must confirm.

### Option 2b: Version-gated explicit, growing prefix (GPT-5.6+)

- **Pro:** Looping GPT-5.6 investigators get real reads; GPT-5.5 and older stay extract-only (no 5.6 fields).
- **Pro:** Same `PromptCache` flag as Claude (Q3). `prompt_cache_key` = existing `execution_id`.
- **Pro:** Growing-prefix breakpoints match Claude Q4, skipping tool-result messages so OpenAI actually writes.
- **Con:** Separate API from Claude (`prompt_cache_options` + `prompt_cache_breakpoint`); 30m TTL can miss long orchestrator waits.
- **Con:** Model-family gate (`gpt-5.` minor ≥ 6) plus 400-retry that strips OpenAI cache kwargs.

**Decision:** Option 2b — GPT-5.6+ looping calls get `prompt_cache_options: {mode: explicit, ttl: 30m}`, `prompt_cache_key: execution_id`, and breakpoints on last tool, system-as-content-block (not Responses top-level `instructions`), and the last **non-tool-result** message. GPT-5.5 and older (`gpt-5.2`, `gpt-5`, `gpt-5-mini`, any 5.3–5.5): extract `cached_tokens` / `cache_write_tokens` only. Do not bake cache policy into the cached `ChatOpenAI` instance; pass key/options per `astream` (or per-request `bind`). 400 → retry stripped.

Phase B must verify the walk-back: OpenAI docs also show breakpoints on `function_call_output`. If a last-message (tool-result) breakpoint actually writes, drop the walk-back and match Claude — no proto change.

_Considered and rejected: Option A / extract-only (5.6 looping pays 1.25× writes with no prefix reads), Option C (ignore OpenAI), Option 2a (tools+system only — late-turn history stays full-price), Option 3 (key only — implicit breakpoint stays on the volatile last message), Option 4 (explicit on all OpenAI — unnecessary risk on 5.2)._
