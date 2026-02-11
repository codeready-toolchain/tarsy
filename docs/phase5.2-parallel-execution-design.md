# Phase 5.2: Parallel Execution — Design

## Overview

Extend `executeStage()` in `RealSessionExecutor` to support parallel multi-agent and replica execution, with goroutine-per-agent concurrency, result aggregation, success policy enforcement, and automatic synthesis agent invocation.

**Current state**: `executeStage()` rejects stages with >1 agent or replicas >1 with an error guard. All stages are single-agent, single-execution.

**Target state**: `executeStage()` detects parallel stages and spawns goroutines. Per-agent context isolation (already achieved in Phase 4.1) enables safe concurrent execution. Results are aggregated per success policy. Synthesis always runs automatically after successful parallel stages (mandatory, no opt-out). The parallel stage guard is removed.

---

## Detection Logic

A stage is "parallel" when it requires more than one concurrent agent execution:

```go
func isParallelStage(cfg config.StageConfig) bool {
    return len(cfg.Agents) > 1 || cfg.Replicas > 1
}
```

Two forms of parallelism:

| Form | Trigger | Description |
|------|---------|-------------|
| **Multi-agent** | `len(agents) > 1` | Different agents investigate in parallel (potentially different strategies, providers, MCP servers) |
| **Replica** | `replicas > 1` | Same agent config runs N times for redundancy/diversity |

Both forms share the same goroutine execution machinery. They differ only in how execution configs are built.

---

## Execution Architecture

### Routing in `executeStage()`

`executeStage()` becomes a thin router that delegates to the appropriate method:

```go
func (e *RealSessionExecutor) executeStage(ctx context.Context, input executeStageInput) stageResult {
    if isParallelStage(input.stageConfig) {
        return e.executeParallelStage(ctx, input)
    }
    return e.executeSequentialStage(ctx, input)
}
```

`executeSequentialStage()` contains the current Phase 5.1 `executeStage()` body (renamed, no behavioral changes).

### Parallel Execution Flow

```
executeParallelStage(ctx, input):
  1. Build execution configs (multi-agent or replica)
  2. Create Stage DB record (with parallel_type, success_policy, expected_agent_count)
  3. Launch goroutines (one per execution config)
  4. Each goroutine: executeAgent(ctx, input, stage, agentConfig, index, displayName) → agentResult
  5. Wait for ALL goroutines to complete (WaitGroup)
  6. Collect agentResults, sort by index
  7. Aggregate status via success policy (in-memory)
  8. Call StageService.UpdateStageStatus() (DB consistency)
  9. Return stageResult (synthesis runs separately in the chain loop)
```

### Goroutine Management

Use `sync.WaitGroup` + buffered channel to collect results:

```go
func (e *RealSessionExecutor) executeParallelStage(ctx context.Context, input executeStageInput) stageResult {
    configs := e.buildParallelConfigs(input.stageConfig)

    // Create Stage DB record with parallel metadata
    stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
        SessionID:          input.session.ID,
        StageName:          input.stageConfig.Name,
        StageIndex:         input.stageIndex + 1, // 1-based in DB
        ExpectedAgentCount: len(configs),
        ParallelType:       parallelTypePtr(input.stageConfig),
        SuccessPolicy:      successPolicyPtr(input.stageConfig),
    })
    // ... error handling ...

    results := make(chan indexedAgentResult, len(configs))
    var wg sync.WaitGroup

    for i, cfg := range configs {
        wg.Add(1)
        go func(idx int, agentCfg config.StageAgentConfig, displayName string) {
            defer wg.Done()
            ar := e.executeAgent(ctx, input, stg, agentCfg, idx, displayName)
            results <- indexedAgentResult{index: idx, result: ar}
        }(i, cfg.agentConfig, cfg.displayName)
    }

    wg.Wait()
    close(results)

    // Collect and sort by original index
    agentResults := collectAndSort(results)

    // Aggregate status
    stageStatus := aggregateParallelStatus(agentResults, resolvedSuccessPolicy(input))

    // Update Stage in DB (triggers aggregation from AgentExecution records)
    input.stageService.UpdateStageStatus(ctx, stg.ID)

    return stageResult{
        stageID:      stg.ID,
        stageName:    input.stageConfig.Name,
        status:       stageStatus,
        err:          aggregateError(agentResults, stageStatus, input.stageConfig),
        agentResults: agentResults,
        // finalAnalysis intentionally empty — synthesis (mandatory) produces it
    }
}
```

**Why `WaitGroup` + channel, not `errgroup`?** We need ALL agents to complete regardless of individual failures — success policy determines the overall outcome. `errgroup` cancels remaining goroutines on first error, which is wrong for `policy: any` where some failures are expected.

### Internal Types

```go
// parallelConfig wraps agent config with display name for parallel execution.
type parallelConfig struct {
    agentConfig config.StageAgentConfig
    displayName string // for DB record and logs (differs from config name for replicas)
}

// indexedAgentResult pairs an agentResult with its original launch index.
type indexedAgentResult struct {
    index  int
    result agentResult
}
```

Extend `stageResult` with optional agent results for parallel stages:

```go
type stageResult struct {
    stageID       string
    executionID   string           // meaningful for sequential stages only
    stageName     string
    status        alertsession.Status
    finalAnalysis string
    err           error
    agentResults  []agentResult    // populated for parallel stages (nil for sequential)
}
```

---

## Context Isolation

Parallel goroutines are safe because `executeAgent()` already provides complete isolation per Phase 4.1/5.1 design:

| Concern | Isolation mechanism |
|---------|-------------------|
| **MCP sessions** | `createToolExecutor()` creates per-agent-execution `mcp.Client` with independent SDK sessions |
| **DB records** | Each execution has its own `AgentExecution`, `Message`, `TimelineEvent`, `LLMInteraction` records |
| **ExecutionContext** | Created fresh per `executeAgent()` call — no shared mutable state |
| **LLM conversation** | Messages stored per-execution (via `execution_id` foreign key) |
| **Logging** | `slog.With()` per execution includes agent_name, agent_index, execution_id |

Goroutines share only **read-only** state: `session` (ent object, immutable), `chain` config, `stageConfig`, `executeStageInput` (services are thread-safe singletons).

The one addition: the shared `ctx` carries session cancellation — this is intentional (cancelling the session should cancel all parallel agents).

---

## Building Execution Configs

### Multi-Agent

Each agent in `stage.Agents` becomes its own execution:

```go
func (e *RealSessionExecutor) buildMultiAgentConfigs(stageCfg config.StageConfig) []parallelConfig {
    configs := make([]parallelConfig, len(stageCfg.Agents))
    for i, agentCfg := range stageCfg.Agents {
        configs[i] = parallelConfig{
            agentConfig: agentCfg,
            displayName: agentCfg.Name,
        }
    }
    return configs
}
```

### Replica

The first agent config is replicated N times:

```go
func (e *RealSessionExecutor) buildReplicaConfigs(stageCfg config.StageConfig) []parallelConfig {
    baseAgent := stageCfg.Agents[0]
    replicas := stageCfg.Replicas
    configs := make([]parallelConfig, replicas)
    for i := 0; i < replicas; i++ {
        configs[i] = parallelConfig{
            agentConfig: baseAgent,                                            // base config — Name used for config lookup
            displayName: fmt.Sprintf("%s-%d", baseAgent.Name, i+1),           // display name for DB/logs
        }
    }
    return configs
}
```

**Replica naming**: `{AgentName}-{index}` (1-based). Matches old TARSy convention.

**Config resolution**: `ResolveAgentConfig()` looks up the agent definition by `agentConfig.Name` (the base name, e.g. "KubernetesAgent"), so config resolution works identically for all replicas. The `displayName` is only used for the `AgentExecution` DB record's `agent_name` field and logging.

### Combined Builder

```go
func (e *RealSessionExecutor) buildParallelConfigs(stageCfg config.StageConfig) []parallelConfig {
    if stageCfg.Replicas > 1 {
        return e.buildReplicaConfigs(stageCfg)
    }
    return e.buildMultiAgentConfigs(stageCfg)
}
```

---

## Changes to `executeAgent()`

Add a `displayName` parameter for the DB record and logging:

```go
func (e *RealSessionExecutor) executeAgent(
    ctx context.Context,
    input executeStageInput,
    stg *ent.Stage,
    agentConfig config.StageAgentConfig,
    agentIndex int,
    displayName string,  // NEW: overrides agentConfig.Name for DB record/logs
) agentResult {
```

- `displayName` is used for: `CreateAgentExecution.AgentName`, `ExecCtx.AgentName`, logger fields
- `agentConfig.Name` is used for: `ResolveAgentConfig()` (config registry lookup), `AgentFactory.CreateAgent()`

For sequential stages (backward-compatible): `executeAgent(ctx, input, stg, agentConfig, 0, agentConfig.Name)` — display name equals config name.

---

## Result Aggregation

### Status Aggregation

In-memory aggregation matching `StageService.UpdateStageStatus()` logic but run before the DB call for the executor to know the stage outcome:

```go
func aggregateParallelStatus(results []agentResult, policy config.SuccessPolicy) alertsession.Status {
    var completed, failed, timedOut, cancelled int

    for _, r := range results {
        switch mapAgentStatusToSessionStatus(r.status) {
        case alertsession.StatusCompleted:
            completed++
        case alertsession.StatusTimedOut:
            timedOut++
        case alertsession.StatusCancelled:
            cancelled++
        default:
            failed++
        }
    }

    nonSuccess := failed + timedOut + cancelled

    switch policy {
    case config.SuccessPolicyAll:
        if nonSuccess == 0 {
            return alertsession.StatusCompleted
        }
    default: // SuccessPolicyAny (default when unset)
        if completed > 0 {
            return alertsession.StatusCompleted
        }
    }

    // Stage failed — use most specific terminal status when uniform
    if nonSuccess == timedOut {
        return alertsession.StatusTimedOut
    }
    if nonSuccess == cancelled {
        return alertsession.StatusCancelled
    }
    return alertsession.StatusFailed
}
```

Matches old TARSy behavior: CANCELLED and TIMED_OUT are treated as non-successes for policy evaluation. When all non-successes share the same status, the stage inherits that specific status (better error messaging). Mixed failures → generic FAILED.

### Error Message Aggregation

For failed parallel stages, build a detailed error message listing each non-successful agent:

```go
func aggregateParallelErrors(
    results []agentResult,
    parallelType string,
    policy config.SuccessPolicy,
) string {
    // Example output:
    // "Multi_agent stage failed: 2/3 executions failed (policy: all)
    //
    // Failed agents:
    //   - KubernetesAgent (failed): LLM timeout
    //   - performance-agent (timed out): context deadline exceeded"
}
```

### Final Analysis Construction

Synthesis always runs after parallel stages (mandatory, no opt-out), so the parallel stage's `stageResult.finalAnalysis` is not used for context passing — the synthesis stage's `finalAnalysis` replaces it. The parallel `stageResult.finalAnalysis` can be left empty or set to a brief summary (e.g., "3/3 agents completed — awaiting synthesis") for logging purposes only.

---

## Success Policy Resolution

Add resolution to the executor with proper defaulting:

```go
func resolvedSuccessPolicy(input executeStageInput) config.SuccessPolicy {
    // Stage-level override
    if input.stageConfig.SuccessPolicy != "" {
        return input.stageConfig.SuccessPolicy
    }
    // System default
    if input.cfg.Defaults.SuccessPolicy != "" {
        return input.cfg.Defaults.SuccessPolicy
    }
    // Fallback default
    return config.SuccessPolicyAny
}
```

**Note**: `SuccessPolicyAny` is the fallback default, matching old TARSy and `tarsy.yaml.example`. The existing `UpdateStageStatus()` currently defaults to `SuccessPolicyAll` when nil — this must be fixed to default to `SuccessPolicyAny` as part of this phase.

The resolved policy is passed to both:
1. `CreateStageRequest.SuccessPolicy` (for DB persistence)
2. `aggregateParallelStatus()` (for in-memory executor logic)

---

## Synthesis Stage

### Invocation Criteria

Synthesis **always** runs after every successful parallel stage. There is no opt-out. The `synthesis:` config block is optional and only controls the agent, strategy, and provider — if omitted, defaults apply:

| Field | Default |
|-------|---------|
| `agent` | `"SynthesisAgent"` |
| `iteration_strategy` | `"synthesis"` |
| `llm_provider` | chain's `llm_provider` → `defaults.llm_provider` |

This eliminates the need for a separate "aggregate parallel final analyses" code path. Every parallel stage produces a single synthesized `finalAnalysis` via the synthesis agent.

### Synthesis as a Separate Stage

Synthesis creates its own `Stage` DB record, separate from the parallel stage. This is the cleanest approach because:
- No changes to `StageService.UpdateStageStatus()` aggregation logic
- Clear separation: parallel Stage status reflects only parallel agents; synthesis Stage status reflects synthesis
- Dashboard shows two distinct stages (e.g., "Investigation", "Investigation - Synthesis")
- Consistent with old TARSy's execution model (synthesis gets its own stage_execution record)

The alternative (synthesis as an AgentExecution within the parallel Stage) was rejected — it would require modifying `UpdateStageStatus()` to exclude synthesis from success policy aggregation and introduces semantic confusion between investigation and post-processing.

### Chain Loop Changes

The chain loop needs a running `dbStageIndex` that tracks the actual DB stage index (which may differ from the config stage index when synthesis stages are inserted):

```go
func (e *RealSessionExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
    // ... chain resolution, service initialization ...

    var completedStages []stageResult
    dbStageIndex := 0

    for _, stageCfg := range chain.Stages {
        if r := e.mapCancellation(ctx); r != nil {
            return r
        }

        // Update session progress
        e.updateSessionProgress(ctx, session.ID, dbStageIndex, "")

        // Publish stage started
        e.publishStageStatus(ctx, session.ID, "", stageCfg.Name, dbStageIndex, events.StageStatusStarted)

        sr := e.executeStage(ctx, executeStageInput{
            // ... fields ...
            stageIndex: dbStageIndex,
        })

        // Publish stage terminal status
        e.publishStageStatus(ctx, session.ID, sr.stageID, sr.stageName, dbStageIndex, mapTerminalStatus(sr))

        dbStageIndex++

        // Fail-fast
        if sr.status != alertsession.StatusCompleted {
            return &ExecutionResult{Status: sr.status, Error: sr.err}
        }

        // Synthesis always runs after successful parallel stages (no opt-out)
        if isParallelStage(stageCfg) {
            synthSr := e.executeSynthesisStage(ctx, executeStageInput{
                // ... fields ...
                stageIndex: dbStageIndex,
            }, sr)

            // Publish synthesis stage events
            e.publishStageStatus(ctx, session.ID, synthSr.stageID, synthSr.stageName, dbStageIndex, mapTerminalStatus(synthSr))

            dbStageIndex++

            if synthSr.status != alertsession.StatusCompleted {
                return &ExecutionResult{Status: synthSr.status, Error: synthSr.err}
            }

            // Synthesis result replaces parallel result for context passing
            completedStages = append(completedStages, synthSr)
        } else {
            completedStages = append(completedStages, sr)
        }

        prevContext = e.buildStageContext(completedStages)
    }

    // ... final analysis extraction, executive summary ...
}
```

**Key design decisions**: Synthesis always runs after parallel stages (mandatory, no opt-out). The synthesis `stageResult` replaces the parallel `stageResult` in `completedStages` — subsequent stages see only the synthesis output, not the raw parallel results. This keeps the context focused and eliminates the need for a separate parallel-aggregate code path.

### Context Building for Synthesis

Synthesis needs the **full investigation history** from each parallel agent — not just final analyses. The synthesis agent must evaluate evidence quality, verify reasoning chains, detect fabrications, and identify contradictions between agents. This means seeing thinking/reasoning, tool calls, tool results, and final conclusions. Passing only final analyses would reduce synthesis to a text-merging exercise.

This matches old TARSy, which passed `investigation_history` (full conversation) to synthesis.

#### Data source: Timeline Events (not Messages)

Timeline events are the right data source because they capture **everything** the agent did, including thinking content which is not stored in the Message schema:

| Timeline Event Type | Content | Why synthesis needs it |
|--------------------|---------|-----------------------|
| `llm_thinking` | Native thinking / internal reasoning | Evaluate reasoning quality, detect flawed logic |
| `llm_response` | Agent's text responses | See the agent's analysis and conclusions |
| `llm_tool_call` | Tool call + raw result | Verify evidence is real, check what data was gathered |
| `mcp_tool_summary` | Summarized tool result | See what the agent actually worked with |
| `final_analysis` | Agent's final conclusion | The agent's own summary |
| `code_execution` | Code execution results | See computed results |
| `google_search_result` | Search grounding | Verify external references |

Messages lack thinking content entirely (the Message schema has no thinking field — thinking is recorded only in timeline events and LLM interactions).

#### Data flow

1. After parallel agents complete, the executor has each agent's `executionID`
2. For each agent: `TimelineService.GetAgentTimeline(ctx, executionID)` — already exists, returns timeline events ordered by sequence number
3. `FormatInvestigationForSynthesis()` formats each agent's timeline into a structured section (reusing the same event-type formatting logic as the existing `FormatInvestigationContext()`)
4. All sections are concatenated into `prevStageContext` for the synthesis agent

#### Formatting function

New function in `pkg/agent/context/`:

```go
// FormatInvestigationForSynthesis formats parallel agents' full investigation
// histories for the synthesis agent. Uses timeline events (which include thinking,
// tool calls, tool results, and responses) rather than raw messages.
// Each agent's investigation is wrapped with identifying metadata.
func FormatInvestigationForSynthesis(agents []ParallelAgentInvestigation, stageName string) string
```

```go
type ParallelAgentInvestigation struct {
    AgentName    string
    AgentIndex   int
    Strategy     string                  // e.g., "native-thinking", "react"
    LLMProvider  string                  // e.g., "gemini-2.5-pro"
    Status       string                  // "completed", "failed", etc.
    Events       []*ent.TimelineEvent    // full investigation (from GetAgentTimeline)
    ErrorMessage string                  // for failed agents
}
```

The per-event formatting reuses the same `switch` logic as `FormatInvestigationContext()` (thinking → "Internal Reasoning", response → "Agent Response", tool call → "Tool Call", etc.). This will be extracted into a shared helper to avoid duplication.

#### Output format

```
<!-- PARALLEL_RESULTS_START -->

### Parallel Investigation: "Investigation" — 3/3 agents succeeded

#### Agent 1: KubernetesAgent (native-thinking, gemini-2.5-pro)
**Status**: completed

**Internal Reasoning:**

[thinking content — agent's chain of thought]

**Agent Response:**

[agent's text response]

**Tool Call:** [tool name + raw result]

**Tool Result Summary:**

[summarized tool result]

**Internal Reasoning:**

[more thinking...]

**Final Analysis:**

[agent's final conclusion]

#### Agent 2: KubernetesAgent (react, gemini-2.5-flash)
**Status**: completed

**Agent Response:**

Thought: I should check the pod status...
Action: kubernetes-server.get_pods
ActionInput: {"namespace": "production"}

**Tool Call:** [tool result]

**Final Analysis:**

[agent's final conclusion]

<!-- PARALLEL_RESULTS_END -->
```

For `policy: any` stages with mixed results, failed agents are included with their status and error:

```
#### Agent 2: performance-agent (react, gemini-2.5-flash)
**Status**: failed
**Error**: LLM call timeout

(No investigation history available)
```

This formatted context is passed as `prevStageContext` to the synthesis agent's `Execute()` call. The existing `SynthesisController` and `BuildSynthesisMessages()` handle it through the standard `FormatChainContext()` path.

#### Performance considerations

- DB queries are bounded: one `GetAgentTimeline()` per parallel agent (typically 2-5 agents)
- Queries run once at synthesis time, not in a loop
- Context window: Gemini models support 1M+ tokens, so even 3 agents × 20 iterations is well within limits
- Timeline events are already in DB from progressive writes during execution — no new writes needed
- Event formatting logic is shared with `FormatInvestigationContext()` (extract common helper)

### `executeSynthesisStage()`

```go
func (e *RealSessionExecutor) executeSynthesisStage(
    ctx context.Context,
    input executeStageInput,
    parallelResult stageResult,
) stageResult {
    synthStageName := parallelResult.stageName + " - Synthesis"

    // Create synthesis Stage DB record
    stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
        SessionID:          input.session.ID,
        StageName:          synthStageName,
        StageIndex:         input.stageIndex + 1, // 1-based in DB
        ExpectedAgentCount: 1,
        // No parallel_type, no success_policy (sequential single-agent)
    })
    // ... error handling ...

    // Build synthesis agent config — synthesis: block is optional, defaults apply
    synthAgentConfig := config.StageAgentConfig{
        Name:              "SynthesisAgent",
        IterationStrategy: config.IterationStrategySynthesis,
    }
    if s := input.stageConfig.Synthesis; s != nil {
        if s.Agent != "" {
            synthAgentConfig.Name = s.Agent
        }
        if s.IterationStrategy != "" {
            synthAgentConfig.IterationStrategy = s.IterationStrategy
        }
        if s.LLMProvider != "" {
            synthAgentConfig.LLMProvider = s.LLMProvider
        }
    }

    // Build synthesis context: query full conversation history for each parallel agent
    synthContext := e.buildSynthesisContext(ctx, parallelResult, input)

    // Execute synthesis agent (reuses executeAgent infrastructure)
    // Override prevContext to feed parallel investigation histories to synthesis
    synthInput := input
    synthInput.prevContext = synthContext

    ar := e.executeAgent(ctx, synthInput, stg, synthAgentConfig, 0, synthAgentConfig.Name)

    // Update synthesis stage status
    input.stageService.UpdateStageStatus(ctx, stg.ID)

    return stageResult{
        stageID:       stg.ID,
        executionID:   ar.executionID,
        stageName:     synthStageName,
        status:        mapAgentStatusToSessionStatus(ar.status),
        finalAnalysis: ar.finalAnalysis,
        err:           ar.err,
    }
}
```

### Synthesis Failure

If synthesis fails (LLM error, timeout), the synthesis stage fails, which triggers fail-fast in the chain loop. The session's final status reflects the synthesis failure (e.g., `failed`, `timed_out`). No fail-open fallback — synthesis is a configured chain step that influences subsequent stages, not a convenience feature. Parallel agents' work is preserved in DB for debugging.

---

## Replica Execution

### Configuration

```yaml
stages:
  - name: "analysis"
    agents:
      - name: "KubernetesAgent"
    replicas: 3
    success_policy: "any"
    synthesis:
      agent: "SynthesisAgent"
      iteration_strategy: "synthesis-native-thinking"
```

### Behavior

- All replicas use the same base agent config (same strategy, provider, MCP servers)
- Each replica gets its own `AgentExecution` record with display name `KubernetesAgent-1`, `KubernetesAgent-2`, etc.
- Config resolution uses the base agent name (`KubernetesAgent`) for registry lookup
- Each replica gets its own independent MCP client (no shared sessions)
- Replicas run concurrently with the same parallel goroutine machinery as multi-agent stages

### Difference from Multi-Agent

| Aspect | Multi-Agent | Replica |
|--------|-------------|---------|
| Config source | `stage.Agents[]` (different entries) | `stage.Agents[0]` × `stage.Replicas` |
| Agent names | Each agent's own name | `{BaseName}-1`, `{BaseName}-2`, ... |
| Config resolution | Each agent resolved independently | All share same resolved config |
| `parallel_type` in DB | `"multi_agent"` | `"replica"` |
| Typical use case | Different domain expertise | Redundancy/accuracy via diversity |

---

## Cancellation Handling

Context cancellation propagates naturally to all parallel goroutines:

1. Session cancellation/timeout sets `ctx.Done()` on the parent context
2. All goroutines share this context through `executeAgent(ctx, ...)`
3. Each agent's iteration controller checks `ctx.Err()` between iterations
4. `BaseAgent.Execute()` maps context errors to appropriate status (`ExecutionStatusCancelled` / `ExecutionStatusTimedOut`)
5. Goroutines complete normally with cancelled/timed_out results
6. `WaitGroup` unblocks when all goroutines finish
7. `aggregateParallelStatus()` produces the stage's terminal status
8. Chain loop's `mapCancellation()` checks between stages

**No special cancellation handling needed** — Go's hierarchical context cancellation and the existing error mapping handle everything. This is one of the advantages of goroutines + context over the more complex cancellation tracking in old TARSy (which used a `CancellationTracker` + `asyncio.CancelledError` + per-agent checking).

---

## Events and Progress Tracking

### Stage Status Events

Parallel stages emit the same `stage.status` events as sequential stages:

| Event | When | StageID present? |
|-------|------|-----------------|
| `stage.status: started` | Before goroutines launch | No (Stage not created yet) |
| `stage.status: completed/failed/...` | After all goroutines complete + aggregation | Yes |

If synthesis follows:

| Event | When | StageID present? |
|-------|------|-----------------|
| `stage.status: started` (synthesis) | Before synthesis agent runs | No |
| `stage.status: completed/failed/...` (synthesis) | After synthesis completes | Yes |

### Session Progress

`current_stage_index` and `current_stage_id` updated as before, using `dbStageIndex` instead of the config loop index. Dashboard shows which stage is currently executing.

### Per-Agent Timeline Events

Each parallel agent's timeline events (thinking, responses, tool calls) are scoped to their `AgentExecution` via the `execution_id` field. The dashboard can display them grouped by agent within the parallel stage. No changes needed — the existing event system handles this through `execution_id` partitioning.

---

## Stage Context for Next Stage

Synthesis always runs after parallel stages, so the next stage always receives the synthesis `stageResult.finalAnalysis` via `BuildStageContext()`. No changes needed to `BuildStageContext()` — it already handles a list of `StageResult{StageName, FinalAnalysis}`, and synthesis produces exactly that.

---

## Implementation Plan

### File Changes

| File | Change |
|------|--------|
| `pkg/queue/executor.go` | **Major**: Remove parallel guard. Add `executeParallelStage()`, `executeSynthesisStage()`, `buildParallelConfigs()`, `aggregateParallelStatus()`. Refactor chain loop for `dbStageIndex` and mandatory synthesis. Add `displayName` param to `executeAgent()`. Rename current `executeStage()` body to `executeSequentialStage()`. |
| `pkg/agent/context/stage_context.go` | **Add**: `FormatInvestigationForSynthesis()` function and `ParallelAgentInvestigation` type. Extract shared event formatting helper from `FormatInvestigationContext()` to avoid duplication. |
| `pkg/config/types.go` | **Verify**: `SynthesisConfig`, `StageAgentConfig`, `SuccessPolicy` already exist. May need `Defaults.SuccessPolicy` wiring. |
| `pkg/queue/types.go` | **Minor**: No changes expected (stageResult is internal to executor.go) |
| `pkg/services/stage_service.go` | **Fix**: `UpdateStageStatus()` default policy from `all` → `any`. Verify `CreateStage()` handles parallel_type/success_policy correctly. |

### Files That Need No Changes

| File | Reason |
|------|--------|
| `pkg/agent/agent.go` | Agent interface unchanged |
| `pkg/agent/base_agent.go` | Controller delegation unchanged |
| `pkg/agent/controller/synthesis.go` | SynthesisController unchanged — receives context via standard prevStageContext |
| `pkg/agent/prompt/builder.go` | BuildSynthesisMessages unchanged — synthesis prompt path already works |
| `pkg/mcp/` | Per-agent MCP isolation already complete |
| `pkg/events/` | Event types and payloads unchanged |
| `ent/schema/` | Stage and AgentExecution schemas already have parallel fields |

### New Test Coverage

| Test | What it validates |
|------|------------------|
| **Parallel multi-agent: all succeed** | 3 agents complete, stage completes, each has own execution record |
| **Parallel multi-agent: one fails (policy=all)** | Stage fails, all agents still run to completion |
| **Parallel multi-agent: one fails (policy=any)** | Stage succeeds, failed agent recorded |
| **Replica: all succeed** | N replicas run, naming convention correct, config resolution uses base name |
| **Replica: mixed results (policy=any)** | Stage succeeds if any replica completes |
| **Synthesis after parallel** | Synthesis runs, creates own Stage, receives formatted parallel context |
| **Synthesis failure** | Synthesis fails → stage chain fails (fail-fast) |
| **Synthesis with defaults** | No `synthesis:` config block → defaults apply (SynthesisAgent, synthesis strategy) |
| **Synthesis with overrides** | Custom agent/strategy/provider from `synthesis:` block respected |
| **Parallel + cancellation** | Session cancel propagates to all goroutines, all terminate cleanly |
| **Parallel + timeout** | Session timeout propagates, timed_out status aggregated |
| **Context isolation** | Each agent's messages/timeline scoped to own execution_id |
| **Parallel stage events** | stage.status published for start and terminal state |
| **Synthesis stage events** | Separate stage.status events for synthesis |
| **Chain: parallel → sequential** | Sequential stage receives synthesis/aggregate context |
| **Chain: sequential → parallel** | Parallel stage receives previous sequential stage context |
| **Success policy default** | Empty policy resolves to configured default |
| **Status aggregation edge cases** | Mixed failures (some timed_out, some failed), all cancelled, etc. |

Integration tests should use `testcontainers-go` with PostgreSQL (matching existing test infrastructure).

---

## Summary of Departures from Old TARSy

| Aspect | Old TARSy | New TARSy | Reason |
|--------|-----------|-----------|--------|
| Concurrency | `asyncio.gather()` | Goroutines + WaitGroup | Go idiomatic |
| Context isolation | Deep copy of `ChainContext` | Per-execution MCP client + ExecutionContext (already isolated) | Go architecture doesn't need deep copies |
| Synthesis context | Full `investigation_history` (conversation) | Full timeline events from DB (includes thinking) | Same approach — synthesis needs full evidence including reasoning to evaluate quality |
| Context to next stage | Both parallel + synthesis results | Only synthesis result | Synthesis consolidates parallel findings — passing both would be redundant and waste context window |
| Synthesis invocation | Always automatic after parallel success | Always automatic (same) | Matches old TARSy — synthesis is mandatory for parallel stages |
| Pause/resume | Supported (SessionPaused exception) | Not implemented (deferred) | New TARSy doesn't have pause/resume |
| Parent/child stages | Parent + child StageExecution records | Stage + AgentExecution records | New TARSy's data model is already cleaner |
| Cancellation tracking | CancellationTracker + is_user_cancel | Go context cancellation (hierarchical) | Simpler, built into language |
| Default success policy | `SuccessPolicy.ANY` | `SuccessPolicyAny` (configurable) | Matches old TARSy default |
