# =============================================================================
# OpenShift Deployment
# =============================================================================

# Colors
GREEN := \033[0;32m
YELLOW := \033[0;33m
RED := \033[0;31m
BLUE := \033[0;34m
NC := \033[0m

# OpenShift variables
OPENSHIFT_NAMESPACE := tarsy
OPENSHIFT_REGISTRY = $(shell oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}' 2>/dev/null || echo "registry.not.found")
TARSY_IMAGE = $(OPENSHIFT_REGISTRY)/$(OPENSHIFT_NAMESPACE)/tarsy
LLM_IMAGE = $(OPENSHIFT_REGISTRY)/$(OPENSHIFT_NAMESPACE)/tarsy-llm
IMAGE_TAG := dev
USE_SKOPEO ?=

# Auto-detect cluster domain
CLUSTER_DOMAIN = $(shell oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}' 2>/dev/null)

# Auto-load openshift.env for OpenShift targets
OPENSHIFT_TARGETS := openshift-check openshift-login-registry openshift-create-namespace \
                     openshift-build-tarsy openshift-build-llm openshift-build-all \
                     openshift-push-tarsy openshift-push-llm openshift-push-all \
                     openshift-create-secrets openshift-check-config-files \
                     openshift-apply openshift-deploy openshift-redeploy \
                     openshift-status openshift-urls openshift-logs \
                     openshift-db-reset openshift-clean openshift-clean-images

ifneq ($(filter $(OPENSHIFT_TARGETS),$(MAKECMDGOALS)),)
	ifneq ($(CLUSTER_DOMAIN),)
		ROUTE_HOST := $(OPENSHIFT_NAMESPACE).$(CLUSTER_DOMAIN)
	endif
	-include deploy/openshift.env
	ifndef ROUTE_HOST
		$(error ROUTE_HOST not defined. Set in deploy/openshift.env or ensure oc context is correct)
	endif
endif

# ── Prerequisites ────────────────────────────────────────

.PHONY: openshift-check
openshift-check: ## Check OpenShift login and registry access
	@echo -e "$(BLUE)Checking OpenShift prerequisites...$(NC)"
	@command -v oc >/dev/null 2>&1 || { echo -e "$(RED)oc CLI not found$(NC)"; exit 1; }
	@oc whoami >/dev/null 2>&1 || { echo -e "$(RED)Not logged into OpenShift. Run: oc login$(NC)"; exit 1; }
	@[ "$(OPENSHIFT_REGISTRY)" != "registry.not.found" ] || { \
		echo -e "$(RED)OpenShift internal registry not exposed$(NC)"; \
		echo -e "$(YELLOW)Expose it with:$(NC)"; \
		echo -e "$(YELLOW)  oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{\"spec\":{\"defaultRoute\":true}}' --type=merge$(NC)"; \
		exit 1; }
	@echo -e "$(GREEN)✓ Logged in as: $$(oc whoami) | Registry: $(OPENSHIFT_REGISTRY)$(NC)"

.PHONY: openshift-login-registry
openshift-login-registry: openshift-check ## Login podman to OpenShift registry
	@podman login --tls-verify=false -u $$(oc whoami) -p $$(oc whoami -t) $(OPENSHIFT_REGISTRY)

.PHONY: openshift-create-namespace
openshift-create-namespace: openshift-check ## Create namespace if needed
	@oc get namespace $(OPENSHIFT_NAMESPACE) >/dev/null 2>&1 || oc create namespace $(OPENSHIFT_NAMESPACE)

# ── Build ────────────────────────────────────────────────

.PHONY: openshift-build-tarsy
openshift-build-tarsy: openshift-login-registry ## Build tarsy image for OpenShift
	@echo -e "$(BLUE)Building tarsy image...$(NC)"
	@podman build -t localhost/tarsy:latest -f Dockerfile .
	@echo -e "$(GREEN)✅ tarsy image built$(NC)"

.PHONY: openshift-build-llm
openshift-build-llm: openshift-login-registry ## Build llm-service image for OpenShift
	@echo -e "$(BLUE)Building llm-service image...$(NC)"
	@podman build -t localhost/tarsy-llm:latest -f llm-service/Dockerfile llm-service/
	@echo -e "$(GREEN)✅ tarsy-llm image built$(NC)"

.PHONY: openshift-build-all
openshift-build-all: openshift-build-tarsy openshift-build-llm ## Build all images

# ── Push ─────────────────────────────────────────────────

.PHONY: openshift-push-tarsy
openshift-push-tarsy: openshift-build-tarsy openshift-create-namespace ## Push tarsy to OpenShift registry
	@podman tag localhost/tarsy:latest $(TARSY_IMAGE):$(IMAGE_TAG)
	@if [ -n "$(USE_SKOPEO)" ]; then \
		podman save localhost/tarsy:latest -o /tmp/tarsy.tar; \
		skopeo copy --dest-tls-verify=false docker-archive:/tmp/tarsy.tar docker://$(TARSY_IMAGE):$(IMAGE_TAG); \
		rm -f /tmp/tarsy.tar; \
	else \
		podman push --tls-verify=false $(TARSY_IMAGE):$(IMAGE_TAG); \
	fi
	@echo -e "$(GREEN)✅ tarsy pushed: $(TARSY_IMAGE):$(IMAGE_TAG)$(NC)"

.PHONY: openshift-push-llm
openshift-push-llm: openshift-build-llm openshift-create-namespace ## Push llm-service to OpenShift registry
	@podman tag localhost/tarsy-llm:latest $(LLM_IMAGE):$(IMAGE_TAG)
	@if [ -n "$(USE_SKOPEO)" ]; then \
		podman save localhost/tarsy-llm:latest -o /tmp/tarsy-llm.tar; \
		skopeo copy --dest-tls-verify=false docker-archive:/tmp/tarsy-llm.tar docker://$(LLM_IMAGE):$(IMAGE_TAG); \
		rm -f /tmp/tarsy-llm.tar; \
	else \
		podman push --tls-verify=false $(LLM_IMAGE):$(IMAGE_TAG); \
	fi
	@echo -e "$(GREEN)✅ tarsy-llm pushed: $(LLM_IMAGE):$(IMAGE_TAG)$(NC)"

.PHONY: openshift-push-all
openshift-push-all: openshift-push-tarsy openshift-push-llm ## Build and push all images

# ── Secrets ──────────────────────────────────────────────

.PHONY: openshift-create-secrets
openshift-create-secrets: openshift-check openshift-create-namespace ## Create secrets from env vars
	@[ -n "$$GOOGLE_API_KEY" ] || { echo -e "$(RED)GOOGLE_API_KEY not set$(NC)"; exit 1; }
	@[ -n "$$GITHUB_TOKEN" ] || { echo -e "$(RED)GITHUB_TOKEN not set$(NC)"; exit 1; }
	@[ -n "$$OAUTH2_CLIENT_ID" ] || { echo -e "$(RED)OAUTH2_CLIENT_ID not set$(NC)"; exit 1; }
	@[ -n "$$OAUTH2_CLIENT_SECRET" ] || { echo -e "$(RED)OAUTH2_CLIENT_SECRET not set$(NC)"; exit 1; }
	@export DATABASE_USER=$${DATABASE_USER:-tarsy}; \
	export DATABASE_NAME=$${DATABASE_NAME:-tarsy}; \
	export DATABASE_HOST=$${DATABASE_HOST:-tarsy-database}; \
	export DATABASE_PORT=$${DATABASE_PORT:-5432}; \
	if [ -z "$$DATABASE_PASSWORD" ]; then \
		EXISTING_PW=$$(oc get secret database-secret -n $(OPENSHIFT_NAMESPACE) -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null); \
		if [ -n "$$EXISTING_PW" ]; then \
			echo -e "$(BLUE)Reusing existing database password from cluster$(NC)"; \
			export DATABASE_PASSWORD="$$EXISTING_PW"; \
		fi; \
	fi; \
	oc process -f deploy/kustomize/base/secrets-template.yaml \
		-p NAMESPACE=$(OPENSHIFT_NAMESPACE) \
		-p GOOGLE_API_KEY="$$GOOGLE_API_KEY" \
		-p GITHUB_TOKEN="$$GITHUB_TOKEN" \
		-p OPENAI_API_KEY="$$OPENAI_API_KEY" \
		-p ANTHROPIC_API_KEY="$$ANTHROPIC_API_KEY" \
		-p XAI_API_KEY="$$XAI_API_KEY" \
		-p VERTEX_AI_PROJECT="$$VERTEX_AI_PROJECT" \
		-p GOOGLE_SERVICE_ACCOUNT_KEY="$$GOOGLE_SERVICE_ACCOUNT_KEY" \
		-p SLACK_BOT_TOKEN="$$SLACK_BOT_TOKEN" \
		-p DATABASE_USER="$$DATABASE_USER" \
		-p DATABASE_NAME="$$DATABASE_NAME" \
		-p DATABASE_HOST="$$DATABASE_HOST" \
		-p DATABASE_PORT="$$DATABASE_PORT" \
		-p OAUTH2_CLIENT_ID="$$OAUTH2_CLIENT_ID" \
		-p OAUTH2_CLIENT_SECRET="$$OAUTH2_CLIENT_SECRET" \
		$${DATABASE_PASSWORD:+-p DATABASE_PASSWORD="$$DATABASE_PASSWORD"} \
		$${OAUTH2_COOKIE_SECRET:+-p OAUTH2_COOKIE_SECRET="$$OAUTH2_COOKIE_SECRET"} \
		| oc apply -f -
	@echo -e "$(GREEN)✅ Secrets created in $(OPENSHIFT_NAMESPACE)$(NC)"

# ── Config Sync ──────────────────────────────────────────

.PHONY: openshift-check-config-files
openshift-check-config-files: ## Sync config files to overlay directory
	@echo -e "$(BLUE)Syncing config files...$(NC)"
	@mkdir -p deploy/kustomize/overlays/development/templates
	@[ -f deploy/config/tarsy.yaml ] && \
		sed -e 's|http://localhost:5173|https://$(ROUTE_HOST)|g' \
		deploy/config/tarsy.yaml > deploy/kustomize/overlays/development/tarsy.yaml || \
		{ echo -e "$(RED)deploy/config/tarsy.yaml not found$(NC)"; exit 1; }
	@[ -f deploy/config/llm-providers.yaml ] && cp deploy/config/llm-providers.yaml deploy/kustomize/overlays/development/ || \
		{ echo -e "$(RED)deploy/config/llm-providers.yaml not found$(NC)"; exit 1; }
	@[ -d deploy/config/templates ] && cp -r deploy/config/templates/* deploy/kustomize/overlays/development/templates/ || \
		{ echo -e "$(RED)deploy/config/templates/ not found$(NC)"; exit 1; }
	@echo -e "$(BLUE)Generating overlay oauth2-proxy.cfg from template...$(NC)"
	@sed -e 's|http://tarsy:8080/|http://localhost:8080/|g' \
		-e 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' \
		-e 's|{{COOKIE_SECURE}}|true|g' \
		-e 's|{{OAUTH2_PROXY_REDIRECT_URL}}|https://$(ROUTE_HOST)/oauth2/callback|g' \
		-e 's|{{GITHUB_ORG}}|$(GITHUB_ORG)|g' \
		-e 's|{{GITHUB_TEAM}}|$(GITHUB_TEAM)|g' \
		-e 's|{{OAUTH2_CLIENT_ID}}|OVERRIDDEN_BY_ENV|g' \
		-e 's|{{OAUTH2_CLIENT_SECRET}}|OVERRIDDEN_BY_ENV|g' \
		-e 's|{{OAUTH2_COOKIE_SECRET}}|OVERRIDDEN_BY_ENV|g' \
		deploy/config/oauth2-proxy.cfg.template \
		> deploy/kustomize/overlays/development/oauth2-proxy.cfg
	@echo -e "$(GREEN)✅ Config files synced$(NC)"

# ── Apply ────────────────────────────────────────────────

.PHONY: openshift-apply
openshift-apply: openshift-check openshift-check-config-files ## Apply Kustomize manifests
	@echo -e "$(BLUE)Applying manifests (Route Host: $(ROUTE_HOST))...$(NC)"
	@oc kustomize deploy/kustomize/overlays/development/ \
		| sed 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' \
		| oc apply -f -
	@echo -e "$(GREEN)✅ Manifests applied to $(OPENSHIFT_NAMESPACE)$(NC)"

# ── Deploy (full) ────────────────────────────────────────

.PHONY: openshift-deploy
openshift-deploy: openshift-create-secrets openshift-push-all openshift-apply ## Full deployment
	@for d in $$(oc get deployments -n $(OPENSHIFT_NAMESPACE) -o name 2>/dev/null | sed 's|deployment.apps/||'); do \
		oc rollout restart deployment/$$d -n $(OPENSHIFT_NAMESPACE); \
	done
	@echo -e "$(GREEN)✅ Deployed to $(OPENSHIFT_NAMESPACE)$(NC)"

.PHONY: openshift-redeploy
openshift-redeploy: openshift-push-all openshift-apply ## Rebuild and update (no secrets)

# ── Status ───────────────────────────────────────────────

.PHONY: openshift-status
openshift-status: openshift-check ## Show deployment status
	@echo -e "$(BLUE)Namespace: $(OPENSHIFT_NAMESPACE)$(NC)"
	@echo -e "\n$(YELLOW)Pods:$(NC)" && oc get pods -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true
	@echo -e "\n$(YELLOW)Services:$(NC)" && oc get services -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true
	@echo -e "\n$(YELLOW)Routes:$(NC)" && oc get routes -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true

.PHONY: openshift-urls
openshift-urls: openshift-check ## Show application URLs
	@WEB=$$(oc get route tarsy -n $(OPENSHIFT_NAMESPACE) -o jsonpath='{.spec.host}' 2>/dev/null); \
	echo -e "$(BLUE)Dashboard: https://$$WEB$(NC)"; \
	echo -e "$(BLUE)API (browser): https://$$WEB/api/v1/$(NC)"; \
	echo -e "$(BLUE)API (in-cluster): https://tarsy-api.$(OPENSHIFT_NAMESPACE).svc:8443/api/v1/$(NC)"; \
	echo -e "$(BLUE)Health: https://$$WEB/health$(NC)"

.PHONY: openshift-logs
openshift-logs: openshift-check ## Show tarsy pod logs (all containers)
	@oc logs -l component=tarsy -n $(OPENSHIFT_NAMESPACE) --all-containers --tail=50 2>/dev/null || echo "No pods found"

# ── Cleanup ──────────────────────────────────────────────

.PHONY: openshift-clean
openshift-clean: openshift-check ## Delete all TARSy resources
	@printf "Delete all TARSy resources from $(OPENSHIFT_NAMESPACE)? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		oc kustomize deploy/kustomize/overlays/development/ \
			| sed 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' \
			| oc delete -f - 2>/dev/null || true; \
		echo -e "$(GREEN)✅ Resources deleted$(NC)";; \
	*) echo "Cancelled";; esac

.PHONY: openshift-clean-images
openshift-clean-images: openshift-check ## Delete images from registry
	@printf "Delete TARSy images from registry? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		oc delete imagestream tarsy -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		oc delete imagestream tarsy-llm -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		echo -e "$(GREEN)✅ Images deleted$(NC)";; \
	*) echo "Cancelled";; esac

# ── Database ─────────────────────────────────────────────

.PHONY: openshift-db-reset
openshift-db-reset: openshift-check ## Reset PostgreSQL (DESTRUCTIVE)
	@printf "DELETE ALL DATABASE DATA? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		oc scale deployment tarsy-database --replicas=0 -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		oc wait --for=delete pod -l component=database -n $(OPENSHIFT_NAMESPACE) --timeout=60s 2>/dev/null || true; \
		oc delete pvc database-data -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		$(MAKE) openshift-apply 2>/dev/null; \
		oc scale deployment tarsy-database --replicas=1 -n $(OPENSHIFT_NAMESPACE); \
		oc wait --for=condition=available deployment/tarsy-database -n $(OPENSHIFT_NAMESPACE) --timeout=120s; \
		oc rollout restart deployment/tarsy -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		echo -e "$(GREEN)✅ Database reset$(NC)";; \
	*) echo "Cancelled";; esac
