# =============================================================================
# Container Orchestration (podman-compose)
# =============================================================================

COMPOSE := COMPOSE_PROJECT_NAME=tarsy podman compose -f deploy/podman-compose.yml

# ── Build ────────────────────────────────────────────────

.PHONY: containers-build
containers-build: ## Build tarsy and llm-service container images
	@echo -e "$(YELLOW)Building container images...$(NC)"
	@podman build -t tarsy:dev -f Dockerfile .
	@podman build -t tarsy-llm:dev -f llm-service/Dockerfile llm-service/
	@echo -e "$(GREEN)✅ Container images built: tarsy:dev, tarsy-llm:dev$(NC)"

# ── Deploy ───────────────────────────────────────────────

.PHONY: containers-deploy
containers-deploy: oauth2-config ## Deploy all containers (build + start)
	@echo -e "$(YELLOW)Deploying containers...$(NC)"
	@$(COMPOSE) up -d --build
	@echo -e "$(GREEN)✅ Containers deployed$(NC)"
	@echo -e "$(BLUE)  Dashboard: http://localhost:8080$(NC)"
	@echo -e "$(BLUE)  Health:    http://localhost:8080/health$(NC)"

.PHONY: containers-deploy-fresh
containers-deploy-fresh: containers-clean containers-deploy ## Clean rebuild and deploy

.PHONY: containers-redeploy
containers-redeploy: oauth2-config ## Rebuild and restart tarsy container only
	@echo -e "$(YELLOW)Redeploying tarsy container...$(NC)"
	@$(COMPOSE) up -d --build tarsy
	@echo -e "$(GREEN)✅ Tarsy container redeployed$(NC)"

# ── Status ───────────────────────────────────────────────

.PHONY: containers-status
containers-status: ## Show container status
	@$(COMPOSE) ps

.PHONY: containers-logs
containers-logs: ## Follow container logs
	@$(COMPOSE) logs -f

.PHONY: containers-logs-tarsy
containers-logs-tarsy: ## Follow tarsy container logs
	@$(COMPOSE) logs -f tarsy

# ── Stop / Clean ─────────────────────────────────────────

.PHONY: containers-stop
containers-stop: ## Stop all containers
	@$(COMPOSE) down
	@echo -e "$(GREEN)✅ Containers stopped$(NC)"

.PHONY: containers-clean
containers-clean: ## Stop containers and remove volumes
	@$(COMPOSE) down -v
	@echo -e "$(GREEN)✅ Containers and volumes cleaned$(NC)"

.PHONY: containers-db-reset
containers-db-reset: ## Reset database (stop, remove volume, restart)
	@$(COMPOSE) stop postgres
	@$(COMPOSE) rm -f postgres
	@podman volume rm tarsy_postgres_data 2>/dev/null || true
	@$(COMPOSE) up -d postgres
	@echo -e "$(GREEN)✅ Database reset$(NC)"

# ── Config Generation ────────────────────────────────────

.PHONY: oauth2-config
oauth2-config: ## Generate oauth2-proxy.cfg from template + oauth.env
	@if [ ! -f deploy/config/oauth.env ]; then \
		echo -e "$(RED)ERROR: deploy/config/oauth.env not found$(NC)"; \
		echo "  Copy from oauth.env.example:"; \
		echo "    cp deploy/config/oauth.env.example deploy/config/oauth.env"; \
		exit 1; \
	fi
	@set -a && source deploy/config/oauth.env && set +a && \
		sed -e "s|{{OAUTH2_CLIENT_ID}}|$${OAUTH2_CLIENT_ID}|g" \
		    -e "s|{{OAUTH2_CLIENT_SECRET}}|$${OAUTH2_CLIENT_SECRET}|g" \
		    -e "s|{{OAUTH2_COOKIE_SECRET}}|$${OAUTH2_COOKIE_SECRET}|g" \
		    -e "s|{{OAUTH2_PROXY_REDIRECT_URL}}|$${OAUTH2_PROXY_REDIRECT_URL:-http://localhost:8080/oauth2/callback}|g" \
		    -e "s|{{ROUTE_HOST}}|$${ROUTE_HOST:-localhost:8080}|g" \
		    -e "s|{{COOKIE_SECURE}}|$${COOKIE_SECURE:-false}|g" \
		    -e "s|{{GITHUB_ORG}}|$${GITHUB_ORG}|g" \
		    -e "s|{{GITHUB_TEAM}}|$${GITHUB_TEAM}|g" \
		    deploy/config/oauth2-proxy.cfg.template > deploy/config/oauth2-proxy.cfg
	@echo -e "$(GREEN)Generated deploy/config/oauth2-proxy.cfg$(NC)"
