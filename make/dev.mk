# =============================================================================
# Development Workflow
# =============================================================================

.PHONY: check
check: fmt build lint-fix test ## Format, build, lint, and run all tests
	@echo ""
	@echo -e "$(GREEN)✅ All checks passed!$(NC)"

.PHONY: dev
dev: db-start build ## Start full dev environment (DB + LLM + backend + dashboard)
	@echo -e "$(GREEN)Starting development environment...$(NC)"
	@echo -e "$(BLUE)  PostgreSQL:   localhost:5432$(NC)"
	@echo -e "$(BLUE)  LLM service:  localhost:50051$(NC)"
	@echo -e "$(BLUE)  Go backend:   localhost:8080$(NC)"
	@echo -e "$(BLUE)  Dashboard:    localhost:5173$(NC)"
	@echo ""
	@trap 'kill 0' EXIT; \
		cd llm-service && uv run python -m llm.server & \
		sleep 2 && ./bin/tarsy & \
		cd web/dashboard && npm run dev

.PHONY: dev-stop
dev-stop: db-stop ## Stop all dev services (DB + LLM + backend + dashboard)
	@echo -e "$(YELLOW)Stopping development services...$(NC)"
	@-pkill -f 'bin/tarsy' 2>/dev/null; true
	@-pkill -f 'llm.server' 2>/dev/null; true
	@-pkill -f 'web/dashboard.*vite' 2>/dev/null; true
	@echo -e "$(GREEN)✅ All services stopped$(NC)"

.PHONY: dev-setup
dev-setup: db-start ent-generate ## Setup development environment
	@echo ""
	@echo -e "$(GREEN)✅ Development environment ready!$(NC)"
	@echo -e "$(BLUE)  Database: $(DB_DSN)$(NC)"
	@echo ""
	@echo -e "$(YELLOW)Next steps:$(NC)"
	@echo "  1. Ensure deploy/.env has your configuration"
	@echo "  2. Run 'make build' to build the application"
	@echo "  3. Run './bin/tarsy' to start the application"

.PHONY: dev-clean
dev-clean: db-clean ent-clean ## Clean all development artifacts
	@echo -e "$(GREEN)✅ Development environment cleaned$(NC)"

# =============================================================================
# Build
# =============================================================================

.PHONY: build
build: ## Build Go application
	@echo -e "$(YELLOW)Building TARSy...$(NC)"
	@go build -o bin/tarsy ./cmd/tarsy
	@echo -e "$(GREEN)✅ Build complete: bin/tarsy$(NC)"

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: test-go test-python ## Run all tests (Go + Python)
	@echo ""
	@echo -e "$(GREEN)✅ All tests passed!$(NC)"

# -----------------------------------------------------------------------------
# Go Tests
# -----------------------------------------------------------------------------

.PHONY: test-go
test-go: ## Run all Go tests (unit + e2e) with coverage
	@echo -e "$(YELLOW)Running Go tests...$(NC)"
	@go test -v -race -coverprofile=coverage.out -coverpkg=./pkg/... $$(go list ./... | grep -v -E '/(ent|proto)(/|$$)')
	@echo -e "$(GREEN)✅ Go tests passed$(NC)"

.PHONY: test-unit
test-unit: ## Run Go unit/integration tests only (excludes e2e)
	@echo -e "$(YELLOW)Running Go unit tests...$(NC)"
	@go test -v -race ./pkg/...
	@echo -e "$(GREEN)✅ Go unit tests passed$(NC)"

.PHONY: test-e2e
test-e2e: ## Run Go e2e tests only (requires Docker for PostgreSQL)
	@echo -e "$(YELLOW)Running Go e2e tests...$(NC)"
	@go test -v -race -timeout 300s ./test/e2e/...
	@echo -e "$(GREEN)✅ Go e2e tests passed$(NC)"

.PHONY: test-go-coverage
test-go-coverage: test-go ## Run Go tests and show coverage report
	@echo -e "$(YELLOW)Generating Go coverage report...$(NC)"
	@go tool cover -func=coverage.out
	@go tool cover -html=coverage.out -o coverage.html
	@echo -e "$(GREEN)HTML report saved to coverage.html$(NC)"

# -----------------------------------------------------------------------------
# Python Tests
# -----------------------------------------------------------------------------

.PHONY: test-python
test-python: test-llm ## Run all Python tests (alias for test-llm)

.PHONY: test-llm
test-llm: ## Run LLM service Python tests
	@echo -e "$(YELLOW)Running LLM service tests...$(NC)"
	@cd llm-service && uv run pytest tests/ -v
	@echo -e "$(GREEN)✅ LLM service tests passed$(NC)"

.PHONY: test-llm-unit
test-llm-unit: ## Run LLM service unit tests only
	@echo -e "$(YELLOW)Running LLM service unit tests...$(NC)"
	@cd llm-service && uv run pytest tests/ -m unit -v
	@echo -e "$(GREEN)✅ LLM service unit tests passed$(NC)"

.PHONY: test-llm-integration
test-llm-integration: ## Run LLM service integration tests only
	@echo -e "$(YELLOW)Running LLM service integration tests...$(NC)"
	@cd llm-service && uv run pytest tests/ -m integration -v
	@echo -e "$(GREEN)✅ LLM service integration tests passed$(NC)"

.PHONY: test-llm-coverage
test-llm-coverage: ## Run LLM service tests with coverage
	@echo -e "$(YELLOW)Running LLM service tests with coverage...$(NC)"
	@cd llm-service && uv run pytest tests/ --cov=llm --cov-report=term-missing --cov-report=xml:coverage.xml
	@echo -e "$(GREEN)✅ LLM service tests complete$(NC)"

# -----------------------------------------------------------------------------
# Dashboard
# -----------------------------------------------------------------------------

.PHONY: dashboard-install
dashboard-install: ## Install dashboard dependencies
	@echo -e "$(YELLOW)Installing dashboard dependencies...$(NC)"
	@cd web/dashboard && npm install
	@echo -e "$(GREEN)✅ Dashboard dependencies installed$(NC)"

.PHONY: dashboard-dev
dashboard-dev: ## Start dashboard dev server (Vite)
	@echo -e "$(YELLOW)Starting dashboard dev server...$(NC)"
	@cd web/dashboard && npm run dev

.PHONY: dashboard-build
dashboard-build: ## Build dashboard for production
	@echo -e "$(YELLOW)Building dashboard...$(NC)"
	@cd web/dashboard && npm run build
	@echo -e "$(GREEN)✅ Dashboard built to web/dashboard/dist/$(NC)"

.PHONY: dashboard-test
dashboard-test: ## Run dashboard tests
	@echo -e "$(YELLOW)Running dashboard tests...$(NC)"
	@cd web/dashboard && npm run test:run
	@echo -e "$(GREEN)✅ Dashboard tests passed$(NC)"

.PHONY: dashboard-lint
dashboard-lint: ## Lint dashboard code
	@echo -e "$(YELLOW)Linting dashboard...$(NC)"
	@cd web/dashboard && npm run lint
	@echo -e "$(GREEN)✅ Dashboard lint passed$(NC)"

.PHONY: lint
lint: ## Run golangci-lint
	@echo -e "$(YELLOW)Running linter...$(NC)"
	@golangci-lint run --timeout=5m

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix
	@echo -e "$(YELLOW)Running linter with auto-fix...$(NC)"
	@golangci-lint run --timeout=5m --fix

.PHONY: lint-config
lint-config: ## Verify golangci-lint configuration
	@echo -e "$(YELLOW)Verifying linter configuration...$(NC)"
	@golangci-lint config verify
	@echo -e "$(GREEN)✅ Configuration is valid$(NC)"

.PHONY: fmt
fmt: ## Format Go code
	@echo -e "$(YELLOW)Formatting code...$(NC)"
	@go fmt ./...
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	fi
	@echo -e "$(GREEN)✅ Code formatted$(NC)"

# =============================================================================
# Protocol Buffers
# =============================================================================

.PHONY: proto-generate
proto-generate: ## Generate Go and Python code from proto files
	@echo -e "$(YELLOW)Generating protobuf files...$(NC)"
	@echo -e "$(BLUE)  -> Generating Go code...$(NC)"
	@protoc \
		--go_out=. \
		--go_opt=paths=source_relative \
		--go-grpc_out=. \
		--go-grpc_opt=paths=source_relative \
		proto/llm_service.proto
	@echo -e "$(BLUE)  -> Generating Python code...$(NC)"
	@cd llm-service && uv run python -m grpc_tools.protoc \
		-I../proto \
		--python_out=proto \
		--grpc_python_out=proto \
		--pyi_out=proto \
		../proto/llm_service.proto
	@sed -i 's/^import llm_service_pb2/from . import llm_service_pb2/' llm-service/proto/llm_service_pb2_grpc.py
	@echo -e "$(GREEN)✅ Proto files generated successfully!$(NC)"

.PHONY: proto-clean
proto-clean: ## Clean generated proto files
	@echo -e "$(YELLOW)Cleaning generated proto files...$(NC)"
	@rm -f proto/*.pb.go
	@rm -f llm-service/proto/llm_service_pb2.py
	@rm -f llm-service/proto/llm_service_pb2_grpc.py
	@rm -f llm-service/proto/llm_service_pb2.pyi
	@echo -e "$(GREEN)✅ Proto files cleaned!$(NC)"

# =============================================================================
# Dependencies
# =============================================================================

.PHONY: deps-install
deps-install: ## Install Go dependencies
	@echo -e "$(YELLOW)Installing dependencies...$(NC)"
	@go mod download
	@go mod tidy
	@echo -e "$(GREEN)✅ Dependencies installed$(NC)"

.PHONY: deps-update
deps-update: ## Update Go dependencies
	@echo -e "$(YELLOW)Updating dependencies...$(NC)"
	@go get -u ./...
	@go mod tidy
	@echo -e "$(GREEN)✅ Dependencies updated$(NC)"

.PHONY: deps-verify
deps-verify: ## Verify Go dependencies
	@echo -e "$(YELLOW)Verifying dependencies...$(NC)"
	@go mod verify
	@echo -e "$(GREEN)✅ Dependencies verified$(NC)"
