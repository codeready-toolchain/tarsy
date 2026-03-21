# ADR-0012: Agent Skills

**Status:** Implemented
**Date:** 2026-03-18

## Overview

Agent Skills add modular, reusable knowledge blocks to TARSy agents. Skills follow the industry-standard `SKILL.md` format, are discovered from the filesystem at startup, presented as a catalog in the system prompt, and loaded on-demand via a `load_skill` tool. This document specifies decisions, architecture, and data flow.

## Design Principles

1. **Follow established patterns** — SkillRegistry mirrors AgentRegistry and MCPServerRegistry. SkillToolExecutor mirrors CompositeToolExecutor. No new architectural concepts.
2. **Zero config for the common case** — Drop a `SKILL.md` file, all agents see it. No YAML changes required.
3. **Startup-only I/O** — Skills are loaded from disk into memory at config initialization. Runtime `load_skill` reads from the in-memory registry (no filesystem access).
4. **Minimal blast radius** — New fields on AgentConfig are optional. Existing configs work unchanged.

## Key Decisions

### Conceptual Decisions (from sketch)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| S1 | How are skills defined? | Industry-standard `skills/<name>/SKILL.md` directories | Aligns with Claude Code, Cursor, OpenAI Codex, Spring AI. Markdown is easy to edit, preview, lint. Supports future Level 3 resources. |
| S2 | How are skills scoped to agents? | `skills` controls on-demand catalog, `required_skills` controls prompt injection — independent fields | Zero config for common case. Decoupled fields avoid confusing subset requirements. `skills: []` disables on-demand without affecting required. |
| S3 | How does the LLM load skills at runtime? | Dedicated `load_skill` tool + per-agent `required_skills` | Progressive disclosure for most skills; safety net for critical domain knowledge via prompt injection. |
| S4 | Where does the skill catalog appear in the prompt? | System prompt Tier 2.5 (required content) and Tier 2.6 (on-demand catalog) | Clean placement between MCP instructions and custom instructions. Consistent with existing tier style. |
| S5 | How do skills work with orchestrators and sub-agents? | Each agent resolves skills independently | Simple, consistent resolution. Sub-agents can have different skill scoping. Matches OpenClaw. |
| S6 | Should `load_skill` support multiple skills per call? | Yes, batch loading via `names` array | Reduces expensive LLM round-trips. Each turn is an API call to the provider. |
| S7 | How is the skill catalog communicated? | Catalog with behavioral guidance | Explicit nudge to load skills early. Adapted from OpenClaw's prescriptive pattern. |

### Implementation Decisions (from design)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| D1 | Where do `skills`/`required_skills` live in config hierarchy? | Agent-level only (no chain/stage overrides) | Skills are knowledge scoping, not infrastructure wiring. Start simple; stage-level overrides can be added later without breaking changes. |
| D2 | How does resolved skill data flow to the prompt builder? | Pre-resolved in config resolution, carried on ResolvedAgentConfig | Follows existing pattern: resolver does all resolution, ResolvedAgentConfig carries the result, PromptBuilder formats it. |
| D3 | Which agent types get skill support? | Investigation + orchestrator + sub-agents + action + chat | All types that interact with the environment or answer user questions. Scoring/synthesis/exec-summary are meta-agents excluded. |
| D4 | How should required skill content appear in the system prompt? | Wrapped with a `## Skill: {name}` provenance header | Clear provenance for operators inspecting prompts. Consistent with `## {name} Instructions` pattern. |
| D5 | How should `load_skill` calls be tracked? | Full interaction records, identical to MCP tool calls | Full observability. Zero special-case code — existing controller loop handles it. |

## Architecture

### SkillConfig and SkillRegistry

Each skill has a name and short description (from frontmatter) plus a markdown body loaded on demand. The registry holds all parsed skills in memory with thread-safe read access — same lifecycle idea as other config registries. Lookup by name, list all names, and existence checks support validation and tool behavior.

### SkillToolExecutor

A wrapper around the inner tool executor intercepts only `load_skill`; everything else delegates unchanged — same wrapping idea as the orchestrator composite executor. `ListTools` prepends `load_skill` to the inner list; `Execute` handles `load_skill` locally and forwards other tools; `Close` delegates.

The `load_skill` tool accepts a `names` array (batch load). **Partial failure semantics:** When called with a mix of valid and invalid names, it returns the content of all valid skills and appends an error note listing the invalid names with the available skill catalog. This is non-fatal — the LLM receives the valid content and can retry or proceed. If *all* names are invalid, the result is an error listing available skills.

### Skill loader

At config load time, the loader scans a `skills/` directory under the config root. It supports two layouts: **directory layout** (`<name>/SKILL.md`, typical for local dev and Podman) and **flat file layout** (each key from a Kubernetes ConfigMap becomes a single file under `skills/`). If the directory is missing, the registry is empty (skills optional). Parsing splits on YAML frontmatter delimiters; dotfiles are skipped so ConfigMap mount artifacts are ignored.

### AgentConfig fields

Two new optional agent-level fields: **`skills`** controls which skills appear in the on-demand catalog and are loadable via `load_skill` — `nil` means all registry skills, empty slice means none, a list means that subset. **`required_skills`** lists skills whose bodies are injected into the system prompt (Tier 2.5). Required skills are validated against the registry independently of the `skills` allowlist; they are excluded from the on-demand catalog. Built-in agent definitions mirror these fields with the same merge semantics; defaults keep prior behavior (nil = all on-demand).

### Config and validation

The top-level config holds the skill registry after load. Validation ensures every name in either `skills` or `required_skills` exists in the registry, and warns if agents reference skills but the registry is empty.

### ResolvedAgentConfig skill data

Resolution runs when building an agent execution context: required skills become **required skill content** (name + full body for injection). On-demand skills become **catalog entries** (name + description only; bodies load via tool). Required set is resolved first; on-demand is derived from the `skills` allowlist (nil / empty / list) minus required names. The same logic applies to chat agent resolution.

Investigation, orchestrator, sub-agent, action, and chat agents get skills wherever they compose instructions; meta-agents (scoring, synthesis, exec summary) do not.

### Prompt tiers (Tier 2.5 and 2.6)

Between MCP instructions and custom instructions, the prompt builder inserts:

1. **Tier 2.5** — Pre-loaded required skill bodies under a clear container heading so the model treats them as reference material.
2. **Tier 2.6** — The on-demand catalog with behavioral guidance, only when the agent has on-demand skills.

`formatSkillCatalog` generates the on-demand catalog with decision-tree instructions. The template:

```text
## Available Skills

Skills provide domain-specific knowledge that may help with your task.
The following additional skills can be loaded on demand using the `load_skill` tool.
Scan the descriptions and decide:
- If one or more match your task: load them before proceeding.
- If none match: skip and proceed without them.

<available_skills>
- **{name}**: {description}
- ...
</available_skills>
```

The same catalog shape is used for standard and chat instruction composition. The `<available_skills>` tags bound the list for parsing.

### Tool executor wrapping order

After the MCP tool executor is built (and optionally wrapped for orchestration), wrap with the skill executor when the resolved agent has on-demand skills — for main investigation/action execution, chat execution, and sub-agents created by orchestrators (each sub-agent uses its own resolved on-demand set).

**Order (outermost last):** MCP → Orchestrator (if any) → Skill. `load_skill` is handled before requests reach inner layers.

`load_skill` calls are recorded like any other tool call (full interaction trail, no special casing in the controller).

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

## What's Out of Scope

- **Level 3 resources** (scripts, templates): TARSy agents don't have filesystem access.
- **Hot-reload**: Skills are loaded at startup. Changes require restart. Future work could add file watching.
- **Skill marketplace / external discovery**: Skills are operator-defined under the config directory's `skills/` tree.
- **Cross-conversation learning**: Skills are static config.
- **Skill versioning**: Handled by Git / deployment pipeline.
