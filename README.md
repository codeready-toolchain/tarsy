# TARSy Go-Python PoC

A minimal proof-of-concept demonstrating the Go orchestrator + Python LLM client architecture split.

## Architecture

```
┌─────────┐    WebSocket    ┌────────────┐    gRPC     ┌─────────────┐
│ Browser │ ←─────────────→ │ Go         │ ←─────────→ │ Python LLM  │
│         │    HTTP/WS      │ Orchestrator│   Stream    │ Service     │
└─────────┘                 └────────────┘             └─────────────┘
                                  │                           │
                            In-Memory Store            Gemini API
```

## Components

### 1. LLM Service (`llm-service/`)

Thin gRPC service wrapping Gemini native thinking client (Python-based).

- **Proto**: `proto/llm_service.proto`
- **Client**: Simplified Gemini native thinking client
- **Servicer**: gRPC server streaming responses

### 2. Go Orchestrator (`cmd/tarsy/`, `pkg/`)

HTTP API + WebSocket server with in-memory session management.

- **Session Manager**: Thread-safe in-memory session storage
- **LLM Client**: gRPC client to Python service
- **API Handlers**: REST endpoints + WebSocket
- **Streaming**: Fan-out from gRPC to WebSocket clients

### 3. Frontend (`dashboard/`)

Minimal HTML/CSS/JS interface.

- Real-time streaming updates via WebSocket
- Session list and message display
- Auto-reconnection logic

## Prerequisites

- Python 3.11+ with `uv` installed
- Go 1.21+ 
- `protoc` (Protocol Buffers compiler)
- Google API key for Gemini

## Setup

### 1. Configure Environment

```bash
# Copy the example configuration
cp deploy/.env.example deploy/.env

# Edit deploy/.env and set your API key
# Required: GOOGLE_API_KEY
# Optional: Adjust other settings as needed
```

### 2. LLM Service

```bash
cd llm-service

# Install dependencies
uv sync
```

### 3. Go Orchestrator

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
│   └── session/            # In-memory session management
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

## Next Steps (Beyond PoC)

- [ ] Add PostgreSQL for persistent storage
- [ ] Implement MCP tool integration
- [ ] Add multi-stage agent chains
- [ ] Implement pause/resume functionality
- [ ] Add authentication/authorization
- [ ] Create Kubernetes deployment configs
- [ ] Add observability (metrics, tracing)
- [ ] Multiple LLM provider support

## Key Learnings

This PoC validates:

1. **Go-Python split is viable**: Clean separation via gRPC
2. **Streaming works smoothly**: gRPC → Go → WebSocket chain
3. **Go concurrency patterns excel**: goroutines + channels handle fan-out naturally
4. **Simple architecture**: Minimal components, clear responsibilities

The architecture shows promise for scaling to full TARSy implementation.
