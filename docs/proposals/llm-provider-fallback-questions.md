# LLM Provider Fallback — Design Questions

**Status:** Resolved — all decisions made  
**Related:** [Design document](llm-provider-fallback-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Fallback scope — stick for rest of execution or per-call?

When a fallback is triggered, should the agent continue using the fallback provider for all remaining iterations in that execution, or should each LLM call independently evaluate which provider to use?

### Option A: Stick with fallback for rest of execution ✅

Once a fallback is triggered, the execution switches to the fallback provider and stays there. Subsequent new executions (different stages, sub-agents) still start with the primary provider per config.

- **Pro:** Simple to implement — just swap `execCtx.Config.LLMProvider` and `LLMBackend` once
- **Pro:** Predictable behavior — the agent won't oscillate between providers mid-execution
- **Pro:** Avoids wasting iterations retrying a provider that just failed
- **Pro:** Matches the assumption in the spec: "provider failures are assumed to be provider-wide"
- **Con:** If the primary recovers quickly, this execution won't benefit

**Decision:** Option A — provider outages last longer than a single execution. Each new execution (stage, sub-agent) naturally resets to the primary via `ResolveAgentConfig`, giving recovery a chance without extra complexity.

**Implementation note — Python Content Cache:** The Google Native provider caches raw Gemini `Content` objects per `execution_id` to preserve `thought_signatures` across multi-turn conversations. When a fallback changes the model mid-execution, these cached objects become invalid (the new model won't understand the old model's thought signatures). The Go side must signal the provider switch (e.g., `clear_cache` flag on `GenerateRequest`) so Python clears `_model_contents[execution_id]`. The fallback path in `_convert_messages` already handles cache misses gracefully — it reconstructs Content from proto fields. The LangChain provider's model cache is stateless and unaffected.

_Considered and rejected: Option B — per-call health tracking (too complex, oscillation risk, contradicts provider-wide failure assumption). Option C — reset per-stage (collapses into Option A since each new execution already starts fresh via config resolution)._

---

## Q2: Where should adaptive timeouts be implemented?

The current 5-minute flat timeout is enforced in two places: Go (`context.WithTimeout` in the controller) and Python (`asyncio.timeout(300)` in providers). Where should the new adaptive timeout logic (initial-response, stall detection) live?

### Option A: Go controller ✅

Implement all adaptive timeout logic in Go's `collectStreamWithCallback`. The Go side already processes each chunk — it can track "time since last chunk" and "time to first chunk" and cancel the context accordingly. Python's existing 300s timeout stays as a static safety net (no changes needed).

- **Pro:** Single implementation, no proto changes needed
- **Pro:** Go already has the stream processing loop with per-chunk callbacks
- **Pro:** Python stays simple and stateless
- **Pro:** Timeout values are configurable through the existing Go config hierarchy
- **Con:** The gRPC stream from Python adds a small latency buffer — Go sees chunks slightly after Python produces them

### Option B: Python LLM service

Move adaptive timeouts to Python. Pass timeout config via proto. Python tracks first-chunk and inter-chunk timing.

- **Pro:** Closer to the source — detects stalls before gRPC buffering adds delay
- **Con:** Requires proto changes (`initial_timeout_seconds`, `stall_timeout_seconds` in `GenerateRequest`)
- **Con:** Both providers (`google_native`, `langchain`) need identical timeout logic
- **Con:** Error reporting back to Go would need new error codes/metadata

**Decision:** Option A — Go's `collectStreamWithCallback` already processes every chunk in real-time, making first-chunk and stall tracking straightforward. No proto changes needed. Python's existing 300s timeout stays as defense-in-depth.

_Considered and rejected: Option B — requires proto changes, duplicated logic in both Python providers, and added complexity for marginal latency benefit._

---

## Q3: Should each fallback entry explicitly specify its backend?

In the fallback provider list, should each entry carry its own `backend` field, or should the system infer the backend from the provider's definition (or from the agent's configured backend)?

### Option A: Explicit backend per entry ✅

```yaml
fallback_providers:
  - provider: "gemini-2.5-pro"
    backend: "google-native"
  - provider: "anthropic-vertex"
    backend: "langchain"
```

- **Pro:** Clear and unambiguous — what you see is what you get
- **Pro:** Supports the same provider with different backends (unlikely but possible)
- **Pro:** Easy to validate at startup — no need to cross-reference provider definitions
- **Pro:** Future-proof — no implicit mapping to maintain as new backends are added

### Option B: Infer backend from agent's configured backend (filter-only)

```yaml
fallback_providers:
  - "gemini-2.5-pro"
  - "anthropic-vertex"
```

- **Pro:** Minimal configuration
- **Con:** Requires maintaining a provider-type → backend compatibility mapping
- **Con:** Implicit behavior breaks when new backends are introduced

**Decision:** Option A — explicit backend per entry. Avoids hidden compatibility mappings and stays straightforward as the backend landscape evolves. The config verbosity is minimal (one extra field per entry).

_Considered and rejected: Option B — infer from agent's backend (implicit mapping adds complexity and fragility as backends evolve). Option C — optional backend with default (partial explicitness is confusing)._

---

## Q4: When should fallback provider credentials be validated?

Should the system check that fallback providers have valid API keys/credentials at startup, or defer the check to fallback selection time?

### Option A: Validate at startup ✅

At config load time, check that every provider in every fallback list has its required environment variables set. Fail startup if any are missing.

- **Pro:** Fast-fail — misconfigurations are caught immediately, not during an incident
- **Pro:** Operators know the fallback list is fully operational before any sessions run
- **Pro:** A fallback to a broken provider is worse than no fallback — false sense of security

### Option B: Validate at fallback selection time

- **Pro:** Graceful degradation, handles secret rotation
- **Con:** You discover the fallback is broken during an incident — the worst time to find out

**Decision:** Option A — fail startup if any fallback provider has missing credentials. A fallback list is a safety net; every entry must work. Configuration problems should be caught at deploy time, not during an outage.

_Considered and rejected: Option B — runtime validation (discovers broken fallback during incidents). Option C — warn at startup, skip at runtime (warnings get ignored; a half-working fallback list gives false confidence)._

---

## Q5: How should fallback metadata be stored on execution records?

When a fallback occurs, we need to record which provider was originally configured and which one is now active. The existing `agent_executions` table has `llm_provider` and `llm_backend` fields.

### Option A: New columns (simplified) ✅

Add `original_llm_provider` and `original_llm_backend` columns to `agent_executions`. Both nullable, only set when fallback occurs. The existing `llm_provider`/`llm_backend` fields get updated to the fallback provider.

- **Pro:** Strongly typed, directly queryable (`WHERE original_llm_provider IS NOT NULL`)
- **Pro:** Execution record tells you "supposed to use X, ended up on Y"
- **Pro:** Timeline events provide the detailed audit trail (tried X, then Y, then Z)

### Option B: JSON metadata field

- **Pro:** Flexible, single column
- **Con:** Not directly queryable, less discoverable

### Option C: No new columns — just timeline events

- **Pro:** Zero schema changes
- **Con:** "Did this execution fall back?" requires a timeline event join

**Decision:** Option A (simplified) — two new nullable columns: `original_llm_provider` and `original_llm_backend`. NULL means no fallback occurred. The existing `llm_provider`/`llm_backend` are updated to reflect the active provider. Timeline events provide the full attempt chain for detailed audit.

_Considered and rejected: Option B — JSONB metadata (not queryable without JSON operators). Option C — timeline events only (common dashboard query requires a join)._

---

## Q6: Should fallback apply to forced conclusion and single-shot controllers?

The iterating controller has a natural retry loop where fallback can be injected. But two other paths also make LLM calls: the forced conclusion (when max iterations are hit) and the single-shot controller (used for synthesis, executive summary, scoring).

### Option A: All controllers get fallback ✅

Apply fallback logic to every LLM call site: iteration loop, forced conclusion, and single-shot. Extract shared fallback logic into a helper function.

- **Pro:** Consistent behavior — every LLM call benefits from fallback
- **Pro:** If the primary is down during investigation and we switch, synthesis/scoring would still resolve to the same broken primary without their own fallback
- **Pro:** The whole session survives the outage, not just the investigation phase

### Option B: Iterating controller only

- **Pro:** Simpler — one integration point
- **Con:** Scoring/synthesis fail on the same broken provider the investigation escaped from

**Decision:** Option A — all controllers get fallback. Extract shared fallback logic into a helper (e.g., `callLLMWithFallback`) that wraps `callLLMWithStreaming`. Single-shot controllers get a mini-retry with fallback around their single LLM call. The whole session should survive a provider outage, not just the iteration loop.

_Considered and rejected: Option B — iterating only (scoring/synthesis would still fail on the broken primary). Option C — iterating + forced conclusion only (same problem for single-shot; separate config resolution still points to the broken primary)._

---

## Q7: How many consecutive failures should trigger fallback?

Currently, a single failed LLM call (after Python's 3 retries for retryable errors, or immediately for non-retryable errors) causes the Go controller to add an error message and retry via the next iteration. The fallback trigger should depend on the error type, since Python's retry behavior differs by error category.

### Decision: Error-code-aware fallback triggers ✅

Python sends different error codes to Go, each with different retry history:

| Error Code | Python Retried? | Fallback Trigger |
|---|---|---|
| `max_retries` | Yes (3x with backoff) | **Immediate** — Python exhausted retries, provider is down |
| `credentials` | No | **Immediate** — missing API key, guaranteed failure, retrying is pointless |
| `provider_error` | No | **After 1 Go retry** — non-retryable exception from provider (auth, rate limit, model error); give the primary one more chance since Python didn't retry |
| `invalid_request` | No | **After 1 Go retry** — message/tool conversion failure, could be model-specific; different provider may handle it |
| `partial_stream_error` | No | **After 2 consecutive partial errors** — model started streaming but crashed mid-stream; usually recovers on retry, but repeated failures suggest a model-specific issue with this conversation |

**Rationale:**
- For `max_retries`: Python already made 3 API calls with exponential backoff. The provider is clearly down.
- For `credentials`: env var is missing. No amount of retrying will produce an API key. (Note: Q4 decided startup validation catches this for fallback providers, so this only applies to the primary.)
- For `provider_error` / `invalid_request`: Python didn't retry at all (these are non-retryable exceptions). One Go-level retry gives the primary a second chance before giving up.
- For `partial_stream_error`: Go treats these as recoverable (doesn't count as a hard failure, appends partial text to conversation). The model often picks up where it left off. But 2 consecutive partial errors on the same execution suggest the model consistently crashes on this conversation content.

_This replaces the original Options A/B/C which assumed a single threshold for all error types._

---

## Q8: What should the default adaptive timeout values be?

The adaptive timeout system needs defaults for: initial response timeout (time to first chunk), stall timeout (max gap between chunks), and maximum call timeout (overall ceiling).

### Decision: Conservative defaults ✅

- **Initial response: 120s (2m)** — time to first chunk
- **Stall: 60s** — max gap between chunks after streaming starts
- **Max call: 5m** — overall ceiling (unchanged)

**Rationale:** TARSy agents send heavy context (full conversation history, tool results, runbooks, system prompts). Thinking models like Gemini 2.5 Pro can take 20-40s on typical prompts, potentially longer on complex ones. The 120s initial timeout is generous enough to never false-positive while still saving **3 minutes** vs. the flat 5m timeout on dead providers — that's the bulk of the win. The 60s stall timeout catches mid-stream failures reliably since once streaming starts, a 60s gap strongly indicates a problem. These are configurable defaults — operators can tune them.

_Considered and rejected: Aggressive defaults of 15s (false-positive risk with thinking models). Balanced defaults of 45s (still borderline for heavy prompts). Model-aware defaults (complexity without clear payoff)._
