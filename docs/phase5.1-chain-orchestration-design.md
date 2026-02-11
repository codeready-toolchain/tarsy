# Phase 5.1: Chain Orchestration + Session Completion — Design

## Overview

Transform `RealSessionExecutor` from single-stage/single-agent execution to full sequential chain orchestration with inter-stage context passing, stage lifecycle events, and session completion (final analysis extraction + executive summary generation).

**Current state**: Executor runs `chain.Stages[0].Agents[0]` only. `prevStageContext` is always `""`. No stage events published. No executive summary.

**Target state**: Executor loops through all stages sequentially. Each stage runs its single agent (parallel execution deferred to Phase 5.2). Context flows between stages via DB queries. Stage lifecycle events are published. Session completes with final analysis and executive summary.

---

## Prerequisite: Fix Backend Derivation in LLM Client

**Problem**: `toProtoLLMConfig()` in `pkg/agent/llm_grpc.go` currently derives the Python backend from provider type (`Google → "google-native"`, everything else → `"langchain"`). This was a Phase 3 shortcut. The correct behavior — matching old TARSy and the Phase 3 iteration controllers design — is: **iteration strategy determines the backend**.

| Strategy | Backend | Reason |
|----------|---------|--------|
| `react` | `"langchain"` | Text-based tool calling, works with any provider via LangChain |
| `native-thinking` | `"google-native"` | Requires Google SDK for native thinking/tool calling |
| `synthesis` | `"langchain"` | Multi-provider synthesis |
| `synthesis-native-thinking` | `"google-native"` | Gemini thinking for synthesis |

**Non-agent LLM calls** inherit backend from context:
- **Summarization** (tool result summarization): Part of the agent execution — uses the agent's resolved `Backend` via `execCtx.Config.Backend`.
- **Executive summary**: Resolves its own strategy from chain/system defaults (see below), then derives backend from that strategy.

**Fix — two parts**:

#### Part 1: Resolve backend from strategy in `ResolvedAgentConfig`

Add a `Backend` field to `ResolvedAgentConfig` and resolve it from iteration strategy in `ResolveAgentConfig()`:

```go
// pkg/agent/context.go — add Backend field
type ResolvedAgentConfig struct {
    AgentName          string
    IterationStrategy  config.IterationStrategy
    LLMProvider        *config.LLMProviderConfig
    MaxIterations      int
    IterationTimeout   time.Duration
    MCPServers         []string
    CustomInstructions string
    NativeToolsOverride *models.NativeToolsConfig
    Backend            string // "google-native" or "langchain" — resolved from iteration strategy
}

const (
    BackendGoogleNative = "google-native"
    BackendLangChain    = "langchain"
)
```

```go
// pkg/agent/config_resolver.go — add backend resolution
func ResolveBackend(strategy config.IterationStrategy) string {
    switch strategy {
    case config.IterationStrategyNativeThinking,
         config.IterationStrategySynthesisNativeThinking:
        return BackendGoogleNative
    default:
        return BackendLangChain
    }
}
```

Called in `ResolveAgentConfig()` after resolving the iteration strategy:

```go
resolved.Backend = ResolveBackend(strategy)
```

#### Part 2: Flow backend through `GenerateInput` to gRPC

Add `Backend string` to `GenerateInput`:

```go
// pkg/agent/llm_client.go — add Backend field
type GenerateInput struct {
    SessionID   string
    ExecutionID string
    Messages    []ConversationMessage
    Config      *config.LLMProviderConfig
    Tools       []ToolDefinition
    Backend     string // "google-native" or "langchain"
}
```

**All callers within an agent execution** (controllers + summarization) pass through `execCtx.Config.Backend`:

```go
// All controllers (react, native_thinking, synthesis) and summarizeToolResult() use:
&agent.GenerateInput{
    ...
    Config:  execCtx.Config.LLMProvider,
    Backend: execCtx.Config.Backend,
}
```

**Executive summary** resolves its own backend from chain/system default strategy (see Section 7).

**gRPC layer change** — `toProtoLLMConfig()` stops deriving backend from type; `toProtoRequest()` uses `input.Backend`:

```go
func toProtoRequest(input *GenerateInput) *llmv1.GenerateRequest {
    req := &llmv1.GenerateRequest{
        SessionId:   input.SessionID,
        ExecutionId: input.ExecutionID,
        Messages:    toProtoMessages(input.Messages),
        Tools:       toProtoTools(input.Tools),
    }
    if input.Config != nil {
        req.LlmConfig = toProtoLLMConfig(input.Config)
    }
    // Backend is set by the caller based on iteration strategy, not derived from provider type
    if req.LlmConfig != nil {
        req.LlmConfig.Backend = input.Backend
    }
    return req
}
```

This is a prerequisite for Phase 5.1 because `generateExecutiveSummary()` needs to resolve its backend from the chain/system default strategy, and all existing controllers need to be updated to pass `execCtx.Config.Backend` through `GenerateInput`.

---

## Architecture

### Component Responsibilities

| Component | Responsibility |
|-----------|----------------|
| `RealSessionExecutor.Execute()` | Chain loop, stage orchestration, session completion |
| `executeStage()` | Stage execution (agents within a stage); Phase 5.1 handles one agent, Phase 5.2 extends to parallel goroutines — same method, same signature |
| `BuildStageContext()` | Format previous stages' final analyses for next stage prompt |
| `generateExecutiveSummary()` | LLM call to produce short session summary |
| `EventPublisher` | Stage lifecycle events (new: `stage.status`) |
| `StageService` | Stage + AgentExecution CRUD (existing, no changes needed) |
| `TimelineService` | Timeline event CRUD + queries (existing, minor addition) |

### Execution Flow

```
Worker claims session → RealSessionExecutor.Execute(ctx, session)
  │
  ├─ 1. Resolve chain from ChainRegistry
  │
  ├─ 2. Initialize shared services (StageService, MessageService, etc.)
  │
  ├─ 3. FOR each stage in chain.Stages:
  │     │
  │     ├─ Check context cancellation
  │     ├─ Guard: reject parallel stages (Phase 5.2)
  │     ├─ Update session progress (current_stage_index, current_stage_id)
  │     ├─ Publish stage.status (started)
  │     │
  │     ├─ executeStage(ctx, ...)
  │     │   ├─ Create Stage DB record
  │     │   ├─ For each agent in stage (Phase 5.1: always 1; Phase 5.2: parallel goroutines):
  │     │   │   ├─ Create AgentExecution DB record
  │     │   │   ├─ Resolve agent config (hierarchy)
  │     │   │   ├─ Resolve MCP selection (per-alert override or agent config)
  │     │   │   ├─ Create MCP ToolExecutor (or stub)
  │     │   │   ├─ Build ExecutionContext
  │     │   │   ├─ Create Agent via AgentFactory
  │     │   │   ├─ agent.Execute(ctx, execCtx, prevStageContext)
  │     │   │   └─ Update AgentExecution status
  │     │   ├─ Update Stage status (aggregation)
  │     │   └─ Return stageResult{stageID, status, finalAnalysis, error}
  │     │
  │     ├─ Publish stage.status (terminal status)
  │     │
  │     ├─ On failure: return immediately (fail-fast)
  │     │
  │     └─ Build prevStageContext for next stage (BuildStageContext)
  │
  ├─ 4. Extract final analysis from last completed stage
  │
  ├─ 5. Generate executive summary (LLM call, fail-open)
  │
  └─ 6. Return ExecutionResult{Status, FinalAnalysis, ExecutiveSummary}
        → Worker writes terminal session status
```

---

## Detailed Design

### 1. Chain Orchestrator Loop (`pkg/queue/executor.go`)

The main `Execute()` method is refactored from a flat single-stage flow into a loop:

```go
func (e *RealSessionExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
    // 1. Resolve chain configuration
    chain, err := e.cfg.GetChain(session.ChainID)
    // ... error handling (unchanged)

    // 2. Initialize shared services (created once, shared across stages)
    stageService := services.NewStageService(e.dbClient)
    messageService := services.NewMessageService(e.dbClient)
    timelineService := services.NewTimelineService(e.dbClient)
    interactionService := services.NewInteractionService(e.dbClient, messageService)

    // 3. Sequential stage execution loop
    var prevStageContext string
    var completedStages []stageResult

    for i, stageConfig := range chain.Stages {
        // Check for cancellation before starting next stage
        if ctx.Err() != nil {
            return e.mapCancellation(ctx)
        }

        // Update session progress tracking
        e.updateSessionProgress(ctx, session.ID, i+1, "")

        // Publish stage.status (started)
        e.publishStageStatus(ctx, session.ID, "", stageConfig.Name, i+1, "started")

        // Execute stage (Phase 5.1: single agent; Phase 5.2: extends to parallel)
        result := e.executeStage(ctx, executeStageInput{
            session:            session,
            chain:              chain,
            stageConfig:        stageConfig,
            stageIndex:         i + 1, // 1-based
            prevStageContext:    prevStageContext,
            stageService:       stageService,
            messageService:     messageService,
            timelineService:    timelineService,
            interactionService: interactionService,
        })

        // Update session progress with stage ID (now known)
        if result.stageID != "" {
            e.updateSessionProgress(ctx, session.ID, i+1, result.stageID)
        }

        // Publish stage.status (terminal)
        e.publishStageStatus(ctx, session.ID, result.stageID, stageConfig.Name, i+1, string(result.status))

        // Fail-fast: stop chain on non-completed stage
        if result.status != agent.ExecutionStatusCompleted {
            return &ExecutionResult{
                Status: mapAgentStatusToSessionStatus(result.status),
                Error:  result.err,
            }
        }

        // Track completed stage for context building
        completedStages = append(completedStages, result)

        // Build context for next stage (if not the last stage)
        if i < len(chain.Stages)-1 {
            prevStageContext = e.buildStageContext(completedStages)
        }
    }

    // 4. All stages completed — extract final analysis
    finalAnalysis := extractFinalAnalysis(completedStages)

    // 5. Generate executive summary (fail-open)
    executiveSummary, summaryErr := e.generateExecutiveSummary(ctx, generateSummaryInput{
        session:         session,
        finalAnalysis:   finalAnalysis,
        timelineService: timelineService,
    })
    if summaryErr != nil {
        logger.Warn("Executive summary generation failed (continuing)", "error", summaryErr)
    }

    return &ExecutionResult{
        Status:           alertsession.StatusCompleted,
        FinalAnalysis:    finalAnalysis,
        ExecutiveSummary: executiveSummary,
    }
}
```

### 2. Stage Execution (`executeStage`)

Extracted from the current `Execute()` code. Creates the Stage DB record, then runs agents within it. Phase 5.1 runs a single agent directly; Phase 5.2 extends this same method to spawn goroutines for parallel agents — no separate code path.

```go
// stageResult is an internal type for passing data between the chain loop
// and post-chain logic (context building, final analysis extraction).
type stageResult struct {
    stageID       string
    executionID   string
    stageName     string
    status        agent.ExecutionStatus
    finalAnalysis string
    err           error
}

type executeStageInput struct {
    session            *ent.AlertSession
    chain              *config.ChainConfig
    stageConfig        config.StageConfig
    stageIndex         int
    prevStageContext    string
    stageService       *services.StageService
    messageService     *services.MessageService
    timelineService    *services.TimelineService
    interactionService *services.InteractionService
}

func (e *RealSessionExecutor) executeStage(
    ctx context.Context,
    input executeStageInput,
) stageResult {
    // Guard: Phase 5.1 only supports single-agent stages.
    // Phase 5.2 replaces this guard with goroutine-per-agent execution.
    if len(input.stageConfig.Agents) > 1 || input.stageConfig.Replicas > 1 {
        return stageResult{
            status: agent.ExecutionStatusFailed,
            err:    fmt.Errorf("parallel stages not yet supported (stage %q has %d agents or %d replicas)",
                input.stageConfig.Name, len(input.stageConfig.Agents), input.stageConfig.Replicas),
        }
    }

    agentConfig := input.stageConfig.Agents[0]

    // 1. Create Stage DB record
    stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
        SessionID:          input.session.ID,
        StageName:          input.stageConfig.Name,
        StageIndex:         input.stageIndex,
        ExpectedAgentCount: 1, // Phase 5.2: len(agents) or replicas
    })
    if err != nil {
        return stageResult{status: agent.ExecutionStatusFailed, err: ...}
    }

    // 2. Run the agent (Phase 5.2: loop over agents, goroutine-per-agent)
    agentResult := e.executeAgent(ctx, stg, input, agentConfig, 1)

    // 3. Update Stage status (aggregation)
    // ... update calls (same as current code)

    return stageResult{
        stageID:       stg.ID,
        executionID:   agentResult.executionID,
        stageName:     input.stageConfig.Name,
        status:        agentResult.status,
        finalAnalysis: agentResult.finalAnalysis,
        err:           agentResult.err,
    }
}

// executeAgent runs a single agent within a stage. Separated from executeStage
// so Phase 5.2 can call it per-goroutine for parallel agents.
func (e *RealSessionExecutor) executeAgent(
    ctx context.Context,
    stg *ent.Stage,
    input executeStageInput,
    agentConfig config.StageAgentConfig,
    agentIndex int,
) agentResult {
    // 1. Create AgentExecution DB record
    exec, err := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
        StageID:           stg.ID,
        SessionID:         input.session.ID,
        AgentName:         agentConfig.Name,
        AgentIndex:        agentIndex,
        IterationStrategy: string(agentConfig.IterationStrategy),
    })
    if err != nil {
        return agentResult{status: agent.ExecutionStatusFailed, err: ...}
    }

    // 2. Resolve agent config from hierarchy
    resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, input.chain, input.stageConfig, agentConfig)
    // ... error handling

    // 3. Resolve MCP selection (per-alert override applies to all stages)
    serverIDs, toolFilter, err := e.resolveMCPSelection(input.session, resolvedConfig)
    // ... error handling

    // 4. Create MCP tool executor (or stub) — per-agent-execution lifecycle
    toolExecutor, failedServers := e.createToolExecutor(ctx, serverIDs, toolFilter)
    defer func() { _ = toolExecutor.Close() }()

    // 5. Build ExecutionContext
    execCtx := &agent.ExecutionContext{
        SessionID:      input.session.ID,
        StageID:        stg.ID,
        ExecutionID:    exec.ID,
        AgentName:      agentConfig.Name,
        AgentIndex:     agentIndex,
        AlertData:      input.session.AlertData,
        AlertType:      input.session.AlertType,
        RunbookContent: config.GetBuiltinConfig().DefaultRunbook,
        Config:         resolvedConfig,
        LLMClient:      e.llmClient,
        ToolExecutor:   toolExecutor,
        EventPublisher: e.eventPublisher,
        PromptBuilder:  e.promptBuilder,
        FailedServers:  failedServers,
        Services: &agent.ServiceBundle{
            Timeline:    input.timelineService,
            Message:     input.messageService,
            Interaction: input.interactionService,
            Stage:       input.stageService,
        },
    }

    // 6. Create and execute agent
    agentInstance, err := e.agentFactory.CreateAgent(execCtx)
    // ... error handling

    result, err := agentInstance.Execute(ctx, execCtx, input.prevStageContext)
    // ... error handling

    // 7. Update AgentExecution status
    entStatus := mapAgentStatusToEntStatus(result.Status)
    // ... update call (same as current code)

    return agentResult{
        executionID:   exec.ID,
        status:        result.Status,
        finalAnalysis: result.FinalAnalysis,
        err:           result.Error,
    }
}
```

### 3. Stage Context Building (`pkg/agent/context/stage_context.go`)

New file for building inter-stage context from completed stage results.

```go
package context

import (
    "fmt"
    "strings"
)

// StageResult holds the output of a completed stage for context building.
// Populated by the executor from DB queries (lazy context building — no
// stored output fields on Stage or AgentExecution entities).
type StageResult struct {
    StageName     string
    FinalAnalysis string
}

// BuildStageContext formats completed stage results into a context string
// for the next stage's agent prompt. Each stage's final analysis is included
// with its stage name as a header.
//
// The returned string is passed as prevStageContext to Agent.Execute() and
// wrapped by FormatChainContext() in the prompt builder.
func BuildStageContext(stages []StageResult) string {
    if len(stages) == 0 {
        return ""
    }

    var sb strings.Builder
    sb.WriteString("<!-- CHAIN_CONTEXT_START -->\n\n")

    for i, stage := range stages {
        sb.WriteString(fmt.Sprintf("### Stage %d: %s\n\n", i+1, stage.StageName))
        if stage.FinalAnalysis != "" {
            sb.WriteString(stage.FinalAnalysis)
        } else {
            sb.WriteString("(No final analysis produced)")
        }
        sb.WriteString("\n\n")
    }

    sb.WriteString("<!-- CHAIN_CONTEXT_END -->")
    return sb.String()
}
```

**Executor integration** — the chain loop calls `buildStageContext()` which converts `[]stageResult` to `[]context.StageResult` and delegates:

```go
func (e *RealSessionExecutor) buildStageContext(stages []stageResult) string {
    results := make([]agentctx.StageResult, len(stages))
    for i, s := range stages {
        results[i] = agentctx.StageResult{
            StageName:     s.stageName,
            FinalAnalysis: s.finalAnalysis,
        }
    }
    return agentctx.BuildStageContext(results)
}
```

Note: `stageResult.finalAnalysis` comes from `agent.ExecutionResult.FinalAnalysis` which is set by the controller when the agent produces its `final_analysis` timeline event. No additional DB query needed — the value flows through the in-memory chain loop.

### 4. Stage Lifecycle Events

#### New Event Payload (`pkg/events/payloads.go`)

```go
// StageStatusPayload is the payload for stage.status events.
// Single event type for all stage lifecycle transitions (started, completed, failed, etc.).
type StageStatusPayload struct {
    Type       string `json:"type"`        // always EventTypeStageStatus
    SessionID  string `json:"session_id"`
    StageID    string `json:"stage_id"`    // may be empty on "started" if stage creation hasn't happened yet
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"` // 1-based
    Status     string `json:"status"`      // started, completed, failed, timed_out, cancelled
    Timestamp  string `json:"timestamp"`
}
```

#### New Publisher Method (`pkg/events/publisher.go`)

```go
func (p *EventPublisher) PublishStageStatus(ctx context.Context, sessionID string, payload StageStatusPayload) error {
    payloadJSON, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("failed to marshal StageStatusPayload: %w", err)
    }
    return p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON)
}
```

#### Updated EventPublisher Interface (`pkg/agent/context.go`)

```go
type EventPublisher interface {
    PublishTimelineCreated(ctx context.Context, sessionID string, payload events.TimelineCreatedPayload) error
    PublishTimelineCompleted(ctx context.Context, sessionID string, payload events.TimelineCompletedPayload) error
    PublishStreamChunk(ctx context.Context, sessionID string, payload events.StreamChunkPayload) error
    PublishSessionStatus(ctx context.Context, sessionID string, payload events.SessionStatusPayload) error
    // New in Phase 5.1:
    PublishStageStatus(ctx context.Context, sessionID string, payload events.StageStatusPayload) error
}
```

#### Event Type Convention

Event types follow these patterns:
- **Single `.status` type** when the payload shape is the same across all states: `session.status`, `stage.status`
- **Separate types** when payloads carry fundamentally different data: `timeline_event.created` (full context) vs `timeline_event.completed` (event_id + final content only)
- **Standalone type** for transient high-frequency events: `stream.chunk`

Cleanup: Remove unused `EventTypeSessionCompleted` constant and rename `EventTypeStageStarted`/`EventTypeStageCompleted` to single `EventTypeStageStatus` in `pkg/events/types.go`.

**Design note**: Stage events are published from the **executor** (chain loop), not from controllers. Controllers are unaware of stage boundaries. This is correct because stage lifecycle is an executor concern.

### 5. Session Progress Tracking

Update `AlertSession.current_stage_index` and `current_stage_id` as each stage starts:

```go
func (e *RealSessionExecutor) updateSessionProgress(ctx context.Context, sessionID string, stageIndex int, stageID string) {
    update := e.dbClient.AlertSession.UpdateOneID(sessionID).
        SetCurrentStageIndex(stageIndex)
    if stageID != "" {
        update = update.SetCurrentStageID(stageID)
    }
    if err := update.Exec(ctx); err != nil {
        slog.Warn("Failed to update session progress",
            "session_id", sessionID, "stage_index", stageIndex, "error", err)
    }
}
```

Non-blocking: progress tracking failure is logged but does not stop execution. These fields are informational for dashboard/debugging visibility.

### 6. Final Analysis Extraction

After the chain loop completes, extract the final analysis from the last completed stage:

```go
// extractFinalAnalysis returns the final analysis from the last completed stage.
// Falls back to earlier stages if the last one has no final analysis.
func extractFinalAnalysis(stages []stageResult) string {
    // Reverse search: prefer later stages (typically the final-diagnosis or synthesis stage)
    for i := len(stages) - 1; i >= 0; i-- {
        if stages[i].finalAnalysis != "" {
            return stages[i].finalAnalysis
        }
    }
    return ""
}
```

This matches old TARSy's `_extract_final_analysis_from_stages()` which iterates in reverse.

### 7. Executive Summary Generation

After all stages complete successfully, generate a short executive summary via LLM call:

```go
type generateSummaryInput struct {
    session         *ent.AlertSession
    finalAnalysis   string
    timelineService *services.TimelineService
}

func (e *RealSessionExecutor) generateExecutiveSummary(
    ctx context.Context,
    input generateSummaryInput,
) (string, error) {
    if input.finalAnalysis == "" {
        return "", nil // Nothing to summarize
    }

    // 1. Build prompts
    systemPrompt := e.promptBuilder.BuildExecutiveSummarySystemPrompt()
    userPrompt := e.promptBuilder.BuildExecutiveSummaryUserPrompt(input.finalAnalysis)

    messages := []agent.ConversationMessage{
        {Role: "system", Content: systemPrompt},
        {Role: "user", Content: userPrompt},
    }

    // 2. Resolve LLM provider and backend for executive summary
    // Provider hierarchy: chain.executive_summary_provider → chain.llm_provider → defaults.llm_provider
    // Strategy hierarchy: chain.iteration_strategy → defaults.iteration_strategy
    chain, _ := e.cfg.GetChain(input.session.ChainID)
    providerName := e.cfg.Defaults.LLMProvider
    if chain != nil && chain.LLMProvider != "" {
        providerName = chain.LLMProvider
    }
    if chain != nil && chain.ExecutiveSummaryProvider != "" {
        providerName = chain.ExecutiveSummaryProvider
    }
    provider, err := e.cfg.GetLLMProvider(providerName)
    if err != nil {
        return "", fmt.Errorf("LLM provider for exec summary: %w", err)
    }

    // Resolve backend from chain/system default strategy
    strategy := e.cfg.Defaults.IterationStrategy
    if chain != nil && chain.IterationStrategy != "" {
        strategy = chain.IterationStrategy
    }
    backend := agent.ResolveBackend(strategy)

    // 3. Call LLM (single call, no tools)
    generateInput := &agent.GenerateInput{
        SessionID: input.session.ID,
        Messages:  messages,
        Config:    provider,
        Tools:     nil,
        Backend:   backend,
    }

    ch, err := e.llmClient.Generate(ctx, generateInput)
    if err != nil {
        return "", fmt.Errorf("executive summary LLM call failed: %w", err)
    }

    // 4. Collect response (non-streaming to timeline — exec summary is short)
    var summary strings.Builder
    for chunk := range ch {
        if textChunk, ok := chunk.(*agent.TextChunk); ok {
            summary.WriteString(textChunk.Text)
        }
        if errChunk, ok := chunk.(*agent.ErrorChunk); ok {
            return "", fmt.Errorf("LLM error during exec summary: %s", errChunk.Message)
        }
    }

    summaryText := strings.TrimSpace(summary.String())
    if summaryText == "" {
        return "", fmt.Errorf("executive summary LLM returned empty response")
    }

    // 5. Create session-level executive_summary timeline event
    // StageID and ExecutionID are omitted — this is a session-level event
    // (enabled by the Optional() schema change on those fields)
    _, err = input.timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
        SessionID:      input.session.ID,
        SequenceNumber: 9999, // High number to sort after all stage events
        EventType:      timelineevent.EventTypeExecutiveSummary,
        Content:        summaryText,
    })
    if err != nil {
        // Non-fatal: summary was generated, just couldn't persist the timeline event
        slog.Warn("Failed to create executive_summary timeline event", "error", err)
    }

    return summaryText, nil
}
```

**Failure handling**: Fail-open. If executive summary generation fails:
- `executiveSummary` is empty in `ExecutionResult`
- The error message is stored in `AlertSession.executive_summary_error` (schema field already exists)
- The session still completes successfully (the investigation itself succeeded)

**Modification to worker** (`pkg/queue/worker.go`): Store executive_summary_error when summary is empty but session completed:

```go
func (w *Worker) updateSessionTerminalStatus(ctx context.Context, session *ent.AlertSession, result *ExecutionResult) error {
    update := w.client.AlertSession.UpdateOneID(session.ID).
        SetStatus(result.Status).
        SetCompletedAt(time.Now())

    // ... existing fields ...

    if result.ExecutiveSummaryError != "" {
        update = update.SetExecutiveSummaryError(result.ExecutiveSummaryError)
    }

    return update.Exec(ctx)
}
```

Update `ExecutionResult` to carry the error:

```go
type ExecutionResult struct {
    Status                alertsession.Status
    FinalAnalysis         string
    ExecutiveSummary      string
    ExecutiveSummaryError string  // New: records why summary generation failed
    Error                 error
}
```

### 8. MCP Tool Executor Creation Helper

Extract the MCP creation logic into a reusable helper (called per agent execution):

```go
// createToolExecutor creates an MCP-backed tool executor for the given server
// configuration, falling back to a stub if MCP is unavailable.
func (e *RealSessionExecutor) createToolExecutor(
    ctx context.Context,
    serverIDs []string,
    toolFilter map[string][]string,
) (agent.ToolExecutor, map[string]string) {
    if e.mcpFactory == nil || len(serverIDs) == 0 {
        return agent.NewStubToolExecutor(nil), nil
    }

    mcpExecutor, mcpClient, err := e.mcpFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
    if err != nil {
        slog.Warn("Failed to create MCP tool executor, using stub", "error", err)
        return agent.NewStubToolExecutor(nil), nil
    }

    var failedServers map[string]string
    if mcpClient != nil {
        failedServers = mcpClient.FailedServers()
    }
    return mcpExecutor, failedServers
}
```

### 9. Error Handling & Cancellation

#### Stage Failure → Chain Stops

If a stage returns any non-completed status (failed, timed_out, cancelled), the chain loop exits immediately. The session is marked with that status. No subsequent stages execute.

#### Context Cancellation

Checked at two levels:
1. **Before each stage**: `ctx.Err()` check in the chain loop prevents starting new stages after timeout/cancel
2. **During agent execution**: Context propagates to the agent, which checks it per-iteration

#### Cancellation Status Mapping

```go
func (e *RealSessionExecutor) mapCancellation(ctx context.Context) *ExecutionResult {
    if errors.Is(ctx.Err(), context.DeadlineExceeded) {
        return &ExecutionResult{
            Status: alertsession.StatusTimedOut,
            Error:  fmt.Errorf("session timed out"),
        }
    }
    return &ExecutionResult{
        Status: alertsession.StatusCancelled,
        Error:  context.Canceled,
    }
}
```

---

## Event Flow (WebSocket)

For a 2-stage chain with executive summary:

```
session.status: in_progress

stage.status:    {stage_name: "data-collection", stage_index: 1, status: "started"}
  timeline_event.created:   {event_type: "llm_thinking", ...}
  stream.chunk:             {delta: "..."}
  timeline_event.completed: {event_type: "llm_thinking", ...}
  timeline_event.created:   {event_type: "llm_tool_call", ...}
  timeline_event.completed: {event_type: "llm_tool_call", ...}
  timeline_event.created:   {event_type: "final_analysis", ...}
  timeline_event.completed: {event_type: "final_analysis", ...}
stage.status:    {stage_name: "data-collection", stage_index: 1, status: "completed"}

stage.status:    {stage_name: "final-diagnosis", stage_index: 2, status: "started"}
  timeline_event.created:   {event_type: "llm_thinking", ...}
  ...
  timeline_event.completed: {event_type: "final_analysis", ...}
stage.status:    {stage_name: "final-diagnosis", stage_index: 2, status: "completed"}

timeline_event.created:   {event_type: "executive_summary", ...}  ← session-level (no stage_id/execution_id)

session.status: completed
```

---

## Files Changed

### New Files

| File | Purpose |
|------|---------|
| `pkg/agent/context/stage_context.go` | `BuildStageContext()` function + `StageResult` type |
| `pkg/agent/context/stage_context_test.go` | Unit tests for stage context formatting |
| `pkg/database/migrations/{timestamp}_optional_stage_execution_on_timeline.sql` | Atlas-generated migration for optional stage_id/execution_id |

### Modified Files

| File | Changes |
|------|---------|
| `pkg/agent/context.go` | Add `Backend` field to `ResolvedAgentConfig`; add `BackendGoogleNative`/`BackendLangChain` constants; add `PublishStageStatus()` to `EventPublisher` interface |
| `pkg/agent/config_resolver.go` | Add `ResolveBackend()` function; wire into `ResolveAgentConfig()` |
| `pkg/agent/llm_client.go` | Add `Backend` field to `GenerateInput` |
| `pkg/agent/llm_grpc.go` | `toProtoRequest()` uses `input.Backend` instead of deriving from provider type; remove backend derivation from `toProtoLLMConfig()` |
| `pkg/agent/controller/*.go` | All controllers pass `execCtx.Config.Backend` through to `GenerateInput.Backend` |
| `ent/schema/timelineevent.go` | `stage_id` and `execution_id` fields become `Optional()`; edges lose `Required()` |
| `ent/**` (generated) | `make ent-generate` regenerates all Ent code |
| `pkg/config/chain.go` | Add `ExecutiveSummaryProvider` and `IterationStrategy` fields to `ChainConfig` |
| `pkg/services/timeline_service.go` | `CreateTimelineEvent` makes StageID/ExecutionID conditional instead of required |
| `pkg/queue/executor.go` | Chain loop, `executeStage()`, `executeAgent()`, `generateExecutiveSummary()`, `buildStageContext()`, `createToolExecutor()`, `updateSessionProgress()` |
| `pkg/queue/types.go` | Add `ExecutiveSummaryError` to `ExecutionResult` |
| `pkg/queue/worker.go` | Store `ExecutiveSummaryError` in terminal status update |
| `pkg/events/types.go` | Replace `EventTypeStageStarted`/`EventTypeStageCompleted` with `EventTypeStageStatus`; remove unused `EventTypeSessionCompleted` |
| `pkg/events/payloads.go` | Add `StageStatusPayload`; add `omitempty` to StageID/ExecutionID on `TimelineCreatedPayload` |
| `pkg/events/publisher.go` | Add `PublishStageStatus()` |

### Test Files

| File | Purpose |
|------|---------|
| `pkg/queue/executor_test.go` | Chain loop tests (multi-stage, fail-fast, cancellation, exec summary) |
| `pkg/agent/context/stage_context_test.go` | Context formatting tests |
| `pkg/events/publisher_test.go` | Stage event publishing tests |

---

## Implementation Plan

Ordered by implementation sequence, each step is independently testable:

### Step 0: Fix Backend Derivation + Chain-Level Config (Prerequisite)
- Add `IterationStrategy` field to `ChainConfig` (`pkg/config/chain.go`) — chain-level default strategy, optional
- Update `ResolveAgentConfig()` to include chain-level strategy in resolution: stage-agent → agent-def → **chain** → defaults
- Add `Backend` field to `ResolvedAgentConfig` (`pkg/agent/context.go`) with `BackendGoogleNative`/`BackendLangChain` constants
- Add `ResolveBackend()` function to `pkg/agent/config_resolver.go` (maps iteration strategy → backend)
- Wire `ResolveBackend()` into `ResolveAgentConfig()` to populate `Backend` on resolved config
- Add `Backend` field to `GenerateInput` (`pkg/agent/llm_client.go`)
- Update `toProtoRequest()` in `pkg/agent/llm_grpc.go` to use `input.Backend`; remove backend derivation from `toProtoLLMConfig()`
- Update all controllers to pass `execCtx.Config.Backend` through to `GenerateInput.Backend` (react, native_thinking, synthesis, summarize — summarization is part of agent execution, uses same backend)
- Run existing tests to verify no regression (behavior unchanged for current single-provider usage)

### Step 1: Schema Migration — Optional stage_id/execution_id on TimelineEvent
- Update `ent/schema/timelineevent.go`: add `Optional()` to `stage_id` and `execution_id` fields; remove `Required()` from stage and agent_execution edges
- Run `make ent-generate` to regenerate Ent code
- Run `make migrate-create NAME=optional_stage_execution_on_timeline` to generate Atlas migration
- Update `TimelineService.CreateTimelineEvent()`: make StageID/ExecutionID conditional (set only when non-empty)
- Update `TimelineCreatedPayload`: add `omitempty` to StageID/ExecutionID
- Run existing tests to verify no regression (all existing code still passes non-empty values)

### Step 2: Stage Event Infrastructure
- Replace `EventTypeStageStarted`/`EventTypeStageCompleted` with single `EventTypeStageStatus` in `pkg/events/types.go`
- Remove unused `EventTypeSessionCompleted` constant
- Add `StageStatusPayload` to `pkg/events/payloads.go` (single payload for all stage lifecycle transitions)
- Add `PublishStageStatus()` to `pkg/events/publisher.go`
- Update `agent.EventPublisher` interface in `pkg/agent/context.go`
- Update mock/test implementations of EventPublisher
- Add publisher unit tests

### Step 3: Stage Context Builder
- Create `pkg/agent/context/stage_context.go` with `BuildStageContext()` and `StageResult`
- Add unit tests in `pkg/agent/context/stage_context_test.go`
- Test: empty stages, single stage, multiple stages, missing final analysis

### Step 4: Refactor Executor — Extract `executeStage` and `executeAgent`
- Extract `executeStage()` and `executeAgent()` from current `Execute()` code
- Extract `createToolExecutor()` helper
- Add `stageResult` and `agentResult` internal types
- Add temporary parallel guard inside `executeStage()` (removed in Phase 5.2)
- **Existing behavior unchanged** — loop still runs only first stage at this point
- Run existing tests to verify no regression

### Step 5: Implement Chain Loop
- Replace single-stage execution with loop over `chain.Stages`
- Add `mapCancellation()` helper
- Add `buildStageContext()` executor method
- Wire prevStageContext passing between stages
- Test with multi-stage chain configs

### Step 6: Session Progress Tracking
- Add `updateSessionProgress()` method
- Wire into chain loop (before each stage starts)
- Test that current_stage_index and current_stage_id are updated

### Step 7: Stage Lifecycle Events
- Wire `PublishStageStatus()` calls into chain loop (status: "started" before stage, terminal status after)
- Add nil-safety for EventPublisher (may be nil)
- Test event sequence

### Step 8: Final Analysis Extraction + Executive Summary
- Add `ExecutiveSummaryProvider` field to `ChainConfig` (`pkg/config/chain.go`) — note: `IterationStrategy` already added in Step 0
- Update config validator to validate `executive_summary_provider` references a known LLM provider (when set)
- Add `extractFinalAnalysis()` function
- Add `generateExecutiveSummary()` method — resolves provider via hierarchy: `chain.executive_summary_provider` → `chain.llm_provider` → `defaults.llm_provider`; resolves backend via strategy hierarchy: `chain.iteration_strategy` → `defaults.iteration_strategy`; creates session-level timeline event (no stage_id/execution_id)
- Add `ExecutiveSummaryError` to `ExecutionResult`
- Update worker to store `ExecutiveSummaryError`
- Wire into chain loop post-completion
- Test: successful summary, failed summary (fail-open), empty final analysis, custom exec summary provider, backend derived from chain strategy

### Step 9: Integration Tests
- Multi-stage chain with real DB (testcontainers)
- Verify: Stage DB records, timeline events, stage events, context passing, session completion
- Verify: executive_summary timeline event has NULL stage_id/execution_id
- Cancellation mid-chain test
- Stage failure stops chain test

---

## Compatibility & Migration

- **One schema migration** — `stage_id` and `execution_id` on `timeline_events` become nullable (`DROP NOT NULL`). Existing rows are unaffected (all have non-null values). All other fields already exist (current_stage_index, current_stage_id, executive_summary, executive_summary_error).
- **No API changes** — executor interface unchanged, session API responses gain executive_summary
- **Backward compatible** — single-stage chains work identically, just now going through the loop. Existing code passes non-empty StageID/ExecutionID, so behavior is unchanged.
- **Config compatible** — existing chain configs work without modification. New optional fields on `ChainConfig`: `executive_summary_provider` (defaults to chain/system `llm_provider`), `iteration_strategy` (defaults to system `defaults.iteration_strategy`). Both used by exec summary resolution and agent config hierarchy.

---

## Comparison with Old TARSy

| Aspect | Old TARSy | New TARSy (Phase 5.1) |
|--------|-----------|----------------------|
| Chain loop | `_execute_chain_stages()` in AlertService | `Execute()` loop in RealSessionExecutor |
| Context passing | In-memory `ChainContext.stage_outputs` dict | DB-backed lazy building via `BuildStageContext()` |
| Stage outputs | `AgentExecutionResult` stored in memory | `final_analysis` from `agent.ExecutionResult` (in-memory during chain loop) |
| MCP client | Per-session (shared across stages) | Per-agent-execution (create + teardown); Phase 5.2 parallel agents work without refactoring |
| Exec summary | `ExecutiveSummaryAgent` (full agent) | Direct LLM call in executor, configurable provider via `chain.executive_summary_provider` |
| Backend selection | Implicit: LangChain for ReAct/synthesis/exec summary, Google SDK for NativeThinking/synthesis-native-thinking | Explicit: `Backend` field resolved from iteration strategy via `ResolveBackend()` on `ResolvedAgentConfig`, passed through `GenerateInput` |
| Synthesis | Automatic after parallel stages | Phase 5.2 |
| Pause/resume | Supported | Not supported (force conclusion at max iterations) |
| Cancellation | `CancellationTracker` service | Context cancellation (Go idiom) |
| Progress tracking | Not tracked on session | `current_stage_index`, `current_stage_id` on AlertSession |
