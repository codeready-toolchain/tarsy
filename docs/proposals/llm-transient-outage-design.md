# Transient LLM Outage Handling

**Status:** Draft — verified against source; no blocking questions  
**Related:** [ADR-0003](../adr/0003-llm-provider-fallback.md), [ADR-0017](../adr/0017-native-tool-fallback-safety.md)

## Overview

Vertex (and similar providers) sometimes return **intermittent** `404 NOT_FOUND` on publisher models that were succeeding in the same session. Today that does not get a real retry. After two `provider_error`s, Go **demotes the execution for good**, can **walk back to a model that already failed**, and injects the 404 JSON into the **model prompt** (operators already see it on the timeline).

This proposal makes retries absorb short blips, and makes fallback monotonic and less hair-trigger. Sticky fallback for a real outage stays.

## Design Principles

1. **HTTP-level retry before fallback.** Same request, no conversation mutation, no extra “please try again” turn.
2. **Fallback is a demotion, not a search.** Never retry a provider this execution already left.
3. **“Consecutive” means consecutive.** A success in between resets the streak.
4. **Operators keep seeing errors; the model does not.** Keep today’s timeline/UI events. Do not put provider-error text into LLM messages. Exception: mid-stream **partial output** still goes into the next prompt so the model can continue (without the error JSON).
5. **v1 is surgical.** No circuit breaker, no probing the primary, no new YAML.

## Current gaps (why a partial outage cascaded)

Python already has a 3-attempt loop (`MAX_RETRIES = 3`, `delay = 2 ** attempt` including a sleep after the last failed attempt). LangChain only raises `_RetryableError` for **timeout** and **empty response**. google-native also retries typed `ServerError` (5xx). A Vertex `404` is a normal `Exception` / `ClientError` → immediate `provider_error` / `retryable=false`.

SDK `max_retries=0` on Anthropic / Vertex Claude / OpenAI constructors; TARSy’s loop is the retry. Prompt cache does **not** disable that loop: it sits **inside** each `cache_degrade_sequence` variant, so a 404 with cache markers on is retried with the same markers. Cache `400` is a different path (`is_bad_request` → strip TTL, then strip markers) and must stay that way.

Go then:

- Counts `provider_error` toward fallback **without resetting on success**. `IterationState.RecordSuccess()` (already called after a good Generate) only clears timeout-abort tracking. `FallbackState.resetCounters()` runs only after a **successful provider switch**.
- On the first miss (`tryFallback` returns false), **appends the full error** via `buildRetryMessage` + `storeObservationMessage`. That user turn is resent on the next Generate. Timeline already has `EventTypeError`. The fallback path (`tryFallback` true → `continue`) does **not** append — so classified `max_retries` already avoids the blob.
- Skips fallback entries only when `candidate.ProviderName ==` the *current* provider ([ADR-0017](../adr/0017-native-tool-fallback-safety.md) native-tool skip is separate and stays). `AttemptedProviders` already records the chain (primary starts in the list) but is not used for skip.

Fallback then **sticks** for the rest of the execution (ADR-0003 Q1). That is still right for a sustained outage; it is wrong as the *first* reaction to a 10–30s blip.

Go’s same-provider retry `continue`s the iterating `for` and **consumes a max-iterations slot**. Python retries do not. That is why HTTP retry belongs in Python.

## How it works

```
Generate (0 chunks yet)
  ├─ 429 / 404 / 5xx → Python _RetryableError, 3× backoff
  │     success → done (Go never sees a failure)
  │     exhausted → ErrorInfo code=max_retries → Go fallback immediately
  ├─ cache 400 → existing degrade (unchanged; not an identical retry)
  └─ other 4xx (400/401/403/…) / unknown → provider_error (unchanged)

Go iterating, on LLM error:
  ├─ max_retries / credentials → fallback now (no prompt append; already true today)
  ├─ no-partial provider_error / invalid_request / transport / initial_timeout
  │     → EventTypeError as today; do NOT append to messages / DB conversation
  │     → 2nd consecutive → fallback
  ├─ partial_stream / stall with PartialText → continue-from-here + truncated text only
  ├─ loop detection → keep today’s instructional nudge (not an error dump)
  ├─ empty-response nudge → unchanged
  ├─ success → fbState.resetCounters()
  └─ fallback skip: name already in AttemptedProviders (including primary);
        keep RequiresNativeTools skip
```

### Python (LangChain + google-native)

Raise `_RetryableError` when **no chunks have been yielded** and the error is HTTP **429, 404, or 5xx**.

Do **not** treat all google-native `ClientError` like `ServerError`. `ServerError` is 5xx by type; `ClientError` is **all 4xx**. Catch `ClientError` and retry only when the status is 429 or 404.

Status extraction cannot copy `prompt_cache.is_bad_request` as-is (`status_code` / `status == 400`). Vertex Claude 404s are Anthropic-shaped (`Error code: 404 - [{'error': ...}]`). google-genai uses `.code` (int) and `.status` (`NOT_FOUND`, not `404`). Walk `__cause__` / `__context__` and take the first HTTP-like int from:

- `status_code`, `code` (if int), `status` (if int)
- nested `response.status_code`
- otherwise parse `Error code: NNN` in `str(err)` (covers wrapped SDK errors with no attributes)

| Status | Retry? |
|---|---|
| 429, 404, 5xx | yes (any provider) |
| 400 | no identical retry (cache 400 still degrades) |
| 401 / 403 | no |

Keep `MAX_RETRIES = 3` and existing backoff. A permanently missing model ID waits the current extra sleep window, then `max_retries` → fallback.

After those retries fail, yield `code=max_retries` (not `provider_error`) so Go matches ADR-0003: “Python already retried 3× → fallback immediately.” The `for`/`else` in `langchain_provider.py` / `google_native.py` already does this once the error is `_RetryableError`.

### Go fallback

**Skip already attempted names.** `AttemptedProviders` already records the chain; use it in the `tryFallback` (and `nextSummarizationFallback`) skip loop. Primary starts in that list, so if it also appears later in `fallback_providers` it is not selected again. Native-tool incompatible skip unchanged; those names stay out of `AttemptedProviders`.

**Reset counters on success.** After a successful `callLLMWithStreaming` in `iterating.go`, call `fbState.resetCounters()`. Do not confuse this with `IterationState.RecordSuccess()`. Scoring and single-shot already use `SingleShot` (threshold 1), so reset is optional there. `forceConclusion` shares the iterating `fbState` and has **no** same-provider retry today (fallback-or-fail). After PR 1, 404/429/5xx arrive as `max_retries` and fallback immediately even with counters at 0.

**Show errors to users, not to the model.** Do not add or remove timeline events:

| What happened | Timeline / UI today | v1 |
|---|---|---|
| Python retry then success | nothing | unchanged |
| First Go no-partial error | `EventTypeError` | keep event; **stop** `buildRetryMessage` / `storeObservationMessage` / `messages` append |
| Fallback switch | `EventTypeProviderFallback` + execution record + dashboard chip; **no** extra `EventTypeError` on that call | unchanged |
| Summarization failover | log + `summarization_fallback` metadata; no `provider_fallback` event | unchanged |

Prompt injection to **keep**: loop instructional nudge; empty-response “please provide a response”; truncated **partial text** (“continue from where you left off”) **without** `poe.Cause` / publisher-model JSON.

`forceConclusion` / single-shot / scoring already do not call `buildRetryMessage`.

Sticky-for-execution is unchanged.

## Core concepts

- **Python retry** — repeat the same Generate inside the LLM service. Go never sees a failure if it succeeds.
- **Go same-provider retry** — `continue` with unchanged messages. Operators still get `EventTypeError`. Burns one iterating slot.
- **Attempted set** — providers this execution has already used; fallback only moves forward.

## Implementation plan

### PR 1 — Python: retry 429 / 404 / 5xx - DONE

- Shared status helper (or small sibling of `is_bad_request`): 429 / 404 / 5xx via the walk above.
- `langchain_provider.py`: in the `except Exception` path, if `chunks_yielded == 0` and status matches, raise `_RetryableError` (after the cache-400 degrade check, so 400 still degrades).
- `google_native.py`: retry `ClientError` only for 429/404; `ServerError` stays as today.
- Exhausted `_RetryableError` → existing `max_retries`.
- Tests: 404 retries then succeeds (`status_code=404`, `.code=404`, and message-only `Error code: 404 - ...`); 429 retries; exhausted 404 yields `max_retries`; 400 does not enter this loop (cache degrade unchanged); 401/403 do not retry; google-native `ClientError(400)` does not retry; no retry after chunks yielded.

**Gap until PR 2:** Go can still ping-pong and demote on non-adjacent unclassified `provider_error`s. A classified 404 that exhausts Python retries already avoids the prompt blob (`max_retries` → `tryFallback` → `continue`).

### PR 2 — Go: monotonic fallback, reset-on-success, no error-in-prompt

- `fallback.go`: skip names in `AttemptedProviders`. Keep native-tool skip. Same attempted-name skip in `nextSummarizationFallback` (today only skips the walk’s start name via `inUseName`).
- `iterating.go`: `fbState.resetCounters()` after a successful LLM call.
- `iterating.go`: skip `buildRetryMessage` / observation append for no-partial errors; keep `EventTypeError` + `provider_fallback`. For `PartialText != ""`, append continue-from-here + truncated text only.
- Tests: ping-pong skipped; success then one `provider_error` does not fallback; two in a row still does; **timeline still has the error/fallback events**; messages after a 404 retry contain no `Publisher model` blob; partial retry still contains the truncated text.
- Touch ADR-0003: Python *does* retry 429/404/5xx (then `max_retries`); skip is “already attempted,” not only “same as current”; consecutive resets on success. ADR-0017 skip line: attempted set, not only current name.

e2e: not required in PR 1; optional in PR 2 if an existing fallback fixture can assert skip-already-tried. No new Testcontainers scenario unless a unit test cannot cover skip + conversation.

## Later (not v1)

- **Probe / unstick.** After N successful fallback turns (or a cooldown), try the original primary once. ADR Q1 assumed outages last longer than one execution; a short blip within one session shows the opposite. Easy to oscillate — design separately.
- **Process-wide circuit breaker.** If a primary 404s twice in ~60s, skip it for ~2 minutes on *all* executions in this replica so a second sub-agent does not rediscover the same blip. Needs shared in-process state + clock.
- **Do not stick on transient 404.** Only stick on `max_retries` after a long fail, or credentials. Weaker than a probe; still a behavior change to Q1.
