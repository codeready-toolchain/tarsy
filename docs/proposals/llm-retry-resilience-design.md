# LLM Retry Resilience

**Status:** Final — decisions recorded in [llm-retry-resilience-questions.md](llm-retry-resilience-questions.md)

**Amends:** [ADR-0003](../adr/0003-llm-provider-fallback.md), [ADR-0027](../adr/0027-llm-transient-outage.md)

Promote to ADR after implementation via `/promote-to-adr`.

## Overview

TARSy already has a two-layer LLM resilience stack: Python identical retries (3 attempts, zero chunks yielded) then Go provider fallback (sticky for the execution, monotonic attempted-name skip). That architecture matches LiteLLM, OpenHands, LangGraph, and Gemini CLI. ADR-0027 made the inner retry cover Vertex `404` blips and stopped injecting provider-error JSON into the model prompt.

Production flake is no longer “we forgot to retry.” It is:

1. The retry **predicate is too HTTP-status-shaped** — connection drops never get an identical retry.
2. The retry **wait is naive** — no jitter, no `Retry-After` / Google `RetryInfo`, a wasted sleep after the last failed attempt, and nested SDK retries still on for xAI and LangChain Gemini.

This design tightens the Python retry loop. It does **not** replace ADR-0003 fallback, does not add YAML, does not raise `MAX_RETRIES`, and does not skip a provider for other executions. Raising attempt count would delay fallback, not improve it. Skip-primary cooldown misfires on Vertex publisher-model 404 flicker (200s interleaved with 404s) and would start new work on a weaker model while Claude still answers.

## Decisions

| # | Topic | Decision |
|---|---|---|
| [Q1](llm-retry-resilience-questions.md) | Scope | Hygiene only. Defer probe, don’t-stick-404, replica cooldown, 409, stall extra retry, keep-completed-blocks, proto deadline. Never raise attempts as the first move, SSE reconnect, hedge, watchdog, replay after tool/text, per-call unstick, YAML circuit breaker, or `ErrorInfo.retryable`. |
| [Q2](llm-retry-resilience-questions.md) | Wait numbers | Retry-After / RetryInfo cap **60s**. No-hint delay: full jitter `uniform(0, min(8s, RETRY_BACKOFF_BASE ** attempt))`. No sleep after the last failed attempt. Hint above 60s fail-fasts. |
| [Q3](llm-retry-resilience-questions.md) | Retry predicate | Connection/protocol/reset (not TLS certs), 408, throttle 429, 404, 5xx (including 529), timeout, empty response. Not 400/401/403, spend-cap 429, over-cap hint, or 409. |
| [Q4](llm-retry-resilience-questions.md) | SDK pin | TARSy’s loop owns retries. `max_retries=0` on `ChatXAI` and both `ChatGoogleGenerativeAI` sites. google-native `HttpRetryOptions(attempts=1)`. |
| [Q5](llm-retry-resilience-questions.md) | Cooldown | Not in this design. Two `max_retries` in 60s is a poor dead-primary signal on 404 flicker. |
| [Q6](llm-retry-resilience-questions.md) | Stickiness | Unchanged. All `max_retries` still stick the current execution. |
| [Q7](llm-retry-resilience-questions.md) | Probe | Not in this design. |
| [Q8](llm-retry-resilience-questions.md) | Mid-stream / stall | Unchanged from ADR-0027. |
| [Q9](llm-retry-resilience-questions.md) | Attempt count | Keep `MAX_RETRIES = 3`. |
| [Q10](llm-retry-resilience-questions.md) | Config | Hardcoded constants. No `defaults.llm_retry` YAML, no env-var override. |
| [Q11](llm-retry-resilience-questions.md) | Cancel | Honor RPC abort in the retry loop. Cancel means **stop**, not fail: no error chunk, no fallback. |
| [Q12](llm-retry-resilience-questions.md) | Observability | Existing Go metrics and timeline. Successful Python retries stay log-only. |

## Design Principles

1. **HTTP-identical retry before fallback.** Same request, no conversation mutation. Unchanged from ADR-0027.
2. **One retry owner.** TARSy’s Python loop is the retry. SDK constructors stay at zero inner retries so waits do not stack.
3. **Honor the server, then jitter.** `Retry-After` / `retry-after-ms` / `google.rpc.RetryInfo.retryDelay` win when present and ≤ 60s. Otherwise full jitter. Never wait hours on a spend-cap 429.
4. **Retry transients only.** Connection errors, 408, 429 (throttle), 404 (Vertex blip), 5xx including 529. Not 400/401/403. Not spend-cap / `x-should-retry: false`. Not 409.
5. **Do not replay after the stream has started.** Zero-chunk rule stays. Mid-stream continuation stays a Go concern.
6. **Surgical, hardcoded v1.** No new YAML. Constants live next to today’s `MAX_RETRIES` / `RETRY_BACKOFF_BASE`.
7. **Operators see exhaustion, not success.** Successful identical retries stay quiet. No provider-error JSON in the prompt (ADR-0027 Q6 unchanged).
8. **Cancel means stop.** RPC abort is not `max_retries`.

## Architecture / How It Works

### Call flow (unchanged shape)

```
Go Generate
  → Python generate() retry loop (MAX_RETRIES=3, only if 0 chunks yielded)
       success → stream to Go
       exhausted → ErrorInfo code=max_retries
       RPC cancelled → no error chunk (CancelledError propagates)
  → Go adaptive timeouts (120s first chunk, 60s stall, 5m ceiling)
  → on error: tryFallback (sticky, attempted-name skip)
       else same-provider continue / empty-response nudge / partial-text continue
```

### Python wait (same loop)

```
attempt 0..MAX_RETRIES-1
  stream
  success → return
  if CancelledError → re-raise (no error yield)
  if chunks_yielded > 0 → partial_stream_error (no retry)   # unchanged
  if not retryable → provider_error                         # expanded predicate
  if last attempt → yield max_retries (no sleep)
  delay = server_hint if 0 < hint <= 60s else uniform(0, min(8s, RETRY_BACKOFF_BASE ** attempt))
  sleep(delay)  # CancelledError must propagate; do not catch as retryable
```

Shared helper used by `google_native.py` and `langchain_provider.py`. Delete unused `EMPTY_RESPONSE_RETRY_DELAY`.

**Server hint** (first match, parsed as a duration): HTTP `Retry-After` (delta-seconds or HTTP-date), `retry-after-ms`, `google.rpc.RetryInfo.retryDelay` (e.g. `"53s"`). Use the hint as-is when it is in `(0, 60s]` — do not add extra jitter on top. Hint `> 60s` is non-retryable (`provider_error`) so Go can fall back. HTTP-date in the past → treat as no hint (jitter). Spend-cap / over-cap therefore arrive as `provider_error`; Go still applies ADR-0003 Q7 (one same-provider miss, then fallback). That is intentional: Python did not retry.

**No-hint ceiling** uses existing `RETRY_BACKOFF_BASE` (2), i.e. `uniform(0, min(8s, RETRY_BACKOFF_BASE ** attempt))`.

**Cancel:** grpc.aio already injects `CancelledError` into the handler task. `CancelledError` is a `BaseException` in Python 3.13; the servicer `except Exception` must not swallow it. Do not yield `max_retries` / `provider_error` / `internal`. Do not pass `ServicerContext` into providers (optional `asyncio.Event` is fine). Do not abort shared SDK HTTP clients in v1. Tests: cancel during sleep → no second SDK call, no error yield.

### Retryable predicate

Retry (zero chunks only) when any of:

| Class | Today | This design |
|---|---|---|
| 429 throttle | yes | yes; wait = Retry-After / RetryInfo if ≤ 60s |
| 429 spend-cap / `x-should-retry: false` / `usage_limit_reached` | retried as 429 | **no** — `provider_error` |
| Retry-After / RetryInfo `> 60s` | slept then retried | **no** — `provider_error` |
| 404 | yes (Vertex blip) | yes |
| 408 | no | yes |
| 409 | no | **no** (deferred) |
| 5xx including 529 | yes (500–599) | yes |
| empty response / asyncio timeout | yes | yes |
| connection / protocol / reset | **no** → `provider_error` | **yes** |
| 400 / 401 / 403 | no | no (cache 400 degrade unchanged) |
| TLS certificate verification failure | n/a | **no** (config, not flake) |

Connection detection walks cause/context like `extract_http_status` today. Match httpx/google-genai transients (`ConnectError`, `ConnectTimeout`, `ReadTimeout`, `RemoteProtocolError`, `LocalProtocolError`) and common `errno`/message fragments (`ECONNRESET`, `ETIMEDOUT`, `EPIPE`, `ENOTFOUND`, `EAI_AGAIN`, `ECONNREFUSED`).

### Nested SDK retries

ADR-0027 said SDK constructors keep `max_retries=0`. That is true for OpenAI, Anthropic, and Vertex Claude (`ChatAnthropicVertex`). Gaps to pin:

```python
# LangChain — both ChatGoogleGenerativeAI construction sites (GOOGLE and VERTEXAI Gemini)
ChatXAI(..., max_retries=0)
ChatGoogleGenerativeAI(..., max_retries=0)

# google-native
genai.Client(
    api_key=api_key,
    http_options=types.HttpOptions(
        retry_options=types.HttpRetryOptions(attempts=1),  # original only
    ),
)
```

`attempts=1` is “original only” in current google-genai (`attempts=0` is coerced to 1). TARSy’s loop stays the retry owner: SDKs disagree with each other, do not retry Vertex 404, and would not honor spend-cap fail-fast or the zero-chunk rule.

### Sticky fallback

Unchanged. Exhausted 404/429/5xx still arrive as `max_retries`, fallback immediately, then stick for the rest of the execution.

### Constants (hardcoded)

| Constant | Value | Where |
|---|---|---|
| `MAX_RETRIES` | 3 | Python (existing) |
| `RETRY_BACKOFF_BASE` | 2 | Python (existing) |
| Retry-After / RetryInfo cap | 60s | Python wait helper |
| Full-jitter cap | 8s | Python wait helper |
| No-hint ceiling | `RETRY_BACKOFF_BASE ** attempt` | Python wait helper |

No `defaults.llm_retry` YAML. No env-var override.

### Explicitly out of this design

| Item | Why |
|---|---|
| Replica-local provider cooldown | 404 flicker is a poor dead-primary signal; skip would start new work on a weaker model while Claude still serves. Sibling rediscovery of a true outage stays ADR-0027 later #2 |
| Probe / unstick after N healthy fallback turns | Oscillation risk |
| Don’t-stick-on-transient-404 | Amends ADR-0003 Q1; own design |
| Retry HTTP 409 | Rare for Generate |
| Raise Python `MAX_RETRIES` to 5 or 10 | Delays fallback; first identical retry is the 404 absorber; Claude Code needs 10 because it has no ordered provider list |
| Hedged / parallel duplicate Generates | Doubles token cost |
| Indefinite retry watchdog | Session timeout already bounds wait |
| Replay after a tool/text block has started | Duplicate MCP side effects |
| Codex-style SSE reconnect | Stall timeout + continue-from-partial already covers mid-stream |
| Keep-completed-blocks mid-stream | Iterating tools run only after a successful Generate |
| Remaining-deadline proto field | Q11 uses RPC abort instead |
| OpenHands per-call reset to primary | Fights ADR-0003 Q1 |
| YAML circuit breaker / `defaults.llm_retry` | Same skip-primary misfire; wait constants stay hardcoded |
| Plumb `ErrorInfo.retryable=true` | Go keys off error *codes* |
| Count Python retry attempts on the Go side | Proto or Python process metrics; later |

## Core Concepts

### Identical retry

Repeat the same Generate inside the LLM service. Go never sees a failure if it succeeds. Still only when zero chunks have been yielded over gRPC.

### Server-informed wait

If the provider said how long to wait, use that value when it is in `(0, 60s]`. Otherwise full jitter on `RETRY_BACKOFF_BASE ** attempt` with an 8s cap. If the hinted wait is **above 60s**, treat as non-retryable (`provider_error`) rather than sleeping the hinted duration.

### Full jitter

`sleep = random.uniform(0, min(8s, RETRY_BACKOFF_BASE ** attempt))` with `RETRY_BACKOFF_BASE = 2`. AWS’s recommendation; better than “exponential plus ±25%” when many workers fail together. MCP already uses a jittered window (`RetryBackoffMin`–`RetryBackoffMax`); LLM should not be the synchronized path.

### Nested-retry pin

SDK `max_retries=0` / `HttpRetryOptions(attempts=1)` so a 429 is retried only by TARSy’s loop.

### Spend-cap 429 vs throttle 429

Throttle: short Retry-After, `x-should-retry` absent or true → identical retry. Spend-cap / quota exhaustion: Retry-After of hours, `x-should-retry: false`, or error type `usage_limit_reached` → do not retry; surface to Go as `provider_error`. Go then follows ADR-0003 Q7 (threshold 2 / SingleShot 1), not immediate `max_retries` fallback.

### Cancel vs exhaust

RPC abort is not an LLM error. Go already left via `ctx.Done()`. Python must not invent `max_retries` after abort.

## Implementation Plan

One code PR. Docs can ride with it. Tests ship with the change.

### PR 1 — Python retry hygiene

Leaves Go fallback behavior unchanged. A 404/429/5xx that still exhausts 3 attempts still becomes `max_retries`.

**Lands:**

- Shared wait helper in `llm-service/llm/providers/` (e.g. `retry.py`) used by `google_native.py` and `langchain_provider.py`: full jitter, skip sleep on last attempt, honor Retry-After / RetryInfo with 60s cap, `CancelledError` not swallowed. Extend `http_status.py` for 408 and connection/protocol errors; spend-cap / over-cap hint → not retryable; TLS cert failures → not retryable. Keep 404/429/5xx.
- Pin `max_retries=0` on `ChatXAI` and both `ChatGoogleGenerativeAI` sites (`ProviderType.GOOGLE` and Vertex Gemini). Pin google-native `HttpRetryOptions(attempts=1)` on `genai.Client(api_key=...)`.
- Delete unused `EMPTY_RESPONSE_RETRY_DELAY` (defined, never referenced; ADR-0027’s “extra sleep window” is this leftover).
- Tests: `test_http_status.py` (new retryable / not-retryable cases), `test_google_native.py` / `test_langchain_provider.py` (jitter bounded, no sleep after last fail, Retry-After honored including 53s, spend-cap and >60s hint not retried, connection error retried, cancel during sleep yields no error and does not call the SDK again). Extend existing ctor asserts (`test_openai_max_retries_*`, Anthropic, Vertex Claude) to xAI and both ChatGoogleGenerativeAI sites; add google-native `attempts=1`.

**Does not:** change Go fallback counters, stickiness, adaptive timeouts, or proto. Does not skip a provider for other executions. LangChain already yields usage only after content; do not treat usage chunks as `chunks_yielded` for the zero-chunk rule (google-native already buffers usage).

### PR 2 — Docs (same PR as 1 is fine)

- Short operator note in `docs/architecture-overview.md`: retries honor Retry-After (capped at 60s); no new YAML. Also update the Python-retry sentence (today: timeout / empty / 429/404/5xx) to include connection errors, 408, jitter, and spend-cap fail-fast.
- Amend ADR-0027 “permanently missing model ID waits the existing extra sleep window” — that referred to unused `EMPTY_RESPONSE_RETRY_DELAY`; last-attempt sleep is gone. Leave process-wide cooldown, probe, and don’t-stick-404 as later.

**Promote to ADR** after implementation via `/promote-to-adr` (not this plan’s PR).

### Later (not this design)

| Item | Notes |
|---|---|
| Replica-local provider cooldown | Needs a dead-primary signal that is not two 404 `max_retries` in 60s |
| Don’t-stick-on-transient-404 | Amends ADR-0003 Q1; own design |
| Probe / unstick | Own design; anti-oscillation rules |
| Retry 409 | If Generate starts returning it |
| Stall-with-zero-text one extra retry | Separate from identical HTTP retry |
| Remaining deadline proto field | Only if Q11’s abort path is not enough |
| `defaults.llm_retry` YAML | Only with evidence a lab needs a different cap |
| Python retry attempt metrics | Proto or Python process metrics |
