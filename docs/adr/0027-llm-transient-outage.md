# ADR-0027: Transient LLM Outage Handling

**Status:** Implemented  
**Date:** 2026-08-29  
**Amends:** [ADR-0003](0003-llm-provider-fallback.md), [ADR-0017](0017-native-tool-fallback-safety.md), [ADR-0024](0024-tool-summarization-provider.md), [ADR-0026](0026-prompt-caching.md)

## Overview

Vertex (and similar providers) sometimes return **intermittent** `404 NOT_FOUND` on publisher models that were succeeding in the same session. Before this decision that did not get a real HTTP retry. After two `provider_error`s, Go **demoted the execution for good**, could **walk back to a model that already failed**, and injected the 404 JSON into the **model prompt** (operators already saw it on the timeline).

This decision makes retries absorb short blips, and makes fallback monotonic and less hair-trigger. Sticky fallback for a real outage stays ([ADR-0003](0003-llm-provider-fallback.md) Q1).

## Design Principles

1. **HTTP-level retry before fallback.** Same request, no conversation mutation, no extra “please try again” turn.
2. **Fallback is a demotion, not a search.** Never retry a provider this execution already left.
3. **“Consecutive” means consecutive.** A success in between resets the streak.
4. **Operators keep seeing errors; the model does not.** Keep timeline/UI events. Do not put provider-error text into LLM messages. Exception: mid-stream **partial output** still goes into the next prompt so the model can continue (without the error JSON).
5. **v1 is surgical.** No circuit breaker, no probing the primary, no new YAML.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Where to absorb short 404/429/5xx blips | Python LLM service: identical Generate retry, 3 attempts with existing backoff, **before** Go fallback | Go same-provider retry burns a max-iterations slot and used to mutate the conversation. Python retries do not. A 10–30s Vertex 404 is a blip, not an outage. |
| Q2 | Which HTTP statuses retry identically | **429, 404, 5xx** with **zero chunks yielded**. Not 400, 401, 403. Not after streaming has started | 404 is the observed publisher-model blip. 400 is prompt-cache degrade ([ADR-0026](0026-prompt-caching.md)). 401/403 are credentials. Mid-stream failures are a different path (partial text / stall). |
| Q3 | What Go sees after Python retries exhaust | `max_retries` → immediate fallback ([ADR-0003](0003-llm-provider-fallback.md) Q7) | Python already retried 3×. Classifying exhausted 404s as `provider_error` would force an extra Go miss and a prompt inject. |
| Q4 | Fallback candidate skip | Skip any name already on the **attempted** list (primary starts on it). Keep [ADR-0017](0017-native-tool-fallback-safety.md) native-tool skip; incompatible names stay off the attempted list | Skipping only the *current* name allowed ping-pong back to a provider this execution already left. |
| Q5 | Consecutive error counters | Reset after a **successful** iterating LLM call, not only after a provider switch | Timeout-abort tracking already reset on success; fallback counters did not. Two non-adjacent `provider_error`s were treated as a streak. |
| Q6 | Error text in the conversation | Timeline/UI events stay. Do **not** append no-partial provider errors to messages. Keep loop nudge, empty-response nudge, and truncated partial text **without** the cause JSON | Operators already have `error` / `provider_fallback` events. Publisher-model JSON in the next prompt made later models fail or get confused. |
| Q7 | Sticky fallback for the rest of the execution | Unchanged ([ADR-0003](0003-llm-provider-fallback.md) Q1) | Right for a sustained outage. Wrong as the *first* reaction to a blip — Q1–Q5 fix that without unsticking. |

## Architecture

### Call flow

```
Generate (0 chunks yet)
  ├─ 429 / 404 / 5xx → Python retryable error, 3× backoff
  │     success → done (Go never sees a failure)
  │     exhausted → error code max_retries → Go fallback immediately
  ├─ cache 400 → existing degrade (unchanged; not an identical retry)
  └─ other 4xx (400/401/403/…) / unknown → provider_error (unchanged)

Go iterating, on LLM error:
  ├─ max_retries / credentials → fallback now (no prompt append)
  ├─ no-partial provider_error / invalid_request / transport / initial_timeout
  │     → error timeline event; do NOT append to messages / DB conversation
  │     → 2nd consecutive → fallback
  ├─ partial_stream / stall with partial text → continue-from-here + truncated text only
  ├─ loop detection → instructional nudge (not an error dump)
  ├─ empty-response nudge → unchanged
  ├─ success → reset consecutive fallback counters
  └─ fallback skip: name already attempted this execution (including primary);
        keep RequiresNativeTools skip
```

### Python HTTP retry

The LLM service already had a 3-attempt loop. It previously treated as retryable only **timeout** and **empty response** (LangChain), plus typed **5xx** (google-native `ServerError`). A Vertex `404` was a normal client exception → immediate `provider_error` / `retryable=false`.

SDK constructors keep `max_retries=0`; TARSy’s loop is the retry. Prompt cache does **not** disable that loop: it sits inside each cache-degrade variant, so a 404 with cache markers on is retried with the same markers. Cache `400` stays the dedicated strip path ([ADR-0026](0026-prompt-caching.md)).

Raise a retryable error when **no chunks have been yielded** and the HTTP status is **429, 404, or 5xx**. Do **not** treat all google-native 4xx client errors like 5xx; retry `ClientError` only for 429 or 404.

Status extraction must cover Anthropic-shaped Vertex Claude errors (`Error code: 404 - [...]`), google-genai `.code` / `.status` (`NOT_FOUND`, not `404`), nested `response.status_code`, and wrapped SDK errors with no attributes. Walk cause/context and take the first HTTP-like integer.

| Status | Identical retry? |
|---|---|
| 429, 404, 5xx | yes (any provider) |
| 400 | no (cache 400 still degrades) |
| 401 / 403 | no |

A permanently missing model ID waits the existing extra sleep window, then `max_retries` → Go fallback.

### Go fallback

**Skip already-attempted names.** The attempted set already recorded the chain (primary starts in it) but was unused for skip. Fallback now refuses any name on that list. Native-tool incompatible skip is unchanged and those names stay off the set.

The same attempted-name skip applies to **summarization-local** failover ([ADR-0024](0024-tool-summarization-provider.md)): seed the set with the call’s start name, append each hop, do not share the investigator’s set, do not apply native-tool skip.

**Reset counters on success.** After a successful iterating Generate, reset consecutive provider/partial error counters. This is not the iteration timeout-abort reset. Scoring and single-shot already trip on the first error (`SingleShot`). Forced conclusion has no same-provider retry (fallback-or-fail); after Q3, 404/429/5xx arrive as `max_retries` and fallback immediately.

**Show errors to users, not to the model.**

| What happened | Timeline / UI | Conversation |
|---|---|---|
| Python retry then success | nothing | unchanged |
| First Go no-partial error | `error` event | **no** append |
| Fallback switch | `provider_fallback` + execution record + dashboard chip; no extra `error` on that call | no append (already true) |
| Summarization failover | log + `summarization_fallback` metadata; no `provider_fallback` event | unchanged |

Prompt injection to **keep**: loop instructional nudge; empty-response “please provide a response”; truncated **partial text** (“continue from where you left off”) **without** publisher-model JSON.

Sticky-for-execution is unchanged.

## Core concepts

- **Python retry** — repeat the same Generate inside the LLM service. Go never sees a failure if it succeeds.
- **Go same-provider retry** — continue with unchanged messages. Operators still get an `error` timeline event. Burns one iterating slot.
- **Attempted set** — providers this execution (or this summarization call) has already used; fallback only moves forward.

## Later (not v1)

- **Probe / unstick.** After N successful fallback turns (or a cooldown), try the original primary once. ADR-0003 Q1 assumed outages last longer than one execution; a short blip within one session shows the opposite. Easy to oscillate — design separately.
- **Process-wide circuit breaker.** If a primary 404s twice in ~60s, skip it for ~2 minutes on *all* executions in this replica so a second sub-agent does not rediscover the same blip. Needs shared in-process state + clock.
- **Do not stick on transient 404.** Only stick on `max_retries` after a long fail, or credentials. Weaker than a probe; still a behavior change to ADR-0003 Q1.
