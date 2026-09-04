# ADR-0003: LLM Provider Fallback

**Status:** Implemented  
**Date:** 2026-03-03  
**Amended by:** [ADR-0027: Transient LLM Outage Handling](0027-llm-transient-outage.md) (2026-08-29), [ADR-0030: Named Fallback Lists](0030-named-fallback-lists.md) (2026-09-03)

## Overview

When a primary LLM provider fails during an agent execution (server errors, timeouts, empty responses), TARSy currently retries 3 times at the Python level and then either retries via the Go iteration loop or marks the execution as failed/timed-out. This wastes the entire session when the provider is experiencing a sustained outage.

This feature adds **automatic fallback to alternative LLM providers** when the current provider is failing, **adaptive streaming-aware timeouts** to detect failures faster, and **observability** so operators can see when and why providers were switched.

## Design Principles

1. **Existing retry logic remains the first line of defense.** Python-level retries (3 attempts with exponential backoff) handle transient errors. Fallback only triggers after those retries are exhausted and the Go-level error propagates.
2. **Each fallback entry is self-contained.** Provider is required. Omitted backend defaults to langchain (not inferred from provider type, not inherited from `defaults.llm_backend`). The resolved pair is used as-is for general compatibility — there is no mapping from provider type. Native-tool-incompatible entries are still skipped at runtime ([ADR-0017](0017-native-tool-fallback-safety.md)); startup warns when a native-tool agent has no `google-native` fallback.
3. **Operator preference is respected.** The fallback list order represents cost/quality preference. The system does not re-rank providers automatically.
4. **Minimal blast radius.** The fallback mechanism integrates at the iteration level in the Go controller, not in the Python LLM service. This keeps the Python service stateless and provider-agnostic.
5. **Observable by default.** Every fallback event is recorded in the timeline, on the execution record, and surfaced in the dashboard without additional configuration.

## Architecture

### Where Fallback Lives

Fallback operates at the **Go iterating controller**, at the point where an LLM call fails and the controller decides what to do next. This is the natural place because:

- The controller already handles LLM errors and iteration-level retry logic
- It has access to execution context with full config
- It can swap the provider/backend for subsequent calls within the same execution
- The Python LLM service stays stateless — it serves whatever provider/backend the Go client sends

### Call Flow with Fallback

```
Iteration N: LLM call fails (after Python retries exhausted)
    │
    ├─ Parent context cancelled? → Return immediately (session expired)
    │
    ├─ Loop detection error? → Not a provider issue, no fallback
    │
    └─ Evaluate error code against trigger rules:
         │
         ├─ max_retries / credentials → Immediate fallback trigger
         │
         ├─ provider_error / invalid_request / partial_stream_error
         │    → Increment consecutive counter; trigger after 2 consecutive
         │      failures (1 Go retry on the same provider first)
         │
         └─ Fallback triggered?
              │
              ├─ Fallback providers available?
              │    │
              │    ├─ YES → Select next fallback provider
              │    │         Record fallback timeline event
              │    │         Update execution metadata
              │    │         Swap provider in execution config
              │    │         Continue iteration loop with new provider
              │    │
              │    └─ NO → Record failure, continue as today
              │
              └─ NO trigger → Retry iteration with same provider
```

This is the original Go-side flow. Later tightening of Python retries, consecutive counters, candidate skip, and same-provider retry is in the Amendments section below.

**Fallback scope:** Fallback sticks for the rest of the execution. Each new execution (stage, sub-agent) starts fresh with the primary provider via config resolution.

**Cache invalidation:** When the provider changes mid-execution, the Go client signals the LLM service to clear any per-execution model content cache so the new model reconstructs conversation history correctly. Stateless backends are unaffected.

### Adaptive Timeouts

The prior flat long timeout wastes time when a provider is completely down (no response). The adaptive timeout system uses three tiers:

```
LLM call starts
    │
    ├─ Phase 1: Initial Response Timeout (default: 120s)
    │   No chunks received yet. If this expires → cancel, treat as retryable.
    │
    ├─ Phase 2: Stall Timeout (default: 60s between chunks)
    │   Streaming started but stalled. If no new chunk within stall window → cancel.
    │
    └─ Phase 3: Maximum Call Timeout (default: 5m)
        Overall ceiling. Even active streaming gets cut off here.
```

Adaptive timeouts are applied in the Go streaming collector, which already processes every chunk. Python's existing static timeout stays as a safety net — no behavioral dependency on changing Python for the tiered logic.

### Configuration Structure

Fallback providers are configured as an ordered list at the defaults level and overridable per chain/stage/agent, following the existing config resolution hierarchy:

```yaml
defaults:
  llm_provider: "gemini-3-flash"
  llm_backend: "google-native"
  fallback_providers:
    - provider: "gemini-2.5-pro"
      backend: "google-native"
    - provider: "anthropic-vertex"  # omitted backend → langchain

chains:
  my-chain:
    fallback_providers:
      - provider: "gemini-2.5-flash"
        backend: "google-native"
```

Omitted `backend` defaults to langchain. `google-native` must be written explicitly. There is no mapping from provider type.

### Fallback State Tracking

**FallbackState** (conceptual) tracks, for the current execution: the original provider and backend; which list index is active (primary vs fallback slot); which providers were attempted (for observability); the last error that triggered fallback; and consecutive error counters used for threshold-based triggers.

This state lives in the controller's iteration loop and drives selecting the next fallback entry and recording the attempt chain.

### Observability

When a fallback occurs, the system records:

1. **Timeline event** (`provider_fallback` type):
   - `original_provider`, `fallback_provider`, `reason`, `timestamp`
   - Visible in the conversation timeline in the dashboard

2. **Execution record update**:
   - New fields: `original_llm_provider`, `original_llm_backend` (nullable, only set on fallback)
   - Existing `llm_provider` and `llm_backend` updated to reflect current provider

3. **LLM interaction records**:
   - Each `llm_interaction` already has `model_name` — this naturally captures per-call provider info

Two new nullable columns on agent executions: `original_llm_provider` and `original_llm_backend`. Only set when fallback occurs. Existing `llm_provider`/`llm_backend` reflect the active provider. Timeline events provide the full attempt chain.

## Core Concepts

### Fallback Provider Entry

An entry in the fallback list: required provider name plus optional backend. The provider name references a registered LLM provider config. The backend specifies which SDK path to use when this provider is active; omitted backend is langchain.

### Backend Switching

Each fallback entry specifies a provider and optionally a backend. When fallback triggers, the system switches to both — including changing the backend if the fallback entry uses a different one (e.g., native vs LangChain). Startup does not verify that the provider works with that backend (Claude on `google-native` is not rejected). Native-tool agents skip non-`google-native` entries at runtime and get a startup warning if the list has none ([ADR-0017](0017-native-tool-fallback-safety.md)).

### Same-Provider Skip

When selecting the next fallback entry, the original logic skipped entries whose provider name matches the currently active provider (regardless of backend). This prevented wasting iterations by "falling back" to the same model that is already failing.

**Amendment (ADR-0027):** skip is now any name already on the **attempted** list for this execution (the primary starts on that list), not only the current name. Walking back to a provider this execution already left is no longer allowed. Native-tool incompatible skip is unchanged — see [ADR-0017](0017-native-tool-fallback-safety.md).

### Fallback Trigger Conditions

Fallback triggers depend on the error code from the Python LLM service, since each code carries different retry history:

| Error Code | Python Retried? | Fallback Trigger |
|---|---|---|
| `max_retries` | Yes (3x) | Immediate |
| `credentials` | No | Immediate (guaranteed failure) |
| `provider_error` | No | After 1 Go retry (2 consecutive failures) |
| `invalid_request` | No | After 1 Go retry (2 consecutive failures) |
| `partial_stream_error` | No | After 1 Go retry (2 consecutive failures) |

In all cases, fallback also requires:
- The parent context is not cancelled/expired
- At least one untried fallback provider remains

Fallback is NOT triggered when:
- The error is a loop detection (not a provider issue)
- All fallback providers have been tried
- The parent context is done (session expired)

### Provider Credential Validation

At startup, the system validates each fallback provider entry:

1. **Provider exists** — the referenced provider name is registered
2. **Backend is valid** — the backend value is a known enum (omitted defaults to langchain)
3. **Credentials are set** — the required environment variable for that provider is present and non-empty

Startup fails if any of those checks fail — a fallback list with broken entries gives a false sense of safety. Provider/backend pairing is not validated. Native-tool agents with no `google-native` entry in the effective list get a warning only ([ADR-0017](0017-native-tool-fallback-safety.md)).

## Decisions Summary

| # | Question | Decision | Rationale |
|---|---|---|---|
| Q1 | Fallback scope | Stick with fallback for rest of execution; new executions reset to primary | Provider outages last longer than a single execution. New executions naturally reset via config resolution. Avoids oscillation. |
| Q2 | Where adaptive timeouts live | Go streaming path; Python's long timeout stays as safety net | Go already processes every chunk in real-time. Single implementation for tiered behavior. |
| Q3 | Backend specification | Omitted backend defaults to langchain; google-native must be explicit. No mapping from provider type | Same default as named-provider `llm_backend` layers. Inferring google-native from Gemini providers would silently break Claude/Vertex fallbacks. |
| Q4 | Credential validation timing | Validate at startup; fail if any fallback provider has missing credentials | A broken fallback is worse than no fallback — false sense of security. Catch misconfigs at deploy time. |
| Q5 | Fallback metadata storage | Two new nullable columns: `original_llm_provider`, `original_llm_backend` | Directly queryable (`WHERE original_llm_provider IS NOT NULL`). Timeline events provide full audit trail. |
| Q6 | Fallback scope across controllers | All controllers get fallback (iterating, forced conclusion, single-shot) | Whole session should survive an outage, not just the iteration loop. Scoring/synthesis would otherwise hit the same broken primary. |
| Q7 | Failure threshold | Error-code-aware: immediate for `max_retries`/`credentials`, 2 consecutive for `provider_error`/`invalid_request`/`partial_stream_error` | Respects Python's retry history per error type. `max_retries` already exhausted 3 attempts; `credentials` is guaranteed failure; others get one Go retry since Python didn't retry. |
| Q8 | Adaptive timeout defaults | 120s initial, 60s stall, 5m max | Conservative to avoid false-positives with thinking models on heavy context. 120s initial still saves time vs a single flat long timeout on dead providers. |

## Amendments ([ADR-0027](0027-llm-transient-outage.md), 2026-08-29)

These original decisions still stand. ADR-0027 changed *how* they behave under short provider blips:

- **Python retry coverage (principle 1 / Q7).** The 3-attempt Python loop originally treated as retryable only timeout, empty response, and google-native 5xx. Intermittent Vertex `404` arrived as `provider_error` with no identical HTTP retry. ADR-0027 retries **429 / 404 / 5xx** (no chunks yet) in Python; exhaustion yields `max_retries` so Go still falls back immediately per Q7. Cache `400` degrade is unchanged ([ADR-0026](0026-prompt-caching.md)).
- **Consecutive counters (Q7).** “2 consecutive” originally counted toward fallback without resetting on a successful Generate (only after a provider *switch*). ADR-0027 resets those counters after a successful iterating LLM call.
- **Candidate skip (Same-Provider Skip).** Originally current-name only, so a later list entry with the primary’s name could be selected again. ADR-0027 skips any already-attempted name.
- **Same-provider Go retry.** Originally appended the full provider-error text into the next model prompt. ADR-0027 keeps `error` / `provider_fallback` timeline events and stops injecting no-partial error JSON into the conversation.
- **Q1 (sticky for the rest of the execution) is not changed.** Probe/unstick remains out of scope.

## Amendment (2026-09-01)

**Q3 / backend specification.** Omitted `fallback_providers[].backend` defaults to `langchain`. `google-native` remains explicit. Backend is still not inferred from provider type and does not inherit `defaults.llm_backend`.

The same pairing applies to investigation, chat, scoring, compose, and executive-summary named-provider layers: a YAML node that sets `llm_provider` without a sibling backend resolves backend to `langchain` and does not keep a parent `llm_backend`. `google-native` paired with a non-google provider at the same node is a load-time error.

## Amendment ([ADR-0030](0030-named-fallback-lists.md), 2026-09-03)

**How the list is chosen.** Inline `fallback_providers` remains the runtime entry shape and the deprecated YAML spelling on defaults / chain / stage / stage-agent. Operators now bind executions via a named catalog (`fallback_lists` + `fallback_list`). Runtime walk (triggers, stickiness, skip) is unchanged.

**Compose / exec-summary backend.** Those jobs use nested `compose` / `executive_summary` blocks with sibling `llm_backend`. Omitted still means langchain. Deprecated `compose_backend` / `executive_summary_backend` aliases still load.
