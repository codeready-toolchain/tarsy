#!/bin/bash

# TARSy PoC Stop Script

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}=== Stopping TARSy PoC ===${NC}\n"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

# Stop Go orchestrator
if [ -f "logs/go.pid" ]; then
    GO_PID=$(cat logs/go.pid)
    if kill -0 $GO_PID 2>/dev/null; then
        echo "Stopping Go orchestrator (PID: $GO_PID)..."
        kill $GO_PID
        echo -e "${GREEN}✓ Stopped Go orchestrator${NC}"
    fi
    rm logs/go.pid
fi

# Stop LLM service
if [ -f "logs/llm.pid" ]; then
    LLM_PID=$(cat logs/llm.pid)
    if kill -0 $LLM_PID 2>/dev/null; then
        echo "Stopping LLM service (PID: $LLM_PID)..."
        kill $LLM_PID
        echo -e "${GREEN}✓ Stopped LLM service${NC}"
    fi
    rm logs/llm.pid
fi

echo -e "\n${GREEN}All services stopped${NC}"
