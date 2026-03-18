# Agent Skills — Design Document

**Status:** All questions decided (Q1–Q5) — see [agent-skills-design-questions.md](agent-skills-design-questions.md)

**Prior art:** [Sketch](agent-skills-sketch.md) and [sketch questions (Q1–Q7)](agent-skills-questions.md) — all conceptual decisions are finalized there.

## Overview

Agent Skills add modular, reusable knowledge blocks to TARSy agents. The [sketch document](agent-skills-sketch.md) established the conceptual design: skills follow the industry-standard `SKILL.md` format, are discovered from the filesystem at startup, presented as a catalog in the system prompt, and loaded on-demand via a `load_skill` tool. This document specifies the implementation: new types, modified components, data flow, and phased delivery.

## Design Principles

1. **Follow established patterns** — SkillRegistry mirrors AgentRegistry/MCPServerRegistry. SkillToolExecutor mirrors CompositeToolExecutor. No new architectural concepts.
2. **Zero config for the common case** — Drop a `SKILL.md` file, all agents see it. No YAML changes required.
3. **Startup-only I/O** — Skills are loaded from disk into memory at config initialization. Runtime `load_skill` reads from the in-memory registry (no filesystem access).
4. **Minimal blast radius** — New fields on AgentConfig are optional. Existing configs work unchanged.

## Architecture

### New Components

#### 1. `pkg/config/skill.go` — SkillConfig + SkillRegistry

```go
// SkillConfig holds a parsed SKILL.md file.
type SkillConfig struct {
    Name        string // From frontmatter
    Description string // From frontmatter (catalog entry)
    Body        string // Markdown body (loaded on demand)
}

// SkillRegistry stores skills in memory with thread-safe access.
// Same pattern as AgentRegistry, MCPServerRegistry.
type SkillRegistry struct {
    skills map[string]*SkillConfig
    mu     sync.RWMutex
}
```

Methods: `Get(name)`, `GetAll()`, `Has(name)`, `Len()`, `Names()`.

#### 2. `pkg/agent/skill/tool_executor.go` — SkillToolExecutor

Wraps an inner `agent.ToolExecutor` and intercepts `load_skill` calls. Everything else passes through to the inner executor. Same wrapping pattern as `orchestrator.CompositeToolExecutor`.

```go
type SkillToolExecutor struct {
    inner        agent.ToolExecutor
    registry     *config.SkillRegistry
    allowedNames map[string]struct{} // On-demand skills for this agent
}
```

Methods: `ListTools(ctx)` (prepends `load_skill` to inner tools), `Execute(ctx, call)` (intercepts `load_skill`, delegates rest), `Close()` (delegates to inner).

The `load_skill` tool definition (shown as JSON Schema for clarity; implementation uses `agent.ToolDefinition` Go structs like `orchestrationTools` in `orchestrator/tool_executor.go`):

```json
{
  "name": "load_skill",
  "description": "Load domain knowledge skills by name. Returns the full skill content.",
  "parameters": {
    "type": "object",
    "properties": {
      "names": {
        "type": "array",
        "items": {"type": "string"},
        "description": "Skill names to load"
      }
    },
    "required": ["names"]
  }
}
```

**Partial failure semantics:** When `load_skill` is called with a mix of valid and invalid names, it returns the content of all valid skills and appends an error note listing the invalid names with the available skill catalog. This is non-fatal — the LLM receives the valid content and can retry or proceed. If *all* names are invalid, the result is an error (`IsError: true`) listing available skills.

#### 3. `pkg/config/skill_loader.go` — SKILL.md parsing

```go
// LoadSkills scans configDir/skills/*/SKILL.md, parses each file,
// and returns a SkillRegistry.
func LoadSkills(configDir string) (*SkillRegistry, error)
```

Parsing: split file content on `---` delimiters, `yaml.Unmarshal` the frontmatter block into a struct, keep the remainder as `Body`. No external library needed — the frontmatter format is trivial (`---\n<yaml>\n---\n<markdown>`).

If the `skills/` directory doesn't exist, return an empty registry (skills are optional).

### Modified Components

#### 1. `AgentConfig` — two new optional fields

```go
type AgentConfig struct {
    // ... existing fields ...

    // Skills allowlist. nil = all skills available (default).
    // Empty slice = no skills. Non-nil = only these skills.
    Skills *[]string `yaml:"skills,omitempty"`

    // RequiredSkills are injected into the system prompt (Tier 2.5).
    // These are excluded from the on-demand catalog.
    RequiredSkills []string `yaml:"required_skills,omitempty"`
}
```

`skills` and `required_skills` are agent-level only (Q1 decision). No chain/stage overrides.

**Built-in agents:** `BuiltinAgentConfig` (in `builtin.go`) also needs `Skills` and `RequiredSkills` fields, and `mergeAgents()` (in `merge.go`) must copy them when converting built-in agents to `AgentConfig`. Built-in agents default to nil (all skills available), so no changes to existing built-in definitions are needed.

#### 2. `Config` — new SkillRegistry field

```go
type Config struct {
    // ... existing fields ...
    SkillRegistry *SkillRegistry
}
```

Stats() gains a `Skills int` field.

#### 3. `loader.go` — skill loading in `load()`

After loading YAML files and building other registries, call `LoadSkills(configDir)` and attach the result to `Config.SkillRegistry`.

#### 4. `validator.go` — skill validation

New `validateSkills()` method:
- For each agent with `Skills` allowlist: verify every name exists in SkillRegistry.
- For each agent with `RequiredSkills`: verify every name exists in SkillRegistry and is within the agent's effective skill scope.
- Warn if SkillRegistry is empty but agents reference skills.

#### 5. `ResolvedAgentConfig` — resolved skill data

Skills are pre-resolved in `config_resolver.go` and carried on `ResolvedAgentConfig` (Q2 decision). PromptBuilder only formats the data.

```go
type ResolvedAgentConfig struct {
    // ... existing fields ...

    // RequiredSkillContent: skill bodies to inject at Tier 2.5.
    // Pre-resolved at config resolution time.
    RequiredSkillContent []ResolvedSkill

    // OnDemandSkills: skills available via load_skill tool.
    // Names + descriptions for the catalog prompt. Bodies loaded on tool call.
    OnDemandSkills []SkillCatalogEntry
}

type ResolvedSkill struct {
    Name string
    Body string
}

type SkillCatalogEntry struct {
    Name        string
    Description string
}
```

#### 6. `config_resolver.go` — skill resolution

New `resolveSkills()` function called within `ResolveAgentConfig`:

1. Determine effective skill set: agent's `Skills` allowlist (nil → all, empty → none, list → filter).
2. Split into required (agent's `RequiredSkills`) and on-demand (everything else).
3. Populate `RequiredSkillContent` and `OnDemandSkills` on `ResolvedAgentConfig`.

Same logic applies to `ResolveChatAgentConfig`. `resolveSkills()` accesses the `SkillRegistry` via the `cfg *config.Config` parameter already passed to all `Resolve*` functions, and reads `Skills`/`RequiredSkills` from the agent definition obtained via `cfg.GetAgent()`.

Skill support covers investigation (default), orchestrator, sub-agent, action, and chat agents (Q3 decision). These all call `ComposeInstructions()`, so skills appear in their prompts automatically. Scoring, synthesis, and exec-summary agents are excluded — they are meta-agents that don't investigate alerts.

#### 7. `prompt/instructions.go` — Tier 2.5 and 2.6

`ComposeInstructions` gains two new sections between MCP instructions and custom instructions:

```go
// Tier 2.5: Required skill content
for _, skill := range execCtx.Config.RequiredSkillContent {
    sections = append(sections, formatRequiredSkill(skill))
}

// Tier 2.6: On-demand skill catalog
if len(execCtx.Config.OnDemandSkills) > 0 {
    sections = append(sections, formatSkillCatalog(execCtx.Config.OnDemandSkills))
}
```

Required skills are wrapped with a `## Skill: {name}` header (Q4 decision), consistent with MCP's `## {serverID} Instructions` pattern.

`formatSkillCatalog` generates the behavioral nudge + catalog. The template (from [sketch Q7](agent-skills-questions.md#q7)):

```
## Available Domain Knowledge

Before starting your task, scan the skill descriptions below and load any
that match the current context (alert type, environment, workload type).
These contain domain-specific knowledge that may not be in your training data.

- **{name}**: {description}
- ...

Use the `load_skill` tool to load relevant skills by name before proceeding.
You can load multiple skills in one call. If no skill description matches
your current task, do not load any.
```

The same catalog and nudge is used by `ComposeInstructions` (investigation, orchestrator, action, sub-agent) and `ComposeChatInstructions` (chat). The wording "Before starting your task" is intentionally generic — it applies equally to investigating an alert, executing a remediation action, or answering a follow-up question.

#### 8. `executor_helpers.go` / `executor.go` — tool executor wrapping

After creating the MCP tool executor (and optionally wrapping with CompositeToolExecutor for orchestrators), wrap with SkillToolExecutor if the agent has on-demand skills:

```go
// existing: toolExecutor = createToolExecutor(ctx, ...)
// existing: if orchestrator { toolExecutor = orchestrator.NewCompositeToolExecutor(...) }

// NEW: wrap with skill tool if on-demand skills exist
if len(resolvedConfig.OnDemandSkills) > 0 {
    toolExecutor = skill.NewSkillToolExecutor(toolExecutor, e.cfg.SkillRegistry, onDemandNames)
}

execCtx.ToolExecutor = toolExecutor
```

The same wrapping applies in three places:
- **`executor.go` `executeAgent()`** — investigation and action agents
- **`chat_executor.go`** — chat agents
- **`orchestrator/runner.go` `createSubAgentToolExecutor()`** — sub-agents spawned by orchestrators. The orchestrator's `ResolveAgentConfig` already resolves skills for the sub-agent (via `r.deps.Config.SkillRegistry`); the `SkillToolExecutor` wrapping must happen inside `createSubAgentToolExecutor` after the MCP executor is built, using the sub-agent's own resolved skill data.

The wrapping order is: MCP → Orchestrator → Skill (outermost). This means `load_skill` is intercepted before reaching the orchestrator or MCP layers.

`load_skill` calls are tracked identically to MCP tool calls (Q5 decision): interaction records, timeline events, the full pipeline. The existing controller loop handles this with zero special-case code.

### Data Flow

```
Startup:
  configDir/skills/*/SKILL.md → LoadSkills() → SkillRegistry (in Config)

Agent initialization:
  AgentConfig.Skills + AgentConfig.RequiredSkills + SkillRegistry
    → resolveSkills()
    → ResolvedAgentConfig.{RequiredSkillContent, OnDemandSkills}

Prompt construction:
  ResolvedAgentConfig.RequiredSkillContent → Tier 2.5 (injected body)
  ResolvedAgentConfig.OnDemandSkills → Tier 2.6 (catalog + nudge)

Tool registration:
  OnDemandSkills.Names → SkillToolExecutor (wraps inner executor)

Runtime:
  LLM calls load_skill(names: [...])
    → SkillToolExecutor.Execute() intercepts
    → reads from SkillRegistry (in-memory)
    → returns concatenated skill bodies as tool result
```

### Prompt Tier Diagram (Final)

```
Tier 1:   General SRE Instructions        (generalInstructions — hardcoded)
Tier 2:   MCP Server Instructions          (from MCPServerRegistry, per server ID)
   ↕      Unavailable server warnings      (from FailedServers)
Tier 2.5: Required skill content           (from ResolvedAgentConfig.RequiredSkillContent)
Tier 2.6: On-demand skill catalog          (from ResolvedAgentConfig.OnDemandSkills)
Tier 3:   Agent Custom Instructions        (from AgentConfig.CustomInstructions)
   +      Mode-specific blocks             (orchestrator catalog, action safety, etc.)
```

## Implementation Plan

### Phase 1: Foundation — SkillRegistry + Config Loading

**New files:**
- `pkg/config/skill.go` — SkillConfig, SkillRegistry
- `pkg/config/skill_loader.go` — LoadSkills(), SKILL.md parsing
- `pkg/config/skill_loader_test.go`
- `pkg/config/skill_test.go` (registry tests)
- `pkg/config/testdata/skills/*/SKILL.md` — test fixture skills for all phases

**Modified files:**
- `pkg/config/config.go` — add SkillRegistry field, update Stats()
- `pkg/config/loader.go` — call LoadSkills() in load()
- `pkg/config/agent.go` — add Skills, RequiredSkills fields to AgentConfig
- `pkg/config/builtin.go` — add Skills, RequiredSkills fields to BuiltinAgentConfig
- `pkg/config/merge.go` — copy Skills, RequiredSkills in mergeAgents()
- `pkg/config/validator.go` — add validateSkills() (required skill not in allowlist, empty registry, etc.)
- `pkg/config/errors.go` — add ErrSkillNotFound sentinel error

**Verification:** Config loads with and without `skills/` directory. Skills are discoverable via registry. Validation catches invalid references and edge cases (required skill outside allowlist, referencing nonexistent skills).

### Phase 2: Prompt Integration — Tiers 2.5 and 2.6

**New files:**
- `pkg/agent/prompt/skills.go` — formatRequiredSkill(), formatSkillCatalog()
- `pkg/agent/prompt/skills_test.go`

**Modified files:**
- `pkg/agent/context.go` — add ResolvedSkill, SkillCatalogEntry types to ResolvedAgentConfig
- `pkg/agent/config_resolver.go` — add resolveSkills(), call from ResolveAgentConfig + ResolveChatAgentConfig
- `pkg/agent/prompt/instructions.go` — insert Tier 2.5 and 2.6 in ComposeInstructions + ComposeChatInstructions
- `pkg/agent/prompt/instructions_test.go` — verify tier ordering with skills

**Verification:** System prompts contain required skill content and on-demand catalog in correct order. Catalog respects agent skill allowlists.

### Phase 3: load_skill Tool + Executor Wiring

**New files:**
- `pkg/agent/skill/tool_executor.go` — SkillToolExecutor
- `pkg/agent/skill/tool_executor_test.go`

**Modified files:**
- `pkg/queue/executor.go` — wrap tool executor with SkillToolExecutor for investigation and action agents
- `pkg/queue/executor_helpers.go` — helper to extract on-demand skill name set from ResolvedAgentConfig
- `pkg/queue/chat_executor.go` — same wrapping for chat agents
- `pkg/agent/orchestrator/runner.go` — wrap in `createSubAgentToolExecutor()` for sub-agents
- `deploy/config/` — example skills directory structure

**Verification:** LLM can call `load_skill` and receive skill content. Invalid names return partial success with error note. All-invalid names return error with available skill list. Tool is absent when agent has no on-demand skills. Sub-agents can load skills independently.

## Testing Strategy

- **Unit tests:** SkillRegistry CRUD, SKILL.md parsing (valid, missing frontmatter, empty body, no skills dir), skill resolution (all skills, allowlist, empty, required), prompt tier ordering, SkillToolExecutor (valid call, invalid name, partial failure with mix of valid/invalid, multi-skill, no skills)
- **Integration tests:** Full config → prompt → tool flow with test SKILL.md files in test fixtures
- **Existing test compatibility:** All existing tests pass unchanged (skills are optional; empty registry is the default)

## What's Out of Scope

- **Level 3 resources** (scripts, templates): TARSy agents don't have filesystem access.
- **Hot-reload**: Skills are loaded at startup. Changes require restart. Future work could add file watching.
- **Skill marketplace / external discovery**: Skills are operator-defined in `<configDir>/skills/`.
- **Cross-conversation learning**: Skills are static config.
- **Skill versioning**: Handled by Git / deployment pipeline.
