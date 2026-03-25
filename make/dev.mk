# =============================================================================
# Environment Check
# =============================================================================

.PHONY: doctor
doctor: ## Check if dev prerequisites are installed
	@echo -e "$(YELLOW)Checking development prerequisites...$(NC)"
	@ok=true; \
	check_cmd() { \
		required=$${4:-required}; \
		if command -v "$$1" >/dev/null 2>&1; then \
			ver=$$($$2 2>&1 | head -1); \
			echo -e "  $(GREEN)✓$(NC) $$1  $$ver"; \
		else \
			if [ "$$required" = "required" ]; then \
				echo -e "  $(RED)✗$(NC) $$1  -- not found ($$3)"; \
				ok=false; \
			else \
				echo -e "  $(YELLOW)-$(NC) $$1  -- not found (optional: $$3)"; \
			fi; \
		fi; \
	}; \
	check_cmd go             "go version"               "https://go.dev/dl/"; \
	check_cmd python3        "python3 --version"        "https://www.python.org/downloads/"; \
	check_cmd node           "node --version"           "https://nodejs.org/"; \
	check_cmd npm            "npm --version"            "https://nodejs.org/"; \
	check_cmd uv             "uv --version"             "curl -LsSf https://astral.sh/uv/install.sh | sh"; \
	check_cmd podman         "podman --version"         "https://podman.io/docs/installation"; \
	echo ""; \
	echo -e "$(YELLOW)Optional tools:$(NC)"; \
	check_cmd protoc         "protoc --version"         "https://grpc.io/docs/protoc-installation/" optional; \
	check_cmd golangci-lint  "golangci-lint --version"  "https://golangci-lint.run/welcome/install/" optional; \
	check_cmd atlas          "atlas version"            "https://atlasgo.io/getting-started#installation" optional; \
	check_cmd goimports      "go version -m $$(which goimports)" "go install golang.org/x/tools/cmd/goimports@latest" optional; \
	echo ""; \
	echo -e "$(YELLOW)Port availability (make dev):$(NC)"; \
	check_port() { \
		if (echo >/dev/tcp/127.0.0.1/$$1) 2>/dev/null; then \
			if [ "$$3" = "warn" ]; then \
				echo -e "  $(YELLOW)⚠  :$$1  -- already in use ($$2, ok if from previous make dev)$(NC)"; \
			else \
				echo -e "  $(RED)✗$(NC) :$$1  -- already in use ($$2)"; \
				ok=false; \
			fi; \
		else \
			echo -e "  $(GREEN)✓$(NC) :$$1  available ($$2)"; \
		fi; \
	}; \
	check_port 5432  "PostgreSQL" warn; \
	check_port 50051 "LLM service"; \
	check_port 8080  "Go backend"; \
	check_port 5173  "Dashboard"; \
	echo ""; \
	echo -e "$(YELLOW)Configuration files (make dev):$(NC)"; \
	check_file() { \
		if [ -f "$$1" ]; then \
			echo -e "  $(GREEN)✓$(NC) $$1"; \
		else \
			echo -e "  $(RED)✗$(NC) $$1  -- not found ($$2)"; \
			ok=false; \
		fi; \
	}; \
	check_file deploy/config/tarsy.yaml          "cp deploy/config/tarsy.yaml.quickstart deploy/config/tarsy.yaml"; \
	check_file deploy/config/llm-providers.yaml   "cp deploy/config/llm-providers.yaml.quickstart deploy/config/llm-providers.yaml"; \
	check_file deploy/config/.env                 "cp deploy/config/.env.example deploy/config/.env"; \
	echo ""; \
	if $$ok; then \
		echo -e "$(GREEN)✅ All checks passed$(NC)"; \
	else \
		echo -e "$(RED)❌ Some checks failed -- see above$(NC)"; \
		exit 1; \
	fi

# =============================================================================
# Development Workflow
# =============================================================================

.PHONY: check-all
check-all: fmt build lint-fix test ## Format, build, lint, and run all tests
	@echo ""
	@echo -e "$(GREEN)✅ All checks passed!$(NC)"

.PHONY: dev
dev: db-start build ## Start full dev environment (DB + LLM + backend + dashboard)
	@# Kill stale processes from a previous run so ports are free
	@-pkill -f 'bin/tarsy' 2>/dev/null; true
	@-pkill -f 'llm.server' 2>/dev/null; true
	@-pkill -f 'web/dashboard.*vite' 2>/dev/null; true
	@sleep 0.3
	@echo -e "$(GREEN)Starting development environment...$(NC)"
	@echo -e "$(BLUE)  PostgreSQL:   localhost:5432$(NC)"
	@echo -e "$(BLUE)  LLM service:  localhost:50051$(NC)"
	@echo -e "$(BLUE)  Go backend:   localhost:8080$(NC)"
	@echo -e "$(BLUE)  Dashboard:    localhost:5173$(NC)"
	@echo ""
	@trap 'kill 0' EXIT; \
		cd llm-service && uv run python -m llm.server & \
		echo "Waiting for LLM service on :50051..."; \
		for i in $$(seq 1 40); do (echo >/dev/tcp/127.0.0.1/50051) 2>/dev/null && break; sleep 0.5; done; \
		(echo >/dev/tcp/127.0.0.1/50051) 2>/dev/null || { echo "ERROR: LLM service did not start on :50051 within 20s" >&2; exit 1; }; \
		./bin/tarsy & TARSY_PID=$$!; \
		sleep 1; \
		if ! kill -0 $$TARSY_PID 2>/dev/null; then \
			echo -e "\n$(RED)ERROR: TARSy backend failed to start (check logs above)$(NC)" >&2; \
			exit 1; \
		fi; \
		echo -e "$(GREEN)✅ TARSy backend running (pid $$TARSY_PID)$(NC)"; \
		cd web/dashboard && npm run dev

.PHONY: dev-stop
dev-stop: db-stop ## Stop all dev services (DB + LLM + backend + dashboard)
	@echo -e "$(YELLOW)Stopping development services...$(NC)"
	@-pkill -f 'bin/tarsy' 2>/dev/null; true
	@-pkill -f 'llm.server' 2>/dev/null; true
	@-pkill -f 'web/dashboard.*vite' 2>/dev/null; true
	@echo -e "$(GREEN)✅ All services stopped$(NC)"

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
test: test-go test-python test-dashboard ## Run all tests (Go + Python + Dashboard)
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

.PHONY: dashboard-test-watch
dashboard-test-watch: ## Run dashboard tests in watch mode
	@echo -e "$(YELLOW)Starting dashboard tests in watch mode...$(NC)"
	@cd web/dashboard && npm run test

.PHONY: dashboard-test-build
dashboard-test-build: ## TypeScript check for dashboard
	@echo -e "$(YELLOW)Checking dashboard TypeScript...$(NC)"
	@cd web/dashboard && npx tsc -b
	@echo -e "$(GREEN)✅ Dashboard TypeScript check passed$(NC)"

.PHONY: test-dashboard
test-dashboard: dashboard-test dashboard-test-build ## Run dashboard tests + TypeScript check
	@echo -e "$(GREEN)✅ All dashboard checks passed$(NC)"

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
		--python_out=llm_proto \
		--grpc_python_out=llm_proto \
		--pyi_out=llm_proto \
		../proto/llm_service.proto
	@sed -i 's/^import llm_service_pb2/from . import llm_service_pb2/' llm-service/llm_proto/llm_service_pb2_grpc.py
	@echo -e "$(GREEN)✅ Proto files generated successfully!$(NC)"

.PHONY: proto-clean
proto-clean: ## Clean generated proto files
	@echo -e "$(YELLOW)Cleaning generated proto files...$(NC)"
	@rm -f proto/*.pb.go
	@rm -f llm-service/llm_proto/llm_service_pb2.py
	@rm -f llm-service/llm_proto/llm_service_pb2_grpc.py
	@rm -f llm-service/llm_proto/llm_service_pb2.pyi
	@echo -e "$(GREEN)✅ Proto files cleaned!$(NC)"

# =============================================================================
# Dependencies
# =============================================================================

.PHONY: setup
setup: ## Install all dependencies (Go + Python + Dashboard) and bootstrap config
	@echo -e "$(YELLOW)Installing Go dependencies...$(NC)"
	@go mod download
	@go mod tidy
	@echo -e "$(YELLOW)Installing LLM service dependencies...$(NC)"
	@cd llm-service && uv sync
	@echo -e "$(YELLOW)Installing dashboard dependencies...$(NC)"
	@cd web/dashboard && npm ci
	@echo -e "$(GREEN)✅ All dependencies installed$(NC)"
	@echo ""
	@echo -e "$(YELLOW)Bootstrapping configuration...$(NC)"
	@if [ ! -f deploy/config/tarsy.yaml ]; then \
		cp deploy/config/tarsy.yaml.quickstart deploy/config/tarsy.yaml; \
		echo -e "  $(GREEN)✓$(NC) Created deploy/config/tarsy.yaml (from quickstart)"; \
	else \
		echo -e "  $(YELLOW)-$(NC) deploy/config/tarsy.yaml already exists, skipping"; \
	fi
	@if [ ! -f deploy/config/llm-providers.yaml ]; then \
		cp deploy/config/llm-providers.yaml.quickstart deploy/config/llm-providers.yaml; \
		echo -e "  $(GREEN)✓$(NC) Created deploy/config/llm-providers.yaml (from quickstart)"; \
	else \
		echo -e "  $(YELLOW)-$(NC) deploy/config/llm-providers.yaml already exists, skipping"; \
	fi
	@if [ ! -f deploy/config/.env ]; then \
		echo -e "  $(RED)!$(NC) deploy/config/.env not found — copy and edit before running make dev:"; \
		echo -e "      cp deploy/config/.env.example deploy/config/.env"; \
	else \
		echo -e "  $(YELLOW)-$(NC) deploy/config/.env already exists, skipping"; \
	fi

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
