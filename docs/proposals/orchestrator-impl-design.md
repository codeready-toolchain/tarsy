# Orchestrator Agent — Implementation Design

**Status:** All questions decided — ready for implementation
**Vision:** [orchestrator-agent-design.md](orchestrator-agent-design.md)
**Last updated:** 2026-02-25

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
│                                      │                               │
│                             ┌────────┴───────────────────┐           │
│                             │  Agent                     │           │
│                             │  (type=orchestrator)       │           │
│                             └────────┬───────────────────┘           │
│                                      │                               │
│                             ┌────────┴───────────────────┐           │
│                             │  IteratingController       │           │
│                             │  (+ push-based drain/wait) │           │
│                             └────────┬───────────────────┘           │
│                                      │                               │
│                             ┌────────┴───────────────────┐           │
│                             │  CompositeToolExecutor     │           │
│                             │  ├─ MCP tools (Loki, etc.) │           │
│                             │  └─ Orchestration tools    │           │
│                             │     ├─ dispatch_agent      │           │
│                             │     ├─ cancel_agent        │           │
│                             │     └─ list_agents         │           │
│                             └────────┬───────────────────┘           │
│                                      │                               │
│                             ┌────────┴───────────────────┐           │
│                             │  SubAgentRunner            │           │
│                             │  (spawns/tracks sub-agents)│           │
│                             │                            │           │
│                             │  goroutine per sub-agent:  │           │
│                             │  ┌──────────────────────┐  │           │
│                             │  │ ResolveAgentConfig   │  │           │
│                             │  │ CreateToolExecutor   │  │           │
│                             │  │ AgentFactory.Create  │  │           │
│                             │  │ agent.Execute()      │  │           │
│                             │  └──────────────────────┘  │           │
│                             └────────────────────────────┘           │
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
7. Controller continues iteration → LLM sees result, dispatches more or produces final answer
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
    # sub_agents: [...]                         # Chain-level (optional, broadest)
    stages:
      - name: investigate
        # sub_agents: [...]                     # Stage-level (optional)
        agents:
          - name: MyOrchestrator
            sub_agents: [LogAnalyzer, MetricChecker]  # Stage-agent level (recommended)
```

Stage-agent level is recommended — it makes the orchestrator-to-sub-agent relationship explicit. Chain and stage levels are useful when multiple orchestrators share the same sub-agent pool.

If omitted at all levels, the orchestrator sees the full global registry (all agents with `description`).

## New Built-In Agents

Three new built-in agents ship with the orchestrator feature. All have `description` set, making them orchestrator-visible by default. No MCP servers — they use either Gemini native tools or pure LLM reasoning.

### WebResearcher

Uses Gemini's native `google_search` and `url_context` tools. Search and URL analysis are naturally complementary — the agent searches for something, then reads what it found.

```yaml
WebResearcher:
  type: default
  llm_backend: native-gemini
  description: "Searches the web and analyzes URLs for real-time information"
  native_tools:
    google_search: true
    url_context: true
    code_execution: false
  custom_instructions: |
    You research topics using web search and URL analysis.
    Report findings with sources. Be thorough but concise.
```

**Use cases:** real-time incident context ("what version was released?"), CVE lookups, documentation lookups, external service status checks.

### CodeExecutor

Uses Gemini's native `code_execution` tool for Python code execution in a sandbox. Fundamentally different from research — computation, data analysis, math.

```yaml
CodeExecutor:
  type: default
  llm_backend: native-gemini
  description: "Executes Python code for computation, data analysis, and calculations"
  native_tools:
    google_search: false
    code_execution: true
    url_context: false
  custom_instructions: |
    You solve computational tasks by writing and executing Python code.
    Show your work. Report results clearly.
```

**Use cases:** log pattern analysis (regex, counting), metric calculations, data transformations, statistical analysis.

### GeneralWorker

Pure LLM reasoning — no tools. Handles tasks that don't need external data access: summarization, comparison, drafting, text analysis. Operators can add MCP tools via config override if they want a more capable worker.

```yaml
GeneralWorker:
  type: default
  description: "General-purpose agent for analysis, summarization, reasoning, and other tasks"
  custom_instructions: |
    You are GeneralWorker, a general-purpose agent.
    Complete the assigned task thoroughly and concisely.
```

**Use cases:** synthesize sub-agent findings, draft incident summaries, compare multiple data points, analyze error messages.

### Orchestrator

Built-in orchestrator agent — users can reference it by name in chains without defining it themselves. Uses `AgentTypeOrchestrator` which triggers orchestrator wiring in `executeAgent`. No MCP servers (delegates tool usage to sub-agents). No LLM backend or native tools (inherits from defaults). Auto-excluded from the `SubAgentRegistry` by the `agent.Type == AgentTypeOrchestrator` filter.

```yaml
Orchestrator:
  type: orchestrator
  description: "Dynamic investigation orchestrator that dispatches specialized sub-agents"
  custom_instructions: |
    You are Orchestrator, a dynamic investigation orchestrator.
    You analyze alerts by dispatching specialized sub-agents in parallel,
    collecting their results, and producing a comprehensive root cause analysis.

    Strategy:
    1. Analyze the alert to identify what needs investigation
    2. Dispatch relevant sub-agents in parallel for independent investigation tracks
    3. As results arrive, assess whether follow-up investigation is needed
    4. When all relevant data is collected, produce a final root cause analysis

    Principles:
    - Dispatch agents for independent tasks in parallel
    - Cancel agents whose work is no longer needed based on earlier findings
    - Be specific in task descriptions — include relevant context from the alert
    - Synthesize all findings into a clear root cause analysis
```

The `custom_instructions` only contains strategy and principles. Universal operational mechanics (result delivery, agent catalog) are system-injected for all orchestrators — see [Orchestrator system prompt](#orchestrator-system-prompt) below.

### Built-in agent summary

| Agent | Type | Native Tools | MCP | Purpose |
|-------|------|-------------|-----|---------|
| Orchestrator | orchestrator | none | none | Dispatches and coordinates sub-agents |
| WebResearcher | default | google_search, url_context | none | Web research and URL analysis |
| CodeExecutor | default | code_execution | none | Python computation and analysis |
| GeneralWorker | default | none | none | Reasoning, summarization, drafting |

These complement existing built-in agents (KubernetesAgent, etc.) which already have descriptions and are orchestrator-visible.

### Prerequisite: Extend `BuiltinAgentConfig`

The current `BuiltinAgentConfig` struct only has `Description`, `Type`, `MCPServers`, and `CustomInstructions`. WebResearcher and CodeExecutor need `LLMBackend` and `NativeTools` — fields that don't exist on the built-in config struct today.

```go
// pkg/config/builtin.go — extension
type BuiltinAgentConfig struct {
    Description        string
    Type               AgentType
    MCPServers         []string
    CustomInstructions string
    LLMBackend         LLMBackend                  // NEW: for agents that need a specific backend
    NativeTools        map[GoogleNativeTool]bool    // NEW: per-agent native tool overrides
}
```

The `mergeAgents` function in `pkg/config/merge.go` must also carry these new fields when converting `BuiltinAgentConfig` → `AgentConfig`. This change is part of PR0/PR1.

### Prerequisite: `native_tools` override on `AgentConfig`

Currently, native tools are configured only on the LLM provider level (`LLMProviderConfig.NativeTools`). The orchestrator built-in agents need per-agent control. A new `native_tools` field on `AgentConfig` overrides the provider's defaults:

```go
// pkg/config/agent.go — addition
type AgentConfig struct {
    // ... existing fields ...
    NativeTools map[GoogleNativeTool]bool `yaml:"native_tools,omitempty"`
}
```

**Merge semantics:** Agent-level `native_tools` overrides the provider's `native_tools` per-key. Missing keys fall back to the provider's setting. This follows TARSy's existing override philosophy.

```go
// Resolution at execution time:
func resolveNativeTools(provider *LLMProviderConfig, agent *AgentConfig) map[GoogleNativeTool]bool {
    resolved := make(map[GoogleNativeTool]bool)
    for k, v := range provider.NativeTools {
        resolved[k] = v
    }
    for k, v := range agent.NativeTools {
        resolved[k] = v  // agent overrides provider
    }
    return resolved
}
```

**Three-level resolution chain:** The full native tools resolution at execution time is: LLM provider defaults → agent-level `native_tools` (this PR) → per-alert `NativeToolsOverride` (existing, from `resolveMCPSelection`). The per-alert override remains highest priority, applied after agent-level resolution.

This is a small, independent change that can land as a separate PR before the orchestrator work. It's useful on its own — any agent can override native tools without needing a dedicated LLM provider.

## Sub-Agent Registry

A new `SubAgentRegistry` type built at config load time from the merged agent registry:

```go
// pkg/config/sub_agent_registry.go
type SubAgentEntry struct {
    Name        string
    Description string
    MCPServers  []string
    NativeTools []string   // Gemini native tools (google_search, url_context, code_execution)
}

type SubAgentRegistry struct {
    entries []SubAgentEntry
}

func BuildSubAgentRegistry(agents map[string]*AgentConfig) *SubAgentRegistry {
    // Include agents with non-empty Description (built-in descriptions are already
    // in the merged AgentConfig map from AgentRegistry.GetAll())
    // Exclude orchestrator agents themselves (type == orchestrator)
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

**Observability:** When orchestration tool calls are recorded as `MCPInteraction` records, they use `server_name: "orchestrator"` (via `OrchestrationServerName` constant) instead of an empty string. This lets dashboards distinguish orchestration tool calls from real MCP server calls by checking `server_name == "orchestrator"`.

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
    // Cancel any still-running sub-agents and wait for them to finish.
    //
    // Uses context.Background() intentionally: Close() is called from a defer in
    // executeAgent, where the parent context may already be cancelled (e.g. session
    // cancellation). Cleanup must proceed regardless — the 30s timeout is the real
    // upper bound, not the parent context's lifetime.
    //
    // Deadlock safety: goroutine sends to resultsCh are ctx-aware (select with
    // ctx.Done), so cancelled goroutines can always exit even if the channel is full.
    c.runner.CancelAll()
    waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    c.runner.WaitAll(waitCtx)
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
    resultsCh   chan *SubAgentResult             // completed results (buffered, cap = MaxConcurrentAgents)
    pending     int32                            // atomic count of running sub-agents
    
    parentCtx          context.Context               // Session-level context (long-lived) — sub-agent contexts derive from this
    deps               *SubAgentDeps                // Dependency bundle (Q10)
    parentExecID       string                       // Orchestrator's execution_id
    sessionID          string                       // Orchestrator's session_id (for sub-agent DB records)
    stageID            string                       // Orchestrator's stage_id (for sub-agent DB records)
    nextSubAgentIndex  int32                        // Atomic counter for sub-agent agent_index (starts at 1)
    registry           *config.SubAgentRegistry     // Available agents for dispatch
    guardrails         *OrchestratorGuardrails      // Resolved from defaults + per-agent config (Q5)
}

// SubAgentDeps bundles dependencies extracted from the session executor.
// Uses service interfaces instead of raw *ent.Client to follow existing
// data access patterns (confirmed during PR3 planning).
type SubAgentDeps struct {
    Config         *config.Config
    Chain          *config.ChainConfig        // For ResolveAgentConfig
    AgentFactory   *agent.AgentFactory
    MCPFactory     *mcp.ClientFactory
    LLMClient      agent.LLMClient
    EventPublisher agent.EventPublisher
    PromptBuilder  agent.PromptBuilder        // Interface (avoids agent↔prompt import cycle)

    StageService       *services.StageService
    TimelineService    *services.TimelineService
    MessageService     *services.MessageService
    InteractionService *services.InteractionService

    // Orchestrator's session context, passed through to sub-agent ExecutionContexts.
    AlertData      string
    AlertType      string
    RunbookContent string
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
    //    (counts executions where status == active)
    // 3. Create AgentExecution DB record:
    //    - stage_id = orchestrator's stage ID (from r.stageID)
    //    - session_id = orchestrator's session ID (from r.sessionID)
    //    - parent_execution_id = orchestrator's execution ID (from r.parentExecID)
    //    - task = task text
    //    - status = "active"
    //    - agent_index = next sub-agent index (from r.nextSubAgentIndex, atomic)
    // 4. Create task_assigned timeline event
    // 5. Resolve agent config (same path as executeAgent)
    // 6. Create MCP tool executor for sub-agent's own MCP servers
    // 7. Build ExecutionContext with SubAgent field set (triggers sub-agent prompt template)
    // 8. Increment pending counter (atomic) — BEFORE spawning goroutine to avoid race
    // 9. Spawn goroutine:
    //    - Derive context with agent_timeout from r.parentCtx (session-level, NOT
    //      the per-iteration ctx passed to Dispatch — that ctx is short-lived and
    //      would cancel the sub-agent when the orchestrator's current iteration ends)
    //    - agentFactory.CreateAgent → agent.Execute
    //    - On completion: send SubAgentResult to resultsCh (ctx-aware, see below)
    //    - On timeout/cancel: update DB status → "cancelled" / "failed"
    //    - Signal done channel (always, even on send failure)
    //
    //    The send to resultsCh MUST be non-blocking during shutdown to avoid
    //    deadlock during Close(). Use a dedicated closeCh (not ctx.Done()):
    //      select {
    //      case r.resultsCh <- result:
    //      case <-r.closeCh:
    //          // CancelAll closed closeCh — orchestrator is shutting down.
    //      }
    //    Using closeCh (closed only by CancelAll) instead of ctx.Done() ensures
    //    that individual sub-agent cancellations still deliver their result to
    //    the orchestrator. Only bulk shutdown drops results.
    //
    // 10. Return execution_id immediately
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
    // Return status of all dispatched sub-agents (active, completed, failed, cancelled)
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

The `ExecutionContext` struct gains three new optional fields:

```go
// pkg/agent/context.go — additions

// SubAgentResultCollector provides push-based delivery of completed sub-agent
// results to the controller. Defined as interface in pkg/agent to avoid a
// circular import between pkg/agent and pkg/agent/orchestrator.
// Implemented by orchestrator.ResultCollector (adapter over SubAgentRunner).
type SubAgentResultCollector interface {
    TryDrainResult() (ConversationMessage, bool)
    WaitForResult(ctx context.Context) (ConversationMessage, error)
    HasPending() bool
}

// SubAgentContext carries sub-agent-specific data for controllers and prompt builders.
// Follows the same pattern as ChatContext.
type SubAgentContext struct {
    Task         string  // Task assigned by orchestrator
    ParentExecID string  // Parent orchestrator's execution ID
}

type ExecutionContext struct {
    // ... existing fields ...
    
    SubAgent          *SubAgentContext          // nil for non-sub-agents (same pattern as ChatContext)
    SubAgentCollector SubAgentResultCollector   // nil for non-orchestrator agents
    SubAgentCatalog   []config.SubAgentEntry   // Available sub-agents (set for orchestrators)
}
```

The `SubAgentRunner` (PR3) is NOT referenced directly from `ExecutionContext` — that would create an `agent↔orchestrator` import cycle. Instead, the controller interacts through the `SubAgentResultCollector` interface. An `orchestrator.ResultCollector` adapter wraps the runner and converts raw `SubAgentResult` values into `ConversationMessage` via `FormatSubAgentResult`.

- `SubAgent`: set when the agent is a sub-agent dispatched by an orchestrator. Detected via `execCtx.SubAgent != nil` — same pattern as `execCtx.ChatContext != nil`. Triggers `buildSubAgentMessages` in the prompt builder.
- `SubAgentCollector`: set when the agent is an orchestrator. The `IteratingController` uses this to drain and wait for sub-agent results. `nil` for non-orchestrator agents — all drain/wait code is skipped (zero impact on existing agents).
- `SubAgentCatalog`: set when the agent is an orchestrator. Used by `BuildFunctionCallingMessages` to include the agent catalog in the system prompt. Avoids coupling the prompt builder to the orchestrator package.

### Result Message Format

Sub-agent results are injected into the conversation as user-role messages (external inputs the orchestrator LLM did not produce):

```go
func FormatSubAgentResult(result *SubAgentResult) agent.ConversationMessage {
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
    if collector := execCtx.SubAgentCollector; collector != nil {
        for {
            msg, ok := collector.TryDrainResult()  // non-blocking
            if !ok {
                break
            }
            messages = append(messages, msg)
        }
    }

    resp := callLLMWithStreaming(ctx, messages, tools)
    // ... handle response, execute tool calls ...

    if len(resp.ToolCalls) == 0 {
        // NEW: if sub-agents are still running, wait for at least one result
        if collector := execCtx.SubAgentCollector; collector != nil && collector.HasPending() {
            msg, err := collector.WaitForResult(ctx)  // blocking
            if err != nil {
                break  // context cancelled
            }
            messages = append(messages, msg)
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
8. D finishes → result injected → LLM produces final response → done

The `SubAgentResultCollector` is accessed via `ExecutionContext.SubAgentCollector` (a new optional field). For non-orchestrator agents, this field is nil and the drain/wait code is skipped — zero impact on existing agents.

Cleanup (cancel remaining sub-agents + wait for goroutines) is handled by `CompositeToolExecutor.Close()`, which is already deferred in `executeAgent()`.

### Orchestrator final response vs. stage synthesis

TARSy has two distinct mechanisms for producing combined output — they should not be confused:

**Stage-level synthesis (existing):** When a stage has multiple parallel agents, a `SynthesisAgent` (type=synthesis) automatically runs after all agents complete to merge their outputs. This is a separate agent execution with its own `SingleShotController` (synthesis-configured), dedicated prompt, and `AgentExecution` record. Driven by `executeStage`.

**Orchestrator final response:** The orchestrator is typically a **single agent in a stage**. It produces its final output within the same execution — no separate agent, no separate controller. This is just the LLM's last response when it has no more work to do.

```
Current parallel pattern:              Orchestrator pattern:

Stage:                                 Stage:
├─ Agent A (parallel) ──┐              └─ Orchestrator (single) ──────────┐
├─ Agent B (parallel) ──┼─ SynthesisAgent    ├─ dispatch LogAnalyzer      │
└─ Agent C (parallel) ──┘  (separate exec)   ├─ dispatch MetricChecker    │ same
                                             ├─ collect results           │ execution
                                             └─ final response → output ──┘
```

**Implementation:** The orchestrator's final response requires no special code. It uses the existing `IteratingController` completion path — the same path every iterating agent uses when it finishes:

1. All sub-agents have finished → results are in the conversation
2. LLM responds with text and **no tool calls**
3. `SubAgentRunner.HasPending()` returns false (no pending sub-agents)
4. Controller hits the `break` — exits the loop
5. The LLM's last text response becomes `FinalAnalysis` (same as any iterating agent)
6. A `final_analysis` timeline event is created (existing code)

```go
// Existing code in the controller — no change needed:
if len(resp.ToolCalls) == 0 {
    // ... (orchestrator drain/wait logic — skipped when HasPending() is false) ...
    createTimelineEvent(ctx, execCtx,
        timelineevent.EventTypeFinalAnalysis, resp.Text, nil, &eventSeq)
    return &agent.ExecutionResult{
        Status:        agent.ExecutionStatusCompleted,
        FinalAnalysis: resp.Text,  // ← the orchestrator's final response
        TokensUsed:    totalUsage,
    }, nil
}
```

There is no separate "synthesis" step. The orchestrator's `custom_instructions` guide what the LLM produces in its final response (e.g., "produce a root cause analysis"). The LLM has the full conversation (all dispatches, all sub-agent results) and naturally produces its final answer when it has nothing left to do.

**Forced conclusion** also works unchanged: if the orchestrator hits `max_iterations` before producing its final answer, the existing forced-conclusion path sends a conclusion prompt with no tools, forcing the LLM to produce a final response. This becomes the `FinalAnalysis` even if some sub-agents are still running (they're cancelled by `CompositeToolExecutor.Close()`).

Edge case: other agents (including multiple orchestrators) *can* be placed in the same stage, in which case stage-level synthesis would run after everything completes. This works because sub-agent `agent_index` values are scoped to their parent execution (see `agent_index` constraint below), so no collisions. Still unusual — the orchestrator is designed to be the sole agent handling the dynamic workflow.

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

The `task` field serves two purposes:
1. **Dashboard tree view** — shown in the sub-agent's row/card so operators can see what each sub-agent was asked to do without drilling in
2. **Timeline event** — a `task_assigned` timeline event is created at the start of each sub-agent execution, making the task visible in the detailed timeline:

```go
// In SubAgentRunner.Dispatch, after creating the AgentExecution:
createTimelineEvent(ctx, subExecCtx, timelineevent.EventTypeTaskAssigned, task, nil, &eventSeq)
```

This gives operators immediate visibility into what each sub-agent was asked to do — both at a glance (tree view) and in detail (timeline).

### No new Stage for sub-agents

Sub-agent executions are created under the **same Stage** as the orchestrator. The `parent_execution_id` field distinguishes them from the orchestrator's own execution. This avoids creating synthetic stages and disrupting stage indexing.

**`agent_index` — parent-execution scoped for sub-agents:** The existing `UNIQUE(stage_id, agent_index)` constraint is replaced in PR2 with two partial indexes (PostgreSQL):

```sql
-- Top-level agents: unique within stage (same semantics as before)
UNIQUE(stage_id, agent_index) WHERE parent_execution_id IS NULL

-- Sub-agents: unique within parent orchestrator
UNIQUE(parent_execution_id, agent_index) WHERE parent_execution_id IS NOT NULL
```

Top-level agents keep indices 1..N (assigned by `executeStage`). Sub-agents get their own index space starting at 1, scoped to their parent orchestrator (`SubAgentRunner.nextSubAgentIndex` starts at 1, incremented atomically). Multiple orchestrators in the same stage work naturally — each has an independent index space.

**`UpdateStageStatus` filter:** The `UpdateStageStatus` function aggregates status from ALL `AgentExecution` records in a stage. Without filtering, sub-agent failures would incorrectly mark the stage as failed even if the orchestrator completed successfully. PR2 adds a filter: `WHERE parent_execution_id IS NULL` — only top-level executions participate in stage status aggregation.

### Query patterns

```go
// Get all sub-agent executions for an orchestrator run
client.AgentExecution.Query().
    Where(agentexecution.ParentExecutionID(orchestratorExecID)).
    All(ctx)
```

## Prompt Construction

### Orchestrator system prompt

The orchestrator prompt is handled by the existing `BuildFunctionCallingMessages` method — no new method on the `PromptBuilder` interface. The method already receives `execCtx` which contains `Config.Type`. When the type is `orchestrator`, it includes the agent catalog in the system prompt (from `execCtx.SubAgentCatalog` — see [ExecutionContext Changes](#executioncontext-changes) above).

Inside `BuildFunctionCallingMessages`, the orchestrator branch:

```go
func (b *PromptBuilder) BuildFunctionCallingMessages(execCtx *agent.ExecutionContext, prevStageContext string) []agent.ConversationMessage {
    // Existing: detect chat mode via execCtx.ChatContext
    // NEW: detect orchestrator via execCtx.Config.Type
    if execCtx.Config.Type == config.AgentTypeOrchestrator {
        return b.buildOrchestratorMessages(execCtx, prevStageContext)
    }
    // NEW: detect sub-agent via execCtx.SubAgent (same pattern as ChatContext)
    if execCtx.SubAgent != nil {
        return b.buildSubAgentMessages(execCtx)
    }
    // ... existing investigation / chat paths ...
}
```

This avoids adding new methods to the `PromptBuilder` interface (no test mock changes) and follows the same detection pattern already used for chat mode (`execCtx.ChatContext != nil`).

The system prompt includes:
1. General SRE instructions (Tier 1)
2. MCP server instructions for orchestrator's own MCP servers (Tier 2)
3. Custom instructions (Tier 3)
4. **Agent catalog** — list of available sub-agents with name, description, tools (from `execCtx.SubAgentCatalog`)

Example agent catalog section in the prompt:

```
## Available Sub-Agents

You can dispatch these agents using the dispatch_agent tool.
Results are delivered automatically when each sub-agent finishes — do not poll.
Use cancel_agent to stop unnecessary work. Use list_agents to check status.

- **LogAnalyzer**: Analyzes logs from Loki to find error patterns and anomalies
  MCP tools: loki

- **MetricChecker**: Queries Prometheus for metric anomalies and threshold breaches
  MCP tools: prometheus

- **K8sInspector**: Inspects Kubernetes resources, pod status, and events
  MCP tools: kubernetes-server

- **WebResearcher**: Searches the web and analyzes URLs for real-time information
  Native tools: google_search, url_context

- **CodeExecutor**: Executes Python code for computation, data analysis, and calculations
  Native tools: code_execution

- **GeneralWorker**: General-purpose agent for analysis, summarization, reasoning, and other tasks
  Tools: none (pure reasoning)
```

### Sub-agent prompt — DECIDED

Sub-agents are detected via `ExecutionContext.SubAgent` (non-nil, same pattern as `ChatContext`). When set, `BuildFunctionCallingMessages` dispatches to `buildSubAgentMessages` which uses a clean task-focused user message instead of the investigation template (`FormatAlertSection` with `<!-- ALERT_DATA_START -->` markers, runbook, chain context):

```
## Task

Find all 5xx errors for service-X in the last 30 min. Report: error count,
top error messages, time pattern.
```

The system message includes Tier 1-3 instructions (`custom_instructions` + MCP instructions) followed by a `subAgentFocus` block that is auto-injected for **all** sub-agents (built-in and custom):

```
You are a sub-agent dispatched by an orchestrator for a specific task.

Rules:
- Focus exclusively on your assigned task — do not investigate unrelated areas.
- Your final response is automatically reported back to the orchestrator. Do not address the user directly.
- Be concise: state what you found, key evidence, and any relevant details the orchestrator should know.
- If you have tools available, use them to complete your task. If not, use reasoning alone.
```

This replaces the generic `taskFocus` ("Focus on investigation and providing recommendations for human operators to execute.") which was misleading for sub-agents — they report to the orchestrator, not to human operators. The task text comes from `execCtx.SubAgent.Task`.

See [questions](orchestrator-impl-questions.md), Q8.

### Orchestrator result delivery prompt

The orchestrator system prompt includes a `orchestratorResultDelivery` block that is auto-injected for **all** orchestrators (built-in and custom), alongside the agent catalog and task focus:

```
## Result Delivery

Sub-agent results appear automatically as messages prefixed with [Sub-agent completed] or [Sub-agent failed/cancelled]. You do not need to poll for them.
If you have no more tool calls but sub-agents are still running, the system automatically waits — you do not need to take any action to stay alive.
You may receive results one at a time. React to each as needed: dispatch follow-ups, cancel unnecessary agents, or continue waiting.
When all relevant results are collected, produce your final analysis.
```

This separation means:
- **System-injected for all orchestrators**: agent catalog + result delivery + task focus (universal mechanics)
- **`custom_instructions` per-agent**: strategy, principles, domain guidance (customizable by operators)

## Agent Factory Changes

Per [ADR-0001](../adr/0001-agent-type-refactor.md), the controller factory already switches on `AgentType`. The current factory handles `AgentTypeDefault` and `AgentTypeSynthesis`. The orchestrator adds one new case:

```go
// pkg/agent/controller/factory.go — current + new case
func (f *Factory) CreateController(agentType AgentType, execCtx *ExecutionContext) (Controller, error) {
    switch agentType {
    case config.AgentTypeDefault:
        return NewIteratingController(), nil
    case config.AgentTypeSynthesis:
        return NewSynthesisController(execCtx.PromptBuilder), nil
    case config.AgentTypeOrchestrator:              // NEW
        return NewIteratingController(), nil        // Same controller, different tools
    default:
        return nil, fmt.Errorf("unknown agent type: %q", agentType)
    }
}
```

The `AgentFactory.CreateAgent` already handles agent-type → agent-wrapper selection (`BaseAgent` vs `ScoringAgent`). The orchestrator uses `BaseAgent` — no new agent wrapper needed.

The orchestrator maps to `IteratingController` — the same multi-turn tool-calling loop as default agents. The orchestration behavior comes entirely from the `CompositeToolExecutor` (tool routing) and the `SubAgentRunner` (push-based result delivery via `SubAgentResultCollector` interface on `ExecutionContext`). The controller itself just gains the generic drain/wait logic that activates when `SubAgentCollector` is non-nil.

## Session Executor Changes

### New field on `RealSessionExecutor`

The `SubAgentRegistry` is built once at construction time and stored on the executor:

```go
// pkg/queue/executor.go — addition
type RealSessionExecutor struct {
    // ... existing fields (cfg, dbClient, llmClient, eventPublisher, agentFactory, promptBuilder, mcpFactory, runbookService) ...
    subAgentRegistry *config.SubAgentRegistry    // NEW: built from merged agents at startup
}

func NewRealSessionExecutor(...) *RealSessionExecutor {
    // ... existing init ...
    return &RealSessionExecutor{
        // ... existing fields ...
        subAgentRegistry: config.BuildSubAgentRegistry(cfg.AgentRegistry.GetAll()),
    }
}
```

### Helper functions

```go
// resolveOrchestratorGuardrails merges defaults.orchestrator + agent-level orchestrator config.
func resolveOrchestratorGuardrails(cfg *config.Config, agentDef *config.AgentConfig) *orchestrator.OrchestratorGuardrails {
    g := &orchestrator.OrchestratorGuardrails{
        MaxConcurrentAgents: 5,       // hardcoded fallback
        AgentTimeout:        300 * time.Second,
        MaxBudget:           600 * time.Second,
    }
    // Apply global defaults
    if cfg.Defaults != nil && cfg.Defaults.Orchestrator != nil {
        applyOrchestratorDefaults(g, cfg.Defaults.Orchestrator)
    }
    // Apply per-agent override
    if agentDef.Orchestrator != nil {
        applyOrchestratorDefaults(g, agentDef.Orchestrator)
    }
    return g
}

// resolveSubAgents determines the sub_agents list from chain → stage → stage-agent hierarchy.
// Returns nil if no override at any level (meaning: use full global registry).
func resolveSubAgents(chain *config.ChainConfig, stage config.StageConfig, agentCfg config.StageAgentConfig) []string {
    // stage-agent > stage > chain (same pattern as mcp_servers)
    if len(agentCfg.SubAgents) > 0 { return agentCfg.SubAgents }
    if len(stage.SubAgents) > 0    { return stage.SubAgents }
    if len(chain.SubAgents) > 0    { return chain.SubAgents }
    return nil
}
```

### Orchestrator wiring in `executeAgent`

The `executeAgent` method detects orchestrator agents and wires up the `CompositeToolExecutor`. Note: the orchestrator check uses `resolvedConfig.Type` (resolved from the agent registry via `ResolveAgentConfig`), not `agentConfig.Type` (`StageAgentConfig` has no `Type` field):

```go
func (e *RealSessionExecutor) executeAgent(...) agentResult {
    resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, input.chain, input.stageConfig, agentConfig)
    // ... error handling, DB record creation, MCP resolution ...
    
    toolExecutor, failedServers := createToolExecutor(ctx, e.mcpFactory, serverIDs, toolFilter, logger)
    defer func() { _ = toolExecutor.Close() }()
    
    // Build execution context (same as today)
    execCtx := &agent.ExecutionContext{ /* ... existing fields ... */ }
    
    // If orchestrator: wrap with CompositeToolExecutor + set up SubAgentRunner
    if resolvedConfig.Type == config.AgentTypeOrchestrator {
        agentDef, _ := e.cfg.GetAgent(agentConfig.Name)
        deps := &orchestrator.SubAgentDeps{
            Config: e.cfg, Chain: input.chain,
            AgentFactory: e.agentFactory, MCPFactory: e.mcpFactory,
            LLMClient: e.llmClient, EventPublisher: e.eventPublisher,
            PromptBuilder: e.promptBuilder,
            StageService: input.stageService, TimelineService: input.timelineService,
            MessageService: input.messageService, InteractionService: input.interactionService,
            AlertData: input.session.AlertData, AlertType: input.session.AlertType,
            RunbookContent: e.resolveRunbook(ctx, input.session),
        }
        guardrails := resolveOrchestratorGuardrails(e.cfg, agentDef)
        subAgentNames := resolveSubAgents(input.chain, input.stageConfig, agentConfig)
        registry := e.subAgentRegistry.Filter(subAgentNames)  // nil = full registry
        runner := orchestrator.NewSubAgentRunner(ctx, deps, exec.ID, input.session.ID, stg.ID, registry, guardrails)
        toolExecutor = orchestrator.NewCompositeToolExecutor(toolExecutor, runner, registry)
        execCtx.SubAgentCollector = orchestrator.NewResultCollector(runner)
        execCtx.SubAgentCatalog = registry.Entries()
    }
    
    execCtx.ToolExecutor = toolExecutor
    // ... rest stays the same (CreateAgent, Execute, status updates)
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

Implementation: `SubAgentRunner` stores the session-level context (`parentCtx`) passed at construction time. Sub-agent goroutines derive their contexts from `parentCtx` (with `agent_timeout` deadline), **not** from the per-iteration `ctx` passed to `Dispatch`. This is critical — the per-iteration context is short-lived and cancelled at the end of each orchestrator iteration, which would prematurely terminate sub-agents. The session-level context lives for the entire session, so sub-agents survive across orchestrator iterations and are only cancelled when the session itself is cancelled.

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

### PR0: `native_tools` on AgentConfig (prerequisite) ✅ DONE
- `native_tools` field on `AgentConfig` — per-agent override of provider's native tools
- Merge logic: agent-level keys override provider-level keys
- Pass resolved native tools through to the LLM client
- Independent of orchestrator — useful on its own

### PR1: Config foundation ✅ DONE
- `sub_agents` override at chain/stage/agent level (full hierarchy) — new fields on `ChainConfig`, `StageConfig`, `StageAgentConfig`
- `orchestrator` nested config section on `AgentConfig`
- `defaults.orchestrator` global defaults — new `Orchestrator` field on `Defaults` struct
- `SubAgentRegistry` built from merged agents (agents with `description`) — stored on `RealSessionExecutor`
- New built-in agents: `WebResearcher`, `CodeExecutor`, `GeneralWorker` (depends on PR0)
- Extend `BuiltinAgentConfig` with `LLMBackend` and `NativeTools` fields; update `mergeAgents` to carry them
- Config validation: `orchestrator` section forbidden on non-orchestrator agents
- **Note:** `AgentConfig.MCPServers` has a `validate:"required,min=1"` struct tag, but the actual validator (custom `validateAgents`) treats MCP servers as optional. Existing agents (ChatAgent, SynthesisAgent) have none. The new built-ins follow the same pattern. The struct tag should be corrected to `validate:"omitempty"` as a housekeeping fix.

### PR2: DB schema ✅ DONE
- `parent_execution_id` on `AgentExecution` (nullable)
- `task` on `AgentExecution` (nullable)
- Replace `UNIQUE(stage_id, agent_index)` with two partial indexes: `UNIQUE(stage_id, agent_index) WHERE parent_execution_id IS NULL` (top-level) + `UNIQUE(parent_execution_id, agent_index) WHERE parent_execution_id IS NOT NULL` (sub-agents)
- New timeline event type: `task_assigned`
- `UpdateStageStatus` filter: exclude sub-agents (non-null `parent_execution_id`)
- Query helpers: sub-agents by parent, trace tree
- DTOs: `ParentExecutionID` + `Task` on `CreateAgentExecutionRequest` and `ExecutionOverview`; `SubAgents []ExecutionOverview` on `ExecutionOverview`
- `GetSessionDetail`: eager-loads sub-agents; filters top-level `Executions` to exclude sub-agents (they appear only nested under their parent via `SubAgents`)
- `buildExecutionOverview` helper extracted for reuse across top-level and sub-agent overviews

### PR3: SubAgentRunner + CompositeToolExecutor ✅ DONE
- `SubAgentRunner` — dispatch, cancel, list, results channel (`TryGetNext`, `WaitForNext`, `HasPending`)
- `CompositeToolExecutor` — wraps MCP executor + orchestration tools
- `SubAgentDeps` dependency bundle (uses services, not raw `*ent.Client` — see struct above)
- `SubAgentResult`, `SubAgentStatus`, `OrchestratorGuardrails` types
- `FormatSubAgentResult` (exported — called from controller package in PR4)
- Tool name constants (`ToolDispatchAgent`, `ToolCancelAgent`, `ToolListAgents`)
- `closeCh` mechanism for clean result delivery on individual cancel vs bulk shutdown
- `SubAgentRegistry.Get(name)` method for dispatch validation
- Unit tests (channel mechanics, dispatch validation, tool executor routing)
- Integration tests (testcontainers DB + mock agent: concurrent dispatch, DB record verification, timeout, cancel)

### PR4: Controller modification + orchestrator prompt ✅ DONE
- Push-based drain/wait logic in `IteratingController` (via `ExecutionContext.SubAgentCollector`)
- `SubAgentResultCollector` interface in `pkg/agent/context.go` — breaks `agent↔orchestrator` import cycle
- `ResultCollector` adapter in `pkg/agent/orchestrator/collector.go` — wraps `SubAgentRunner`, converts `SubAgentResult` → `ConversationMessage` via `FormatSubAgentResult`
- New fields on `ExecutionContext`: `SubAgentCollector`, `SubAgent *SubAgentContext`, `SubAgentCatalog`
- `SubAgentContext` struct (same pattern as `ChatContext`)
- `AgentTypeOrchestrator` case in controller factory → `IteratingController`
- `buildSubAgentMessages` — custom `## Task` template (detected via `execCtx.SubAgent != nil`)
- `buildOrchestratorMessages` — agent catalog in system prompt (detected via `execCtx.Config.Type`)
- Both paths dispatched from existing `BuildFunctionCallingMessages` — no new methods on `PromptBuilder` interface
- Tests for prompt building, controller behavior, collector adapter, and factory

### PR5: Session executor wiring ✅ DONE
- Add `subAgentRegistry` field to `RealSessionExecutor` (built at construction time)
- `resolveOrchestratorGuardrails` + `resolveSubAgents` helper functions
- Detect orchestrator type in `executeAgent` (via `resolvedConfig.Type`) → create runner + composite executor
- Wire `SubAgentDeps` from session executor fields
- Pass session-level `ctx` to `NewSubAgentRunner` as `parentCtx` — sub-agent contexts must derive from the long-lived session context, not the per-iteration context (which is cancelled at the end of each orchestrator iteration)
- Set `ExecutionContext.SubAgentCollector` (via `orchestrator.NewResultCollector`) and `SubAgentCatalog` for orchestrator agents; `SubAgent` set by `SubAgentRunner.Dispatch`
- Integration tests

### PR6: E2E Tests ✅ DONE
- **Config**: `testdata/configs/orchestrator/tarsy.yaml` — orchestrator agent (`type: orchestrator`) with `sub_agents` at stage-agent level, one custom sub-agent (LogAnalyzer with MCP tools, `max_iterations: 2`) and one built-in sub-agent (GeneralWorker, used as-is with no overrides). This mix tests both custom agent definition and built-in agent reuse — the production-realistic pattern.
- **Config**: `testdata/configs/orchestrator-cancel/tarsy.yaml` — same structure with `defaults.max_iterations: 1` for cancellation test (prevents retry loops after context cancellation).
- **7 E2E tests** covering deterministic golden-file verification, realistic multi-agent scenarios, reactive multi-phase dispatch, failure handling, tool coverage (dispatch/list/cancel), and cascading cancellation:

1. **`TestE2E_Orchestrator`** (deterministic golden files): dispatches single sub-agent (LogAnalyzer) with MCP tools. Single sub-agent keeps orchestrator iteration count deterministic (3 iterations) for stable golden files. Uses `WaitCh` to force the wait path — sub-agent is gated until the orchestrator's iteration 2 enters `WaitForResult`. Verifies: DB records (stage, executions, parent_execution_id, task, timeline events), API responses (session status, trace list with nested sub-agents), WS events (`OrchestratorExpectedEvents`), golden files (session, stages, timeline, trace list, per-interaction trace details including sub-agent MCP tool calls), executive summary generation.

2. **`TestE2E_OrchestratorMultiAgent`** (realistic production scenario): dispatches both LogAnalyzer (MCP tools) and GeneralWorker (built-in, pure reasoning). Asserts on end results only — no golden files, no iteration count assertions. Verifies: 3 executions all completed, correct parent-child relationships, task fields, trace API nesting (2 sub-agents under orchestrator), executive summary, WS session completion.

3. **`TestE2E_OrchestratorMultiPhase`** (reactive multi-phase dispatch): Phase 1 dispatches LogAnalyzer and GeneralWorker in parallel. After receiving their results, Phase 2 reactively dispatches GeneralWorker *again* with a follow-up remediation task derived from earlier findings. Tests the same built-in agent dispatched twice with different tasks (severity assessment vs remediation analysis). Verifies: 4 executions (orchestrator + 3 sub-agents), 2 GeneralWorker executions with different tasks, 3 `dispatch_agent` tool calls across 2 phases, trace API nesting with 3 sub-agents.

4. **`TestE2E_OrchestratorSubAgentFailure`**: dispatches LogAnalyzer (LLM errors, `max_iterations: 2` → 2 retry entries) and GeneralWorker (succeeds). Verifies: orchestrator completes despite sub-agent failure, failed sub-agent has `status=failed` and `error_message`, session completes.

5. **`TestE2E_OrchestratorListAgents`**: dispatches both sub-agents, then calls `list_agents` tool to check status mid-execution. Verifies: `list_agents` tool call appears in timeline, all 3 executions complete.

6. **`TestE2E_OrchestratorCancelSpecific`** (`cancel_agent` tool): dispatches LogAnalyzer (blocks via `BlockUntilCancelled`) and GeneralWorker (completes quickly). After GeneralWorker completes, orchestrator calls `cancel_agent` with LogAnalyzer's execution_id. Uses `RewriteChunks` callback on `ScriptedLLMClient` to dynamically inject the real execution_id from the conversation history (extracted from `dispatch_agent` tool result). Verifies: LogAnalyzer `status=cancelled`, GeneralWorker `status=completed`, `cancel_agent` tool call in timeline, 2 sub-agents in trace API.

7. **`TestE2E_OrchestratorCancellation`** (`orchestrator_cancel_test.go`): dispatches sub-agents, session cancelled via API while sub-agents are running. Uses `BlockUntilCancelled` on sub-agent LLM entries + `OnBlock` synchronization to ensure sub-agents are running before cancel. Verifies: all executions end in `cancelled` status, clean goroutine shutdown.

- **Bug fixes** discovered during E2E test review:
  - **Executive summary tokens**: `generateExecutiveSummary` now captures `UsageChunk` from the LLM stream and records `InputTokens`/`OutputTokens`/`TotalTokens` on the LLM interaction. Previously tokens were silently dropped.
  - **Timeline sequence collision**: Sub-agent executions had `task_assigned` (sequence 1) colliding with the first `llm_thinking` (also sequence 1). Fixed by adding `GetMaxSequenceForExecution` to `TimelineService` and initializing the controller's `eventSeq` from the DB rather than from 0.
  - **Orchestration tool `server_name`**: `dispatch_agent`/`cancel_agent`/`list_agents` tool calls are now recorded with `server_name: "orchestrator"` instead of `""`, enabling dashboard distinction between orchestration and real MCP calls.

### PR7: Dashboard
- Tree view: orchestrator → sub-agents (backend API already returns nested `SubAgents` in `ExecutionOverview` — see PR2)
- Sub-agent status, timelines, results
- Real-time updates via existing WebSocket events
