# Named Fallback Lists

**Status:** Final
**Related:** [Questions document](named-fallback-lists-questions.md)

**Amends:** [ADR-0003](../adr/0003-llm-provider-fallback.md), [ADR-0017](../adr/0017-native-tool-fallback-safety.md), [ADR-0024](../adr/0024-tool-summarization-provider.md) Q4 (adds the summarization-local list ADR-0024 rejected for v1)

## Overview

TARSy already has an ordered LLM fallback list ([ADR-0003](../adr/0003-llm-provider-fallback.md)). On failure it walks that list, skips names already attempted in the execution, skips native-tool-incompatible backends ([ADR-0017](../adr/0017-native-tool-fallback-safety.md)), and sticks for the rest of the execution.

That list is **one ordered preference**. Operators can replace it at chain / stage / stage-agent, but there is no way to **name and reuse** several preferences. In practice deployments keep a single `defaults.fallback_providers` list and every execution walks it.

That is the wrong shape once primaries differ in cost and quality. Dev Sandbox is the concrete case:

| Work | Typical primary | Desired fallback |
|------|-----------------|------------------|
| Parallel investigation / orchestrator / synthesis | Claude Opus (or Gemini Pro) | Other frontier models, then mid-tier |
| Remediation, scoring, exec summary, compose | Claude Sonnet | Other cheap/mid models — **not** Opus |
| WebResearcher / CodeExecutor | `google-default` + `google-native` | Other Gemini native models |
| Tool summarization | Flash / `google-default` | Other cheap Gemini — **not** the investigator's Opus list |

Today a Sonnet remediator whose primary fails tries `vertexai-claude-opus-4-8` first, because that is the head of the shared list. Attempted-name skip only drops the current name; it does not keep the walk inside a cost band.

This design adds **named fallback lists** and a **`fallback_list` selector** so each execution binds one list. Runtime fallback (triggers, stickiness, native-tool skip, summarization-local walk) does not change — only **which list** is resolved onto the execution.

## Design Principles

1. **A fallback list is a cost/quality preference, not a global ranking.** Different work may prefer “stay expensive” vs “stay cheap” vs “stay google-native.”
2. **Reuse by name.** Lists live in a catalog. Call sites pick a name. After migration, one-offs are extra catalog entries, not inline slices.
3. **Zero migration for current YAML, except the native-tool primary pair.** `fallback_providers` still loads (deprecated, startup warning). Configs that never define `fallback_lists` keep today’s fallback *walks*. PR 2 still sets builtin WebResearcher / CodeExecutor `llm_provider: google-default` so short-form native-tool agents stop pairing defaults Opus with `google-native`.
4. **Resolution only.** Controllers, Python retries, and timeline events are unchanged. The resolver still produces `ResolvedFallbackProviders`.
5. **Fail at load for real mistakes.** Unknown list names, invalid entries, and both selector + inline on the same node are startup errors. Unused lists may omit credentials (same as unused providers); unknown providers/backends in any catalog entry still fail.
6. **Operator order is still law.** The system does not re-rank by price or quality ([ADR-0003](../adr/0003-llm-provider-fallback.md) principle 3).
7. **Provider and backend are a pair** at every pairing site (defaults, `defaults.agents.<name>` for any registered agent, job knobs, chain / stage / ref). Identity (`agents:` / builtins) keeps `llm_backend` and tools; it does **not** gain YAML `llm_provider` / `fallback_list`. Global pair for an agent — builtin (explicit or implicit) or custom — lives under `defaults`. A chain/stage/ref that names that agent overrides when set. Omitted backend still means langchain at a pairing node that names a provider.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Mechanism | Named catalog + `fallback_list` selector | Copy-paste inline lists drift; no tier model |
| Q2 | Catalog location | Top-level `fallback_lists` in `tarsy.yaml` | Named registry, like agents / MCP servers |
| Q3 | Legacy field | `defaults.fallback_list` is the default; `fallback_providers` deprecated | Happy path is names only; existing YAML keeps working |
| Q4 | Both fields on one node | Load-time error | Mixing new + old is a migration mistake |
| Q5 | Selector sites | `fallback_list` on every primary-model site + stage; `fallback_providers` only where it already exists | New surfaces must not grow the deprecated field |
| Q6 | Builtin native-tool pair | Go sets `google-default` + `google-native`; no pairing on `agents:` YAML | Short-form primary is safe; identity map stays identity (Q13) |
| Q7 | Side-path selectors | Skipped (Q5) | Already covered |
| Q8 | Summarization | Optional `defaults.summarization.fallback_list`; no per-server overlay | Flash stays in a cheap/native band; ADR-0024 walk otherwise |
| Q9 | Inheritance + implicit jobs | Inherit defaults → chain when unset; job knobs for scoring / compose / exec summary / summarization | Dedicated knobs beat investigation `premium`; chain/stage override when set |
| Q10 | List includes | Flat lists | No cycles; copied Gemini tails are cheap |
| Q11 | Unused catalog lists | Structure-validate all; credentials only if referenced | Typos fail; spare lists without keys do not |
| Q12 | Config viewer | Catalog + raw selectors as written | ADR-0019 snapshot, not a resolved agent graph |
| Q13 | Named-agent global pairing | `defaults.agents.<name>` for any registered agent | Not `agents:` identity (would replace builtins). Chain/stage/ref that names the agent wins when set |

## Architecture / How It Works

### What exists today

```
defaults.fallback_providers
    ↑ last non-nil wins (explicit empty slice clears)
chain.fallback_providers
stage.fallback_providers
stage-agent.fallback_providers     ← stage agents only
```

Then the resolver looks up each entry and stores `ResolvedFallbackProviders` on the execution. `tryFallback()` walks that slice.

Gaps that made the single-list problem worse:

- **Sub-agent refs** have `llm_provider` / `llm_backend` but **no** fallback field. Dispatched sub-agents call `ResolveAgentConfig` with an **empty stage** and the ref mapped onto `StageAgentConfig`, so they get defaults → chain only (not the parent orchestrator’s stage-agent list).
- **Chat and scoring** use dedicated resolvers (`ResolveChatAgentConfig` / `ResolveScoringConfig`) and inherit defaults → chain only. Existing tests: stage fallback must not leak into chat/scoring.
- **Exec summary** at runtime also uses `ResolveAgentConfig`, but the executor passes a **zero `StageConfig`**, so it does not inherit a stage list. The dedicated `ResolveExecSummaryConfig` helper is used in tests, not by the session executor.
- **Compose** at runtime uses `ResolveAgentConfig` with the **action stage** that just completed. If that stage sets `fallback_providers`, compose inherits it today — unlike `ResolveComposeConfig` (tests only), which is defaults → chain. This design treats the test helper as the intended contract and **fixes the runtime path**.
- **Synthesis** reuses the investigation stage via `ResolveAgentConfig` (provider/backend copied from `stage.synthesis` onto a synthetic stage-agent). A stage-level investigation list therefore applies unless `stage.synthesis` sets its own list.
- **Summarization** walks the **calling agent’s** resolved list ([ADR-0024](../adr/0024-tool-summarization-provider.md) Q4). The reflector reuses the scoring execution context, so it walks scoring’s list.
- **Exec summary** has no `defaults.*` knob. **Compose** has `compose_provider` with no sibling backend.
- **Agent definitions** have `llm_backend` but not `llm_provider`, so short-form WebResearcher can resolve to Opus + `google-native`.
- **Native-tool startup warning** only inspects stage agents and **chain-level** `sub_agents`. Stage / stage-agent / chat refs are not warned today.

```
Alert / chat / scoring / compose / exec-summary / tool summarization
        │
        ▼
  ResolveAgentConfig / Resolve*Config
        │  binds one []FallbackProviderEntry
        ▼
  ResolvedFallbackProviders on the execution
        │
        ├─ iterating / single-shot tryFallback()   (sticky, native-tool skip)
        └─ summarization-local walk                (does not mutate investigator)
```

### Named lists + selector

Top-level catalog next to `agents` / `mcp_servers`. `defaults.fallback_list` selects the default. Each other layer may name a list or inherit.

```yaml
fallback_lists:
  premium:
    - provider: vertexai-claude-opus-4-8
    - provider: vertexai-claude-opus-4-6
    - provider: vertexai-claude-opus
    - provider: gpt-5.6-sol
    - provider: google-default
      backend: google-native
  mid:
    - provider: vertexai-claude-sonnet
    - provider: gemini-3.7-flash
      backend: google-native
    - provider: gemini-3.1-pro
      backend: google-native
  google-native:
    - provider: google-default
      backend: google-native
    - provider: gemini-3.7-flash
      backend: google-native
    - provider: gemini-3.1-pro
      backend: google-native

defaults:
  llm_provider: vertexai-claude-opus
  llm_backend: langchain
  fallback_list: premium
  scoring:
    llm_provider: vertexai-claude-sonnet
    llm_backend: langchain
    fallback_list: mid
  compose_provider: vertexai-claude-sonnet
  compose_backend: langchain          # omit → langchain
  compose_fallback_list: mid
  executive_summary_provider: vertexai-claude-sonnet
  executive_summary_backend: langchain
  executive_summary_fallback_list: mid
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

Builtin WebResearcher / CodeExecutor set `llm_provider: google-default` and `llm_backend: google-native` **in Go**. Catalog names stay out of builtins. List names are independent of backend enum values — a catalog key `google-native` is allowed and is not the backend `google-native`.

`defaults.agents` is pair + list only, keyed by registry name (explicit builtins, implicit named agents, custom). It does **not** replace identity. Do not put `llm_provider` / `fallback_list` on the `agents:` map (`mergeAgents` would replace a builtin of the same name). Unset `defaults.agents.<name>` inherits global `defaults` (and the Go builtin pair for WebResearcher / CodeExecutor). A chain/stage/ref that names the agent overrides when set.

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

The orchestrator’s `llm_provider` / `fallback_list` on the **stage-agent or parent ref** does **not** leak to dispatched sub-agents (empty stage; ref is its own layer). `defaults.agents.<name>` beats the investigation **chain** global list (so `chain.fallback_list: premium` does not poison WebResearcher). Stage-agent / ref / `chain.chat` / `stage.synthesis` still win when set. Do not set a chain-level list *instead of* `defaults.agents` if named agents need a different walk — set both.

Deprecated `fallback_providers` still loads on defaults / chain / stage / stage-agent. Startup warns. A fully migrated config never mentions it.

### Binding a list to an execution

Each YAML node that can choose a list has **at most one** of:

| Field | Meaning |
|-------|---------|
| `fallback_list: <name>` | Use that catalog entry |
| `fallback_providers: [...]` | Deprecated inline list (existing four layers only) |
| neither | Inherit the next less-specific layer |

Both set on the same node is a **load-time error**.

Per-node resolution: non-empty `fallback_list` → expand catalog; else deprecated `fallback_providers` (including explicit empty slice); else inherit. Omitted or empty-string `fallback_list` inherits. “No fallback” is deprecated `fallback_providers: []` or a catalog entry whose list is empty.

Investigation / dispatched-agent hierarchy (last non-nil slice wins), after expanding names:

```
defaults (fallback_list or deprecated inline)
  → chain (fallback_list or deprecated inline)
  → defaults.agents.<resolved agent name>
  → stage                          (investigation stages; does not leak to chat/scoring/exec summary)
  → stage-agent / sub-agent ref
```

Go builtin `llm_provider` / `llm_backend` is the pairing layer under `defaults.agents` when that map omits the name (same slot as today’s agent-def backend overlay).

Side paths inherit **defaults → chain** when their own selector is unset (stage lists still do not leak into chat/scoring/exec summary). Dedicated knobs override when set:

```
defaults.fallback_list / deprecated inline
  → chain.fallback_list / deprecated inline
  → defaults.agents.<name>            # ChatAgent, SynthesisAgent, …
  → defaults.<job> selector           # scoring.fallback_list, compose_fallback_list, …
  → chain/stage job selector          # chain.chat, chain.scoring, stage.synthesis
```

`defaults.agents.ChatAgent` applies when the chat agent name is `ChatAgent` (or `defaults.agents.<chain.chat.agent>` when chat names a custom agent). `chain.chat` wins when set. There is no separate `defaults.chat` block.

This fallback order is **not** the same as scoring’s *provider* order. `chain.llm_provider` still beats `defaults.scoring.llm_provider` (existing). Dedicated `defaults.scoring.fallback_list` still beats an investigation `chain.fallback_list` / `defaults.fallback_list`. That divergence is intentional: the new knobs exist so Sonnet scoring does not walk `premium`.

Compose/exec-summary *provider* order already matches this shape (`defaults.compose_provider` beats `chain.llm_provider`).

Summarization: if `defaults.summarization.fallback_list` is set, the summarization-local walk uses that catalog list (resolved onto the execution context **separately** from the investigator’s `ResolvedFallbackProviders`). Unset → calling agent’s effective list (ADR-0024). Still does not mutate the investigator; still no native-tool skip. A per-server Opus overlay still walks this same global summarization list (no per-server `fallback_list` in v1).

Explicit empty deprecated `fallback_providers: []` still means “no fallback.” A `fallback_list` that names an empty catalog entry is the same.

The controller never sees list names. After resolve, `ResolvedFallbackProviders` is a flat slice, same as today. Each YAML layer is expanded to a slice-or-nil **before** last-non-nil (a named list is a non-nil slice even when empty).

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
  resolveFullFallbackEntries()  (unchanged)
            │
            ▼
  tryFallback() / summarization-local walk  (unchanged)
```

## Core Concepts

### Fallback list (catalog entry)

A named, ordered `[]FallbackProviderEntry`. Same entry schema as today: required `provider`, optional `backend` (omitted → langchain). Lists are **flat** — no include of other lists.

### List selector (`fallback_list`)

A string naming a catalog entry. Expanded at resolve/validate time. Unknown names fail startup. This is the first-class default (`defaults.fallback_list`) and the override at every other layer.

### Deprecated inline list (`fallback_providers`)

Today’s field, kept on defaults / chain / stage / stage-agent only. Startup warns. Do not add it to new sites. Remove in a later change.

### Effective list

The concrete slice bound to one execution after hierarchy + expansion. Native-tool startup warnings and `tryFallback()` use this. The config viewer shows catalog + raw selectors, not the expanded per-agent walk.

### Named-agent pairing (`defaults.agents`)

A map of registry name → pair + list. Global setting for **any** named agent: explicit builtins (`WebResearcher`), implicit named agents (`ChatAgent`, optionally `SynthesisAgent`), and custom agents (`MyCustomKubeAgent`). Not for jobs that are not an agent you dispatch (summarization, scoring, compose, exec-summary field names). Identity stays in `agents:` / Go builtins. Override at the chain/stage/ref that names the agent.

### Implicit jobs

Scoring, compose, exec summary, and summarization keep **job** knobs under `defaults` (`scoring`, `compose_*`, `executive_summary_*`, `summarization`). Chat global pairing is `defaults.agents.ChatAgent` (or the custom chat agent name); `chain.chat` wins. Memory reflector reuses the scoring execution context, so it walks scoring’s effective list with no extra knob.

## Configuration (user-facing contract)

### Catalog

```yaml
fallback_lists:
  <list-name>:
    - provider: <registered provider>
      backend: langchain | google-native   # optional; omit → langchain
```

List names are non-empty YAML mapping keys. Duplicate names cannot exist (YAML map). Every catalog entry is structure-validated (provider exists, backend valid, `google-native` only with a Google provider). Credentials are required only for **referenced** lists.

**Referenced** means every `fallback_list` / `*_fallback_list` string that appears in YAML (including `defaults.fallback_list` and `defaults.summarization.fallback_list`), plus every deprecated `fallback_providers` entry (today’s rule). It is not a full per-execution resolve: a default list named in YAML is credential-checked even if every chain overrides it. Unnamed catalog entries are structure-only.

Also treat as referenced LLM providers (existing collector, extended): defaults/chain/stage-agent/sub-agent/side-path `llm_provider` fields, including **`executive_summary_provider`** (missing from the collector today — add it here), `defaults.agents.*.llm_provider`, and new compose/exec-summary names. Do not credential-check builtin WebResearcher’s `google-default` merely because the builtin exists; do check it when `defaults.agents.WebResearcher` (or a reachable ref) names it or when the builtin pair is used because the agent can run.

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
| compose | `compose_fallback_list` | **not added** | existing `compose_provider` + **`compose_backend`** (defaults and chain) |
| exec summary | `executive_summary_fallback_list` | **not added** | **`executive_summary_provider`** + **`executive_summary_backend`** (defaults and chain) |
| `defaults.summarization` | yes | **not added** | existing; **no** per-MCP-server `fallback_list`; **not** `defaults.agents` |

Keep current names (`compose_provider`, `executive_summary_provider`). Do not nest them into a new mapping that would migrate existing YAML.

Omitted `*_backend` / `llm_backend` still means langchain. Adding sibling backend on compose and exec summary **amends** ADR-0003’s “those provider fields have no sibling backend.”

Sub-agent short-form (`sub_agents: [WebResearcher]`) has no ref overlay. Effective list is last non-nil among defaults → chain → `defaults.agents.WebResearcher` → empty stage. Long-form objects may set `fallback_list` and the provider pair; those must be copied from `SubAgentRef` onto `StageAgentConfig` in the orchestrator runner (and `subAgentRefAllowedKeys` must allow `fallback_list` or long-form YAML fails parse).

### Inheritance that must not surprise

- **Stage-level fallback does not leak into chat, scoring, or exec summary** (chat/scoring: dedicated resolvers; exec summary: zero stage at runtime). Compose must match that: do not pass the action stage into resolve.
- **Parent orchestrator `llm_provider` / stage-agent `fallback_list` does not leak to sub-agents** (existing empty-stage dispatch).
- **`defaults.agents.<name>` beats chain-global list** so investigation `premium` does not poison WebResearcher. **Ref / stage-agent / `chain.chat` / `stage.synthesis` still win** when set.
- Synthesis uses `stage.synthesis.fallback_list` when set (copy onto the synthetic stage-agent, same as provider/backend today); otherwise `defaults.agents.SynthesisAgent` if set, else the investigation stage list after defaults → chain.

## Runtime behavior (unchanged)

All of the following stay as specified in ADR-0003 / 0017 / 0027 / 0028:

- Python identical retries, then Go fallback
- Sticky for the rest of the execution; new executions resolve fresh
- Skip already-attempted provider names
- Skip non-`google-native` entries when `RequiresNativeTools`
- Summarization-local walk does not mutate the investigator and does not apply native-tool skip
- Timeline `provider_fallback` events, execution `original_llm_*` columns, dashboard chips

Native-tool startup **warning** uses the **effective** list after named-list expansion, including `defaults.agents` and **all** `SubAgentRefs` sites (chain, stage, stage-agent, chat — not only chain-level refs as today).

## Observability and config viewer

- Timeline / metrics: no new event types. Provider names in existing fallback events already identify what was used.
- Config viewer (`GET /api/v1/system/config`): emit the `fallback_lists` catalog, `defaults.agents`, and the raw `fallback_list` / deprecated `fallback_providers` / `*_fallback_list` fields as written. Do not compute per-agent expanded walks in v1. Do **not** add `llm_provider` / `fallback_list` to `AgentView` (identity has no pairing YAML).
- Startup: warn on any use of `fallback_providers`.

## Out of scope

- Auto-sorting fallbacks by cost or by “tier”
- `include` of other named lists
- Per-MCP-server summarization `fallback_list`
- Process-wide provider cooldown / probe-unstick (ADR-0027 later)
- Changing retry counts, stickiness, or native-tool skip rules
- Removing `fallback_providers` in this change (deprecation only)
- Editing Sandbox YAML in this repository (separate `sandbox-sre` change once the product lands)
- Computed `effective_fallback_providers` in the config viewer

## Implementation Plan

Split so each PR leaves TARSy working. PR 1: configs without `fallback_lists` / `fallback_list` behave as today (aside from a deprecation warning if they already set `fallback_providers`). PR 2’s builtin `llm_provider: google-default` on native-tool agents is the documented exception.

### PR 1 — Catalog, existing four layers, deprecation

**Lands**

- Top-level `fallback_lists` on `TarsyYAMLConfig` **and** a field on in-memory `Config` (resolver / validator / viewer read `Config`, not the YAML struct)
- `fallback_list` on defaults, chain, stage, stage-agent
- Per-node expand-then-last-non-nil helper (name → copy of catalog slice; else deprecated inline; else nil)
- Load-time error if `fallback_list` and `fallback_providers` are both set on one node
- Startup warning on `fallback_providers` (including explicit empty slice)
- Validator: unknown list name; structure-validate every catalog entry (no credential check); credential-check referenced lists only (split today’s `validateFallbackProviders`); `collectReferencedLLMProviders` includes entries from referenced lists
- Native-tool warning uses expanded effective lists for those four layers

**Tests in this PR**

- Loader: catalog + selectors parse; omitted catalog is fine
- Validator: unknown list name, typo in an unused list, missing creds only on a referenced list, both fields on one node, empty named list
- Resolver: `fallback_list` at defaults / chain / stage / **stage-agent**; deprecated inline still works when `fallback_list` is unset; existing tests stay green

**Deferred:** `defaults.agents`, sub-agents, job knobs, builtin `llm_provider`, config viewer.

**Temporary gap:** operators can name lists and attach them to stage agents, but sub-agents and Sonnet side paths still inherit the default/chain list until PR 2. That matches today’s gap; it does not regress.

### PR 2 — `defaults.agents`, sub-agents, implicit jobs, summarization

**Lands**

- `defaults.agents` map (pair + list only) for any registered agent name. Unknown names fail load. Not added to `AgentConfig` / `agents:` YAML
- `BuiltinAgentConfig` gains `LLMProvider`; `mergeAgents` copies it. Builtin WebResearcher / CodeExecutor: `llm_provider: google-default` (keep `llm_backend: google-native`). **This changes short-form native-tool primaries** even when the operator never adopts named lists
- `SubAgentRef.fallback_list` + `subAgentRefAllowedKeys`; copy provider/backend/list in the orchestrator runner (investigation and chat share this path)
- Scoring / compose / exec summary: `fallback_list` (and compose/exec-summary sibling backend; new `defaults.executive_summary_*`). Chat: `defaults.agents.ChatAgent` + `chain.chat.fallback_list` (chain.chat wins). Synthesis: `defaults.agents.SynthesisAgent` optional + `stage.synthesis.fallback_list`
- **Runtime wiring, not only helpers:** session executor compose/exec-summary today call `ResolveAgentConfig`, not `ResolveComposeConfig` / `ResolveExecSummaryConfig`. Bind the new knobs on that path (or switch those stages to the dedicated resolvers). Compose must pass an empty stage so the action stage list does not leak. Synthesis copies `stage.synthesis.fallback_list` onto the synthetic stage-agent the same way it already copies provider/backend. Failed-exec best-effort `ResolveLLMPair` in `executeAgent` should include the builtin / `defaults.agents` layer
- `defaults.summarization.fallback_list`; store a resolved summarization list on the execution context; summarization-local walk uses it when set
- Hierarchy: defaults → chain → `defaults.agents.<name>` → stage → ref; job knobs after chain global; `chain.chat` / `stage.synthesis` / scoring/compose/exec-summary knobs last on those paths
- Native-tool warning covers `defaults.agents` and all `SubAgentRefs` sites
- `collectReferencedLLMProviders`: `defaults.agents`; `executive_summary_provider`; new side-path names; catalog entries from referenced lists

**Tests in this PR**

- Short-form WebResearcher under an Opus orchestrator (no chain `llm_provider`) resolves to `google-default` + `google-native`; with `defaults.agents.WebResearcher.fallback_list` walks that list even if `defaults.fallback_list` / `chain.fallback_list` is `premium`
- Custom `defaults.agents.MyCustomKubeAgent` applies when the chain/stage/ref omits a pair; a ref-level pair / list wins
- `chain.chat.fallback_list` beats `defaults.agents.ChatAgent`; custom `chain.chat.agent` uses `defaults.agents.<that name>`
- Sub-agent ref-level list is used, not the parent orchestrator’s stage-agent list
- Scoring/chat/compose/exec-summary do not inherit stage lists; `defaults.scoring.fallback_list` beats investigation `premium`
- Compose/exec-summary omit-backend still langchain; explicit `*_backend` works
- Summarization uses `defaults.summarization.fallback_list` when set; otherwise the agent list
- `agents.WebResearcher:` in user YAML still **replaces** the builtin (unchanged `mergeAgents`); pairing belongs in `defaults.agents`
- ADR-0017 warning cases with named lists, including a chat/stage sub-agent ref
- Long-form `sub_agents: [{name: WebResearcher, fallback_list: google-native}]` parses (`unknown field` must not fire)

**Deferred:** dashboard. e2e not required — fallback is already unit-tested at the controller; this PR is config binding.

### PR 3 — Config viewer + dashboard types

**Lands**

- API DTO: `fallback_lists` catalog + `defaults.agents` + raw selectors / deprecated inline / `*_fallback_list` on the same nodes as today’s config
- Dashboard TypeScript types and Config tab display (structured + YAML/JSON secondary views)

**Tests in this PR**

- `system_config` builder tests for the new fields
- Dashboard types compile; no visual redesign beyond showing the new keys

**Deferred:** none for the product feature. Sandbox YAML update is a separate repo PR after this ships.

## References

- [ADR-0003: LLM Provider Fallback](../adr/0003-llm-provider-fallback.md)
- [ADR-0017: Native Tool Fallback Safety](../adr/0017-native-tool-fallback-safety.md)
- [ADR-0024: Tool Summarization Provider](../adr/0024-tool-summarization-provider.md)
- [ADR-0027: Transient LLM Outage Handling](../adr/0027-llm-transient-outage.md)
- [ADR-0019: Read-Only Configuration Viewer](../adr/0019-config-viewer.md)
