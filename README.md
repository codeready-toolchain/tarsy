# TARSy

[![CI](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml/badge.svg)](https://github.com/codeready-toolchain/tarsy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/codeready-toolchain/tarsy/graph/badge.svg)](https://codecov.io/gh/codeready-toolchain/tarsy)
[![Go Coverage](https://codecov.io/gh/codeready-toolchain/tarsy/graph/badge.svg?flag=go)](https://codecov.io/gh/codeready-toolchain/tarsy?flags[0]=go)
[![Python Coverage](https://codecov.io/gh/codeready-toolchain/tarsy/graph/badge.svg?flag=python)](https://codecov.io/gh/codeready-toolchain/tarsy?flags[0]=python)

New TARSy implementation with Go orchestrator + Python LLM service architecture.

## Current Status

**Phase 2.1 Complete**: Database schema and persistence layer implemented with Ent ORM and PostgreSQL.

## Architecture

```
┌─────────┐    WebSocket    ┌─────────────┐    gRPC     ┌─────────────┐
│ Browser │ ←─────────────→ │ Go          │ ←─────────→ │ Python LLM  │
│         │    HTTP/WS      │ Orchestrator│   Stream    │ Service     │
└─────────┘                 └─────────────┘             └─────────────┘
                                  │                           │
                             PostgreSQL                 Gemini API
                            (Ent ORM)
```

## Components

### 1. LLM Service (`llm-service/`)

Thin gRPC service wrapping Gemini native thinking client (Python-based).

- **Proto**: `proto/llm_service.proto`
- **Client**: Simplified Gemini native thinking client
- **Servicer**: gRPC server streaming responses

### 2. Go Orchestrator (`cmd/tarsy/`, `pkg/`, `ent/`)

HTTP API + WebSocket server with PostgreSQL persistence.

- **Database Layer**: Ent ORM with five-layer architecture
- **LLM Client**: gRPC client to Python service
- **API Handlers**: REST endpoints + WebSocket  
- **Streaming**: Fan-out from gRPC to WebSocket clients

### 3. Frontend (`dashboard/`)


## Prerequisites

- Python 3.11+ with `uv` installed
- Go 1.21+
- PostgreSQL 16+ (or podman/docker for local development)
- `protoc` (Protocol Buffers compiler)

## Setup

### 1. Configure Environment

```bash
# Copy the example configuration
cp deploy/.env.example deploy/.env

# Edit deploy/.env and set your configuration
# Required: GOOGLE_API_KEY, DB_PASSWORD
# Optional: Adjust other settings as needed
```

### 2. Start PostgreSQL

```bash
# Start PostgreSQL container (waits until ready)
make db-start
```

## Makefile Commands

TARSy uses a modular Makefile structure (see `make/` directory for organization).

Quick reference for common development tasks:

```bash
# Quick start
make help             # Show all available commands
make dev-setup        # Complete setup (DB + code generation)
make build            # Build the application

# Database operations
make db-start         # Start PostgreSQL container
make db-stop          # Stop PostgreSQL container

# Code generation
make ent-generate     # Generate Ent ORM code
make proto-generate   # Generate protobuf code

# Development
make fmt              # Format code
make test             # Run tests
make lint             # Run linter
```

For the complete list of commands, run `make help`.

## Troubleshooting

### Database connection issues

- Verify PostgreSQL is running: `make db-status`
- Check PostgreSQL logs: `make db-logs`
- Connect to verify manually: `make db-psql`
- Reset if corrupted: `make db-reset`

## Development Status

See [`docs/project-plan.md`](docs/project-plan.md) for full roadmap.

### Completed
- ✅ Phase 1: Proof of Concept - Go-Python integration
- ✅ Phase 2.1: Schema & Migrations - Database persistence layer

