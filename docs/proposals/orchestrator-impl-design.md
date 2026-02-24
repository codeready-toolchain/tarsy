# Orchestrator Agent — Implementation Design

**Status:** All questions decided — ready for implementation
**Vision:** [orchestrator-agent-design.md](orchestrator-agent-design.md)
**Last updated:** 2026-02-19

## Overview

This document translates the [orchestrator vision](orchestrator-agent-design.md) into a concrete implementation plan against TARSy's existing codebase. It covers config model changes, new Go types, controller architecture, database schema evolution, prompt construction, and dashboard integration.

The orchestrator agent is a standard TARSy agent with three additional tools (`dispatch_agent`, `cancel_agent`, `list_agents`) and a push-based result collection model. Sub-agent results are automatically injected into the orchestrator's conversation as they complete — the LLM never polls for results.

## Design Principles (Implementation-Specific)

1. **Push-based result collection.** Inspired by OpenClaw's sub-agent architecture. `dispatch_agent` returns immediately; sub-agent results are automatically injected into the orchestrator's conversation as they complete. The LLM never polls — it dispatches, continues working, and sees results appear between iterations.

2. **Minimal controller modification.** The orchestrator reuses `IteratingController` with one targeted change: before the loop exits (when the LLM has no tool calls), it checks for pending sub-agents and waits for them. Available results are also drained non-blockingly before each LLM call. The iteration loop structure stays intact.

3. **ToolExecutor is the seam.** The existing `ToolExecutor` interface (`ListTools` + `Execute`) is the integration point. A composite executor wraps MCP tools + orchestration tools into a single tool set.

4. **Sub-agents are regular executions.** Sub-agents run through the same `ResolveAgentConfig → CreateToolExecutor → AgentFactory.CreateAgent → Execute` path as any agent. No shortcut.

5. **DB records follow the existing model.** Sub-agent runs create real `AgentExecution` records with timeline events, linked to the orchestrator via a new `parent_execution_id` column.

## Architecture

### Component Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│  Session Executor (pkg/queue/executor.go)                            │
│                                                                      │
│  executeStage → executeAgent → AgentFactory.CreateAgent              │
│       │                              │                               │
│       │                     ┌────────┴──────────┐                    │
│       │                     │  Agent             │                    │
│       │                     │  (type=orchestrator)│                    │
│       │                     └────────┬──────────┘                    │
│       │                              │                               │
│       │                     ┌────────┴──────────────────┐            │
│       │                     │  IteratingController       │            │
│       │                     │  (+ push-based drain/wait) │            │
│       │                     └────────┬──────────────────┘            │
│       │                              │                               │
│       │                     ┌────────┴──────────────────┐            │
│       │                     │  CompositeToolExecutor     │            │
│       │                     │  ├─ MCP tools (Loki, etc.)│            │
│       │                     │  └─ Orchestration tools    │            │
│       │                     │     ├─ dispatch_agent      │            │
│       │                     │     ├─ cancel_agent        │            │
│       │                     │     └─ list_agents         │            │
│       │                     └────────┬──────────────────┘            │
│       │                              │                               │
│       │                     ┌────────┴──────────────────┐            │
│       │                     │  SubAgentRunner            │            │
│       │                     │  (spawns/tracks sub-agents)│            │
│       │                     │                            │            │
│       │                     │  goroutine per sub-agent:  │            │
│       │                     │  ┌──────────────────────┐  │            │
│       │                     │  │ ResolveAgentConfig   │  │            │
│       │                     │  │ CreateToolExecutor   │  │            │
│       │                     │  │ AgentFactory.Create  │  │            │
│       │                     │  │ agent.Execute()      │  │            │
│       │                     │  └──────────────────────┘  │            │
│       │                     └────────────────────────────┘            │
└──────────────────────────────────────────────────────────────────────┘
```

### Data Flow

```
1. Orchestrator's LLM call returns tool_calls:
   dispatch_agent(name="LogAnalyzer", task="Find 5xx errors...")
   dispatch_agent(name="MetricChecker", task="Check latency...")
2. CompositeToolExecutor.Execute routes each to SubAgentRunner.Dispatch
3. SubAgentRunner (per dispatch):
   a. Creates AgentExecution record (parent_execution_id = orchestrator's execution_id)
   b. Resolves agent config, creates MCP tool executor
   c. Spawns goroutine: agentFactory.CreateAgent → agent.Execute
   d. Sends result to shared results channel when goroutine finishes
   e. Returns immediately: { execution_id: "sub-exec-123", status: "accepted" }
4. Tool results go back to LLM as: "dispatched, execution_id: sub-exec-123"
5. LLM has no more tool calls → controller checks SubAgentRunner.HasPending()
   → YES → SubAgentRunner.WaitForNext(ctx) blocks until a sub-agent finishes
6. LogAnalyzer finishes → result injected into conversation:
   "[Sub-agent completed] LogAnalyzer (exec-abc): Found 2,847 5xx errors..."
7. Controller continues iteration → LLM sees result, dispatches more or synthesizes
8. Before each LLM call: SubAgentRunner.TryGetNext() drains any results
   that arrived while the LLM was being called or tools were executing
```

### Push-Based Result Collection

The orchestrator LLM never polls for results. Instead:

- **`dispatch_agent`** returns immediately with an execution ID
- **Sub-agent results** are automatically injected into the conversation as they complete
- **Before each LLM call**: non-blocking drain of any available results (`TryGetNext`)
- **When LLM is idle** (no tool calls but sub-agents pending): blocking wait (`WaitForNext`)

This means the LLM can:
1. Dispatch across multiple iterations (dispatch A → do other work → dispatch B)
2. See results as soon as they're ready (even mid-workflow)
3. React to early results (cancel unnecessary agents, dispatch follow-ups)
4. Never waste iterations polling

## Config Model Changes

### `AgentConfig` — `Type` and `Description` already exist (ADR-0001)

Per [ADR-0001](../adr/0001-agent-type-refactor.md), `AgentConfig` already has `Type`, `Description`, and `LLMBackend` fields. The orchestrator adds one constant:

```go
const AgentTypeOrchestrator AgentType = "orchestrator"
```

The controller factory already switches on `Type` and maps the orchestrator to `IteratingController` (the multi-turn tool-calling loop). The `LLMBackend` is orthogonal — the orchestrator can use `native-gemini` or `langchain`.

`Description` and `Type` are already carried through the built-in → user config merge.

### Orchestrator-specific config — DECIDED

> **Decision:** Nested `orchestrator` section + global defaults under `defaults:` — see [questions](orchestrator-impl-questions.md), Q5.

```yaml
defaults:
  orchestrator:                     # Global orchestrator defaults
    max_concurrent_agents: 5
    agent_timeout: 300s
    max_budget: 600s

agents:
  MyOrchestrator:
    type: orchestrator
    description: "Dynamic SRE investigation orchestrator"
    custom_instructions: |
      You investigate alerts by dispatching specialized sub-agents...
    orchestrator:                    # Per-agent override (optional)
      max_concurrent_agents: 3
```

### `sub_agents` override — DECIDED

> **Decision:** Full hierarchy (chain + stage + stage-agent), all levels optional — see [questions](orchestrator-impl-questions.md), Q6.

Follows the same override pattern as `mcp_servers`, `llm_provider`, `max_iterations`:

```yaml
agent_chains:
  focused-investigation:
    sub_agents: [LogAnalyzer, MetricChecker]    # Chain-level (most common)
    stages:
      - name: investigate
        # sub_agents: [...]                     # Stage-level (optional)
        agents:
          - name: MyOrchestrator
            # sub_agents: [...]                 # Stage-agent level (optional)
```

If omitted at all levels, the orchestrator sees the full global registry (all agents with `description`).

## Sub-Agent Registry

A new `SubAgentRegistry` type built at config load time from the merged agent registry:

```go
// pkg/config/sub_agent_registry.go
type SubAgentEntry struct {
    Name        string
    Description string
    MCPServers  []string
}

type SubAgentRegistry struct {
    entries []SubAgentEntry
}

func BuildSubAgentRegistry(agents map[string]*AgentConfig, builtinDescs map[string]string) *SubAgentRegistry {
    // Include agents with Description set (config agents + built-ins)
    // Exclude orchestrator agents themselves
}
```

The registry is filtered at runtime when the orchestrator is created, applying any `sub_agents` override from the chain/stage config.

## Orchestration Tools

### Tool Definitions

Two action tools + one status tool registered via `CompositeToolExecutor.ListTools`. There is no `get_result` tool — results are pushed automatically into the conversation.

```go
var orchestrationTools = []agent.ToolDefinition{
    {
        Name:        "dispatch_agent",
        Description: "Dispatch a sub-agent to execute a task. Returns immediately. Results are automatically delivered when the sub-agent finishes — do not poll.",
        ParametersSchema: `{
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Agent name from the available agents list"},
                "task": {"type": "string", "description": "Natural language task description"}
            },
            "required": ["name", "task"]
        }`,
    },
    {
        Name:        "cancel_agent",
        Description: "Cancel a running sub-agent.",
        ParametersSchema: `{
            "type": "object",
            "properties": {
                "execution_id": {"type": "string", "description": "Execution ID from dispatch_agent"}
            },
            "required": ["execution_id"]
        }`,
    },
    {
        Name:        "list_agents",
        Description: "List all dispatched sub-agents and their current status. Use for status overview before deciding to cancel or dispatch more.",
        ParametersSchema: `{
            "type": "object",
            "properties": {}
        }`,
    },
}
```

### Tool Naming — DECIDED

> **Decision:** Plain names (`dispatch_agent`, `cancel_agent`, `list_agents`) — see [questions](orchestrator-impl-questions.md), Q3.

MCP tools use `server.tool` naming (e.g., `kubernetes-server.get_pod`). Orchestration tools use plain names without dots — natural namespace separation. The `CompositeToolExecutor` routes by matching the known orchestration tool names.

## CompositeToolExecutor — DECIDED

> **Decision:** Wrapper pattern (Option A) — see [questions](orchestrator-impl-questions.md), Q2.

```go
// pkg/agent/orchestrator/tool_executor.go
type CompositeToolExecutor struct {
    mcpExecutor agent.ToolExecutor          // Existing MCP tools (may be nil/stub)
    runner      *SubAgentRunner             // Handles dispatch/cancel/list
    registry    *config.SubAgentRegistry    // Available agents for dispatch
}

func (c *CompositeToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
    // MCP tools + orchestration tools (dispatch_agent, cancel_agent, list_agents)
}

func (c *CompositeToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
    if isOrchestrationTool(call.Name) {
        return c.executeOrchestrationTool(ctx, call)
    }
    return c.mcpExecutor.Execute(ctx, call)
}

func (c *CompositeToolExecutor) Close() error {
    // Cancel any still-running sub-agents and wait for them to finish
    c.runner.CancelAll()
    c.runner.WaitAll(context.Background())
    if c.mcpExecutor != nil {
        return c.mcpExecutor.Close()
    }
    return nil
}
```

## SubAgentRunner

Manages the lifecycle of sub-agent goroutines within an orchestrator execution. Provides both push-based result delivery (via a results channel) and lifecycle management.

```go
// pkg/agent/orchestrator/runner.go
type SubAgentRunner struct {
    mu          sync.Mutex
    executions  map[string]*subAgentExecution   // execution_id → state
    resultsCh   chan *SubAgentResult             // completed results (buffered)
    pending     int32                            // atomic count of running sub-agents
    
    deps           *SubAgentDeps                // Dependency bundle (Q10)
    parentExecID   string                       // Orchestrator's execution_id
    registry       *config.SubAgentRegistry     // Available agents for dispatch
    guardrails     *OrchestratorGuardrails       // Resolved from defaults + per-agent config (Q5)
}

// SubAgentDeps bundles dependencies extracted from the session executor.
type SubAgentDeps struct {
    Config         *config.Config
    AgentFactory   *agent.AgentFactory
    MCPFactory     *mcp.ClientFactory
    LLMClient      agent.LLMClient
    EventPublisher agent.EventPublisher
    PromptBuilder  *prompt.PromptBuilder
    DBClient       *ent.Client
}

// OrchestratorGuardrails holds resolved orchestrator limits (defaults + per-agent override).
type OrchestratorGuardrails struct {
    MaxConcurrentAgents int
    AgentTimeout        time.Duration
    MaxBudget           time.Duration
}

type subAgentExecution struct {
    executionID string
    agentName   string
    task        string
    status      agent.ExecutionStatus
    result      *agent.ExecutionResult
    cancel      context.CancelFunc
    done        chan struct{}
}

type SubAgentResult struct {
    ExecutionID string
    AgentName   string
    Task        string
    Status      agent.ExecutionStatus
    Result      string  // free text output
    Error       string  // error message if failed
}
```

### Dispatch

```go
func (r *SubAgentRunner) Dispatch(ctx context.Context, name, task string) (string, error) {
    // 1. Validate agent exists in registry → error if not found
    // 2. Check max_concurrent_agents guardrail → error if exceeded
    //    (counts executions where status == running)
    // 3. Create AgentExecution DB record:
    //    - parent_execution_id = orchestrator's execution ID
    //    - task = task text
    //    - status = "running"
    // 4. Resolve agent config (same path as executeAgent)
    // 5. Create MCP tool executor for sub-agent's own MCP servers
    // 6. Build ExecutionContext with Task field set (triggers sub-agent prompt template)
    // 7. Spawn goroutine:
    //    - Derive context with agent_timeout deadline from guardrails
    //    - agentFactory.CreateAgent → agent.Execute
    //    - On completion: send SubAgentResult to resultsCh
    //    - On timeout/cancel: update DB status → "cancelled" / "failed"
    // 8. Increment pending counter (atomic)
    // 9. Return execution_id immediately
}
```

### TryGetNext (non-blocking)

```go
func (r *SubAgentRunner) TryGetNext() (*SubAgentResult, bool) {
    // Non-blocking read from resultsCh
    select {
    case result := <-r.resultsCh:
        atomic.AddInt32(&r.pending, -1)
        return result, true
    default:
        return nil, false
    }
}
```

Called before each LLM call to drain any results that arrived while tools were executing.

### WaitForNext (blocking)

```go
func (r *SubAgentRunner) WaitForNext(ctx context.Context) (*SubAgentResult, error) {
    // Blocking read — waits until at least one sub-agent finishes
    select {
    case result := <-r.resultsCh:
        atomic.AddInt32(&r.pending, -1)
        return result, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

Called when the LLM has no tool calls but sub-agents are still pending. The controller pauses here until a result arrives.

### HasPending

```go
func (r *SubAgentRunner) HasPending() bool {
    return atomic.LoadInt32(&r.pending) > 0
}
```

### Cancel

```go
func (r *SubAgentRunner) Cancel(executionID string) string {
    // Call cancel() on the sub-agent's context
    // Return status
}
```

### List

```go
type SubAgentStatus struct {
    ExecutionID string
    AgentName   string
    Task        string
    Status      agent.ExecutionStatus
}

func (r *SubAgentRunner) List() []SubAgentStatus {
    // Return status of all dispatched sub-agents (running, completed, failed, cancelled)
}
```

### CancelAll + WaitAll (cleanup)

```go
func (r *SubAgentRunner) CancelAll() {
    // Cancel all running sub-agent contexts
}

func (r *SubAgentRunner) WaitAll(ctx context.Context) {
    // Wait for all sub-agent goroutines to exit
    // Called by CompositeToolExecutor.Close() (deferred in executeAgent)
}
```

## ExecutionContext Changes

The `ExecutionContext` struct gains one new optional field:

```go
// pkg/agent/context.go — addition
type ExecutionContext struct {
    // ... existing fields ...
    
    Task            string                          // Sub-agent task (set by orchestrator dispatch)
    SubAgentRunner  *orchestrator.SubAgentRunner    // nil for non-orchestrator agents
}
```

- `Task`: set when the agent is a sub-agent dispatched by an orchestrator. Triggers `buildSubAgentUserMessage` in the prompt builder.
- `SubAgentRunner`: set when the agent is an orchestrator. The `IteratingController` uses this to drain and wait for sub-agent results. `nil` for non-orchestrator agents — all drain/wait code is skipped (zero impact on existing agents).

### Result Message Format

Sub-agent results are injected into the conversation as user-role messages (external inputs the orchestrator LLM did not produce):

```go
func formatSubAgentResult(result *SubAgentResult) agent.ConversationMessage {
    var content string
    if result.Status == agent.ExecutionStatusCompleted {
        content = fmt.Sprintf(
            "[Sub-agent completed] %s (exec %s):\n%s",
            result.AgentName, result.ExecutionID, result.Result,
        )
    } else {
        content = fmt.Sprintf(
            "[Sub-agent %s] %s (exec %s): %s",
            result.Status, result.AgentName, result.ExecutionID, result.Error,
        )
    }
    return agent.ConversationMessage{Role: "user", Content: content}
}
```

The `user` role is used because these messages are external inputs to the LLM (the orchestrator did not produce them). The `[Sub-agent completed]` prefix provides a clear signal the LLM can recognize.

## Controller Approach — DECIDED

> **Decision:** Reuse `IteratingController` with a targeted modification for push-based result collection — see [questions](orchestrator-impl-questions.md), Q4.

The orchestrator reuses the existing `IteratingController` with one targeted change to support push-based sub-agent result delivery. The modification adds two behaviors to the iteration loop:

1. **Before each LLM call**: non-blocking drain of available sub-agent results
2. **At loop exit**: if sub-agents are pending, wait instead of terminating

```go
// Pseudocode — changes to IteratingController.Run()
for iteration := 0; iteration < maxIter; iteration++ {
    // NEW: drain any sub-agent results that are already available
    if runner := execCtx.SubAgentRunner; runner != nil {
        for {
            result, ok := runner.TryGetNext()  // non-blocking
            if !ok {
                break
            }
            messages = append(messages, formatSubAgentResult(result))
        }
    }

    resp := callLLMWithStreaming(ctx, messages, tools)
    // ... handle response, execute tool calls ...

    if len(resp.ToolCalls) == 0 {
        // NEW: if sub-agents are still running, wait for at least one result
        if runner := execCtx.SubAgentRunner; runner != nil && runner.HasPending() {
            result, err := runner.WaitForNext(ctx)  // blocking
            if err != nil {
                break  // context cancelled
            }
            messages = append(messages, formatSubAgentResult(result))
            continue  // give LLM another iteration with the new result
        }
        break  // truly done — no tool calls, no pending sub-agents
    }
}
```

This enables multi-phase orchestration within the existing loop:

1. **Iteration 1**: LLM dispatches agents A, B → both return "accepted"
2. **Iteration 2**: LLM dispatches C, calls an MCP tool → results returned
3. **Iteration 3**: LLM has no tool calls → pending sub-agents → **wait**
4. Agent A finishes → result injected → **continue loop**
5. **Iteration 4**: LLM sees A's result, dispatches D → "accepted"
6. Before iteration 5: agents B and C finished → results drained non-blockingly
7. **Iteration 5**: LLM sees B and C results → no more tools → wait for D
8. D finishes → result injected → LLM synthesizes → done

The `SubAgentRunner` is accessed via `ExecutionContext.SubAgentRunner` (a new optional field). For non-orchestrator agents, this field is nil and the drain/wait code is skipped — zero impact on existing agents.

Cleanup (cancel remaining sub-agents + wait for goroutines) is handled by `CompositeToolExecutor.Close()`, which is already deferred in `executeAgent()`.

## Database Schema Changes

### `AgentExecution` — add `parent_execution_id` — DECIDED

> **Decision:** Sub-agents are `AgentExecution` records with `parent_execution_id` — see [questions](orchestrator-impl-questions.md), Q7.

```go
// ent/schema/agentexecution.go — new field
field.String("parent_execution_id").
    Optional().
    Nillable().
    Comment("For orchestrator sub-agents: links to the parent orchestrator execution"),
```

- `NULL` for regular agents and orchestrators themselves
- Set to orchestrator's `execution_id` for sub-agents

### `AgentExecution` — add `task`

For sub-agents dispatched by the orchestrator, store the task text:

```go
field.Text("task").
    Optional().
    Nillable().
    Comment("Task description from orchestrator dispatch"),
```

### No new Stage for sub-agents

Sub-agent executions are created under the **same Stage** as the orchestrator. The `parent_execution_id` field distinguishes them from the orchestrator's own execution. This avoids disrupting stage indexing and the stage status aggregation logic.

### Query patterns

```go
// Get all sub-agent executions for an orchestrator run
client.AgentExecution.Query().
    Where(agentexecution.ParentExecutionID(orchestratorExecID)).
    All(ctx)
```

## Prompt Construction

### Orchestrator system prompt

The orchestrator needs a custom system prompt that includes the available agents catalog. A new method on `PromptBuilder`:

```go
func (b *PromptBuilder) BuildOrchestratorMessages(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
    agentCatalog []config.SubAgentEntry,
) []agent.ConversationMessage
```

The system prompt includes:
1. General SRE instructions (Tier 1)
2. MCP server instructions for orchestrator's own MCP servers (Tier 2)
3. Custom instructions (Tier 3)
4. **Agent catalog** — list of available sub-agents with name, description, MCP servers

Example agent catalog section in the prompt:

```
## Available Sub-Agents

You can dispatch these agents using the dispatch_agent tool.
Results are delivered automatically when each sub-agent finishes — do not poll.
Use cancel_agent to stop unnecessary work. Use list_agents to check status.

- **LogAnalyzer**: Analyzes logs from Loki to find error patterns and anomalies
  Tools: loki

- **MetricChecker**: Queries Prometheus for metric anomalies and threshold breaches
  Tools: prometheus

- **K8sInspector**: Inspects Kubernetes resources, pod status, and events
  Tools: kubernetes-server
```

### Sub-agent prompt — DECIDED

Sub-agents use `BuildFunctionCallingMessages` with a custom user message template. Instead of the investigation template (`FormatAlertSection` with `<!-- ALERT_DATA_START -->` markers, runbook, chain context), sub-agents get a clean task-focused message:

```
## Task

Find all 5xx errors for service-X in the last 30 min. Report: error count,
top error messages, time pattern.
```

The system message still includes `custom_instructions` + MCP instructions (Tier 1-3). The template is selected via `ExecutionContext.Task` — if set, the prompt builder uses `buildSubAgentUserMessage` instead of `buildInvestigationUserMessage`.

See [questions](orchestrator-impl-questions.md), Q8.

## Agent Factory Changes

Per [ADR-0001](../adr/0001-agent-type-refactor.md), the controller factory already switches on `AgentType`. The orchestrator adds one new case:

```go
func (f *Factory) CreateController(agentType AgentType, execCtx *ExecutionContext) (Controller, error) {
    switch agentType {
    case AgentTypeDefault, "":
        return NewIteratingController(), nil
    case AgentTypeSynthesis:
        return NewSynthesisController(execCtx.PromptBuilder), nil
    case AgentTypeScoring:
        return NewScoringController(execCtx.PromptBuilder), nil
    case AgentTypeOrchestrator:                    // NEW
        return NewIteratingController(), nil       // Same controller, different tools
    }
}
```

The orchestrator maps to `IteratingController` — the same multi-turn tool-calling loop as default agents. The orchestration behavior comes entirely from the `CompositeToolExecutor` (tool routing) and the `SubAgentRunner` (push-based result delivery via `ExecutionContext`). The controller itself just gains the generic drain/wait logic that activates when `SubAgentRunner` is non-nil.

## Session Executor Changes

The `executeAgent` method in `pkg/queue/executor.go` needs to detect orchestrator agents and wire up the `CompositeToolExecutor`:

```go
func (e *RealSessionExecutor) executeAgent(...) agentResult {
    resolvedConfig := ...
    
    // Standard MCP tool executor
    toolExecutor := createToolExecutor(ctx, e.mcpFactory, serverIDs, toolFilter, logger)
    
    // If orchestrator: wrap with CompositeToolExecutor + set up SubAgentRunner
    if agentConfig.Type == config.AgentTypeOrchestrator {
        deps := &orchestrator.SubAgentDeps{
            Config: e.config, AgentFactory: e.agentFactory,
            MCPFactory: e.mcpFactory, LLMClient: e.llmClient,
            EventPublisher: e.eventPublisher,
            PromptBuilder: e.promptBuilder, DBClient: e.dbClient,
        }
        guardrails := resolveOrchestratorGuardrails(e.config, agentConfig)
        registry := filterSubAgentRegistry(e.subAgentRegistry, resolvedSubAgents)
        runner := orchestrator.NewSubAgentRunner(deps, execID, registry, guardrails)
        toolExecutor = orchestrator.NewCompositeToolExecutor(toolExecutor, runner, registry)
        execCtx.SubAgentRunner = runner   // Enables push-based drain/wait in controller
    }
    
    execCtx.ToolExecutor = toolExecutor
    // ... rest stays the same
}
```

## Cancellation Cascade

When the orchestrator is cancelled (session cancel via API):

1. Session context is cancelled → orchestrator agent's `ctx` is cancelled
2. Orchestrator's current LLM call fails with `context.Canceled`
3. `SubAgentRunner` detects parent context cancellation → cancels all sub-agent contexts
4. Sub-agents receive cancelled context → their LLM calls fail → status set to `cancelled`
5. `SubAgentRunner.WaitAll()` waits for all sub-agent goroutines to exit
6. Orchestrator returns `ExecutionStatusCancelled`

Implementation: the `SubAgentRunner` spawns sub-agent goroutines with contexts derived from the orchestrator's context. When the parent context is cancelled, all child contexts are automatically cancelled.

## Dashboard Impact — DECIDED

> **Decision:** Tree view from the start — simple but hierarchical. See [questions](orchestrator-impl-questions.md), Q9.

The dashboard needs to show sub-agent executions with clear parent-child hierarchy:
1. Detect orchestrator executions (has child executions via `parent_execution_id`)
2. Display sub-agents nested under or grouped with their parent orchestrator
3. Show sub-agent status, results, and timelines
4. Stream real-time updates for sub-agent progress

The initial implementation can be a simple nested/indented view — doesn't need a full tree visualization. But the hierarchy must be visible from day one so operators can trace which sub-agent produced what data.

## Observability / WebSocket Events

Sub-agent executions publish the same events as regular executions:
- `execution.status` — status changes
- `execution.progress` — phase updates
- `timeline_event.created` / `timeline_event.completed` — timeline events
- `stream.chunk` — LLM streaming

The dashboard can subscribe to the session channel and receive events for both the orchestrator and all sub-agents. The `execution_id` in each event identifies which execution it belongs to.

New: the dashboard queries `parent_execution_id` to build the trace tree.

## Decided Questions

| # | Question | Decision | Reference |
|---|----------|----------|-----------|
| Q1 | How is an orchestrator identified in config? | Existing `type` field — `AgentTypeOrchestrator` (ADR-0001) | [Q1](orchestrator-impl-questions.md) |
| Q2 | How are orchestration tools combined with MCP tools? | CompositeToolExecutor (wrapper pattern) | [Q2](orchestrator-impl-questions.md) |
| Q3 | How are orchestration tools named? | Plain names (`dispatch_agent`, `cancel_agent`, `list_agents`) | [Q3](orchestrator-impl-questions.md) |
| Q4 | Controller approach? | Reuse IteratingController + push-based result injection | [Q4](orchestrator-impl-questions.md) |
| Q5 | Guardrail config location? | Nested `orchestrator` section + global defaults under `defaults:` | [Q5](orchestrator-impl-questions.md) |
| Q6 | `sub_agents` override hierarchy? | Full hierarchy (chain + stage + stage-agent), all optional | [Q6](orchestrator-impl-questions.md) |
| Q7 | Sub-agent DB model? | `parent_execution_id` on `AgentExecution` | [Q7](orchestrator-impl-questions.md) |
| Q8 | Task injection into sub-agent? | Custom sub-agent template (`## Task` + task text) | [Q8](orchestrator-impl-questions.md) |
| Q9 | Dashboard changes? | Tree view from the start — simple but hierarchical | [Q9](orchestrator-impl-questions.md) |
| Q10 | Dependency injection? | Dependency bundle struct (`SubAgentDeps`) | [Q10](orchestrator-impl-questions.md) |
| Q11 | Implementation phasing? | Horizontal layers (6 PRs) | [Q11](orchestrator-impl-questions.md) |

## Implementation Phases — DECIDED

> **Decision:** Horizontal layers — 6 PRs. See [questions](orchestrator-impl-questions.md), Q11.

### PR1: Config foundation
- `sub_agents` override at chain/stage/agent level (full hierarchy)
- `orchestrator` nested config section on `AgentConfig`
- `defaults.orchestrator` global defaults
- `SubAgentRegistry` built from merged agents (agents with `description`)
- Config validation: `orchestrator` section forbidden on non-orchestrator agents

### PR2: DB schema
- `parent_execution_id` on `AgentExecution` (nullable)
- `task` on `AgentExecution` (nullable)
- `UpdateStageStatus` filter: exclude sub-agents (non-null `parent_execution_id`)
- Query helpers: sub-agents by parent, trace tree

### PR3: SubAgentRunner + CompositeToolExecutor
- `SubAgentRunner` — dispatch, cancel, list, results channel (`TryGetNext`, `WaitForNext`, `HasPending`)
- `CompositeToolExecutor` — wraps MCP executor + orchestration tools
- `SubAgentDeps` dependency bundle
- `SubAgentResult` type
- Unit tests

### PR4: Controller modification + orchestrator prompt
- Push-based drain/wait logic in `IteratingController` (via `ExecutionContext.SubAgentRunner`)
- `buildSubAgentUserMessage` — custom `## Task` template
- `BuildOrchestratorMessages` — agent catalog in system prompt
- `ExecutionContext.Task` field
- Tests for prompt building and controller behavior

### PR5: Session executor wiring
- Detect orchestrator type in `executeAgent` → create runner + composite executor
- Wire `SubAgentDeps` from session executor fields
- Set `ExecutionContext.SubAgentRunner` for orchestrator agents
- End-to-end integration test

### PR6: Dashboard
- Tree view: orchestrator → sub-agents (query `parent_execution_id`)
- Sub-agent status, timelines, results
- Real-time updates via existing WebSocket events
