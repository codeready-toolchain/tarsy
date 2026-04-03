# TARSy System Design - Functional Areas Overview

**Purpose**: Modular system understanding guide for targeted improvements

---

## Introduction

This document provides a functional area breakdown of the TARSy system, enabling engineers to understand and work on specific parts of the system without needing to comprehend the entire codebase. Each section focuses on a distinct functional area with clear boundaries and responsibilities.

**How to Use This Document:**
- Start with the [High-Level System Overview](#high-level-system-overview) for context
- Jump to specific functional areas based on what you're working on
- Each area includes core concepts, key components, and integration points
- Cross-references help you understand dependencies between areas

---

## High-Level System Overview

TARSy is an AI-powered incident analysis system built on a Go/Python split architecture. The Go orchestrator manages the entire lifecycle of alert processing -- from API submission through chain execution to WebSocket streaming. The Python LLM service is a stateless gRPC proxy for LLM provider interactions.

**Core Flow**: Alert -> Queue -> Chain Selection -> Sequential Stage Execution -> Analysis & Recommendations -> Dashboard Display

---

## Table of Contents

### Foundation Layer
- [1. Alert Processing & Orchestration](#1-alert-processing--orchestration)
  - [Background Processing & Concurrency Management](#background-processing--concurrency-management)
- [2. Configuration Management](#2-configuration-management)

### Core Processing Pipeline
- [3. Chain Management & Execution](#3-chain-management--execution)
- [4. Agent Architecture, Controllers & Orchestration](#4-agent-architecture-controllers--orchestration)
- [5. MCP Integration & Tool Management](#5-mcp-integration--tool-management)
- [6. LLM Integration & Multi-Provider Support](#6-llm-integration--multi-provider-support)

### Event Capture & Observability
- [7. Event System & Real-time Streaming](#7-event-system--real-time-streaming)
  - [WebSocket & Cross-Pod Communication](#websocket--cross-pod-communication)
- [8. History & Audit Trail](#8-history--audit-trail)
- [9. Follow-up Chat](#9-follow-up-chat)
- [10. Dashboard & Real-time Monitoring](#10-dashboard--real-time-monitoring)
- [11. Slack Notifications](#11-slack-notifications)

### Learning & Feedback
- [12. Investigation Memory](#12-investigation-memory)

### Cross-Cutting Concerns
- [13. Security & Data Protection](#13-security--data-protection)
- [14. Authentication & Access Control](#14-authentication--access-control)
- [15. Trace / Observability API](#15-trace--observability-api)
- [16. Cleanup & Retention](#16-cleanup--retention)
- [17. Prometheus Metrics](#17-prometheus-metrics)

---

## Functional Areas

### 1. Alert Processing & Orchestration
**Purpose**: Core workflow management from alert receipt to completion
**Key Responsibility**: Coordinating the entire alert processing pipeline

TARSy accepts alerts from any monitoring system through a REST API, validates them, and coordinates the entire processing workflow. The system uses async background processing with database-backed concurrency control across multiple replicas.

#### Core Alert Flow

```mermaid
sequenceDiagram
    participant Client as Monitoring System
    participant API as Go API Server
    participant DB as PostgreSQL
    participant Worker as Worker Pool
    participant Exec as SessionExecutor

    Client->>API: POST /api/v1/alerts (JSON)
    API->>API: Validate + resolve chain
    API->>DB: Create session (status=PENDING)
    API-->>Client: 200 OK (session_id, status: pending)

    Note over Worker: Background workers poll for capacity
    Worker->>DB: Claim next PENDING (FOR UPDATE SKIP LOCKED)
    DB-->>Worker: Session claimed
    Worker->>Exec: Execute(session)
    Exec->>Exec: Resolve chain, download runbook
    Exec->>Exec: Execute stages sequentially
    Exec-->>DB: Update session (completed/failed)
```

#### Key Components

**Main Entry Point**: `cmd/tarsy/main.go`
- Application startup, flag parsing, service wiring
- Initializes database, gRPC client, MCP health monitor, worker pool
- Serves dashboard static files via `-dashboard-dir` flag

**Alert API Handler**: `pkg/api/handler_alert.go`
- `POST /api/v1/alerts` with validation
- Queue size check (rejects with HTTP 429 when full)
- Alert data masking before database storage

**Alert Service**: `pkg/services/alert_service.go`
- `SubmitAlert()` -- creates session with chain resolved from alert type
- Validates runbook URL (domain allowlist)
- Applies alert data masking

**Alert Model** (submitted via API):
```json
{
  "alert_type": "kubernetes",
  "data": { "namespace": "production", "pod": "app-1" },
  "runbook": "https://github.com/org/repo/blob/main/runbooks/k8s.md",
  "mcp_selection": { "servers": [{ "name": "kubernetes-server" }] },
  "slack_message_fingerprint": "alert-12345"
}
```

#### Background Processing & Concurrency Management

**Global Alert Queue System**:

TARSy uses a database-backed global queue to manage alert processing across all pods in multi-replica deployments. PostgreSQL `FOR UPDATE SKIP LOCKED` ensures atomic session claiming without conflicts.

**Architecture Components**:

1. **Database-Backed Queue**: Sessions created in `PENDING` state, stored in PostgreSQL
2. **Worker Pool** (`pkg/queue/`): Configurable number of worker goroutines per replica
3. **Atomic Claiming**: `FOR UPDATE SKIP LOCKED` prevents duplicate claims across pods
4. **Global Concurrency Limit**: `max_concurrent_sessions` enforces system-wide active session limit
5. **Orphan Detection**: Periodic scan for stuck sessions with stale heartbeats

**Configuration** (`deploy/config/tarsy.yaml`):
```yaml
queue:
  worker_count: 5
  max_concurrent_sessions: 5
  poll_interval: 1s
  poll_interval_jitter: 500ms
  session_timeout: 15m
  orphan_detection_interval: 5m
  orphan_threshold: 5m
```

**Worker Implementation**: `pkg/queue/worker.go`
- Each worker runs a poll loop checking for available capacity
- Claims sessions atomically, dispatches to `SessionExecutor`
- Handles Slack start/terminal notifications
- Updates session status using `context.Background()` for post-cancellation reliability

**Session Executor**: `pkg/queue/executor.go`
- `RealSessionExecutor.Execute()` orchestrates the full chain lifecycle
- Resolves chain config, downloads runbook, iterates stages
- Extracts final analysis, runs executive summary as a typed `exec_summary` stage via SingleShotController (fail-open)
- Maps context errors to session status (timed_out / cancelled)

**Key Implementation Files**:
- `pkg/queue/worker.go` -- Worker poll loop and session lifecycle
- `pkg/queue/pool.go` -- WorkerPool management and cancellation
- `pkg/queue/executor.go` -- RealSessionExecutor and shared helpers
- `pkg/queue/chat_executor.go` -- ChatMessageExecutor for follow-up chat
- `pkg/services/alert_service.go` -- Alert submission and validation
- `pkg/api/handler_alert.go` -- HTTP handler with queue size check

---

### 2. Configuration Management
**Purpose**: System configuration and extensibility
**Key Responsibility**: Flexible system configuration without code changes

TARSy supports both built-in components and YAML-based configuration, enabling system extension without code changes. Configuration defines agents, chains, MCP servers, LLM providers, and their relationships.

#### Configuration Architecture

```mermaid
graph TB
    ENV[".env file<br/>API keys, secrets"] --> Loader
    YAML["tarsy.yaml<br/>Main config"] --> Loader[Config Loader]
    LLMYaml["llm-providers.yaml<br/>Optional"] --> Loader
    SkillFiles["skills/*<br/>Domain knowledge"] --> Loader
    BuiltIn["Built-in Defaults<br/>builtin.go"] --> Registries

    Loader --> Validation[Validation]
    Validation --> Registries[Component Registries]

    Registries --> AgentReg[AgentRegistry]
    Registries --> ChainReg[ChainRegistry]
    Registries --> MCPReg[MCPServerRegistry]
    Registries --> LLMReg[LLMProviderRegistry]
    Registries --> SkillReg[SkillRegistry]
```

#### Key Configuration Types

**Built-in Configuration**: `pkg/config/builtin.go`
- Single source of truth for built-in agents, MCP servers, chains, and LLM providers
- Data structures only, no circular imports

**YAML Configuration**: `deploy/config/tarsy.yaml` (see `tarsy.yaml.example`)
- **Go template variables**: `{{.VARIABLE_NAME}}` resolved from environment
- **Agent definitions**: Custom agents with MCP servers, instructions, and skill scoping
- **Chain definitions**: Multi-stage workflows with alert type mappings
- **MCP server configurations**: Custom tool servers with transport, masking, summarization
- **Skill definitions**: `skills/*/SKILL.md` (directory layout) or `skills/*` (flat file layout for Kubernetes ConfigMap mounts) with YAML frontmatter (name, description) and Markdown body — loaded at startup, available on-demand via `load_skill` tool
- **Override support**: YAML definitions override built-in components with same name/ID

**LLM Provider Configuration**: `deploy/config/llm-providers.yaml` (optional)
- Override built-in provider models, add custom proxy configurations
- Per-provider content truncation controls (`max_tool_result_tokens`)
- Native tools for Gemini (google_search, code_execution, url_context)

#### Configuration Registries

| Registry | Package | Lookup | Key Fields |
|----------|---------|--------|------------|
| `AgentRegistry` | `pkg/config/` | `Get(name)` | MCPServers, CustomInstructions, Type, LLMBackend, MaxIterations, Orchestrator, Skills, RequiredSkills |
| `ChainRegistry` | `pkg/config/` | `Get(id)`, `GetByAlertType(type)` | AlertTypes, Stages[], Chat, LLMProvider, MCPServers, SubAgents |
| `MCPServerRegistry` | `pkg/config/` | `Get(id)` | Transport, Instructions, DataMasking, Summarization |
| `LLMProviderRegistry` | `pkg/config/` | `Get(name)`, `GetAll()` | Type, Model, APIKeyEnv, BaseURL, NativeTools |
| `SkillRegistry` | `pkg/config/` | `Get(name)`, `GetAll()`, `Has(name)`, `Names()` | Name, Description, Body — loaded from `skills/` at startup (directory or flat file layout) |
| `SubAgentRegistry` | `pkg/config/` | `GetAll()`, `Get(name)` | Available sub-agents (agents with description), filtered by `sub_agents` override |

#### Configuration Loading Process

**Config Loader**: `pkg/config/loader.go`
1. Load and parse YAML files with Go template variable resolution
2. Merge with built-in defaults (YAML takes precedence)
3. Validate references (MCP server references exist, LLM provider types valid)
4. Build registries for runtime lookup

**Config Validator**: `pkg/config/validator.go`
- Validates chain stage references, MCP server existence, runbook domains
- Validates fallback provider entries: provider exists, backend valid, credentials set (fail-fast at startup)
- Validates skill references: agent `skills` allowlist entries exist in SkillRegistry, `required_skills` exist in SkillRegistry (validated independently of `skills` allowlist)
- Startup-time validation prevents runtime failures

**Key Implementation Files**:
- `pkg/config/loader.go` -- YAML loading, template resolution, merging
- `pkg/config/builtin.go` -- Built-in agents, MCP servers, chains, LLM providers
- `pkg/config/validator.go` -- Configuration validation
- `pkg/config/system.go` -- System config types (GitHub, Runbook, Slack, Retention)
- `pkg/config/enums.go` -- AgentType (`exec_summary`, `action`, `synthesis`, `scoring`), LLMBackend, LLMProviderType, SuccessPolicy, TransportType
- `pkg/config/skill.go` -- SkillConfig, SkillRegistry (thread-safe in-memory store)
- `pkg/config/skill_loader.go` -- LoadSkills(), SKILL.md frontmatter parsing (directory and flat file layouts)
- `pkg/config/sub_agent_registry.go` -- SubAgentRegistry for orchestrator agent discovery

---

### 3. Chain Management & Execution
**Purpose**: Chain definition, selection, and sequential stage execution
**Key Responsibility**: Managing multi-stage agent workflows with parallel execution support

Chains enable multi-stage workflows where specialized agents build upon each other's work. Each chain maps to one or more alert types and consists of sequential stages. Stages can run multiple agents in parallel, with automatic synthesis of results.

#### Chain Architecture

```mermaid
sequenceDiagram
    participant E as SessionExecutor
    participant CR as ChainRegistry
    participant A1 as Stage 1 Agent
    participant A2 as Stage 2 Agent
    participant S as SynthesisAgent

    E->>CR: GetByAlertType("kubernetes")
    CR-->>E: Chain definition
    E->>E: Initialize context

    loop For each stage
        E->>A1: executeAgent(context)
        A1-->>E: Stage 1 result
        E->>E: Build stage context for next stage
        E->>A2: executeAgent(context + previous results)
        A2-->>E: Stage 2 result
    end

    Note over E: If parallel stage with >1 agent
    E->>S: Synthesize parallel results
    S-->>E: Unified analysis

    E->>E: Extract final analysis
    E->>E: Execute exec_summary stage (SingleShotController)
```

#### Key Components

**Chain Registry**: `pkg/config/` (built via config loader)
- O(1) chain lookup by alert type
- YAML chains override built-in chains for same alert type
- Built-in + YAML merging with YAML taking precedence

**Chain Execution**: `pkg/queue/executor.go`
- `executeStage()` -- unified handler for all stages (single or multi-agent)
- `executeAgent()` -- per-agent lifecycle (DB record, config resolution, MCP creation, agent execution)
- `executeSynthesisStage()` -- automatic synthesis after parallel stages with >1 agent
- `executeExecSummaryStage()` -- executive summary as a typed `exec_summary` stage via SingleShotController
- `buildConfigs()` / `buildMultiAgentConfigs()` / `buildReplicaConfigs()` -- execution config building

#### Parallel Stage Execution

All stages use the same goroutine + WaitGroup + channel machinery regardless of agent count. A single-agent stage is N=1 -- not a special case.

**Multi-Agent Parallelism** (different agents run concurrently):
```yaml
stages:
  - name: "investigation"
    agents:
      - name: "KubernetesAgent"
        llm_provider: "gemini-2.5-flash"
        llm_backend: "google-native"
      - name: "performance-agent"
        llm_provider: "gemini-2.5-pro"
        llm_backend: "langchain"
    synthesis:
      llm_backend: "google-native"
      llm_provider: "gemini-3.1-pro"
```

**Replica Parallelism** (same agent runs multiple times):
```yaml
stages:
  - name: "analysis"
    agents:
      - name: "KubernetesAgent"
    replicas: 3
```

**Key parallel execution features**:
- **Automatic synthesis**: SynthesisAgent synthesizes results after stages with >1 agent
- **Success policies**: `all` (strict) or `any` (resilient, default) success requirements
- **Per-agent configuration**: Each parallel agent can specify its own LLM provider and LLM backend
- **Synthesis replaces investigation**: For downstream context, the synthesis result replaces raw per-agent results

#### Stage Context & Data Flow

**Chain Context Builder**: `pkg/agent/context/stage_context.go`
```go
type StageResult struct {
    StageName     string
    FinalAnalysis string
}

func BuildStageContext(stages []StageResult) string
```

Each stage sees all previous stage results formatted with `<!-- CHAIN_CONTEXT_START/END -->` markers. Context flows in-memory through the chain loop -- no additional DB query needed.

**Design principle**: No stored output fields on Stage or AgentExecution. Context is built lazily via `BuildStageContext()` when the next stage needs it.

#### Per-Stage LLM Provider Configuration

Chains support flexible LLM provider configuration with a resolution hierarchy:

1. **Stage-level `llm_provider`** (highest priority for stages)
2. **Chain-level `llm_provider`** (default for all stages in the chain)
3. **Global `defaults.llm_provider`** (system-wide default)

Non-stage components (executive summary, follow-up chat) use chain-level provider if defined, otherwise global default.

**Fallback providers** are resolved with the following precedence (highest to lowest): agent-level → stage-level → chain-level → `defaults.fallback_providers`. The first non-nil list wins (an explicit empty list clears inherited values). See [ADR-0003](adr/0003-llm-provider-fallback.md).

---

### 4. Agent Architecture, Controllers & Orchestration
**Purpose**: Agent framework, controller system, and dynamic orchestration
**Key Responsibility**: Agent behavior, iteration patterns, and LLM-driven sub-agent dispatch

Agents are specialized AI-powered components that analyze alerts using domain expertise and configurable iteration controllers. The system supports both built-in agents (KubernetesAgent, ChatAgent, SynthesisAgent, ExecSummaryAgent, ScoringAgent, WebResearcher, CodeExecutor, GeneralWorker) and YAML-configured agents.

Agent behavior is governed by two orthogonal configuration axes:

- **`AgentType`** (`""` | `"synthesis"` | `"exec_summary"` | `"action"` | `"scoring"`) — determines which controller runs the agent
- **`LLMBackend`** (`"google-native"` | `"langchain"`) — determines which Python SDK path handles LLM calls

#### Agent Framework Architecture

```mermaid
graph TB
    subgraph agentTypes [Agent Types]
        BA["BaseAgent<br/>(delegates to Controller)"]
        KA["KubernetesAgent (built-in)"]
        SA["SynthesisAgent (built-in)"]
        CA["ChatAgent (built-in)"]
        WR["WebResearcher (built-in)"]
        CE["CodeExecutor (built-in)"]
        GW["GeneralWorker (built-in)"]
        ScA["ScoringAgent (built-in)"]
        YA["YAML-configured agents"]
    end

    subgraph controllers [Controllers]
        FC["IteratingController<br/>(multi-turn with tools)"]
        SC["SingleShotController<br/>(one LLM call, no tools)"]
        SCC["ScoringController<br/>(2-turn LLM conversation)"]
    end

    subgraph backends [LLM Backends]
        GN["google-native<br/>(Gemini SDK)"]
        LC["langchain<br/>(Multi-provider)"]
    end

    BA --> KA
    BA --> SA
    BA --> CA
    BA --> WR
    BA --> CE
    BA --> GW
    BA --> ScA
    BA --> YA

    KA -.-> FC
    CA -.-> FC
    WR -.-> FC
    CE -.-> FC
    GW -.-> FC
    SA -.-> SC
    ScA -.-> SCC
    YA -.-> FC
    YA -.-> SC

    FC -.-> GN
    FC -.-> LC
    SC -.-> GN
    SC -.-> LC
    SCC -.-> GN
    SCC -.-> LC
```

#### Core Interfaces

**Agent Interface** (`pkg/agent/agent.go`):
```go
type Agent interface {
    Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

**Controller Interface** (`pkg/agent/base_agent.go`):
```go
type Controller interface {
    Run(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

**AgentType-to-controller mapping**:

| AgentType | Controller | Pattern | Use Case |
|-----------|-----------|---------|----------|
| `""` (default) | IteratingController | Iterating (multi-turn loop with tools) | Investigation agents (+ implicit orchestration when sub-agents present) |
| `"action"` | IteratingController | Iterating (multi-turn with tools) + safety prompt | Automated remediation based on findings |
| `"synthesis"` | SingleShotController | Single-shot (one LLM call, no tools) | Synthesis of parallel results |
| `"exec_summary"` | SingleShotController | Single-shot (one LLM call, no tools) | Executive summary generation |
| `"scoring"` | ScoringController | 2-turn LLM conversation (score + tool improvement report) | Session quality evaluation |

**LLMBackend determines the Python SDK path** (orthogonal to controller):

| LLMBackend | Python Provider | Use Case |
|------------|----------------|----------|
| `"google-native"` | GoogleNativeProvider | Gemini native SDK with thinking + function calling |
| `"langchain"` | LangChainProvider | Multi-provider (OpenAI, Anthropic, xAI, etc.) |

#### IteratingController — iterating (`pkg/agent/controller/iterating.go`)

Multi-turn iterating controller that loops: LLM call → tool execution → LLM call, until the agent produces a final answer or hits the iteration limit. Works with any `LLMBackend`:

1. Build initial messages (system prompt + user context)
2. List tools from MCP servers
3. **Iteration loop** (up to `MaxIterations`):
   - Call LLM with streaming AND tool bindings (structured function calling)
   - If **tool calls** in response: execute each tool, append results, continue
   - If **no tool calls**: this is the final answer -- create `final_analysis` event, return
4. If max iterations reached: `forceConclusion()` -- call LLM WITHOUT tools to force text-only response

#### SingleShotController — single-shot (`pkg/agent/controller/single_shot.go`)

Parameterized single-shot controller: one LLM call without tools, configured via `SingleShotConfig`. Used for synthesizing multi-agent investigation results and executive summary generation. Receives full investigation history via timeline events (thinking, tool calls, results, analyses).

#### ScoringController — 2-turn scoring (`pkg/agent/controller/scoring.go`)

Dedicated controller for session quality evaluation. Orchestrated by `ScoringExecutor` (`pkg/queue/scoring_executor.go`) which handles stage/execution lifecycle and result persistence.

```
ScoringExecutor.ScoreSession(sessionID)
  → Gather investigation context
    (stages: investigation + exec_summary + action; synthesis results attached via parent reference)
  → Create scoring Stage + AgentExecution records
  → ScoringController.Run()
    → Turn 1: Outcome-first evaluation (5 dimensions → holistic score) → total_score (0–100) + score_analysis + failure_tags
    → Turn 2: Tool improvement report (missing tools + existing tool improvements) → tool_improvement_report
  → Write to session_scores table
  → Memory extraction (if enabled): Reflector LLM call → create/reinforce/deprecate memories
  → Publish scoring stage status events
```

Turn 1 uses an outcome-first ceiling mechanic: conclusion quality determines the score range (60-100 correct / 35-59 partial / 0-34 wrong), then process quality places the score within that range. A failure vocabulary (defined as a Go slice in `pkg/agent/prompt/vocabulary.go`) is dynamically injected into the prompt; after scoring, `scanFailureTags()` extracts matched terms into a `failure_tags` JSON column. Turn 2 covers both missing MCP tools and improvements to existing tools.

Both turns persist LLM interactions and create streaming timeline events via `callLLMWithStreaming`. Auto-triggered by the worker after session completion (if chain scoring enabled via per-chain `scoring:` block or globally via `defaults.scoring`) or on-demand via `POST /api/v1/sessions/:id/score`. The `defaults.scoring` block also supports `llm_provider` and `llm_backend` overrides specific to scoring, inserted into the resolution hierarchy between global defaults and chain-level settings. See [ADR-0008: Session Scoring](adr/0008-session-scoring.md) and [ADR-0011: Scoring Framework Redesign](adr/0011-scoring-framework-redesign.md).

#### Implicit Orchestration (`pkg/agent/orchestrator/`)

Any agent that resolves a non-empty sub-agent catalog at runtime gains orchestration capabilities. The `IteratingController` includes two additions to the iteration loop for orchestration:

1. **Before each LLM call**: non-blocking drain of available sub-agent results
2. **At loop exit** (no tool calls): if sub-agents are pending, blocking wait instead of terminating

**Key components**:

- **`CompositeToolExecutor`** (`pkg/agent/orchestrator/composite_executor.go`) — wraps MCP tools + orchestration tools (`dispatch_agent`, `cancel_agent`, `list_agents`) into a single `ToolExecutor`. Routes by name: orchestration tools go to `SubAgentRunner`, everything else delegates to MCP.
- **`SubAgentRunner`** (`pkg/agent/orchestrator/runner.go`) — manages sub-agent goroutine lifecycle. Push-based result delivery via buffered channel. Sub-agent contexts derive from session-level context (survive across orchestrator iterations).
- **`SubAgentRegistry`** (`pkg/config/sub_agent_registry.go`) — agents with a `description` field, filtered by optional `sub_agents` override at chain/stage/agent level.

**Result flow**: `dispatch_agent` returns immediately → sub-agent runs in goroutine → result sent to channel → controller drains before next LLM call → injected as user-role message (injection format is internal to `FormatSubAgentResult` and intentionally not disclosed in the orchestrator prompt).

**DB model**: Sub-agents create real `AgentExecution` records with `parent_execution_id` linking to the parent agent, plus a `task` field for the dispatch description.

**Built-in sub-agent-eligible agents**: WebResearcher (google_search + url_context), CodeExecutor (code_execution), GeneralWorker (pure reasoning).

**Chat orchestrator**: `ChatConfig.SubAgents` enables chat agents to gain orchestration. Resolution follows: `chat.sub_agents` > `chain.sub_agents` > none.

**For detailed design**: See [ADR-0002: Orchestrator Agent](adr/0002-orchestrator-impl.md) (runtime mechanics) and [ADR-0015: Implicit Orchestrator](adr/0015-implicit-orchestrator.md) (trigger, prompt injection, stage-level skills)

#### Instruction Composition

**Tiered system** (`pkg/agent/prompt/builder.go`, `pkg/agent/prompt/instructions.go`, `pkg/agent/prompt/skills.go`):
```
Tier 1:   General SRE Instructions        (hardcoded)
Tier 2:   MCP Server Instructions          (per configured server)
Tier 2.5: Required Skill Content           (from required_skills — injected bodies)
Tier 2.6: On-Demand Skill Catalog          (names + descriptions, with load_skill tool)
Tier 3:   Agent Custom Instructions        (from custom_instructions)
Tier 4:   Lessons from Past Investigations (auto-injected memories, investigation sessions only)
```

**Agent Skills** (`pkg/agent/skill/tool_executor.go`) provide reusable domain knowledge. Required skills are injected as content (Tier 2.5); on-demand skills appear as a catalog with a `load_skill` tool (Tier 2.6). Skill resolution happens in `config_resolver.go` — the prompt builder only formats pre-resolved data from `ResolvedAgentConfig.RequiredSkillContent` and `ResolvedAgentConfig.OnDemandSkills`. `SkillToolExecutor` wraps the inner tool executor and intercepts `load_skill` calls, reading from the in-memory `SkillRegistry`. See [ADR-0012: Agent Skills](adr/0012-agent-skills.md).

When an agent has a non-empty **sub-agent catalog** (resolved via `sub_agents` configuration), the prompt builder auto-injects additional layers after the tiers:
```
System prompt = Tier 1-3 (above)
              + Orchestrator behavioral strategy (auto-injected)
              + Sub-agent catalog
              + Result delivery mechanics
              + Task focus
```

For **action agents** (`type: action`), the prompt builder auto-injects a safety-focused behavioral layer:
```text
System prompt = Tier 1-3 (above)
              + Action behavioral instructions (auto-injected safety preamble)
              + Action task focus
```

The safety preamble enforces: require hard evidence before acting, focus on evaluating upstream analysis, prefer inaction over incorrect action, explain reasoning before executing tools, and preserve the investigation report amended with an actions section. See [ADR-0007: Automated Actions](adr/0007-automated-actions.md) for full design.

This auto-injection pattern means custom agents with sub-agents or `type: action` receive their respective behavioral guidance without duplicating it in `custom_instructions`. The `custom_instructions` field remains purely for domain-specific context.

**PromptBuilder** methods:
- `BuildFunctionCallingMessages()` -- system + user messages for investigation
- `BuildSynthesisMessages()` -- system + user for synthesis
- `BuildForcedConclusionPrompt()` -- force answer at max iterations
- `ComposeInstructions()` / `ComposeChatInstructions()` -- instruction composition
- `BuildMCPSummarizationSystemPrompt()` / `BuildMCPSummarizationUserPrompt()` -- tool result summarization
- `BuildExecutiveSummarySystemPrompt()` / `BuildExecutiveSummaryUserPrompt()` -- executive summary

#### Forced Conclusion

At max iterations, `forceConclusion()` makes one extra LLM call without tools, asking for the best conclusion with available data. Every investigation produces actionable output.

#### Provider Fallback

All LLM call sites (iterating loop, forced conclusion, single-shot) support automatic fallback to alternative providers when the primary fails. A shared `callLLMWithFallback` helper wraps the streaming LLM call with error-code-aware trigger logic:

- **Immediate triggers**: `max_retries` (Python exhausted 3 retries), `credentials` (guaranteed failure)
- **Consecutive failure triggers**: `provider_error`, `invalid_request`, `partial_stream_error` (after 2 consecutive failures)

`FallbackState` tracks the original provider, current fallback index, attempted providers, and consecutive error counters. When fallback triggers, the controller selects the next untried provider from the configured fallback list, records a `provider_fallback` timeline event, updates execution metadata (`original_llm_provider`, `original_llm_backend`), and continues with the new provider. Fallback sticks for the rest of the execution; new executions reset to the primary.

**Adaptive timeouts** reduce time wasted on unresponsive providers. Implemented in `collectStreamWithCallback`: initial response timeout (120s default), stall timeout (60s default), and max call timeout (5m). Configurable per agent through the config hierarchy.

**For detailed design**: See [ADR-0003: LLM Provider Fallback](adr/0003-llm-provider-fallback.md)

**Key Implementation Files**:
- `pkg/agent/agent.go` -- Agent interface
- `pkg/agent/base_agent.go` -- BaseAgent with Controller delegation
- `pkg/agent/controller/iterating.go` -- IteratingController (includes sub-agent drain/wait for orchestrators)
- `pkg/agent/controller/single_shot.go` -- SingleShotController
- `pkg/agent/controller/scoring.go` -- ScoringController (2-turn LLM conversation for session scoring)
- `pkg/agent/controller/fallback.go` -- FallbackState, provider selection, callLLMWithFallback helper, error-code-aware triggers
- `pkg/agent/controller/streaming.go` -- Stream collection with adaptive timeouts (initial response, stall, max call)
- `pkg/agent/controller/tool_execution.go` -- Shared tool execution logic
- `pkg/agent/controller/summarize.go` -- Tool result summarization
- `pkg/agent/controller/timeline.go` -- Timeline event helpers
- `pkg/agent/orchestrator/` -- CompositeToolExecutor, SubAgentRunner, orchestration tool handlers
- `pkg/agent/skill/tool_executor.go` -- SkillToolExecutor (intercepts `load_skill`, delegates rest to inner executor)
- `pkg/memory/tool_executor.go` -- ToolExecutor (intercepts `recall_past_investigations`, delegates rest to inner executor)
- `pkg/agent/prompt/` -- PromptBuilder, templates, instructions (including orchestrator + sub-agent prompts)
- `pkg/agent/prompt/skills.go` -- formatRequiredSkill(), formatSkillCatalog() for Tier 2.5/2.6
- `pkg/agent/scoring_agent.go` -- ScoringAgent (delegates to ScoringController)
- `pkg/queue/scoring_executor.go` -- ScoringExecutor (scoring workflow + memory extraction orchestration)
- `pkg/agent/config_resolver.go` -- Hierarchical config resolution (AgentType, LLMBackend, provider, iterations, fallback providers, adaptive timeouts, stage-level skill merging)
- `pkg/agent/iteration.go` -- IterationState tracking
- `pkg/agent/context.go` -- ExecutionContext, ResolvedAgentConfig, ChatContext, SubAgentContext

---

### 5. MCP Integration & Tool Management
**Purpose**: External tool server integration and tool orchestration
**Key Responsibility**: Connecting agents to external systems and tools

MCP (Model Context Protocol) servers provide agents with external tools and system access. TARSy integrates with MCP servers using the official Go SDK v1.3.0, supporting stdio, HTTP, and SSE transports with automatic data masking and tool result summarization.

#### MCP Architecture

```mermaid
graph TB
    subgraph agentLayer [Agent Layer]
        Controller[IteratingController]
    end

    subgraph mcpLayer [MCP Integration]
        Executor["ToolExecutor<br/>(agent.ToolExecutor)"]
        Client["Client<br/>(MCP Go SDK)"]
        Factory[ClientFactory]
        Health[HealthMonitor]
        Masking[MaskingService]
    end

    subgraph servers [MCP Servers]
        K8s["kubernetes-server (stdio)"]
        HTTP["HTTP MCP servers"]
        SSE["SSE MCP servers"]
    end

    Controller --> Executor
    Executor --> Client
    Factory --> Client
    Factory --> Executor
    Executor --> Masking
    Health --> Client

    Client --> K8s
    Client --> HTTP
    Client --> SSE
```

#### Package Layout (`pkg/mcp/`)

| File | Purpose |
|------|---------|
| `client.go` | Client -- MCP SDK session manager, per-server mutex |
| `client_factory.go` | ClientFactory -- per-session client creation |
| `executor.go` | ToolExecutor -- implements `agent.ToolExecutor` |
| `params.go` | ActionInput parameter parsing (JSON/YAML/key-value/raw cascade) |
| `router.go` | Tool name normalization (`server__tool` to `server.tool`), splitting, validation |
| `recovery.go` | Error classification, retry with session recreation |
| `health.go` | HealthMonitor -- background health checks every 15s |
| `tokens.go` | Token estimation, two-tier truncation (storage 8K / summarization 100K) |
| `transport.go` | Transport creation from config (stdio/HTTP/SSE) |

#### Tool Lifecycle During Execution

```
Controller: LLM returns ToolCallChunk with "server__tool" + JSON args
  -> ToolExecutor.Execute(ToolCall)
    -> NormalizeToolName: server__tool -> server.tool
    -> SplitToolName: "server" + "tool"
    -> Validate server in allowed list, check tool filter
    -> ParseActionInput: JSON -> YAML -> key-value -> raw string cascade
    -> Client.CallTool(ctx, serverID, toolName, params)
      -> MCP SDK session.CallTool() with 90s timeout
      -> On error: classify -> retry once with session recreation (if transient)
    -> extractTextContent(result)
    -> MaskingService.MaskToolResult(content, serverID)
    -> Return ToolResult{Content, IsError}
```

#### Per-Agent-Execution Isolation

Every agent execution gets its own `Client` instance with independent MCP SDK sessions. Created via `createToolExecutor()` in the executor, torn down via `defer Close()`. No shared state between agents or stages.

For orchestrator agents, the MCP `ToolExecutor` is wrapped by `CompositeToolExecutor` (see [Agent Architecture](#4-agent-architecture-controllers--orchestration)), which adds orchestration tools while delegating MCP calls to the underlying executor.

#### Hierarchical MCP Server Configuration

Configuration hierarchy (lowest to highest priority):
1. **Agent level** -- defined in agent's `mcp_servers` config
2. **Chain level** -- optional `mcp_servers` in chain definition
3. **Stage level** -- optional `mcp_servers` in stage definition
4. **Parallel agent level** -- optional per parallel agent
5. **Chat level** -- optional in chat config
6. **Alert level** -- via API `mcp_selection` field (highest, replaces all)

#### Custom MCP Configuration (Per-Alert Override)

When an alert provides `mcp_selection`, it **replaces** the chain/agent's MCP server list entirely. Tool filtering within a server is additive restriction (empty list = all tools).

```json
{
  "mcp_selection": {
    "servers": [
      { "name": "kubernetes-server", "tools": ["kubectl_get_pods"] }
    ],
    "native_tools": { "google_search": true }
  }
}
```

**API Discovery**: `GET /api/v1/system/default-tools?alert_type=kubernetes` returns the default MCP tool configuration for a given alert type. `GET /api/v1/system/mcp-servers` returns all configured servers with their available tools and health status.

#### Health Monitoring

**HealthMonitor** (`pkg/mcp/health.go`): Dedicated long-lived MCP Client (not shared with sessions) that checks server health every 15s via `ListTools`. On failure: attempts session recreation, marks unhealthy, adds `SystemWarning`. On recovery: clears warning automatically.

**Startup validation**: All configured MCP servers are validated eagerly at startup. Failures are logged as warnings -- TARSy starts in a degraded state rather than refusing to start.

**Key Implementation Files**:
- `pkg/mcp/client.go` -- MCP Client wrapping Go SDK
- `pkg/mcp/executor.go` -- ToolExecutor implementing `agent.ToolExecutor`
- `pkg/mcp/client_factory.go` -- Per-session client creation
- `pkg/mcp/health.go` -- Background health monitoring
- `pkg/mcp/transport.go` -- Transport creation (stdio/HTTP/SSE)
- `pkg/mcp/params.go` -- Multi-format ActionInput parsing
- `pkg/mcp/router.go` -- Tool name routing and validation

---

### 6. LLM Integration & Multi-Provider Support
**Purpose**: AI/LLM provider abstraction and management
**Key Responsibility**: Unified LLM access across multiple providers via gRPC

TARSy uses a Go/Python split for LLM integration. The Go orchestrator communicates with the Python LLM service via gRPC, which routes to the appropriate provider backend.

#### LLM Architecture

```mermaid
graph TB
    subgraph goSide [Go Orchestrator]
        LLMClient["GRPCLLMClient<br/>(pkg/agent/llm_client.go)"]
        Config["LLMProviderRegistry"]
    end

    subgraph pythonSide [Python LLM Service]
        Servicer["gRPC Servicer<br/>(llm/servicer.py)"]
        Registry["Provider Registry"]
        GoogleNative["GoogleNativeProvider<br/>google-genai SDK"]
        LangChain["LangChainProvider<br/>LangChain ecosystem"]
    end

    subgraph providers [LLM APIs]
        Gemini[Google Gemini]
        OpenAI[OpenAI]
        Anthropic[Anthropic]
        XAI[xAI]
        VertexAI[Vertex AI]
    end

    LLMClient -->|"gRPC Generate(stream)"| Servicer
    Config --> LLMClient
    Servicer --> Registry
    Registry --> GoogleNative
    Registry --> LangChain

    GoogleNative --> Gemini
    LangChain --> OpenAI
    LangChain --> Anthropic
    LangChain --> XAI
    LangChain --> Gemini
    LangChain --> VertexAI
```

#### Go Side: LLMClient

**GRPCLLMClient** (`pkg/agent/llm_client.go`):
```go
type LLMClient interface {
    Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error)
    Close() error
}
```

Chunk types: `TextChunk`, `ThinkingChunk`, `ToolCallChunk`, `CodeExecutionChunk`, `UsageChunk`, `ErrorChunk`, `GroundingChunk`.

**GenerateInput** carries: `SessionID`, `ExecutionID`, `Messages`, `Config` (LLMProviderConfig), `Tools`, `Backend`.

#### Python Side: Provider Routing

```
Generate(request) -> backend = request.llm_config.backend || "google-native"
  -> registry.get(backend) -> provider.generate(request) -> stream chunks
```

**GoogleNativeProvider** (`llm/providers/google_native.py`):
- `google-genai` SDK with `generate_content_stream()`
- Native thinking with `ThinkingConfig(include_thoughts=True)`
- Thought signatures for multi-turn reasoning continuity
- Native function calling (structured tool calls)
- Per-execution content caching (1-hour TTL)

**LangChainProvider** (`llm/providers/langchain_provider.py`):
- Multi-provider: OpenAI (`ChatOpenAI`), Anthropic (`ChatAnthropic`), xAI (`ChatXAI`), Google (`ChatGoogleGenerativeAI`), VertexAI
- Model caching by `(provider, model, api_key_env)` tuple
- `bind_tools()` for structured function calling
- Streaming via `astream()` with thinking content extraction

#### Tool Name Encoding

Shared convention between Go and Python:
- **Canonical**: `server.tool` (Go backend, internal routing)
- **API**: `server__tool` (LLM APIs -- dots invalid in function names)
- Go: `NormalizeToolName()` in `pkg/mcp/router.go`
- Python: `tool_name_to_api()` / `tool_name_from_api()` in `llm/providers/tool_names.py`

#### gRPC Protocol (`proto/llm_service.proto`)

- **RPC**: `Generate(GenerateRequest) returns (stream GenerateResponse)`
- **LLMConfig**: `backend`, `provider`, `model`, `api_key_env`, `base_url`, `native_tools`, `max_tool_result_tokens`
- **GenerateRequest flags**: `clear_cache` (signals provider switch mid-execution — Google Native clears `_model_contents` cache to avoid stale thought signatures)
- **Response streaming**: `TextDelta`, `ThinkingDelta`, `ToolCallDelta`, `UsageInfo`, `ErrorInfo`, `CodeExecutionDelta`, `GroundingDelta`

#### Built-in LLM Providers

| Name | Type | Model | Context |
|------|------|-------|---------|
| `google-default` | google | gemini-3-flash-preview | 1M |
| `gemini-2.5-flash` | google | gemini-2.5-flash | 1M |
| `gemini-2.5-pro` | google | gemini-2.5-pro | 1M |
| `gemini-3.1-pro` | google | gemini-3.1-pro-preview | 1M |
| `openai-default` | openai | gpt-5.2 | 400K |
| `anthropic-default` | anthropic | claude-sonnet-4-6 | 1M (beta) |
| `xai-default` | xai | grok-4-1-fast-reasoning | 2M |
| `vertexai-default` | vertexai | claude-sonnet-4-6 | 1M (beta) |

Google providers include native tools (google_search, url_context enabled; code_execution disabled by default). Native tools are disabled when MCP tools are present.

**Key Implementation Files**:
- `pkg/agent/llm_client.go` -- GRPCLLMClient, GenerateInput, Chunk types
- `pkg/agent/llm_grpc.go` -- gRPC client implementation (includes `clear_cache` flag on provider switch)
- `proto/llm_service.proto` -- gRPC service definition
- `llm-service/llm/servicer.py` -- gRPC servicer with provider routing and `clear_cache` passthrough
- `llm-service/llm/providers/google_native.py` -- GoogleNativeProvider (handles `clear_cache` to invalidate `_model_contents` cache)
- `llm-service/llm/providers/langchain_provider.py` -- LangChainProvider
- `llm-service/llm/providers/tool_names.py` -- Tool name encoding

---

### 7. Event System & Real-time Streaming
**Purpose**: Event-driven real-time system coordination
**Key Responsibility**: Enabling loose coupling through typed events and cross-pod delivery

The event system powers real-time dashboard updates through PostgreSQL LISTEN/NOTIFY for cross-pod delivery, with WebSocket connections to browser clients.

#### Event Architecture

```mermaid
graph LR
    subgraph sources [Event Sources]
        Controller[Controllers]
        Executor[Executors]
    end

    subgraph eventSystem [Event System]
        Publisher[EventPublisher]
        DB[(PostgreSQL<br/>Events table)]
        Listener[NotifyListener]
    end

    subgraph delivery [Delivery]
        ConnMgr[ConnectionManager]
        WS["WebSocket Clients"]
    end

    Controller -->|timeline, stream| Publisher
    Executor -->|session, stage, chat| Publisher

    Publisher -->|INSERT + pg_notify| DB
    DB -->|LISTEN| Listener
    Listener --> ConnMgr
    ConnMgr --> WS
```

#### Key Components

**EventPublisher** (`pkg/events/publisher.go`):
```go
func (p *EventPublisher) Publish(ctx, sessionID, channel, payload) error        // Persistent (DB + NOTIFY)
func (p *EventPublisher) PublishTransient(ctx, channel, payload) error           // Transient (NOTIFY only)
func (p *EventPublisher) PublishStageStatus(ctx, sessionID, payload) error       // Stage lifecycle
```

The `agent.EventPublisher` interface (`pkg/agent/context.go`) exposes typed methods: `PublishTimelineCreated`, `PublishTimelineCompleted`, `PublishStreamChunk`, `PublishSessionStatus`, `PublishStageStatus`, `PublishReviewStatus`, `PublishChatCreated`, `PublishChatUserMessage`.

**Event Types**:
- **Persistent** (DB + NOTIFY): `timeline_event.created`, `timeline_event.completed`, `session.status`, `stage.status`, `execution.status`, `execution.progress`, `review.status`, `chat.created`, `chat.user_message`
- **Transient** (NOTIFY only): `stream.chunk` (LLM token deltas)

Timeline event payloads carry an `event_type` field that distinguishes the kind of event (e.g., `llm_response`, `llm_tool_call`, `final_analysis`, `provider_fallback`). See the [TimelineEvent schema](#8-history--audit-trail) for the full list.

All event payloads include `parent_execution_id` when present, enabling the dashboard to route sub-agent events without cross-referencing.

**Event Channels**:
- `sessions` -- global session lifecycle events
- `session:{session_id}` -- per-session detail events (including chat)

#### WebSocket & Cross-Pod Communication

**WebSocket Endpoint**: `pkg/api/handler_ws.go`
- Single connection per browser tab at `/api/v1/ws`
- Client actions: `subscribe`, `unsubscribe`, `catchup`, `ping`

**ConnectionManager** (`pkg/events/manager.go`):
- Tracks active WebSocket connections and channel subscriptions
- Broadcasts events to all subscribers of a channel

**NotifyListener** (`pkg/events/listener.go`):
- PostgreSQL `LISTEN` via `pgx.WaitForNotification`
- Dispatches notifications to registered handlers

**Cross-Pod Event Delivery**:
```
Event Published -> DB (INSERT + NOTIFY) -> All Backend Pods (LISTEN) -> WebSocket Clients
```

**Auto-catchup**: New channel subscriptions automatically receive prior events. On reconnect, clients send `catchup` with `last_event_id` for fine-grained replay. Server returns missed events (limit: 200). Overflow triggers `catchup.overflow` signaling the client to do a full REST reload.

**Cross-Pod Cancellation**: Uses a dedicated `cancellations` NOTIFY channel. Cancel handler sets DB status to `cancelling`, cancels locally, publishes session ID to the channel. All pods LISTEN and cancel the session context on the owning pod.

**Key Implementation Files**:
- `pkg/events/publisher.go` -- EventPublisher (persistent + transient)
- `pkg/events/manager.go` -- ConnectionManager (WebSocket routing)
- `pkg/events/listener.go` -- NotifyListener (PostgreSQL LISTEN)
- `pkg/api/handler_ws.go` -- WebSocket endpoint and protocol
- `pkg/services/event_service.go` -- Event persistence and cleanup

---

### 8. History & Audit Trail
**Purpose**: Complete processing history capture and timeline reconstruction
**Key Responsibility**: Providing comprehensive audit trails and operational visibility

The history system captures the complete lifecycle of alert processing through a five-layer data model, providing detailed audit trails for debugging, monitoring, and compliance.

#### Five-Layer Data Model

```
AlertSession (session metadata, status, alert data)
  +-- Stage (chain stage -- configuration + coordination)
  |     +-- AgentExecution (individual agent work within a stage)
  |           +-- Layer 1: TimelineEvent (UX timeline -- what the user sees)
  |           +-- Layer 2: Message (LLM conversation -- linear, no duplication)
  |           +-- Layer 3-4: LLMInteraction / MCPInteraction (trace/observability)
  +-- SessionReviewActivity (review workflow audit trail)
  +-- Event (WebSocket distribution -- transient)
  +-- Chat -> ChatUserMessage (follow-up chat)
```

#### Key Entity Fields

**AlertSession** (`ent/schema/alertsession.go`):
`id`, `alert_data`, `agent_type`, `alert_type`, `status` (pending/in_progress/cancelling/completed/failed/cancelled/timed_out), `chain_id`, `pod_id`, `final_analysis`, `executive_summary`, `mcp_selection`, `author`, `runbook_url`, `review_status` (needs_review/in_progress/reviewed, nullable — NULL while investigation active), `assignee`, `assigned_at`, `reviewed_at`, `quality_rating` (accurate/partially_accurate/inaccurate), `action_taken`, `investigation_feedback`, `deleted_at` (soft delete), timestamps

**Stage** (`ent/schema/stage.go`):
`id`, `session_id`, `stage_name`, `stage_index`, `stage_type` (investigation/synthesis/chat/exec_summary/scoring/action), `referenced_stage_id` (nullable FK — synthesis→investigation pairing), `expected_agent_count`, `parallel_type`, `success_policy`, `chat_id`, `chat_user_message_id`, `status`, `error_message`, timestamps

**AgentExecution** (`ent/schema/agentexecution.go`):
`id`, `stage_id`, `session_id`, `agent_name`, `agent_index`, `llm_backend`, `llm_provider`, `original_llm_provider` (nullable — set on fallback), `original_llm_backend` (nullable — set on fallback), `status`, `error_message`, `parent_execution_id` (nullable — links sub-agents to orchestrator), `task` (nullable — orchestrator dispatch description), timestamps

**TimelineEvent** (`ent/schema/timelineevent.go`):
`id`, `session_id`, `stage_id` (optional), `execution_id` (optional), `parent_execution_id` (nullable — for sub-agent event partitioning), `sequence_number`, `event_type` (llm_thinking/llm_response/llm_tool_call/mcp_tool_summary/error/user_question/executive_summary/final_analysis/code_execution/google_search_result/url_context_result/task_assigned/provider_fallback), `status` (streaming/completed/failed/cancelled/timed_out), `content`, `metadata` (JSON), timestamps. **GIN index** on `content` for full-text search across dashboard session list queries (see [ADR-0006](adr/0006-search-text.md)).

**Message** (`ent/schema/message.go`):
`id`, `session_id`, `stage_id`, `execution_id`, `sequence_number`, `role` (system/user/assistant/tool), `content`, `tool_calls` (JSON), `tool_call_id`, `tool_name`, timestamps

**SessionReviewActivity** (`ent/schema/sessionreviewactivity.go`):
`activity_id`, `session_id`, `actor`, `action` (claim/unclaim/complete/reopen/update_feedback), `from_status`, `to_status`, `quality_rating` (accurate/partially_accurate/inaccurate), `note` (action_taken snapshot on complete/update_feedback), `investigation_feedback`, `created_at`. Every review workflow transition is logged here for auditability. See [ADR-0009: Session Workflow](adr/0009-session-workflow.md).

#### Service Layer

| Service | File | Purpose |
|---------|------|---------|
| `SessionService` | `pkg/services/session_service.go` | Session CRUD, status updates, pagination, FTS search across timeline content |
| `StageService` | `pkg/services/stage_service.go` | Stage lifecycle, parallel status aggregation, fallback metadata updates |
| `TimelineService` | `pkg/services/timeline_service.go` | Timeline event CRUD, streaming updates |
| `MessageService` | `pkg/services/message_service.go` | LLM conversation message storage |
| `InteractionService` | `pkg/services/interaction_service.go` | LLM/MCP interaction recording |
| `ChatService` | `pkg/services/chat_service.go` | Chat and user message management |

#### REST API Endpoints

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `/api/v1/sessions` | Paginated session list with filtering |
| GET | `/api/v1/sessions/active` | Active + queued sessions |
| GET | `/api/v1/sessions/filter-options` | Distinct alert types and chain IDs |
| GET | `/api/v1/sessions/:id` | Session details |
| GET | `/api/v1/sessions/:id/summary` | Final analysis + executive summary |
| GET | `/api/v1/sessions/:id/status` | Lightweight polling status (id, status, final_analysis, executive_summary, error_message) |
| GET | `/api/v1/sessions/:id/timeline` | Timeline events ordered by sequence |
| POST | `/api/v1/sessions/:id/cancel` | Cancel running session or chat |
| GET | `/api/v1/sessions/:id/score` | Latest scoring result (total score, analysis, failure tags, tool improvement report) |
| POST | `/api/v1/sessions/:id/score` | Trigger on-demand re-scoring (202 Accepted, 409 if in-progress) |
| GET | `/api/v1/sessions/:id/memories` | Memories extracted from this session |
| GET | `/api/v1/sessions/:id/injected-memories` | Memories auto-injected into this session |
| GET | `/api/v1/memories` | List all memories (paginated, filterable) |
| GET | `/api/v1/memories/:id` | Single memory detail |
| PATCH | `/api/v1/memories/:id` | Edit memory (content, category, valence, deprecated) |
| DELETE | `/api/v1/memories/:id` | Delete memory |
| PATCH | `/api/v1/sessions/review` | Review workflow transition for one or more sessions (claim/unclaim/complete/reopen/update_feedback) |
| GET | `/api/v1/sessions/:id/review-activity` | Review activity audit log |
| GET | `/api/v1/sessions/triage/:group` | Per-group paginated triage view (investigating/needs_review/in_progress/reviewed) |
| GET | `/health` | Health check (DB, worker pool) |

---

### 9. Follow-up Chat
**Purpose**: Interactive investigation continuation after session completion
**Key Responsibility**: Enabling engineers to ask follow-up questions with full context and tool access

After an investigation reaches a terminal state, engineers can start follow-up chat conversations. The chat agent receives the full investigation timeline as context and has access to the same MCP tools.

#### Chat Architecture

```mermaid
graph TB
    subgraph chatFlow [Chat Flow]
        API["POST /sessions/:id/chat/messages"]
        ChatExec[ChatMessageExecutor]
        Agent["Agent + Controller"]
    end

    subgraph reuse [Infrastructure Reuse]
        Stage[Stage + AgentExecution records]
        Timeline[Timeline Events]
        Events[Event System]
        WS[WebSocket streaming]
    end

    API --> ChatExec
    ChatExec --> Agent
    Agent --> Stage
    Stage --> Timeline
    ChatExec --> Events
    Events --> WS
```

#### Key Components

**ChatMessageExecutor** (`pkg/queue/chat_executor.go`):
- Spawns one goroutine per message (no pool -- chats are rare, one-at-a-time per chat enforced)
- Resolves chain + chat agent config via `ResolveChatAgentConfig()`
- Creates Stage (type: `chat`) and AgentExecution records (reusing existing audit trail infrastructure)
- Builds context using `stage_type` filtering and `referenced_stage_id` for synthesis→investigation pairing (replaces name-based backward scanning)
- Runs `agent.Execute()` with same controllers as investigation

**Chat Service** (`pkg/services/chat_service.go`):
- `GetOrCreateChat()` -- one Chat per session
- Chat metadata, context snapshot, pod tracking

**Design principle**: Chat is a prompt concern, not a controller concern. The same Iterating and SingleShot controllers handle both investigation and chat -- the `ChatContext` on `ExecutionContext` triggers chat-specific prompting. Same iteration limits, same `forceConclusion()` at max iterations.

#### Lifecycle Constraints

- **One Chat per session**: enforced by schema uniqueness on `session_id`
- **Terminal sessions only**: available for completed/failed/timed_out sessions
- **One-at-a-time per chat**: new message while processing returns 409 Conflict
- **Chat enabled check**: `chain.Chat.Enabled` must be true

#### Configuration

```yaml
agent_chains:
  kubernetes:
    chat:
      enabled: true
      agent: "ChatAgent"
      llm_backend: "google-native"
      llm_provider: "google-default"
      mcp_servers: ["kubernetes-server"]
      max_iterations: 20
```

**REST Endpoints** (`pkg/api/handler_chat.go`):
- `POST /api/v1/sessions/:id/chat/messages` -- send message (202 Accepted, response via WebSocket)

---

### 10. Dashboard & Real-time Monitoring
**Purpose**: User interface and live system monitoring
**Key Responsibility**: Providing real-time visibility and historical analysis

TARSy provides a React SPA served statically by the Go backend, with real-time updates through WebSocket.

#### UI Architecture

**Technology**: React 19 + TypeScript + Vite 7 + MUI 7

**Pages**:

| Route | Page | Purpose |
|-------|------|---------|
| `/` | DashboardPage | Session list + Triage view (tabbed: Sessions \| Triage) with filters, pagination |
| `/sessions/:id` | SessionDetailPage | Session detail with conversation timeline, streaming, chat |
| `/sessions/:id/trace` | TracePage | Trace view with LLM/MCP interaction details |
| `/submit-alert` | SubmitAlertPage | Alert submission with MCP override selection |
| `/sessions/:id/scoring` | ScoringPage | Scoring reports (score analysis, failure tags, tool improvement report), re-score trigger |
| `/system` | SystemStatusPage | MCP server health, tools, system warnings |

#### Data Flow

- **REST + WebSocket**: REST for initial load and paginated data; WebSocket for live updates
- **Streaming pattern**: `timeline_event.created` -> `stream.chunk` (token deltas) -> `timeline_event.completed`
- **Event-notification pattern**: Trace page debounces WebSocket events and re-fetches via REST
- **Optimistic UI**: Chat injects temporary `user_question` items before server confirmation
- **Orchestrator sub-agents**: `parent_execution_id` on timeline events and WS payloads enables the dashboard to partition sub-agent events without cross-referencing. `SubAgentCard` components render inline in the orchestrator's timeline; trace view nests sub-agents as tabs within the orchestrator panel.
- **Provider fallback indicators**: `provider_fallback` timeline events render in the conversation timeline showing original → fallback provider and reason. Trace view shows original vs. active provider on executions where `original_llm_provider` is set (`ProviderFallbackIndicator` component).
- **Scoring flow**: Session list shows a color-coded `ScoreBadge` (green ≥80, yellow ≥60, red <60) from `latest_score` on each session item. Session detail page includes a score indicator linking to the dedicated `ScoringPage` (`/sessions/:id/scoring`). ScoringPage fetches the full scoring report via `GET /api/v1/sessions/:id/score` and supports on-demand re-scoring via `POST /api/v1/sessions/:id/score`. Real-time scoring progress is delivered through existing WebSocket `stage.status` events for the `scoring` stage type. See [ADR-0008: Session Scoring](adr/0008-session-scoring.md).
- **Triage view**: The dashboard has a "Triage" tab alongside the existing "Sessions" tab. Triage shows sessions grouped by review status (`investigating`, `needs_review`, `in_progress`, `reviewed`) with collapsible sections and action buttons (Claim, Complete, Reopen). Review transitions use `PATCH /api/v1/sessions/review` with optimistic UI. Real-time updates via `review.status` WebSocket events move sessions between groups. Filter bar supports assignee and alert type filtering. See [ADR-0009: Session Workflow](adr/0009-session-workflow.md).

#### Text Search

Two-phase search implementation (see [ADR-0006](adr/0006-search-text.md)):

- **Dashboard search** (server-side): The session list search extends to `timeline_events.content` via PostgreSQL full-text search (`to_tsvector/plainto_tsquery`) with a GIN index. Sessions matched via timeline content carry a `matched_in_content` flag shown as an indicator in the session list.
- **In-session search** (client-side): A search bar on `SessionDetailPage` (terminated sessions only) performs substring matching on loaded `FlowItem[]` content, highlights matches via `rehypeSearchHighlight`, auto-expands collapsed stages containing matches, and provides next/previous match navigation.

#### State Management

No global state library. State is local `useState` per page + two React Contexts:
- **AuthContext**: User identity, auth availability
- **VersionContext**: Polls `/health` and `index.html` for version mismatch detection

Filter state, pagination, and sort preferences persist in `localStorage`.

#### Static Serving from Go

`SetDashboardDir(dir)` on the HTTP server registers dashboard routes after API routes:
- `/assets/*` -- `Cache-Control: public, max-age=31536000, immutable` (Vite content-hashed filenames)
- All other non-API routes -- SPA fallback to `index.html` with `Cache-Control: no-cache`
- API routes registered first take priority over the SPA fallback

**Key Implementation Files**:
- `web/dashboard/src/pages/` -- Page components (including `ScoringPage.tsx` for scoring reports)
- `web/dashboard/src/components/` -- UI components (timeline, trace, chat, session, etc.)
- `web/dashboard/src/components/timeline/SubAgentCard.tsx` -- Collapsible inline sub-agent card with streaming
- `web/dashboard/src/components/session/SessionSearchBar.tsx` -- In-session search bar with match navigation
- `web/dashboard/src/utils/rehypeSearchHighlight.ts` -- Rehype plugin for search term highlighting in markdown
- `web/dashboard/src/services/websocketService.ts` -- WebSocket with reconnect + catchup
- `web/dashboard/src/services/api.ts` -- Axios-based API client with retry
- `web/dashboard/src/components/dashboard/TriageGroupedList.tsx` -- Triage grouped list with collapsible sections
- `web/dashboard/src/components/dashboard/TriageFilterBar.tsx` -- Triage filter bar with assignee filter
- `web/dashboard/src/hooks/` -- useChatState, useVersionMonitor, useAdvancedAutoScroll
- `web/dashboard/src/contexts/` -- AuthContext, VersionContext

---

### 11. Slack Notifications
**Purpose**: External notification integration for alert processing outcomes
**Key Responsibility**: Delivering analysis results to Slack channels

TARSy provides optional Slack integration for automatic notifications at two lifecycle points: session start (for Slack-originated alerts) and terminal status (completed, failed, timed out, cancelled). Supports threaded replies via fingerprint correlation.

**For complete setup guide**: See [Slack Integration Documentation](slack-integration.md)

**Package**: `pkg/slack/`

| File | Purpose |
|------|---------|
| `service.go` | SlackService -- orchestrator, nil-safe, fail-open |
| `client.go` | SlackClient -- thin wrapper around slack-go SDK |
| `message.go` | Block Kit message builders |

**Key design patterns**:
- **Nil-safe service**: `NewService` returns nil when unconfigured; all methods are nil-receiver safe
- **Fail-open**: Slack API failures are logged but never block session processing
- **ThreadTS caching**: Start notification resolves fingerprint -> `thread_ts`; terminal notification reuses it

---

### 12. Investigation Memory
**Purpose**: Cross-session learning through memory extraction, storage, and retrieval
**Key Responsibility**: Enabling investigations to benefit from past learnings

TARSy learns from past investigations through a memory system that extracts discrete learnings after each scored investigation and injects relevant memories into future sessions. The system uses pgvector cosine similarity for semantic-first retrieval within project boundaries.

**For detailed design**: See [ADR-0014: Investigation Memory](adr/0014-investigation-memory.md)

#### Memory Architecture

```mermaid
graph TB
    subgraph extraction [Memory Extraction — after scoring]
        ScoringExec[ScoringExecutor]
        Reflector[Reflector LLM Call]
        Embedder[Embedder — direct HTTP]
    end

    subgraph storage [Storage]
        DB[(PostgreSQL + pgvector)]
    end

    subgraph retrieval [Memory Retrieval — at investigation start]
        SessionExec[SessionExecutor]
        Retriever[MemoryRetriever]
        PromptBuilder[PromptBuilder Tier 4]
        ToolExec[ToolExecutor]
    end

    subgraph refinement [Human Refinement — on review]
        ReviewHandler[Review Handler]
        FeedbackJob[Feedback Reflector Job]
    end

    ScoringExec --> Reflector
    Reflector -->|create/reinforce/deprecate| DB
    Reflector --> Embedder
    Embedder -->|embedding API| DB

    SessionExec --> Retriever
    Retriever -->|hybrid search: vector + keyword RRF| DB
    Retriever --> PromptBuilder
    ToolExec -->|recall_past_investigations| DB
    ToolExec -->|search_past_sessions| DB

    ReviewHandler -->|confidence adjustment| DB
    FeedbackJob -->|background Reflector| DB
```

#### Key Components

**Memory Service** (`pkg/memory/service.go`):
- CRUD operations for investigation memories
- `FindSimilarWithBoosts()` — hybrid search (vector + keyword with RRF), similarity threshold (0.7), confidence weighting, temporal decay (90-day half-life)
- `FindSimilar()` — pgvector similarity search for Reflector dedup context (broader threshold)
- `SearchSessions()` — full-text search on `alert_sessions.alert_data` via tsvector/GIN for entity-level recall
- `ApplyReflectorActions()` — processes create/reinforce/deprecate actions from the Reflector

**Embedder** (`pkg/memory/embedder.go`):
- Direct HTTP calls to embedding provider API (Google or OpenAI)
- Dispatches to correct API format based on configured `provider`
- Supports `EmbeddingTaskDocument` (storage) and `EmbeddingTaskQuery` (search)

**Reflector** (`pkg/memory/reflector.go`):
- Builds a separate LLM conversation with "memory extraction specialist" role
- Receives investigation context (reused from scoring), scoring results, existing memories
- Outputs structured JSON: create/reinforce/deprecate actions

**Memory Retriever** (`pkg/memory/retriever.go`):
- Hybrid retrieval: vector candidates (pgvector HNSW) + keyword candidates (tsvector/GIN) fused via Reciprocal Rank Fusion (RRF, k=60)
- Similarity threshold (0.7) — the quality gate; every result must have a vector match above this floor
- Vector-required: keyword matches only boost vector-matched results (LEFT JOIN, not FULL OUTER) — keyword-only matches excluded to prevent common-term noise
- Confidence weighting: `(0.7 + 0.3 × confidence)` — human-reviewed memories rank higher
- Temporal decay: `EXP(-0.0077 × age_in_days)` — 90-day half-life, reinforcement resets the clock
- Project is the hard security filter; minimum injection score (0.01) as conservative floor

**Memory Tool Executor** (`pkg/memory/tool_executor.go`):
- Wraps inner executor (same pattern as `SkillToolExecutor`)
- Intercepts `recall_past_investigations` (hybrid memory search) and `search_past_sessions` (entity-level session search with LLM summarization)
- Excludes already-injected memory IDs from recall results; excludes current session from session search results
- Memory tools only registered when the memory service is available (nil-safe)

**Feedback Executor** (`pkg/queue/feedback_executor.go`):
- Background job triggered when review includes `investigation_feedback` text
- Runs Reflector variant focused on human feedback

#### Data Model

The `InvestigationMemory` entity stores discrete learnings with:
- **Content + embedding** — the learning text and its pgvector embedding for similarity search
- **Category** (`semantic`/`episodic`/`procedural`) and **valence** (`positive`/`negative`/`neutral`)
- **Confidence** (0-1) — flat initial 0.7 (Reflector is the quality gate), refined by human review and reinforcement
- **Scope metadata** — `alert_type`, `chain_id` (soft boosts), `project` (hard filter)
- **Lifecycle** — `deprecated` flag, `seen_count`, `last_seen_at`

Two Ent edges connect memories to sessions:
- `source_session` — the investigation that produced the memory (one-to-many)
- `injected_into_sessions` — sessions where the memory was auto-injected (many-to-many)

#### Memory Lifecycle

```text
1. Investigation completes → scoring runs
2. ScoringExecutor triggers Reflector (separate LLM call)
3. Reflector analyzes investigation + scoring results + existing memories
4. Outputs: CREATE new memories, REINFORCE confirmed ones, DEPRECATE outdated ones
5. Embeddings generated for new memories via Embedder (direct HTTP)
6. Next investigation: top N memories retrieved by hybrid search (vector + keyword RRF), injected as Tier 4
7. Agent can call recall_past_investigations for deeper memory search, or search_past_sessions for entity-level session history
8. Human review: confidence adjusted (inline) + feedback Reflector (async)
```

#### Prompt Integration

Memories are injected into the system prompt as Tier 4 (after custom instructions):

```text
Tier 1:   General SRE Instructions
Tier 2:   MCP Server Instructions
Tier 2.5: Required Skill Content
Tier 2.6: On-Demand Skill Catalog
Tier 3:   Agent Custom Instructions
Tier 4:   Lessons from Past Investigations (auto-injected memories)
```

#### Configuration

```yaml
defaults:
  memory:
    enabled: true
    max_inject: 5                    # max memories auto-injected (default: 5)
    reflector_memory_limit: 20       # max existing memories for Reflector dedup (default: 20)
    embedding:
      provider: "google"             # google | openai
      model: "gemini-embedding-2-preview"
      api_key_env: "GOOGLE_API_KEY"
      dimensions: 768                # must match pgvector column
```

#### REST API Endpoints

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `/api/v1/sessions/:id/memories` | Memories extracted from this session |
| GET | `/api/v1/sessions/:id/injected-memories` | Memories auto-injected into this session |
| GET | `/api/v1/memories` | List all memories (paginated, filterable) |
| GET | `/api/v1/memories/:id` | Single memory detail |
| PATCH | `/api/v1/memories/:id` | Edit memory |
| DELETE | `/api/v1/memories/:id` | Delete memory |

#### Key Implementation Files
- `pkg/memory/service.go` — MemoryService (CRUD, hybrid retrieval, session search, refinement)
- `pkg/memory/types.go` — Memory, SessionSearchParams, SessionSearchResult types
- `pkg/memory/embedder.go` — Embedding generation (direct HTTP to provider API)
- `pkg/memory/retriever.go` — Hybrid retrieval (vector + keyword RRF, pgvector queries)
- `pkg/memory/reflector.go` — Reflector prompt builder + response parser
- `pkg/memory/parser.go` — Lenient JSON parser for Reflector output
- `pkg/memory/tool_executor.go` — ToolExecutor (recall_past_investigations + search_past_sessions)
- `pkg/queue/feedback_executor.go` — Background feedback Reflector job
- `ent/schema/investigationmemory.go` — InvestigationMemory Ent schema

---

### 13. Security & Data Protection
**Purpose**: Sensitive data protection and secure operations
**Key Responsibility**: Preventing sensitive data exposure to LLM providers, logs, and storage

TARSy implements comprehensive data masking using a hybrid approach: code-based structural analysis + regex patterns.

#### Security Architecture

```mermaid
graph TB
    subgraph mcpFlow [MCP Tool Results]
        MCP[MCP Server Response]
        Extract[extractTextContent]
        MaskTool["MaskToolResult<br/>(fail-closed)"]
    end

    subgraph alertFlow [Alert Payloads]
        Alert[Alert Submission]
        MaskAlert["MaskAlertData<br/>(fail-open)"]
    end

    subgraph destinations [Protected Destinations]
        LLM[LLM Providers]
        Timeline[Timeline Events]
        DB[History Database]
    end

    MCP --> Extract --> MaskTool --> LLM
    MaskTool --> Timeline
    MaskTool --> DB
    Alert --> MaskAlert --> DB
```

#### Masking Design Principles

1. **Fail-closed for MCP** -- on masking failure, redact entire content rather than leaking secrets
2. **Fail-open for alerts** -- on masking failure, continue with unmasked data (availability over secrecy)
3. **One-way masking** -- original values never stored or recoverable
4. **Code maskers before regex** -- structural maskers run first for precision, then regex patterns sweep
5. **Single chokepoint** -- MCP content masked in `ToolExecutor.Execute()` before `ToolResult` returns

#### Hybrid Masking Architecture

**Two-Phase Process**:

1. **Code-Based Maskers** (applied first):
   - `KubernetesSecretMasker` -- parses YAML/JSON to mask Secret resources but preserve ConfigMaps
   - Handles SecretList, mixed Lists, and JSON-in-annotations

2. **Regex Patterns** (applied second):
   - 15 built-in patterns: API keys, passwords, certificates, tokens, SSH keys, emails, etc.
   - Pattern groups: `basic`, `secrets`, `security`, `kubernetes`, `cloud`, `all`
   - Replacement format: `[MASKED_X]`

#### Configuration

Per-server MCP masking in `tarsy.yaml`:
```yaml
mcp_servers:
  kubernetes-server:
    data_masking:
      enabled: true
      pattern_groups: ["kubernetes"]
      patterns: ["certificate", "token"]
      custom_patterns:
        - pattern: "CUSTOM_SECRET_.*"
          replacement: "[MASKED_CUSTOM]"
```

Alert masking under `defaults.alert_masking`:
```yaml
defaults:
  alert_masking:
    enabled: true
    pattern_group: "security"
```

**Key Implementation Files**:
- `pkg/masking/service.go` -- MaskingService (core orchestrator)
- `pkg/masking/pattern.go` -- CompiledPattern, pattern resolution, group expansion
- `pkg/masking/masker.go` -- Masker interface for code-based maskers
- `pkg/masking/kubernetes_secret.go` -- KubernetesSecretMasker

---

### 14. Authentication & Access Control
**Purpose**: Optional authentication for enhanced security
**Key Responsibility**: Protecting dashboard and API access

TARSy supports two authentication paths via sidecar containers:

#### OAuth2-Proxy (Browser Access)

OAuth2-proxy handles GitHub OAuth for browser-based access with cookie-based sessions.

**Request flow**: User -> oauth2-proxy (:4180) -> GitHub OAuth redirect -> cookie set -> proxy with `X-Forwarded-User/Email` headers to TARSy

**Configuration**: `deploy/config/oauth2-proxy.cfg.template` + `oauth.env`

#### kube-rbac-proxy (API Client Access)

For OpenShift environments, kube-rbac-proxy validates Kubernetes ServiceAccount tokens via TokenReview/SubjectAccessReview APIs. Zero custom token code.

**Request flow**: API Client -> kube-rbac-proxy (:8443) -> SA token validation -> proxy with `X-Remote-User/Groups` to TARSy

**Author extraction** (`pkg/api/auth.go`):
```go
func extractAuthor(c *echo.Context) string {
    // oauth2-proxy headers (browser)
    if user := c.Request().Header.Get("X-Forwarded-User"); user != "" { return user }
    if email := c.Request().Header.Get("X-Forwarded-Email"); email != "" { return email }
    // kube-rbac-proxy headers (API clients)
    if user := c.Request().Header.Get("X-Remote-User"); user != "" { return user }
    return "api-client"
}
```

#### Security Middleware (`pkg/api/server.go`)

**Security headers**: X-Frame-Options DENY, X-Content-Type-Options nosniff, Referrer-Policy, Permissions-Policy

**CORS**: Dashboard URL-based origin allowlist + localhost variants for development

**WebSocket origins**: Configurable `OriginPatterns` derived from `system.dashboard_url` + localhost + `system.allowed_ws_origins`

#### Health Endpoint

`GET /health` returns minimal response for unauthenticated access: status, version, database and worker pool checks. External dependencies (MCP, LLM) excluded to prevent K8s from restarting TARSy when external services are unhealthy.

---

### 15. Trace / Observability API
**Purpose**: Deep investigation inspection for debugging and analysis
**Key Responsibility**: Exposing all four data layers through REST endpoints

Three-level trace endpoints for inspecting investigation internals, designed for the dashboard's trace view.

#### Trace Endpoints (`pkg/api/handler_trace.go`)

**Level 1: Interaction List** (`GET /sessions/:id/trace`)
Returns interactions grouped in a stage -> execution hierarchy:
```go
type TraceListResponse struct {
    Stages              []TraceStageGroup
    SessionInteractions []LLMInteractionListItem
}
```

**Level 2: LLM Interaction Detail** (`GET /sessions/:id/trace/llm/:interaction_id`)
Full LLM interaction with reconstructed conversation from the Message table. For self-contained interactions (summarization), conversation extracted from inline `llm_request` JSON.

**Level 2: MCP Interaction Detail** (`GET /sessions/:id/trace/mcp/:interaction_id`)
Full MCP interaction: tool arguments, result, available tools, timing, error details.

**Key Implementation Files**:
- `pkg/api/handler_trace.go` -- Trace HTTP handlers
- `pkg/models/interaction.go` -- Trace API response types
- `pkg/services/interaction_service.go` -- LLM/MCP interaction queries

---

### 16. Cleanup & Retention
**Purpose**: Data lifecycle management and storage hygiene
**Key Responsibility**: Enforcing retention policies and preventing unbounded growth

#### CleanupService (`pkg/cleanup/service.go`)

Standalone service with `Start`/`Stop` lifecycle running a periodic background loop:

1. **Soft-delete old sessions**: Completed sessions older than `session_retention_days` (default: 365 days) get `deleted_at` set. Also covers stale pending sessions.
2. **Remove orphaned events**: Event rows past `event_ttl` (default: 1 hour) are hard-deleted.

All operations are fail-open (log + continue). Cascade deletes for related records handled via Ent schema FK constraints.

#### Configuration

```yaml
system:
  retention:
    session_retention_days: 365
    event_ttl: 1h
    cleanup_interval: 12h
```

**Key Implementation Files**:
- `pkg/cleanup/service.go` -- CleanupService with periodic loop
- `ent/schema/` -- FK constraints for cascade deletes

---

### 17. Prometheus Metrics
**Purpose**: Runtime observability via Prometheus time-series metrics
**Key Responsibility**: Exposing quantitative signals for capacity planning, performance monitoring, and incident detection

TARSy exports Prometheus metrics via a `/metrics` endpoint on the existing HTTP server, covering all major subsystems.

#### Package Layout

```
pkg/metrics/
├── metrics.go          # All metric declarations + init() registration
└── collector.go        # GaugeCollector: DB-polling goroutine for global gauges
```

All metrics are declared as package-level vars registered against `prometheus.DefaultRegisterer`. Other packages import `pkg/metrics` and call `.Inc()`, `.Observe()`, etc. at instrumentation points.

#### Metric Categories

| Category | Key Metrics | Labels |
|----------|-------------|--------|
| Session Lifecycle | `tarsy_sessions_submitted_total`, `tarsy_sessions_terminal_total`, `tarsy_session_duration_seconds`, `tarsy_session_wait_seconds`, `tarsy_sessions_active`, `tarsy_sessions_queued` | `alert_type`, `status` |
| Worker Pool | `tarsy_workers_total`, `tarsy_workers_active`, `tarsy_orphans_recovered_total` | — |
| LLM Calls | `tarsy_llm_calls_total`, `tarsy_llm_errors_total`, `tarsy_llm_duration_seconds`, `tarsy_llm_tokens_total`, `tarsy_llm_fallbacks_total` | `provider`, `model`, `direction`, `error_code` |
| MCP Tool Calls | `tarsy_mcp_calls_total`, `tarsy_mcp_errors_total`, `tarsy_mcp_duration_seconds`, `tarsy_mcp_health_status` | `server`, `tool` |
| HTTP API | `tarsy_http_requests_total`, `tarsy_http_duration_seconds` | `method`, `path`, `status_code` |
| WebSocket | `tarsy_ws_connections_active` | — |

#### Gauge Strategy

- **Event-driven** for local worker gauges (`tarsy_workers_active`) — `Inc()`/`Dec()` on worker status transitions
- **DB-polled** for global session gauges (`tarsy_sessions_active`, `tarsy_sessions_queued`) — `GaugeCollector` queries the DB every ~15s via a `SessionCounter` interface

#### `/metrics` Endpoint

Served via `promhttp.Handler()` alongside `/health` on port 8080 without authentication. Includes standard Go runtime/process metrics via the default registry.

**For detailed design**: See [ADR-0010: Prometheus Metrics](adr/0010-prometheus-metrics.md)

**Key Implementation Files**:
- `pkg/metrics/metrics.go` -- All metric declarations
- `pkg/metrics/collector.go` -- GaugeCollector for DB-polled gauges
- `pkg/api/server.go` -- `/metrics` endpoint, HTTP metrics middleware
