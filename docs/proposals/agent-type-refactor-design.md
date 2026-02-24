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

  # Future: orchestrator type (not part of this refactor)
  # MyOrchestrator:
  #   type: orchestrator
  #   description: "Dynamic investigation orchestrator"

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
    AgentTypeDefault   AgentType = ""             // Regular investigation agent
    AgentTypeSynthesis AgentType = "synthesis"
    AgentTypeScoring   AgentType = "scoring"
    // Future: AgentTypeOrchestrator AgentType = "orchestrator"
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
    // Future: AgentTypeOrchestrator → NewIteratingController() (tools from CompositeToolExecutor)
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
| *future: `orchestrator`* | *IteratingController* | *tools from CompositeToolExecutor* | *BaseAgent* |

`llm_backend` is orthogonal — any type can use either `native-gemini` or `langchain`.

## Backward Compatibility (Q4)

**Clean cut — no migration code.** The `iteration_strategy` field is removed entirely. Config validation rejects it as an unknown field. Users update their configs to use `type` + `llm_backend` before upgrading.

### Before → after examples

**Top-level defaults:**

```yaml
# Before                              # After
defaults:                             defaults:
  iteration_strategy: native-thinking   llm_backend: native-gemini
```

**Chain-level:**

```yaml
# Before                              # After
agent_chains:                         agent_chains:
  my-chain:                             my-chain:
    iteration_strategy: langchain         llm_backend: langchain
```

**Stage agent override:**

```yaml
# Before                              # After
agents:                               agents:
  - name: SecurityAgent                 - name: SecurityAgent
    iteration_strategy: native-thinking     llm_backend: native-gemini
```

**Synthesis config:**

```yaml
# Before                              # After
synthesis:                            synthesis:
  iteration_strategy: native-thinking   llm_backend: native-gemini
  # type is implicit — synthesis config always creates a synthesis agent
```

**Chat config:**

```yaml
# Before                              # After
chat:                                 chat:
  iteration_strategy: native-thinking   llm_backend: native-gemini
  # ChatAgent is type: default (iterating), no change needed for type
```

**Scoring config:**

```yaml
# Before                              # After
scoring:                              scoring:
  iteration_strategy: scoring           llm_backend: langchain
  # type: scoring is now implicit — scoring config always creates a scoring agent
```

**Built-in SynthesisAgent:**

```yaml
# Before (compound strategy encodes both type and backend)
iteration_strategy: synthesis-native-thinking

# After (type and backend are separate)
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

## ResolvedAgentConfig changes

The internal `ResolvedAgentConfig` struct (used by controllers and executors) needs corresponding updates:

```go
// pkg/agent/context.go
type ResolvedAgentConfig struct {
    AgentName          string
    Type               config.AgentType         // NEW — drives controller + wrapper selection
    LLMBackend         config.LLMBackend        // RENAMED from IterationStrategy
    LLMProvider        *config.LLMProviderConfig
    LLMProviderName    string
    MaxIterations      int
    IterationTimeout   time.Duration
    MCPServers         []string
    CustomInstructions string
    Backend            string                   // Resolved from LLMBackend (unchanged semantics)
    NativeToolsOverride *models.NativeToolsConfig
}
```

The `resolveIterationStrategy()` helper becomes `resolveLLMBackend()` — same last-non-empty-wins logic, different type. The `Type` is resolved from agent definition (not overridable at chain/stage level — an agent's type is intrinsic).

### Config inheritance structs that carry `IterationStrategy` → `LLMBackend`

All of these structs have an `IterationStrategy` field that becomes `LLMBackend`:

- `Defaults` (`pkg/config/defaults.go`) — global default
- `ChainConfig` (`pkg/config/types.go`) — chain-level override
- `StageAgentConfig` (`pkg/config/types.go`) — per-agent-in-stage override
- `SynthesisConfig` (`pkg/config/types.go`) — synthesis backend override
- `ChatConfig` (`pkg/config/types.go`) — chat backend override
- `ScoringConfig` (`pkg/config/types.go`) — scoring backend override
- `AgentConfig` (`pkg/config/agent.go`) — agent definition

### Scoring resolution simplification

`ResolveScoringConfig` currently validates `IsValidForScoring()` on the resolved strategy. After the refactor, scoring is identified by `type: scoring` — the `LLMBackend` is just a backend choice and doesn't need scoring-specific validation.

## DB schema impact

`ent/schema/agentexecution.go` has an `iteration_strategy` column used for observability. This becomes `llm_backend`. Since Q4 decided on a clean cut (no migration code), this is a schema migration — rename the column and update all writers/readers.

## Impact Analysis

### Files affected

| Area | Files | Change scope |
|------|-------|-------------|
| Config types | `pkg/config/agent.go`, `pkg/config/enums.go` | Add `AgentType`, `LLMBackend` types; remove `IterationStrategy` type and all methods (`IsValid`, `IsValidForScoring`) |
| Config structs | `pkg/config/types.go`, `pkg/config/defaults.go` | `IterationStrategy` → `LLMBackend` on `Defaults`, `ChainConfig`, `StageAgentConfig`, `SynthesisConfig`, `ChatConfig`, `ScoringConfig`; add `Type`, `Description` to `AgentConfig` |
| Config loading | `pkg/config/loader.go`, `pkg/config/merge.go` | Carry `Type`, `Description`, `LLMBackend` through merge |
| Config validation | `pkg/config/validation.go` | Validate `Type` and `LLMBackend` values; remove strategy validation |
| Built-in agents | `pkg/config/builtin.go` | Update `BuiltinAgentConfig`: `IterationStrategy` → `Type`; update `SynthesisAgent` definition |
| Resolved config | `pkg/agent/context.go` | `ResolvedAgentConfig`: add `Type`, rename `IterationStrategy` → `LLMBackend` |
| Config resolver | `pkg/agent/config_resolver.go` | `ResolveBackend` takes `LLMBackend`; `resolveIterationStrategy` → `resolveLLMBackend`; `ResolveChatAgentConfig`/`ResolveScoringConfig` resolve `LLMBackend` instead of strategy; remove `IsValidForScoring` check from scoring resolution |
| Agent factory | `pkg/agent/factory.go` | Switch on `Type` instead of strategy |
| Controller factory | `pkg/agent/controller/factory.go` | Switch on `Type`; create configured controllers |
| Controllers | `pkg/agent/controller/` | Rename `FunctionCallingController` → `IteratingController`; replace `SynthesisController` with parameterized `SingleShotController` |
| Session executor | `pkg/queue/executor.go` | Wire `Type` and `LLMBackend` through to agent factory |
| Chat executor | `pkg/queue/chat_executor.go` | Pass `LLMBackend` instead of `IterationStrategy` to `CreateAgentExecution` |
| DB schema | `ent/schema/agentexecution.go` | Rename `iteration_strategy` column → `llm_backend` |
| Tests | Many | Strategy → type + llm_backend migration in test cases |

### Risk

- **Medium**: Touches core agent creation path
- **Mitigated by**: existing test coverage, clean-cut migration (no dual-path code)
- **Testable**: all changes are in config resolution and factory wiring — well-covered by unit tests

## Sequencing (Q5)

This refactor ships **before** the orchestrator. It is independently valuable and establishes the `type` pattern that the orchestrator depends on.

### Implementation phases

1. **Config foundation:** Add `AgentType`, `LLMBackend` types to `enums.go`. Add `Type`, `Description`, `LLMBackend` fields to `AgentConfig`. Replace `IterationStrategy` with `LLMBackend` on all config structs (`Defaults`, `ChainConfig`, `StageAgentConfig`, `SynthesisConfig`, `ChatConfig`, `ScoringConfig`). Remove `IterationStrategy` type and methods. Update `BuiltinAgentConfig` and built-in agent definitions. Update validation.
2. **Config resolution:** Update `ResolvedAgentConfig` (add `Type`, rename `IterationStrategy` → `LLMBackend`). `resolveIterationStrategy` → `resolveLLMBackend`. Update `ResolveAgentConfig`, `ResolveChatAgentConfig`, `ResolveScoringConfig`. Simplify `ResolveBackend`. Remove `IsValidForScoring` check.
3. **Controller restructuring:** Rename `FunctionCallingController` → `IteratingController`. Replace `SynthesisController` with parameterized `SingleShotController`. Update controller factory to switch on `Type`.
4. **Factory + executor wiring:** Switch agent factory from strategy to `Type`. Update session executor and chat executor to wire `Type` and `LLMBackend`.
5. **DB schema migration:** Rename `iteration_strategy` column → `llm_backend` on `AgentExecution`. Update all writers/readers.

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
