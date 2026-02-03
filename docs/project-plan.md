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

### Phase 2: Core Infrastructure

**Database & Persistence**
- [ ] PostgreSQL integration (Go)
- [ ] Database models & repositories (Go)
- [ ] Alembic-style migrations (Go)
- [ ] Session/alert persistence

**Configuration System**
- [ ] YAML-based agent definitions
- [ ] YAML-based chain definitions
- [ ] YAML-based MCP server registry
- [ ] YAML-based LLM provider configuration
- [ ] Hierarchical config resolution

**Queue & Worker System**
- [ ] Database-backed job queue (Go)
- [ ] Session claim worker pattern (Go)
- [ ] Concurrency limits & backpressure
- [ ] Background worker lifecycle

---

### Phase 3: Agent Framework

**Base Agent Architecture**
- [ ] BaseAgent interface (Go/Python bridge)
- [ ] Agent lifecycle management
- [ ] Agent execution context
- [ ] Configuration-based agent instantiation

**Iteration Controllers**
- [ ] ReAct controller (Python LLM service)
- [ ] Native thinking controller (Python)
- [ ] Stage controller variants
- [ ] Synthesis controller
- [ ] Chat controller
- [ ] Final analysis controller

**Prompt System**
- [ ] Prompt builder framework (Python)
- [ ] Template system (Python)
- [ ] Component-based prompts (Python)
- [ ] Chain context injection

---

### Phase 4: MCP Integration

**MCP Client Infrastructure**
- [ ] MCP client factory (Python/Go)
- [ ] Transport layer (stdio/HTTP/SSE)
- [ ] Tool registry & discovery
- [ ] Error handling & recovery
- [ ] Health monitoring

**MCP Features**
- [ ] Custom MCP configuration per alert
- [ ] Built-in MCP servers (kubernetes, etc.)
- [ ] Tool result summarization
- [ ] MCP server health tracking

---

### Phase 5: Chain Execution

**Chain Orchestration**
- [ ] Chain registry (Go)
- [ ] Multi-stage execution (Go)
- [ ] Stage execution manager (Go)
- [ ] Data flow between stages
- [ ] Chain selection logic

**Parallel Execution**
- [ ] Parallel stage executor (Go)
- [ ] Result aggregation
- [ ] Synthesis agent invocation
- [ ] Replica & comparison parallelism

**Pause/Resume**
- [ ] Iteration limits & pausing
- [ ] Session state preservation
- [ ] Resume logic
- [ ] Force conclusion option

---

### Phase 6: Integrations

**Runbook System**
- [ ] GitHub integration (Go)
- [ ] Runbook fetching
- [ ] Per-chain runbook configuration
- [ ] Runbook caching

**Multi-LLM Support**
- [ ] LLM provider abstraction (Python)
- [ ] Provider registry (Python)
- [ ] OpenAI, Anthropic, xAI clients (Python)
- [ ] Google Search grounding (Python)

**Slack Notifications**
- [ ] Slack client (Go/Python)
- [ ] Notification templates
- [ ] Message threading/fingerprinting
- [ ] Configurable notifications

---

### Phase 7: Security & Data

**Authentication & Authorization**
- [ ] OAuth2-proxy integration
- [ ] Token validation
- [ ] GitHub OAuth flow
- [ ] Session/user tracking

**Data Masking**
- [ ] Masking service (Go)
- [ ] Kubernetes secret masker
- [ ] Regex-based maskers
- [ ] Alert payload sanitization

---

### Phase 8: History & Chat

**History System**
- [ ] History repository (Go)
- [ ] Timeline reconstruction
- [ ] Conversation retrieval
- [ ] Session querying & filtering

**Follow-up Chat**
- [ ] Chat service (Go)
- [ ] Chat agent (Python)
- [ ] Context preservation
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
- [ ] Metrics collection
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
- [ ] Pause/resume UI controls

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

### Phase 11: Deployment & DevOps

**Containerization**
- [ ] Multi-stage Docker builds
- [ ] Container orchestration (docker-compose)
- [ ] Service health checks
- [ ] Volume management

**Kubernetes/OpenShift**
- [ ] Kustomize manifests
- [ ] Service deployments
- [ ] ConfigMaps & secrets
- [ ] Routes/ingress
- [ ] ImageStreams

**CI/CD**
- [ ] GitHub Actions workflows
- [ ] Test automation
- [ ] Build & push images
- [ ] Deployment automation

---

### Phase 12: Testing & Quality

**Backend Tests**
- [ ] Unit tests (Go)
- [ ] Integration tests (Go)
- [ ] E2E tests (Go)
- [ ] Mock infrastructure

**Frontend Tests**
- [ ] Component tests
- [ ] Integration tests
- [ ] E2E tests

**Test Infrastructure**
- [ ] Test utilities
- [ ] Fixtures & mocks
- [ ] CI integration

---

## Related Documents

- [README.md](../README.md) - Project overview and setup instructions
- [Architecture Proposal](../../tarsy-bot/temp/go-python-split-proposal.md) - Original architecture proposal (in tarsy-bot repo)
