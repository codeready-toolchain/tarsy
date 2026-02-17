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

**Phase 3: Agent Framework** -- Base agent architecture with strategy pattern for controllers, gRPC protocol evolution, FunctionCalling/Synthesis iteration controllers, ToolExecutor interface (stub), prompt system with three-tier instruction composition, real-time streaming via PostgreSQL NOTIFY + WebSocket, native tool timeline events (code execution, grounding).

**Phase 4.1: MCP Client Infrastructure** -- MCP client wrapping official Go SDK (v1.3.0), transport layer (stdio/HTTP/SSE), tool executor implementing `agent.ToolExecutor` interface, ActionInput parameter parsing (JSON/YAML/key-value/raw cascade), tool name routing (`server.tool` ↔ `server__tool`), error classification with retry/session recreation, per-session client isolation, health monitor with system warnings service, eager startup validation (fatal on failure).

**Phase 4.2: Data Masking** -- MaskingService singleton with 15 built-in regex patterns, pattern groups, custom regex from YAML config, and KubernetesSecretMasker (code-based, structural YAML/JSON parsing to mask Secrets but not ConfigMaps). Two integration points: MCP tool results (fail-closed) and alert payloads (fail-open). Replacement format `[MASKED_X]`. Alert masking configurable under `defaults.alert_masking`.

**Phase 4.3: MCP Features** -- Per-alert MCP selection override (replace-not-merge semantics, API + executor validation), LLM-based tool result summarization at controller level (shared `executeToolCall()` path, fail-open, token estimation heuristic, two-tier truncation: 8K storage / 100K summarization), and tool call streaming lifecycle (single `llm_tool_call` event with created→completed states, `mcp_tool_summary` events for summarization streaming). Removed `tool_result` and `mcp_tool_call` event types. Added `"summarization"` LLMInteraction type.

**Phase 5.1: Chain Orchestration + Session Completion** -- Sequential multi-stage chain loop in `RealSessionExecutor` with `executeStage()`/`executeAgent()` extraction, per-agent-execution MCP lifecycle (create + teardown per agent, not per session), in-memory inter-stage context passing via `BuildStageContext()`, `stage.status` event type (single event for all lifecycle transitions), session progress tracking (`current_stage_index`/`current_stage_id`), final analysis extraction (reverse search from last completed stage), fail-open executive summary generation (direct LLM call, configurable provider via `chain.executive_summary_provider`), session-level timeline events (optional `stage_id`/`execution_id` on TimelineEvent schema). Fixed backend derivation: `Backend` field on `ResolvedAgentConfig` resolved from iteration strategy via `ResolveBackend()`, passed through `GenerateInput` to gRPC — replacing implicit derivation from provider type.

**Phase 5.2: Parallel Execution** -- Unified `executeStage()` with goroutine + WaitGroup + channel machinery for all stages (N=1 agents handled identically to N=many — no separate code paths). Multi-agent and replica execution via `buildConfigs()`/`buildMultiAgentConfigs()`/`buildReplicaConfigs()`. In-memory result aggregation (`aggregateStatus()`/`aggregateError()`) with success policy enforcement (all/any, defaulting to `any`). Automatic synthesis after stages with >1 agent — synthesis creates its own Stage DB record, receives full investigation history via timeline events (through `FormatInvestigationForSynthesis()` with shared `formatTimelineEvents()` helper and tool call/summary deduplication), replaces investigation result for downstream context. Chain loop tracks `dbStageIndex` separately from config index to accommodate inserted synthesis stages. `displayName` parameter on `executeAgent()` supports replica naming (`{BaseName}-1`, etc.). Stage status events moved inside `executeStage()` (after Stage creation, so `stageID` is always present). Fixed `UpdateStageStatus()` default policy from `all` → `any`.

**Phase 5.3: Follow-up Chat** -- Full end-to-end chat: `POST /sessions/:id/chat/messages` → `ChatMessageExecutor` async execution → streaming response via existing WebSocket. Chat is a prompt concern — same controllers (FunctionCalling, Synthesis) handle chat via `ChatContext` on `ExecutionContext`, no separate chat controllers. `ChatMessageExecutor` (`pkg/queue/chat_executor.go`) spawns one goroutine per message (no pool — chats are rare, one-at-a-time per chat enforced). Context built from unified timeline (`GetSessionTimeline` + `FormatInvestigationContext`) — deleted `ChatExchange`/`ChatHistory`/`FormatChatHistory`/`GetChatHistory` in favor of timeline-based context. `ResolveChatAgentConfig()` added to `pkg/agent/config_resolver.go` with `aggregateChainMCPServers()` fallback. Refactored `createToolExecutor()`, `resolveMCPSelection()`, `publishStageStatus()` from `RealSessionExecutor` methods to shared package-level functions. New events: `chat.created`, `chat.user_message`. Cancel handler extended via `CancelBySessionID()`. Chat executor shuts down before worker pool.

**Phase 6: End-to-End Testing** -- Comprehensive in-process e2e test suite (`test/e2e/`) exercising the full pipeline from HTTP API through chain execution to WebSocket delivery. Real PostgreSQL (testcontainers, per-test schema), real event streaming, real WebSocket — only LLM (ScriptedLLMClient with dual dispatch) and MCP servers (in-memory SDK) are mocked, while the full `mcp.Client` → `mcp.ToolExecutor` pipeline is exercised. 7 test scenarios: Pipeline (4 stages, synthesis, FunctionCalling + NativeThinking, 2 MCP servers, summarization, forced conclusion, replicas, chat — with 31 golden-file interaction details), FailureResilience (policy=any, exec summary fail-open), FailurePropagation (policy=all, fail-fast), Cancellation (investigation + chat), Timeout (session + chat), Concurrency (MaxConcurrentSessions enforcement), MultiReplica (cross-replica WS via NOTIFY/LISTEN). Bug fixes during testing: cancel handler for completed sessions with active chats, post-cancellation DB updates using `context.Background()`, agent status mapping from `ctx.Err()`, API startup wiring validation. New APIs: timeline endpoint (`GET /sessions/:id/timeline`), trace/observability endpoints (interaction list, LLM detail with conversation reconstruction, MCP detail). Infrastructure: `pkg/mcp/testing.go` (InjectSession, NewTestClientFactory), `test/database/` (SharedTestDB), auto-catchup on WebSocket subscribe, `AgentExecution.llm_provider` field, Makefile targets (test-unit, test-e2e, test-go, test-go-coverage).

Full design docs for completed phases are in `docs/archive/`.

---

### Phase 7: Dashboard

See docs/phase7-dashboard-plan.md

**Phase 7.0: Backend API Extensions** -- ✅ DONE. Session list/active/filter-options/summary endpoints, system endpoints (warnings, MCP servers, default tools), alert types endpoint, progress events (session.progress, execution.progress, interaction.created), health version field, WebSocket moved to /api/v1/ws.

**Phase 7.1: Dashboard Foundation** -- ✅ DONE. React 19 + TypeScript + Vite 7 + MUI 7 project setup, shared layout (SharedHeader with logo/glass styling, VersionFooter), auth with graceful degradation (LoginButton, UserMenu, AuthContext/AuthProvider), API client with retry/cancel logic, WebSocket service with channel subscriptions and reconnect, routing (createBrowserRouter), Go static file serving with SPA fallback, theme ported from old dashboard, `make dev` / `make dev-stop` targets.

---

### Phase 8: Integrations

**Runbook System (Phase 8.1)**
- [ ] GitHub integration
- [ ] Runbook fetching & caching
- [ ] Per-chain runbook configuration

**Multi-LLM Support (Phase 8.2)** -- ✅ DONE. Replaced LangChain stub with real `LangChainProvider` supporting OpenAI, Anthropic, xAI, Google (via LangChain), and VertexAI. Completely removed ReAct iteration strategy and `ReActController`; renamed `NativeThinkingController` → `FunctionCallingController` (shared by `native-thinking` and new `langchain` strategies). Both use native/structured tool calling. Deleted all text-based ReAct parsing (`react_parser.go`, `tools.go`), ReAct streaming code, ReAct prompt templates. Added shared `tool_names.py` utility for canonical↔API name encoding. LangChain provider features: streaming via `astream()`, `content_blocks` for thinking/reasoning, `bind_tools()` for function calling, model caching, retry with exponential backoff. Dashboard cleanup: removed dead `isReActResponse()`, `NATIVE_THINKING` constant. Four strategies remain: `native-thinking`, `langchain`, `synthesis`, `synthesis-native-thinking`.

**Slack Notifications (Phase 8.3)**
- [ ] Slack client
- [ ] Notification templates
- [ ] Message threading/fingerprinting
- [ ] Configurable notifications

---

### Phase 9: Security

**Authentication & Authorization (Phase 9.1)**
- [ ] OAuth2-proxy integration
- [ ] Token validation
- [ ] GitHub OAuth flow
- [ ] Session/user tracking
- [ ] WebSocket origin validation (replace InsecureSkipVerify)

---

### Phase 10: History

**History System (Phase 10.1)**
- [ ] History repository
- [ ] Timeline reconstruction
- [ ] Conversation retrieval
- [ ] Session querying & filtering

---

### Phase 11: Monitoring & Operations

**System Health (Phase 11.1)**
- [ ] Health check endpoint enhancements
- [ ] Queue metrics

**Observability (Phase 11.2)**
- [ ] Structured logging
- [ ] Metrics collection (Prometheus)
- [ ] Error tracking
- [ ] Performance monitoring

**History Cleanup (Phase 11.3)**
- [ ] Retention policies
- [ ] Cleanup service
- [ ] Cascade deletes

---

### Phase 12: Deployment, DevOps & CI/CD

**Containerization (Phase 12.1)**
- [ ] Multi-stage Docker builds
- [ ] Container orchestration (podman-compose)
- [ ] Build & push images
- [ ] Service health checks
- [ ] Volume management

**Kubernetes/OpenShift (Phase 12.2)**
- [ ] Kustomize manifests
- [ ] Service deployments
- [ ] ConfigMaps & secrets
- [ ] Routes/ingress
- [ ] ImageStreams

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
