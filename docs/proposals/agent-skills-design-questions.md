# Agent Skills — Design Questions

**Status:** All questions decided (Q1–Q5)
**Related:** [Design document](agent-skills-design.md)
**Prior art:** [Sketch questions (Q1–Q7)](agent-skills-questions.md) — conceptual decisions are finalized there. These questions cover implementation specifics.

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where do `skills` and `required_skills` live in the config hierarchy?

TARSy resolves MCP servers through a multi-level hierarchy: `AgentConfig` → `ChainConfig` → `StageConfig` → `StageAgentConfig`. The question is whether skills should follow the same pattern or stay agent-level only.

### Option A: Agent-level only

`skills` (allowlist) and `required_skills` are fields on `AgentConfig` only. No chain/stage/stage-agent overrides.

```yaml
agents:
  InfraAgent:
    required_skills: [platform-environment-context]
    # skills: nil → sees all
```

- **Pro:** Simple — one place to configure, one place to resolve
- **Pro:** Skills are knowledge scoping, not tool assignment; different semantic from MCP servers
- **Pro:** Matches the sketch decisions (Q2, Q3) which describe agent-level scoping
- **Pro:** Fewer modified files and test cases
- **Con:** Can't narrow skills per-stage (e.g., an agent in a "triage" stage gets different skills than the same agent in a "deep-dive" stage)

**Decision:** Option A — Agent-level only. Skills are knowledge scoping, not infrastructure wiring. Start simple; stage-level overrides can be added later without breaking changes.

_Considered and rejected: Option B — multi-level like MCP servers (YAGNI; complex merge semantics), Option C — agent + stage-agent override (inconsistent with MCP pattern while still adding complexity)_

---

## Q2: How does resolved skill data flow to the prompt builder?

The prompt builder needs skill data (required content for Tier 2.5, catalog entries for Tier 2.6) to compose the system prompt. Currently `PromptBuilder` takes `MCPServerRegistry` in its constructor and receives `ExecutionContext` per call. The question is how skill data reaches it.

### Option B: Pre-resolve skills, pass through ResolvedAgentConfig

Skill resolution happens in `config_resolver.go` (alongside MCP server resolution). `ResolvedAgentConfig` carries `RequiredSkillContent []ResolvedSkill` and `OnDemandSkills []SkillCatalogEntry`. The prompt builder just formats pre-resolved data.

- **Pro:** PromptBuilder stays simple — format-only, no resolution logic
- **Pro:** Resolution is centralized in `config_resolver.go` where all other hierarchy resolution lives
- **Pro:** Consistent with how `CustomInstructions` is already resolved (config_resolver puts it on ResolvedAgentConfig, prompt builder formats it)
- **Con:** ResolvedAgentConfig grows with more fields (already has 15+ fields)
- **Con:** Resolved skill bodies (RequiredSkillContent) are carried on every agent execution even if large

**Decision:** Option B — Pre-resolve skills, pass through ResolvedAgentConfig. Follows the existing pattern: config_resolver does all resolution, ResolvedAgentConfig carries the result, PromptBuilder formats it.

_Considered and rejected: Option A — add SkillRegistry to PromptBuilder constructor (pushes resolution logic into the prompt builder, which should only format)_

---

## Q3: Which agent types get skill support?

TARSy has several agent types: investigation (default), orchestrator, sub-agent, chat, scoring, synthesis, executive summary, and action. The question is which types should support skills.

### Option B (expanded): Investigation + orchestrator + sub-agents + action + chat

All agent types that perform investigation or take action on the environment. Action agents call `ComposeInstructions()` and flow through the same `executeAgent()` path as investigation agents, so they get skill support at zero additional cost. Domain knowledge is valuable for actions too — e.g., knowing environment quotas or runbook procedures before executing remediation.

- **Pro:** Chat agents get domain knowledge for follow-up questions
- **Pro:** Chat agents already have tools (MCP) — adding `load_skill` is consistent
- **Pro:** `ResolveChatAgentConfig` already resolves MCP servers; adding skill resolution is parallel
- **Pro:** Action agents participate for free (same code path as investigation)
- **Pro:** Action agents benefit from domain knowledge when executing remediation
- **Con:** One more executor to modify (`ChatMessageExecutor`)

**Decision:** Option B (expanded) — Investigation + orchestrator + sub-agents + action + chat. All agent types that interact with the environment or answer user questions.

_Considered and rejected: Option A — investigation only (chat and action agents can't access domain knowledge), Option C — all agent types (scoring/synthesis/exec-summary are meta-agents that don't need domain skills)_

---

## Q4: How should required skill content appear in the system prompt?

Required skills (Tier 2.5) are injected as content into the system prompt. The question is whether to inject the raw SKILL.md body or wrap it with a provenance header.

### Option B: Wrapped with a header

```
## Skill: platform-environment-context

## About the Platform
- Multi-tenant shared cluster with per-user namespace isolation
...
```

- **Pro:** Clear provenance — operators inspecting prompts can see where each block came from
- **Pro:** LLM understands the content is domain knowledge, separate from behavioral instructions
- **Pro:** Consistent style with other sections (MCP instructions use `## {serverID} Instructions`)
- **Con:** Adds a heading level that may clash with the skill body's own headings

**Decision:** Option B — Wrapped with a provenance header. Follows the existing `## {name} Instructions` pattern used by MCP and custom instructions sections.

_Considered and rejected: Option A — raw body (no provenance, headings blend with other sections, harder to debug)_

---

## Q5: How should `load_skill` calls be tracked?

TARSy tracks all MCP tool calls as interaction records in the database (via `InteractionService`). The `load_skill` tool executes in the same tool-call loop. The question is whether skill loads should be tracked the same way or handled silently.

### Option A: Tracked like MCP tools — full interaction records

`load_skill` calls flow through the normal tool-call tracking in the agent controller. Each call creates an interaction record with the tool name, arguments (skill names), and result (skill content). Timeline events are emitted as with any other tool call.

- **Pro:** Full observability — operators can see which skills agents loaded and when
- **Pro:** Zero additional code — the existing controller loop already records tool interactions
- **Pro:** Skill content appears in the investigation timeline (useful for debugging)
- **Con:** Skill content can be large (60+ lines); storing it as interaction records adds DB volume
- **Con:** Timeline gets noisier with skill-loading entries alongside actual investigation tools

**Decision:** Option A — Full tracking. `load_skill` calls are tracked identically to MCP tool calls: interaction records, timeline events, the full pipeline. Zero special-case code; the existing controller loop handles it.

_Considered and rejected: Option B — lightweight tracking without body (requires special-case handling), Option C — silent with no tracking (no observability, more complex to implement)_
