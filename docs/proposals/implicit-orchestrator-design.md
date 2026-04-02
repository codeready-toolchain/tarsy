# Implicit Orchestrator

**Status:** Final — all decisions resolved in [implicit-orchestrator-questions.md](implicit-orchestrator-questions.md)

## Overview

Today, an agent must be declared `type: orchestrator` to gain sub-agent dispatch capabilities. This couples orchestration to agent identity rather than configuration, and makes it impossible for chat agents (or any other agent type) to dispatch sub-agents.

This proposal makes orchestration an **additive capability**: any agent that resolves non-empty `sub_agents` at runtime automatically receives orchestrator tools (`dispatch_agent`, `cancel_agent`, `list_agents`) and orchestrator prompt sections injected into its existing system prompt. The `AgentTypeOrchestrator` enum value is removed entirely.

**Primary goals:**

1. Remove `AgentTypeOrchestrator` — clean code, no dead type.
2. Gate orchestrator wiring on the presence of resolved sub-agents, not on type.
3. Make orchestration an injection layer, not a separate prompt path — any agent keeps its identity.
4. Enable chat agents to become orchestrators purely through configuration (PR2).

## Design Principles

- **Orchestration is a capability, not an identity.** An investigation agent with sub-agents is still an investigation agent. A chat agent with sub-agents is still a chat agent. They just gain orchestrator tools and instructions.
- **Single trigger.** The orchestrator wiring is gated on exactly one condition: the filtered sub-agent catalog is non-empty after resolving refs and intersecting with the registry. Raw `sub_agents` refs that resolve to zero runnable agents do not trigger orchestration. This applies uniformly across all execution paths.
- **Additive injection.** Orchestrator prompt sections (behavioral strategy, agent catalog, result delivery rules) are appended to the agent's existing system prompt. No separate prompt path.
- **Minimal config migration.** Existing configs require two changes: (1) remove `type: orchestrator` from agent definitions, and (2) replace references to the built-in `Orchestrator` agent with a suitable agent (e.g., `KubernetesAgent` or a custom agent) — orchestration is injected automatically when `sub_agents` are present. All other YAML syntax is unchanged.
- **Convention over configuration.** Sub-agents present = orchestrator mode. One source of truth.

## Architecture

### Orchestration trigger (before vs. after)

**Before:**
```
resolvedConfig.Type == AgentTypeOrchestrator?
  YES → resolve sub-agents, build SubAgentRunner, wrap tools, build orchestrator prompts
  NO  → plain agent
```

**After:**
```
refs := resolveSubAgents(chain, stage, agentConfig)
catalog := registry.Filter(refs.Names())
  catalog non-empty → build SubAgentRunner, wrap tools, inject orchestrator prompt sections
  catalog empty     → plain agent
```

### Prompt injection model

The separate `buildOrchestratorMessages` dispatch path is eliminated. Instead, orchestrator sections are injected into whatever system prompt the agent already has:

```
[Normal system prompt — investigation / chat / action / custom instructions]
+ [Orchestrator Strategy]           ← injected when SubAgentCatalog non-empty
+ [Available Sub-Agents catalog]    ← injected when SubAgentCatalog non-empty
+ [Result Delivery rules]           ← injected when SubAgentCatalog non-empty
```

The user message is unaffected — it stays whatever the agent type produces (investigation context, chat question, action findings, etc.).

### Chat sub-agent resolution (PR2)

`ChatConfig` gains a `SubAgents SubAgentRefs` field. Resolution follows the same precedence pattern as `ChatConfig.MCPServers`:

```
chatCfg.SubAgents > chain.SubAgents > (empty — no orchestration)
```

### Guardrails

The `orchestrator:` config block (max_concurrent_agents, agent_timeout, max_budget) is allowed on any agent definition. Resolution is unchanged: hardcoded defaults → `defaults.orchestrator` → `agentDef.Orchestrator`. The block is inert if the agent never resolves sub-agents.

### Circularity prevention

No explicit prevention needed. Sub-agents run via `SubAgentRunner` with `execCtx.SubAgent` set, which gives them a task-only prompt and no orchestrator tools — `SubAgentCatalog` and `SubAgentCollector` are never set on the sub-agent's `ExecutionContext`. A sub-agent cannot dispatch further sub-agents by runtime design, regardless of configuration. This invariant is enforced by dedicated tests (see PR1 test item 14).

## Core Concepts

| Concept | Before | After |
|---------|--------|-------|
| Orchestrator trigger | `type: orchestrator` on agent | Non-empty filtered sub-agent catalog at runtime |
| Prompt architecture | Separate `buildOrchestratorMessages` path | Injection layer onto existing prompt |
| `AgentTypeOrchestrator` | Required for orchestration | Removed |
| Built-in `Orchestrator` agent | Dedicated orchestrator identity | Removed — use any agent with sub-agents |
| Chat orchestrator | Not supported | Supported via `ChatConfig.SubAgents` or chain inheritance |
| Guardrails config | Only on `type: orchestrator` agents | Allowed on any agent |
| Sub-agent registry | Excludes `type: orchestrator` | No type exclusion needed |
| Memory support | Type-based check includes orchestrator | `AgentTypeDefault` covers implicit orchestrators |

## Implementation Plan

**Hard constraint:** After every PR, TARSy must be fully functional. Config changes are acceptable, but no PR may leave any feature broken. Final code must be clean — no dead or legacy code.

### PR1: Sub-agent-driven orchestration + type removal

Existing orchestrator chains already have `sub_agents` configured, so orchestration keeps working via the new trigger. Configs need updating: `type: orchestrator` becomes invalid and must be removed from agent definitions.

#### Config layer

1. **`pkg/config/enums.go`** — Remove `AgentTypeOrchestrator` from the enum and `IsValid()`.
2. **`pkg/config/builtin.go`** — Remove the built-in `Orchestrator` agent (`AgentNameOrchestrator` constant and its `BuiltinAgentConfig` entry). A dedicated orchestrator agent contradicts the "capability, not identity" principle — any agent with sub-agents gains orchestration automatically.
3. **`pkg/config/validator.go`** — Remove the rule tying `orchestrator:` block to `type: orchestrator`. Remove the `type == AgentTypeOrchestrator` check in `validateSubAgentRefs`.
4. **`pkg/config/sub_agent_registry.go`** — Remove the `agent.Type == AgentTypeOrchestrator` exclusion from `BuildSubAgentRegistry`.

#### Executor layer

5. **`pkg/queue/executor.go`** — Replace `if resolvedConfig.Type == AgentTypeOrchestrator` with: resolve sub-agent refs, filter against the registry, and gate on the filtered catalog being non-empty. The orchestrator wiring block stays the same internally.
6. **`pkg/queue/executor_memory.go`** — Remove `AgentTypeOrchestrator` from `agentTypeSupportsMemory` switch (implicit orchestrators are `AgentTypeDefault`, already covered).

#### Prompt layer

7. **`pkg/agent/prompt/builder.go`** — Remove the `if execCtx.Config.Type == AgentTypeOrchestrator` dispatch in `BuildFunctionCallingMessages`. Instead, after building messages via the normal path (investigation/chat/action/sub-agent), inject orchestrator sections into the system message when `SubAgentCatalog` is non-empty.
8. **`pkg/agent/prompt/orchestrator.go`** — Refactor from a standalone message builder into an injection helper (e.g., `InjectOrchestratorSections(systemContent, catalog) string`). The orchestrator behavioral instructions, catalog formatting, and result delivery constants remain unchanged.

#### Controller layer

9. **`pkg/agent/controller/factory.go`** — Remove the `AgentTypeOrchestrator` case (was identical to `AgentTypeDefault` → `IteratingController`).

#### Config files

10. **`deploy/config/tarsy.yaml`** — Remove `type: orchestrator` from agent definitions. Replace `name: "Orchestrator"` stage agent references with a suitable agent (e.g., `KubernetesAgent` or a custom agent defined in `agents:`).
11. **`deploy/config/tarsy.yaml.example`** — Remove `type: orchestrator` from examples. Replace built-in `Orchestrator` references. Add `orchestrator:` guardrails block example to an agent definition (currently missing from the example file — see Q4).
12. **`test/e2e/testdata/configs/`** — Remove `type: orchestrator` from all test configs. Update `builtin-orchestrator/tarsy.yaml` to use a custom agent instead of the removed built-in.
13. **E2E golden files** — Update as needed.

#### Tests

14. Update unit tests across: `config/enums_test.go`, `config/validator_test.go`, `config/builtin_test.go`, `config/sub_agent_registry_test.go`, `config/loader_test.go`, `agent/config_resolver_test.go`, `agent/controller/factory_test.go`, `agent/prompt/builder_test.go`, `agent/prompt/orchestrator_test.go`, `queue/executor_memory_test.go`, `queue/executor_integration_test.go`. Remove or replace all uses of `AgentNameOrchestrator` constant (`services/*_test.go`, `api/handler_trace_test.go`). Add recursion-safety tests verifying `SubAgentRunner` sets `execCtx.SubAgent` and never sets `SubAgentCatalog`/`SubAgentCollector` — sub-agents must not gain orchestrator tools or dispatch further sub-agents.
15. Update E2E orchestrator tests in `test/e2e/orchestrator_test.go`.

### PR2: Chat orchestrator support (additive, opt-in)

Only configs that add `chat.sub_agents` are affected.

1. **`pkg/config/types.go`** — Add `SubAgents SubAgentRefs` to `ChatConfig`.
2. **`pkg/config/validator.go`** — Validate `ChatConfig.SubAgents` refs.
3. **`pkg/agent/config_resolver.go`** — In `ResolveChatAgentConfig`, resolve sub-agents: `chatCfg.SubAgents` > `chain.SubAgents` > nil.
4. **`pkg/queue/chat_executor.go`** — Add `subAgentRegistry` field. Wire `SubAgentRunner` + `CompositeToolExecutor` + `SubAgentCollector` + `SubAgentCatalog` when resolved sub-agents are non-empty.
5. **`pkg/agent/prompt/builder.go`** — The injection model from PR1 handles this automatically — chat system prompt gets orchestrator sections injected when `SubAgentCatalog` is non-empty.
6. Tests and config examples.
