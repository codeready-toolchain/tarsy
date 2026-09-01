# ADR-0028: LLM Retry Resilience

**Status:** Implemented  
**Date:** 2026-08-31  
**Amends:** [ADR-0003](0003-llm-provider-fallback.md), [ADR-0027](0027-llm-transient-outage.md)

## Overview

TARSy already has a two-layer LLM resilience stack: Python identical retries (3 attempts, only when zero chunks have been yielded) then Go provider fallback (sticky for the execution, monotonic attempted-name skip). That shape matches LiteLLM, OpenHands, LangGraph, and Gemini CLI. [ADR-0027](0027-llm-transient-outage.md) made the inner retry cover Vertex `404` blips and stopped injecting provider-error JSON into the model prompt.

Production flake was no longer “we forgot to retry.” It was:

1. The retry **predicate was too HTTP-status-shaped** — connection drops never got an identical retry.
2. The retry **wait was naive** — no jitter, no `Retry-After` / Google `RetryInfo`, a wasted sleep after the last failed attempt, and nested SDK retries still on for xAI and LangChain Gemini.

This decision tightens the Python retry loop. It does **not** replace [ADR-0003](0003-llm-provider-fallback.md) fallback, does not add YAML, does not raise the attempt count, and does not skip a provider for other executions. Raising attempts would delay fallback, not improve it. Skip-primary cooldown misfires on Vertex publisher-model 404 flicker (200s interleaved with 404s) and would start new work on a weaker model while Claude still answers.

## Design Principles

1. **HTTP-identical retry before fallback.** Same request, no conversation mutation. Unchanged from ADR-0027.
2. **One retry owner.** TARSy’s Python loop is the retry. SDK constructors stay at zero inner retries so waits do not stack.
3. **Honor the server, then jitter.** `Retry-After` / `retry-after-ms` / `google.rpc.RetryInfo.retryDelay` win when present and ≤ 60s. Otherwise full jitter. Never wait hours on a spend-cap 429.
4. **Retry transients only.** Connection errors, 408, 429 (throttle), 404 (Vertex blip), 5xx including 529. Not 400/401/403. Not spend-cap / `x-should-retry: false`. Not 409.
5. **Do not replay after the stream has started.** Zero-chunk rule stays. Mid-stream continuation stays a Go concern.
6. **Surgical, hardcoded v1.** No new YAML. Wait and attempt constants live next to the existing retry loop.
7. **Operators see exhaustion, not success.** Successful identical retries stay quiet. No provider-error JSON in the prompt (ADR-0027 Q6 unchanged).
8. **Cancel means stop.** RPC abort is not `max_retries`.

## Decisions

| # | Topic | Decision | Rationale |
|---|---|---|---|
| Q1 | Scope | Hygiene only: jitter, no last-attempt sleep, Retry-After/RetryInfo with cap, spend-cap fail-fast, connection retries, 408, SDK pin. Defer probe, don’t-stick-404, replica cooldown, 409, stall extra retry, keep-completed-blocks, proto deadline. Never raise attempts as the first move, SSE reconnect, hedge, watchdog, replay after tool/text, per-call unstick, YAML circuit breaker, or `ErrorInfo.retryable`. | Two-layer shape is already sound. Hygiene closes wait / predicate / nested-SDK gaps without YAML, probe oscillation, or skipping a primary that still serves. Vertex publisher-model 404s are per-request flicker; a replica skip would treat two unlucky exhaustions as “primary is dead.” |
| Q2 | Wait numbers | Retry-After / RetryInfo cap **60s**. No-hint delay: full jitter `uniform(0, min(8s, 2 ** attempt))`. No sleep after the last failed attempt. Hint above 60s fail-fasts. | Vertex 429s often hint `53s`; a cap below that fail-fasts them. 60s matches first-party “reasonable Retry-After”; 8s is the SDK-style no-hint ceiling. At most one long server-informed wait, not a wait after exhaustion. |
| Q3 | Retry predicate | Connection/protocol/reset (not TLS certs), 408, throttle 429, 404, 5xx (including 529), timeout, empty response. Not 400/401/403, spend-cap 429, over-cap hint, or 409. | Connection drops were the remaining flake ADR-0027’s HTTP-status list missed. TLS cert failures are config. 409 is rare for Generate. |
| Q4 | SDK pin | TARSy’s loop owns retries. LangChain xAI and both Gemini construction sites pin inner retries to zero. google-native HTTP retry is original-only (`attempts=1`; `attempts=0` is coerced to 1). | ADR-0027 already required this for OpenAI / Anthropic / Vertex Claude; xAI and Gemini still stacked SDK waits. SDKs disagree with each other, do not retry Vertex 404, and would not honor spend-cap fail-fast or the zero-chunk rule. |
| Q5 | Cooldown | Not in this design. | Two `max_retries` in 60s is a poor dead-primary signal on 404 flicker. New work would start on a weaker model while Claude still answers. |
| Q6 | Stickiness | Unchanged. All `max_retries` still stick the current execution. | Right for a sustained outage. Unsticking 404 amends ADR-0003 Q1 and needs its own design. Revisit if traces show long executions stuck on a weaker fallback after a short publisher-model blip. |
| Q7 | Probe | Not in this design. | Easy to oscillate. Per-call reset to primary fights ADR-0003 sticky-for-execution. |
| Q8 | Mid-stream / stall | Unchanged from ADR-0027. | Stall timeout + continue-from-partial already covers mid-stream. SSE reconnect and replay after a tool/text block would duplicate MCP side effects. |
| Q9 | Attempt count | Keep 3 identical retries. | The first identical retry is the 404 absorber. Extra attempts delay fallback to the next list entry. Claude Code needs 10 because it has no ordered provider list. |
| Q10 | Config | Hardcoded constants. No `defaults.llm_retry` YAML, no env-var override. | Fallback *list* is already YAML; retry *policy* is not. A 3-hour Retry-After must not be configurable by accident. A lab that needs a different cap is a later discussion with evidence. |
| Q11 | Cancel | Honor RPC abort in the retry loop. Cancel means **stop**, not fail: no error chunk, no fallback. | A 60s Retry-After must not outlive session/iteration cancel or Go’s first-byte timeout. grpc.aio already injects `CancelledError`; it is a `BaseException` in Python 3.13, so it must not be mapped onto `max_retries` / `provider_error` / `internal`. |
| Q12 | Observability | Existing Go metrics and timeline. Successful Python retries stay log-only. | Matches ADR-0027. No proto, no new counter. Exhausted retries remain `max_retries`. |

## Architecture

### Call flow (unchanged shape)

```
Go Generate
  → Python generate() retry loop (3 attempts, only if 0 chunks yielded)
       success → stream to Go
       exhausted → ErrorInfo code=max_retries
       RPC cancelled → no error chunk (CancelledError propagates)
  → Go adaptive timeouts (120s first chunk, 60s stall, 5m ceiling)
  → on error: tryFallback (sticky, attempted-name skip)
       else same-provider continue / empty-response nudge / partial-text continue
```

### Python wait (same loop)

```
attempt 0..2
  stream
  success → return
  if CancelledError → re-raise (no error yield)
  if chunks_yielded > 0 → partial_stream_error (no retry)
  if not retryable → provider_error
  if last attempt → yield max_retries (no sleep)
  delay = server_hint if 0 < hint <= 60s else uniform(0, min(8s, 2 ** attempt))
  sleep(delay)  # CancelledError must propagate; do not catch as retryable
```

**Server hint** (first match, parsed as a duration): HTTP `Retry-After` (delta-seconds or HTTP-date), `retry-after-ms`, `google.rpc.RetryInfo.retryDelay` (e.g. `"53s"`). Use the hint as-is when it is in `(0, 60s]` — do not add extra jitter on top. Hint `> 60s` is non-retryable (`provider_error`) so Go can fall back. HTTP-date in the past → treat as no hint (jitter). Spend-cap / over-cap therefore arrive as `provider_error`; Go still applies ADR-0003 Q7 (one same-provider miss, then fallback). That is intentional: Python did not retry.

**Cancel:** grpc.aio already injects `CancelledError` into the handler task. Do not yield `max_retries` / `provider_error` / `internal`. Do not pass the gRPC servicer context into providers. Do not abort shared SDK HTTP clients in v1. Cancel during sleep must not start a second provider call and must not yield an error chunk.

Usage-only chunks do not count as `chunks_yielded` for the zero-chunk rule.

### Retryable predicate

Retry (zero chunks only) when any of:

| Class | ADR-0027 | This decision |
|---|---|---|
| 429 throttle | yes | yes; wait = Retry-After / RetryInfo if ≤ 60s |
| 429 spend-cap / `x-should-retry: false` / `usage_limit_reached` | retried as 429 | **no** — `provider_error` |
| Retry-After / RetryInfo `> 60s` | slept then retried | **no** — `provider_error` |
| 404 | yes | yes |
| 408 | no | yes |
| 409 | no | **no** (deferred) |
| 5xx including 529 | yes | yes |
| empty response / asyncio timeout | yes | yes |
| connection / protocol / reset | **no** → `provider_error` | **yes** |
| 400 / 401 / 403 | no | no (cache 400 degrade unchanged) |
| TLS certificate verification failure | n/a | **no** (config, not flake) |

Connection detection walks cause/context like HTTP status extraction. Match httpx/google-genai transients (`ConnectError`, `ConnectTimeout`, `ReadTimeout`, `RemoteProtocolError`, `LocalProtocolError`) and common `errno`/message fragments (`ECONNRESET`, `ETIMEDOUT`, `EPIPE`, `ENOTFOUND`, `EAI_AGAIN`, `ECONNREFUSED`).

### Nested SDK retries

ADR-0027 said SDK constructors keep inner retries at zero. That was true for OpenAI, Anthropic, and Vertex Claude. Gaps this decision pins: LangChain xAI, both LangChain Gemini construction sites (Google API and Vertex Gemini), and google-native HTTP retry options (original attempt only). TARSy’s loop stays the retry owner.

### Sticky fallback

Unchanged. Exhausted 404/429/5xx still arrive as `max_retries`, fallback immediately, then stick for the rest of the execution.

### Constants (hardcoded)

| Constant | Value |
|---|---|
| Identical retry attempts | 3 |
| No-hint backoff base | 2 |
| Retry-After / RetryInfo cap | 60s |
| Full-jitter cap | 8s |
| No-hint ceiling | `2 ** attempt` |

No `defaults.llm_retry` YAML. No env-var override.

ADR-0027’s “permanently missing model ID waits the existing extra sleep window” referred to an unused empty-response delay. Last-attempt sleep is gone; exhaustion yields `max_retries` immediately.

## Core Concepts

### Identical retry

Repeat the same Generate inside the LLM service. Go never sees a failure if it succeeds. Still only when zero chunks have been yielded over gRPC.

### Server-informed wait

If the provider said how long to wait, use that value when it is in `(0, 60s]`. Otherwise full jitter on `2 ** attempt` with an 8s cap. If the hinted wait is **above 60s**, treat as non-retryable (`provider_error`) rather than sleeping the hinted duration.

### Full jitter

`sleep = random.uniform(0, min(8s, 2 ** attempt))`. AWS’s recommendation; better than “exponential plus ±25%” when many workers fail together. MCP already uses a jittered window; LLM should not be the synchronized path.

### Nested-retry pin

SDK inner retries stay off so a 429 is retried only by TARSy’s loop.

### Spend-cap 429 vs throttle 429

Throttle: short Retry-After, `x-should-retry` absent or true → identical retry. Spend-cap / quota exhaustion: Retry-After of hours, `x-should-retry: false`, or error type `usage_limit_reached` → do not retry; surface to Go as `provider_error`. Go then follows ADR-0003 Q7 (threshold 2 / SingleShot 1), not immediate `max_retries` fallback.

### Cancel vs exhaust

RPC abort is not an LLM error. Go already left via `ctx.Done()`. Python must not invent `max_retries` after abort.

## Explicitly out of this design

| Item | Why |
|---|---|
| Replica-local provider cooldown | 404 flicker is a poor dead-primary signal; skip would start new work on a weaker model while Claude still serves. Sibling rediscovery of a true outage stays ADR-0027 later #2 |
| Probe / unstick after N healthy fallback turns | Oscillation risk |
| Don’t-stick-on-transient-404 | Amends ADR-0003 Q1; own design |
| Retry HTTP 409 | Rare for Generate |
| Raise Python attempts to 5 or 10 | Delays fallback |
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

## Future considerations

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

## Related

- [ADR-0003: LLM Provider Fallback](0003-llm-provider-fallback.md)
- [ADR-0024: Tool Summarization Provider](0024-tool-summarization-provider.md) — summarization still follows the same Python retry, then local failover
- [ADR-0026: Prompt Caching](0026-prompt-caching.md) — cache 400 degrade unchanged; identical retry still runs with cache markers on
- [ADR-0027: Transient LLM Outage Handling](0027-llm-transient-outage.md)
