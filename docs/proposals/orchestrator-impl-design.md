# Orchestrator Agent — Implementation Design

**Status:** Draft — pending decisions from [orchestrator-impl-questions.md](orchestrator-impl-questions.md)
**Vision:** [orchestrator-agent-design.md](orchestrator-agent-design.md)
**Last updated:** 2026-02-19

## Overview

This document translates the [orchestrator vision](orchestrator-agent-design.md) into a concrete implementation plan against TARSy's existing codebase. It covers config model changes, new Go types, controller architecture, database schema evolution, prompt construction, and dashboard integration.

The orchestrator agent is a standard TARSy agent whose controller intercepts three additional tools (`dispatch_agent`, `get_result`, `cancel_agent`) to dynamically spawn and manage sub-agents.

## Design Principles (Implementation-Specific)

1. **Reuse the iteration loop.** The orchestrator runs the same LLM → tool calls → results → LLM cycle as `FunctionCallingController`. Orchestration tools are just another kind of tool alongside MCP tools.

2. **ToolExecutor is the seam.** The existing `ToolExecutor` interface (`ListTools` + `Execute`) is the integration point. A composite executor wraps MCP tools + orchestration tools into a single tool set. The controller doesn't need to change.

3. **Sub-agents are regular executions.** Sub-agents run through the same `ResolveAgentConfig → CreateToolExecutor → AgentFactory.CreateAgent → Execute` path as any agent. No shortcut.

4. **DB records follow the existing model.** Sub-agent runs create real `AgentExecution` records with timeline events, linked to the orchestrator via a new `parent_execution_id` column.

## Architecture

### Component Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│  Session Executor (pkg/queue/executor.go)                            │
│                                                                      │
│  executeStage → executeAgent → AgentFactory.CreateAgent              │
│       │                              │                               │
│       │                     ┌────────┴──────────┐                    │
│       │                     │  type=orchestrator │                    │
│       │                     │  OrchestratorAgent │                    │
│       │                     └────────┬──────────┘                    │
│       │                              │                               │
│       │                     ┌────────┴──────────────────┐            │
│       │                     │  OrchestratorController    │            │
│       │                     │  (reuses FC iteration loop)│            │
│       │                     └────────┬──────────────────┘            │
│       │                              │                               │
│       │                     ┌────────┴──────────────────┐            │
│       │                     │  CompositeToolExecutor     │            │
│       │                     │  ├─ MCP tools (Loki, etc.)│            │
│       │                     │  └─ Orchestration tools    │            │
│       │                     │     ├─ dispatch_agent      │            │
│       │                     │     ├─ get_result          │            │
│       │                     │     └─ cancel_agent        │            │
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
1. Orchestrator's LLM call returns tool_call: dispatch_agent(name="LogAnalyzer", task="...")
2. CompositeToolExecutor.Execute routes to SubAgentRunner.Dispatch
3. SubAgentRunner:
   a. Creates AgentExecution record (parent_execution_id = orchestrator's execution_id)
   b. Resolves agent config from registry
   c. Creates MCP tool executor for the sub-agent's servers
   d. Spawns goroutine: agentFactory.CreateAgent → agent.Execute
   e. Returns immediately: { execution_id: "sub-exec-123" }
4. Tool result goes back to LLM as: "{ execution_id: \"sub-exec-123\" }"
5. Next LLM call returns: get_result(execution_id="sub-exec-123")
6. SubAgentRunner checks goroutine status:
   - Running → returns { status: "running" }
   - Done → returns { status: "completed", result: "..." }
```

## Config Model Changes

### `AgentConfig` — add `Description` and `Type`

> **Open question:** How to identify an orchestrator in config — see [questions](orchestrator-impl-questions.md), Q1.

```go
// pkg/config/agent.go
type AgentConfig struct {
    Description        string            `yaml:"description,omitempty"`
    Type               AgentType         `yaml:"type,omitempty"`
    MCPServers         []string          `yaml:"mcp_servers"`
    CustomInstructions string            `yaml:"custom_instructions"`
    IterationStrategy  IterationStrategy `yaml:"iteration_strategy,omitempty"`
    MaxIterations      *int              `yaml:"max_iterations,omitempty"`
}
```

- `Description` — required for orchestrator visibility (agents without it are excluded from the sub-agent registry). Also useful for documentation on non-orchestrator agents.
- `Type` — `""` (default, regular agent) or `"orchestrator"`. Determines whether the agent gets orchestration tools.

### `BuiltinAgentConfig` → `AgentConfig` merge — preserve `Description`

Currently `mergeAgents` drops `Description` from built-ins. Fix: add `Description` to `AgentConfig` and carry it through the merge.

### Orchestrator-specific config

> **Open question:** Where do orchestrator guardrails live — see [questions](orchestrator-impl-questions.md), Q5.

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    description: "Dynamic SRE investigation orchestrator"
    custom_instructions: |
      You investigate alerts by dispatching specialized sub-agents...
    max_concurrent_agents: 5
    agent_timeout: 300s
```

### `sub_agents` override

> **Open question:** Where does `sub_agents` live in the config hierarchy — see [questions](orchestrator-impl-questions.md), Q6.

```yaml
agent_chains:
  focused-investigation:
    sub_agents: [LogAnalyzer, MetricChecker]
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
```

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

Three tools registered via `CompositeToolExecutor.ListTools`:

```go
var orchestrationTools = []agent.ToolDefinition{
    {
        Name:        "dispatch_agent",
        Description: "Dispatch a sub-agent to execute a task asynchronously. Returns an execution_id for tracking.",
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
        Name:        "get_result",
        Description: "Get the status and result of a dispatched sub-agent.",
        ParametersSchema: `{
            "type": "object",
            "properties": {
                "execution_id": {"type": "string", "description": "Execution ID from dispatch_agent"}
            },
            "required": ["execution_id"]
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
}
```

### Tool Naming

> **Open question:** How orchestration tools are named and routed — see [questions](orchestrator-impl-questions.md), Q3.

MCP tools use `server.tool` naming (e.g., `kubernetes-server.get_pod`). Orchestration tools need a distinct namespace to avoid collisions with MCP tool names.

## CompositeToolExecutor

> **Open question:** How the composite executor combines MCP and orchestration tools — see [questions](orchestrator-impl-questions.md), Q2.

```go
// pkg/agent/orchestrator/tool_executor.go
type CompositeToolExecutor struct {
    mcpExecutor agent.ToolExecutor          // Existing MCP tools (may be nil/stub)
    runner      *SubAgentRunner             // Handles dispatch/get_result/cancel
    registry    *config.SubAgentRegistry    // Available agents for dispatch
}

func (c *CompositeToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
    // MCP tools + orchestration tools
}

func (c *CompositeToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
    if isOrchestrationTool(call.Name) {
        return c.executeOrchestrationTool(ctx, call)
    }
    return c.mcpExecutor.Execute(ctx, call)
}
```

## SubAgentRunner

Manages the lifecycle of sub-agent goroutines within an orchestrator execution.

```go
// pkg/agent/orchestrator/runner.go
type SubAgentRunner struct {
    mu          sync.Mutex
    executions  map[string]*subAgentExecution   // execution_id → state
    
    // Dependencies (injected at creation)
    parentExecCtx  *agent.ExecutionContext     // Orchestrator's context
    cfg            *config.Config
    agentFactory   *agent.AgentFactory
    mcpFactory     *mcp.ClientFactory
    promptBuilder  agent.PromptBuilder
    // ... other dependencies from RealSessionExecutor
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
```

### Dispatch

```go
func (r *SubAgentRunner) Dispatch(ctx context.Context, name, task string) (string, error) {
    // 1. Validate agent exists in registry
    // 2. Check max_concurrent_agents guardrail
    // 3. Create AgentExecution DB record (parent_execution_id = orchestrator's ID)
    // 4. Resolve agent config
    // 5. Create MCP tool executor for sub-agent
    // 6. Build ExecutionContext for sub-agent
    // 7. Spawn goroutine
    // 8. Return execution_id immediately
}
```

### GetResult

```go
func (r *SubAgentRunner) GetResult(executionID string) (status string, result string, err string) {
    // Check in-memory registry for the execution
    // Return current status + result if completed
}
```

### Cancel

```go
func (r *SubAgentRunner) Cancel(executionID string) string {
    // Call cancel() on the sub-agent's context
    // Return status
}
```

### Cleanup

```go
func (r *SubAgentRunner) WaitAll(ctx context.Context) {
    // Wait for all sub-agents to finish (called when orchestrator completes)
    // Cancel any remaining sub-agents if orchestrator is cancelled
}
```

## Controller Approach

> **Open question:** New controller or reuse FunctionCallingController — see [questions](orchestrator-impl-questions.md), Q4.

The orchestrator reuses the existing `FunctionCallingController` iteration loop. The orchestration behavior comes entirely from the `CompositeToolExecutor` — the controller doesn't know it's running an orchestrator. This is the key insight: the controller just sees tools and calls them.

The only controller-level change needed: the orchestrator's controller must call `SubAgentRunner.WaitAll()` when it finishes to clean up any still-running sub-agents. This could be handled by the agent's `Execute()` method (post-controller cleanup) rather than the controller itself.

## Database Schema Changes

### `AgentExecution` — add `parent_execution_id`

> **Open question:** How sub-agent executions are modeled in the DB — see [questions](orchestrator-impl-questions.md), Q7.

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

You can dispatch these agents using the dispatch_agent tool:

- **LogAnalyzer**: Analyzes logs from Loki to find error patterns and anomalies
  Tools: loki

- **MetricChecker**: Queries Prometheus for metric anomalies and threshold breaches
  Tools: prometheus

- **K8sInspector**: Inspects Kubernetes resources, pod status, and events
  Tools: kubernetes-server
```

### Sub-agent prompt

Sub-agents use the standard `BuildFunctionCallingMessages` with one difference: the `task` from the orchestrator replaces the alert data as the primary user message content.

> **Open question:** How the task is injected into the sub-agent prompt — see [questions](orchestrator-impl-questions.md), Q8.

## Agent Factory Changes

```go
// pkg/agent/controller/factory.go
func (f *Factory) CreateController(strategy config.IterationStrategy, execCtx *agent.ExecutionContext) (agent.Controller, error) {
    switch strategy {
    case config.IterationStrategyNativeThinking, config.IterationStrategyLangChain:
        return NewFunctionCallingController(), nil
    case config.IterationStrategySynthesis, config.IterationStrategySynthesisNativeThinking:
        return NewSynthesisController(), nil
    default:
        return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
    }
}
```

The orchestrator uses the **same** iteration strategy as regular agents (`native-thinking` or `langchain`). The `type: orchestrator` config triggers the session executor to wire up `CompositeToolExecutor` instead of the plain MCP executor. The controller factory doesn't change.

> **Open question:** Whether the controller factory needs changes — see [questions](orchestrator-impl-questions.md), Q4.

## Session Executor Changes

The `executeAgent` method in `pkg/queue/executor.go` needs to detect orchestrator agents and wire up the `CompositeToolExecutor`:

```go
func (e *RealSessionExecutor) executeAgent(...) agentResult {
    resolvedConfig := ...
    
    // Standard MCP tool executor
    toolExecutor := createToolExecutor(ctx, e.mcpFactory, serverIDs, toolFilter, logger)
    
    // If orchestrator: wrap with CompositeToolExecutor
    if agentConfig.Type == config.AgentTypeOrchestrator {
        runner := orchestrator.NewSubAgentRunner(...)
        toolExecutor = orchestrator.NewCompositeToolExecutor(toolExecutor, runner, subAgentRegistry)
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

## Dashboard Impact

> **Open question:** Dashboard changes required — see [questions](orchestrator-impl-questions.md), Q9.

The dashboard needs to:
1. Detect orchestrator executions (has child executions)
2. Display the trace tree: orchestrator → sub-agents
3. Show sub-agent status, results, and timelines as nested views
4. Stream real-time updates for sub-agent progress

This is a significant frontend change. The existing timeline view shows a flat list of events per execution. The orchestrator view needs to show a tree.

## Observability / WebSocket Events

Sub-agent executions publish the same events as regular executions:
- `execution.status` — status changes
- `execution.progress` — phase updates
- `timeline_event.created` / `timeline_event.completed` — timeline events
- `stream.chunk` — LLM streaming

The dashboard can subscribe to the session channel and receive events for both the orchestrator and all sub-agents. The `execution_id` in each event identifies which execution it belongs to.

New: the dashboard queries `parent_execution_id` to build the trace tree.

## Open Questions

| # | Question | Reference |
|---|----------|-----------|
| Q1 | How is an orchestrator identified in config? | [Q1](orchestrator-impl-questions.md) |
| Q2 | How are orchestration tools combined with MCP tools? | [Q2](orchestrator-impl-questions.md) |
| Q3 | How are orchestration tools named to avoid MCP collisions? | [Q3](orchestrator-impl-questions.md) |
| Q4 | Does the orchestrator need a new controller or reuse FunctionCallingController? | [Q4](orchestrator-impl-questions.md) |
| Q5 | Where do orchestrator guardrails (max concurrent, timeout) live in config? | [Q5](orchestrator-impl-questions.md) |
| Q6 | Where does `sub_agents` override live in the config hierarchy? | [Q6](orchestrator-impl-questions.md) |
| Q7 | How are sub-agent executions modeled in the DB? | [Q7](orchestrator-impl-questions.md) |
| Q8 | How is the orchestrator's task injected into the sub-agent prompt? | [Q8](orchestrator-impl-questions.md) |
| Q9 | What dashboard changes are needed and how to phase them? | [Q9](orchestrator-impl-questions.md) |
| Q10 | How are SubAgentRunner dependencies injected? | [Q10](orchestrator-impl-questions.md) |
| Q11 | What is the implementation phasing? | [Q11](orchestrator-impl-questions.md) |

## Implementation Phases (Draft)

> **Open question:** Implementation phasing — see [questions](orchestrator-impl-questions.md), Q11.

### Phase 1: Config + Registry (foundation)
- Add `Description` and `Type` fields to `AgentConfig`
- Fix `mergeAgents` to preserve `Description` from built-ins
- Build `SubAgentRegistry` from merged agents
- Add `sub_agents` override to chain config
- Config validation: orchestrator must have iteration_strategy, cannot be used in synthesis, etc.

### Phase 2: Core Orchestration (backend)
- `CompositeToolExecutor` — combine MCP + orchestration tools
- `SubAgentRunner` — spawn, track, and cancel sub-agents
- Orchestrator prompt building (agent catalog in system prompt)
- Sub-agent prompt building (task injection)
- DB schema: `parent_execution_id` and `task` on `AgentExecution`
- Session executor wiring: detect orchestrator → create composite executor

### Phase 3: Observability (backend + frontend)
- Sub-agent timeline events with proper `execution_id` and `parent_execution_id`
- Dashboard: orchestrator trace tree view
- Dashboard: sub-agent status tracking
- API: query sub-agent executions by parent

### Phase 4: Guardrails + Polish
- Max concurrent sub-agents enforcement
- Per sub-agent timeout
- Total orchestrator budget
- Cancellation cascade testing
- Error handling edge cases
