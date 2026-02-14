# =============================================================================
# Help and Documentation
# =============================================================================

# Color definitions
GREEN := \033[0;32m
YELLOW := \033[0;33m
BLUE := \033[0;34m
RED := \033[0;31m
NC := \033[0m # No Color

.PHONY: help
help: ## Show this help message
	@echo -e "$(GREEN)TARSy Development Commands$(NC)"
	@echo "================================="
	@echo ""
	@echo -e "$(YELLOW)🚀 Quick Start:$(NC)"
	@echo "  make dev-setup    # First time setup (DB + code generation)"
	@echo "  make dev          # Start everything (DB + backend + dashboard)"
	@echo "  make build        # Build the application"
	@echo ""
	@echo -e "$(YELLOW)📋 Available Commands:$(NC)"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(BLUE)%-25s$(NC) %s\n", $$1, $$2}'
	@echo ""
	@echo -e "$(YELLOW)💡 Tip:$(NC) Use 'make <tab>' for command completion"
