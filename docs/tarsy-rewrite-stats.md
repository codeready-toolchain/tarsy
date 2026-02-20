# TARSy Rewrite: Code Statistics

Comparison of old TARSy (`tarsy-bot`, Python monolith) vs new TARSy (Go/Python split architecture).

## Old TARSy

| Component | Code | Tests | Total |
|---|--:|--:|--:|
| Python backend | 37,331 | 109,174 | 146,505 |
| Dashboard (React/TS) | 32,094 | 10,047 | 42,141 |
| **Total** | **69,425** | **119,221** | **188,646** |

- Architecture: Python monolith (FastAPI + SQLAlchemy + LangChain)
- Test-to-code ratio: **1.7x**
- Python test-to-code ratio: **2.9x**

## New TARSy

| Component | Code | Tests | Total |
|---|--:|--:|--:|
| Go orchestrator | 27,840 | 32,997 | 60,837 |
| Python LLM service | 1,505 | 1,776 | 3,281 |
| Dashboard (React/TS) | 20,655 | 2,790 | 23,445 |
| **Hand-written total** | **50,000** | **37,563** | **87,563** |
| Ent ORM (generated) | 58,593 | — | 58,593 |
| **Including generated** | **108,593** | **37,563** | **146,156** |

- Architecture: Go orchestrator + Python LLM microservice + gRPC
- Hand-written test-to-code ratio: **0.75x**
- Development: 11 phases, AI-assisted (Cursor + Claude)

## Comparison

| Metric | Old | New | Change |
|---|--:|--:|---|
| Hand-written code | 69,425 | 50,000 | **-28%** |
| Hand-written tests | 119,221 | 37,563 | **-68%** |
| Hand-written total | 188,646 | 87,563 | **-54%** |
| Python application code | 37,331 | 1,505 | **-96%** |
| Python tests | 109,174 | 1,776 | **-98%** |
| Dashboard code | 32,094 | 20,655 | **-36%** |
| Dashboard tests | 10,047 | 2,790 | **-72%** |

## Architecture: Old TARSy (Production)

2 Deployments, 3 containers, 4 Routes.

```mermaid
graph TB
    browser_old["Browser"] -- "HTTPS" --> dashboard_old
    browser_old -- "HTTPS" --> oauth_old
    api_client_old["API Client"] -- "HTTPS + JWT" --> oauth_old

    subgraph "Pod: tarsy-backend"
        oauth_old["oauth2-proxy"]
        backend["Python Backend<br/>FastAPI + LangChain"]
    end

    subgraph "Pod: tarsy-dashboard"
        dashboard_old["Nginx + React SPA"]
    end

    subgraph "RDS: tarsy-database"
        db_old["PostgreSQL"]
    end

    dashboard_old -- "API calls" --> oauth_old
    oauth_old --> backend
    backend --> db_old
    backend -- "LangChain" --> llms_old["LLM APIs<br/>Gemini, OpenAI, etc."]
    backend -- "HTTP/SSE" --> mcp_old["MCP Servers"]

    style backend fill:#3776ab,color:#fff
    style dashboard_old fill:#61dafb,color:#000
    style db_old fill:#336791,color:#fff
    style oauth_old fill:#e74c3c,color:#fff
    style llms_old fill:#f39c12,color:#000
    style mcp_old fill:#2ecc71,color:#000
```

## Architecture: New TARSy (Production)

1 Deployment, 4 containers, 1 Route.

```mermaid
graph TB
    browser_new["Browser"] -- "HTTPS" --> oauth_new
    api_client_new["API Client"] -- "HTTPS + K8s SA Token" --> rbac

    subgraph "Pod: tarsy"
        oauth_new["oauth2-proxy"]
        rbac["kube-rbac-proxy"]
        tarsy["Go Orchestrator<br/>+ embedded dashboard"]
        llm["Python LLM Service<br/>gRPC"]
    end

    subgraph "RDS: tarsy-database"
        db_new["PostgreSQL"]
    end

    oauth_new --> tarsy
    rbac --> tarsy
    tarsy -- "gRPC" --> llm
    tarsy --> db_new
    tarsy -- "HTTP/SSE" --> mcp_new["MCP Servers"]
    llm -- "SDKs" --> llms_new["LLM APIs<br/>Gemini, OpenAI, etc."]

    style tarsy fill:#00add8,color:#fff
    style llm fill:#3776ab,color:#fff
    style db_new fill:#336791,color:#fff
    style oauth_new fill:#e74c3c,color:#fff
    style rbac fill:#326ce5,color:#fff
    style llms_new fill:#f39c12,color:#000
    style mcp_new fill:#2ecc71,color:#000
```

**Key differences:**
- 3 Deployments → 2 (dashboard embedded in Go binary)
- 5 Routes → 1 (single entry point)
- Custom JWT auth → kube-rbac-proxy (K8s-native, zero key management)
- Python does everything → Python only talks to LLMs

## Chain Execution: Parallel Agents

Stages run agents in parallel using goroutines. A single-agent stage is just N=1 — no special case.

```mermaid
sequenceDiagram
    participant E as Executor
    participant A1 as Agent 1
    participant A2 as Agent 2
    participant A3 as Agent 3
    participant S as Synthesis Agent

    Note over E: Stage 1 (sequential, 1 agent)
    E->>A1: execute
    A1-->>E: result

    Note over E: Stage 2 (parallel, 3 agents)
    par
        E->>A1: execute
    and
        E->>A2: execute
    and
        E->>A3: execute
    end
    A1-->>E: result
    A2-->>E: result
    A3-->>E: result

    Note over E: Auto-synthesis (>1 agent)
    E->>S: synthesize all results
    S-->>E: unified analysis

    Note over E: Executive summary
```

## Chain Execution: Full Pipeline

```mermaid
sequenceDiagram
    participant E as Executor
    participant K as KubernetesAgent
    participant P as PerformanceAgent
    participant S as Synthesis
    participant D as DeepAnalysis
    participant LLM as Executive Summary

    Note over E: Stage 1: Investigation (parallel)
    par
        E->>K: execute(alert)
        Note right of K: MCP tools + LLM loop
    and
        E->>P: execute(alert)
        Note right of P: MCP tools + LLM loop
    end
    K-->>E: findings
    P-->>E: findings

    E->>S: synthesize(all findings)
    S-->>E: unified analysis

    Note over E: Stage 2: Deep Analysis (single)
    E->>D: execute(alert + Stage 1 context)
    Note right of D: MCP tools + LLM loop
    D-->>E: deep analysis

    Note over E: Final
    E->>LLM: generate executive summary
    LLM-->>E: summary
```

## Why Fewer Lines

- **Go's type system + Ent ORM** eliminated entire categories of tests that Python required (type validation, schema enforcement, FK constraints)
- **Python went from 37K to 1.5K** — the LLM service is just a stateless gRPC proxy; all orchestration moved to Go
- **Dashboard** kept the same UI but rewrote the data layer against a cleaner API, dropping ~11K lines
- **Test ratio dropped from 1.7x to 0.75x** — not less coverage, but compile-time guarantees replacing runtime checks
