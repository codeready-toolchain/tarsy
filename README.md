[![CI](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml/badge.svg)](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/codeready-toolchain/tarsy/graph/badge.svg)](https://codecov.io/gh/codeready-toolchain/tarsy)

<div align="center">
  <img src="./docs/img/TARSy-logo.png" alt="TARSy" width="100"/>
</div>

**TARSy** (Thoughtful Alert Response System) is an intelligent SRE system that automatically processes alerts through parallel agent chains, using MCP (Model Context Protocol) servers and optional runbooks for comprehensive multi-stage incident analysis and automated remediation.

This is the Go-based hybrid rewrite of TARSy, replacing the [original Python implementation](https://github.com/codeready-toolchain/tarsy-bot) (now deprecated). The new architecture splits responsibilities between a Go orchestrator and a stateless Python LLM service for better performance, type safety, and scalability.

[tarsy-gh-demo.webm](https://github.com/user-attachments/assets/dae0e409-ef7f-46a6-b390-dbf287497963)

## Documentation

- **[README.md](README.md)** -- This file: project overview and quick start
- **[docs/architecture-overview.md](docs/architecture-overview.md)** -- High-level architecture, components, and processing flow
- **[docs/functional-areas-design.md](docs/functional-areas-design.md)** -- Detailed design of each functional area with file paths and interfaces
- **[docs/slack-integration.md](docs/slack-integration.md)** -- Slack notification setup, configuration, and threading
- **[deploy/README.md](deploy/README.md)** -- Deployment and configuration guide
- **[deploy/config/README.md](deploy/config/README.md)** -- Configuration reference

## Prerequisites

- **Go 1.25+** -- Backend orchestrator
- **Python 3.13+** -- LLM service runtime
- **Node.js 24+** -- Dashboard development and build tools
- **uv** -- Modern Python package manager
  - Install: `curl -LsSf https://astral.sh/uv/install.sh | sh`
- **Podman** (or Docker) -- Container runtime (used for PostgreSQL)

**Optional** (not needed for `make dev`):
- **protoc** -- Protocol Buffers compiler (`make proto-generate`)
- **golangci-lint** -- Go linter (`make lint`, `make check-all`)
- **Atlas** -- Migration authoring (`make migrate-create`)

> **Quick Check**: Run `make doctor` to verify all prerequisites are installed.

## Quick Start

### Development Mode

```bash
# 1. Install dependencies and bootstrap config
make setup

# 2. Configure environment variables
cp deploy/config/.env.example deploy/config/.env
# Edit deploy/config/.env and set at minimum:
#   - GOOGLE_API_KEY   (required — used by built-in Gemini providers)
#   - KUBECONFIG       (path to your kubeconfig, for Kubernetes investigation)

# 3. Start everything (database, backend, LLM service, dashboard)
make dev
```

**Services will be available at:**
- **TARSy Dashboard**: http://localhost:5173
- **Backend API**: http://localhost:8080
- **LLM Service**: gRPC on port 50051

**Stop all services:** `make dev-stop`

### Container Deployment (Production-like)

For containerized and OpenShift deployment with OAuth authentication, see **[deploy/README.md](deploy/README.md)**.

## Key Features

### Agent Architecture
- **Configuration-Based Agents**: Deploy new agents and chain definitions via YAML without code changes
- **Parallel Agent Execution**: Run multiple agents concurrently with automatic synthesis. Supports multi-agent, replica, and comparison parallelism for A/B testing providers or strategies
- **Dynamic Orchestration with Sub-Agents**: Orchestrator agents use LLM reasoning to dispatch specialized sub-agents at runtime, react to partial results, and synthesize findings -- replacing static parallel chains with adaptive, multi-phase investigation flows
- **MCP Server Integration**: Agents dynamically connect to MCP servers for domain-specific tools (kubectl, database clients, monitoring APIs)
- **Multi-LLM Provider Support**: OpenAI, Google Gemini, Anthropic, xAI, Vertex AI -- configure and switch via YAML with native thinking mode
- **Automated Actions**: Action agents (`type: action`) evaluate investigation findings and execute remediation via MCP tools with auto-injected safety guardrails -- no custom safety prompt required
- **Automatic Provider Fallback**: When a primary LLM provider fails, automatically switches to the next configured fallback provider with error-code-aware triggers and adaptive streaming timeouts
- **Agent Skills**: Modular, reusable domain knowledge (SKILL.md files) that agents discover at startup and load on-demand via a `load_skill` tool -- or inject directly into the system prompt via `required_skills`. Zero config by default; all skills are available to all agents
- **Force Conclusion**: Automatic conclusion at iteration limits with hierarchical configuration (system, chain, stage, or agent level)

### Investigation & Analysis
- **Flexible Alert Processing**: Accept arbitrary text payloads from any monitoring system
- **Optional Runbook Integration**: Fetch supplemental guidance from GitHub repositories to steer agent behavior
- **Data Masking**: Hybrid masking combining structural analysis (Kubernetes Secrets) with regex patterns to protect sensitive data
- **Tool Result Summarization**: Enabled by default — LLM-powered summarization of verbose MCP outputs (>5K tokens) to reduce token usage and improve reasoning

### Observability & Operations
- **Prometheus Metrics**: `/metrics` endpoint exposing session lifecycle, LLM performance, MCP tool reliability, worker pool health, HTTP request patterns, and WebSocket connections with custom histogram buckets
- **SRE Dashboard**: Real-time monitoring with live LLM streaming and interactive chain timeline visualization
- **Full-Text Search**: Dashboard search extends to timeline event content via PostgreSQL FTS; in-session search with highlight and navigation for terminated sessions
- **Session Scoring**: Automated quality evaluation of completed investigations (0–100 score across four categories) with missing tools reports, re-scoring via API, and a dedicated scoring dashboard page
- **Investigation Memory**: Cross-session learning — the Reflector extracts discrete learnings after each scored investigation; relevant memories are auto-injected into future investigations via pgvector semantic retrieval. Human review feedback refines memory quality over time. Agents can also search memories on-demand via the `recall_past_investigations` tool
- **Triage Workflow**: Post-investigation review lifecycle with self-claim assignment, complete with `quality_rating` and `action_taken`, and a grouped Triage view alongside the session list — real-time updates via WebSocket
- **Follow-up Chat**: Continue investigating after sessions complete with full context and tool access
- **Slack Notifications**: Automatic notifications with thread-based message grouping via fingerprint matching
- **Comprehensive Audit Trail**: Full visibility into chain processing with stage-level timeline and trace views

## Architecture

TARSy uses a hybrid Go + Python architecture where the Go orchestrator handles all business logic, session management, and real-time streaming, while a stateless Python service manages LLM interactions over gRPC.

```
                           ┌───────────────┐
                           │  MCP Servers  │
                           │  (kubectl,    │
                           │   monitoring) │
                           └───────┬───────┘
                                   │
┌──────────┐  WebSocket  ┌─────────┴──────────┐  gRPC   ┌──────────────┐
│ Browser  │◄───────────►│   Go Orchestrator  │◄───────►│  Python LLM  │
│ (React)  │   HTTP      │   (Echo + Ent)     │ Stream  │  Service     │
└──────────┘             └─────────┬──────────┘         └──────┬───────┘
                                   │                           │
                               PostgreSQL              Gemini / OpenAI /
                               (Ent ORM)               Anthropic / xAI /
                                                           Vertex AI
```

### How It Works

1. **Alert arrives** from monitoring systems with flexible text payload
2. **Chain selected** based on alert type -- static parallel chains or dynamic orchestrator
3. **Runbook injected** (optional) -- if configured, fetches supplemental guidance from GitHub to steer agent behavior
4. **Skills loaded** -- required skills are injected into the system prompt; on-demand skills are presented as a catalog and loaded via `load_skill` during the investigation
5. **Agents investigate** -- static chains launch parallel agents per stage; orchestrator agents dynamically dispatch sub-agents based on LLM reasoning, react to partial results, and dispatch follow-ups
6. **Results synthesized** -- static chains use a dedicated SynthesisAgent; orchestrators synthesize within the same execution as results arrive
7. **Forced conclusion** at iteration limits -- one final LLM call produces the best analysis with available data (no pause/resume)
8. **Automated actions** (optional) -- action agents evaluate findings and execute justified remediation with built-in safety guardrails
9. **Comprehensive analysis** provided to engineers with actionable recommendations
10. **Session scored** (if enabled) -- async quality evaluation with score, analysis, and missing tools report
11. **Memories extracted** (if enabled) -- Reflector analyzes the investigation and scoring results to extract reusable learnings into the memory store
12. **Session enters triage** -- automatically queued as "Needs Review" for human triage (claim, complete, dismiss)
13. **Follow-up chat available** after investigation completes
14. **Full audit trail** captured with stage-level detail and sub-agent trace trees

### Components

| Component | Location | Tech |
|-----------|----------|------|
| **Go Orchestrator** | `cmd/tarsy/`, `pkg/` | Go 1.25, Echo v5, Ent ORM, gRPC |
| **Python LLM Service** | `llm-service/` | Python 3.13, gRPC, Gemini, LangChain |
| **Dashboard** | `web/dashboard/` | React 19, TypeScript, Vite 7, MUI 7 |
| **Database** | `ent/` | PostgreSQL 17, Ent ORM with migrations |
| **Proto Definitions** | `proto/` | Protocol Buffers (gRPC service contracts) |
| **Deployment** | `deploy/` | Podman Compose, OAuth2-proxy, Nginx |
| **E2E Tests** | `test/e2e/` | Testcontainers, real PostgreSQL, WebSocket |

## API Endpoints

### Core
- `POST /api/v1/alerts` -- Submit an alert for processing (queue-based, returns `session_id`)
- `GET /api/v1/alert-types` -- Supported alert types
- `GET /api/v1/ws` -- WebSocket for real-time progress updates with channel subscriptions
- `GET /health` -- Health check with service status and queue metrics
- `GET /metrics` -- Prometheus metrics endpoint

### Sessions
- `GET /api/v1/sessions` -- List sessions with filtering and pagination
- `GET /api/v1/sessions/active` -- Currently active sessions
- `GET /api/v1/sessions/filter-options` -- Available filter values
- `GET /api/v1/sessions/:id` -- Session detail with chronological timeline
- `GET /api/v1/sessions/:id/summary` -- Session statistics, token usage, chain stats, and score (if available)
- `GET /api/v1/sessions/:id/status` -- Lightweight polling status (id, status, final_analysis, executive_summary, error_message)
- `POST /api/v1/sessions/:id/cancel` -- Cancel an active or paused session

### Chat
- `POST /api/v1/sessions/:id/chat/messages` -- Send message (AI response streams via WebSocket)

### Scoring
- `GET /api/v1/sessions/:id/score` -- Get latest session score (analysis, missing tools report, metadata)
- `POST /api/v1/sessions/:id/score` -- Trigger (re-)scoring (202 Accepted, 409 if already in progress)

### Review & Triage
- `PATCH /api/v1/sessions/review` -- Review workflow transition for one or more sessions (claim, unclaim, complete, reopen, update_feedback)
- `GET /api/v1/sessions/:id/review-activity` -- Review activity audit log
- `GET /api/v1/sessions/triage/:group` -- Per-group paginated triage view

### Memory
- `GET /api/v1/sessions/:id/memories` -- Memories extracted from this session
- `GET /api/v1/sessions/:id/injected-memories` -- Memories auto-injected into this session
- `GET /api/v1/memories` -- List all memories (paginated, filterable by category, valence, deprecated)
- `GET /api/v1/memories/:id` -- Memory detail
- `PATCH /api/v1/memories/:id` -- Edit memory (content, category, valence, deprecated)
- `DELETE /api/v1/memories/:id` -- Delete memory

### Trace & Observability
- `GET /api/v1/sessions/:id/timeline` -- Session timeline events
- `GET /api/v1/sessions/:id/trace` -- List LLM and MCP interactions
- `GET /api/v1/sessions/:id/trace/llm/:interaction_id` -- LLM interaction detail with conversation reconstruction
- `GET /api/v1/sessions/:id/trace/mcp/:interaction_id` -- MCP interaction detail

### System
- `GET /api/v1/runbooks` -- List available runbooks from configured GitHub repo
- `GET /api/v1/system/warnings` -- Active system warnings
- `GET /api/v1/system/mcp-servers` -- Available MCP servers and tools
- `GET /api/v1/system/default-tools` -- Default tool configuration

## Container Architecture

The containerized deployment provides a production-like environment:

```
Browser → OAuth2-Proxy (8080) → Go Backend (8080) → LLM Service (gRPC)
                                      ↓
                                 PostgreSQL
```

- **OAuth2 Authentication**: GitHub OAuth integration via oauth2-proxy
- **PostgreSQL Database**: Persistent storage with auto-migration
- **Production Builds**: Optimized multi-stage container images
- **Security**: All API endpoints protected behind authentication

## Development

### Adding New Components

- **Alert Types**: Define in `deploy/config/tarsy.yaml` -- no code changes required
- **MCP Servers**: Add to `tarsy.yaml` with stdio, HTTP, or SSE transport
- **Agents**: Create Go agent classes extending BaseAgent, or define configuration-based agents in YAML
- **Chains**: Define multi-stage workflows in YAML with parallel execution support
- **Skills**: Drop a `SKILL.md` file into `deploy/config/skills/<name>/` -- all agents see it automatically. Scope with `skills` (on-demand allowlist) and/or `required_skills` (always prompt-injected). The two fields are independent: required skills are auto-excluded from the on-demand catalog. See [deploy/config/README.md](deploy/config/README.md#agent-skills)
- **LLM Providers**: Built-in providers work out-of-the-box. Add custom providers via `deploy/config/llm-providers.yaml`

### Running Tests

```bash
make test               # Run all tests (Go + Python + Dashboard)
make test-go            # Go tests only
make test-unit          # Go unit tests
make test-e2e           # Go end-to-end tests (requires Docker/Podman)
make test-llm           # Python LLM service tests
make test-dashboard     # Dashboard tests
```

### Useful Commands

```bash
make doctor             # Check if dev prerequisites are installed
make check-all          # Format, build, lint, and run all tests
make help               # Show all available commands
make fmt                # Format code (Go + Python)
make lint               # Run linters (Go)
make ent-generate       # Regenerate Ent ORM code
make proto-generate     # Regenerate protobuf/gRPC code
make db-psql            # Connect to PostgreSQL shell
make db-reset           # Reset database
```

## Troubleshooting

### Database connection issues
- Verify PostgreSQL is running: `make db-status`
- Check PostgreSQL logs: `make db-logs`
- Connect manually: `make db-psql`
- Reset if corrupted: `make db-reset`
