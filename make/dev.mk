# =============================================================================
# Development Workflow
# =============================================================================

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
# Build & Test
# =============================================================================

.PHONY: build
build: ## Build Go application
	@echo -e "$(YELLOW)Building TARSy...$(NC)"
	@go build -o bin/tarsy ./cmd/tarsy
	@echo -e "$(GREEN)✅ Build complete: bin/tarsy$(NC)"

.PHONY: test
test: ## Run Go tests
	@echo -e "$(YELLOW)Running tests...$(NC)"
	@go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-coverage
test-coverage: test ## Run tests and show coverage report
	@echo -e "$(YELLOW)Generating coverage report...$(NC)"
	@go tool cover -html=coverage.out

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
