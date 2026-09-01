# LLM Retry Resilience — Design Questions

**Status:** Decided
**Related:** [Design document](llm-retry-resilience-design.md)

All questions resolved. The design document is **Final**.

**Q1 is decided (Option A).** That locked include / defer / never for the research catalog. Several later questions were *whether* to do those items; they are recorded as settled by Q1 and were not walked through.

---

## Q1: Which improvements are in this design?

Research compared TARSy to OpenAI/Anthropic SDKs, google-genai, LiteLLM, LangGraph, OpenHands, Aider, Cline, Goose, Gemini CLI, Codex CLI, Claude Code, and the OpenAI Agents SDK. TARSy’s two-layer shape (identical retry, then sticky fallback) is already sound. The list below is everything that came out as a real gap or a tempting copy. This question picks **include / defer / never** so we do not silently drop or inflate scope.

Catalog (marking is now the Q1 decision):

| # | Improvement | Q1 decision |
|---|---|---|
| 1 | Full jitter on Python backoff (MCP already jitters) | **Include** |
| 2 | Skip sleep after the last failed Python attempt | **Include** |
| 3 | Honor `Retry-After` / `retry-after-ms` / Google `RetryInfo.retryDelay`, with a cap | **Include** |
| 4 | Fail-fast on spend-cap / over-cap / `x-should-retry: false` 429s | **Include** |
| 5 | Retry connection / protocol / reset errors (not only HTTP 429/404/5xx) | **Include** |
| 6 | Retry HTTP 408 | **Include** |
| 7 | Retry HTTP 409 | Defer (rare for Generate) |
| 8 | Pin nested SDK retries off (`ChatXAI`, `ChatGoogleGenerativeAI`, google-native `HttpRetryOptions`) | **Include** |
| 9 | Replica-local provider cooldown (ADR-0027 later #2) | Defer — skip-primary on recent `max_retries` misfires on Vertex 404 flicker (200s interleaved); new work would start on a weaker model |
| 10 | Do not stick fallback on exhausted transient 404 (ADR-0027 later #3) | Defer (amends ADR-0003 Q1; own design) |
| 11 | Probe / unstick primary after N healthy fallback turns (ADR-0027 later #1) | Defer (oscillation risk) |
| 12 | Raise Python `MAX_RETRIES` from 3 to 5 or 10 | **Never** as a substitute for 1–6; optional later once waits are smart |
| 13 | One extra retry on stall-with-zero-text (Claude Code / Gemini CLI) | Defer |
| 14 | Codex-style SSE reconnect (`stream_max_retries`) | **Never** — stall timeout + continue-from-partial is the TARSy path |
| 15 | Claude Code “keep completed blocks” mid-stream | Defer — iterating tools run only after a successful Generate |
| 16 | Pass remaining session/iteration deadline into Python | Defer |
| 17 | Hedged / parallel duplicate Generates | **Never** — doubles token cost |
| 18 | Indefinite retry watchdog (Claude Code unattended) | **Never** — session timeout already bounds wait |
| 19 | Replay Generate after a tool/text block has started | **Never** — duplicate MCP side effects |
| 20 | OpenHands per-call reset to primary | **Never** — fights ADR-0003 sticky-for-execution |
| 21 | YAML-heavy circuit breaker | **Never** — same skip-primary misfire as catalog 9; retry waits stay hardcoded |
| 22 | Plumb Python `ErrorInfo.retryable=true` (today always false) | **Never** — Go keys off error *codes*, not the flag |

### Option A: Hygiene only (catalog 1–6, 8)

- **Pro:** Closes the wait / predicate / nested-SDK gaps. No YAML, no probe oscillation, no mid-stream change, no skip of a primary that is still serving.
- **Pro:** Vertex publisher-model 404s are per-request flicker (same model 200s in the same second). The first identical retry already absorbs short blips; a replica skip would treat two unlucky exhaustions as “primary is dead” and start new executions on fallback (often a weaker model) while Claude still answers.
- **Con:** Sibling sub-agents on the same replica still rediscover a primary that is *actually* down (each pays identical retries, then fallback). Sticky-for-execution still protects the execution that already failed. A later skip needs a signal that is not “two 404 `max_retries` in 60s.”

**Decision:** Option A — Python retry hygiene (jitter, last-attempt no-sleep, Retry-After/RetryInfo with cap, spend-cap fail-fast, connection retries, 408, SDK pin). Sticky-for-execution, probe, don’t-stick-404, and replica cooldown stay out.

_Considered and rejected: Option B (hygiene + replica cooldown — skip-primary on `max_retries` collides with 404 flicker; does not unstick the current execution), Option C (also unstick 404 on the current execution — amends ADR-0003 Q1; own design), Option D (kitchen sink — probe, mid-stream, and deadline interact; fights surgical v1)._

---

## Q2: Retry-After cap and jitter shape

Q1 already included full jitter, skip-sleep-on-last-attempt, honor `Retry-After` / `RetryInfo` **with a cap**, and fail-fast when the hint is over the cap. This question is only the numbers.

Vertex 429s often say `retryDelay: "53s"`. Go’s first-byte timeout is 120s. OpenAI/Anthropic SDKs treat Retry-After ≤ 60s as reasonable. A cap below 53s turns those Vertex hints into immediate fallback instead of waiting.

### Option A: Cap 60s; full jitter `uniform(0, min(8s, 2**attempt))` when there is no hint

- **Pro:** 53s Vertex RetryInfo is honored once, then retry or fallback. Matches first-party “reasonable Retry-After” (60s) and SDK max delay (8s) for the no-hint path.
- **Pro:** Skip last sleep (Q1) still applies: at most one long server-informed wait, not a wait after exhaustion.
- **Con:** One Generate can block ~60s. Must be cancellable (Q11) so session/iteration cancel is not stuck in sleep.
- **Con:** Three no-hint attempts still only jitter inside 1s then 2s windows (`2**0`, `2**1`); the 8s cap barely matters with `MAX_RETRIES=3`.

**Decision:** Option A — Retry-After / RetryInfo cap 60s; no-hint delay is full jitter up to `min(8s, 2**attempt)`; no sleep after the last failed attempt (Q1). Hint above 60s fail-fasts (Q3 spend-cap / over-cap path).

_Considered and rejected: Option B (8s cap — fail-fasts the common Vertex 53s hint), Option C (30s cap — still miss 53s; harder to explain)._

---

## Q3: Which errors should the identical-retry predicate accept?

Q1 already included connection retries, HTTP 408, spend-cap / over-cap / `x-should-retry: false` fail-fast, and deferred 409. Membership of the predicate is not an open question.

**Decision:** Implied by Q1.A — retry connection/protocol/reset (not TLS cert failures), 408, throttle 429, 404, 5xx (including 529), timeout, empty response. Do not retry 400/401/403, spend-cap 429, Retry-After above the Q2 cap, or 409.

_Considered and rejected: adding 409 now (Q1 deferred catalog 7), retrying all 429s including spend-cap (Q1 included catalog 4 fail-fast), connection-errors-only (Q1 also included 408 and spend-cap handling)._

---

## Q4: How do we pin nested SDK retries off?

Q1 included pinning nested SDK retries (catalog 8). This question is only the pin style.

LangChain OpenAI/Anthropic/Vertex Claude already pass `max_retries=0`. `ChatXAI` and `ChatGoogleGenerativeAI` do not. google-native `Client(api_key=...)` passes no `HttpRetryOptions`; current google-genai source treats `None` as one attempt, but docs/defaults have moved around (`attempts=0` is documented as “infinity” in one LangChain issue). SDK retries are better than TARSy’s loop *today* on OpenAI/Anthropic, but they disagree across backends, do not retry Vertex 404, do not implement the zero-chunk / spend-cap contract, and stacked with TARSy’s loop delay Go fallback.

### Option B: LangChain `max_retries=0` everywhere it is missing, and explicit `HttpRetryOptions(attempts=1)` on google-native Client

- **Pro:** ADR-0027 “SDK constructors keep max_retries=0” becomes actually true.
- **Pro:** `attempts=1` is “original only” in current google-genai (`attempts=0` is coerced to 1).
- **Con:** One more constructor kwarg to keep in tests.

**Decision:** Option B — pin `max_retries=0` on `ChatXAI` and `ChatGoogleGenerativeAI`; pin google-native `HttpRetryOptions(attempts=1)`. TARSy’s loop stays the retry owner. Tests already assert OpenAI/Anthropic/Vertex Claude `max_retries==0`; extend those asserts to xAI and ChatGoogleGenerativeAI; add a google-native client-construction test for `attempts=1`.

_Considered and rejected: Option A (LangChain only — google-genai default change would silently stack retries), Option C (delete TARSy’s loop and use SDK retries — contradicts Q1; SDKs disagree, do not retry Vertex 404, and would not honor spend-cap fail-fast / zero-chunk)._

---

## Q5: Replica-local provider cooldown?

Q1 deferred replica cooldown (catalog 9). YAML-heavy circuit breakers are never (catalog 21). There is no threshold question because the skip is out of this design.

Vertex publisher-model 404s (`NOT_FOUND` on `publishers/anthropic/models/…` in `locations/global`) are interleaved with 200s on the same model in the same second. The first identical retry recovers a subset; a further retry on the same Generate does not. Two exhaustions in 60s are therefore a poor “primary is dead” signal: new executions would skip Claude for minutes while other calls still succeed, and would start on a weaker fallback.

**Decision:** Implied by Q1.A — no replica cooldown. Sibling rediscovery of a *true* outage stays ADR-0027 later #2 and needs a different signal than two 404 `max_retries`.

_Considered and rejected: 2× `max_retries` in 60s → skip that name for 2 minutes on new executions (ADR-0027 sketch — misfires on flicker), cooldown on first `max_retries` (hair-trigger), keying by model string or `type` (would couple unrelated YAML names)._

---

## Q6: Should exhausted transients still stick the current execution?

Q1 deferred don’t-stick-on-404 (catalog 10) and kept ADR-0003 sticky-for-execution.

**Decision:** Implied by Q1.A — keep sticky-for-execution for all `max_retries` (including 404/429). Revisit if traces show long executions stuck on a weaker fallback after a short publisher-model blip.

_Considered and rejected: unstick 404 only (Q1 Option C), unstick all 404/429/5xx `max_retries` (not in Q1.A)._

---

## Q7: Probe / unstick the primary after healthy fallback turns?

Q1 deferred probe (catalog 11) and never’d per-call reset to primary (catalog 20).

**Decision:** Implied by Q1.A — no probe in this design.

_Considered and rejected: probe after N healthy fallback turns, time-based probe._

---

## Q8: Mid-stream and stall — anything beyond ADR-0027?

Q1 deferred stall-with-zero-text extra retry (catalog 13) and keep-completed-blocks (catalog 15), and never’d Codex SSE reconnect (catalog 14) and replay after a tool/text block (catalog 19).

**Decision:** Implied by Q1.A — no change. Zero-chunk identical retry, Go continue-from-partial, and adaptive timeouts stay as ADR-0027.

_Considered and rejected: thinking-only chunks not blocking identical retry, extra Python retry on stall-with-zero-text, SSE reconnect._

---

## Q9: Keep Python `MAX_RETRIES = 3`?

Q1 never’d raising attempts as the first move (catalog 12). Fallback stays the second line of defense.

Publisher-model 404s that recover on the identical-retry path recover on the **first** retry. A second retry on the same Generate does not convert a failing sequence. Extra attempts would delay fallback to the next list entry (often still Claude) without evidence they stay on the primary.

**Decision:** Implied by Q1.A — keep `MAX_RETRIES = 3`. Re-evaluate only if production metrics show connection-error (or 404) `max_retries` that later succeed on the same Generate within seconds.

_Considered and rejected: raise to 5 now that waits are smart, raise to 10 like Claude Code, extra fast 404-only attempts (same-Generate retries after the first look correlated; siblings succeeding is a different experiment)._

---

## Q10: Hardcoded constants or new YAML?

Q1.A’s bundle assumed no new YAML (catalog 21 never’d a YAML circuit breaker; ADR-0027 v1 was surgical). Fallback *list* is already YAML; retry *policy* is not. This is still a real choice for the Retry-After / jitter numbers.

### Option A: Hardcoded (jitter cap, Retry-After cap, MAX_RETRIES=3)

- **Pro:** Matches Q1.A and ADR-0027. Cannot misconfigure a 3-hour Retry-After.
- **Con:** Changing the 60s cap needs a deploy.

**Decision:** Option A — jitter cap, Retry-After cap (60s), and `MAX_RETRIES=3` stay hardcoded next to today’s retry constants. A lab that needs a different cap is a later YAML discussion with evidence.

_Considered and rejected: Option B (`defaults.llm_retry` YAML — expands Q1.A; easy to copy-paste a bad cap), Option C (env vars — hidden, untyped, splits policy from `tarsy.yaml`)._

---

## Q11: Cancellable retry sleep (not a proto deadline)

Q1 deferred passing remaining session/iteration deadline into Python (catalog 16). A new `deadline_unix_ms` proto field is out. What remains: today’s `await asyncio.sleep(delay)` is **not** tied to the gRPC context. Q1’s Retry-After waits can be up to the Q2 cap (~60s), so an expired session could sit in sleep.

Python `_stream_with_timeout` defaults to 300s. Go first-byte is 120s. Go cancels the gRPC context when the call context is done. grpc.aio already injects `CancelledError` into the handler task; `CancelledError` is a `BaseException` in Python 3.13 so the servicer `except Exception` does not swallow it. The work is to **not map cancel onto an error code**.

### Option B: Honor RPC abort in the retry loop; no new proto field

- **Pro:** A 60s Retry-After does not outlive session/iteration cancel or Go’s first-byte timeout.
- **Pro:** Same contract as mid-stream cancel today: Go already left via `ctx.Done()`.
- **Con:** Must be implemented so cancel means stop, not fail.

**Decision:** Option B — do not swallow `CancelledError`; do not yield `max_retries` / `provider_error` / `internal` after abort; do not walk fallback. Re-raise or return with no final error chunk. Do not pass `ServicerContext` into providers (optional `asyncio.Event` is fine). Do not abort shared SDK HTTP clients in v1. Tests: cancel during sleep → no second SDK call, no error yield.

_Considered and rejected: Option A (leave sleep unwired — Q2’s 60s wait can race a Go client that already gave up), mapping cancel to `max_retries`/`provider_error` (would fallback a dead session), relying on `context.cancelled()` inside `add_done_callback` (grpc.aio has returned False there)._

---

## Q12: What observability ships with v1?

Successful Python retries stay invisible (ADR-0027: “Python retry then success → nothing”). This design does not add a preemptive-skip path, so there is no new fallback reason to distinguish.

Today: Python logs retry attempts; Go has `tarsy_llm_errors_total` and `tarsy_llm_fallbacks_total`; timeline `error` / `provider_fallback`.

### Option A: Existing metrics only; Python retries stay log-only on success

- **Pro:** Matches ADR-0027. No proto, no new counter.
- **Con:** Operators still infer identical-retry cost from Python logs, not Go metrics.

**Decision:** Option A — no new metrics. Python successful retries remain log-only. Exhausted retries remain `max_retries` as today. Summarization still follows ADR-0024.

_Considered and rejected: Option B (count Python retries on the Go side — proto or Python process metrics; not in the Q1 include list)._
