# Agent Type System Refactor — Design Document

**Status:** Ready for implementation
**Context:** Identified during [orchestrator implementation design](orchestrator-impl-design.md)
**Decisions:** All questions resolved — see [agent-type-refactor-questions.md](agent-type-refactor-questions.md)
**Last updated:** 2026-02-23

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

## Current Architecture

### Controller factory (today)

```go
// pkg/agent/controller/factory.go
func (f *Factory) CreateController(strategy IterationStrategy, execCtx *ExecutionContext) (Controller, error) {
    switch strategy {
    case "native-thinking", "langchain":
        return NewFunctionCallingController(), nil
    case "synthesis", "synthesis-native-thinking":
        return NewSynthesisController(), nil
    default:
        return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
    }
}
```

### Agent factory (today)

```go
// pkg/agent/factory.go
func (f *AgentFactory) CreateAgent(execCtx *ExecutionContext) (Agent, error) {
    controller := f.controllerFactory.CreateController(execCtx.Config.IterationStrategy, execCtx)
    if execCtx.Config.IterationStrategy.IsValidForScoring() {
        return NewScoringAgent(controller), nil
    }
    return NewBaseAgent(controller), nil
}
```

### ResolveBackend (today)

```go
// pkg/agent/config_resolver.go
func ResolveBackend(strategy IterationStrategy) string {
    switch strategy {
    case "native-thinking", "synthesis-native-thinking", "scoring-native-thinking":
        return "google-native"
    default:
        return "langchain"
    }
}
```

## Proposed Architecture

### Config model

```yaml
agents:
  LogAnalyzer:
    description: "Analyzes logs from Loki"
    mcp_servers: [loki]

  MyOrchestrator:
    type: orchestrator
    description: "Dynamic investigation orchestrator"

agent_chains:
  security-investigation:
    llm_backend: native-gemini       # chain-level default
    stages:
      - name: analysis
        agents:
          - name: SecurityAgent
          - name: SecurityAgent
            llm_backend: langchain   # per-agent override
        synthesis:
          llm_backend: native-gemini # backend for synthesis, type is implicit
```

### AgentConfig changes

```go
type AgentConfig struct {
    Type               AgentType  `yaml:"type,omitempty"`
    Description        string     `yaml:"description,omitempty"`
    MCPServers         []string   `yaml:"mcp_servers"`
    CustomInstructions string     `yaml:"custom_instructions"`
    LLMBackend         LLMBackend `yaml:"llm_backend,omitempty"`
    MaxIterations      *int       `yaml:"max_iterations,omitempty"`
}

type AgentType string
const (
    AgentTypeDefault      AgentType = ""             // Regular investigation agent
    AgentTypeSynthesis    AgentType = "synthesis"
    AgentTypeScoring      AgentType = "scoring"
    AgentTypeOrchestrator AgentType = "orchestrator"
)

type LLMBackend string
const (
    LLMBackendNativeGemini LLMBackend = "native-gemini"
    LLMBackendLangChain    LLMBackend = "langchain"
)
```

### ResolveBackend simplification

```go
func ResolveBackend(backend LLMBackend) string {
    if backend == LLMBackendNativeGemini {
        return BackendGoogleNative
    }
    return BackendLangChain
}
```

### Controller architecture — two structural patterns (Q1)

Controllers are split by **control flow pattern**, not by agent type. Variable behaviors are injected via configuration.

**Layer 1 — Shared functions (already exists):** Package-level functions (`callLLMWithStreaming`, `storeMessages`, `storeAssistantMessage`, `createTimelineEvent`, `recordLLMInteraction`, etc.) in `streaming.go`, `messages.go`, `timeline.go`, `helpers.go`.

**Layer 2 — Two control flow patterns:**

```go
// IteratingController — multi-turn loop with tools
// (current FunctionCallingController, renamed)
type IteratingController struct{}

func (c *IteratingController) Run(ctx, execCtx, prevStageContext) (*ExecutionResult, error) {
    // build messages → list tools → loop { call LLM → execute tool calls → repeat }
    // → forceConclusion if max iterations
}

// SingleShotController — one request, one response, no tools
type SingleShotController struct {
    cfg SingleShotConfig
}

type SingleShotConfig struct {
    BuildMessages    func(*ExecutionContext, string) []ConversationMessage
    ThinkingFallback bool  // use thinking text if response text is empty
}

func (c *SingleShotController) Run(ctx, execCtx, prevStageContext) (*ExecutionResult, error) {
    messages := c.cfg.BuildMessages(execCtx, prevStageContext)
    // single LLM call → store → return
}
```

**Layer 3 — Specializations via configuration:**

```go
// Synthesis = SingleShotController with synthesis prompt + thinking fallback
func NewSynthesisController(pb PromptBuilder) *SingleShotController {
    return &SingleShotController{cfg: SingleShotConfig{
        BuildMessages:    pb.BuildSynthesisMessages,
        ThinkingFallback: true,
    }}
}

// Scoring = SingleShotController with scoring prompt (no MCP tools)
func NewScoringController(pb PromptBuilder) *SingleShotController {
    return &SingleShotController{cfg: SingleShotConfig{
        BuildMessages:    pb.BuildScoringMessages,
        ThinkingFallback: false,
    }}
}
```

**Escape hatch:** Any future agent type that doesn't fit `SingleShotConfig` or the iterating loop can implement the `Controller` interface directly and compose the shared package-level functions.

### Controller factory — type-driven

```go
func (f *Factory) CreateController(agentType AgentType, execCtx *ExecutionContext) (Controller, error) {
    switch agentType {
    case AgentTypeDefault, "":
        return NewIteratingController(), nil
    case AgentTypeSynthesis:
        return NewSynthesisController(execCtx.PromptBuilder), nil
    case AgentTypeScoring:
        return NewScoringController(execCtx.PromptBuilder), nil
    case AgentTypeOrchestrator:
        return NewIteratingController(), nil  // orchestration via CompositeToolExecutor
    default:
        return nil, fmt.Errorf("unknown agent type: %q", agentType)
    }
}
```

### Agent factory — type-driven

`ScoringAgent` stays separate from `BaseAgent` (Q2). It skips `UpdateAgentExecutionStatus` because scoring lifecycle is managed externally by `ScoringService`.

```go
func (f *AgentFactory) CreateAgent(execCtx *ExecutionContext) (Agent, error) {
    controller := f.controllerFactory.CreateController(execCtx.Config.Type, execCtx)
    switch execCtx.Config.Type {
    case AgentTypeScoring:
        return NewScoringAgent(controller), nil
    default:
        return NewBaseAgent(controller), nil
    }
}
```

## Agent type → controller → wrapper mapping (after refactor)

| `type` | Controller | Config | Agent wrapper |
|--------|-----------|--------|--------------|
| default (investigation) | IteratingController | — | BaseAgent |
| `synthesis` | SingleShotController | `BuildSynthesisMessages`, `ThinkingFallback: true` | BaseAgent |
| `scoring` | SingleShotController | `BuildScoringMessages`, `ThinkingFallback: false` | ScoringAgent |
| `orchestrator` | IteratingController | tools from CompositeToolExecutor | BaseAgent |

`llm_backend` is orthogonal — any type can use either `native-gemini` or `langchain`.

## Backward Compatibility (Q4)

**Clean cut — no migration code.** The `iteration_strategy` field is removed entirely. Config validation rejects it as an unknown field. Users update their configs to use `type` + `llm_backend` before upgrading.

### Before → after examples

```yaml
# Before
agents:
  - name: SecurityAgent
    iteration_strategy: native-thinking

# After
agents:
  - name: SecurityAgent
    llm_backend: native-gemini
```

```yaml
# Before
agents:
  - name: SynthesisAgent
    iteration_strategy: synthesis-native-thinking

# After
agents:
  - name: SynthesisAgent
    type: synthesis
    llm_backend: native-gemini
```

### Built-in agent updates

```go
// Current
"SynthesisAgent": {
    IterationStrategy: IterationStrategySynthesis,
    ...
}

// After refactor
"SynthesisAgent": {
    Type: AgentTypeSynthesis,
    ...
}
```

## Impact Analysis

### Files affected

| Area | Files | Change scope |
|------|-------|-------------|
| Config types | `pkg/config/agent.go`, `pkg/config/enums.go`, `pkg/config/types.go` | Add `Type`, `Description`, `LLMBackend`; remove `IterationStrategy` |
| Config loading | `pkg/config/loader.go`, `pkg/config/merge.go` | Carry `Type`, `Description`, `LLMBackend` through merge |
| Config validation | `pkg/config/validation.go` | Validate `Type` and `LLMBackend` values; remove strategy validation |
| Agent factory | `pkg/agent/factory.go` | Switch on `Type` instead of strategy |
| Controller factory | `pkg/agent/controller/factory.go` | Switch on `Type`; create configured controllers |
| Controllers | `pkg/agent/controller/` | Rename FC → IteratingController; replace SynthesisController with SingleShotController |
| Config resolver | `pkg/agent/config_resolver.go` | `ResolveBackend` takes `LLMBackend` instead of strategy |
| Built-in agents | `pkg/config/builtin.go` | Update `SynthesisAgent` to use `Type` |
| Session executor | `pkg/queue/executor.go` | Wire `Type` and `LLMBackend` through to agent factory |
| Tests | Many | Strategy → type + llm_backend migration in test cases |

### Risk

- **Medium**: Touches core agent creation path
- **Mitigated by**: existing test coverage, clean-cut migration (no dual-path code)
- **Testable**: all changes are in config resolution and factory wiring — well-covered by unit tests

## Sequencing (Q5)

This refactor ships **before** the orchestrator. It is independently valuable and establishes the `type` pattern that the orchestrator depends on.

### Implementation phases

1. **Config foundation:** Add `Type`, `Description`, `LLMBackend` fields. Remove `IterationStrategy`. Update validation. Update built-in agents.
2. **Controller restructuring:** Rename `FunctionCallingController` → `IteratingController`. Replace `SynthesisController` with parameterized `SingleShotController`. Update controller factory to switch on `Type`.
3. **Agent factory update:** Switch agent wrapper selection from `IsValidForScoring()` to `Type`. Update session executor wiring.
4. **Config inheritance:** Ensure `LLMBackend` inherits through global defaults → chain → stage → agent (same path as old `IterationStrategy`).
5. **Test migration:** Update all test cases from strategy-based to type + llm_backend.

Each phase is a reviewable PR. The orchestrator work begins after phase 5.

## Decisions

All questions resolved — see [agent-type-refactor-questions.md](agent-type-refactor-questions.md) for full rationale.

| # | Question | Decision |
|---|----------|----------|
| Q1 | Controller restructuring | Two structural controllers (IteratingController + SingleShotController) with parameterized config |
| Q2 | ScoringAgent merge | Keep separate; uses SingleShotController with scoring config |
| Q3 | Backend selection | Remove `iteration_strategy`, introduce `llm_backend` (`native-gemini`, `langchain`). Controller from `type`. |
| Q4 | Backward compatibility | Clean cut — no migration code |
| Q5 | Sequencing | Refactor first, then orchestrator |
