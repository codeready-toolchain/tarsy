# LLM Provider Fallback — Design Document

**Status:** Final — all decisions resolved in [llm-provider-fallback-questions.md](llm-provider-fallback-questions.md)

## Overview

When a primary LLM provider fails during an agent execution (server errors, timeouts, empty responses), TARSy currently retries 3 times at the Python level and then either retries via the Go iteration loop or marks the execution as failed/timed-out. This wastes the entire session when the provider is experiencing a sustained outage.

This feature adds **automatic fallback to alternative LLM providers** when the current provider is failing, **adaptive streaming-aware timeouts** to detect failures faster, and **observability** so operators can see when and why providers were switched.

## Design Principles

1. **Existing retry logic remains the first line of defense.** Python-level retries (3 attempts with exponential backoff) handle transient errors. Fallback only triggers after those retries are exhausted and the Go-level error propagates.
2. **Each fallback entry is self-contained.** Each entry specifies both provider and backend explicitly. The system uses them as-is — no runtime compatibility filtering. Invalid combinations are caught at startup.
3. **Operator preference is respected.** The fallback list order represents cost/quality preference. The system does not re-rank providers automatically.
4. **Minimal blast radius.** The fallback mechanism integrates at the iteration level in the Go controller, not in the Python LLM service. This keeps the Python service stateless and provider-agnostic.
5. **Observable by default.** Every fallback event is recorded in the timeline, on the execution record, and surfaced in the dashboard without additional configuration.

## Architecture

### Where Fallback Lives

Fallback operates at the **Go controller level** (`pkg/agent/controller/iterating.go`), specifically at the point where an LLM call fails and the controller decides what to do next. This is the natural place because:

- The controller already handles LLM errors and iteration-level retry logic
- It has access to `ExecutionContext` with full config
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
              │    │         Swap provider in execCtx.Config
              │    │         Continue iteration loop with new provider
              │    │
              │    └─ NO → Record failure, continue as today
              │
              └─ NO trigger → Retry iteration with same provider
```

**Decision (Q1):** Fallback sticks for the rest of the execution. Each new execution (stage, sub-agent) starts fresh with the primary provider via `ResolveAgentConfig`.

**Cache invalidation:** When the provider changes mid-execution, the Go controller sends a `clear_cache` flag on `GenerateRequest`. The Google Native provider's `_model_contents` cache (which stores raw Gemini `Content` objects with `thought_signatures` per `execution_id`) must be cleared so the new model reconstructs conversation history from proto fields instead of replaying the old model's cached objects. The LangChain provider is stateless and unaffected.

### Adaptive Timeouts

The current flat 5-minute timeout wastes significant time when a provider is completely down (no response). The adaptive timeout system uses three tiers:

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

**Decision (Q2):** Adaptive timeouts are implemented in Go's `collectStreamWithCallback`, which already processes every chunk. Python's existing 300s timeout stays as a static safety net — no changes needed on the Python side.

### Configuration Structure

Fallback providers are configured as an ordered list at the defaults level and overridable per chain/stage/agent, following the existing config resolution hierarchy:

```yaml
defaults:
  llm_provider: "gemini-3-flash"
  llm_backend: "google-native"
  fallback_providers:
    - provider: "gemini-2.5-pro"
      backend: "google-native"
    - provider: "anthropic-vertex"
      backend: "langchain"

chains:
  my-chain:
    # Chain-level override
    fallback_providers:
      - provider: "gemini-2.5-flash"
        backend: "google-native"
```

**Decision (Q3):** Each fallback entry explicitly specifies its backend. No implicit mapping — future-proof as new backends are added.

### Fallback State Tracking

A new `FallbackState` struct tracks fallback progress within an execution:

```go
type FallbackState struct {
    OriginalProvider        string
    OriginalBackend         config.LLMBackend
    CurrentProviderIndex    int      // -1 = primary, 0+ = fallback list index
    AttemptedProviders      []string // For observability
    FallbackReason          string   // Last error that triggered fallback
    ConsecutiveProviderErrors int     // Counts consecutive provider_error/invalid_request/transport (threshold: 2)
    ConsecutivePartialErrors int     // Counts consecutive partial_stream_error (threshold: 2)
}
```

This state is maintained in the controller's iteration loop and used to:
- Select the next fallback provider from the list
- Record which providers were attempted

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

**Decision (Q5):** Two new nullable columns on `agent_executions`: `original_llm_provider` and `original_llm_backend`. Only set when fallback occurs. Existing `llm_provider`/`llm_backend` updated to the fallback provider. Timeline events provide the full attempt chain.

## Core Concepts

### Fallback Provider Entry

An entry in the fallback list: `{provider: string, backend: LLMBackend}`. The provider name references a registered `LLMProviderConfig`. The backend specifies which SDK path to use when this provider is active.

### Backend Switching

Each fallback entry specifies both a provider and a backend. When fallback triggers, the system switches to both — including changing the backend if the fallback entry uses a different one (e.g., `google-native` → `langchain`). If a provider/backend combination doesn't work, that's a configuration error caught at startup (Q4).

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
1. **Provider exists** — the referenced provider name is registered in `LLMProviderRegistry`
2. **Backend is valid** — the backend value is a known `LLMBackend` enum
3. **Credentials are set** — the required environment variable (`api_key_env` or `credentials_env`) is present and non-empty

Startup fails if any check fails — a fallback list with broken entries gives a false sense of safety.

**Decision (Q4):** Validate at startup — fail if any fallback provider has missing credentials. A broken fallback is worse than no fallback.

## Implementation Plan

### Phase 1: Configuration & Schema (P1) - ✅ DONE

**Goal:** Define the fallback configuration structure, schema changes, and startup validation. Everything downstream depends on this.

Changes:
- `pkg/config/types.go` — Define `FallbackProviderEntry` struct
- `pkg/config/defaults.go` — Add `FallbackProviders` field to `Defaults`
- `pkg/config/chain.go` — Add `FallbackProviders` to `ChainConfig`
- `pkg/config/types.go` — Add `FallbackProviders` to `StageAgentConfig`
- `pkg/agent/context.go` — Add `FallbackProviders` to `ResolvedAgentConfig`; add adaptive timeout config fields (`InitialResponseTimeout`, `StallTimeout`)
- `pkg/agent/config_resolver.go` — Resolve fallback list through hierarchy (defaults → chain → stage → agent); resolve for synthesis/scoring/executive summary (inherits from chain/defaults); set adaptive timeout defaults
- `pkg/config/validator.go` — Validate fallback entries at startup (provider exists, backend valid, credentials set)
- `ent/schema/timelineevent.go` — Add `provider_fallback` event type
- `ent/schema/agentexecution.go` — Add `original_llm_provider`, `original_llm_backend` fields (nullable)
- Database migration for new fields
- `proto/llm_service.proto` — Add `clear_cache` flag to `GenerateRequest`

Tests:
- `pkg/config/validator_test.go` — Startup validation: missing provider, invalid backend, missing credentials
- `pkg/agent/config_resolver_test.go` — Fallback list resolution through hierarchy

### Phase 2: Core Fallback Logic (P2) - ✅ DONE

**Goal:** When a provider fails, automatically switch to the next fallback provider based on error-code-aware trigger rules (Q7). All LLM call sites get fallback (Q6).

Changes:
- `pkg/agent/controller/fallback.go` — New file: `FallbackState`, provider selection, shared `callLLMWithFallback` helper, error-code-aware trigger logic
- `pkg/agent/controller/iterating.go` — Integrate fallback after LLM error in the iteration loop and forced conclusion
- `pkg/agent/controller/single_shot.go` — Add fallback wrapper for synthesis/scoring calls
- `pkg/queue/executor_synthesis.go` — Add fallback to `generateExecutiveSummary` (uses direct LLM call with `chain.ExecutiveSummaryProvider`, not the single-shot controller)
- `pkg/agent/llm_grpc.go` — Pass `clear_cache` flag on `GenerateRequest` when provider has changed
- `llm-service/llm/servicer.py` — Route `clear_cache` flag through to the provider
- `llm-service/llm/providers/google_native.py` — Handle `clear_cache`: delete `_model_contents[execution_id]` when set
- `pkg/services/stage_service.go` — Method to update `llm_provider`, `llm_backend`, `original_llm_provider`, `original_llm_backend` on fallback
- `pkg/events/payloads.go` — New `ProviderFallbackPayload` for WebSocket events

Tests:
- `pkg/agent/controller/fallback_test.go` — Error-code-aware triggers, provider selection, state tracking, consecutive error counters
- `pkg/agent/controller/iterating_test.go` — Fallback integration in iteration loop (mock LLM errors → verify provider switch)
- `pkg/agent/controller/single_shot_test.go` — Fallback for single-shot calls
- `llm-service/tests/test_google_native.py` — `clear_cache` flag clears `_model_contents`

### Phase 3: Adaptive Timeouts (P3) - ✅ DONE

**Goal:** Reduce time wasted on unresponsive providers. Independent of fallback but makes it more effective.

Changes:
- `pkg/agent/controller/streaming.go` — Implement initial-response and stall timeouts in `collectStreamWithCallback` (track time-to-first-chunk and time-since-last-chunk; cancel context when thresholds exceeded)

Tests:
- `pkg/agent/controller/streaming_test.go` — Initial response timeout (no chunks → cancel), stall timeout (gap between chunks → cancel), active streaming within max timeout (no cancel)

### Phase 4: Dashboard Visibility (P4)

**Goal:** Operators can see fallback events and provider switches in the dashboard.

Changes:
- `web/dashboard/src/components/timeline/StageContent.tsx` — Render `provider_fallback` timeline event with fallback indicator (original → fallback provider, reason)
- `web/dashboard/src/components/trace/StageAccordion.tsx` — Show original vs. fallback provider when `original_llm_provider` is set
- `web/dashboard/src/components/trace/ParallelExecutionTabs.tsx` — Same fallback indicator for parallel executions
- `web/dashboard/src/components/trace/SubAgentTabs.tsx` — Same for sub-agents

## Decisions Summary

All decisions resolved — see [llm-provider-fallback-questions.md](llm-provider-fallback-questions.md) for full discussion:

1. **Q1** — Stick with fallback provider for rest of execution; new executions reset to primary
2. **Q2** — Adaptive timeouts in Go controller; Python's 300s timeout stays as safety net
3. **Q3** — Explicit backend per fallback entry; no implicit mapping
4. **Q4** — Validate fallback credentials at startup; fail if any are missing
5. **Q5** — Two new nullable columns: `original_llm_provider`, `original_llm_backend`
6. **Q6** — All controllers get fallback (iterating, forced conclusion, single-shot)
7. **Q7** — Error-code-aware triggers: immediate for `max_retries`/`credentials`, 2 consecutive failures for `provider_error`/`invalid_request`/`partial_stream_error`
8. **Q8** — Conservative defaults: 120s initial, 60s stall, 5m max
