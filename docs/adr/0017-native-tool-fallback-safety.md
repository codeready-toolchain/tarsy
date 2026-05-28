# ADR-0017: Native Tool Fallback Safety

**Status:** Implemented  
**Date:** 2026-05-27  
**Supersedes:** Extends ADR-0003 (LLM Provider Fallback)

## Overview

When an LLM provider fails during agent execution, TARSy's fallback mechanism (ADR-0003) switches to alternative providers. However, certain agents — notably `WebResearcher` and `CodeExecutor` — require the `google-native` backend because they depend on native Gemini tools (`google_search`, `url_context`, `code_execution`) that only work through the Google SDK.

Previously, the fallback mechanism would switch these agents to incompatible providers (e.g. LangChain/Claude), logging a warning but proceeding anyway. This silently stripped the agent's core capabilities, causing it to "run" without its essential tools — a worse outcome than failing fast.

## Design Principles

1. **Fail safely over silently degrading.** An agent that loses its core tools should not continue pretending to work. Skipping an incompatible fallback entry is better than switching to it.
2. **Minimal change, maximum safety.** The fix is a small guard in the existing fallback path, not a rearchitecture.
3. **Configuration remains simple.** No new config surface. The system infers compatibility from existing metadata (backend + native tools).
4. **Startup validation catches obvious misconfigs.** If an operator configures only incompatible fallback entries for a chain that runs native-tool agents, warn at startup.

## Architecture

### Fallback Candidate Selection

```
tryFallback() called
  → shouldFallback() returns true (error threshold met)
  → Loop over ResolvedFallbackProviders from current index:
      → Skip if same provider name as current
      → Skip if entry would drop required native tools (log at Info)
  → If no compatible entries remain → return false (no fallback available)
  → Switch to first compatible entry
```

Both skip conditions (same-provider and incompatible-backend) are checked in the same loop. Skipped-incompatible entries are NOT added to `FallbackState.AttemptedProviders` since they were never actually attempted.

The skip is unconditional (hard guard). An agent without its native tools is broken, not degraded. No config option to override.

### Determining "Required Native Tools"

The guard distinguishes between:
- **Agent requires native tools** — agent definition declares `NativeTools` with at least one enabled tool (e.g. WebResearcher, CodeExecutor). Losing them breaks the agent.
- **Provider happens to support native tools** — agent uses a Gemini provider but doesn't declare `NativeTools` in its definition (e.g. VMRemediationAgent, orchestrators running on `gemini-3.5-flash`). Falling back to langchain is fine.

Checking the provider's NativeTools alone is insufficient — all Gemini providers declare `google_search: true, url_context: true` by default, so any agent on a Gemini provider would trigger the guard incorrectly.

**Solution:** A `RequiresNativeTools` boolean on the resolved agent config, set to `true` when the agent definition has at least one enabled native tool. The runtime guard checks this field, not the provider's native tools map.

### Compatibility Rule

An entry is "incompatible" when:

1. `RequiresNativeTools` is `true`
2. The fallback entry's backend is NOT `google-native`

Backend-level check only — no per-tool validation within `google-native` entries. If an edge case occurs (e.g., image model as fallback), the runtime error naturally triggers the next fallback attempt.

### Observability

When an entry is skipped due to incompatibility:
- Log at **Info** level (correct behavior, not an anomaly)
- Include `skipped_incompatible` entries in timeline metadata when fallback ultimately succeeds

No dedicated metric — skipping is a feature, not a problem to alert on.

### Startup Validation

A validation pass checks chains that include agents with native tools and warns if no compatible fallback entry exists in the resolved fallback list for that chain. Warning only — non-breaking, defense-in-depth.

## Core Concepts

### Compatible Fallback Entry

A fallback entry is "compatible" with the current execution when switching to it would not break the agent's required capabilities:
- If `RequiresNativeTools` is true → only `google-native` backend entries are compatible
- If `RequiresNativeTools` is false → any entry is compatible

### Native Tool Agents

Agents whose **definition** includes `NativeTools` with at least one enabled tool. Currently: `WebResearcher` and `CodeExecutor`. Agents that merely *use* a Gemini provider (which supports native tools at the provider level) do NOT qualify — they can fall back to any backend.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Skip observability | Info log + timeline metadata. No metric. | Skipping is correct behavior, not an anomaly. Timeline provides audit trail for operators who need it. |
| Q2 | Hard skip vs. configurable | Hard skip, always. No config option. | An agent without its core tools is broken, not degraded. A configurable option adds complexity for a useless mode. |
| Q3 | Per-tool check within google-native | Backend-level only. | Most Gemini providers support all tools. Agent native-tool overrides are merged onto fallback configs. Edge cases (image models) are handled naturally by error→fallback flow. |
| Q4 | Startup validation severity | Warning only. | Runtime guard is the real safety net. Non-breaking for existing deployments. Can be promoted to error later. |
| Q5 | Resolution-time vs. runtime filtering | Runtime-only in `tryFallback()`. | Single location for the logic. Faithful config representation. Easier to debug (all entries visible, skips logged). |
