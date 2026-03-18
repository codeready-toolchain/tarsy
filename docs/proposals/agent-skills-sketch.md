# Agent Skills: Modular, Reusable Knowledge for TARSy Agents

**Status:** All questions decided (Q1–Q7) — see [agent-skills-questions.md](agent-skills-questions.md)

## Problem

TARSy agents are configured with `custom_instructions` — free-form text injected into the system prompt at Tier 3. This works for agent-specific behavior, but breaks down when multiple agents need **shared domain knowledge**.

In production deployments, the same knowledge blocks are duplicated across multiple agents:

| Knowledge Block | Duplicated In | ~Lines |
|---|---|---|
| Platform environment context (architecture, quotas, tenancy model) | 5 agents | 10 |
| Alert classification criteria (legitimate vs anomalous) | 2 agents | 60 |
| Required output format | 2 agents | 20 |

This causes:
- **Drift risk** — changing a single fact requires editing multiple places
- **Bloated config** — the production `tarsy.yaml` is ~600 lines, mostly duplicated prompt text
- **No composability** — can't mix-and-match knowledge blocks per agent without copy-paste

ADR-0002 (Orchestrator) explicitly deferred this: *"Skills system: defer — `custom_instructions` covers this. Reusable blocks can be added later."* (V7)

## Proposal

Add an **Agent Skills** system to TARSy — named, reusable knowledge blocks that agents can load on-demand during an investigation, following the industry-standard progressive disclosure pattern.

Skills are not a replacement for `custom_instructions`. They complement it: skills carry **shared domain knowledge** (environment context, classification criteria, report formats), while `custom_instructions` remains for **agent-specific behavioral directives**.

## How It Relates to the Existing System

TARSy's prompt builder composes system prompts in tiers:

```
Tier 1:   General SRE Instructions     (generalInstructions — hardcoded)
Tier 2:   MCP Server Instructions       (from MCPServerRegistry, per server ID)
   ↕      Unavailable server warnings
Tier 2.5: Required skill content        (injected from SkillRegistry)
Tier 2.6: On-demand skill catalog       (names + descriptions, with behavioral nudge)
Tier 3:   Agent Custom Instructions     (from AgentConfig.CustomInstructions)
   +      Mode-specific blocks           (orchestrator catalog, action safety, etc.)
```

Skills introduce a **new knowledge layer** between MCP instructions and agent custom instructions. Required skills are injected as content (Tier 2.5), while on-demand skills appear as a catalog with a `load_skill` tool (Tier 2.6). Domain knowledge sits before agent-specific behavioral directives, consistent with how MCP instructions are presented.

## Key Concepts

### Skill Definition

Skills follow the industry-standard `skills/<name>/SKILL.md` directory format, adopted by Claude Code, Cursor, OpenAI Codex, Spring AI, and 16+ other tools.

Skills live under `<configDir>/skills/`:

```
deploy/config/
├── tarsy.yaml
├── llm-providers.yaml
└── skills/
    ├── platform-environment-context/
    │   └── SKILL.md
    ├── alert-classification-criteria/
    │   └── SKILL.md
    └── investigation-report-format/
        └── SKILL.md
```

Each `SKILL.md` has YAML frontmatter (metadata) and a Markdown body (instructions):

```markdown
---
name: platform-environment-context
description: >
  Platform environment details: architecture, quotas, tenancy model,
  user capabilities. Use for any platform-related alert investigation.
---

## About the Platform
- Multi-tenant shared cluster with per-user namespace isolation
- Each user gets a single namespace with resource quotas
- Workloads are scheduled on shared compute nodes
...
```

By default, **all discovered skills are available to all agents** — the LLM uses the `description` field to decide which are relevant. Agents can optionally declare a `skills` allowlist in `tarsy.yaml` to restrict their catalog:

```yaml
agents:
  # No skills field → sees all discovered skills (default)
  SecurityInvestigationAgent:
    custom_instructions: |
      You are a Security Operations Engineer...

  # Explicit allowlist → sees only these skills
  ArgoCD:
    skills: [argocd-troubleshooting]
    custom_instructions: |
      ...

  # Empty list → no skills (disables skill loading)
  SomeSimpleAgent:
    skills: []
```

The config loader scans `<configDir>/skills/*/SKILL.md` at startup, parses frontmatter for the catalog, and holds the body for on-demand loading.

### Skill Catalog (Level 1 — Always Present)

At agent initialization, a lightweight **catalog** of all available skill names and descriptions is presented to the LLM in the system prompt (Tier 2.6). This costs ~100 tokens per skill and lets the LLM know what knowledge is available without loading any content. The catalog includes a behavioral nudge adapted from OpenClaw's prescriptive pattern and the agentskills.io implementation guide:

```
## Available Domain Knowledge

Before starting your task, scan the skill descriptions below and load any
that match the current context (alert type, environment, workload type).
These contain domain-specific knowledge that may not be in your training data.

- **platform-environment-context**: Platform environment details: architecture,
  quotas, tenancy model, user capabilities. Use for any platform-related alert.
- **alert-classification-criteria**: Criteria for classifying alert severity and
  distinguishing expected behavior from anomalies. Use when triaging alerts.
- **investigation-report-format**: Standard format and severity scale for
  investigation reports. Use when writing the final assessment.

Use the `load_skill` tool to load relevant skills by name before proceeding.
You can load multiple skills in one call. If no skill description matches
your current task, do not load any.
```

The skill list is generated dynamically from the `SkillRegistry`, filtered per agent. Required skills (already injected at Tier 2.5) do not appear in the catalog.

### On-Demand Loading (Level 2 — Tool-Based)

The LLM loads skills by invoking the **`load_skill` tool**. The tool accepts one or more skill names, reads from the in-memory `SkillRegistry` (not the filesystem — skills are loaded at startup), and returns the full SKILL.md body as a tool result. The LLM then follows the instructions in the skill content.

```
load_skill(names: ["platform-environment-context", "alert-classification-criteria"])
```

Multiple skills per call reduces expensive LLM round-trips — each turn in TARSy is an API call to the provider. This aligns with how Claude Code, Cursor, and Copilot handle multi-skill loading (OpenClaw's single-skill constraint is the industry outlier).

This is the **progressive disclosure** pattern: the LLM pays the token cost of a skill's content only when it determines the skill is relevant to the current task.

The `load_skill` tool is only registered for agents that have at least one on-demand skill available. If an agent has no skills (empty catalog after filtering), the tool is not registered — avoiding confusing the LLM with an unusable tool.

### Required Skills (Always-Injected)

Agents can optionally declare `required_skills` — skills whose content is injected directly into the system prompt, guaranteeing the LLM always has critical domain knowledge:

```yaml
agents:
  SecurityAgent:
    required_skills: [platform-environment-context, alert-classification-criteria]
    # Remaining discovered skills → available via load_skill tool
```

Required skills are excluded from the `load_skill` catalog (already loaded). This provides a safety net for knowledge that's too critical to leave to the LLM's judgment, while keeping the default behavior fully on-demand.

### Skill Scoping

By default, all discovered skills are available to all agents. The LLM uses each skill's `description` to decide relevance. For agents that need explicit scoping, an optional `skills` allowlist in `tarsy.yaml` restricts the catalog (see Skill Definition above).

### Interaction with Orchestrator and Sub-Agents

Each agent (including sub-agents) resolves skills independently via the scoping mechanism: all skills by default, optional `skills` allowlist per agent definition. Orchestrator and sub-agent skill sets are independent — no propagation from parent to child. This matches how OpenClaw handles it and keeps the resolution logic consistent across all agent types.

## Rough Behavior

1. **Config load time**: Loader scans `<configDir>/skills/*/SKILL.md`, parses frontmatter, builds a `SkillRegistry` (similar to `AgentRegistry`, `MCPServerRegistry`)
2. **Agent initialization**: Resolve which skills are available — all by default, or filtered by the agent's optional `skills` allowlist. Split into required (prompt-injected) and on-demand (tool-loaded).
3. **System prompt construction**:
   - Required skills (`required_skills`): content injected into system prompt between MCP instructions and custom instructions
   - On-demand skills: catalog (names + descriptions) included in system prompt
4. **Tool registration**: If the agent has on-demand skills, a `load_skill` tool is registered alongside MCP tools. If no on-demand skills remain (all required or none available), no tool.
5. **Runtime**: The LLM invokes `load_skill(names: [...])` → tool reads from in-memory registry → returns full SKILL.md body as tool result
6. **Investigation continues**: The LLM incorporates the skill's knowledge into its analysis

## What's Out of Scope

- **Level 3 resources (scripts, templates, reference files)**: TARSy agents don't have filesystem or code execution access (unlike Claude Code / Cursor). Skills are instruction-only.
- **Skill marketplace / external skill discovery**: Skills are operator-defined in `<configDir>/skills/`, not fetched from external sources.
- **Cross-conversation skill learning**: Skills are static config, not learned from past investigations.
- **Skill versioning**: Handled by the existing config management (Git, deployment pipeline). No in-product versioning system.

## Example (Before/After)

### Before — duplicated knowledge in custom_instructions

```yaml
agents:
  InfraAgent:
    custom_instructions: |
      You are an SRE specializing in infrastructure alerts...
      ## About the Platform                   # ← copy 1 of 3
      - Multi-tenant shared cluster...
      ## INVESTIGATION REPORT FORMAT          # ← copy 1 of 2
      ...

  NetworkAgent:
    custom_instructions: |
      You are an SRE specializing in network issues...
      ## About the Platform                   # ← copy 2 of 3
      - Multi-tenant shared cluster...
      # Network-specific stuff here

  InvestigationOrchestrator:
    custom_instructions: |
      You are an SRE coordinating investigations...
      ## About the Platform                   # ← copy 3 of 3
      - Multi-tenant shared cluster...
      ## INVESTIGATION REPORT FORMAT          # ← copy 2 of 2
      ...
```

### After — shared skills as SKILL.md files, agent-specific custom_instructions

Skills live as standard `SKILL.md` files:

```
config/skills/platform-environment-context/SKILL.md     # Platform details — defined once
config/skills/investigation-report-format/SKILL.md      # Report format — defined once
```

`tarsy.yaml` is dramatically smaller — all skills are available by default, no per-agent skill lists needed:

```yaml
# Skills are discovered from config/skills/*/SKILL.md — no skills section in tarsy.yaml

agents:
  InfraAgent:
    # No skills field → sees all skills; LLM loads what's relevant based on descriptions
    custom_instructions: |
      You are an SRE specializing in infrastructure alerts.
      Focus on resource utilization, pod health, and node conditions.

  NetworkAgent:
    # No skills field → sees all skills; description-based filtering handles scoping
    custom_instructions: |
      You are an SRE specializing in network issues.
      Focus on connectivity, DNS resolution, and ingress configuration.

  InvestigationOrchestrator:
    # No skills field → sees all skills; loads what's relevant before dispatching sub-agents
    custom_instructions: |
      You are an SRE coordinating investigations.
      Dispatch specialized sub-agents based on the alert context.
```
