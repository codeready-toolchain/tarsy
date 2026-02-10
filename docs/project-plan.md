# TARSy Project Plan

## Project Goal

**New TARSy Implementation: Go & Python Split Architecture**

Reimplementation of TARSy (`/home/igels/Projects/AI/tarsy-bot`) with a modernized hybrid architecture:

- **Go Orchestrator**: HTTP/WebSocket API, session management, orchestration, business logic
- **Python LLM Service**: Stateless microservice for LLM interactions (Gemini, with future multi-provider support)
- **gRPC**: Inter-service communication with streaming support

See `docs/architecture-context.md` for comprehensive architectural details, interfaces, patterns, and technology choices from all completed phases.

---

## Development Roadmap

### Completed Phases

**Phase 1: Proof of Concept** -- Validated Go-Python split architecture with end-to-end streaming.

**Phase 2: Core Infrastructure** -- PostgreSQL + Ent ORM, YAML-based configuration system with hierarchical resolution, database-backed queue with worker pool, session cancellation API. Web framework: Echo v5.

**Phase 3: Agent Framework** -- Base agent architecture with strategy pattern for controllers, gRPC protocol evolution, ReAct/NativeThinking/Synthesis iteration controllers, ReAct parser, ToolExecutor interface (stub), prompt system with three-tier instruction composition, real-time streaming via PostgreSQL NOTIFY + WebSocket, native tool timeline events (code execution, grounding).

Full design docs for completed phases are in `docs/archive/`.

---

### Phase 4: MCP Integration

**MCP Client Infrastructure (Go)**
- [ ] MCP client implementation (Go -- uses official mcp go sdk)
- [ ] Transport layer -- stdio (subprocess via `os/exec`), HTTP, SSE
- [ ] Tool registry & discovery (list tools from MCP servers)
- [ ] Error handling & recovery (retry, session recreation)
- [ ] Per-session MCP client isolation
- [ ] MCP server health monitoring
- [ ] System warnings service (in-memory, used by health monitor and dashboard)
- [ ] ReAct `ActionInput` parameter parsing -- parse raw text into structured MCP parameters (JSON/YAML/key-value/raw string cascade, type coercion). See deferred items in `docs/architecture-context.md`.
- [ ] Tool name validation -- split `"server.tool"` string, validate both parts, route to correct MCP server.

**Data Masking** (moved from Phase 7 -- required for MCP tool results)
- [ ] Masking service (Go)
- [ ] Regex-based maskers (15 patterns defined in builtin.go)
- [ ] MCP tool result masking integration
- [ ] Alert payload sanitization

**MCP Features**
- [ ] Custom MCP configuration per alert (mcp_selection override)
- [ ] Tool result summarization (LLM-based, configurable threshold)
- [ ] MCP server health tracking
- [ ] Tool output streaming -- extend streaming protocol with `stream.chunk` for live MCP tool output during execution.

**Note on MCP servers**: TARSy does not embed MCP servers. It connects to external MCP servers (e.g., `npx -y kubernetes-mcp-server@0.0.54`) via stdio subprocess, HTTP, or SSE transports.

---

### Phase 5: Chain Execution

**Chain Orchestration (Go)**
- [ ] Chain orchestrator -- uses existing ChainRegistry from Phase 2 config
- [ ] Multi-stage sequential execution
- [ ] Stage execution manager (create Stage + AgentExecution records)
- [ ] Lazy context building (Agent.BuildStageContext)
- [ ] Data flow between stages (previous stage context -> next stage prompt)

**Parallel Execution (Go)**
- [ ] Parallel stage executor (goroutine-per-agent)
- [ ] Result aggregation from parallel agents
- [ ] Success policy enforcement (all/any)
- [ ] Synthesis agent invocation (automatic after parallel stages)
- [ ] Replica execution (same agent N times)

**Session Completion**
- [ ] Max iteration enforcement (force conclusion, no pause/resume)
- [ ] Executive summary generation (LLM call)
- [ ] Final analysis formatting and storage

---

### Phase 6: Dashboard

**Real-time Features**
- [ ] Live LLM streaming UI
- [ ] Stage timeline visualization
- [ ] Native thinking indicators

**History Views**
- [ ] Session list with filters
- [ ] Detailed session view
- [ ] Conversation replay
- [ ] Chat interface

**System Views**
- [ ] MCP server status
- [ ] System warnings display
- [ ] Queue metrics dashboard

---

### Phase 7: Integrations

**Runbook System (Go)**
- [ ] GitHub integration
- [ ] Runbook fetching & caching
- [ ] Per-chain runbook configuration

**Multi-LLM Support (Python)**
- [ ] Real LangChainProvider (replace stub)
- [ ] OpenAI, Anthropic, xAI client implementations
- [ ] Google Search grounding support
- [ ] VertexAI support

**Slack Notifications (Go)**
- [ ] Slack client
- [ ] Notification templates
- [ ] Message threading/fingerprinting
- [ ] Configurable notifications

---

### Phase 8: Security

**Authentication & Authorization**
- [ ] OAuth2-proxy integration
- [ ] Token validation
- [ ] GitHub OAuth flow
- [ ] Session/user tracking
- [ ] WebSocket origin validation (replace InsecureSkipVerify)

**Advanced Data Masking**
- [ ] Kubernetes secret masker (structural parser)
- [ ] Parse YAML/JSON structures
- [ ] Distinguish K8s Secrets vs ConfigMaps
- [ ] Integrate with masking pattern groups

---

### Phase 9: History & Chat

**History System (Go)**
- [ ] History repository
- [ ] Timeline reconstruction
- [ ] Conversation retrieval
- [ ] Session querying & filtering

**Follow-up Chat (Go + Python LLM)**
- [ ] Chat service (Go orchestration)
- [ ] Chat agent with investigation context
- [ ] Context preservation (lazy context building)
- [ ] Multi-user support
- [ ] Chat-investigation timeline merging

---

### Phase 10: Monitoring & Operations

**System Health**
- [ ] Health check endpoint enhancements
- [ ] Queue metrics

**Observability**
- [ ] Structured logging
- [ ] Metrics collection (Prometheus)
- [ ] Error tracking
- [ ] Performance monitoring

**History Cleanup**
- [ ] Retention policies
- [ ] Cleanup service
- [ ] Cascade deletes

---

### Phase 11: Deployment, DevOps & CI/CD

**Containerization**
- [ ] Multi-stage Docker builds
- [ ] Container orchestration (podman-compose)
- [ ] Service health checks
- [ ] Volume management

**Kubernetes/OpenShift**
- [ ] Kustomize manifests
- [ ] Service deployments
- [ ] ConfigMaps & secrets
- [ ] Routes/ingress
- [ ] ImageStreams

**CI/CD & Testing Infra**
- [ ] GitHub Actions workflows
- [ ] Test automation (Go + Python)
- [ ] Build & push images
- [ ] Deployment automation
- [ ] E2E test suite

**Note on testing**: Each phase includes its own test suite (unit + integration). There is no separate testing phase.

---

## Documentation Structure

| Document | Purpose |
|----------|---------|
| `docs/project-plan.md` | This file -- roadmap and phase overview |
| `docs/architecture-context.md` | Cumulative architecture: interfaces, patterns, decisions, tech stack |
| `docs/phase{N}-*-design.md` | Current/upcoming phase detailed design |
| `docs/phase{N}-*-questions.md` | Current/upcoming phase open questions |
| `docs/archive/` | Completed phase design & question docs (reference only) |
| `docs/ai-prompt-templates.md` | Prompt templates for the AI-assisted development workflow |
