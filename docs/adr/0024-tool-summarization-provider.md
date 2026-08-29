# ADR-0024: Tool Summarization LLM Provider

**Status:** Implemented  
**Date:** 2026-08-21  
**Amended by:** [ADR-0027: Transient LLM Outage Handling](0027-llm-transient-outage.md) (2026-08-29) — summarization-local skip uses the attempted set, not current-name-only

## Overview

Large MCP tool results are summarized by an extra LLM call before they are appended to the investigating agent's conversation. That call previously always used the **calling agent's** provider and backend. Operators running an expensive investigation model therefore paid that model's rates to compress kubectl dumps, log files, and similar high-volume tool output.

This decision lets operators point tool summarization at a **named LLM provider** (typically a cheap, fast model) without changing investigation, synthesis, scoring, or chat models.

The Go gRPC client is already per-call: each generate request carries provider config and backend. No new LLM client, proto field, or Python path is required — only config, resolution, fallback local to summarization, and observability.

**Compatibility:**

- **Provider selection is opt-in.** Unset `defaults.summarization.llm_provider` (and no per-server overlay) keeps the calling agent's model.
- **On summarization LLM error**, TARSy walks the agent's `fallback_providers` locally before MCP fail-open / `search_past_sessions` fail-closed. An empty fallback list stays immediate fail-open / fail-closed.

## Design Principles

1. **Opt-in model, backward-compatible provider selection.** Unset means the calling agent's model.
2. **Reuse named providers.** Operators already define models in `llm-providers.yaml` (plus TARSy built-ins). Summarization references those names; it does not invent a parallel model catalog.
3. **Same code path, different generate input.** One summarization call site. It must not grow a second client or a second streaming stack.
4. **No tools on summarization calls.** Summarization is text-in / text-out. MCP tools stay empty. Provider-default Gemini native tools (`google_search`, `url_context`, `code_execution`) must be stripped so a Flash summarizer cannot start searching the web.
5. **Fail-open / fail-closed contracts stay after fallbacks are exhausted.** MCP summarization still falls back to the raw result; `search_past_sessions` still fail-closes. The agent's `fallback_providers` list is walked locally first (never mutating the investigator).
6. **Minimum hierarchy.** Defaults + optional per-MCP-server overlay. No chain/stage/agent level. Summarization is about the tool output, not which chain is running.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Config hierarchy | Defaults + per-MCP-server overlay; no chain/stage/agent. Only provider/backend inherit from defaults | One global line for the 90% case; per-server override for the 10% (e.g. a security MCP names Opus while kubectl inherits Flash). Enablement and size thresholds stay per-server so a global block cannot rewrite every server's 5k cutoff. Rejected: per-server only (copy-paste; cannot cover `search_past_sessions`); defaults only (forces a follow-up the first time one server needs a different model); full hierarchy (unused chain/stage complexity). |
| Q2 | Unset behavior | Calling agent's provider and backend | Zero behavior change for current deployments; explicit opt-in. Rejected: built-in `google-default` or Sonnet (surprise model swap; Claude-only shops would start needing a Google API key). |
| Q3 | `search_past_sessions` | Same resolver; `defaults.summarization` only (no per-server overlay) | One knob, one call site. There is no MCP server to overlay. Rejected: always the agent model (would make the global default lie for the built-in that justified having defaults); a separate YAML field (extra surface for a rare call). |
| Q4 | Summarization LLM failure | Reuse `fallback_providers` locally (skip current name, SingleShot, no native-tool skip, sticky per primary). Never the investigation fallback helper. Exhausted → existing fail-open / fail-closed. Own index; do not reuse the investigator's fallback state | Raw dumps after a cheap-model failure are often *more* expensive than retrying on the next list entry. The list already encodes family preference; no new YAML for v1. Investigation fallback mutates the investigator and must not run. Rejected: fail-open-only; one agent-model retry (Sandbox would skip Sonnet and jump to Opus); a dedicated summarization fallback list. |
| Q5 | Backend | Explicit `llm_backend`; default langchain when a named provider is set at that layer and backend is omitted. Never inherit the agent's backend. Always strip native tools | Same pair as scoring and stage agents. Inheriting `google-native` from a Gemini clone onto a Claude summarizer would be wrong. Rejected: always LangChain (blocks google-native for Flash); infer from provider type (magic). |
| Q6 | Pin one server back to the investigator | No sentinel. Per-server overlay names another provider | Resolver stays “last non-empty name.” “This MCP should use Opus” is `llm_provider: vertexai-claude-opus` on that server. Rejected: sentinel `agent` / `inherit` (magic name); `use_agent_model: true` (two ways to spell which model). |

## Architecture

### Current vs decided flow

```
MCP / built-in tool
  → mask
  → maybe summarize (MCP results over token threshold)
    or required summarization (e.g. search_past_sessions)
  → resolve summarization LLM (defaults, optional server overlay, agent)
  → if sticky entry for this primary is set, use that clone instead of the primary
  → clone provider (shallow copy; native tools cleared — do not mutate the registry)
  → generate { Config, Backend, Tools: empty, ExecutionID: execID + ":summarization" }
  → on LLM error (including empty summary): summarization-local walk of
       the agent's resolved fallback list, starting at index 0
       → skip entries whose name equals the provider currently in use
       → no RequiresNativeTools filter (see ADR-0017)
       → SingleShot thresholds (first error trips)
       → success: stick that provider for this primary for the rest of the execution
       → exhausted or empty list: MCP fail-open / session-search fail-closed
  → model_name, metrics, timeline metadata use the model that actually answered
```

**Amendment (ADR-0027):** the skip is no longer “name equals the provider currently in use.” Each summarization call seeds an attempted list with the start name and appends each hop; later candidates whose names are already on that list are skipped. Native-tool skip still does not apply. The investigator’s attempted list is still not shared.

```mermaid
flowchart TD
    tool[Tool result] --> decide{MCP over threshold<br/>or required summarization?}
    decide -->|no| raw[Pass raw result]
    decide -->|yes| resolve[Resolve summarization LLM]
    resolve --> sticky{Sticky entry for this primary?}
    sticky -->|yes| named[Sticky provider]
    sticky -->|no| named2[Resolved primary]
    named --> strip[Clone; native tools nil]
    named2 --> strip
    strip --> call[Summarization LLM call]
    call --> grpc[gRPC Generate]
    grpc -->|ok| wrap[Summary into conversation]
    grpc -->|fail| next{Next list entry whose name<br/>differs from current?}
    next -->|yes| strip
    next -->|no, MCP| failOpen[Use raw result]
    next -->|no, session search| failClosed[Error string to agent]
```

The mermaid above is the original skip (“differs from current”). [ADR-0027](0027-llm-transient-outage.md) changed that diamond to “not already attempted this summarization call” (start name is seeded; hops are appended). Sticky-per-primary and fail-open / fail-closed are unchanged.

On a later tool result with the same primary, start at the sticky provider (do not retry the failed primary). If the sticky provider then fails, continue **forward** in the list from there — do not walk back to the primary.

MCP enablement and size limits (`enabled`, `size_threshold_tokens`, `summary_max_token_limit`) stay on the per-server summarization block. They have no provider field of their own; provider resolution is a separate overlay on the same block.

`search_past_sessions` is a built-in memory tool, not an MCP server. It is the **only** non-MCP tool that invokes the summarization LLM (required summarization). Other built-ins either return small results or hit the MCP summarizer with a server ID that is not in the MCP registry, which is a no-op.

Tool calls on one execution run **sequentially**. Sticky failover needs no mutex in v1.

### Why a second client is unnecessary

Each generate request serializes its provider config into the proto on every call. The Python service constructs (or reuses a cached) chat model from that config. Switching summarization to a cheaper model is passing a different provider config on that one call. Resolution happens inside the summarizer, not by swapping the execution context's investigator config.

## Core Concepts

### Summarization provider

A **named** entry in the LLM provider registry (`google-default`, `vertexai-claude-sonnet`, …). Not a raw model string.

Set on `defaults.summarization` and optionally overlaid on `mcp_servers.*.summarization`. Last non-empty **provider name** wins: agent (implicit) → defaults → this MCP server. No chain/stage/agent level. No sentinel for “use the investigator”; a server that needs Opus names `vertexai-claude-opus`.

`search_past_sessions` has no MCP server, so it takes `defaults.summarization` only (or the calling agent when that is unset).

### Resolution result

A cloned provider config (native tools cleared), the provider name, and the backend. Lives on execution context so the summarizer can hold sticky state without an import cycle.

When no `llm_provider` is set at defaults or server, this is a **clone** of the calling agent's provider with native tools cleared. Backend in that unset case is the agent's backend. When a named summarization provider *is* set and `llm_backend` is omitted **at that layer**, backend is **langchain**, not the agent's backend and not a parent summarization layer's backend.

### Two call sites, one resolver

| Call site | Trigger | Provider |
|-----------|---------|----------|
| MCP summarization | MCP result over token threshold | defaults, then this server's overlay |
| Required summarization (`search_past_sessions`) | always when the tool returns matches | defaults only |

### Native-tool stripping (non-negotiable)

Built-in `google-default` enables `google_search` and `url_context` on the provider. Google-native tool conversion attaches those even when MCP tools are empty. LangChain ignores native tools; strip anyway so a `google-native` summarizer is safe.

Registry lookups often return the **shared registry pointer**. Clone with a shallow struct copy and clear native tools on the copy. Do **not** delete from the map in place — that would mutate the registry. Do this even when the resolved provider is the agent model, and again when walking fallback entries (those configs may have had agent native tools merged on).

The investigating agent's provider on the execution context must be left unchanged.

### Backend

| Situation | Backend |
|-----------|---------|
| No summarization `llm_provider` at defaults or this server | Calling agent's backend (Q2) |
| Named provider at a layer, `llm_backend` omitted **at that layer** | `langchain` |
| Named provider at a layer, `llm_backend` set at that layer | That value |
| Fallback list entry | The entry's already-resolved backend (not re-defaulted to langchain) |

Never inherit the investigating agent's backend onto a named summarization provider. A Gemini parallel clone must not force `google-native` onto a Claude summarizer, or the reverse.

Each named-provider layer fully specifies the pair. If the server overlay sets `llm_provider` and omits `llm_backend`, the backend is langchain even when `defaults.summarization` set `google-native` for a different (or the same) provider. Parent-layer backend does not carry forward.

### Resolver algorithm (normative)

Inputs: agent provider / name / backend, default summarization (may be absent), optional per-call server summarization (absent for `search_past_sessions`).

1. Primary = clone of the agent's provider (native tools cleared), agent's name and backend.
2. If defaults set `llm_provider`: look up that name; backend = defaults' `llm_backend` if set, else `langchain`; replace primary.
3. If this call has a server `llm_provider`: look up that name; backend = server's `llm_backend` if set, else `langchain`; replace primary.
4. Record the primary name **before** applying sticky (sticky is keyed by this name).
5. If execution context has a sticky entry for that primary name, use that clone (already stripped) instead of retrying a failed primary.
6. Return the clone. Never mutate registry entries or the investigator's provider.

Lookup failure at runtime is not expected (startup validation); treat it as an LLM error so fallback / fail-open still apply.

### Failure handling (local to summarization)

Do **not** use investigation provider fallback ([ADR-0003](0003-llm-provider-fallback.md)). That helper mutates the investigator's config, increments `LLMFallbacksTotal`, emits a `provider_fallback` timeline event, and writes `original_llm_provider` on the agent execution — any of which would switch or mis-label the investigator.

Instead, a summarization-local walk of the agent's resolved fallback list:

1. Own state, not the investigator's fallback index. Start at list index 0 on the first failure of a primary (the investigator may already have advanced its own index).
2. Reuse the same error classification as investigation fallback, with SingleShot thresholds (trip on the first error), like scoring. Empty summary counts as an error. If the call context is already cancelled, do not walk; return the error (MCP fail-open / session-search fail-closed as today).
3. Skip entries whose provider name equals the summarization provider **currently in use** (same skip-same-name rule as investigation fallback).

   **Amendment (ADR-0027):** skip names already attempted **this summarization call** (seed the start name, append each hop). Investigation fallback made the same change. Re-listing the primary still skips; earlier hops cannot be selected again. Still do not share the investigator’s attempted list.
4. Do **not** skip Claude entries for `RequiresNativeTools` ([ADR-0017](0017-native-tool-fallback-safety.md)). Summarization never needs Gemini native tools.
5. Use each entry's backend and a **fresh clone** of the entry config with native tools cleared.
6. Stick per **resolved primary name** for the rest of this execution: if `google-default` failed over to Sonnet, later calls whose primary is still `google-default` start on Sonnet. A later call whose primary is `vertexai-claude-opus` (per-server overlay) is independent and tries Opus first. If the sticky provider then fails, continue forward from the next list index; do not retry the original primary.
7. Empty fallback list: no walk; existing fail-open / fail-closed immediately.
8. After the list is exhausted: MCP **fail-open** (raw result); `search_past_sessions` **fail-closed** (error string).
9. Do **not** increment `LLMFallbacksTotal` (that counter is investigation fallback). Each summarization attempt (including failures) is still recorded under the provider that was called.
10. Observability for a summarization switch: structured log plus `summarization_fallback: true` on the successful MCP `mcp_tool_summary` metadata. No `provider_fallback` timeline event. No update of the agent execution's fallback record.

This applies even when summarization is still on the agent model (unset config): an Opus summary that fails walks the same list before dumping raw logs.

Example with `defaults.summarization.llm_provider: google-default` and a fallback list of Sonnet, Opus, `google-default`, Gemini Pro, Gemini Flash: Flash fails then **Sonnet → Opus → (skip google-default) → Gemini Pro → Gemini Flash**.

Originally `google-default` was skipped because it matched the name currently in use. After ADR-0027 it is skipped because the call seeded it on the attempted list (and would also skip any earlier hop if the list walked back).

### Google-native content cache

Google-native keys model-content (thought signatures) by generate execution ID. Summarization is a one-shot `[system, user]` conversation; it must not append turns into the investigator's cache or replay investigation turns as if they were the summary prompt.

Set the generate execution ID to `executionID + ":summarization"` on every summarization call (including the unset-agent-model path). DB / timeline / LLM interaction rows keep using the unsuffixed execution ID. If summarization itself falls over to another google-native provider, clear cache on that retry — it clears only the `:summarization` key, not the investigator's cache.

This also fixes a pre-existing bug: Gemini clones that already summarize on `google-native` with the same execution ID pollute thought-signature replay.

### Streaming on retry

- **MCP (new timeline event):** a failed attempt that streamed may leave a failed `mcp_tool_summary` row. The successful retry creates a new event. Acceptable for v1.
- **Required summarization (existing tool-call event):** only the **first** attempt streams into the tool-call card. Retries collect silently. Completing the tool-call event writes the winning summary (or the fail-closed error string) as the event content, replacing any partial first-attempt text.

v1 records an LLM interaction only for the successful summary. Failed attempts that then fallback are visible in Prometheus and logs, not in `llm_interactions`.

### Runtime wiring

Execution context gains the LLM provider registry, default summarization config (may be absent), and a sticky map keyed by resolved primary provider name. Do not pass the entire process config on execution context.

Thread registry and defaults onto every context that **executes MCP or memory tools**: investigation / action / orchestrator workers, chat, and sub-agent runs. Scoring, executive summary, and the feedback reflector do not call the summarizer; leave the new fields unset there.

Each sub-agent execution has its own sticky map. The summarizer uses the resolved (possibly sticky) value for generate input, metrics, interaction `model_name`, and timeline metadata `summarization_model` / `summarization_provider`.

Thinking / adaptive thinking is **not** a new proto flag. Claude on LangChain always enables adaptive thinking. Operators who want a cheap compressor should pick a non-Claude provider.

## Configuration

```yaml
# tarsy.yaml (fragment)
defaults:
  llm_provider: "vertexai-claude-opus"   # investigations unchanged
  fallback_providers:
    - {provider: "vertexai-claude-sonnet", backend: "langchain"}
    - {provider: "vertexai-claude-opus", backend: "langchain"}
    - {provider: "google-default", backend: "google-native"}
  summarization:
    llm_provider: "google-default"
    llm_backend: "google-native"         # optional; omit → langchain

mcp_servers:
  kubernetes-server:
    summarization:
      enabled: true
      size_threshold_tokens: 5000
      summary_max_token_limit: 1000
      # llm_provider omitted → inherit defaults.summarization
  # Optional overlay: this server's dumps use Opus, then the shared fallback list
  # devsandbox-mcp-server:
  #   summarization:
  #     llm_provider: "vertexai-claude-opus"
  #     llm_backend: "langchain"
```

The same summarization block is used on `defaults.summarization` and `mcp_servers.*.summarization` (Q1). Optional `llm_provider` / `llm_backend` sit next to the existing size knobs (`enabled`, `size_threshold_tokens`, `summary_max_token_limit`).

MCP user overlays still **replace** the whole server config (existing merge); a kubernetes-server overlay that sets `summarization.llm_provider` must keep or restate enablement / thresholds as it does today. The loader already fills `size_threshold_tokens == 0` to 5000 on enabled MCP servers before validation; that filler does **not** apply to `defaults.summarization`.

`defaults.summarization` inherits **only** `llm_provider` / `llm_backend`. Enablement and size thresholds stay per-server. A global block must not rewrite every server's 5k cutoff.

### Validation

- If `llm_provider` is set, it must exist in the LLM provider registry (same pattern as `defaults.scoring.llm_provider`).
- If `llm_backend` is set, it must be a valid backend (empty is omitted, not invalid).
- `llm_backend` without `llm_provider` at the same level is a validation error.
- On `defaults.summarization`, `enabled`, `size_threshold_tokens`, and `summary_max_token_limit` must be unset. Those fields are per-server only; setting them on defaults is a validation error so they cannot silently do nothing.
- Referenced summarization providers (`defaults.summarization.llm_provider` **and** every `mcp_servers.*.summarization.llm_provider`) are included in referenced-provider collection so missing API keys fail at config load.
- `llm_provider` on a server whose summarization is explicitly `enabled: false` is a validation error (dead config). This check must run even when size-threshold validation is skipped for disabled servers.

### Config viewer

MCP server views already serialize the summarization block; new fields appear automatically. Defaults view adds a summarization object ([ADR-0019](0019-config-viewer.md)). No schema migration.

## Observability

| Surface | Change |
|---------|--------|
| `llm_interactions.model_name` | Model that actually answered (successful attempt only, as today) |
| Prometheus LLM call metric | Each attempt's provider name + model (including failures) |
| `LLMFallbacksTotal` | **Unchanged** — investigation only ([ADR-0003](0003-llm-provider-fallback.md)) |
| `mcp_tool_summary` metadata | `summarization_model` already exists; add `summarization_provider`; set both from the answering model; set `summarization_fallback: true` when the answerer is not the resolved primary |
| Trace view | `interaction_type=summarization` already; cheaper model shows naturally |
| Dashboard tool-summary / session history | Header stays “TOOL RESULT SUMMARY”. Caption shows the answering `summarization_model` on MCP tool summaries and on `search_past_sessions` session-history cards (absent on older events) |
| Session cost ([ADR-0020](0020-session-usage-cost.md)) | Already sums summarization interactions; `model_name` is enough for the catalog |
| `provider_fallback` on the **agent execution** | Must **not** fire for summarization |

No schema migration.

## What this is not

- Not a general “job type → model” framework. Scoring and exec summary already have their own provider fields. Memory reflector still follows scoring.
- Not conversation / history compaction. Only tool-result summarization.
- Not changing the 5k token threshold or the 100k truncation cap.
- Not investigation provider fallback. Summarization must not use that helper.
- Not a dedicated summarization fallback list (Q4 rejected that for v1).

## Out of scope / future

- Failed summarization attempts that later succeed via fallback are not stored as `llm_interactions` rows.
- No dedicated summarization fallback list; operators who want a different order must reorder investigation `fallback_providers`.
- No sentinel to pin one MCP server back to “whoever is investigating.”
- A partial-then-failed MCP summary may leave an extra failed `mcp_tool_summary` row.

## Amendments ([ADR-0027](0027-llm-transient-outage.md), 2026-08-29)

Q4 originally reused investigation’s skip-current-name rule for the summarization-local walk. ADR-0027 changed that sibling skip to **already-attempted this call** (seed start name, append hops). Unchanged: own state vs investigator; SingleShot thresholds; no native-tool skip; sticky per primary; no `provider_fallback` event; fail-open / fail-closed after the list is exhausted.

## References

- [ADR-0003: LLM Provider Fallback](0003-llm-provider-fallback.md)
- [ADR-0014: Investigation Memory](0014-investigation-memory.md) (`search_past_sessions` required summarization)
- [ADR-0017: Native Tool Fallback Safety](0017-native-tool-fallback-safety.md)
- [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md)
- [ADR-0020: Session Usage Cost](0020-session-usage-cost.md)
- [ADR-0027: Transient LLM Outage Handling](0027-llm-transient-outage.md) — summarization-local skip is attempted-set, not current-name-only
- [Architecture overview](../architecture-overview.md)
- [Config README](../../deploy/config/README.md)
