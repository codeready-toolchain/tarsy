# Named Fallback Lists — Design Questions

**Status:** All decisions made
**Related:** [Design document](named-fallback-lists-design.md)

---

## Q1: Named reusable lists vs only expanding inline lists

ADR-0003 already allows replacing `fallback_providers` at chain / stage / stage-agent. The Sandbox problem is not “you cannot set a different list” — it is that **one ordered list is shared**, and copying an 8-entry list onto every Opus agent, Sonnet remediator, and Gemini sub-agent is how that sharing survives. Sub-agents, scoring, chat, compose, and exec summary cannot even set a list today.

### Option A: Named catalog + selector (`fallback_lists` + `fallback_list`)

- **Pro:** Define `premium` / `mid` / `google-native` once; every agent names the preference it wants.
- **Pro:** Matches how operators already think (“investigation stays on frontier models”).
- **Pro:** Named lists stay maintainable; missing selector sites (sub-agents, scoring, …) still need fields either way.
- **Con:** New config concept and validation (unknown names, unused lists).
- **Con:** Slightly more YAML surface than “just add the missing inline fields.”

**Decision:** Option A — named reusable lists plus a selector. Missing inline fields are necessary but not enough; multiple lists stay maintainable only if they are named and reused.

_Considered and rejected: Option B (copy-paste inline lists drift), Option C (invents a tier model and fights operator-ordered walks)._

---

## Q2: Where is the catalog defined?

### Option A: Top-level `fallback_lists` in `tarsy.yaml`

Sibling of `agents`, `mcp_servers`, `agent_chains`, `defaults`.

- **Pro:** Lists are a named registry, like agents and MCP servers — not a “default value.”
- **Pro:** Easy to find; not buried under `defaults`.
- **Con:** Another root key.

**Decision:** Option A — catalog at the root of `tarsy.yaml`. `defaults` only selects a list (or keeps an inline default).

_Considered and rejected: Option B (a catalog is not a default and looks inheritable), Option C (fallback order is orchestration, not a provider property)._

---

## Q3: What happens to today’s `defaults.fallback_providers`?

### Option A: First-class default is `fallback_list`; `fallback_providers` is deprecated compatibility

The new default list is a **name**, not a second anonymous list.

Happy path (no deprecated fields):

```yaml
fallback_lists:
  premium: [...]
  mid: [...]

defaults:
  fallback_list: premium
```

`defaults.fallback_list` is the same selector every other layer uses. The catalog holds the lists; `defaults` only points at one. No reserved catalog name (`default`). One-offs after migration are extra named catalog entries, not inline slices.

Deprecated path: `fallback_providers` still loads at any layer with today’s meaning. Startup warns. A config that never defines `fallback_lists` keeps working. A fully migrated config never mentions `fallback_providers`. Removal is a later change, not this design.

Per node: `fallback_list` → expand catalog; else `fallback_providers` → inline slice; else inherit. Both set is Q4.

- **Pro:** Clean new contract (`fallback_lists` + `fallback_list`) with no anonymous list in the happy path.
- **Pro:** Zero migration; existing YAML keeps working until operators opt in.
- **Pro:** No magic rewrite into `fallback_lists.default`.
- **Con:** Two spellings exist until the deprecated field is removed.
- **Con:** Startup warning and later removal need a follow-up.

**Decision:** Option A — `defaults.fallback_list` is the real default; `fallback_providers` is deprecated compatibility (warn, still honor, remove later). Mixing both on one node is Q4.

_Considered and rejected: Option B (rewrite inline lists into a reserved `default` catalog name — collisions and magic), Option C (require named lists immediately — breaks every current config)._

---

## Q4: Both `fallback_list` and `fallback_providers` on the same YAML node

### Option A: Load-time error

- **Pro:** One way to set a layer; no hidden precedence.
- **Pro:** Catches copy-paste leftovers.
- **Con:** Slightly stricter YAML.

**Decision:** Option A — each node inherits, names a list, or (deprecated) inlines a list. Mixing new + old on the same node is a migration mistake, not precedence.

_Considered and rejected: Option B (silent ignore of `fallback_list` is hard to debug), Option C (hidden compose/append semantics)._

---

## Q5: Which layers get a selector in v1?

### Option A: `fallback_list` on every primary-model site; deprecated `fallback_providers` only where it already exists

`fallback_list` is added wherever a primary is chosen (`llm_provider`, `compose_provider`, `executive_summary_provider`), plus **stage** (has fallback today, no `llm_provider`). Summarization is Q8. Named-agent global pairing is Q13 (`defaults.agents.<name>` for any registered agent — explicit builtin, implicit, or custom). Not the `agents:` identity map. Builtin Go still sets WebResearcher / CodeExecutor `llm_provider` + `llm_backend` (Q6).

Deprecated `fallback_providers` is **not** added to new sites.

| Site | `fallback_list` | `fallback_providers` |
|------|-----------------|----------------------|
| defaults, chain, stage, stage-agent | yes | keep (deprecated) |
| sub-agent, scoring, chat, synthesis, compose, exec summary | yes | not added |

- **Pro:** Anywhere you pick a primary, you can pick the walk that matches it.
- **Pro:** Solves Sandbox in one design: Opus clones, Sonnet remediator, Gemini native tools, scoring.
- **Pro:** New surfaces never grow the field we are deprecating.
- **Con:** More fields to thread through structs, validator, config viewer, and tests.

**Decision:** Option A — `fallback_list` on all primary-model sites (and stage); `fallback_providers` stays only on defaults / chain / stage / stage-agent.

_Considered and rejected: Option B (four layers only — leaves sub-agents and scoring on the investigation walk), Option C (four layers + sub-agents only — scoring/compose/exec summary still fall up to Opus)._

---

## Q6: Selector (and optional model pair) on agent definitions (`agents.<name>`)?

Agent definitions already have `llm_backend` and native tools, but **not** `llm_provider`. Primaries are chosen at chain/stage/ref. Short-form `WebResearcher` can resolve to defaults Opus plus agent-def `google-native` — a bad pair. The parent orchestrator’s `llm_provider` does **not** leak to sub-agents; chain-level provider would.

### Option A: Agent definition is a pairing layer — `llm_provider` + `llm_backend` + `fallback_list`

Hierarchy (last non-empty wins), same slot as today’s agent-def backend:

```
defaults → agent definition → chain → stage → stage-agent / sub-agent ref
```

`llm_provider` and `llm_backend` are one pairing layer, like every other YAML node ([ADR-0003](../adr/0003-llm-provider-fallback.md)): a node that names a provider without a sibling backend resolves backend to `langchain`. Omitted provider with a backend still overlays backend only (today’s WebResearcher). Builtin WebResearcher / CodeExecutor set **both** (`google-default` + `google-native`) so native tools stay on Gemini.

`fallback_list` is allowed on the definition (new site → no deprecated `fallback_providers`). Catalog names stay out of builtins — overlays add `fallback_list: google-native`. `llm_provider: google-default` on builtins is fine (`google-default` is a builtin provider).

- **Pro:** Short-form `WebResearcher` in a chain whose orchestrator is Opus still uses Gemini + the agent’s named list.
- **Pro:** One overlay line for `fallback_list` instead of every `sub_agents` entry.
- **Pro:** Provider and backend stay a pair; no new pairing rule.
- **Con:** Chain-level `llm_provider` / `fallback_list` still overrides the definition (explicit layer, not parent-orchestrator leak). Sandbox should keep using per-agent/ref primaries if it wants short-form native-tool agents.
- **Con:** User overlays that set `fallback_list` must define that name in the catalog.

**Decision (amended by Q13):** Builtin WebResearcher / CodeExecutor set `llm_provider: google-default` + `llm_backend: google-native` **in Go** so short-form refs get a safe primary without YAML. Catalog names stay out of builtins. **Do not** add `llm_provider` / `fallback_list` on the `agents:` identity map — not for builtins and not for custom agents. Global pairing for any named agent is `defaults.agents.<name>` (Q13). A chain/stage/ref that names the agent wins when set.

_Considered and rejected: Option B (no definition-level selector — would keep repeating Gemini + list on every WebResearcher ref). Q13 is the YAML “set once” path instead of pairing on `agents:`._

---

## Q7: Side-path selectors (scoring, chat, compose, exec summary, synthesis)

**Skipped — moot after Q5.** Q5 already puts `fallback_list` (and not `fallback_providers`) on scoring, chat, synthesis, compose, and exec summary. Inheritance of chain lists into those paths is Q9.

---

## Q8: Does tool summarization get its own list?

[ADR-0024](../adr/0024-tool-summarization-provider.md) Q4 **rejected** a dedicated summarization fallback list: walk the investigator’s `fallback_providers` locally so a dead Flash still tries the next family member. In Sandbox that walk is Opus-first, so a Flash summarizer fails into Sonnet then Opus — the expensive outcome ADR-0024 wanted to avoid by *having* a walk at all.

### Option A: Optional `defaults.summarization.fallback_list` only (no per-server overlay)

Unset → keep today’s “use the calling agent’s effective list.” Set → summarization-local walk uses that named list (still does not mutate the investigator, still no native-tool skip). No `fallback_list` on per-MCP-server summarization blocks in v1.

- **Pro:** Flash can stay in the cheap/native band without a second catalog type.
- **Pro:** Opt-in; current deployments unchanged.
- **Pro:** One global cheap walk covers kubectl and security dumps; a server that needs Opus summarization already names an Opus `llm_provider` and can leave this unset.
- **Con:** Revisits an ADR-0024 decision.
- **Con:** Cannot pick a different summarization walk per MCP server until a later overlay.

**Decision:** Option A — `defaults.summarization.fallback_list` only; no per-server overlay in v1.

_Considered and rejected: Option B (agent list unchanged — premium investigation lists make Flash fail into Opus), Option C (required named list — breaks configs with no catalog)._

---

## Q9: Should a chain/stage investigation list apply to scoring, chat, compose, and exec summary?

Today: **yes for chain** (defaults → chain), **no for stage** (tests: stage fallback must not leak into chat/scoring). Compose and exec summary follow the same defaults → chain resolve as scoring/chat. Exec summary has **no** `defaults.*` knob (only `chain.executive_summary_provider`). Compose has `compose_provider` with **no sibling backend**.

### Option A: Keep inheriting when unset; give every implicit builtin a `defaults.*` pair + list, overridable on the chain

Inheritance stays as today if a side path does not set its own list. Dedicated selectors override when set (including `defaults.scoring.fallback_list` beating an investigation `premium` list).

Fill the missing knobs for builtins that operators do not put under `agents:`:

| Builtin | `defaults.*` (new or extend) | Chain override |
|---------|------------------------------|----------------|
| Scoring | existing `scoring.llm_provider` / `llm_backend` + `fallback_list` | existing `scoring.*` + `fallback_list` |
| Summarization | Q8: `summarization.fallback_list` | none (Q8) |
| Compose | existing `compose_provider` + **`compose_backend`** + **`compose_fallback_list`** | same three fields (provider already exists) |
| Exec summary | **`executive_summary_provider`** + **`executive_summary_backend`** + **`executive_summary_fallback_list`** | same three (provider already exists) |
| Chat | **`defaults.agents.ChatAgent`** (or `defaults.agents.<chain.chat.agent>`) | existing `chain.chat.*` + `fallback_list` (wins) |
| Synthesis | **`defaults.agents.SynthesisAgent`** (optional) | stage `synthesis.*` already has provider/backend; add `fallback_list` |

Omitted `*_backend` still means langchain (same pairing as ADR-0003). Adding a sibling backend **amends** “compose/exec-summary provider has no sibling backend” — the field exists now; omit is still langchain.

Keep current names (`compose_provider`, `executive_summary_provider`); do not nest them into a new mapping that would migrate existing YAML.

- **Pro:** Sandbox can set Sonnet + `mid` once under `defaults` for scoring, compose, and exec summary; chains only override when needed.
- **Pro:** Provider, backend, and list stay a pair on every implicit builtin.
- **Pro:** Unset still inherits investigation defaults/chain (no silent behavior change).
- **Con:** More `defaults.*` keys. Chat/synthesis defaults are unused if every chain already sets them.

**Decision:** Option A — inherit when unset; add `defaults` + chain (or stage, for synthesis) provider / backend / `fallback_list` for compose and exec summary; scoring and summarization only gain `fallback_list` on blocks they already have. Chat global pairing is `defaults.agents.ChatAgent` (Q13), not a separate `defaults.chat` block; `chain.chat` wins when set. Synthesis global pairing may use `defaults.agents.SynthesisAgent`; `stage.synthesis` wins.

_Considered and rejected: Option B (stop inheriting chain lists — behavior change for existing chain `fallback_providers`), Option C (chat inherits chain, others do not — three stories)._

---

## Q10: May a named list include other lists?

### Option A: No — lists are flat

- **Pro:** Trivial validation, no cycles, obvious order.
- **Pro:** Sandbox’s three lists (`premium`, `mid`, `google-native`) do not need includes.
- **Con:** Shared tails (all lists end with the same two Gemini entries) are copied.

**Decision:** Option A — lists are flat. Revisit includes if catalogs grow large.

_Considered and rejected: Option B (`include` — cycles, diamonds, prepend vs append)._

---

## Q11: Validate catalog entries that no execution references?

Unused LLM providers already skip credential checks until referenced. Fallback entries on the live default list are always referenced.

### Option B: Structure-validate all lists; credential-check only referenced lists

Referenced = default list + every `fallback_list` name that appears on a reachable execution (including scoring/chat/compose/sub-agents).

- **Pro:** Same as unused providers: labs can keep a spare list in YAML without wiring keys.
- **Con:** A list that is only referenced after a config edit can fail on the next boot — still load-time, not mid-session.

**Decision:** Option B — unknown providers/backends fail in any catalog entry (typos). Credentials required only for lists some resolved execution can walk.

_Considered and rejected: Option A (unused lists with missing keys fail boot), Option C (ignore unused lists entirely — typos hide until selected)._

---

## Q12: What should the config viewer show?

### Option A: Catalog + raw selectors (`fallback_list` name and/or inline entries) as written

- **Pro:** Matches “effective YAML” elsewhere ([ADR-0019](../adr/0019-config-viewer.md) is post-merge config, not fully resolved agent snapshots).
- **Pro:** Small DTO change.
- **Con:** Operator must mentally expand `fallback_list: mid`.

**Decision:** Option A — catalog + raw selectors. Per-agent expansion is a follow-up if names are not enough.

_Considered and rejected: Option B (computed effective lists in the snapshot — resolver-shaped DTO, easy to drift), Option C (catalog only — new selectors invisible)._

---

## Q13: How to set a global pair + list on a named agent without replacing the builtin

Q6’s “one overlay line” under `agents.WebResearcher` is the wrong map: `agents:` is identity, and `mergeAgents` **replaces** a builtin of the same name. Operators need a global pairing site that does not touch tools or instructions.

### Option D: `defaults.agents.<name>` pair + list (any registered agent)

Identity stays in Go builtins / `agents:` YAML (`llm_backend`, native tools, instructions). Global pairing for **any** named agent — explicit builtin, implicit (`ChatAgent`), or custom — lives under `defaults.agents`. Override at the chain/stage/ref that names the agent. Job-type pairing stays on existing job blocks (scoring, summarization, compose, exec summary).

```yaml
defaults:
  llm_provider: vertexai-claude-opus
  fallback_list: premium
  scoring:
    llm_provider: vertexai-claude-sonnet
    fallback_list: mid
  summarization:
    llm_provider: google-default
    llm_backend: google-native
    fallback_list: google-native
  compose_provider: vertexai-claude-sonnet
  compose_fallback_list: mid
  executive_summary_provider: vertexai-claude-sonnet
  executive_summary_fallback_list: mid
  agents:
    WebResearcher:
      llm_provider: google-default   # omit → builtin google-default
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

`defaults.agents` is pair + list only (not `AgentConfig`). Unknown names fail at load. `agents:` YAML does **not** gain `llm_provider` / `fallback_list`.

Chat: `defaults.agents.ChatAgent` (or `defaults.agents.<chain.chat.agent>` if chat uses a custom agent). `chain.chat` wins when set. No separate `defaults.chat` block.

- **Pro:** One `defaults` section holds global pairing for every named agent; identity is never replaced.
- **Pro:** Same rule for explicit builtins, implicit named agents, and custom agents. Chain/stage/ref overrides when set.
- **Pro:** No `mergeAgents` change. MCP overlays stay replace.
- **Con:** Scoring/compose/exec-summary/summarization stay as job keys (they are jobs, not “add this agent to a chain”). Pairing for those is not under `defaults.agents`.

**Decision:** Option D — `defaults.agents.<name>` for any registered agent; no pairing fields on `agents:` identity; job knobs unchanged except chat uses `defaults.agents.ChatAgent` instead of `defaults.chat`.

_Considered and rejected: Option A (full replace of builtin identity), Option B (field-merge on `agents:` — wrong map, `skills: []` vs omit), Option C (magic catalog name on the builtin)._
