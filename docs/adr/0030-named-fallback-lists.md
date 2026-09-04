# ADR-0030: Named Fallback Lists

**Status:** Implemented  
**Date:** 2026-09-03  
**Amends:** [ADR-0003](0003-llm-provider-fallback.md), [ADR-0017](0017-native-tool-fallback-safety.md), [ADR-0024](0024-tool-summarization-provider.md) Q4, [ADR-0019](0019-config-viewer.md)

## Overview

TARSy already has an ordered LLM fallback walk ([ADR-0003](0003-llm-provider-fallback.md)). On failure it walks that list, skips names already attempted in the execution, skips native-tool-incompatible backends ([ADR-0017](0017-native-tool-fallback-safety.md)), and sticks for the rest of the execution.

That list was **one ordered preference**. Operators could replace it at chain / stage / stage-agent, but there was no way to **name and reuse** several preferences. In practice deployments kept a single `defaults.fallback_providers` list and every execution walked it.

That is the wrong shape once primaries differ in cost and quality:

| Work | Typical primary | Desired fallback |
|------|-----------------|------------------|
| Parallel investigation / orchestrator / synthesis | Frontier model | Other frontier models, then mid-tier |
| Remediation, scoring, exec summary, compose | Mid-tier (e.g. Sonnet) | Other cheap/mid models — not the frontier head |
| WebResearcher / CodeExecutor | `google-default` + `google-native` | Other Gemini native models |
| Tool summarization | Flash / `google-default` | Other cheap Gemini — not the investigator’s frontier list |

A Sonnet remediator whose primary failed tried the head of the shared list first (often Opus). Attempted-name skip only drops the current name; it does not keep the walk inside a cost band.

This decision adds **named fallback lists** and a **`fallback_list` selector** so each execution binds one list. Runtime fallback (triggers, stickiness, native-tool skip, summarization-local walk) does not change — only **which list** is resolved onto the execution.

## Design Principles

1. **A fallback list is a cost/quality preference, not a global ranking.** Different work may prefer “stay expensive” vs “stay cheap” vs “stay google-native.”
2. **Reuse by name.** Lists live in a catalog. Call sites pick a name. After migration, one-offs are extra catalog entries, not inline slices.
3. **Zero migration for current YAML, except the native-tool primary pair.** `fallback_providers` still loads (deprecated, startup warning). Configs that never define `fallback_lists` keep today’s fallback *walks*. Builtin WebResearcher / CodeExecutor set `llm_provider: google-default` so short-form native-tool agents stop pairing the defaults primary with `google-native`.
4. **Resolution only.** Controllers, Python retries, and timeline events are unchanged. The resolver still produces `ResolvedFallbackProviders`.
5. **Fail at load for real mistakes.** Unknown list names, invalid entries, and both selector + inline on the same node are startup errors. Unused lists may omit credentials (same as unused providers); unknown providers/backends in any catalog entry still fail.
6. **Operator order is still law.** The system does not re-rank by price or quality ([ADR-0003](0003-llm-provider-fallback.md) principle 3).
7. **Provider and backend are a pair** at every pairing site (defaults, `defaults.agents.<name>` for any registered agent, job knobs, chain / stage / ref). Identity (`agents:` / builtins) keeps `llm_backend` and tools; it does **not** gain YAML `llm_provider` / `fallback_list`. Global pair for an agent — builtin (explicit or implicit) or custom — lives under `defaults`. A chain/stage/ref that names that agent overrides when set. Omitted backend still means langchain at a pairing node that names a provider.

## Decisions

| # | Topic | Decision | Rationale |
|---|-------|----------|-----------|
| Q1 | Mechanism | Named catalog + `fallback_list` selector | Copy-paste inline lists drift. A tier model would fight operator-ordered walks. Missing selector sites still needed fields either way. |
| Q2 | Catalog location | Top-level `fallback_lists` in `tarsy.yaml` | Named registry, like agents / MCP servers — not a default value and not a provider property. |
| Q3 | Legacy field | `defaults.fallback_list` is the default; `fallback_providers` deprecated | Happy path is names only. Existing YAML keeps working. No magic rewrite into a reserved `default` catalog name. Removal is a later change. |
| Q4 | Both fields on one node | Load-time error | Mixing new + old is a migration mistake, not precedence. Silent ignore or append would hide leftovers. |
| Q5 | Selector sites | `fallback_list` on every primary-model site + stage; `fallback_providers` only where it already exists | Anywhere you pick a primary, you can pick the matching walk. New surfaces must not grow the deprecated field. Four layers only would leave sub-agents and scoring on the investigation walk. |
| Q6 | Builtin native-tool pair | Go sets `google-default` + `google-native`; no pairing on `agents:` YAML | Short-form primary is safe. Identity map stays identity (Q13). Catalog names stay out of builtins. |
| Q7 | Side-path selectors | Skipped (Q5) | Already covered. |
| Q8 | Summarization | Optional `defaults.summarization.fallback_list`; no per-server overlay | Flash stays in a cheap/native band. Unset keeps the ADR-0024 walk (calling agent’s effective list). A required named list would break configs with no catalog. |
| Q9 | Inheritance + implicit jobs | Inherit defaults → chain when unset; job knobs for scoring / compose / exec summary / summarization | Dedicated knobs beat investigation `premium`. Stopping chain inherit would change existing `fallback_providers` behavior. Chat global pairing is `defaults.agents.ChatAgent`, not a separate `defaults.chat` block. |
| Q10 | List includes | Flat lists | No cycles. Copied Gemini tails are cheap. Revisit includes if catalogs grow large. |
| Q11 | Unused catalog lists | Structure-validate all; credentials only if referenced | Typos fail; spare lists without keys do not. Ignoring unused lists entirely would hide typos until selected. |
| Q12 | Config viewer | Catalog + raw selectors as written | [ADR-0019](0019-config-viewer.md) snapshot, not a resolved agent graph. Computed per-agent walks would drift from the resolver. |
| Q13 | Named-agent global pairing | `defaults.agents.<name>` for any registered agent | Not `agents:` identity (`mergeAgents` would replace a builtin of the same name). Chain/stage/ref that names the agent wins when set. |

## Architecture

### Binding a list to an execution

Top-level catalog next to `agents` / `mcp_servers`. `defaults.fallback_list` selects the default. Each other layer may name a list or inherit.

Each YAML node that can choose a list has **at most one** of:

| Field | Meaning |
|-------|---------|
| `fallback_list: <name>` | Use that catalog entry |
| `fallback_providers: [...]` | Deprecated inline list (existing four layers only) |
| neither | Inherit the next less-specific layer |

Both set on the same node is a **load-time error**.

Per-node resolution: non-empty `fallback_list` → expand catalog; else deprecated `fallback_providers` (including explicit empty slice); else inherit. Omitted or empty-string `fallback_list` inherits. “No fallback” is deprecated `fallback_providers: []` or a catalog entry whose list is empty.

The controller never sees list names. After resolve, `ResolvedFallbackProviders` is a flat slice. Each YAML layer is expanded to a slice-or-nil **before** last-non-nil (a named list is a non-nil slice even when empty).

```
YAML node
  ├─ fallback_list: "mid"
  │     → lookup fallback_lists["mid"]
  │     → []FallbackProviderEntry
  └─ fallback_providers: [...]          # deprecated; existing four layers only
        → []FallbackProviderEntry
            │
            ▼
  last non-nil layer
            │
            ▼
  resolve entries (unchanged)
            │
            ▼
  tryFallback() / summarization-local walk  (unchanged)
```

### Investigation / dispatched-agent hierarchy

Last non-nil slice wins, after expanding names:

```
defaults (fallback_list or deprecated inline)
  → chain (fallback_list or deprecated inline)
  → defaults.agents.<resolved agent name>
  → stage                          (investigation stages; does not leak to chat/scoring/exec summary)
  → stage-agent / sub-agent ref
```

Go builtin `llm_provider` / `llm_backend` is the pairing layer under `defaults.agents` when that map omits the name (same slot as today’s agent-def backend overlay).

The parent orchestrator’s `llm_provider` / `fallback_list` on the stage-agent or parent ref does **not** leak to dispatched sub-agents (empty stage; ref is its own layer). `defaults.agents.<name>` beats the investigation **chain** global list (so `chain.fallback_list: premium` does not poison WebResearcher). Stage-agent / ref / `chain.chat` / `stage.synthesis` still win when set.

### Side-path hierarchy

Side paths inherit **defaults → chain** when their own selector is unset (stage lists still do not leak into chat/scoring/exec summary/compose). Dedicated knobs override when set:

```
defaults.fallback_list / deprecated inline
  → chain.fallback_list / deprecated inline
  → defaults.agents.<name>            # ChatAgent, SynthesisAgent, …
  → defaults.<job> selector           # scoring.fallback_list, compose.fallback_list, …
  → chain/stage job selector          # chain.chat, chain.scoring, stage.synthesis
```

`defaults.agents.ChatAgent` applies when the chat agent name is `ChatAgent` (or `defaults.agents.<chain.chat.agent>` when chat names a custom agent). `chain.chat` wins when set. There is no separate `defaults.chat` block.

This fallback order is **not** the same as scoring’s *provider* order. `chain.llm_provider` still beats `defaults.scoring.llm_provider` (existing). Dedicated `defaults.scoring.fallback_list` still beats an investigation `chain.fallback_list` / `defaults.fallback_list`. That divergence is intentional: the new knobs exist so Sonnet scoring does not walk `premium`.

Compose/exec-summary *provider* order already matches this shape (`defaults.compose` beats `chain.llm_provider`). Compose must not inherit the action stage’s list. Exec summary uses a zero stage at runtime.

Synthesis uses `stage.synthesis.fallback_list` when set (copied onto the synthetic stage-agent, same as provider/backend); otherwise `defaults.agents.SynthesisAgent` if set, else the investigation stage list after defaults → chain.

### Summarization

If `defaults.summarization.fallback_list` is set, the summarization-local walk uses that catalog list (resolved onto the execution context **separately** from the investigator’s `ResolvedFallbackProviders`). Unset → calling agent’s effective list ([ADR-0024](0024-tool-summarization-provider.md)). Still does not mutate the investigator; still no native-tool skip. A per-server Opus overlay still walks this same global summarization list (no per-server `fallback_list` in v1).

This amends ADR-0024 Q4, which rejected a dedicated summarization fallback list for v1.

### Named-agent pairing (`defaults.agents`)

A map of registry name → pair + list. Global setting for **any** named agent: explicit builtins (`WebResearcher`), implicit named agents (`ChatAgent`, optionally `SynthesisAgent`), and custom agents. Not for jobs that are not an agent you dispatch (summarization, scoring, compose, exec-summary field names). Identity stays in `agents:` / Go builtins. Override at the chain/stage/ref that names the agent.

Builtin WebResearcher / CodeExecutor set `llm_provider: google-default` and `llm_backend: google-native` **in Go**. Catalog names stay out of builtins. List names are independent of backend enum values — a catalog key `google-native` is allowed and is not the backend `google-native`.

Do not put `llm_provider` / `fallback_list` on the `agents:` map (that would replace a builtin of the same name). Unset `defaults.agents.<name>` inherits global `defaults` (and the Go builtin pair for WebResearcher / CodeExecutor).

### Implicit jobs

Scoring, compose, exec summary, and summarization keep **job** knobs under `defaults` (`scoring`, `compose`, `executive_summary`, `summarization`). Chat global pairing is `defaults.agents.ChatAgent` (or the custom chat agent name); `chain.chat` wins. Memory reflector reuses the scoring execution context, so it walks scoring’s effective list with no extra knob.

Adding sibling backend on compose and exec summary **amends** ADR-0003’s “those provider fields have no sibling backend.” Omitted `llm_backend` still means langchain.

## Configuration (user-facing contract)

### Catalog

```yaml
fallback_lists:
  premium:
    - llm_provider: vertexai-claude-opus-4-8
    - llm_provider: gpt-5.6-sol
    - llm_provider: google-default
      llm_backend: google-native
  mid:
    - llm_provider: vertexai-claude-sonnet
    - llm_provider: gemini-3.7-flash
      llm_backend: google-native
  google-native:
    - llm_provider: google-default
      llm_backend: google-native
    - llm_provider: gemini-3.7-flash
      llm_backend: google-native

defaults:
  llm_provider: vertexai-claude-opus
  llm_backend: langchain
  fallback_list: premium
  scoring:
    llm_provider: vertexai-claude-sonnet
    fallback_list: mid
  compose:
    llm_provider: vertexai-claude-sonnet
    llm_backend: langchain          # omit → langchain
    fallback_list: mid
  executive_summary:
    llm_provider: vertexai-claude-sonnet
    llm_backend: langchain
    fallback_list: mid
  summarization:
    llm_provider: google-default
    llm_backend: google-native
    fallback_list: google-native
  agents:
    WebResearcher:
      llm_provider: google-default      # omit → builtin google-default
      llm_backend: google-native
      fallback_list: google-native
    CodeExecutor:
      fallback_list: google-native
    ChatAgent:
      llm_provider: vertexai-claude-sonnet
      fallback_list: mid
    MyCustomKubeAgent:
      llm_provider: vertexai-claude-opus
      fallback_list: premium
```

Short-form sub-agent (parent orchestrator is Opus; WebResearcher is not overridden by that):

```yaml
- name: SecurityInvestigationOrchestrator
  llm_provider: vertexai-claude-opus
  llm_backend: langchain
  sub_agents:
    - name: MyCustomKubeAgent          # omit pair → defaults.agents.MyCustomKubeAgent
    - name: SecurityInvestigationAgent
      llm_provider: vertexai-claude-sonnet
      fallback_list: mid               # chain ref wins over defaults.agents
    - name: WebResearcher              # google-default + google-native + defaults.agents list
```

Catalog entries use the same `llm_*` keys as pairing sites. Unknown keys fail load (`provider` / `backend` on a catalog entry hint `llm_provider` / `llm_backend`; the reverse on `defaults.agents`). Deprecated `fallback_providers` keeps `provider` / `backend`.

List names are non-empty YAML mapping keys. Duplicate names cannot exist (YAML map). Every catalog entry is structure-validated (provider exists, backend valid, `google-native` only with a Google provider). Credentials are required only for **referenced** lists.

**Referenced** means every `fallback_list` string that appears in YAML (including `defaults.fallback_list`, `defaults.compose.fallback_list`, and `defaults.summarization.fallback_list`), plus every deprecated `fallback_providers` entry and deprecated `compose_fallback_list` / `executive_summary_fallback_list` alias. It is not a full per-execution resolve: a default list named in YAML is credential-checked even if every chain overrides it. Unnamed catalog entries are structure-only.

Also treat as referenced LLM providers: defaults/chain/stage-agent/sub-agent/side-path `llm_provider` fields, including `defaults.executive_summary.llm_provider`, `defaults.agents.*.llm_provider`, and compose/exec-summary names. Do not credential-check builtin WebResearcher’s `google-default` merely because the builtin exists; do check it when `defaults.agents.WebResearcher` (or a reachable ref) names it or when the builtin pair is used because the agent can run.

Deprecated `fallback_providers` still loads on defaults / chain / stage / stage-agent. Startup warns. A fully migrated config never mentions it.

### Selector and pairing sites

| Site | `fallback_list` | `fallback_providers` | Provider / backend |
|------|-----------------|----------------------|--------------------|
| `defaults` | yes | keep (deprecated) | existing `llm_provider` / `llm_backend` |
| `defaults.agents.<name>` | yes | **not added** | `llm_provider` / `llm_backend` (pair + list only) |
| `agent_chains.<id>` | yes | keep (deprecated) | existing |
| `stages[]` | yes | keep (deprecated) | none (no stage `llm_provider`) |
| `stages[].agents[]` | yes | keep (deprecated) | existing |
| `agents.<name>` (identity) | **not added** | **not added** | existing `llm_backend` only |
| `sub_agents[]` | yes | **not added** | existing |
| `defaults.scoring` / `chain.scoring` | yes | **not added** | existing |
| `chain.chat` | yes | **not added** | existing; global pairing is `defaults.agents.ChatAgent` (or custom chat agent name) |
| `stage.synthesis` | yes | **not added** | existing; optional global `defaults.agents.SynthesisAgent` |
| compose | `defaults.compose` / `chain.compose` (`fallback_list`) | **not added** | `llm_provider` + `llm_backend` (defaults and chain) |
| exec summary | `defaults.executive_summary` / `chain.executive_summary` (`fallback_list`) | **not added** | `llm_provider` + `llm_backend` (defaults and chain) |
| `defaults.summarization` | yes | **not added** | existing; **no** per-MCP-server `fallback_list`; **not** `defaults.agents` |

Keep current names (`compose.llm_provider`, `executive_summary.llm_provider`). Deprecated flat aliases (`compose_provider`, `executive_summary_provider`, and their `*_backend` / `*_fallback_list` siblings) still load with a startup warning. Mixing nested + deprecated keys on the same node is a load-time error.

Sub-agent short-form (`sub_agents: [WebResearcher]`) has no ref overlay. Effective list is last non-nil among defaults → chain → `defaults.agents.WebResearcher` → empty stage. Long-form objects may set `fallback_list` and the provider pair.

### Inheritance that must not surprise

- **Stage-level fallback does not leak into chat, scoring, exec summary, or compose.**
- **Parent orchestrator `llm_provider` / stage-agent `fallback_list` does not leak to sub-agents.**
- **`defaults.agents.<name>` beats chain-global list** so investigation `premium` does not poison WebResearcher. **Ref / stage-agent / `chain.chat` / `stage.synthesis` still win** when set.
- Do not set a chain-level list *instead of* `defaults.agents` if named agents need a different walk — set both.

## Runtime behavior (unchanged)

All of the following stay as specified in [ADR-0003](0003-llm-provider-fallback.md) / [0017](0017-native-tool-fallback-safety.md) / [0027](0027-llm-transient-outage.md) / [0028](0028-llm-retry-resilience.md):

- Python identical retries, then Go fallback
- Sticky for the rest of the execution; new executions resolve fresh
- Skip already-attempted provider names
- Skip non-`google-native` entries when native tools are required
- Summarization-local walk does not mutate the investigator and does not apply native-tool skip
- Timeline `provider_fallback` events, execution `original_llm_*` columns, dashboard chips

Native-tool startup **warning** uses the **effective** list after named-list expansion, including `defaults.agents` and **all** sub-agent ref sites (chain, stage, stage-agent, chat).

## Observability and config viewer

- Timeline / metrics: no new event types. Provider names in existing fallback events already identify what was used.
- Config viewer (`GET /api/v1/system/config`): emit the `fallback_lists` catalog as `{llm_provider, llm_backend}` (omitted backend filled as `langchain`), `defaults.agents`, nested `compose` / `executive_summary` pairings, and the raw `fallback_list` / deprecated `fallback_providers` (`provider` / `backend`) fields as written. Do not compute per-agent expanded walks. Do **not** add `llm_provider` / `fallback_list` to agent identity.
- Startup: warn on any use of `fallback_providers` and of deprecated `compose_*` / `executive_summary_*` keys.

## Out of scope

- Auto-sorting fallbacks by cost or by “tier”
- `include` of other named lists
- Per-MCP-server summarization `fallback_list`
- Process-wide provider cooldown / probe-unstick ([ADR-0027](0027-llm-transient-outage.md) later)
- Changing retry counts, stickiness, or native-tool skip rules
- Removing `fallback_providers` (deprecation only)
- Computed `effective_fallback_providers` in the config viewer

## References

- [ADR-0003: LLM Provider Fallback](0003-llm-provider-fallback.md)
- [ADR-0017: Native Tool Fallback Safety](0017-native-tool-fallback-safety.md)
- [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md)
- [ADR-0024: Tool Summarization Provider](0024-tool-summarization-provider.md)
- [ADR-0027: Transient LLM Outage Handling](0027-llm-transient-outage.md)
- [ADR-0028: LLM Retry Resilience](0028-llm-retry-resilience.md)

## Amendment (2026-09-03)

**Nested compose / executive_summary.** Scoring and summarization already used nested job blocks (`llm_provider` / `llm_backend` / `fallback_list`). Compose and exec-summary originally kept prefixed flat keys to avoid a YAML migration. They now use the same nested shape (`defaults.compose`, `chain.compose`, `defaults.executive_summary`, `chain.executive_summary`). The old `compose_*` / `executive_summary_*` keys still load and are copied into the nested blocks with a startup warning. Mixing nested + deprecated keys on one node is a load-time error.
