# Agent Skills — Sketch Questions

**Status:** All questions decided (Q1–Q7)
**Related:** [Sketch document](agent-skills-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: How are skills defined?

Skills need a name, description, and content. The question is where the content lives and how it's authored.

### Option B: Industry-standard `skills/<name>/SKILL.md` directories

Skills are filesystem directories under `<configDir>/skills/`, following the open standard adopted by Claude Code, Cursor, OpenAI Codex, Spring AI, and 16+ other tools:

```
deploy/config/
├── tarsy.yaml
├── llm-providers.yaml
└── skills/
    ├── developer-sandbox-context/
    │   └── SKILL.md
    ├── security-classification-criteria/
    │   └── SKILL.md
    └── security-report-format/
        └── SKILL.md
```

Each `SKILL.md` has YAML frontmatter (name, description) and a Markdown body (instructions):

```markdown
---
name: developer-sandbox-context
description: >
  Developer Sandbox environment: trial details, quotas, VM policies,
  user capabilities. Use for any Developer Sandbox alert investigation.
---

## About the Developer Sandbox
- Free 30-day trial on a shared multi-tenant OpenShift cluster
- Anyone with an email can sign up and get automatically provisioned
...
```

The config loader scans `<configDir>/skills/*/SKILL.md` at startup, parses frontmatter for the catalog, and holds the body for on-demand loading.

- **Pro:** Aligns with the industry standard — anyone familiar with Agent Skills knows the format
- **Pro:** Markdown files are easy to edit, preview, lint, and diff in PRs
- **Pro:** Clean separation — YAML config stays small, knowledge lives in proper Markdown
- **Pro:** Directory structure supports future Level 3 resources (scripts, references) without format changes
- **Pro:** Works naturally in all deployment environments (local dev, Podman, OpenShift)
- **Con:** Filesystem dependency at config load time (trivial — `configDir` is already tracked)

**Decision:** Option B — Industry-standard `skills/<name>/SKILL.md` directories.

_Considered and rejected: Option A — inline YAML (fragile for large content, doesn't align with standard), Option C — both inline and files (unnecessary complexity)_

---

## Q2: How are skills scoped to agents?

Agents need to know which skills are available to them. The question is how this mapping is expressed.

### Option D: All skills available by default, optional per-agent allowlist filter (OpenClaw pattern)

All discovered skills are available to all agents by default — the LLM uses the `description` field to decide which are relevant. Agents can optionally declare a `skills` allowlist to restrict which skills they see:

```yaml
agents:
  # No skills config → sees all skills, LLM picks based on description
  SecurityInvestigationAgent:
    custom_instructions: |
      ...

  # Explicit allowlist → sees only these skills
  ArgoCD:
    skills: [argocd-troubleshooting]
    custom_instructions: |
      ...
```

Semantics (following OpenClaw):
- **No `skills` field** (default): all discovered skills in catalog
- **`skills: [a, b]`**: only those skills in catalog
- **`skills: []`**: no skills (disables skill loading for this agent)

- **Pro:** Zero config for the common case — add a skill file, all agents see it
- **Pro:** Follows the industry pattern (OpenClaw, Claude Code, Cursor all default to "all available")
- **Pro:** Optional per-agent filtering for agents with specialized roles
- **Pro:** `description` field in SKILL.md does the semantic scoping naturally
- **Con:** Agents may see irrelevant skills in the catalog (mitigated by LLM filtering on description)

**Decision:** Option D — All skills available by default with optional per-agent allowlist filter (OpenClaw pattern).

_Considered and rejected: Option A — explicit list per agent (too restrictive, every agent must list skills), Option B — tag matching (over-engineered), Option C — global + exclusions (exclude lists harder to reason about)_

---

## Q3: How does the LLM load skills at runtime?

This is the core progressive disclosure question. The LLM sees a catalog of available skills and needs a mechanism to load the full content when needed.

### Option A: Dedicated `load_skill` tool with per-agent `required_skills`

Two loading paths, one mechanism:

**On-demand (default):** A `load_skill` tool is registered for agents that have skills available. The LLM calls `load_skill(name: "developer-sandbox-context")` and receives the content as a tool result. The tool reads from the in-memory `SkillRegistry` (not the filesystem — skills are loaded at startup). If an agent has no skills available (empty catalog after filtering), the tool is not registered.

**Always-injected (`required_skills`):** Agents can optionally declare `required_skills` — skills whose content is injected directly into the system prompt at build time, guaranteeing the LLM always has critical domain knowledge. Required skills are excluded from the `load_skill` catalog (already loaded, no point offering them again).

```yaml
agents:
  SecurityInvestigationAgent:
    required_skills: [developer-sandbox-context, security-classification-criteria]
    # Remaining discovered skills → available via load_skill tool

  SecurityInvestigationOrchestrator:
    # No required_skills → everything is on-demand via load_skill

  ArgoCD:
    skills: [argocd-troubleshooting]           # allowlist filter (Q2)
    required_skills: [argocd-troubleshooting]   # also always-injected
```

Precedent: OpenClaw supports `metadata.openclaw.always: true` on skills (global, not per-agent). TARSy's `required_skills` adapts this to the multi-agent model where different agents need different knowledge guaranteed.

- **Pro:** Clean progressive disclosure for most skills via `load_skill` tool
- **Pro:** Safety net for critical domain knowledge via `required_skills`
- **Pro:** Per-agent control — same skill can be required for one agent and on-demand for another
- **Pro:** No filesystem access needed at runtime
- **Con:** Two loading paths to implement (prompt injection + tool)
- **Con:** Operator must decide which skills are "required" vs on-demand (mitigated: start with none required, promote based on production experience)

**Decision:** Option A — Dedicated `load_skill` tool for on-demand loading, plus optional per-agent `required_skills` for guaranteed prompt injection. The default is fully on-demand (progressive disclosure). `required_skills` is a safety net for critical knowledge where relying on the LLM to load it is too risky.

_Considered and rejected: Option B — server-side injection only (no progressive disclosure, defeats Approach B), Option C — hybrid with two separate lists as the primary mechanism (framing it as `required_skills` is cleaner — the default is on-demand, required is the exception)_

---

## Q4: Where does the skill catalog appear in the prompt?

The on-demand skill catalog (names + descriptions) needs to be visible to the LLM so it knows what skills are available via `load_skill`. Required skills are already injected as content.

### Option A: In the system prompt, as a new tier

Add catalog and required skill content between MCP instructions (Tier 2) and custom instructions (Tier 3):

```
Tier 1:   General SRE Instructions
Tier 2:   MCP Server Instructions
Tier 2.5: Required skill content (injected)
Tier 2.6: On-demand skill catalog (names + descriptions)
Tier 3:   Agent Custom Instructions
```

- **Pro:** Clean placement — domain knowledge before agent-specific behavior
- **Pro:** Consistent with how MCP instructions are presented
- **Con:** System prompt grows with each skill catalog entry (~1 line per skill)

**Decision:** Option A — System prompt as a new tier.

_Considered and rejected: Option B — embedded in tool description (length limits, mixes concerns), Option C — split across system prompt and tool description (fragmented)_

---

## Q5: How do skills work with orchestrators and sub-agents?

TARSy's orchestrator dispatches sub-agents dynamically. Sub-agents build their own prompts via `buildSubAgentMessages()`. The question is how skills flow through this hierarchy.

### Option A: Each agent resolves skills independently

Each agent (including sub-agents) resolves skills independently via the Q2 mechanism: all skills by default, optional `skills` allowlist per agent definition. Orchestrator and sub-agent skill sets are independent. This matches how OpenClaw handles it — sub-agents resolve skills from the same global directory, filtered by their own agent config, with no propagation from the parent.

- **Pro:** Simple, consistent — same resolution logic for all agents
- **Pro:** Sub-agents can have different skill scoping (VM agent can filter to VM-only skills)
- **Pro:** No new resolution logic beyond Q2
- **Pro:** Matches OpenClaw's implementation
- **Con:** No centralized control from the orchestrator level

**Decision:** Option A — Each agent resolves skills independently via the Q2 mechanism. Confirmed by OpenClaw's implementation where sub-agents resolve their own skills from the global directory without parent propagation.

_Considered and rejected: Option B — orchestrator propagates skills (complex merge logic, sub-agents may get irrelevant skills), Option C — chain/stage-level skills (adds to already complex hierarchy)_

---

## Q6: Should the `load_skill` tool support loading multiple skills at once?

An agent might need several skills for a single investigation. The question is whether this happens one-at-a-time or in batch.

### Option B: Multiple skills per call

```
load_skill(names: ["developer-sandbox-context", "security-classification-criteria"])
```

- **Pro:** Load all needed skills in one turn; reduces expensive LLM round-trips
- **Con:** Slightly more complex tool schema

**Decision:** Option B — Multiple skills per call. Each LLM turn in TARSy is an API call to the provider. Batch loading lets the LLM grab everything it needs in one shot.

_Considered and rejected: Option A — single skill per call (loading 3 skills costs 3 LLM turns, too expensive)_

---

## Q7: How is the skill catalog communicated alongside the `load_skill` tool?

When an agent has skills available, the LLM needs to know: (a) that skills exist, (b) what's available, and (c) how to load them. The question is how to word the instruction that introduces skills.

### Option B: Catalog with behavioral guidance

- **Pro:** Explicit nudge to load skills early; reduces risk of skipping critical knowledge
- **Con:** Slightly more prescriptive

**Decision:** Option B — Catalog with behavioral guidance. Adapted from OpenClaw's prescriptive `## Skills (mandatory)` section and the agentskills.io implementation guide's recommended behavioral instructions. The prompt nudges the LLM to scan and load relevant skills before starting work, supports multi-skill loading (Q6), and tells the LLM to skip loading when no skills match.

_Considered and rejected: Option A — minimal catalog only (too passive; risk of LLM skipping critical knowledge)_

### Prompt template

The catalog is injected as Tier 2.5/2.6 in `ComposeInstructions`, between MCP instructions and agent custom instructions. The skill list is generated dynamically from the `SkillRegistry`, filtered per agent (Q2). Required skills (Q3) are injected as content before the catalog; they do not appear in the catalog list.

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

The prompt has three parts:

1. **Behavioral nudge** (lines 1–3): "Before starting your task, scan..." — tells the LLM to review skills early, framed around TARSy's SRE context. Mentions "training data" to motivate loading domain-specific knowledge the LLM likely doesn't have.
2. **Skill catalog** (dynamic list): One entry per on-demand skill, formatted as `- **name**: description`. Generated from the registry at prompt-build time. Bold name for visual scanning. Description comes from SKILL.md frontmatter.
3. **Tool usage** (last 3 lines): Explains `load_skill`, mentions multi-skill support, and tells the LLM to skip loading when nothing matches (prevents speculative loading).

Design notes:
- No XML tags — TARSy's prompts use plain markdown throughout (consistent with Tier 1/2/3 style).
- No "mandatory" or "IMPORTANT" — TARSy agents are purpose-built for specific investigation types, so skill relevance is usually obvious from the alert context. A nudge is enough.
- "before proceeding" — encourages loading skills before the first tool call, not mid-investigation.
- Multi-skill mention is explicit to avoid the LLM self-imposing a one-at-a-time pattern.
