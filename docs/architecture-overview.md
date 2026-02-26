# TARSy - High-Level Architecture Overview

> **For detailed technical implementation**: See [Functional Areas Design Document](functional-areas-design.md)

## What is TARSy?

TARSy is an **AI-powered incident analysis system** built on a Go/Python split architecture. When an alert arrives, TARSy automatically selects the appropriate agent chain, executes multiple stages where specialized agents investigate using external tools (via MCP), and delivers comprehensive analysis and recommendations for engineers to act upon.

The **Go Orchestrator** owns all orchestration logic: session management, chain execution, MCP tool execution, prompt building, conversation management, and real-time WebSocket streaming. The **Python LLM Service** is a stateless gRPC microservice that handles LLM provider interactions (Gemini, OpenAI, Anthropic, xAI, VertexAI), existing solely because LLM provider SDKs have best support in Python.

After an investigation completes, engineers can continue the conversation through **follow-up chat** - asking clarifying questions, requesting deeper analysis, or exploring different aspects of the incident. The chat agent maintains full access to the investigation context and tools, enabling continued exploration without restarting the analysis pipeline.

## Core Concept

```mermaid
graph LR
    A[Alert Arrives] --> B[Go Orchestrator]
    B --> C[Selects Agent Chain]
    C --> D["Stage 1: Data Collection"]
    D --> E["Stage 2: Analysis"]
    E --> F["Stage 3: Recommendations"]
    F --> G[Engineers Take Action]
```

## Architecture: Go/Python Split

```mermaid
graph TB
    subgraph goOrchestrator [Go Orchestrator]
        API["HTTP API (Echo v5)"]
        Queue[Worker Pool]
        Executor[Session Executor]
        AgentFW[Agent Framework]
        MCPClient["MCP Client (Go SDK)"]
        Events[Event System]
        WS[WebSocket Server]
    end

    subgraph pythonLLM [Python LLM Service]
        GRPC[gRPC Server]
        GoogleNative[GoogleNativeProvider]
        LangChain[LangChainProvider]
    end

    subgraph external [External Systems]
        DB[(PostgreSQL)]
        MCPServers["MCP Servers (kubectl, ArgoCD, etc.)"]
        LLMAPIs["LLM APIs (Gemini, OpenAI, Anthropic, xAI)"]
        Slack[Slack]
    end

    subgraph clients [Clients]
        Dashboard[React Dashboard]
        Monitoring[Monitoring Systems]
    end

    Dashboard --> API
    Dashboard --> WS
    Monitoring --> API

    API --> Queue
    Queue --> Executor
    Executor --> AgentFW
    AgentFW --> MCPClient
    AgentFW -->|gRPC| GRPC
    Events --> WS
    Executor --> Events

    MCPClient --> MCPServers
    GRPC --> GoogleNative
    GRPC --> LangChain
    GoogleNative --> LLMAPIs
    LangChain --> LLMAPIs

    Executor --> DB
    Events --> DB
    Executor --> Slack
```

## Key Components

### 1. Go Orchestrator

The Go backend is the brain of the system. It handles:

- **HTTP API** (Echo v5) for alert submission, session management, chat, and system health
- **Worker pool** with database-backed queue for session processing across multiple replicas
- **Chain execution** with sequential multi-stage workflows and parallel agent support
- **Agent framework** with pluggable iteration controllers
- **MCP client** (Go SDK v1.3.0) for executing external tools with stdio/HTTP/SSE transports
- **Event system** using PostgreSQL LISTEN/NOTIFY for cross-pod real-time streaming
- **WebSocket server** for dashboard live updates
- **Data masking** to prevent sensitive data from reaching LLM providers
- **Slack notifications** for session lifecycle events

### 2. Python LLM Service

A stateless gRPC microservice with a single RPC: `Generate(GenerateRequest) returns (stream GenerateResponse)`. It routes to two provider backends:

- **GoogleNativeProvider**: Gemini models via `google-genai` SDK with native thinking features, thought signatures for multi-turn reasoning, and native function calling
- **LangChainProvider**: Multi-provider support (OpenAI, Anthropic, xAI, Google, VertexAI) via LangChain ecosystem with structured tool calling

The Python service has zero orchestration state and zero MCP knowledge. It receives messages + config via gRPC, calls the LLM provider API, and streams response chunks back.

### 3. Sequential Agent Chains

- **Multi-stage workflows** where specialized agents build upon each other's work
- Each chain consists of **sequential stages** with data accumulating between stages
- **Flexible chain definitions** via YAML configuration without code changes
- **Parallel execution support** where multiple agents investigate independently within a stage
- **Automatic synthesis** after parallel stages -- a SynthesisAgent unifies findings from multiple agents
- **Replica execution** for running the same agent multiple times with different providers for comparison

### 4. Specialized Agents & Controllers

Agents are specialized AI-powered components that analyze alerts using domain expertise and configurable iteration controllers. Agent behavior is governed by two orthogonal configuration axes:

- **`AgentType`** (`""` | `"synthesis"` | `"scoring"`) — determines which controller runs the agent
- **`LLMBackend`** (`"google-native"` | `"langchain"`) — determines which Python SDK path handles LLM calls

**Two controller types** (text-based ReAct parsing was completely removed):

- **IteratingController**: Multi-turn tool-calling loop with tool definitions bound to the LLM. Works with any `LLMBackend` — `google-native` (Gemini native SDK) or `langchain` (multi-provider)
- **SingleShotController**: Tool-less single LLM call, parameterized via `SingleShotConfig`. Used for synthesis (and future scoring)

**Forced Conclusion**: When agents reach their maximum iteration limit, the system forces a conclusion -- one extra LLM call without tools, asking the agent to provide the best analysis with available data. There is no pause/resume mechanism.

### 5. MCP Integration & Tool Management

- **Go MCP SDK v1.3.0** for external tool integration (kubectl, ArgoCD, monitoring, etc.)
- **Three transport types**: stdio (command-line servers), HTTP (JSON-RPC endpoints), SSE (Server-Sent Events)
- **Per-agent-execution isolation** -- each agent execution gets its own MCP Client with independent sessions
- **Data masking** with hybrid approach: code-based structural maskers (K8s Secrets) + regex patterns
- **Tool result summarization** -- large MCP results are automatically summarized by an LLM call before being sent back to the investigating agent
- **Health monitoring** -- background service checks server health, attempts recovery, surfaces warnings

### 6. LLM Multi-Provider Support

Built-in support for multiple AI providers with zero-configuration defaults:

| Provider | Default Model | Context Window |
|----------|--------------|----------------|
| Google (default) | gemini-3-flash-preview | 1M tokens |
| OpenAI | gpt-5.2 | 400K tokens |
| Anthropic | claude-sonnet-4-6 | 1M tokens (beta) |
| xAI | grok-4-1-fast-reasoning | 2M tokens |
| VertexAI | claude-sonnet-4-6 | 1M tokens (beta) |

**Per-chain/stage provider configuration**: Different stages can use different LLM providers for cost/performance optimization. Native thinking mode available for Gemini models with exposed internal reasoning.

### 7. Real-time Dashboard

- **React 19 + TypeScript + Vite 7 + MUI 7** single-page application
- **Session list** with filtering by status, alert type, chain, date range, and search
- **Conversation timeline** with real-time LLM streaming (thinking, tool calls, final answers)
- **Parallel execution tabs** for viewing multiple agent results side by side
- **Trace view** with hierarchical LLM/MCP interaction details for debugging
- **Alert submission interface** with MCP tool override selection
- **System status page** showing MCP server health and system warnings
- **WebSocket-driven updates** with automatic reconnection and event catchup

### 8. Follow-up Chat

- **Interactive investigation continuation** after sessions reach terminal state (completed, failed, timed out)
- **Context preservation** -- chat agent receives the full investigation timeline as context
- **Same tool access** -- uses the original investigation's MCP server configuration
- **Unified timeline** -- chat messages appear inline with investigation stages
- **Real-time streaming** -- follow-up responses stream through the same WebSocket infrastructure

### 9. Slack Notifications

TARSy can automatically send Slack notifications when alert processing starts (for Slack-originated alerts) and reaches a terminal status (completed, failed, timed out, cancelled). The system supports both standard channel notifications and threaded replies to alert messages via fingerprint correlation.

**For complete Slack setup guide**: See [Slack Integration Documentation](slack-integration.md)

## How It Works

### Alert Processing Flow

```mermaid
sequenceDiagram
    participant M as Monitoring System
    participant API as Go API Server
    participant DB as PostgreSQL
    participant W as Worker Pool
    participant E as Session Executor
    participant A as Agent + Controller
    participant MCP as MCP Servers
    participant LLM as Python LLM Service
    participant WS as WebSocket
    participant D as Dashboard

    M->>API: POST /api/v1/alerts
    API->>DB: Create session (PENDING)
    API-->>M: 200 OK (session_id)

    Note over W: Background worker checks capacity
    W->>DB: Claim next session (FOR UPDATE SKIP LOCKED)
    W->>E: Execute session
    E->>E: Resolve chain config
    E->>E: Download runbook (if configured)

    loop For each stage in chain
        E->>A: Execute agent(s) for stage
        A->>MCP: List available tools
        MCP-->>A: Tool definitions

        loop IteratingController iterations
            A->>LLM: Generate (messages + tools) via gRPC
            LLM-->>A: Stream response chunks
            A-->>WS: Stream thinking + response to dashboard

            alt Tool calls in response
                A->>MCP: Execute tool calls
                MCP-->>A: Tool results (masked + summarized)
                A->>A: Append tool results, continue loop
            else No tool calls (final answer)
                A->>A: Extract final analysis
            end
        end

        E->>DB: Store stage results
        E-->>WS: Publish stage status
    end

    E->>LLM: Generate executive summary
    E->>DB: Update session (completed)
    E-->>WS: Publish session status
    D->>D: Engineers review analysis
```

### IteratingController Iteration Detail

For agents using the IteratingController, the investigation follows a structured function calling pattern:

```mermaid
sequenceDiagram
    participant C as Controller
    participant LLM as Python LLM Service
    participant MCP as MCP Servers

    C->>LLM: Messages + tool definitions (gRPC stream)

    loop Investigation iterations (up to max_iterations)
        LLM-->>C: Streaming response (thinking + text + tool calls)
        Note over LLM,C: Thinking: internal reasoning (Gemini native)<br/>Tool calls: structured function declarations

        alt Tool calls in response
            C->>C: Store assistant message with tool calls
            loop For each tool call
                C->>MCP: Execute tool (via ToolExecutor)
                MCP-->>C: Tool result (masked, optionally summarized)
                C->>C: Append tool result message (role=tool)
            end
            C->>LLM: Continue with updated conversation
        else No tool calls
            C->>C: This IS the final answer
            Note over C: Create final_analysis timeline event
        end
    end

    alt Max iterations reached
        C->>LLM: Force conclusion (call WITHOUT tools)
        Note over C,LLM: "Provide your best analysis<br/>with the data collected so far"
        LLM-->>C: Forced final answer
    end
```

### Forced Conclusion at Max Iterations

When an agent reaches its configured `max_iterations` limit, the system forces a conclusion rather than leaving the investigation incomplete. The controller makes one extra LLM call **without tool bindings**, asking the agent to synthesize everything it has learned into a final analysis. This ensures every investigation produces actionable output.

**Hierarchical iteration configuration** allows fine-grained control:
- **System defaults** (e.g., `max_iterations: 20`)
- **Agent-level** override
- **Chain-level** override
- **Stage-level** override
- **Parallel agent-level** override (highest precedence)

### Follow-up Chat

After a session reaches a terminal state (completed, failed, or timed out), engineers can start a chat conversation to ask follow-up questions. The chat agent receives the full investigation timeline as context and has access to the same MCP tools. Responses stream in real-time and appear inline in the conversation timeline.

Chat is a **prompt concern, not a controller concern** -- the same IteratingController and SingleShotController handle both investigation and chat. The `ChatContext` on the execution context triggers chat-specific prompting.

## Authentication & Access Control

TARSy supports two authentication paths for different client types:

### Browser Access (OAuth2-Proxy)

OAuth2-proxy handles GitHub OAuth authentication for browser-based access. The dashboard and API are protected behind cookie-based sessions.

### API Client Access (kube-rbac-proxy)

For programmatic API access in Kubernetes/OpenShift environments, kube-rbac-proxy validates ServiceAccount tokens via the Kubernetes TokenReview API and checks authorization via SubjectAccessReview.

```mermaid
graph LR
    Browser -->|GitHub OAuth| OAuth2Proxy["oauth2-proxy (:4180)"]
    APIClient -->|SA Token| KubeRBAC["kube-rbac-proxy (:8443)"]
    OAuth2Proxy --> TARSy["TARSy API (:8080)"]
    KubeRBAC --> TARSy
    Health[Health Probes] --> TARSy
```

### Development Modes

- **Host dev** (`make dev`): Direct access to TARSy API without authentication
- **Container dev** (`make containers-deploy`): Full stack with OAuth2-proxy for real auth testing
- **OpenShift**: Production deployment with both auth paths active

## Agent Intelligence Model

Each agent operates with three tiers of knowledge composed into its system prompt:

1. **General SRE Instructions**: Universal best practices for incident response
2. **MCP Server Instructions**: Tool-specific guidance from each configured MCP server
3. **Agent Custom Instructions**: Domain expertise specific to the agent's specialty area

Additionally, **runbook content** (fetched from GitHub or using a configured default) is injected into the user prompt for alert-specific investigation procedures.

## Deployment Architecture

TARSy uses a single-pod multi-container architecture:

```mermaid
graph TB
    subgraph pod [TARSy Pod]
        OAuth2["oauth2-proxy (:4180)<br/>Browser auth"]
        KRBAC["kube-rbac-proxy (:8443)<br/>API client auth"]
        Tarsy["TARSy Go (:8080)<br/>Backend + Dashboard"]
        LLMSvc["LLM Service (:50051)<br/>Python gRPC"]
    end

    subgraph dbPod [Database Pod]
        PG["PostgreSQL (:5432)"]
        PVC[Persistent Volume]
    end

    Route[OpenShift Route] --> OAuth2
    OAuth2 --> Tarsy
    KRBAC --> Tarsy
    Tarsy -->|gRPC| LLMSvc
    Tarsy --> PG
    PG --> PVC
```

All 4 containers share localhost network within the pod. The same container images work for both podman-compose (development) and OpenShift (production).

## Extensibility

- **New Agent Types**: Add custom agents via `agents` section in `tarsy.yaml` with MCP servers, instructions, LLM backend, and iteration configuration
- **New MCP Servers**: Integrate additional diagnostic tools via `mcp_servers` section (stdio, HTTP, or SSE transports)
- **New Agent Chains**: Deploy multi-stage workflows via `agent_chains` section with alert type mappings, parallel execution, and synthesis
- **LLM Provider Configuration**: Override built-in providers or add custom proxy configurations via `llm-providers.yaml`
- **Per-Alert MCP Override**: Fine-grained tool control per alert request via the `mcp_selection` API field
- **Integration Points**: Connect with monitoring systems (AlertManager, PagerDuty) and notification systems (Slack)

**Example chain configuration** (`deploy/config/tarsy.yaml`):
```yaml
agent_chains:
  parallel-investigation:
    alert_types: ["CriticalIncident"]
    llm_provider: "google-default"
    stages:
      - name: "parallel-analysis"
        agents:
          - name: "KubernetesAgent"
            llm_backend: "google-native"
            llm_provider: "gemini-3.1-pro"
            custom_instructions: "Focus on pod health, resource limits, and recent restarts"
          - name: "KubernetesAgent"
            llm_backend: "langchain"
            llm_provider: "anthropic-default"
            custom_instructions: "Focus on networking, service endpoints, and ingress"
        max_iterations: 15
        synthesis_llm_provider: "openai-default"
    chat:
      enabled: true
```

## Next Steps

For detailed technical implementation, file paths, interfaces, data models, and deployment information, see the comprehensive [Functional Areas Design Document](functional-areas-design.md).
