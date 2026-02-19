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

**Phase 4.1: MCP Client Infrastructure** -- MCP client wrapping official Go SDK (v1.3.0), transport layer (stdio/HTTP/SSE), tool executor implementing `agent.ToolExecutor` interface, ActionInput parameter parsing (JSON/YAML/key-value/raw cascade), tool name routing (`server.tool` ↔ `server__tool`), error classification with retry/session recreation, per-session client isolation, health monitor with system warnings service, eager startup validation (warn on failure).

**Phase 4.2: Data Masking** -- MaskingService singleton with 15 built-in regex patterns, pattern groups, custom regex from YAML config, and KubernetesSecretMasker (code-based, structural YAML/JSON parsing to mask Secrets but not ConfigMaps). Two integration points: MCP tool results (fail-closed) and alert payloads (fail-open). Replacement format `[MASKED_X]`. Alert masking configurable under `defaults.alert_masking`.

**Phase 4.3: MCP Features** -- Per-alert MCP selection override (replace-not-merge semantics, API + executor validation), LLM-based tool result summarization at controller level (shared `executeToolCall()` path, fail-open, token estimation heuristic, two-tier truncation: 8K storage / 100K summarization), and tool call streaming lifecycle (single `llm_tool_call` event with created→completed states, `mcp_tool_summary` events for summarization streaming). Removed `tool_result` and `mcp_tool_call` event types. Added `"summarization"` LLMInteraction type.

**Phase 5.1: Chain Orchestration + Session Completion** -- Sequential multi-stage chain loop in `RealSessionExecutor` with `executeStage()`/`executeAgent()` extraction, per-agent-execution MCP lifecycle (create + teardown per agent, not per session), in-memory inter-stage context passing via `BuildStageContext()`, `stage.status` event type (single event for all lifecycle transitions), session progress tracking (`current_stage_index`/`current_stage_id`), final analysis extraction (reverse search from last completed stage), fail-open executive summary generation (direct LLM call, configurable provider via `chain.executive_summary_provider`), session-level timeline events (optional `stage_id`/`execution_id` on TimelineEvent schema). Fixed backend derivation: `Backend` field on `ResolvedAgentConfig` resolved from iteration strategy via `ResolveBackend()`, passed through `GenerateInput` to gRPC — replacing implicit derivation from provider type.

**Phase 5.2: Parallel Execution** -- Unified `executeStage()` with goroutine + WaitGroup + channel machinery for all stages (N=1 agents handled identically to N=many — no separate code paths). Multi-agent and replica execution via `buildConfigs()`/`buildMultiAgentConfigs()`/`buildReplicaConfigs()`. In-memory result aggregation (`aggregateStatus()`/`aggregateError()`) with success policy enforcement (all/any, defaulting to `any`). Automatic synthesis after stages with >1 agent — synthesis creates its own Stage DB record, receives full investigation history via timeline events (through `FormatInvestigationForSynthesis()` with shared `formatTimelineEvents()` helper and tool call/summary deduplication), replaces investigation result for downstream context. Chain loop tracks `dbStageIndex` separately from config index to accommodate inserted synthesis stages. `displayName` parameter on `executeAgent()` supports replica naming (`{BaseName}-1`, etc.). Stage status events moved inside `executeStage()` (after Stage creation, so `stageID` is always present). Fixed `UpdateStageStatus()` default policy from `all` → `any`.

**Phase 5.3: Follow-up Chat** -- Full end-to-end chat: `POST /sessions/:id/chat/messages` → `ChatMessageExecutor` async execution → streaming response via existing WebSocket. Chat is a prompt concern — same controllers (FunctionCalling, Synthesis) handle chat via `ChatContext` on `ExecutionContext`, no separate chat controllers. `ChatMessageExecutor` (`pkg/queue/chat_executor.go`) spawns one goroutine per message (no pool — chats are rare, one-at-a-time per chat enforced). Context built from unified timeline (`GetSessionTimeline` + `FormatInvestigationContext`) — deleted `ChatExchange`/`ChatHistory`/`FormatChatHistory`/`GetChatHistory` in favor of timeline-based context. `ResolveChatAgentConfig()` added to `pkg/agent/config_resolver.go` with `aggregateChainMCPServers()` fallback. Refactored `createToolExecutor()`, `resolveMCPSelection()`, `publishStageStatus()` from `RealSessionExecutor` methods to shared package-level functions. New events: `chat.created`, `chat.user_message`. Cancel handler extended via `CancelBySessionID()`. Chat executor shuts down before worker pool.

**Phase 6: End-to-End Testing** -- Comprehensive in-process e2e test suite (`test/e2e/`) exercising the full pipeline from HTTP API through chain execution to WebSocket delivery. Real PostgreSQL (testcontainers, per-test schema), real event streaming, real WebSocket — only LLM (ScriptedLLMClient with dual dispatch) and MCP servers (in-memory SDK) are mocked, while the full `mcp.Client` → `mcp.ToolExecutor` pipeline is exercised. 7 test scenarios: Pipeline (4 stages, synthesis, FunctionCalling + NativeThinking, 2 MCP servers, summarization, forced conclusion, replicas, chat — with 31 golden-file interaction details), FailureResilience (policy=any, exec summary fail-open), FailurePropagation (policy=all, fail-fast), Cancellation (investigation + chat), Timeout (session + chat), Concurrency (MaxConcurrentSessions enforcement), MultiReplica (cross-replica WS via NOTIFY/LISTEN). Bug fixes during testing: cancel handler for completed sessions with active chats, post-cancellation DB updates using `context.Background()`, agent status mapping from `ctx.Err()`, API startup wiring validation. New APIs: timeline endpoint (`GET /sessions/:id/timeline`), trace/observability endpoints (interaction list, LLM detail with conversation reconstruction, MCP detail). Infrastructure: `pkg/mcp/testing.go` (InjectSession, NewTestClientFactory), `test/database/` (SharedTestDB), auto-catchup on WebSocket subscribe, `AgentExecution.llm_provider` field, Makefile targets (test-unit, test-e2e, test-go, test-go-coverage).

**Phase 7: Dashboard** -- React 19 + TypeScript + Vite 7 + MUI 7 dashboard ported from old TARSy with hybrid approach (old visual layer, new data layer). Backend API extensions (7.0), foundation with auth/WebSocket/routing/Go static serving (7.1), session list with filters/pagination/localStorage persistence (7.2), alert submission with MCP override (7.3), session detail with conversation timeline/streaming/auto-scroll (7.4), follow-up chat (7.5), trace view with LLM/MCP interaction details (7.6), system status page with version/warning wiring (7.7), polish with cache headers (7.8). See `docs/archive/phase7-dashboard-plan.md` for detailed design.

Full design docs for completed phases are in `docs/archive/`.

**Runbook System (Phase 8.1)** -- ✅ DONE. GitHub integration for fetching runbook content from repositories. `runbook.Service` (`pkg/runbook/`) orchestrates resolution, caching, and listing. Per-alert runbook URL (submitted via API `runbook` field, stored on `AlertSession.runbook_url`) fetched via `GitHubClient` with blob→raw URL conversion and bearer token auth. In-memory TTL cache (`runbook.Cache`). URL validation (scheme + domain allowlist) at both API handler and service level. Resolution hierarchy: per-alert URL → default content (from config). Fail-open in executors: fetch failure falls back to default runbook. Config: `system.github.token_env` (env var name for GitHub token), `system.runbooks.repo_url` (GitHub tree URL for listing), `system.runbooks.cache_ttl`, `system.runbooks.allowed_domains`. `GET /api/v1/runbooks` endpoint lists available `.md` files from configured repo (recursive, via GitHub Contents API). Dashboard: Autocomplete dropdown for runbook URLs in ManualAlertForm. System warning when repo URL configured without GitHub token. E2E tests: runbook URL flow, invalid domain rejection, listing endpoint, default fallback.

**Multi-LLM Support (Phase 8.2)** -- ✅ DONE. Replaced LangChain stub with real `LangChainProvider` supporting OpenAI, Anthropic, xAI, Google (via LangChain), and VertexAI. Completely removed ReAct iteration strategy and `ReActController`; renamed `NativeThinkingController` → `FunctionCallingController` (shared by `native-thinking` and new `langchain` strategies). Both use native/structured tool calling. Deleted all text-based ReAct parsing (`react_parser.go`, `tools.go`), ReAct streaming code, ReAct prompt templates. Added shared `tool_names.py` utility for canonical↔API name encoding. LangChain provider features: streaming via `astream()`, `content_blocks` for thinking/reasoning, `bind_tools()` for function calling, model caching, retry with exponential backoff. Dashboard cleanup: removed dead `isReActResponse()`, `NATIVE_THINKING` constant. Four strategies remain: `native-thinking`, `langchain`, `synthesis`, `synthesis-native-thinking`.

**Slack Notifications (Phase 8.3)** -- ✅ DONE. Session lifecycle notifications via Slack using Block Kit. `slack.Service` (`pkg/slack/`) orchestrates delivery at two lifecycle points: session start (only for Slack-originated alerts with a `slack_message_fingerprint`) and terminal status (completed, failed, timed_out, cancelled). `slack.Client` wraps the `slack-go` SDK for `chat.postMessage` and `conversations.history`. Message templates in `message.go` use Block Kit with emoji status indicators and dashboard links; content truncated at 2900 chars. Fingerprint-based threading: `FindMessageByFingerprint()` searches last 24h channel history (case-insensitive, whitespace-normalized) and returns `thread_ts` for reply threading; `thread_ts` cached across worker lifecycle to avoid redundant lookups. `slack.NewService` returns nil when unconfigured (fail-closed creation); nil-receiver safe methods make caller code nil-check-free (same pattern as `MaskingService`). Fail-open delivery: Slack API errors are logged, never propagate. Config: `system.dashboard_url` (shared, default `http://localhost:8080`) + `system.slack.{enabled, token_env, channel}` with eager startup validation. E2E test: mock Slack HTTP server captures `chat.postMessage` calls; verifies start + terminal notifications, threading, disabled notifications, fail-open on API errors.

**Security & Containerization (Phase 9)** -- ✅ DONE. OAuth2-proxy integration for GitHub OAuth browser auth (cookie-based sessions, `X-Forwarded-User/Email` headers, custom sign-in page ported from old TARSy). WebSocket origin validation replacing `InsecureSkipVerify` with configurable `OriginPatterns` derived from `system.dashboard_url` + localhost variants. Security headers middleware (X-Frame-Options DENY, X-Content-Type-Options nosniff, Referrer-Policy, Permissions-Policy) and CORS middleware with `DashboardURL`-based origin allowlist. Multi-stage `Dockerfile` (Go builder + Node dashboard builder + Alpine runtime, non-root UID 65532, OpenShift GID 0 compat) and `llm-service/Dockerfile` (Python slim, same UID/GID pattern). gRPC health checking service added to llm-service (`grpcio-health-checking`, sets SERVING after init). Minimal `/health` endpoint (status + version + db/worker_pool checks only — no internals leaked). 4-service `podman-compose.yml` (postgres, llm-service, tarsy, oauth2-proxy) with health check chain. `make/containers.mk` with build/deploy/status/clean targets. OAuth2-proxy config template generation from `oauth.env`. Universal container images (same for compose and OpenShift). Config: `system.allowed_ws_origins` for additional WebSocket origins.

**Kubernetes/OpenShift Deployment (Phase 10)** -- ✅ DONE. Kustomize manifests (`deploy/kustomize/base/` + `overlays/development/`) deploying TARSy to OpenShift. Single-pod 4-container Deployment (tarsy + llm-service + oauth2-proxy + kube-rbac-proxy) replacing old TARSy's 3-Deployment architecture. kube-rbac-proxy sidecar for API client auth via Kubernetes ServiceAccount tokens (replaces old TARSy's custom JWT infrastructure). RBAC resources: `tarsy` ServiceAccount, `tarsy-kube-rbac-proxy` ClusterRole (TokenReview/SubjectAccessReview), `tarsy-api-client` ClusterRole for API access grants. Author extraction updated with `X-Remote-User` header support. Separate database Deployment with PVC (Recreate strategy; replaceable with managed DB). Services: `tarsy-web` (oauth2-proxy :4180), `tarsy-api` (kube-rbac-proxy :8443 with auto-generated serving cert), `tarsy-database`. Single Route with edge TLS termination. OpenShift Template for secrets (tarsy-secrets, database-secret, oauth2-proxy-secret, gcp-service-account-secret). ConfigMaps via `configMapGenerator` (tarsy-app-config, oauth2-config, oauth2-templates, tarsy-config). ImageStreams (tarsy, tarsy-llm). Health probes: HTTP for tarsy, native gRPC for llm-service, HTTP /ping for oauth2-proxy, TCP for kube-rbac-proxy. GitHub Actions CI workflows for quay.io (buildah-build + push-to-registry, path-triggered). `make/openshift.mk` with full workflow: build, push (with skopeo fallback), create-secrets, apply, deploy, redeploy, status, logs, clean, db-reset. Rolling update strategy (maxUnavailable: 0, maxSurge: 1). `terminationGracePeriodSeconds: 960` (alert timeout + buffer).

---

### Phase 11: Monitoring & Operations

- [ ] Prometheus metrics
- [ ] Structured logging
- [ ] Retention policies
- [ ] Cleanup service
- [ ] Cascade deletes

---

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
