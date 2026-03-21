# ADR-0001: Agent Type System Refactor

**Status:** Implemented  
**Date:** 2026-02-23

## Problem Statement

TARSy's `IterationStrategy` enum currently encodes three orthogonal concerns in a single value:

| Strategy | Backend (LLM path) | Controller (iteration) | Agent wrapper |
|---|---|---|---|
| `native-thinking` | google-native | FunctionCallingController | BaseAgent |
| `langchain` | langchain | FunctionCallingController | BaseAgent |
| `synthesis` | langchain | SynthesisController | BaseAgent |
| `synthesis-native-thinking` | google-native | SynthesisController | BaseAgent |
| `scoring` | langchain | FunctionCallingController | ScoringAgent |
| `scoring-native-thinking` | google-native | FunctionCallingController | ScoringAgent |

This conflation causes:

1. **Combinatorial explosion.** Every new agent type (orchestrator, future types) requires N strategy variants — one per backend. Adding an orchestrator would need `orchestrator` + `orchestrator-native-thinking`.

2. **Misleading naming.** "synthesis" is not an iteration strategy — it's an agent type. "native-thinking" is not a strategy — it's a backend choice.

3. **Controller duplication.** `SynthesisController` exists primarily because it calls a different prompt builder method. The actual control flow patterns (multi-turn iteration vs single-shot) are obscured by the naming.

## Goal

Replace `IterationStrategy` with three orthogonal config fields:

- **`type`**: What the agent does — determines controller selection and agent wrapper
- **`llm_backend`**: Which SDK path — `native-gemini` or `langchain`
- **`mcp_servers`**: What tools are available — unchanged from today

`iteration_strategy` is removed entirely.

## Controller architecture (conceptual)

Controllers are split by **control flow pattern**, not by agent type. Variable behaviors are injected via configuration.

- **IteratingController** (renamed from the former function-calling controller): multi-turn loop with tools.
- **SingleShotController**: one request, one response, no tools; parameterized message building (e.g. synthesis vs scoring prompts, thinking fallback on or off).

Synthesis and scoring become **specializations of SingleShotController** via config, not separate controller types. Any future agent that does not fit the iterating loop or single-shot config can still implement the controller interface and compose shared helpers.

## Agent type → controller → wrapper mapping (after refactor)

| `type` | Controller | Config | Agent wrapper |
|--------|-----------|--------|--------------|
| default (investigation) | IteratingController | — | BaseAgent |
| `synthesis` | SingleShotController | synthesis messages, thinking fallback on | BaseAgent |
| `scoring` | SingleShotController | scoring messages, thinking fallback off | ScoringAgent |
| *future: `orchestrator`* | *IteratingController* | *orchestration tools via composite executor* | *BaseAgent* |

`llm_backend` is orthogonal — any type can use either `native-gemini` or `langchain`.

## Configuration (illustrative)

```yaml
agents:
  LogAnalyzer:
    description: "Analyzes logs from Loki"
    mcp_servers: [loki]

agent_chains:
  security-investigation:
    llm_backend: native-gemini
    stages:
      - name: analysis
        agents:
          - name: SecurityAgent
          - name: SecurityAgent
            llm_backend: langchain
        synthesis:
          llm_backend: native-gemini
```

## Backward compatibility

**Clean cut — no migration code.** The `iteration_strategy` field is removed entirely. Config validation rejects it as an unknown field. Users update configs to use `type` + `llm_backend` before upgrading.

Brief mapping: where config used `iteration_strategy: native-thinking`, use `llm_backend: native-gemini`; where it used compound values like `synthesis-native-thinking`, use `type: synthesis` and `llm_backend: native-gemini` separately. Scoring blocks implicitly imply `type: scoring`; only backend needs to be set explicitly.

## Sequencing note

This refactor ships **before** the orchestrator. It is independently valuable and establishes the `type` pattern the orchestrator depends on.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Controller restructuring | Two structural controllers (IteratingController + SingleShotController) with parameterized config | Two controllers match two real control flow patterns. Shared code already lives in package-level helpers. Parameterized single-shot config serves both synthesis and scoring without type-awareness in the controller struct. |
| Q2 | ScoringAgent merge | Keep separate; uses SingleShotController with scoring config | Small surface area, explicitly encapsulates different lifecycle (e.g. execution status handling). Agent wrapper and controller remain separate layers. |
| Q3 | Backend selection | Remove `iteration_strategy`, introduce `llm_backend` (`native-gemini`, `langchain`). Controller from `type`. | `iteration_strategy` conflated backend selection and agent behavior. With `type` handling controller selection, the remaining job is SDK path. Each field does one thing. |
| Q4 | Backward compatibility | Clean cut — no migration code | No deprecated code paths to maintain. Users update configs before upgrading. |
| Q5 | Sequencing | Refactor first, then orchestrator | Independently valuable cleanup. Orchestrator builds on a clear separation of agent kind vs backend. |
