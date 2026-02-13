# =============================================================================
# Database Management
# =============================================================================

# Container configuration
CONTAINER_NAME := tarsy-postgres
IMAGE_NAME := docker.io/library/postgres:16-alpine
COMPOSE_FILE := deploy/podman-compose.yml

# Database configuration (can be overridden via environment)
DB_HOST := localhost
DB_PORT := 5432
DB_USER := tarsy
DB_PASSWORD := tarsy_dev_password
DB_NAME := tarsy

# Connection string
DB_DSN := postgresql://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=disable

# =============================================================================
# Database Lifecycle
# =============================================================================

.PHONY: db-start
db-start: ## Start PostgreSQL container
	@echo -e "$(YELLOW)Starting PostgreSQL container...$(NC)"
	@podman-compose -f $(COMPOSE_FILE) up -d
	@echo -e "$(BLUE)Waiting for PostgreSQL to be ready...$(NC)"
	@until podman exec $(CONTAINER_NAME) pg_isready -U $(DB_USER) > /dev/null 2>&1; do \
		sleep 1; \
	done
	@echo -e "$(GREEN)✅ PostgreSQL is ready!$(NC)"
	@echo -e "$(BLUE)  Host: $(DB_HOST):$(DB_PORT)$(NC)"
	@echo -e "$(BLUE)  Database: $(DB_NAME)$(NC)"
	@echo -e "$(BLUE)  User: $(DB_USER)$(NC)"

.PHONY: db-stop
db-stop: ## Stop PostgreSQL container
	@echo -e "$(YELLOW)Stopping PostgreSQL container...$(NC)"
	@podman-compose -f $(COMPOSE_FILE) down
	@echo -e "$(GREEN)✅ PostgreSQL stopped$(NC)"

.PHONY: db-restart
db-restart: db-stop db-start ## Restart PostgreSQL container

.PHONY: db-status
db-status: ## Check PostgreSQL container status
	@echo -e "$(GREEN)PostgreSQL Container Status:$(NC)"
	@podman ps --filter name=$(CONTAINER_NAME) --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

.PHONY: db-logs
db-logs: ## Show PostgreSQL logs
	@podman logs -f $(CONTAINER_NAME)

# =============================================================================
# Database Inspection
# =============================================================================

.PHONY: db-psql
db-psql: ## Connect to PostgreSQL with psql
	@podman exec -it $(CONTAINER_NAME) psql -U $(DB_USER) -d $(DB_NAME)

.PHONY: db-tables
db-tables: ## List all database tables
	@echo -e "$(GREEN)Database Tables:$(NC)"
	@podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d $(DB_NAME) -c "\dt"

.PHONY: db-indexes
db-indexes: ## List all database indexes
	@echo -e "$(GREEN)Database Indexes:$(NC)"
	@podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d $(DB_NAME) -c "\di"

.PHONY: db-schema
db-schema: ## Show full database schema
	@echo -e "$(GREEN)Database Schema:$(NC)"
	@podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d $(DB_NAME) -c "\d+"

.PHONY: db-size
db-size: ## Show database size statistics
	@echo -e "$(GREEN)Database Size:$(NC)"
	@podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d $(DB_NAME) -c "\l+"

# =============================================================================
# Database Operations
# =============================================================================

.PHONY: db-reset
db-reset: ## Reset database (WARNING: destroys all data)
	@echo -e "$(RED)⚠️  WARNING: This will destroy all data in the database!$(NC)"
	@printf "Are you sure? [y/N] "; \
	read REPLY; \
	case "$$REPLY" in \
		[Yy]|[Yy][Ee][Ss]) \
			echo -e "$(YELLOW)Dropping and recreating database...$(NC)"; \
			podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d postgres -c "DROP DATABASE IF EXISTS $(DB_NAME);"; \
			podman exec $(CONTAINER_NAME) psql -U $(DB_USER) -d postgres -c "CREATE DATABASE $(DB_NAME);"; \
			echo -e "$(GREEN)✅ Database reset complete!$(NC)"; \
			;; \
		*) \
			echo -e "$(GREEN)Cancelled$(NC)"; \
			;; \
	esac

.PHONY: db-clean
db-clean: db-stop ## Stop database and remove all data
	@echo -e "$(YELLOW)Removing PostgreSQL container and volumes...$(NC)"
	@podman-compose -f $(COMPOSE_FILE) down -v
	@echo -e "$(GREEN)✅ Database cleaned!$(NC)"

.PHONY: db-backup
db-backup: ## Backup database to file (usage: make db-backup FILE=backup.sql)
	@if [ -z "$(FILE)" ]; then \
		echo -e "$(RED)Error: Please specify FILE=backup.sql$(NC)"; \
		exit 1; \
	fi
	@echo -e "$(YELLOW)Backing up database to $(FILE)...$(NC)"
	@podman exec $(CONTAINER_NAME) pg_dump -U $(DB_USER) $(DB_NAME) > $(FILE)
	@echo -e "$(GREEN)✅ Backup complete: $(FILE)$(NC)"

.PHONY: db-restore
db-restore: ## Restore database from file (usage: make db-restore FILE=backup.sql)
	@if [ -z "$(FILE)" ]; then \
		echo -e "$(RED)Error: Please specify FILE=backup.sql$(NC)"; \
		exit 1; \
	fi
	@if [ ! -f "$(FILE)" ]; then \
		echo -e "$(RED)Error: File $(FILE) not found$(NC)"; \
		exit 1; \
	fi
	@echo -e "$(YELLOW)Restoring database from $(FILE)...$(NC)"
	@podman exec -i $(CONTAINER_NAME) psql -U $(DB_USER) $(DB_NAME) < $(FILE)
	@echo -e "$(GREEN)✅ Restore complete!$(NC)"

# =============================================================================
# Ent Code Generation
# =============================================================================

# Ent configuration
ENT_SCHEMA_DIR := ./ent/schema
ENT_GENERATE_CMD := go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/versioned-migration --feature sql/lock --feature sql/modifier $(ENT_SCHEMA_DIR)

.PHONY: ent-generate
ent-generate: ## Generate Ent code from schemas
	@echo -e "$(YELLOW)Generating Ent code...$(NC)"
	@$(ENT_GENERATE_CMD)
	@echo -e "$(GREEN)✅ Ent code generation complete!$(NC)"

.PHONY: ent-describe
ent-describe: ## Describe Ent schema (requires ent-generate first)
	@echo -e "$(GREEN)Ent Schema:$(NC)"
	@go run -mod=mod entgo.io/ent/cmd/ent describe $(ENT_SCHEMA_DIR)

.PHONY: ent-clean
ent-clean: ## Clean generated Ent code (keeps schemas)
	@echo -e "$(YELLOW)Cleaning generated Ent code...$(NC)"
	@find ./ent -mindepth 1 -maxdepth 1 -type d ! -name 'schema' -exec rm -rf {} +
	@find ./ent -mindepth 1 -maxdepth 1 -type f ! -name 'generate.go' ! -name 'README.md' -delete
	@echo -e "$(GREEN)✅ Ent code cleaned!$(NC)"

# =============================================================================
# Database Migrations
# =============================================================================
# Uses Atlas CLI to generate migrations, golang-migrate to apply them
# Migrations are stored in pkg/database/migrations/ and embedded into the binary

.PHONY: migrate-create
migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_feature)
	@if [ -z "$(NAME)" ]; then \
		echo -e "$(RED)Error: Please specify NAME=migration_name$(NC)"; \
		exit 1; \
	fi
	@echo -e "$(YELLOW)Creating migration: $(NAME)...$(NC)"
	@atlas migrate diff $(NAME) \
		--dir "file://pkg/database/migrations" \
		--to "ent://ent/schema" \
		--dev-url "$(DB_DSN)"
	@echo -e "$(GREEN)✅ Migration created in pkg/database/migrations/$(NC)"
	@echo -e "$(BLUE)Review the SQL files, then commit to git$(NC)"
	@echo -e "$(BLUE)Note: Migrations are embedded into binary at compile time$(NC)"
