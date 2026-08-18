@AGENTS.md

Shared always-on rules are imported from `AGENTS.md`. This file is the architecture map.

## Project Overview

TARSy (Thoughtful Alert Response System) is an intelligent SRE system that processes alerts through parallel agent chains using MCP servers for multi-stage incident analysis and automated remediation. Hybrid Go + Python architecture: Go orchestrator handles business logic, session management, and real-time streaming; a stateless Python service manages LLM interactions over gRPC.

## Architecture

### Three-service split

- **Go backend** (`cmd/tarsy/`, `pkg/`) -- Orchestrator, API server (Echo v5), worker pool, event streaming. Port 8080.
- **Python LLM service** (`llm-service/`) -- Stateless gRPC server routing to LLM providers (Gemini, OpenAI, Anthropic, xAI, Vertex AI). Port 50051.
- **React dashboard** (`web/dashboard/`) -- React 19 + TypeScript + Vite 7 + MUI 7 SPA. Port 5173.

### Key Go packages (pkg/)

| Package | Purpose |
|---------|---------|
| `agent/` | Agent framework: IteratingController, SingleShotController, ScoringController, prompt building, tool execution |
| `api/` | HTTP handlers (alerts, sessions, chat, review, memory, scoring, trace, timeline, system) |
| `config/` | YAML config loading with registries for agents, chains, MCP servers, providers, skills |
| `database/` | Ent client wrapper, migrations, GIN index hooks |
| `events/` | Real-time event pub via PostgreSQL LISTEN/NOTIFY + WebSocket |
| `queue/` | Worker pool with database-backed job claiming (FOR UPDATE SKIP LOCKED) |
| `mcp/` | MCP client factory and health monitoring |
| `memory/` | Investigation memory: pgvector embeddings, hybrid retrieval (semantic + keyword + RRF) |
| `services/` | Domain services (AlertService, SessionService, ChatService, ScoringService, MemoryService, etc.) |
| `masking/` | Data masking (K8s Secret detection + regex patterns) |

### Data flow

1. Alert received via API -> queued in DB
2. Worker claims session (FOR UPDATE SKIP LOCKED) -> executes agent chain
3. Each agent iteration: Go builds conversation -> gRPC to Python LLM service -> streams response chunks back
4. Tool calls executed by Go via MCP servers -> results appended -> next iteration
5. Real-time updates via PostgreSQL LISTEN/NOTIFY -> WebSocket to dashboard

### Database

- **PostgreSQL 17 + pgvector** for vector similarity search
- **Ent ORM** for type-safe queries; schemas in `ent/schema/`
- **Atlas** for migration authoring; custom hooks add GIN indexes and partial unique indexes
- Migrations auto-applied on startup via `pkg/database/`

### Proto/gRPC

Single service definition in `proto/llm_service.proto` with streaming `Generate` RPC. Generated code lands in `proto/` (Go) and `llm-service/llm_proto/` (Python).

### Configuration

Config files in `deploy/config/`: `tarsy.yaml` (agents, chains, MCP servers), `llm-providers.yaml` (provider configs), `.env` (secrets/env vars). YAML files support Go template interpolation (`{{.VAR_NAME}}`).

### E2E test infrastructure

Tests in `test/e2e/` use a `TestApp` harness that boots a complete TARSy instance with Testcontainers PostgreSQL, a `ScriptedLLMClient` for deterministic LLM responses, and in-memory MCP servers. Test configs in `test/e2e/testdata/configs/`.
