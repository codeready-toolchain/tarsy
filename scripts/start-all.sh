#!/bin/bash

# TARSy PoC Startup Script
# Starts both Python LLM service and Go orchestrator

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== TARSy Go-Python PoC Startup ===${NC}\n"

# Get project root
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

# Check if .env file exists
if [ ! -f "deploy/.env" ]; then
    echo -e "${RED}ERROR: deploy/.env file not found${NC}"
    echo ""
    echo "Please create it first:"
    echo "  cp deploy/.env.example deploy/.env"
    echo "  # Edit deploy/.env and set GOOGLE_API_KEY"
    exit 1
fi

# Source .env file to check for API key
set -a
source deploy/.env
set +a

# Check if API key is set
if [ -z "$GOOGLE_API_KEY" ]; then
    echo -e "${RED}ERROR: GOOGLE_API_KEY is not set in deploy/.env${NC}"
    echo "Please edit deploy/.env and set your Google API key"
    exit 1
fi

echo -e "${GREEN}✓ Configuration loaded from deploy/.env${NC}"
echo -e "${GREEN}✓ GOOGLE_API_KEY is set${NC}"

# Check if uv is installed
if ! command -v uv &> /dev/null; then
    echo -e "${RED}ERROR: uv is not installed${NC}"
    echo "Install with: curl -LsSf https://astral.sh/uv/install.sh | sh"
    exit 1
fi

echo -e "${GREEN}✓ uv is installed${NC}"

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo -e "${RED}ERROR: Go is not installed${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Go is installed${NC}\n"

# Check if podman or docker is installed
if command -v podman &> /dev/null; then
    CONTAINER_CMD="podman"
elif command -v docker &> /dev/null; then
    CONTAINER_CMD="docker"
else
    echo -e "${RED}ERROR: Neither podman nor docker is installed${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Container runtime: $CONTAINER_CMD${NC}"

# Start PostgreSQL
echo -e "${YELLOW}Starting PostgreSQL...${NC}"
cd deploy
if [ "$CONTAINER_CMD" = "podman" ]; then
    podman-compose up -d
else
    docker-compose up -d
fi
cd ..

# Wait for PostgreSQL to be ready
echo "Waiting for PostgreSQL to be ready..."
for i in {1..30}; do
    if $CONTAINER_CMD exec tarsy-postgres pg_isready -U tarsy > /dev/null 2>&1; then
        break
    fi
    sleep 1
done

if ! $CONTAINER_CMD exec tarsy-postgres pg_isready -U tarsy > /dev/null 2>&1; then
    echo -e "${RED}ERROR: PostgreSQL failed to start${NC}"
    exit 1
fi

echo -e "${GREEN}✓ PostgreSQL is ready${NC}\n"

# Build Go orchestrator if needed
if [ ! -f "bin/tarsy" ]; then
    echo -e "${YELLOW}Building Go orchestrator...${NC}"
    go build -o bin/tarsy ./cmd/tarsy
    echo -e "${GREEN}✓ Built Go orchestrator${NC}\n"
fi

# Create log directory
mkdir -p logs

# Start LLM service
echo -e "${YELLOW}Starting LLM service...${NC}"
cd llm-service
uv run python -m llm.server > ../logs/llm-service.log 2>&1 &
LLM_PID=$!
cd ..

# Wait for LLM service to start
echo "Waiting for LLM service to start..."
sleep 3

if ! kill -0 $LLM_PID 2>/dev/null; then
    echo -e "${RED}ERROR: LLM service failed to start${NC}"
    echo "Check logs/llm-service.log for details"
    exit 1
fi

echo -e "${GREEN}✓ LLM service started (PID: $LLM_PID)${NC}\n"

# Start Go orchestrator
echo -e "${YELLOW}Starting Go orchestrator...${NC}"
export GRPC_ADDR=${GRPC_ADDR:-localhost:50051}
export HTTP_PORT=${HTTP_PORT:-8080}

./bin/tarsy > logs/go-orchestrator.log 2>&1 &
GO_PID=$!

# Wait for Go service to start
echo "Waiting for Go orchestrator to start..."
sleep 2

if ! kill -0 $GO_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Go orchestrator failed to start${NC}"
    echo "Check logs/go-orchestrator.log for details"
    kill $LLM_PID 2>/dev/null
    exit 1
fi

echo -e "${GREEN}✓ Go orchestrator started (PID: $GO_PID)${NC}\n"

# Save PIDs
echo $LLM_PID > logs/llm.pid
echo $GO_PID > logs/go.pid

echo -e "${GREEN}=== TARSy is running ===${NC}\n"
echo "PostgreSQL:         localhost:5432"
echo "LLM Service:        http://localhost:50051 (gRPC)"
echo "Go Orchestrator:    http://localhost:$HTTP_PORT"
echo "Dashboard:          http://localhost:$HTTP_PORT"
echo ""
echo "Logs:"
echo "  LLM Service: tail -f logs/llm-service.log"
echo "  Go:          tail -f logs/go-orchestrator.log"
echo ""
echo "To stop services: ./scripts/stop-all.sh"
echo ""
echo -e "${YELLOW}Opening browser in 2 seconds...${NC}"
sleep 2

# Try to open browser
if command -v xdg-open &> /dev/null; then
    xdg-open "http://localhost:$HTTP_PORT" 2>/dev/null &
elif command -v open &> /dev/null; then
    open "http://localhost:$HTTP_PORT" 2>/dev/null &
fi

echo -e "${GREEN}Ready!${NC}"
