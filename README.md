[![CI](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml/badge.svg)](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/codeready-toolchain/tarsy/graph/badge.svg)](https://codecov.io/gh/codeready-toolchain/tarsy)

<div align="center">
  <img src="./docs/img/TARSy-logo.png" alt="TARSy" width="100"/>
</div>

**TARSy** (Thoughtful Alert Response System) is an intelligent SRE system that automatically processes alerts through sequential agent chains, retrieves runbooks, and uses MCP (Model Context Protocol) servers to gather system information for comprehensive multi-stage incident analysis.

This is the Go-based hybrid rewrite of TARSy, replacing the [original Python implementation](https://github.com/codeready-toolchain/tarsy-bot) (now deprecated). The new architecture splits responsibilities between a Go orchestrator and a stateless Python LLM service for better performance, type safety, and scalability.

[tarsy-gh-demo.webm](https://github.com/user-attachments/assets/dae0e409-ef7f-46a6-b390-dbf287497963)

## Documentation

- **[README.md](README.md)** -- This file: project overview and quick start
- **[docs/architecture-context.md](docs/architecture-context.md)** -- Cumulative architecture: interfaces, patterns, decisions, tech stack
- **[deploy/README.md](deploy/README.md)** -- Deployment and configuration guide
- **[deploy/config/README.md](deploy/config/README.md)** -- Configuration reference

## Prerequisites

### For Development Mode
- **Go 1.25+** -- Backend orchestrator
- **Python 3.13+** -- LLM service runtime
- **Node.js 18+** -- Dashboard development and build tools
- **uv** -- Modern Python package manager
  - Install: `curl -LsSf https://astral.sh/uv/install.sh | sh`
- **PostgreSQL 17+** -- Or Podman/Docker for local development
- **protoc** -- Protocol Buffers compiler (for gRPC code generation)

### For Container Deployment (Additional)
- **Podman** (or Docker) -- Container runtime
- **podman-compose** -- Multi-container application management
  - Install: `pip install podman-compose`

> **Quick Check**: Run `make check` to verify your development environment.

## Quick Start

### Development Mode

```bash
# 1. Install all dependencies (Go + Python + Dashboard)
make setup

# 2. Configure environment (REQUIRED)
cp deploy/config/.env.example deploy/config/.env
# Edit deploy/config/.env and set:
#   - GOOGLE_API_KEY (get from https://aistudio.google.com/app/apikey)
#   - DB_PASSWORD

# 3. Start everything (database, backend, LLM service, dashboard)
make dev
```

**Services will be available at:**
- **TARSy Dashboard**: http://localhost:5173
- **Backend API**: http://localhost:8080
- **LLM Service**: gRPC on port 50051

**Stop all services:** `make dev-stop`

### Container Deployment (Production-like)

For production-like testing with containerized services, authentication, and database:

```bash
# 1. Install dependencies
make setup

# 2. Configure environment and OAuth (REQUIRED)
# Edit deploy/config/.env for API keys
# Edit deploy/config/oauth.env for GitHub OAuth (see deploy/config/README.md)

# 3. Deploy the complete stack
make containers-deploy        # Preserves database data (recommended)
# OR for a fresh start:
make containers-deploy-fresh  # Clean rebuild including database
```

**Services will be available at:**
- **TARSy Dashboard**: http://localhost:8080 (with OAuth authentication)
- **Backend API**: http://localhost:8080/api (protected by OAuth2-proxy)
- **PostgreSQL Database**: localhost:5432

**Container Management:**

```bash
make containers-status        # Check running services
make containers-logs          # View all logs
make containers-logs-tarsy    # View TARSy backend logs
make containers-stop          # Stop containers
make containers-clean         # Remove all containers and data
```

## Key Features

- **Configuration-Based Agents**: Deploy new agents and chain definitions via YAML without code changes
- **Flexible Alert Processing**: Accept arbitrary text payloads from any monitoring system
- **Chain-Based Agent Architecture**: Specialized agents with domain-specific tools and AI reasoning working in coordinated stages
- **Parallel Agent Execution**: Run multiple agents concurrently with automatic synthesis. Supports multi-agent parallelism, replica parallelism, and comparison parallelism for A/B testing providers or strategies
- **MCP Server Integration**: Agents dynamically connect to MCP servers for domain-specific tools (kubectl, database clients, monitoring APIs). Add new servers via configuration
- **Multi-LLM Provider Support**: OpenAI, Google Gemini, Anthropic, xAI, Vertex AI -- configure and switch via YAML. Native thinking mode for Gemini 2.5+. LangChain-based provider system for extensibility
- **GitHub Runbook Integration**: Automatic retrieval and inclusion of relevant runbooks from GitHub repositories per agent chain
- **SRE Dashboard**: Real-time monitoring with live LLM streaming and interactive chain timeline visualization
- **Follow-up Chat**: Continue investigating after sessions complete with full context and tool access
- **Force Conclusion**: Configurable automatic conclusion at iteration limits. Hierarchical setting at system, agent, chain, stage, or parallel agent level
- **Data Masking**: Hybrid masking system combining structural analysis (Kubernetes Secrets) with regex patterns (API keys, passwords, certificates) to protect sensitive data
- **Tool Result Summarization**: LLM-powered summarization of verbose MCP tool outputs to reduce token usage and improve agent reasoning
- **Slack Notifications**: Automatic notifications when alert processing completes or fails, with thread-based message grouping via fingerprint matching
- **Comprehensive Audit Trail**: Full visibility into chain processing with stage-level timeline reconstruction and trace views

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
2. **Orchestrator selects** appropriate agent chain based on alert type
3. **Runbook downloaded** (optional) automatically from GitHub for chain guidance
4. **Sequential stages execute** where each agent builds upon previous stage data using AI to select and execute domain-specific tools
   - Stages can run multiple agents in parallel for independent investigation
   - Parallel results automatically synthesized into unified analysis
5. **Automatic pause** if investigation reaches iteration limits (or forced conclusion if configured)
6. **Comprehensive multi-stage analysis** provided to engineers with actionable recommendations
7. **Follow-up chat available** after investigation completes
8. **Full audit trail** captured with stage-level detail

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

### Sessions
- `GET /api/v1/sessions` -- List sessions with filtering and pagination
- `GET /api/v1/sessions/active` -- Currently active sessions
- `GET /api/v1/sessions/filter-options` -- Available filter values
- `GET /api/v1/sessions/:id` -- Session detail with chronological timeline
- `GET /api/v1/sessions/:id/summary` -- Final analysis and executive summary
- `POST /api/v1/sessions/:id/cancel` -- Cancel an active or paused session

### Chat
- `POST /api/v1/sessions/:id/chat/messages` -- Send message (AI response streams via WebSocket)

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

### Container issues
- Check status: `make containers-status`
- View logs: `make containers-logs`
- Fresh rebuild: `make containers-deploy-fresh`
- Clean everything: `make containers-clean`
