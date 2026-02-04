# TARSy

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
# Using podman (recommended)
podman run -d --name tarsy-postgres \
  -e POSTGRES_USER=tarsy \
  -e POSTGRES_PASSWORD=tarsy_dev_password \
  -e POSTGRES_DB=tarsy \
  -p 5432:5432 \
  -v tarsy-postgres-data:/var/lib/postgresql/data \
  docker.io/library/postgres:16-alpine

# Or using podman-compose
cd deploy
podman-compose up -d
cd ..

# Wait for PostgreSQL to be ready
podman exec tarsy-postgres pg_isready -U tarsy
```

### 3. LLM Service

```bash
cd llm-service

# Install dependencies
uv sync
```

### 4. Go Orchestrator

```bash
# Install dependencies (from project root)
go mod tidy

# Build
go build -o bin/tarsy ./cmd/tarsy
```

## Running

### Terminal 1: Start LLM Service

```bash
cd llm-service
uv run python -m llm.server
```

You should see:
```
Loaded configuration from .../deploy/.env
Starting LLM gRPC server on port 50051
Model: gemini-2.0-flash-thinking-exp-01-21
...
LLM gRPC server listening on port 50051
```

### Terminal 2: Start Go Orchestrator

```bash
# From project root
./bin/tarsy
# or: go run cmd/tarsy/main.go
```

You should see:
```
Loaded configuration from deploy/.env
Starting TARSy Go Orchestrator
gRPC LLM Service: localhost:50051
HTTP Port: 8080
...
HTTP server listening on :8080
```

**Note**: Configuration is loaded from `deploy/.env`. You can override any setting by setting environment variables.

### Terminal 3: Open Browser

Navigate to: http://localhost:8080

## Testing

### 1. Manual Testing via UI

1. Open http://localhost:8080
2. Enter a message in the input field (e.g., "What is 2+2?")
3. Click Send
4. Watch real-time streaming:
   - Thinking content (yellow box) appears first
   - Response content (gray box) follows
5. Check session list on the left

### 2. API Testing

```bash
# Create a session
curl -X POST http://localhost:8080/api/alerts \
  -H "Content-Type: application/json" \
  -d '{"message": "Tell me a joke"}'

# List sessions
curl http://localhost:8080/api/sessions

# Get specific session
curl http://localhost:8080/api/sessions/<session-id>

# Cancel a processing session
curl -X POST http://localhost:8080/api/sessions/<session-id>/cancel
```

### 3. WebSocket Testing

```bash
# Using websocat (install: cargo install websocat)
websocat ws://localhost:8080/ws

# You'll receive messages like:
# {"type":"connected","data":{"message":"Connected to TARSy"}}
# {"type":"session.created","session_id":"...","data":{...}}
# {"type":"llm.thinking","session_id":"...","data":{"content":"...","is_complete":false}}
# {"type":"llm.response","session_id":"...","data":{"content":"...","is_complete":true,"is_final":true}}
```

### 4. gRPC Testing (LLM Service Standalone)

```bash
# Using grpcurl (install: go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest)
grpcurl -plaintext -d '{
  "session_id": "test-123",
  "model": "gemini-2.0-flash-thinking-exp-01-21",
  "messages": [
    {"role": "ROLE_SYSTEM", "content": "You are a helpful assistant."},
    {"role": "ROLE_USER", "content": "What is 2+2?"}
  ]
}' localhost:50051 llm.v1.LLMService/GenerateWithThinking
```

## Success Criteria

✅ **Streaming works**: Real-time thinking and response chunks appear in UI  
✅ **Concurrency works**: Multiple sessions can run simultaneously  
✅ **Cancellation works**: Goroutines properly handle context cancellation  
✅ **Simple to understand**: Clean architecture demonstrating Go-Python split

## Project Structure

```
.
├── cmd/tarsy/              # Go application entry point
│   └── main.go
├── pkg/                    # Go packages
│   ├── api/                # HTTP handlers + WebSocket
│   ├── llm/                # gRPC client to LLM service
│   └── database/           # Database client configuration
├── ent/                    # Ent ORM (generated)
│   ├── schema/             # Schema definitions
│   │   ├── alertsession.go
│   │   ├── stage.go
│   │   ├── agentexecution.go
│   │   └── ...
│   └── README.md           # Ent documentation
├── proto/                  # Protocol Buffers definitions
│   ├── llm_service.proto
│   ├── llm_service.pb.go       # Generated
│   └── llm_service_grpc.pb.go  # Generated
├── llm-service/            # LLM service (Python)
│   ├── llm/
│   │   ├── server.py       # gRPC server
│   │   ├── servicer.py     # Service implementation
│   │   ├── gemini_client.py
│   │   └── models.py
│   └── pyproject.toml
├── dashboard/              # Frontend
│   ├── index.html
│   ├── app.js
│   └── styles.css
├── go.mod
└── README.md
```

## Configuration

All configuration is managed through `deploy/.env`. Copy `deploy/.env.example` to get started:

```bash
cp deploy/.env.example deploy/.env
```

### Configuration Variables

**LLM Service**:
- `GOOGLE_API_KEY` (required): Your Google Gemini API key
- `GEMINI_MODEL` (optional): Model name, defaults to `gemini-2.0-flash-thinking-exp-01-21`
- `GEMINI_TEMPERATURE` (optional): Temperature (0.0-2.0), defaults to `1.0`
- `GEMINI_MAX_TOKENS` (optional): Maximum tokens for generation
- `GRPC_PORT` (optional): gRPC port, defaults to `50051`

**Database** (Phase 2.1+):
- `DB_HOST` (optional): PostgreSQL host, defaults to `localhost`
- `DB_PORT` (optional): PostgreSQL port, defaults to `5432`
- `DB_USER` (optional): Database user, defaults to `tarsy`
- `DB_PASSWORD` (required): Database password
- `DB_NAME` (optional): Database name, defaults to `tarsy`
- `DB_SSLMODE` (optional): SSL mode, defaults to `disable`
- `DB_MAX_OPEN_CONNS` (optional): Max open connections, defaults to `10`
- `DB_MAX_IDLE_CONNS` (optional): Max idle connections, defaults to `5`

**Go Orchestrator**:
- `GRPC_ADDR` (optional): LLM service address, defaults to `localhost:50051`
- `HTTP_PORT` (optional): HTTP server port, defaults to `8080`
- `GIN_MODE` (optional): Gin mode (debug/release/test), defaults to `debug`

**Note**: Environment variables will override values in `.env` file.

## Troubleshooting

### LLM service won't start

- Check `GOOGLE_API_KEY` is set in `deploy/.env`
- Verify dependencies: `cd llm-service && uv sync`
- Check port 50051 is not in use: `lsof -i :50051`
- Verify .env file exists: `ls deploy/.env`

### Go orchestrator connection error

- Ensure LLM service is running first
- Check `GRPC_ADDR` matches LLM service address
- Verify no firewall blocking port 50051

### WebSocket disconnects

- Check browser console for errors
- Verify HTTP_PORT matches browser URL
- Check Go logs for connection errors

### No streaming chunks appearing

- Check browser Network tab → WS connection active
- Verify LLM service is receiving requests (check logs)
- Check `GOOGLE_API_KEY` is valid

## Development Status

See [`docs/project-plan.md`](docs/project-plan.md) for full roadmap.

### Completed
- ✅ Phase 1: Proof of Concept - Go-Python integration
- ✅ Phase 2.1: Schema & Migrations - Database persistence layer

### Next Steps
- [ ] Phase 2.2: Service Layer - Business logic implementation
- [ ] Phase 2.3: Queue & Worker System - Background processing
- [ ] Phase 3: Agent Framework - Multi-stage agent chains
- [ ] Phase 4: MCP Integration - Tool system
- [ ] Phase 5+: Advanced features (see project plan)

## Documentation

- **Project Plan**: [`docs/project-plan.md`](docs/project-plan.md) - Full development roadmap
- **Phase 2 Design**: [`docs/phase2-database-persistence-design.md`](docs/phase2-database-persistence-design.md) - Database architecture
- **Ent ORM Guide**: [`ent/README.md`](ent/README.md) - Schema documentation

## Key Design Decisions

### Five-Layer Architecture
Clean separation of concerns across 5 data layers:
- **Layer 0a**: Stage (configuration + coordination)
- **Layer 0b**: AgentExecution (individual agent work)  
- **Layer 1**: TimelineEvent (UX-focused timeline)
- **Layer 2**: Message (LLM conversation context)
- **Layer 3-4**: LLMInteraction/MCPInteraction (debug data)

### Lazy Context Building
No `stage_output` or `agent_output` fields in database. Context generated on-demand when needed via `Agent.BuildStageContext()` method.

### WebSocket-Friendly
TimelineEvent with `event_id` tracking eliminates frontend de-duplication logic. Frontend simply updates existing events by ID during streaming.
