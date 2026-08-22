# Tool Summarization LLM Provider

**Status:** Final
**Questions:** [tool-summarization-provider-questions.md](tool-summarization-provider-questions.md) — all decisions recorded

## Overview

Large MCP tool results are summarized by an extra LLM call before they are appended to the investigating agent's conversation. Today that call always uses the **calling agent's** provider and backend (`execCtx.Config.LLMProvider` / `LLMBackend`). Operators running Opus 5 (or any expensive model) on investigations therefore pay Opus rates to compress kubectl dumps, log files, and similar high-volume tool output.

This feature lets operators point tool summarization at a **named LLM provider** (typically a cheap, fast model such as Gemini 3.7 Flash) without changing the investigation, synthesis, scoring, or chat models.

The Go gRPC client is already per-call: `GenerateInput` carries `Config` and `Backend`. No new LLM client, proto field, or Python path is required for the happy path — only config, resolution, fallback-local-to-summarization, and observability.

**Compatibility split:**

- **Provider selection is opt-in.** Unset `defaults.summarization.llm_provider` (and no per-server overlay) keeps today's model: the calling agent.
- **Phase 1 keeps today's failure path.** On summarization LLM error, MCP still fail-opens to the raw result and `search_past_sessions` still fail-closes — no `fallback_providers` walk. Walking that list locally before fail-open / fail-closed is **PR 2** (Q4). Deployments with an empty fallback list stay unchanged after PR 2 as well.

## Design Principles

1. **Opt-in model, backward-compatible provider selection.** Unset means the calling agent's model. See the compatibility split above for fallback.
2. **Reuse named providers.** Operators already define models in `llm-providers.yaml` (plus TARSy built-ins). Summarization references those names; it does not invent a parallel model catalog.
3. **Same code path, different `GenerateInput`.** `callSummarizationLLM` stays the single call site. It must not grow a second client or a second streaming stack.
4. **No tools on summarization calls.** Summarization is text-in / text-out. MCP tools stay `nil`. Provider-default Gemini native tools (`google_search`, `url_context`, `code_execution`) must be stripped so a Flash summarizer cannot start searching the web.
5. **Fail-open / fail-closed contracts stay after fallbacks are exhausted.** MCP summarization still falls back to the raw result; `search_past_sessions` still fail-closes. Phase 1 uses that path immediately. PR 2 walks the agent's `fallback_providers` list locally first (never mutating the investigator).
6. **Minimum hierarchy.** Defaults + optional per-MCP-server overlay. No chain/stage/agent level. Summarization is about the tool output, not which chain is running.

## Architecture / How It Works

### Current flow

```
MCP / built-in tool
  → mask
  → maybeSummarize  (MCP results over token threshold)
    or callSummarizationLLM  (RequiredSummarization, e.g. search_past_sessions)
  → GenerateInput{
        Config:  execCtx.Config.LLMProvider,   // agent's model
        Backend: execCtx.Config.LLMBackend,
        Tools:   nil,
    }
  → GRPCLLMClient.Generate  (already provider-agnostic)
  → LLMInteraction{type: summarization, model_name: agent model}
  → mcp_tool_summary timeline event (MCP path only)
```

`maybeSummarize` reads `MCPServerConfig.Summarization` for **enablement and size limits only** (`enabled`, `size_threshold_tokens`, `summary_max_token_limit`). It has no provider field.

`search_past_sessions` is a built-in memory tool, not an MCP server. It is currently the **only** non-MCP tool that invokes the summarization LLM (`RequiredSummarization`). Other built-ins (`load_skill`, `recall_past_investigations`, orchestration tools, Gemini native tools) either return small results or hit `maybeSummarize` with a server ID that is not in the MCP registry, which is a no-op.

Tool calls in `IteratingController` run **sequentially**. Sticky failover needs no mutex in v1.

### Proposed flow

```
MCP / built-in tool
  → mask
  → resolveSummarizationLLM(defaults, serverSummarization?, agent)
  → if SummarizationSticky[primaryName] is set, use that clone instead of the primary
  → clone provider (shallow copy; NativeTools = nil — do not mutate the registry map)
  → GenerateInput{ Config, Backend, Tools: nil, ExecutionID: execID + ":summarization" }
  → on LLM error (including empty summary): summarization-local walk of
       ResolvedFallbackProviders, starting at index 0 (not the investigator's FallbackState)
       → skip entries whose name equals the provider currently in use
       → no RequiresNativeTools filter
       → SingleShot thresholds (first error trips)
       → success: stick that provider for this primary for the rest of the execution
       → exhausted or empty list: MCP fail-open / session-search fail-closed
  → model_name, metrics, timeline metadata use the model that actually answered
```

```mermaid
flowchart TD
    tool[Tool result] --> decide{MCP over threshold<br/>or RequiredSummarization?}
    decide -->|no| raw[Pass raw result]
    decide -->|yes| resolve[resolveSummarizationLLM]
    resolve --> sticky{Sticky entry for this primary?}
    sticky -->|yes| named[Sticky provider]
    sticky -->|no| named2[Resolved primary]
    named --> strip[Clone; NativeTools nil]
    named2 --> strip
    strip --> call[callSummarizationLLM]
    call --> grpc[GRPCLLMClient.Generate]
    grpc -->|ok| wrap[Summary into conversation]
    grpc -->|fail| next{Next list entry whose name<br/>differs from current?}
    next -->|yes| strip
    next -->|no, MCP| failOpen[Use raw result]
    next -->|no, session search| failClosed[Error string to agent]
```

On a later tool result with the same primary, start at the sticky provider (do not retry the failed primary). If the sticky provider then fails, continue **forward** in the list from there — do not walk back to the primary.

### Why a second client is unnecessary

`pkg/agent/llm_grpc.go` serializes `input.Config` into proto `LLMConfig` on every `Generate`. The Python service constructs (or reuses a cached) chat model from that config. Switching summarization to Flash is passing a different `*LLMProviderConfig` on that one call. Resolution happens inside `summarize.go`, not by swapping `ExecutionContext.Config` (that field is the investigator).

## Core Concepts

### Summarization provider

A **named** entry in the LLM provider registry (`google-default`, `vertexai-claude-sonnet`, …). Not a raw model string.

Set on `defaults.summarization` and optionally overlaid on `mcp_servers.*.summarization`. Last non-empty **provider name** wins: agent (implicit) → defaults → this MCP server. No chain/stage/agent level. No sentinel for “use the investigator”; a server that needs Opus names `vertexai-claude-opus`.

`search_past_sessions` has no MCP server, so it takes `defaults.summarization` only (or the calling agent when that is unset).

### Resolution result

The type lives in `pkg/agent` (next to `ResolvedFallbackEntry`) so `ExecutionContext` can hold it without a controller → agent import cycle:

```go
type ResolvedSummarizationLLM struct {
    Provider     *config.LLMProviderConfig // cloned; NativeTools nil
    ProviderName string
    Backend      config.LLMBackend
}
```

When no `llm_provider` is set at defaults or server, this is a **clone** of the calling agent's provider with `NativeTools` cleared. Backend in that unset case is the agent's backend (today's behavior). When a named summarization provider *is* set and `llm_backend` is omitted **at that layer**, backend is **langchain**, not the agent's backend and not a parent summarization layer's backend.

### Two call sites, one resolver

| Call site | Trigger | Provider |
|-----------|---------|----------|
| `maybeSummarize` | MCP result over token threshold | defaults, then this server's overlay |
| `RequiredSummarization` (`search_past_sessions`) | always when the tool returns matches | defaults only |

### Native-tool stripping (non-negotiable)

Built-in `google-default` enables `google_search` and `url_context` on the provider. Google-native `_convert_tools` attaches those even when MCP `tools` is empty. LangChain ignores `native_tools`; strip anyway so a `google-native` summarizer is safe.

`LLMProviderRegistry.Get` and `resolveFullFallbackEntries` often return the **shared registry pointer** (`applyAgentNativeTools` returns the original when the agent has no native-tool map). Clone with a shallow struct copy and set `NativeTools = nil` on the copy. Do **not** `delete` from the map in place — that would mutate the registry. Do this even when the resolved provider is the agent model, and again when walking fallback entries (those configs may have had agent native tools merged on).

The agent's `execCtx.Config.LLMProvider` must be left unchanged.

### Backend

| Situation | Backend |
|-----------|---------|
| No summarization `llm_provider` at defaults or this server | Calling agent's backend (Q2) |
| Named provider at a layer, `llm_backend` omitted **at that layer** | `langchain` |
| Named provider at a layer, `llm_backend` set at that layer | That value |
| Fallback list entry | The entry's already-resolved `Backend` (not re-defaulted to langchain) |

Never inherit the investigating agent's backend onto a named summarization provider. A Gemini parallel clone must not force `google-native` onto a Claude summarizer, or the reverse.

Each named-provider layer fully specifies the pair. If the server overlay sets `llm_provider` and omits `llm_backend`, the backend is langchain even when `defaults.summarization` set `google-native` for a different (or the same) provider. Parent-layer backend does not carry forward.

### Resolver algorithm (normative)

Inputs: agent `LLMProvider` / `LLMProviderName` / `LLMBackend`, `DefaultSummarization` (may be nil), optional per-call `serverConfig.Summarization` (nil for `search_past_sessions`).

1. `primary = {Provider: clone(agent.LLMProvider), ProviderName: agent.LLMProviderName, Backend: agent.LLMBackend}` with `NativeTools` nil on the clone.
2. If `DefaultSummarization.LLMProvider != ""`: look up that name in `LLMProviders`; `Backend = DefaultSummarization.LLMBackend` if set, else `langchain`; replace `primary`.
3. If this call has `serverConfig.Summarization.LLMProvider != ""`: look up that name; `Backend = server.LLMBackend` if set, else `langchain`; replace `primary`.
4. Record `primaryName := primary.ProviderName` **before** applying sticky (sticky is keyed by this name).
5. If `execCtx.SummarizationSticky[primaryName]` is set, use that clone (already stripped) instead of retrying a failed primary.
6. Return the clone. Never mutate registry entries or `execCtx.Config.LLMProvider`.

Lookup failure at runtime is not expected (startup validation); treat it as an LLM error so fallback / fail-open still apply.

### Failure handling (local to summarization)

Do **not** call `tryFallback`. That helper mutates `execCtx.Config`, increments `LLMFallbacksTotal`, emits a `provider_fallback` timeline event, and writes `original_llm_provider` on the agent execution — any of which would switch or mis-label the investigator.

Instead, a summarization-local walk of `execCtx.Config.ResolvedFallbackProviders`:

1. Own state, not the investigator's `FallbackState`. Start at list index 0 on the first failure of a primary (the investigator may already have advanced its own index).
2. Reuse `shouldFallback` classification with `SingleShot: true` (trip on the first error), like scoring. Empty summary (`callSummarizationLLM` already errors on blank text) counts as an error. If `ctx.Err() != nil`, do not walk; return the error (MCP fail-open / session-search fail-closed as today).
3. Skip entries whose provider name equals the summarization provider **currently in use** (same skip-same-name rule as investigation fallback).
4. Do **not** skip Claude entries for `RequiresNativeTools`. Summarization never needs Gemini native tools.
5. Use each entry's `Backend` and a **fresh clone** of `entry.Config` with `NativeTools` nil.
6. Stick per **resolved primary name** for the rest of this execution: if `google-default` failed over to Sonnet, later calls whose primary is still `google-default` start on Sonnet. A later call whose primary is `vertexai-claude-opus` (per-server overlay) is independent and tries Opus first. If the sticky provider then fails, continue forward from the next list index; do not retry the original primary.
7. Empty `ResolvedFallbackProviders`: no walk; existing fail-open / fail-closed immediately.
8. After the list is exhausted: MCP **fail-open** (raw result); `search_past_sessions` **fail-closed** (error string).
9. Do **not** increment `metrics.LLMFallbacksTotal` (that counter is investigation fallback). `ObserveLLMCall` still records each summarization attempt (including failures) under the provider that was called.
10. v1 observability for a summarization switch: `slog.Info` plus `summarization_fallback: true` on the successful MCP `mcp_tool_summary` metadata. No `provider_fallback` timeline event. No `UpdateExecutionProviderFallback`.

This applies even when summarization is still on the agent model (unset config): an Opus summary that fails walks the same list (Sandbox: skip Opus → Sonnet first) before dumping raw logs.

Sandbox `defaults.fallback_providers` today:

1. `vertexai-claude-sonnet` / langchain
2. `vertexai-claude-opus` / langchain
3. `google-default` / google-native
4. `gemini-3.1-pro` / google-native
5. `gemini-3.6-flash` / google-native

With `defaults.summarization.llm_provider: google-default`, Flash fails then: **Sonnet → Opus → (skip google-default) → gemini-3.1-pro → gemini-3.6-flash**.

### Google-native content cache

Google-native keys model-content (thought signatures) by `GenerateInput.ExecutionID`. Summarization is a one-shot `[system, user]` conversation; it must not append turns into the investigator's cache or replay investigation turns as if they were the summary prompt.

Set `GenerateInput.ExecutionID` to `execCtx.ExecutionID + ":summarization"` on every summarization call (including the unset-agent-model path). DB / timeline / `LLMInteraction` keep using `execCtx.ExecutionID`. If summarization itself falls over to another google-native provider, set `ClearCache: true` on that retry — it clears only the `:summarization` key, not the investigator's cache.

This also fixes a pre-existing bug: Gemini clones that already summarize on `google-native` with the same execution ID pollute thought-signature replay.

### Streaming on retry

- **MCP (`createEvent`):** a failed attempt that streamed may leave a failed `mcp_tool_summary` row. The successful retry creates a new event. Acceptable for v1.
- **`RequiredSummarization` (`existingEventID`):** only the **first** attempt streams into the tool-call card. Retries collect silently. `completeToolCallEvent` writes the winning summary (or the fail-closed error string) as the event content, replacing any partial first-attempt text.

v1 records `LLMInteraction` only for the successful summary (today's behavior). Failed attempts that then fallback are visible in Prometheus (`ObserveLLMCall` with error) and logs, not in `llm_interactions`.

## Configuration

```yaml
# tarsy.yaml (fragment — not a complete kubernetes-server overlay)
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

`SummarizationConfig` gains optional provider fields next to the existing size knobs:

```go
type SummarizationConfig struct {
    Enabled              *bool      `yaml:"enabled,omitempty"`
    SizeThresholdTokens  int        `yaml:"size_threshold_tokens,omitempty"`
    SummaryMaxTokenLimit int        `yaml:"summary_max_token_limit,omitempty"`
    LLMProvider          string     `yaml:"llm_provider,omitempty"`
    LLMBackend           LLMBackend `yaml:"llm_backend,omitempty"`
}
```

The same struct is used on `defaults.summarization` and `mcp_servers.*.summarization` (Q1). MCP user overlays still **replace** the whole `MCPServerConfig` (existing merge); a kubernetes-server overlay that sets `summarization.llm_provider` must keep or restate enablement / thresholds as it does today. Loader already fills `size_threshold_tokens == 0` to 5000 on enabled MCP servers before validation; do **not** apply that filler to `defaults.summarization`.

`defaults.summarization` inherits **only** `llm_provider` / `llm_backend`. Enablement and size thresholds stay per-server. A global block must not rewrite every server's 5k cutoff.

### Validation

- If `llm_provider` is set, it must exist in the LLM provider registry (`validateDefaults` and `validateMCPServers`, same pattern as `defaults.scoring.llm_provider`).
- If `llm_backend` is set, it must be a valid `LLMBackend` (`IsValid`; empty is omitted, not invalid).
- `llm_backend` without `llm_provider` at the same level is a validation error.
- On `defaults.summarization`, `enabled`, `size_threshold_tokens`, and `summary_max_token_limit` must be unset. Those fields are per-server only; setting them on defaults is a validation error so they cannot silently do nothing.
- Referenced summarization providers (`defaults.summarization.llm_provider` **and** every `mcp_servers.*.summarization.llm_provider`) are included in `collectReferencedLLMProviders()` so missing API keys fail at config load.
- `llm_provider` on a server whose summarization is explicitly `enabled: false` is a validation error (dead config). This check must run even when the existing size-threshold validation is skipped for disabled servers.

### Config viewer

`MCPServerView.Summarization` already serializes `SummarizationConfig`; new fields appear automatically. `DefaultsView` adds a `Summarization` object and `buildDefaultsView` copies it (ADR-0019). No schema migration.

## Runtime wiring

`ExecutionContext` today has `Config *ResolvedAgentConfig` and `PromptBuilder` (MCP registry). It does not have the LLM provider registry or defaults.

Add to `pkg/agent.ExecutionContext`:

```go
LLMProviders         *config.LLMProviderRegistry
DefaultSummarization *config.SummarizationConfig // may be nil
// Sticky summarization failover, keyed by resolved primary provider name.
// Lazily initialized by summarize.go; never written by tryFallback.
// Added in PR 2 (PR 1 resolves per call with no sticky).
SummarizationSticky map[string]ResolvedSummarizationLLM
```

Do not pass the entire `*config.Config` on `ExecutionContext`.

Set `LLMProviders` and `DefaultSummarization` on every context that **executes MCP or memory tools**:

| Builder | Source |
|---------|--------|
| `pkg/queue/executor.go` (investigation / action / orchestrator) | `e.cfg.LLMProviderRegistry`, `e.cfg.Defaults.Summarization` |
| `pkg/queue/chat_executor.go` | same |
| `pkg/agent/orchestrator/runner.go` (sub-agent) | `r.deps.Config` (already on `SubAgentDeps`); new sticky map per child execution |

Scoring, executive summary, and the feedback reflector do not call `maybeSummarize` / `RequiredSummarization`. Leave the new fields nil there.

Each sub-agent execution has its own sticky map. Tool calls on one execution are sequential, so the map needs no lock in v1.

`callSummarizationLLM` uses the resolved (possibly sticky) value for `GenerateInput`, `ObserveLLMCall`, `LLMInteraction.ModelName`, and timeline metadata `summarization_model` / `summarization_provider`.

Thinking / adaptive thinking is **not** a new proto flag. Claude 5 on LangChain always enables adaptive thinking. Operators who want a cheap compressor should pick a non-Claude provider (Flash).

## Observability

| Surface | Change |
|---------|--------|
| `llm_interactions.model_name` | Model that actually answered (successful attempt only, as today) |
| Prometheus `ObserveLLMCall` | Each attempt's provider name + model (including failures) |
| `metrics.LLMFallbacksTotal` | **Unchanged** — investigation only |
| `mcp_tool_summary` metadata | `summarization_model` already exists; add `summarization_provider`; set both from the answering model; set `summarization_fallback: true` when the answerer is not the resolved primary |
| Trace view | `interaction_type=summarization` already; cheaper model shows naturally |
| Dashboard `ToolSummaryItem` | Header stays “TOOL RESULT SUMMARY”. Optional subtitle with model is a follow-up, not v1 |
| Session cost (ADR-0020) | Already sums summarization interactions; `model_name` is enough for the catalog |
| `provider_fallback` on the **agent execution** | Must **not** fire for summarization |

No schema migration.

## What this is not

- Not a general “job type → model” framework. Scoring and exec summary already have their own provider fields. Memory reflector still follows scoring.
- Not conversation / history compaction. Only tool-result summarization (`maybeSummarize` + `RequiredSummarization`).
- Not changing the 5k token threshold or the 100k truncation cap.
- Not investigation provider fallback. Summarization must not call `tryFallback`.
- Not a dedicated summarization fallback list (Q4 rejected that for v1).

## Implementation Plan

Two PRs, in order. Config, resolver, call site, and operator docs are one change: shipping YAML that does nothing (or a docs-only follow-up) is not worth a separate merge. Local fallback is the other PR because it is a **behavior change for every deployment that already has `fallback_providers`**, including those that never set `defaults.summarization`.

Do not split further (no config-only PR, no docs-only PR, no “wire ExecutionContext” PR).

No sandbox-sre change in this repo.

### PR 1 — Named summarization provider - DONE

**Goal:** Operators can point tool summarization at a named provider. Unset config keeps the calling agent's model. On LLM error, behavior is still today's immediate fail-open (MCP) / fail-closed (`search_past_sessions`).

**Keeps TARSy working:** Existing configs omit the new fields → same model as today. New validation only rejects *new* invalid YAML. Lifecycle tests must still pass with unset config.

**Ships together (do not land these as separate PRs):**

1. `SummarizationConfig.LLMProvider` / `LLMBackend`; `Defaults.Summarization`.
2. Validation: unknown provider, backend without provider, invalid backend, defaults size/enabled fields, `enabled: false` + provider; include defaults **and** per-server names in `collectReferencedLLMProviders`.
3. Config viewer: `DefaultsView.Summarization` + `buildDefaultsView`.
4. `ResolvedSummarizationLLM` in `pkg/agent`. `ExecutionContext` gains `LLMProviders` and `DefaultSummarization` only (no sticky map yet).
5. Thread those fields from investigation, chat, and sub-agent builders. Scoring / exec-summary / reflector stay nil.
6. Resolver + `callSummarizationLLM`: clone, `NativeTools = nil`, `:summarization` execution ID suffix, metrics / `model_name` / `summarization_provider` from the resolved model.
7. Docs that match this PR: YAML snippet in `deploy/config/README.md`; architecture-overview bullet for the optional provider. Do **not** claim fallback-before-raw yet.

**Tests:**

- Validator: unknown provider; disabled+provider; backend invalid; backend without provider; defaults `size_threshold_tokens` / `enabled` rejected; overlay-only provider missing API key fails config load; config viewer JSON includes `defaults.summarization`.
- Resolver: nil defaults + nil server → agent name and backend; clone has nil `NativeTools`; registry / agent maps unchanged; defaults `google-default` omit backend → Flash + langchain; defaults + `google-native` → Flash + google-native; server overlay wins; omitted server backend → langchain (does not inherit defaults' google-native).
- Call site: below threshold never calls the LLM; above threshold uses resolved provider; `search_past_sessions` uses defaults only; `GenerateInput.ExecutionID` is `execID + ":summarization"`; investigation Generate calls keep the unsuffixed ID; LLM error → MCP fail-open / session-search fail-closed (one Generate, no retry).

### PR 2 — Summarization-local fallback

**Depends on PR 1.** Do not merge first: it needs the resolver and the isolated execution ID.

**Goal:** On summarization LLM error, walk the agent's `ResolvedFallbackProviders` locally (Q4). Investigator config, `LLMFallbacksTotal`, and `provider_fallback` execution metadata stay untouched.

**Keeps TARSy working:** After fallbacks are exhausted, MCP still fail-opens and `search_past_sessions` still fail-closes. Empty fallback list is identical to PR 1. Unset `defaults.summarization` still uses the agent model; only the *failure* path changes (retry Sonnet/… before dumping raw logs).

**Ships:**

1. `ExecutionContext.SummarizationSticky`.
2. Local walk: own index, SingleShot, skip current name, no `RequiresNativeTools` filter, entry backend as-is, clone + strip `NativeTools`.
3. Sticky per primary name; sticky failure continues forward (does not retry the original primary).
4. `ClearCache: true` only on summarization retries (suffixed ID). First-attempt-only streaming for `existingEventID`; MCP `createEvent` may leave a failed row on a streamed-then-failed attempt.
5. Successful MCP summary metadata: `summarization_fallback: true` when the answerer is not the resolved primary.
6. Docs: architecture-overview + README note that summarization walks `fallback_providers` before fail-open / fail-closed.

**Tests:**

- Flash error → next Generate uses Sonnet; `execCtx.Config.LLMProviderName` still Opus; `LLMFallbacksTotal` unchanged; no `UpdateExecutionProviderFallback`.
- Sticky: second large MCP result does not retry Flash.
- Overlay Opus primary after Flash already failed: still tries Opus first.
- Sticky Sonnet then fails: continues to Opus, does not retry Flash.
- List exhausted → MCP fail-open / session-search fail-closed.
- Empty `ResolvedFallbackProviders` → immediate fail-open / fail-closed (PR 1 behavior).
- `ClearCache` on retry uses the suffixed ID only; investigation Generate calls are not cleared.

## Test plan

Covered per PR above. PR 1 must stay green without PR 2; PR 2 must not regress unset-config investigations.

## v1 limitations

- Failed summarization attempts that later succeed via fallback are not stored as `LLMInteraction` rows.
- No dedicated summarization fallback list; operators who want a different order must reorder investigation `fallback_providers`.
- No sentinel to pin one MCP server back to “whoever is investigating.”
- A partial-then-failed MCP summary may leave an extra failed `mcp_tool_summary` row.

## Decisions

| ID | Decision |
|----|----------|
| Q1 | Defaults + per-MCP-server overlay; no chain/stage/agent. Only provider/backend inherit from defaults. |
| Q2 | Unset = calling agent's provider and backend. Opt-in. |
| Q3 | `search_past_sessions` uses the same resolver (`defaults.summarization` only). Implied by Q1. |
| Q4 | Reuse `fallback_providers` locally (skip current name, SingleShot, no native-tool skip, sticky per primary). Never `tryFallback`. Exhausted → existing fail-open / fail-closed. Own index; do not reuse the investigator's `FallbackState`. |
| Q5 | Explicit `llm_backend`; default langchain when a named provider is set at that layer and backend is omitted. Never inherit the agent's backend. Always strip `NativeTools`. |
| Q6 | No sentinel. Per-server overlay names another provider (e.g. Opus), not “the investigator.” |

## Design-review clarifications

Recorded during `/verify-design` against the current tree. These do not reopen Q1–Q6.

- `ResolvedSummarizationLLM` is exported in `pkg/agent` so sticky state can live on `ExecutionContext`.
- Each named-provider YAML layer resets backend to (explicit or langchain); layers do not inherit each other's backend.
- Fallback entries keep their resolved `Backend`; clone + strip `NativeTools` before use.
- `GenerateInput.ExecutionID` suffix isolates google-native content cache; `ClearCache` on summarization retry must not wipe the investigator.
- Thread registry/defaults only onto tool-running executors. Scoring already has `*config.Config` unused for summarization.
- Do not increment `LLMFallbacksTotal`.
- `llm_backend` without `llm_provider`, and size/enabled fields on `defaults.summarization`, are validation errors.
