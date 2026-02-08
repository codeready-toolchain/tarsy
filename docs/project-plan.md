# TARSy Project Status

## Project Goal

**New TARSy Implementation: Go & Python Split Architecture**

This project represents a reimplementation of TARSy with a modernized architecture that separates concerns:

- **Go Orchestrator**: High-performance backend for HTTP/WebSocket API, session management, orchestration, and business logic
- **Python LLM Service**: Specialized microservice handling LLM interactions (Gemini, with future support for other providers)

### Background

The original TARSy implementation (`/home/igels/Projects/AI/tarsy-bot`) is a 100% Python-based backend. While functional, this project aims to create a production-ready TARSy with a hybrid architecture that:

- Leverages Go's strengths in concurrency, performance, and API handling
- Keeps Python for its rich LLM ecosystem and flexibility
- Uses gRPC for efficient inter-service communication with streaming support
- Provides a clearer separation of concerns for future scalability
- Maintains the same functionality as the existing TARSy with a modernized architecture

## Development Roadmap

### ✅ Phase 1: Proof of Concept (COMPLETED)

**Goal**: Validate the Go-Python split architecture with minimal implementation

**Completed Items**:

- [x] Python gRPC LLM service with Gemini native thinking client
- [x] Go orchestrator with HTTP/WebSocket API
- [x] In-memory session management (no database)
- [x] End-to-end streaming (Gemini → Python gRPC → Go → WebSocket → Browser)
- [x] gRPC communication between Go and Python services
- [x] Primitive frontend (HTML/CSS/JS)
- [x] Configuration management via `.env` files
- [x] Basic scripts for starting/stopping services
- [x] Documentation (README, QUICKSTART)

**Validation**: 
- ✅ Streaming works end-to-end
- ✅ Concurrency works (multiple sessions)
- ✅ Architecture is simple and understandable
- ✅ Code demonstrates Go-Python integration clearly

---

### ✅ Phase 2: Core Infrastructure

**Database & Persistence** ✅  — See `docs/phase2-database-persistence-design.md`
- [x] PostgreSQL integration (Go)
- [x] Database models & repositories (Go)
- [x] Alembic-style migrations (Go)
- [x] Session/alert persistence

**Configuration System** ✅ — See `docs/phase2-configuration-system-design.md`
- [x] YAML-based agent definitions
- [x] YAML-based chain definitions
- [x] YAML-based MCP server registry
- [x] YAML-based LLM provider configuration
- [x] Hierarchical config resolution
- [x] Built-in + user-defined configuration (singleton pattern)
- [x] Environment variable interpolation
- [x] Comprehensive validation with clear error messages
- [x] In-memory registries with thread-safe access
- [x] Example configuration files
- [x] Integration with main.go and services
- [x] Proto file updated for LLM config passing
- [x] Comprehensive test suite (80%+ coverage)

**Web Framework**: Echo v5 (labstack/echo) — chosen over Gin for cleaner error-return handlers, lighter dependency tree, built-in middleware (CORS, RequestID, Timeout), and consistency with other team projects. WebSocket via coder/websocket.

**Queue & Worker System** ✅ — See `docs/phase2-queue-worker-system-design.md`
- [x] Database-backed job queue (Go)
- [x] Session claim worker pattern (Go)
- [x] Concurrency limits & backpressure
- [x] Background worker lifecycle
- [x] Session cancellation API (Go) — `POST /api/v1/sessions/{id}/cancel`, context-based cancellation propagation
- **Design Phase**: ✅ Complete — See `docs/phase2-queue-worker-system-design.md` and `docs/phase2-queue-worker-system-questions.md`

---

### Architecture: Go/Python Boundary

**Critical architectural decision** for all phases going forward:

**Go owns all orchestration** — agent lifecycle, iteration control loops, MCP tool execution, prompt building, conversation management, chain execution, state persistence, WebSocket streaming.

**Python is a thin, stateless LLM API proxy** — receives messages + config via gRPC, calls LLM provider API (Gemini, OpenAI, etc.), streams response chunks back (text, thinking, tool calls). No state, no orchestration, no MCP.

This means: iteration controllers are Go, prompt building is Go, MCP client is Go. Python's role is narrow by design — it exists solely because LLM provider SDKs have best support in Python.

---

### Phase 3: Agent Framework

**3.1: Base Agent Architecture** ✅ — See `docs/phase3-base-agent-architecture-design.md`
- [x] Proto/gRPC evolution (remove PoC fields, add tool calls, tool definitions, usage metadata)
- [x] Python LLM service cleanup (production-quality single provider, new Generate RPC)
- [x] Agent interface & lifecycle (Go)
- [x] Iteration controller interface (Go)
- [x] Session executor framework (Go — replaces stub)
- [x] Agent execution context (Go)
- [x] Configuration resolution at runtime (defaults → chain → stage → agent)
- [x] Conversation management (Go — message building, tool call/result flow)
- [x] Basic single-call controller for validation

**3.2: Iteration Controllers (Go)** — See `docs/phase3-iteration-controllers-design.md`
- **Design Phase**: 🔄 In Progress — See `docs/phase3-iteration-controllers-design.md` and `docs/phase3-iteration-controllers-questions.md`
- [ ] ReAct controller (text-based tool parsing, observation loop)
- [ ] Native thinking controller (Gemini function calling, structured tool calls)
- ~~Stage controller variants (react-stage, react-final-analysis) — dropped, never used in old TARSy production. Strategy pattern allows adding new controllers later if needed.~~
- [x] Cleanup: remove `react-stage` and `react-final-analysis` from Phase 2 code (enums, config examples, validation, built-in configs) — **Complete**: No references found in codebase
- [ ] Synthesis controller (tool-less, single LLM call)
- [ ] Chat support via prompt composition (reuse ReAct/NativeThinking controllers; investigation context + chat history handled in Phase 3.3 prompt builder — no separate ChatReActController or ChatNativeThinkingController)
- ~~Final analysis controller — dropped, not a real strategy. Investigation agents (ReAct/NativeThinking) naturally produce final answers; synthesis handles parallel result merging.~~

**3.2.1: Gemini Native Tool Timeline Events (Go + Python)**
> Deferred from Phase 3.2. Gemini provides built-in native tools (`google_search`, `code_execution`, `url_context`) that produce results inline in the response stream. These results need to be surfaced to users via timeline events. Currently, code execution is collected in `LLMResponse.CodeExecutions` and stored in `LLMInteraction.response_metadata` (debugging only) — not shown to users. Google Search grounding and URL context results are not captured from the stream at all.

**What exists today:**
- Config: `google_search`, `code_execution`, `url_context` enums in `pkg/config/enums.go`
- Proto: `CodeExecutionDelta` message (code + result fields), `native_tools` map in `LLMConfig`
- Python: `google_native.py` streams `CodeExecutionDelta` for `executable_code` and `code_execution_result` parts; enables `GoogleSearch`, `ToolCodeExecution`, `ToolUrlContext` when no MCP tools present
- Go: `CodeExecutionChunk` type collected by `collectStream` into `LLMResponse.CodeExecutions`
- **Missing**: No timeline event types, no controller logic to create events, no grounding/URL context streaming

**What needs to be done:**
- [ ] Add timeline event types to Ent schema (`ent/schema/timelineevent.go`):
  - `code_execution` — Gemini-generated code + execution output
  - `google_search_result` — Google Search grounding results (web references, sources)
  - `url_context_result` — URL content fetching results
- [ ] Python: Extract Google Search grounding metadata from Gemini response (`GroundingMetadata` — contains `grounding_chunks`, `search_entry_point`, web references). Stream as a new proto delta type.
- [ ] Python: Extract URL context results from Gemini response. Stream as a new proto delta type.
- [ ] Proto: Add `GroundingDelta` and/or `UrlContextDelta` messages to `llm_service.proto`
- [ ] Go: Add corresponding chunk types (`GroundingChunk`, `UrlContextChunk`) and collect in `LLMResponse`
- [ ] Controllers: Create timeline events for code execution, grounding, URL context — same "create when content arrives" pattern as other events
- [ ] Note: Native tools are suppressed when MCP tools are present (mutual exclusivity, handled in Python). Only relevant for Native Thinking controller.

**3.3: Prompt System (Go)**
- [ ] Prompt builder framework
- [ ] Template system (Go text/template or string builders)
- [ ] Component-based prompts (alert section, runbook section, tool instructions)
- [ ] Chain context injection (previous stage results formatting)
- [ ] Three-tier instruction composition (general → MCP server → custom)

**3.4: Real-time Streaming**
- [ ] WebSocket endpoint (Echo + coder/websocket)
- [ ] PostgreSQL NOTIFY listener for cross-pod event delivery
- [ ] Real-time TimelineEvent streaming to frontend
- [ ] Frontend event protocol (create → stream chunks → complete)

---

### Phase 4: MCP Integration

**MCP Client Infrastructure (Go)**
- [ ] MCP client implementation (Go — uses `mark3labs/mcp-go` or similar)
- [ ] Transport layer — stdio (subprocess via `os/exec`), HTTP, SSE
- [ ] Tool registry & discovery (list tools from MCP servers)
- [ ] Error handling & recovery (retry, session recreation)
- [ ] Per-session MCP client isolation
- [ ] MCP server health monitoring

**Data Masking** (moved from Phase 7 — required for MCP tool results)
- [ ] Masking service (Go)
- [ ] Regex-based maskers (15 patterns defined in builtin.go)
- [ ] MCP tool result masking integration
- [ ] Alert payload sanitization

**MCP Features**
- [ ] Custom MCP configuration per alert (mcp_selection override)
- [ ] Tool result summarization (LLM-based, configurable threshold)
- [ ] MCP server health tracking

**Note on MCP servers**: TARSy does not embed MCP servers. It connects to external MCP servers (e.g., `npx -y kubernetes-mcp-server@0.0.54`) via stdio subprocess, HTTP, or SSE transports. The stdio transport in Go uses `os/exec.Cmd` with stdin/stdout pipes — straightforward and well-supported.

---

### Phase 5: Chain Execution

**Chain Orchestration (Go)**
- [ ] Chain orchestrator — uses existing ChainRegistry from Phase 2 config
- [ ] Multi-stage sequential execution
- [ ] Stage execution manager (create Stage + AgentExecution records)
- [ ] Lazy context building (Agent.BuildStageContext)
- [ ] Data flow between stages (previous stage context → next stage prompt)

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

### Phase 6: Integrations

**Runbook System (Go)**
- [ ] GitHub integration
- [ ] Runbook fetching & caching
- [ ] Per-chain runbook configuration

**Multi-LLM Support (Python)**
- [ ] LLM provider abstraction in Python service
- [ ] OpenAI, Anthropic, xAI client implementations
- [ ] Google Search grounding support
- [ ] VertexAI support

**Slack Notifications (Go)**
- [ ] Slack client
- [ ] Notification templates
- [ ] Message threading/fingerprinting
- [ ] Configurable notifications

---

### Phase 7: Security

**Authentication & Authorization**
- [ ] OAuth2-proxy integration
- [ ] Token validation
- [ ] GitHub OAuth flow
- [ ] Session/user tracking

**Advanced Data Masking**
- [ ] Kubernetes secret masker (code-based structural parser)
  - [ ] Parse YAML/JSON structures
  - [ ] Distinguish between K8s Secrets (mask) vs ConfigMaps (don't mask)
  - [ ] Integrate with masking pattern groups

---

### Phase 8: History & Chat

**History System (Go)**
- [ ] History repository
- [ ] Timeline reconstruction
- [ ] Conversation retrieval
- [ ] Session querying & filtering

**Follow-up Chat (Go + Python LLM)**
- [ ] Chat service (Go orchestration)
- [ ] Chat agent with investigation context (Go)
- [ ] Context preservation (lazy context building)
- [ ] Multi-user support
- [ ] Chat-investigation timeline merging

---

### Phase 9: Monitoring & Operations

**System Health**
- [ ] Health check endpoint enhancements
- [ ] System warnings service
- [ ] MCP health monitoring
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

### Phase 10: Dashboard Enhancement

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

**Note on testing**: Each phase includes its own test suite (unit + integration). There is no separate testing phase — testing is continuous. Phase 2 established the pattern with 80%+ coverage and testcontainers-based integration tests.

---

## Related Documents

- [README.md](../README.md) - Project overview and setup instructions
- [Architecture Proposal](../../tarsy-bot/temp/go-python-split-proposal.md) - Original architecture proposal (in tarsy-bot repo)
- **Phase 2 Design**:
  - [Database & Persistence Design](phase2-database-persistence-design.md)
  - [Database Schema Questions](phase2-database-schema-questions.md)
  - [Configuration System Design](phase2-configuration-system-design.md)
  - [Configuration System Questions](phase2-configuration-system-questions.md)
  - [Queue & Worker System Design](phase2-queue-worker-system-design.md)
  - [Queue & Worker System Questions](phase2-queue-worker-system-questions.md)
- **Phase 3 Design**:
  - [Base Agent Architecture Design](phase3-base-agent-architecture-design.md)
  - [Base Agent Architecture Questions](phase3-base-agent-architecture-questions.md)
