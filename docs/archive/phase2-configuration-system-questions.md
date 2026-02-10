# Phase 2: Configuration System - Design Questions

This document contains questions and concerns about the proposed configuration system that need discussion before finalizing the design.

**Status**: 🟡 Pending Discussion  
**Created**: 2026-02-04  
**Purpose**: Identify improvements and clarify design decisions for the configuration system

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add your answers inline under each question, then we'll update the main design doc.

---

## 🔥 Critical Priority (Architecture & Loading)

### Q1: Configuration Reload Strategy

**Status**: ✅ **DECIDED**

**Context:**
Old TARSy required restart for configuration changes. The design proposes the same approach (no hot-reload).

**Question:**
Should we support hot-reload of configuration or require restart?

**Decision**: **Option A - No Hot-Reload (Restart Required)**

**Rationale:**
- Keep it simple like the old TARSy
- Configuration changes require service restart
- Simpler implementation and easier to reason about
- No risk of partial configuration states
- Clear deployment process with atomic configuration updates

**Implementation:**
- Configuration is loaded once at startup
- Configuration changes require service restart
- No file watching or hot-reload logic needed
- Validation happens only at startup time

---

### Q2: Configuration File Structure

**Status**: ✅ **DECIDED**

**Context:**
Design proposes separate files for agents, chains, MCP servers, and LLM providers.

**Question:**
Should configuration be split into multiple files or use a single file?

**Decision**: **Two YAML files + .env + OAuth2 config**

```
deploy/
└── config/
    ├── tarsy.yaml.example                # Example main config (tracked in git)
    ├── llm-providers.yaml.example        # Example LLM providers (tracked in git)
    ├── .env.example                      # Example environment variables (tracked in git)
    ├── oauth2-proxy.cfg.template         # OAuth2 proxy template (tracked in git)
    ├── tarsy.yaml                        # User's actual config (gitignored)
    ├── llm-providers.yaml                # User's actual config (gitignored)
    ├── .env                              # User's actual env vars (gitignored)
    └── oauth2-proxy.cfg                  # Generated OAuth2 config (gitignored)

# Users copy and customize:
cp deploy/config/tarsy.yaml.example deploy/config/tarsy.yaml
cp deploy/config/llm-providers.yaml.example deploy/config/llm-providers.yaml
cp deploy/config/.env.example deploy/config/.env
```

**Rationale:**

1. **Readability**: When editing agents (frequent), don't want LLM provider noise cluttering the file
2. **Proven pattern**: Old TARSy used `agents.yaml` + `llm-providers.yaml` successfully
3. **Conceptual separation**: "What agents do" vs "What LLMs to use"
4. **Size is manageable**: ~1000 total lines is well within limits (1MB = ~17,000 lines)
5. **Secrets in .env**: Standard practice, works with Docker, follows 12-factor app
6. **OAuth2 as template**: OAuth2 proxy uses `.cfg` format (not YAML), template with env var substitution

**File naming:**
- `tarsy.yaml` (not `agents.yaml`) - more accurate since it contains agents + chains + MCP + defaults
- `llm-providers.yaml` - clear and specific
- `.env` - industry standard for secrets
- `oauth2-proxy.cfg.template` - template file (tracked in git), generates `oauth2-proxy.cfg` (ignored)

---

### Q3: Environment-Specific Configuration

**Status**: ✅ **DECIDED**

**Context:**
Need to support 4 deployment environments:
1. Local dev (host-based, fast iteration) - postgres in container, no OAuth2
2. Podman-compose local dev (all containers) - test OAuth2 proxy, same config as #1
3. K8s/OpenShift dev (manual testing) - deploy/test in cluster, some config differences
4. Production (OpenShift) - user-managed, not in source code

**Question:**
How should environment-specific configuration be handled?

**Decision**: **Environment Variables Only + .env.example**

**Environment Setup:**

```
deploy/
└── config/
    ├── tarsy.yaml.example            # Example main config (tracked in git)
    ├── llm-providers.yaml.example    # Example LLM providers (tracked in git)
    ├── .env.example                  # Example environment variables with comments (tracked in git)
    ├── oauth2-proxy.cfg.template     # OAuth2 template (tracked in git)
    ├── tarsy.yaml                    # User's actual config (gitignored)
    ├── llm-providers.yaml            # User's actual config (gitignored)
    ├── .env                          # User's actual env vars (gitignored)
    └── oauth2-proxy.cfg              # Generated OAuth2 config (gitignored)

# Users copy and customize:
cp deploy/config/tarsy.yaml.example deploy/config/tarsy.yaml
cp deploy/config/llm-providers.yaml.example deploy/config/llm-providers.yaml
cp deploy/config/.env.example deploy/config/.env
# Edit deploy/config/.env based on your environment
```

**Configuration with Environment Variables:**

```yaml
# deploy/config/tarsy.yaml (environment-agnostic)
agents:
  - id: kubernetes-agent
    max_iterations: ${MAX_ITERATIONS}      # Override per environment via .env

# deploy/config/llm-providers.yaml
llm_providers:
  - id: gemini-thinking
    api:
      endpoint: ${GEMINI_API_ENDPOINT}
      api_key: ${GEMINI_API_KEY}           # Secret from .env
    rate_limit:
      requests_per_minute: ${LLM_RATE_LIMIT}
```

**Example .env file:**

```bash
# deploy/config/.env.example
# TARSy Configuration - Environment Variables
# Copy this file to .env and customize for your environment

# =============================================================================
# Database Configuration
# =============================================================================
# Local dev:        DB_HOST=localhost
# Podman dev:       DB_HOST=postgres
# K8s/OpenShift:    DB_HOST=postgres-service
DB_HOST=localhost
DB_PORT=5432
DB_USER=tarsy
DB_PASSWORD=your-db-password
DB_NAME=tarsy

# =============================================================================
# Service Configuration
# =============================================================================
# Local dev:        HTTP_PORT=8080, GRPC_ADDR=localhost:50051
# Podman dev:       HTTP_PORT=8000, GRPC_ADDR=llm-service:50051
# K8s/OpenShift:    HTTP_PORT=8000, GRPC_ADDR=llm-service:50051
HTTP_PORT=8080
GRPC_ADDR=localhost:50051

# =============================================================================
# LLM Provider Secrets
# =============================================================================
GEMINI_API_KEY=your-gemini-api-key-here
GEMINI_ENDPOINT=https://generativelanguage.googleapis.com

# =============================================================================
# LLM Configuration
# =============================================================================
# Dev: higher limits for testing
# Prod: conservative limits
LLM_RATE_LIMIT=100
MAX_ITERATIONS=30

# =============================================================================
# OAuth2 Proxy Configuration (Optional - for Podman/K8s environments)
# =============================================================================
# Uncomment for Podman or K8s environments with OAuth2 authentication
# OAUTH2_CLIENT_ID=your-github-oauth-id
# OAUTH2_CLIENT_SECRET=your-github-oauth-secret
# GITHUB_ORG=your-org
# GITHUB_TEAM=your-team
# ROUTE_HOST=localhost:8080           # Podman: localhost:8080, K8s: tarsy-dev.apps.cluster.example.com
# COOKIE_SECURE=false                 # Podman: false, K8s: true
```

**Production Configuration:**
- **NOT in source code**
- Users create their own K8s manifests (ConfigMaps, Secrets, Deployments)
- K8s dev environment serves as documentation/reference
- Provided: Example manifests in `deploy/` directory (not production secrets)

**Rationale:**

1. **Simplicity**: No merge logic, no override files, no complex precedence rules
2. **12-factor app**: Standard practice for cloud-native applications
3. **Kubernetes-native**: ConfigMaps/Secrets map directly to environment variables
4. **Flexibility**: Easy to override any value without touching YAML files
5. **Reusable config**: Same tarsy.yaml works across all environments (only .env changes)
6. **Security**: Secrets stay in .env (gitignored) or K8s Secrets (never in repo)
7. **No duplication**: Don't need separate config directories or override files

**What varies between environments:**
- Database connection (host, port)
- Service endpoints (localhost vs service names)
- Secrets (API keys, OAuth2 credentials)
- Rate limits and timeouts
- OAuth2 proxy settings (HTTPS vs HTTP)

**What stays the same:**
- Agent definitions and instructions
- Chain configurations
- MCP server definitions (commands, args use env vars)
- LLM provider definitions (endpoints, keys use env vars)

---

## 📋 High Priority (Validation & Safety)

### Q4: Configuration Validation Timing

**Status**: ✅ **DECIDED**

**Context:**
Design proposes validation on startup. Should we also validate when writing configuration files?

**Question:**
When should configuration validation occur?

**Decision:** **Startup Only**

**Rationale:**
- Keep it simple - validation happens when the service starts
- Clear, immediate error messages during startup
- No additional tooling or developer setup required
- Aligns with the simplicity principle established in other decisions

**Implementation:**
- Configuration is validated once at startup
- Service fails to start if configuration is invalid
- Validation uses Go struct tags (`validate`) and custom business logic
- Clear error messages indicate which configuration is invalid and why

---

### Q5: Configuration Schema Documentation

**Status**: ✅ **DECIDED**

**Context:**
YAML configuration needs schema documentation for editors and validation.

**Question:**
Should we generate JSON Schema for configuration files?

**Decision:** **Comments Only (No Schema)**

**Rationale:**
- Keep it simple - inline comments in YAML files
- Go struct tags provide validation at runtime
- No extra tooling or maintenance overhead
- Configuration is primarily edited by developers who can read the Go structs
- Aligns with the simplicity principle

**Implementation:**
```yaml
# deploy/config/tarsy.yaml

agents:
  - id: kubernetes-agent  # Required: unique agent identifier (lowercase, kebab-case)
    name: "Kubernetes Agent"  # Required: human-readable name
    system_prompt: "..."  # Optional: custom system prompt
    max_iterations: 20  # Optional: max iterations (default: 20)
    # ... more fields with descriptive comments
```

**Validation:**
- Go struct tags (`yaml`, `validate`) provide runtime validation
- Startup validation catches configuration errors immediately
- Go code is the source of truth for configuration structure

---

### Q6: Configuration Validation - Fail Fast vs Collect All Errors

**Status**: ✅ **DECIDED**

**Context:**
When validating configuration, should we stop at first error or collect all errors?

**Question:**
Should validation stop at first error or collect all validation errors?

**Decision:** **Fail Fast (Stop at First Error)**

**Rationale:**
- Simpler implementation
- Clear first error to fix
- Faster validation (stops early)
- Aligns with the simplicity principle

**Implementation:**
```go
func (v *ConfigValidator) ValidateAll() error {
    if err := v.validateAgents(); err != nil {
        return fmt.Errorf("agent validation failed: %w", err)
    }
    
    if err := v.validateChains(); err != nil {
        return fmt.Errorf("chain validation failed: %w", err)
    }
    
    if err := v.validateMCPServers(); err != nil {
        return fmt.Errorf("MCP server validation failed: %w", err)
    }
    
    if err := v.validateLLMProviders(); err != nil {
        return fmt.Errorf("LLM provider validation failed: %w", err)
    }
    
    return nil
}

// Example output:
// ❌ Configuration loading failed:
// agent validation failed: agent 'kubernetes-agent': LLM provider 'invalid' not found
```

---

## 📊 Medium Priority (Features & Usability)

### Q7: Configuration Precedence and Merging

**Status**: ✅ **DECIDED**

**Context:**
With multiple configuration sources (defaults, component configs, env vars, API overrides), need clear precedence rules.

**Question:**
How should configuration precedence and merging work?

**Decision:** **Agreed Precedence Order (Lowest to Highest)**

1. System defaults (`tarsy.yaml` - defaults section)
2. Component configuration (`tarsy.yaml` - agents/chains/mcp_servers sections + `llm-providers.yaml`)
3. Environment variables (`${VAR}` interpolation)
4. Per-alert overrides (API request - runtime only)

**Rationale:**
- Clear hierarchy: defaults → components → env vars → runtime
- Environment variables interpolated at startup using `${VAR}` or `$VAR` syntax (via `os.ExpandEnv`)
- No environment override YAML files (decided in Q3)
- Per-alert API overrides are transient (not persisted)

**How It Works:**

1. **Defaults Apply to Components:**
   ```yaml
   # tarsy.yaml - defaults section
   defaults:
     llm:
       temperature: 0.7
       top_p: 0.95
       max_tokens: 4096
   
   # tarsy.yaml - agent overrides specific values
   agents:
     - id: kubernetes-agent
       llm_config:
         temperature: 0.5  # Override only temperature
         # top_p and max_tokens inherit from defaults
   ```

2. **Environment Variable Interpolation:**
   ```yaml
   # llm-providers.yaml
   llm_providers:
     - id: gemini-thinking
       api:
         api_key: ${GEMINI_API_KEY}        # From .env (required)
         endpoint: ${GEMINI_ENDPOINT}      # From .env (required)
       parameters:
         temperature: ${LLM_TEMP}          # From .env (required)
   
   # .env file provides values:
   # GEMINI_API_KEY=your-key-here
   # GEMINI_ENDPOINT=https://generativelanguage.googleapis.com
   # LLM_TEMP=0.7
   ```

3. **Per-Alert API Overrides (Runtime):**
   ```go
   // API request can override MCP servers and native tools
   POST /api/v1/sessions/:id/interactions
   {
     "message": "...",
     "mcp_server_ids": ["custom-server"],  // Override
     "native_tool_ids": ["kubectl"]        // Override
   }
   ```

**Implementation:**
- Defaults are applied at configuration load time
- Environment variables are interpolated during YAML parsing
- Component configs can explicitly override any default value
- Missing fields inherit from defaults
- Per-alert overrides are applied at runtime (not persisted)

---

### Q8: Configuration Versioning

**Status**: ✅ **DECIDED**

**Context:**
Configuration may evolve over time. Should we support version field and migration?

**Question:**
Should configuration files have a version field and migration support?

**Decision:** **No Versioning (For Now)**

**Rationale:**
- Keep it simple - no version field or migration logic
- Breaking changes can be handled manually with documentation
- Can add versioning later if needed
- Aligns with the simplicity principle

**Implementation:**
```yaml
# deploy/config/tarsy.yaml
agents:
  - id: kubernetes-agent
    name: "Kubernetes Agent"
    # No version field
```

**Future Consideration:**
- If we need versioning later, we can add it
- Breaking changes will be documented in release notes
- Users responsible for updating configuration based on documentation

---

### Q9: Configuration Testing Utilities

**Status**: ✅ **DECIDED**

**Context:**
Developers and operators need to test configuration changes before deploying.

**Question:**
What testing utilities should we provide for configuration?

**Decision:** **No Additional Validation Tools**

**Rationale:**
- Developers can test configuration in dev environment (same as prod)
- Service validates configuration at startup - won't start if config is broken
- Failed deployment is safe - service simply won't start/restart
- Keeps tooling simple and minimal
- Aligns with the simplicity principle

**Testing Strategy:**
1. **Local Dev Testing:** Test configuration changes in local dev environment
2. **Podman Dev Testing:** Test in containerized environment before K8s
3. **K8s Dev Testing:** Test in K8s dev environment before production
4. **Startup Validation:** Service validates configuration on startup
5. **Safe Failure:** If configuration is broken, service fails to start (safe state)

---

## 💡 Low Priority (Nice-to-Have)

### Q10: Configuration Templates and Examples

**Status**: ✅ **DECIDED**

**Context:**
Need examples to help users understand configuration patterns.

**Question:**
What configuration templates and examples should we provide?

**Decision:** **Simple Examples with Prefixes**

**Rationale:**
- Keep it simple - one example per configuration file
- Use "example" or "template" prefix in filename
- Keep examples alongside actual config files for easy discovery
- Users copy and customize the examples

**File Structure:**
```
deploy/
└── config/
    ├── tarsy.yaml.example           # Example main config (tracked in git)
    ├── llm-providers.yaml.example   # Example LLM providers (tracked in git)
    ├── .env.example                 # Example environment variables (tracked in git)
    ├── oauth2-proxy.cfg.template    # OAuth2 proxy template (tracked in git)
    ├── tarsy.yaml                   # User's actual config (gitignored)
    ├── llm-providers.yaml           # User's actual config (gitignored)
    ├── .env                         # User's actual env vars (gitignored)
    └── oauth2-proxy.cfg             # Generated OAuth2 config (gitignored)

# Users copy and customize:
cp deploy/config/tarsy.yaml.example deploy/config/tarsy.yaml
cp deploy/config/llm-providers.yaml.example deploy/config/llm-providers.yaml
cp deploy/config/.env.example deploy/config/.env
```

**What Examples Include:**
- **`tarsy.yaml.example`**: Complete example with agents, chains, MCP servers, defaults, and comments
- **`llm-providers.yaml.example`**: Example LLM provider configurations with comments
- **`.env.example`**: All required environment variables with placeholder values
- **`oauth2-proxy.cfg.template`**: OAuth2 proxy template with env var placeholders

---

### Q11: Configuration Management API

**Status**: ✅ **DECIDED**

**Context:**
Should we expose configuration management via API (read-only or read-write)?

**Question:**
Should we provide an API for configuration management?

**Decision:** **No API (File-Based Only)**

**Rationale:**
- Simple implementation
- GitOps workflow (config in git)
- Clear audit trail (git history)
- Code review for configuration changes
- Aligns with the simplicity principle

**Configuration Management:**
- Configuration stored in YAML files in `deploy/config/`
- Changes made by editing files
- Configuration tracked in git
- Service restart required for changes to take effect
- No runtime configuration API (neither read nor write)

---

### Q12: Configuration Change Notifications

**Status**: ❌ **NOT APPLICABLE**

**Context:**
If hot-reload is supported, how should services be notified of configuration changes?

**Question:**
How should configuration changes be communicated to running services?

**Decision:** **Not Applicable**

**Rationale:**
- Q1 decided: No hot-reload, restart required
- Since configuration changes require restart, no notification system needed
- Service picks up new configuration on startup
- This question is only relevant if hot-reload is implemented in the future

---

## 📝 Summary Checklist

Track which questions we've addressed:

### Critical Priority
- [x] Q1: Configuration Reload Strategy → **No hot-reload, restart required** ✅
- [x] Q2: Configuration File Structure → **tarsy.yaml + llm-providers.yaml + .env** ✅
- [x] Q3: Environment-Specific Configuration → **Environment variables only (.env)** ✅

### High Priority
- [x] Q4: Configuration Validation Timing → **Startup only** ✅
- [x] Q5: Configuration Schema Documentation → **Comments only** ✅
- [x] Q6: Configuration Validation → **Fail fast** ✅

### Medium Priority
- [x] Q7: Configuration Precedence and Merging → **Defaults → Components → Env Vars → API** ✅
- [x] Q8: Configuration Versioning → **No versioning (for now)** ✅
- [x] Q9: Configuration Testing Utilities → **No additional tools** ✅

### Low Priority
- [x] Q10: Configuration Templates and Examples → **Simple .example files** ✅
- [x] Q11: Configuration Management API → **No API, file-based only** ✅
- [x] Q12: Configuration Change Notifications → **N/A (no hot-reload)** ❌

---

## Next Steps

1. ~~Review and discuss each question in order~~ ✅ **COMPLETE**
2. ~~Make decisions and mark status (✅/❌/⏸️)~~ ✅ **COMPLETE**
3. **Update main design document based on decisions** ⬅️ NEXT
4. Begin implementation of agreed-upon design
5. Create example configuration files
6. Write configuration documentation

---

## Notes

**Design Philosophy:**
- Start simple, add complexity only when needed
- Favor configuration in files over database
- Embrace GitOps and infrastructure-as-code practices
- Strong validation to catch errors early
- Clear error messages for easy troubleshooting

**Key Tradeoffs:**
- **Simplicity vs Flexibility**: Hot-reload adds flexibility but increases complexity
- **Validation Timing**: Early validation (pre-commit) vs late validation (runtime)
- **Configuration Structure**: Single file simplicity vs multiple file organization
- **Versioning**: Manual migration simplicity vs automatic migration complexity
