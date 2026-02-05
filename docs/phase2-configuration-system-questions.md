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

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Old TARSy required restart for configuration changes. The design proposes the same approach (no hot-reload).

**Question:**
Should we support hot-reload of configuration or require restart?

**Options:**

**Option A: No Hot-Reload (Restart Required)**
```
Pros:
✅ Simpler implementation
✅ No partial configuration states
✅ Clear deployment process
✅ Easier to test and reason about
✅ Atomic configuration updates

Cons:
❌ Requires service restart for config changes
❌ Brief downtime during restart
❌ Slower iteration during development
```

**Option B: Hot-Reload Support**
```
Pros:
✅ No downtime for config changes
✅ Faster iteration during development
✅ Can update MCP servers without restart
✅ Can enable/disable agents dynamically

Cons:
❌ More complex implementation
❌ Must handle partial configuration states
❌ Risk of inconsistent state during reload
❌ Complex validation (old vs new config)
❌ Agent/chain registry invalidation logic
❌ In-flight sessions might use old config
```

**Option C: Hybrid Approach**
```
Pros:
✅ Hot-reload for safe changes (enable/disable, MCP servers)
✅ Restart required for structural changes (new agents, chains)

Cons:
❌ Most complex to implement
❌ Unclear boundaries between hot-reload and restart
❌ May confuse operators
```

**Implementation Considerations for Option B:**

```go
// pkg/config/reloader.go

type ConfigReloader struct {
    loader     *ConfigLoader
    registries *Registries
    mu         sync.RWMutex
}

func (r *ConfigReloader) Reload(ctx context.Context) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    // Load new configuration
    newConfig, err := r.loader.Load(ctx)
    if err != nil {
        return fmt.Errorf("failed to load new config: %w", err)
    }
    
    // Validate new configuration
    validator := NewValidator(newConfig.Registries)
    if err := validator.ValidateAll(); err != nil {
        return fmt.Errorf("new config validation failed: %w", err)
    }
    
    // Atomic swap (all or nothing)
    r.registries.Agents = newConfig.AgentRegistry
    r.registries.Chains = newConfig.ChainRegistry
    r.registries.MCPServers = newConfig.MCPServerRegistry
    r.registries.LLMProviders = newConfig.LLMProviderRegistry
    
    log.Info("Configuration reloaded successfully")
    return nil
}

// Watch for file changes (optional)
func (r *ConfigReloader) Watch(ctx context.Context) {
    watcher, _ := fsnotify.NewWatcher()
    watcher.Add("./config")
    
    for {
        select {
        case event := <-watcher.Events:
            if event.Op&fsnotify.Write == fsnotify.Write {
                log.Info("Config file changed, reloading", "file", event.Name)
                if err := r.Reload(ctx); err != nil {
                    log.Error("Failed to reload config", "error", err)
                }
            }
        case <-ctx.Done():
            return
        }
    }
}
```

**Question for Discussion:**
1. Is hot-reload important enough to justify the complexity?
2. Do we expect frequent configuration changes in production?
3. Can we tolerate brief downtime for configuration updates?

**Recommendation**: Start with **Option A (No Hot-Reload)** for simplicity. Can add hot-reload later if needed.

---

### Q2: Configuration File Structure

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes separate files for agents, chains, MCP servers, and LLM providers.

**Question:**
Should configuration be split into multiple files or use a single file?

**Current Proposal (Multiple Files):**
```
config/
├── agents.yaml
├── chains.yaml
├── mcp-servers.yaml
├── llm-providers.yaml
└── defaults.yaml
```

**Alternative (Single File):**
```
config/
└── tarsy.yaml  # Contains all configuration
```

**Pros/Cons:**

**Multiple Files:**
```
Pros:
✅ Clear separation of concerns
✅ Easier to find specific configuration
✅ Can edit one file without affecting others
✅ Better for large configurations
✅ Parallel editing by different team members

Cons:
❌ More files to manage
❌ Cross-file references (agent → LLM provider)
❌ Must load and merge multiple files
```

**Single File:**
```
Pros:
✅ One file to manage
✅ All configuration in one place
✅ Easier to copy/share full configuration
✅ No cross-file reference issues

Cons:
❌ Large file (harder to navigate)
❌ All-or-nothing editing
❌ Higher risk of merge conflicts
❌ Harder to find specific configuration
```

**Question for Discussion:**
1. Do we expect configuration to grow large over time?
2. Will different team members edit different parts?
3. Is ease of navigation more important than single-file simplicity?

**Recommendation**: Use **multiple files** for better organization and scalability.

---

### Q3: Environment-Specific Configuration

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes optional environment overrides in `config/environments/{env}.yaml`.

**Question:**
How should environment-specific configuration be handled?

**Options:**

**Option A: Environment Override Files**
```
config/
├── agents.yaml             # Base configuration
├── chains.yaml
├── mcp-servers.yaml
├── llm-providers.yaml
├── defaults.yaml
└── environments/
    ├── development.yaml    # Dev overrides
    ├── staging.yaml        # Staging overrides
    └── production.yaml     # Production overrides

# Start service with environment flag
./tarsy --environment=production
```

**Example Override:**
```yaml
# config/environments/development.yaml
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: 1.0  # Higher temperature for dev testing
    rate_limit:
      requests_per_minute: 1000  # Higher limits for dev
```

**Option B: Environment Variables Only**
```
config/
├── agents.yaml
├── chains.yaml
├── mcp-servers.yaml
├── llm-providers.yaml
└── defaults.yaml

# Use environment variables for all environment-specific config
# agents.yaml
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: ${LLM_TEMPERATURE:-0.7}
    rate_limit:
      requests_per_minute: ${LLM_RATE_LIMIT:-60}
```

**Option C: Separate Config Directories**
```
config/
├── development/
│   ├── agents.yaml
│   ├── chains.yaml
│   └── ...
├── staging/
│   └── ...
└── production/
    └── ...

# Start service with config directory
./tarsy --config-dir=./config/production
```

**Comparison:**

**Override Files:**
```
Pros:
✅ Clear separation of base and overrides
✅ Easy to see what changes per environment
✅ Shared base configuration
✅ Can override specific fields only

Cons:
❌ Merge logic required
❌ Two places to look for configuration
❌ Potential confusion about precedence
```

**Environment Variables:**
```
Pros:
✅ Simple implementation
✅ Standard practice (12-factor app)
✅ Easy to override in deployment

Cons:
❌ Limited to simple values (not complex structures)
❌ Hard to override nested configuration
❌ Environment variables can be verbose
```

**Separate Directories:**
```
Pros:
✅ Complete separation
✅ No merge logic needed
✅ Easy to see full config for environment

Cons:
❌ Configuration duplication
❌ Hard to maintain consistency
❌ Changes to base must be replicated
```

**Question for Discussion:**
1. How much configuration varies between environments?
2. Do we need to override complex nested structures?
3. Is it important to see full configuration for an environment?

**Recommendation**: Use **Option A (Override Files)** for flexibility + **Option B (Environment Variables)** for secrets and simple values.

---

## 📋 High Priority (Validation & Safety)

### Q4: Configuration Validation Timing

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Design proposes validation on startup. Should we also validate when writing configuration files?

**Question:**
When should configuration validation occur?

**Options:**

**Option A: Startup Only**
```
Pros:
✅ Simple implementation
✅ Clear error messages
✅ No additional tooling needed

Cons:
❌ Only find errors when service starts
❌ Long feedback loop
❌ May deploy broken configuration
```

**Option B: Startup + Pre-Commit Hook**
```
# .git/hooks/pre-commit
#!/bin/bash
./scripts/validate-config.sh

# scripts/validate-config.sh
#!/bin/bash
go run cmd/validate-config/main.go --config-dir=./config

Pros:
✅ Catch errors before commit
✅ Fast feedback loop
✅ Prevent broken config in git

Cons:
❌ Requires developer setup
❌ Can be bypassed (--no-verify)
❌ Extra tooling needed
```

**Option C: Startup + CI Pipeline**
```
# .github/workflows/ci.yml
- name: Validate Configuration
  run: go run cmd/validate-config/main.go --config-dir=./config

Pros:
✅ Catch errors before merge
✅ Enforced in CI (can't bypass)
✅ Works for all contributors

Cons:
❌ Slower feedback than pre-commit
❌ Requires CI setup
```

**Option D: All Three (Startup + Pre-Commit + CI)**
```
Pros:
✅ Multiple safety layers
✅ Fast feedback (pre-commit)
✅ Enforced in CI
✅ Runtime safety (startup)

Cons:
❌ Most complex setup
❌ Redundant validation
```

**Implementation Example:**

```go
// cmd/validate-config/main.go

func main() {
    configDir := flag.String("config-dir", "./config", "Configuration directory")
    flag.Parse()
    
    // Load configuration (without starting service)
    cfg, err := config.Load(context.Background(), config.LoadOptions{
        ConfigDir: *configDir,
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "❌ Configuration loading failed:\n%v\n", err)
        os.Exit(1)
    }
    
    // Validate configuration
    validator := config.NewValidator(&config.Registries{
        Agents:       cfg.AgentRegistry,
        Chains:       cfg.ChainRegistry,
        MCPServers:   cfg.MCPServerRegistry,
        LLMProviders: cfg.LLMProviderRegistry,
    })
    
    if err := validator.ValidateAll(); err != nil {
        fmt.Fprintf(os.Stderr, "❌ Configuration validation failed:\n%v\n", err)
        os.Exit(1)
    }
    
    fmt.Println("✅ Configuration valid!")
    
    // Print summary
    fmt.Printf("\nConfiguration Summary:\n")
    fmt.Printf("  Agents: %d\n", len(cfg.AgentRegistry.GetAll()))
    fmt.Printf("  Chains: %d\n", len(cfg.ChainRegistry.GetAll()))
    fmt.Printf("  MCP Servers: %d\n", len(cfg.MCPServerRegistry.GetAll()))
    fmt.Printf("  LLM Providers: %d\n", len(cfg.LLMProviderRegistry.GetAll()))
}
```

**Question for Discussion:**
1. How important is fast feedback for configuration errors?
2. Can we rely on developers to run validation manually?
3. Is CI validation sufficient?

**Recommendation**: Use **Option D (All Three)** for maximum safety with minimal overhead.

---

### Q5: Configuration Schema Documentation

**Status**: 🔄 **IN DISCUSSION**

**Context:**
YAML configuration needs schema documentation for editors and validation.

**Question:**
Should we generate JSON Schema for configuration files?

**Options:**

**Option A: No Schema (Comments Only)**
```yaml
# config/agents.yaml

agents:
  - id: kubernetes-agent  # Required: unique agent identifier
    name: "Kubernetes Agent"  # Required: human-readable name
    # ... more fields with comments
```

**Option B: JSON Schema**
```json
// config/schema/agents.schema.json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "agents": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string",
            "pattern": "^[a-z][a-z0-9-]*$"
          },
          "name": {
            "type": "string"
          }
        },
        "required": ["id", "name"]
      }
    }
  }
}
```

**Option C: Go Struct Tags + Generated Schema**
```go
// pkg/config/agent.go

type AgentConfig struct {
    ID   string `yaml:"id" json:"id" validate:"required" jsonschema:"pattern=^[a-z][a-z0-9-]*$,description=Unique agent identifier"`
    Name string `yaml:"name" json:"name" validate:"required" jsonschema:"description=Human-readable agent name"`
}

// Generate JSON Schema from Go structs
//go:generate go run tools/generate-schema/main.go
```

**Benefits of JSON Schema:**
- ✅ IDE autocomplete and validation (VS Code, IntelliJ)
- ✅ Automatic documentation generation
- ✅ Language-agnostic validation
- ✅ Clear field descriptions and constraints
- ✅ Reusable in other tools (Terraform, Ansible)

**Drawbacks:**
- ❌ Extra maintenance (keep schema in sync with code)
- ❌ Schema generation tooling needed
- ❌ Overhead for simple configurations

**Question for Discussion:**
1. How often do we expect configuration to be edited?
2. Will non-developers edit configuration files?
3. Is IDE support important?

**Recommendation**: Use **Option C (Generate from Go Structs)** for consistency and IDE support.

---

### Q6: Configuration Validation - Fail Fast vs Collect All Errors

**Status**: 🔄 **IN DISCUSSION**

**Context:**
When validating configuration, should we stop at first error or collect all errors?

**Question:**
Should validation stop at first error or collect all validation errors?

**Options:**

**Option A: Fail Fast (Stop at First Error)**
```go
func (v *ConfigValidator) ValidateAll() error {
    if err := v.validateAgents(); err != nil {
        return err  // Stop here
    }
    
    if err := v.validateChains(); err != nil {
        return err  // Stop here
    }
    
    return nil
}

// Output:
// ❌ Agent 'kubernetes-agent': LLM provider 'invalid' not found
```

**Option B: Collect All Errors**
```go
func (v *ConfigValidator) ValidateAll() error {
    var errs []error
    
    if err := v.validateAgents(); err != nil {
        errs = append(errs, err)  // Continue
    }
    
    if err := v.validateChains(); err != nil {
        errs = append(errs, err)  // Continue
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("validation failed:\n%v", errs)
    }
    
    return nil
}

// Output:
// ❌ Configuration validation failed:
//   - Agent 'kubernetes-agent': LLM provider 'invalid' not found
//   - Chain 'k8s-analysis': agent 'nonexistent-agent' not found
//   - MCP server 'prometheus': invalid transport type
```

**Comparison:**

**Fail Fast:**
```
Pros:
✅ Simpler implementation
✅ Clear first error to fix
✅ Faster validation (stops early)

Cons:
❌ Slow feedback loop (fix one error, find next)
❌ Must run validation multiple times
❌ Hard to see full extent of issues
```

**Collect All:**
```
Pros:
✅ See all errors at once
✅ Fix multiple issues in one iteration
✅ Better for batch changes
✅ Clearer picture of configuration health

Cons:
❌ More complex implementation
❌ May be overwhelming if many errors
❌ Harder to prioritize which error to fix first
```

**Implementation Example:**

```go
// pkg/config/validator.go

type ValidationError struct {
    Component string  // "agent", "chain", "mcp_server", "llm_provider"
    ID        string  // Component ID
    Field     string  // Field that failed validation
    Message   string  // Error message
}

func (v ValidationError) Error() string {
    return fmt.Sprintf("%s '%s': %s: %s", v.Component, v.ID, v.Field, v.Message)
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
    if len(e) == 0 {
        return "no errors"
    }
    
    var sb strings.Builder
    sb.WriteString("Configuration validation failed:\n")
    for _, err := range e {
        sb.WriteString(fmt.Sprintf("  - %s\n", err.Error()))
    }
    return sb.String()
}

func (v *ConfigValidator) ValidateAll() error {
    var errs ValidationErrors
    
    // Validate each component and collect errors
    errs = append(errs, v.validateAgents()...)
    errs = append(errs, v.validateChains()...)
    errs = append(errs, v.validateMCPServers()...)
    errs = append(errs, v.validateLLMProviders()...)
    
    if len(errs) > 0 {
        return errs
    }
    
    return nil
}
```

**Question for Discussion:**
1. How important is seeing all errors at once?
2. Do we expect many validation errors typically?
3. Is simplicity more important than comprehensive error reporting?

**Recommendation**: Use **Option B (Collect All Errors)** for better developer experience.

---

## 📊 Medium Priority (Features & Usability)

### Q7: Configuration Precedence and Merging

**Status**: 🔄 **IN DISCUSSION**

**Context:**
With multiple configuration sources (defaults, files, overrides, env vars), need clear precedence rules.

**Question:**
How should configuration precedence and merging work?

**Proposed Precedence (Lowest to Highest):**
1. System defaults (`defaults.yaml`)
2. Component configuration (`agents.yaml`, `chains.yaml`, etc.)
3. Environment overrides (`environments/{env}.yaml`)
4. Environment variables (`${VAR}`)
5. Per-alert overrides (API request - runtime only)

**Merging Strategies:**

**Option A: Deep Merge**
```yaml
# defaults.yaml
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: 0.7
      top_p: 0.95
      top_k: 40

# environments/production.yaml
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: 0.5  # Override only temperature

# Result: Deep merge
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: 0.5  # From production.yaml
      top_p: 0.95       # From defaults.yaml
      top_k: 40         # From defaults.yaml
```

**Option B: Replace Merge**
```yaml
# Same inputs as above

# Result: Replace entire parameters object
llm_providers:
  - id: gemini-thinking
    parameters:
      temperature: 0.5  # From production.yaml
      # top_p and top_k lost!
```

**Deep Merge Considerations:**

```go
// pkg/config/merge.go

func DeepMerge(base, override interface{}) interface{} {
    // Handle different types
    switch baseVal := base.(type) {
    case map[string]interface{}:
        overrideMap := override.(map[string]interface{})
        result := make(map[string]interface{})
        
        // Copy base
        for k, v := range baseVal {
            result[k] = v
        }
        
        // Merge override (recursively)
        for k, v := range overrideMap {
            if baseV, exists := result[k]; exists {
                // Recursively merge nested maps
                result[k] = DeepMerge(baseV, v)
            } else {
                result[k] = v
            }
        }
        
        return result
        
    case []interface{}:
        // Arrays: replace entirely (don't merge arrays)
        return override
        
    default:
        // Primitives: replace
        return override
    }
}
```

**Edge Cases:**

1. **Array Merging:**
   ```yaml
   # Base
   mcp_servers: [server1, server2]
   
   # Override
   mcp_servers: [server3]
   
   # Deep merge: Replace? Append? Merge by ID?
   # Recommendation: Replace arrays entirely
   mcp_servers: [server3]
   ```

2. **Null Values:**
   ```yaml
   # Base
   max_iterations: 20
   
   # Override (intentionally disable)
   max_iterations: null
   
   # Should null remove the field or set it to null?
   # Recommendation: null removes the field
   ```

3. **Type Conflicts:**
   ```yaml
   # Base
   timeout: 30s
   
   # Override (wrong type)
   timeout: "invalid"
   
   # Should fail validation or ignore override?
   # Recommendation: Fail validation
   ```

**Question for Discussion:**
1. Should we support deep merge or just replace?
2. How should arrays be merged?
3. How should null values be handled?

**Recommendation**: Use **Deep Merge for Maps, Replace for Arrays**, with clear documentation.

---

### Q8: Configuration Versioning

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Configuration may evolve over time. Should we support version field and migration?

**Question:**
Should configuration files have a version field and migration support?

**Options:**

**Option A: No Versioning**
```yaml
# config/agents.yaml
agents:
  - id: kubernetes-agent
    name: "Kubernetes Agent"
    # No version field
```

**Option B: File-Level Versioning**
```yaml
# config/agents.yaml
version: "1.0"

agents:
  - id: kubernetes-agent
    name: "Kubernetes Agent"
```

**Option C: Per-Component Versioning**
```yaml
# config/agents.yaml
agents:
  - id: kubernetes-agent
    name: "Kubernetes Agent"
    version: "1.0"  # Component version
```

**Use Cases for Versioning:**

1. **Breaking Changes:**
   ```
   v1.0: iteration_strategy: "react"
   v2.0: iteration_controller: "react"  # Field renamed
   
   Migration: Rename field, preserve values
   ```

2. **Deprecation Warnings:**
   ```go
   if agent.Version == "1.0" {
       log.Warn("Agent config v1.0 is deprecated, please upgrade to v2.0")
   }
   ```

3. **Compatibility Checks:**
   ```go
   func (l *ConfigLoader) validateVersion(version string) error {
       if version < "1.0" || version > "2.0" {
           return fmt.Errorf("unsupported config version: %s", version)
       }
       return nil
   }
   ```

**Migration Support:**

```go
// pkg/config/migration.go

type ConfigMigrator struct {
    migrations map[string]MigrationFunc
}

type MigrationFunc func(data map[string]interface{}) (map[string]interface{}, error)

func (m *ConfigMigrator) Migrate(data map[string]interface{}, fromVersion, toVersion string) (map[string]interface{}, error) {
    currentVersion := fromVersion
    
    for currentVersion != toVersion {
        migration, exists := m.migrations[currentVersion]
        if !exists {
            return nil, fmt.Errorf("no migration from %s", currentVersion)
        }
        
        var err error
        data, err = migration(data)
        if err != nil {
            return nil, fmt.Errorf("migration from %s failed: %w", currentVersion, err)
        }
        
        // Update version
        currentVersion = nextVersion(currentVersion)
    }
    
    return data, nil
}

// Example migration
func migrateAgentsV1ToV2(data map[string]interface{}) (map[string]interface{}, error) {
    // Rename iteration_strategy -> iteration_controller
    for _, agent := range data["agents"].([]interface{}) {
        agentMap := agent.(map[string]interface{})
        if strategy, exists := agentMap["iteration_strategy"]; exists {
            agentMap["iteration_controller"] = strategy
            delete(agentMap, "iteration_strategy")
        }
    }
    
    data["version"] = "2.0"
    return data, nil
}
```

**Pros/Cons:**

**No Versioning:**
```
Pros:
✅ Simpler configuration
✅ No migration logic needed
✅ Less overhead

Cons:
❌ Hard to handle breaking changes
❌ No deprecation warnings
❌ Manual updates required
```

**File-Level Versioning:**
```
Pros:
✅ Single version per file
✅ Clear compatibility checks
✅ Easier to migrate entire file

Cons:
❌ All components must use same version
❌ Can't deprecate individual components
```

**Per-Component Versioning:**
```
Pros:
✅ Granular versioning
✅ Independent component evolution
✅ Gradual migration possible

Cons:
❌ More complex
❌ Harder to track compatibility
❌ More migration logic needed
```

**Question for Discussion:**
1. Do we expect breaking changes to configuration?
2. How important is backward compatibility?
3. Can we handle migrations manually (documentation)?

**Recommendation**: Start with **Option B (File-Level Versioning)** for basic compatibility checks. Add migration support if needed later.

---

### Q9: Configuration Testing Utilities

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Developers and operators need to test configuration changes before deploying.

**Question:**
What testing utilities should we provide for configuration?

**Proposed Utilities:**

**1. Configuration Validator CLI:**
```bash
# Validate configuration
./tarsy validate-config --config-dir=./config

# Output:
# ✅ Configuration valid!
# 
# Configuration Summary:
#   Agents: 5
#   Chains: 3
#   MCP Servers: 4
#   LLM Providers: 2
```

**2. Configuration Diff Tool:**
```bash
# Compare two configurations
./tarsy config-diff \
    --config-dir=./config \
    --compare-dir=./config-new

# Output:
# Agents:
#   + Added: new-agent
#   - Removed: old-agent
#   ~ Modified: kubernetes-agent
#     - max_iterations: 20 → 30
# 
# Chains:
#   ~ Modified: k8s-deep-analysis
#     + Added stage: "Final Recommendations"
```

**3. Configuration Dry-Run:**
```bash
# Test configuration without starting service
./tarsy dry-run \
    --config-dir=./config \
    --chain=k8s-deep-analysis

# Output:
# Chain: k8s-deep-analysis
#   Stage 0: Initial Analysis
#     Agent: kubernetes-agent
#     LLM: gemini-thinking
#     MCP Servers: kubernetes-server, prometheus-server
#   
#   Stage 1: Deep Dive (parallel)
#     Agent 1: kubernetes-agent
#     Agent 2: argocd-agent
#     Agent 3: prometheus-agent
#   
#   Stage 2: Synthesis
#     Agent: synthesis-agent
```

**4. Configuration Export:**
```bash
# Export resolved configuration (with env vars interpolated)
./tarsy export-config \
    --config-dir=./config \
    --environment=production \
    --output=config-resolved.yaml

# Useful for:
# - Debugging environment variable issues
# - Documentation
# - Auditing deployed configuration
```

**5. Configuration Playground (Web UI - Optional):**
```
Interactive web UI for:
- Live configuration editing
- Instant validation feedback
- Visual chain designer
- Configuration templates
```

**Implementation Priority:**

**High Priority:**
- ✅ Configuration Validator CLI (essential)
- ✅ Configuration Dry-Run (helpful for testing)

**Medium Priority:**
- 🔄 Configuration Diff Tool (useful for reviews)
- 🔄 Configuration Export (debugging)

**Low Priority:**
- ⏸️ Configuration Playground (nice-to-have)

**Question for Discussion:**
1. Which utilities are most important?
2. Should we build CLI tools or web UI?
3. How much investment in tooling is worthwhile?

**Recommendation**: Implement **Validator CLI** and **Dry-Run** first. Add others based on user feedback.

---

## 💡 Low Priority (Nice-to-Have)

### Q10: Configuration Templates and Examples

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Need examples to help users understand configuration patterns.

**Question:**
What configuration templates and examples should we provide?

**Proposed Templates:**

**1. Quick Start Template (Minimal):**
```yaml
# config/templates/quickstart/agents.yaml
agents:
  - id: simple-agent
    name: "Simple Investigation Agent"
    iteration_strategy: react
    max_iterations: 10
    llm_provider: gemini-thinking
    system_prompt: "You are a helpful investigation assistant."
    enabled: true

# config/templates/quickstart/chains.yaml
chains:
  - id: simple-investigation
    name: "Simple Investigation"
    stages:
      - name: "Investigate"
        index: 0
        agent: simple-agent
        execution_mode: single
    enabled: true
```

**2. Production Template (Comprehensive):**
```yaml
# Full production-ready configuration with:
- Multiple agents (Kubernetes, ArgoCD, Prometheus)
- Complex multi-stage chains
- Parallel execution examples
- Chat configuration
- MCP server integrations
- Production LLM settings
```

**3. Development Template:**
```yaml
# Development-friendly configuration with:
- Higher iteration limits for testing
- More verbose logging
- Relaxed rate limits
- Development LLM settings
```

**4. Example Patterns:**
```
config/examples/
├── single-agent-chain.yaml          # Simple chain
├── parallel-agents-chain.yaml       # Multi-agent parallel
├── replica-comparison-chain.yaml    # Replica pattern
├── multi-stage-chain.yaml           # Complex multi-stage
└── custom-mcp-server.yaml           # Custom MCP integration
```

**Documentation Structure:**
```
docs/configuration/
├── getting-started.md
├── agents.md
├── chains.md
├── mcp-servers.md
├── llm-providers.md
├── environment-variables.md
├── examples/
│   ├── simple-chain.md
│   ├── parallel-agents.md
│   └── custom-mcp.md
└── reference/
    ├── schema.md
    └── validation-rules.md
```

**Question for Discussion:**
1. How much documentation is needed upfront?
2. Should templates be in main repo or separate docs repo?
3. Are examples important for initial release?

**Recommendation**: Provide **Quick Start** and **Production** templates, plus basic documentation. Expand examples based on user needs.

---

### Q11: Configuration Management API

**Status**: 🔄 **IN DISCUSSION**

**Context:**
Should we expose configuration management via API (read-only or read-write)?

**Question:**
Should we provide an API for configuration management?

**Options:**

**Option A: No API (File-Based Only)**
```
Pros:
✅ Simple implementation
✅ GitOps workflow (config in git)
✅ Clear audit trail (git history)
✅ Code review for changes

Cons:
❌ Requires file system access
❌ Manual deployment process
❌ No programmatic configuration
```

**Option B: Read-Only API**
```
GET /api/config/agents
GET /api/config/chains
GET /api/config/mcp-servers
GET /api/config/llm-providers

Pros:
✅ Visibility into loaded configuration
✅ Debugging and troubleshooting
✅ No risk of runtime modification

Cons:
❌ No programmatic updates
❌ Still requires file access for changes
```

**Option C: Read-Write API**
```
GET    /api/config/agents
POST   /api/config/agents
PUT    /api/config/agents/{id}
DELETE /api/config/agents/{id}

Pros:
✅ Full programmatic control
✅ Web UI for configuration
✅ API-driven workflows

Cons:
❌ Complex implementation
❌ Configuration drift (DB vs files)
❌ Harder to audit
❌ Security concerns (who can modify?)
❌ Conflicts with GitOps workflow
```

**Option D: Read-Only API + Config Management Service (Separate)**
```
TARSy Service: Read-only config API
Config Manager Service: Manages files, git commits, restarts

Pros:
✅ Separation of concerns
✅ Maintains GitOps workflow
✅ Audit trail via git
✅ Programmatic updates possible

Cons:
❌ More complex architecture
❌ Two services to maintain
❌ May be overkill for simple deployments
```

**Read-Only API Example:**

```go
// pkg/api/config_handler.go

func (h *ConfigHandler) GetAgents(w http.ResponseWriter, r *http.Request) {
    agents := h.agentRegistry.GetAll()
    
    response := struct {
        Agents []AgentConfig `json:"agents"`
        Count  int           `json:"count"`
    }{
        Agents: agents,
        Count:  len(agents),
    }
    
    json.NewEncoder(w).Encode(response)
}

func (h *ConfigHandler) GetChain(w http.ResponseWriter, r *http.Request) {
    chainID := chi.URLParam(r, "id")
    
    chain, err := h.chainRegistry.Get(chainID)
    if err != nil {
        http.Error(w, "Chain not found", http.StatusNotFound)
        return
    }
    
    json.NewEncoder(w).Encode(chain)
}
```

**Question for Discussion:**
1. Do we need programmatic configuration access?
2. Is GitOps workflow sufficient?
3. Who would use a configuration API?

**Recommendation**: Start with **Option B (Read-Only API)** for visibility. Add write capabilities later if needed.

---

### Q12: Configuration Change Notifications

**Status**: ⏸️ **DEFERRED**

**Context:**
If hot-reload is supported, how should services be notified of configuration changes?

**Question:**
How should configuration changes be communicated to running services?

**Options:**

**Option A: No Notifications (Restart Required)**
- Services restart to pick up new configuration
- No runtime notification needed

**Option B: Internal Event System**
```go
type ConfigChangeEvent struct {
    Component string  // "agent", "chain", etc.
    ChangeType string // "added", "modified", "deleted"
    ID string         // Component ID
}

// Subscribe to config changes
configEvents := config.Subscribe()
for event := range configEvents {
    log.Info("Config changed", "component", event.Component, "id", event.ID)
    // Handle change
}
```

**Option C: WebHooks**
```yaml
# config/defaults.yaml
notifications:
  webhooks:
    - url: https://monitoring.example.com/config-changed
      events: ["agent.modified", "chain.added"]
```

**Question for Discussion:**
1. Is this needed if no hot-reload?
2. What would consume these notifications?
3. Is internal event system sufficient?

**Recommendation**: **Defer** until hot-reload decision is made (Q1).

---

## 📝 Summary Checklist

Track which questions we've addressed:

### Critical Priority
- [ ] Q1: Configuration Reload Strategy (restart vs hot-reload vs hybrid) 🔄
- [ ] Q2: Configuration File Structure (multiple files vs single file) 🔄
- [ ] Q3: Environment-Specific Configuration (override files vs env vars vs separate dirs) 🔄

### High Priority
- [ ] Q4: Configuration Validation Timing (startup only vs pre-commit vs CI vs all) 🔄
- [ ] Q5: Configuration Schema Documentation (comments vs JSON Schema vs generated) 🔄
- [ ] Q6: Configuration Validation - Fail Fast vs Collect All Errors 🔄

### Medium Priority
- [ ] Q7: Configuration Precedence and Merging (deep merge vs replace, array handling) 🔄
- [ ] Q8: Configuration Versioning (no version vs file-level vs per-component) 🔄
- [ ] Q9: Configuration Testing Utilities (validator, diff, dry-run, export) 🔄

### Low Priority
- [ ] Q10: Configuration Templates and Examples 🔄
- [ ] Q11: Configuration Management API (no API vs read-only vs read-write) 🔄
- [ ] Q12: Configuration Change Notifications ⏸️ **DEFERRED**

---

## Next Steps

1. Review and discuss each question in order
2. Make decisions and mark status (✅/❌/⏸️)
3. Update main design document based on decisions
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
