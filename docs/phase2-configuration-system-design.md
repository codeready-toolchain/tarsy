# Phase 2: Configuration System - Detailed Design

**Status**: ✅ Design Complete - All questions decided  
**Questions Document**: See `phase2-configuration-system-questions.md` for decision rationale  
**Last Updated**: 2026-02-05

## Overview

This document details the configuration system design for the new TARSy implementation. The configuration system manages agent definitions, chain configurations, MCP server registry, and LLM provider settings through YAML files with hierarchical resolution.

**Phase 2.2 Scope**: This phase focuses on **configuration management** (loading, parsing, validating, storing config). Agent **instantiation and execution** is handled in **Phase 3: Agent Framework**.

**Key Design Principles:**
- File-based configuration (version controlled with code)
- YAML for human readability and maintainability
- In-memory registry loaded at startup
- Hierarchical configuration resolution (defaults → files → overrides)
- Strong validation with clear error messages
- Per-alert configuration overrides via API
- Environment variable interpolation for secrets

**Major Design Goals:**
- Clear separation of agent definitions, chains, MCP servers, and LLM providers
- Easy to add new agents, chains, and integrations
- Type-safe configuration loading in Go
- Comprehensive validation on startup
- Support for multiple deployment environments (dev, staging, prod)

**Phase Boundary**:
- **Phase 2.2 (this phase)**: Configuration schema, loading, validation, registries
- **Phase 3**: Agent factory, instantiation, execution logic

---

## Architecture Overview

### Configuration File Structure

```
deploy/
└── config/
    ├── tarsy.yaml.example            # Example main config (tracked in git)
    ├── llm-providers.yaml.example    # Example LLM providers (tracked in git)
    ├── .env.example                  # Example environment variables (tracked in git)
    ├── oauth2-proxy.cfg.template     # OAuth2 proxy template (tracked in git)
    ├── tarsy.yaml                    # User's actual config (gitignored)
    ├── llm-providers.yaml            # User's actual config (gitignored)
    ├── .env                          # User's actual env vars (gitignored)
    └── oauth2-proxy.cfg              # Generated OAuth2 config (gitignored)

# Setup: Users copy and customize
cp deploy/config/tarsy.yaml.example deploy/config/tarsy.yaml
cp deploy/config/llm-providers.yaml.example deploy/config/llm-providers.yaml
cp deploy/config/.env.example deploy/config/.env
```

**File Descriptions:**

- **`deploy/config/tarsy.yaml.example`**: Example main configuration (tracked in git, ~800-1000 lines)
  - **Built-in + User-defined** configuration (user configs override built-ins)
  - `mcp_servers:` - MCP server configurations (transport, instructions, data_masking, summarization)
  - `agents:` - Custom agent definitions with `custom_instructions`
  - `agent_chains:` - Multi-stage chains with `alert_types`
  - `defaults:` - System-wide defaults (NEW: llm_provider, max_iterations, alert_type, runbook, etc.)
  - Environment-agnostic (uses `{{.VAR}}` placeholders for environment-specific values)
  - Users copy to `tarsy.yaml` and customize

- **`deploy/config/llm-providers.yaml.example`**: Example LLM provider configurations (tracked in git, ~50 lines)
  - API endpoints and authentication (via env vars: `{{.GEMINI_API_KEY}}`)
  - Model parameters (can be overridden via env vars: `{{.LLM_TEMPERATURE}}`)
  - Rate limits and retry policies
  - Native tools configuration (Google-specific)
  - Environment-agnostic
  - Users copy to `llm-providers.yaml` and customize

- **`deploy/config/.env.example`**: Example environment variables (tracked in git)
  - Template for users to create their `.env`
  - Contains all required and optional variables with comments
  - Includes examples for different environments (local dev, Podman, K8s)
  - No real secrets (placeholder values only)
  - Users copy to `.env` and customize

- **`deploy/config/oauth2-proxy.cfg.template`**: OAuth2 proxy configuration template (tracked in git)
  - Uses placeholder syntax: `{{OAUTH2_CLIENT_ID}}`
  - Makefile replaces placeholders with environment variables
  - Generates `oauth2-proxy.cfg` (gitignored)
  - Same pattern as old TARSy

- **`deploy/config/tarsy.yaml`**: User's actual configuration (gitignored)
  - Copied from `tarsy.yaml.example` and customized

- **`deploy/config/llm-providers.yaml`**: User's LLM provider configuration (gitignored)
  - Copied from `llm-providers.yaml.example` and customized

- **`deploy/config/.env`**: User's secrets and environment-specific values (gitignored)
  - API keys (GEMINI_API_KEY, etc.)
  - Database credentials (DB_HOST, DB_PORT, DB_PASSWORD)
  - Service endpoints (GRPC_ADDR, HTTP_PORT)
  - OAuth2 credentials (OAUTH2_CLIENT_ID, OAUTH2_CLIENT_SECRET)
  - Environment-specific overrides (LLM_RATE_LIMIT, MAX_ITERATIONS)
  - Copied from `.env.example` and customized

**Environment Strategy:**
- **All environments use the same YAML files** (tarsy.yaml, llm-providers.yaml)
- **Environment differences handled via .env files** (database hosts, service endpoints, secrets)
- **No environment override YAML files** (simpler, follows 12-factor app principles)
- **Production config NOT in source code** (users create their own K8s ConfigMaps/Secrets)
- **Podman-compose integration**: `env_file: ./config/.env` in `podman-compose.yml` for explicit path

### Configuration Loading Flow

```
┌─────────────────────────────────────────────────────────┐
│                    Startup Sequence                      │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  1. Load & Parse YAML Files                             │
│     - tarsy.yaml (agents, chains, MCP servers, defaults)│
│     - llm-providers.yaml                                 │
│     - environment override (if specified)                │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  2. Interpolate Environment Variables                    │
│     Uses text/template (stdlib)                          │
│     Supports {{.VAR}} syntax (no $ collision)            │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  3. Validate Configuration                               │
│     - Required fields present                            │
│     - References valid (chain → agent, agent → LLM)      │
│     - MCP server configurations correct                  │
│     - No duplicate IDs                                   │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  4. Build In-Memory Registries (Phase 2.2)              │
│     - AgentRegistry (stores config metadata only)        │
│     - ChainRegistry                                      │
│     - MCPServerRegistry                                  │
│     - LLMProviderRegistry                                │
│                                                          │
│  Note: Registries store configuration data, not          │
│  implementations. Agent instantiation is Phase 3.        │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  5. Service Ready                                        │
│     Configuration accessible via registries              │
└─────────────────────────────────────────────────────────┘
```

### Runtime Configuration Access

```
┌─────────────────────────────────────────────────────────┐
│                  API Request: Create Session             │
│  POST /api/sessions                                      │
│  {                                                       │
│    "chain_id": "k8s-deep-analysis",                     │
│    "alert_data": "...",                                  │
│    "mcp": { /* optional override */ }                   │
│  }                                                       │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  Session Service                                         │
│  1. Look up chain: chainRegistry.Get("k8s-deep-analysis")│
│  2. Validate chain exists                                │
│  3. Apply MCP override if provided                       │
│  4. Create session with chain_id                         │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  Chain Orchestrator (Phase 3)                            │
│  1. Get chain config from registry                       │
│  2. For each stage:                                      │
│     - Look up agent config from AgentRegistry            │
│     - Look up LLM provider from LLMProviderRegistry      │
│     - Look up MCP servers from MCPServerRegistry         │
│  3. Execute stage with resolved configuration            │
│     (Agent instantiation and execution is Phase 3)       │
└─────────────────────────────────────────────────────────┘
```

---

## Built-in vs User-defined Configuration

TARSy supports both **built-in** and **user-defined** configurations with override capability:

### Built-in Configuration (Go Code)

Built-in configurations are defined in Go code (`pkg/config/builtin.go`) using an encapsulated singleton pattern:

```go
// pkg/config/builtin.go

// BuiltinConfig holds all built-in configuration data (agents, MCP servers, LLM providers, etc.)
// This is a singleton initialized once and accessed via GetBuiltinConfig()
type BuiltinConfig struct {
    Agents           map[string]BuiltinAgentConfig
    MCPServers       map[string]MCPServerConfig
    LLMProviders     map[string]LLMProviderConfig
    ChainDefinitions map[string]ChainConfig
    MaskingPatterns  map[string]MaskingPattern
    PatternGroups    map[string][]string
    CodeMaskers      map[string]string
    DefaultRunbook   string
    DefaultAlertType string
}

// Built-in agent metadata (configuration only - instantiation is Phase 3)
// Note: Agent instantiation/factory pattern is handled in Phase 3: Agent Framework
type BuiltinAgentConfig struct {
    Description       string
    IterationStrategy IterationStrategy
    // Agent implementation/instantiation is Phase 3 - not part of config
}

var (
    builtinConfig     *BuiltinConfig
    builtinConfigOnce sync.Once
)

// GetBuiltinConfig returns the singleton built-in configuration (thread-safe, lazy-initialized)
func GetBuiltinConfig() *BuiltinConfig {
    builtinConfigOnce.Do(initBuiltinConfig)
    return builtinConfig
}

func initBuiltinConfig() {
    builtinConfig = &BuiltinConfig{
        Agents: map[string]BuiltinAgentConfig{
            "KubernetesAgent": {
                Description:       "Kubernetes-specialized agent using ReAct pattern",
                IterationStrategy: IterationStrategyReact,
            },
            "ChatAgent": {
                Description:       "Built-in agent for follow-up conversations",
                IterationStrategy: IterationStrategyReact,
            },
            "SynthesisAgent": {
                Description:       "Synthesizes parallel investigation results",
                IterationStrategy: IterationStrategySynthesis,
            },
        },
        MCPServers: map[string]MCPServerConfig{
            "kubernetes-server": {
                Transport: TransportConfig{
                    Type:    TransportTypeStdio,
                    Command: "npx",
                    Args:    []string{"-y", "kubernetes-mcp-server@0.0.54", "--read-only", "--disable-destructive", "--kubeconfig", "{{.KUBECONFIG}}"},
                },
                Instructions: "For Kubernetes operations: ...",
                DataMasking: &MaskingConfig{
                    Enabled:       true,
                    PatternGroups: []string{"kubernetes"},
                    Patterns:      []string{"certificate", "token", "email"},
                },
            },
        },
        LLMProviders: map[string]LLMProviderConfig{
            "google-default": {
                Type:                LLMProviderTypeGoogle,
                Model:               "gemini-2.5-pro",
                APIKeyEnv:           "GOOGLE_API_KEY",
                MaxToolResultTokens: 950000,
                NativeTools: map[GoogleNativeTool]bool{
                    GoogleNativeToolGoogleSearch:  true,
                    GoogleNativeToolCodeExecution: false,
                    GoogleNativeToolURLContext:    true,
                },
            },
        },
        ChainDefinitions: map[string]ChainConfig{
            "kubernetes-agent-chain": {
                AlertTypes:  []string{"kubernetes"},
                Stages: []StageConfig{
                    {
                        Name: "analysis",
                        Agents: []StageAgentConfig{
                            {Name: "KubernetesAgent"},
                        },
                    },
                },
                Description: "Single-stage Kubernetes analysis",
            },
        },
        MaskingPatterns: map[string]MaskingPattern{
            // Regex-based data masking patterns
            // See old TARSy: backend/tarsy/config/builtin_config.py BUILTIN_MASKING_PATTERNS
            "api_key":                    {Pattern: `(?i)(?:api[_-]?key|apikey|key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-]{20,})["\']?`, Replacement: `"api_key": "__MASKED_API_KEY__"`, Description: "API keys"},
            "password":                   {Pattern: `(?i)(?:password|pwd|pass)["\']?\s*[:=]\s*["\']?([^"\'\s\n]{6,})["\']?`, Replacement: `"password": "__MASKED_PASSWORD__"`, Description: "Passwords"},
            "certificate":                {Pattern: `-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`, Replacement: `__MASKED_CERTIFICATE__`, Description: "SSL/TLS certificates"},
            "certificate_authority_data": {Pattern: `(?i)certificate-authority-data:\s*([A-Za-z0-9+/]{20,}={0,2})`, Replacement: `certificate-authority-data: __MASKED_CA_CERTIFICATE__`, Description: "K8s CA data"},
            "token":                      {Pattern: `(?i)(?:token|bearer|jwt)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`, Replacement: `"token": "__MASKED_TOKEN__"`, Description: "Access tokens"},
            "email":                      {Pattern: `(?<!\\)\b[A-Za-z0-9._%+-]+@[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*\.[A-Za-z]{2,63}\b(?!\()`, Replacement: `__MASKED_EMAIL__`, Description: "Email addresses"},
            "ssh_key":                    {Pattern: `ssh-(?:rsa|dss|ed25519|ecdsa)\s+[A-Za-z0-9+/=]+`, Replacement: `__MASKED_SSH_KEY__`, Description: "SSH public keys"},
            "base64_secret":              {Pattern: `\b([A-Za-z0-9+/]{20,}={0,2})\b`, Replacement: `__MASKED_BASE64_VALUE__`, Description: "Base64 values (20+ chars)"},
            "base64_short":               {Pattern: `(?<=:\s)([A-Za-z0-9+/]{4,19}={0,2})(?=\s|$)`, Replacement: `__MASKED_SHORT_BASE64__`, Description: "Short base64 values"},
            // Full list: See old TARSy builtin_config.py
        },
        PatternGroups: map[string][]string{
            "basic":      {"api_key", "password"},
            "secrets":    {"api_key", "password", "token"},
            "security":   {"api_key", "password", "token", "certificate", "certificate_authority_data", "email", "ssh_key"},
            "kubernetes": {"kubernetes_secret", "api_key", "password", "certificate_authority_data"},
            "all":        {"base64_secret", "base64_short", "api_key", "password", "certificate", "certificate_authority_data", "email", "token", "ssh_key"},
        },
        CodeMaskers: map[string]string{
            // Code-based maskers for complex masking requiring structural parsing
            // Example: kubernetes_secret masker parses YAML/JSON to mask only Secret data (not ConfigMaps)
            // See old TARSy: backend/tarsy/services/maskers/kubernetes_secret_masker.py
            "kubernetes_secret": "pkg/maskers.KubernetesSecretMasker", // Phase 3+ implementation
        },
        DefaultRunbook:   defaultRunbookContent,
        DefaultAlertType: "kubernetes",
    }
}

const defaultRunbookContent = `# Generic Troubleshooting Guide

## Investigation Steps
1. **Analyze the alert** - Review alert data and identify affected system/service
2. **Gather context** - Use tools to check current state and recent changes
3. **Identify root cause** - Investigate potential causes based on alert type
4. **Assess impact** - Determine scope and severity
5. **Recommend actions** - Suggest safe investigation or remediation steps

## Guidelines
- Verify information before suggesting changes
- Consider dependencies and potential side effects
- Document findings and actions taken
`

// Direct access to built-in config (rarely needed):
// builtin := config.GetBuiltinConfig()
// agent := builtin.Agents["KubernetesAgent"]
// mcpServer := builtin.MCPServers["kubernetes-server"]

// Typical usage - access via Config (includes YAML overrides):
// cfg, _ := config.Initialize(ctx, configDir)
// 
// // Access defaults (YAML overrides or built-in fallback)
// runbook := cfg.Defaults.Runbook           // Default runbook for new sessions
// alertType := cfg.Defaults.AlertType       // Default alert type
// maxIter := cfg.Defaults.MaxIterations     // Default max iterations
// llmProvider := cfg.Defaults.LLMProvider   // Default LLM provider
// 
// // Access registry items
// agent, _ := cfg.GetAgent("KubernetesAgent")
// chain, _ := cfg.GetChainByAlertType("kubernetes")
// 
// // Example: Creating a new alert session
// session := &models.AlertSession{
//     AlertType: cfg.Defaults.AlertType,
//     Runbook:   cfg.Defaults.Runbook,
//     // ... other fields
// }
```

**Key Features:**
- **Thread-safe**: Uses `sync.Once` for safe concurrent access
- **Lazy initialization**: Built-in config is created only when first accessed
- **Encapsulated**: No global mutable state - all data inside BuiltinConfig struct
- **Single source of truth**: All built-in data in one place
- **Testable**: Easy to mock GetBuiltinConfig() for testing

### User-defined Configuration (YAML Files)

Users can:
1. **Add new components**: Define custom agents, MCP servers, chains, LLM providers
2. **Override built-ins**: Use same name/ID to replace built-in configuration
3. **Extend built-ins**: Reference built-in agents/MCP servers in custom chains

**Override Priority**: User-defined (YAML) > Built-in (Go code)

**Example - Override Built-in Agent**:
```yaml
# deploy/config/tarsy.yaml

agents:
  KubernetesAgent:  # Same name as built-in = override
    mcp_servers:
      - "kubernetes-server"
      - "custom-k8s-tool"     # Add custom MCP server
    custom_instructions: |
      Custom instructions replace built-in behavior.
    max_iterations: 30
```

---

## Phase 2.2 Scope: Configuration vs Execution

**What Phase 2.2 Includes (Configuration Management):**
- ✅ YAML schemas for agent definitions (metadata)
- ✅ Configuration loading and parsing
- ✅ AgentRegistry: Stores agent **configuration metadata** (name, mcp_servers, custom_instructions, iteration_strategy)
- ✅ Validation: Ensure agent names exist in built-in or user-defined configs
- ✅ Configuration hierarchical resolution
- ✅ Chain definitions that **reference** agents by name

**What Phase 2.2 Does NOT Include (Agent Execution - Phase 3):**
- ❌ Agent interface/base class definition (BaseAgent)
- ❌ Agent factory for instantiation
- ❌ Agent implementation classes (KubernetesAgent, ChatAgent code)
- ❌ Agent execution logic (process_alert, iteration controllers)
- ❌ Agent-LLM integration
- ❌ Agent-MCP client integration

**Key Point**: In Phase 2.2, agents are just **configuration entries** (name + metadata). The actual agent **implementations** and **instantiation** are handled in Phase 3: Agent Framework.

When Phase 3 is implemented, it will:
1. Define the `Agent` interface and `BaseAgent` class
2. Implement built-in agents (KubernetesAgent, ChatAgent, SynthesisAgent)
3. Create an AgentFactory that uses the AgentRegistry to get configuration
4. Handle agent instantiation with dependency injection (LLM client, MCP client, etc.)

---

## Configuration Files

### 1. Custom Agent Definitions (`tarsy.yaml` - agents section)

**Built-in Agents** (in Go code):
- `KubernetesAgent` - Default Kubernetes troubleshooting agent
- `ChatAgent` - Follow-up chat conversations
- `SynthesisAgent` - Synthesizes parallel investigation results

**User-defined Agents** (in YAML - optional, can override built-ins):

```yaml
# deploy/config/tarsy.yaml

agents:
  # Dictionary of custom agent definitions
  # Agent name is the dictionary key (e.g., "security-agent")
  # Can reference built-in or custom MCP servers
  
  security-agent:
    mcp_servers:
      - "security-server"         # Custom MCP server
      - "kubernetes-server"       # Built-in MCP server
    iteration_strategy: "react"   # Optional: "react" | "native-thinking" | "synthesis"
    max_iterations: 25            # Optional: agent-level max iterations (forces conclusion when reached)
    custom_instructions: |
      You are a security-focused SRE agent specializing in cybersecurity incidents.
      
      PRIORITIES:
      1. Data security and compliance over service availability
      2. Immediate containment of security threats
      3. Detailed forensic analysis and documentation
      
      APPROACH:
      - Immediately assess the severity and scope of security incidents
      - Take containment actions to prevent further damage
      - Gather evidence and maintain chain of custody
      - Provide clear recommendations for remediation

  performance-agent:
    mcp_servers:
      - "monitoring-server"
      - "kubernetes-server"
    iteration_strategy: "react"
    custom_instructions: |
      You are a performance-focused SRE agent specializing in system optimization.
      
      Focus on identifying root causes of performance bottlenecks and
      providing actionable recommendations for optimization.

  # Example: override built-in KubernetesAgent with custom instructions
  KubernetesAgent:
    mcp_servers:
      - "kubernetes-server"
      - "custom-k8s-tool"         # Add additional MCP server
    custom_instructions: |
      Custom instructions override built-in agent behavior.
      This allows customization without modifying Go code.
```

**Go Struct:**

```go
// pkg/config/agent.go

type AgentConfig struct {
    MCPServers         []string          `yaml:"mcp_servers" validate:"required,min=1"`
    CustomInstructions string            `yaml:"custom_instructions"`
    IterationStrategy  IterationStrategy `yaml:"iteration_strategy,omitempty"`
    MaxIterations      *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
    // Note: When max_iterations is reached, agent always forces conclusion (no pause/resume)
}

type AgentRegistry struct {
    agents map[string]*AgentConfig  // Key = agent name
    mu     sync.RWMutex
}

func (r *AgentRegistry) Get(name string) (*AgentConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    agent, exists := r.agents[name]
    if !exists {
        return nil, fmt.Errorf("agent not found: %s", name)
    }
    return agent, nil
}

// Note: Built-in agents are defined in Go code (builtin.go) via GetBuiltinConfig()
// User-defined agents can override built-ins by using the same name
```

---

### 2. Agent Chain Definitions (`tarsy.yaml` - agent_chains section)

Defines multi-stage agent chains for handling specific alert types.

**Built-in Chains** (in Go code):
- `kubernetes-agent-chain` - Basic single-stage Kubernetes analysis

**User-defined Chains** (in YAML):

```yaml
# deploy/config/tarsy.yaml

agent_chains:
  # Dictionary of chain configurations
  # Chain ID is the dictionary key
  
  kubernetes-pod-crashloop-troubleshooting:
    alert_types: ["PodCrashLoop"]  # REQUIRED: Alert types this chain handles
    description: "Deep Kubernetes troubleshooting with specialized analysis"
    stages:
      - name: "Investigation"
        agents:                                 # Always use agents array (even for single agent)
          - name: "KubernetesAgent"             # Single agent
            iteration_strategy: "native-thinking"   # Optional: override agent's strategy
            llm_provider: "gemini-2.5-pro"          # Optional: override default
            max_iterations: 15                      # Optional: agent-level override
    chat:                                       # Optional chat configuration
      enabled: true
      agent: "ChatAgent"
    llm_provider: "gemini-2.5-flash"            # Optional: chain-level default

  kubernetes-deep-troubleshooting:
    alert_types: ["PodCrashLoop - ReAct-Tools"]
    description: "2-stage deep Kubernetes troubleshooting"
    stages:
      - name: "system-data-collection"
        agents:
          - name: "KubernetesAgent"
            iteration_strategy: "react-stage"   # Stage-specific strategy
      
      - name: "final-diagnosis"
        agents:
          - name: "KubernetesAgent"
            iteration_strategy: "react-final-analysis"

  kubernetes-multiple-agents:
    alert_types: ["Kubernetes - Multiple agents - Custom Synthesis"]
    description: "Multiple agents with custom synthesis"
    stages:
      - name: "Investigation"
        max_iterations: 10                      # Stage-level max iterations
        agents:                                 # Multiple agents in parallel
          - name: "KubernetesAgent"
            iteration_strategy: "native-thinking"
            llm_provider: "gemini-2.5-flash"
            max_iterations: 10                  # Per-agent override (forces conclusion when reached)
          
          - name: "KubernetesAgent"
            iteration_strategy: "react"
            llm_provider: "gemini-2.5-flash"
            max_iterations: 3
          
          - name: "KubernetesAgent"
            iteration_strategy: "native-thinking"
            llm_provider: "gemini-2.5-pro"
        
        synthesis:                              # Optional synthesis configuration
          agent: "SynthesisAgent"
          iteration_strategy: "synthesis-native-thinking"
          llm_provider: "gemini-3-pro"

  kubernetes-2-replicas:
    alert_types: ["Kubernetes - 2 replicas - ReAct - Default Synthesis"]
    description: "2 replicas of same agent with automatic synthesis"
    stages:
      - name: "replicas"
        agents:
          - name: "KubernetesAgent"
        replicas: 2                             # Run same agent 2 times (simple redundancy)
    llm_provider: "gemini-2.5-flash"

# Optional: Configure system defaults
defaults:
  alert_type: "PodCrashLoop"                    # Default alert type for UI dropdown
  llm_provider: "google-default"
```

**Go Struct:**

```go
// pkg/config/chain.go

type ChainConfig struct {
    AlertTypes    []string      `yaml:"alert_types" validate:"required,min=1"`
    Description   string        `yaml:"description,omitempty"`
    Stages        []StageConfig `yaml:"stages" validate:"required,min=1,dive"`
    Chat          *ChatConfig   `yaml:"chat,omitempty"`
    LLMProvider   string        `yaml:"llm_provider,omitempty"`
    MaxIterations *int          `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
    MCPServers    []string      `yaml:"mcp_servers,omitempty"`
    // Note: When max_iterations is reached, agent always forces conclusion (no pause/resume)
}

type StageConfig struct {
    Name          string                 `yaml:"name" validate:"required"`
    Agents        []StageAgentConfig  `yaml:"agents" validate:"required,min=1,dive"` // Always use agents array (1+ agents)
    Replicas      int                    `yaml:"replicas,omitempty" validate:"omitempty,min=1"` // For simple redundancy (default: 1)
    SuccessPolicy SuccessPolicy          `yaml:"success_policy,omitempty"`
    MaxIterations *int                   `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
    MCPServers    []string               `yaml:"mcp_servers,omitempty"`
    Synthesis     *SynthesisConfig       `yaml:"synthesis,omitempty"`
    // Note: IterationStrategy and LLMProvider are set per-agent in StageAgentConfig
}

// StageAgentConfig represents an agent reference with stage-level overrides
// Used in stage.agents[] array (even for single-agent stages)
// Parallel execution occurs when: len(agents) > 1 OR replicas > 1
type StageAgentConfig struct {
    Name              string            `yaml:"name" validate:"required"` // Agent name to execute
    LLMProvider       string            `yaml:"llm_provider,omitempty"`   // Optional: override LLM provider
    IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"` // Optional: override iteration strategy
    MaxIterations     *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"` // Optional: override max iterations
    MCPServers        []string          `yaml:"mcp_servers,omitempty"`    // Optional: override MCP servers
}

type SynthesisConfig struct {
    Agent             string            `yaml:"agent,omitempty"`
    IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
    LLMProvider       string            `yaml:"llm_provider,omitempty"`
}

type ChatConfig struct {
    Enabled           bool              `yaml:"enabled"`
    Agent             string            `yaml:"agent,omitempty"`
    IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
    LLMProvider       string            `yaml:"llm_provider,omitempty"`
    MCPServers        []string          `yaml:"mcp_servers,omitempty"`
    MaxIterations     *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}

type ChainRegistry struct {
    chains map[string]*ChainConfig  // Key = chain ID
    mu     sync.RWMutex
}

func (r *ChainRegistry) GetByAlertType(alertType string) (*ChainConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    for _, chain := range r.chains {
        for _, at := range chain.AlertTypes {
            if at == alertType {
                return chain, nil
            }
        }
    }
    return nil, fmt.Errorf("no chain found for alert type: %s", alertType)
}
```

---

### 3. MCP Server Registry (`tarsy.yaml` - mcp_servers section)

Defines available MCP servers and their configurations.

**Built-in MCP Servers** (in Go code):
- `kubernetes-server` - Kubernetes MCP server with default configuration

**User-defined MCP Servers** (in YAML - can override built-ins):

```yaml
# deploy/config/tarsy.yaml

mcp_servers:
  # Dictionary of MCP server configurations
  # Server ID is the dictionary key
  
  kubernetes-server:
    transport:
      type: "http"                              # "stdio" | "http" | "sse"
      url: "http://localhost:8888/mcp"
      timeout: 30
    
    # Alternative: stdio transport
    # transport:
    #   type: "stdio"
    #   command: "npx"
    #   args:
    #     - "-y"
    #     - "kubernetes-mcp-server@0.0.54"
    #     - "--read-only"
    #     - "--disable-destructive"
    #     - "--kubeconfig"
    #     - "{{.KUBECONFIG}}"
    
    instructions: |
      For Kubernetes operations:
      - **IMPORTANT: In multi-cluster environments** (when 'configuration_contexts_list' tool is available):
        * ALWAYS start by calling 'configuration_contexts_list' to see all available contexts
        * Use this information to determine which context to target before performing operations
      - Be careful with cluster-scoped resource listings in large clusters
      - Always prefer namespaced queries when possible
      - If you get "server could not find the requested resource" error:
        * Cluster-scoped resources (Namespace, Node, ClusterRole) should NOT have namespace parameter
        * Namespace-scoped resources (Pod, Deployment, Service) REQUIRE namespace parameter
    
    data_masking:                               # Optional but critical for security
      enabled: true
      pattern_groups:
        - "kubernetes"                          # Expands to: kubernetes_secret, api_key, password, etc.
      patterns:
        - "certificate"                         # Additional individual patterns
        - "token"
        - "email"
      # custom_patterns:                        # Optional custom patterns
      #   - name: "custom-secret"
      #     pattern: "SECRET_.*"
      #     replacement: "__MASKED__"
      #     description: "Custom secret pattern"
    
    summarization:                              # Optional but critical for large responses
      enabled: true
      size_threshold_tokens: 5000               # Summarize if response > 5000 tokens
      summary_max_token_limit: 1000             # Max tokens in summary

  argocd-server:
    transport:
      type: "stdio"
      command: "npx"
      args:
        - "-y"
        - "argocd-mcp-server@latest"
        - "--server"
        - "{{.ARGOCD_SERVER}}"
        - "--auth-token"
        - "{{.ARGOCD_TOKEN}}"
    
    instructions: |
      For ArgoCD operations:
      - Check application sync status and health first
      - Look at sync operations and their results
      - Consider GitOps workflow and source repository state
      - Pay attention to resource hooks and sync waves
    
    data_masking:
      enabled: true
      pattern_groups: ["kubernetes"]
      patterns: ["certificate", "token"]
    
    summarization:
      enabled: true
      size_threshold_tokens: 3000

  monitoring-server:
    transport:
      type: "http"
      url: "{{.MONITORING_MCP_URL}}"
      bearer_token: "{{.MONITORING_TOKEN}}"
      verify_ssl: false
      timeout: 30
    
    instructions: |
      For monitoring operations:
      - Query metrics for the last 24 hours by default
      - Focus on anomalies and trends
      - Consider baseline metrics for comparison
    
    data_masking:
      enabled: true
      patterns: ["email", "token"]
    
    summarization:
      enabled: true
      size_threshold_tokens: 4000
```

**Built-in Data Masking Patterns:**

MCP servers can use built-in masking patterns defined in `builtin.go` (via `GetBuiltinConfig().MaskingPatterns`):

- **Pattern Groups** (convenient presets):
  - `basic`: api_key, password
  - `secrets`: api_key, password, token
  - `security`: api_key, password, token, certificate, certificate_authority_data, email, ssh_key
  - `kubernetes`: kubernetes_secret (code-based), api_key, password, certificate_authority_data
  - `all`: All regex-based patterns

- **Individual Patterns** (regex-based):
  - `api_key`, `password`, `token`, `certificate`, `certificate_authority_data`, `email`, `ssh_key`, `base64_secret`, `base64_short`

- **Code-based Maskers** (structural parsing):
  - `kubernetes_secret`: Masks Secret objects in YAML/JSON (not ConfigMaps) - requires context-aware parsing

See full definitions in the Built-in Configuration section above or reference old TARSy: `backend/tarsy/config/builtin_config.py`

**Go Struct:**

```go
// pkg/config/mcp.go

type MCPServerConfig struct {
    Transport      TransportConfig      `yaml:"transport" validate:"required"`
    Instructions   string               `yaml:"instructions,omitempty"`
    DataMasking    *MaskingConfig       `yaml:"data_masking,omitempty"`
    Summarization  *SummarizationConfig `yaml:"summarization,omitempty"`
}

type TransportConfig struct {
    Type        TransportType `yaml:"type" validate:"required"`
    
    // For stdio
    Command     string   `yaml:"command,omitempty"`
    Args        []string `yaml:"args,omitempty"`
    
    // For http/sse
    URL         string `yaml:"url,omitempty"`
    BearerToken string `yaml:"bearer_token,omitempty"`
    VerifySSL   bool   `yaml:"verify_ssl,omitempty"`
    Timeout     int    `yaml:"timeout,omitempty"`
}

type MaskingConfig struct {
    Enabled        bool             `yaml:"enabled"`
    PatternGroups  []string         `yaml:"pattern_groups,omitempty"`
    Patterns       []string         `yaml:"patterns,omitempty"`
    CustomPatterns []MaskingPattern `yaml:"custom_patterns,omitempty"`
}

type MaskingPattern struct {
    Name        string `yaml:"name" validate:"required"`
    Pattern     string `yaml:"pattern" validate:"required"`
    Replacement string `yaml:"replacement" validate:"required"`
    Description string `yaml:"description,omitempty"`
}

type SummarizationConfig struct {
    Enabled              bool `yaml:"enabled"`
    SizeThresholdTokens  int  `yaml:"size_threshold_tokens" validate:"omitempty,min=100"`
    SummaryMaxTokenLimit int  `yaml:"summary_max_token_limit,omitempty" validate:"omitempty,min=50"`
}

type MCPServerRegistry struct {
    servers map[string]*MCPServerConfig  // Key = server ID
    mu      sync.RWMutex
}

func (r *MCPServerRegistry) Get(id string) (*MCPServerConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    server, exists := r.servers[id]
    if !exists {
        return nil, fmt.Errorf("MCP server not found: %s", id)
    }
    return server, nil
}

// Note: Built-in MCP servers are defined in Go code (builtin.go) via GetBuiltinConfig()
// User-defined servers can override built-ins by using the same ID
```

---

### 4. LLM Provider Configuration (`llm-providers.yaml`)

Defines LLM providers and their configurations.

**Built-in LLM Providers** (in Go code):
- `google-default` - Gemini 2.5 Pro with default settings
- `openai-default` - GPT-5 with default settings
- `anthropic-default` - Claude Sonnet 4 with default settings
- `xai-default` - Grok-4 with default settings
- `vertexai-default` - Claude Sonnet 4.5 on Vertex AI

**User-defined LLM Providers** (in YAML):

```yaml
# deploy/config/llm-providers.yaml

llm_providers:
  # Dictionary of LLM provider configurations
  # Provider name is the dictionary key
  
  gemini-2.5-flash:
    type: google                            # google | openai | anthropic | xai | vertexai
    model: gemini-2.5-flash
    api_key_env: GOOGLE_API_KEY            # Environment variable name for API key
    max_tool_result_tokens: 950000          # Conservative for 1M context
    native_tools:                           # Google-specific native tools
      google_search: true
      code_execution: false
      url_context: true
  
  gemini-2.5-pro:
    type: google
    model: gemini-2.5-pro
    api_key_env: GOOGLE_API_KEY
    max_tool_result_tokens: 950000
    native_tools:
      google_search: true
      code_execution: false
      url_context: true
  
  gemini-3-pro:
    type: google
    model: gemini-3-pro-preview
    api_key_env: GOOGLE_API_KEY
    max_tool_result_tokens: 950000
    native_tools:
      google_search: true
      code_execution: false
      url_context: true
  
  openai-gemini-proxy:
    type: openai
    model: gemini-2.5-pro
    api_key_env: OPENAI_API_KEY
    base_url: https://gemini-proxy.example.com/v1beta/openai
    max_tool_result_tokens: 950000
  
  claude-opus:
    type: anthropic
    model: claude-opus-4
    api_key_env: ANTHROPIC_API_KEY
    max_tool_result_tokens: 150000          # Conservative for 200K context
  
  grok-beta:
    type: xai
    model: grok-4-beta
    api_key_env: XAI_API_KEY
    max_tool_result_tokens: 200000          # Conservative for 256K context
```

**Go Struct:**

```go
// pkg/config/llm.go

type LLMProviderConfig struct {
    Type                  LLMProviderType        `yaml:"type" validate:"required"`
    Model                 string                 `yaml:"model" validate:"required"`
    APIKeyEnv             string                 `yaml:"api_key_env,omitempty"`        // Env var name for API key
    ProjectEnv            string                 `yaml:"project_env,omitempty"`        // For VertexAI/GCP
    LocationEnv           string                 `yaml:"location_env,omitempty"`       // For VertexAI/GCP
    BaseURL               string                 `yaml:"base_url,omitempty"`           // For custom endpoints/proxies
    MaxToolResultTokens   int                    `yaml:"max_tool_result_tokens" validate:"required,min=1000"`
    NativeTools           map[GoogleNativeTool]bool `yaml:"native_tools,omitempty"`    // Google-specific native tools
}

type LLMProviderRegistry struct {
    providers map[string]*LLMProviderConfig  // Key = provider name
    mu        sync.RWMutex
}

func (r *LLMProviderRegistry) Get(name string) (*LLMProviderConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    provider, exists := r.providers[name]
    if !exists {
        return nil, fmt.Errorf("LLM provider not found: %s", name)
    }
    if !provider.Enabled {
        return nil, fmt.Errorf("LLM provider disabled: %s", name)
    }
    return provider, nil
}
```

---

### 5. System Defaults (`tarsy.yaml` - defaults section)

**NEW**: System-wide default configurations for agent/chain execution.

**Note**: Old TARSy doesn't have a `defaults:` section - defaults are hardcoded in python code or come from environment variables. This is a new addition to provide configurable defaults.

**Schema:**

```yaml
# deploy/config/tarsy.yaml

defaults:
  llm_provider: "google-default"                  # Default LLM provider for all agents/chains
  max_iterations: 20                              # Default max iterations (forces conclusion when reached)
  iteration_strategy: "react"                     # Default iteration strategy
  success_policy: "any"                           # Default for parallel stages: "any" | "all"
  alert_type: "kubernetes"                        # Default alert type for new sessions
  runbook: |                                      # Default runbook content for new sessions
    # Generic Troubleshooting Guide
    
    ## Investigation Steps
    1. Analyze the alert
    2. Gather context
    3. Identify root cause
```

**Go Struct:**

```go
// pkg/config/defaults.go

type Defaults struct {
    LLMProvider       string            `yaml:"llm_provider,omitempty"`
    MaxIterations     *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
    IterationStrategy IterationStrategy `yaml:"iteration_strategy,omitempty"`
    SuccessPolicy     SuccessPolicy     `yaml:"success_policy,omitempty"`
    AlertType         string            `yaml:"alert_type,omitempty"`     // Default alert type for new sessions
    Runbook           string            `yaml:"runbook,omitempty"`        // Default runbook content for new sessions
    // Note: When max_iterations is reached, agent always forces conclusion (no pause/resume)
}

type EventsDefaults struct {
    CleanupOnCompletion bool          `yaml:"cleanup_on_completion"`
    TTLFallback         time.Duration `yaml:"ttl_fallback"`
}

type RetentionDefaults struct {
    SoftDeleteAfter time.Duration `yaml:"soft_delete_after"`
}
```

---

## Hierarchical Configuration Resolution

### Resolution Order (Lowest to Highest Priority)

1. **Built-in Defaults** (Go code - `builtin.go`) - Base built-in agents/MCP servers/LLM providers/chains (singleton pattern)
2. **System Defaults** (tarsy.yaml - `defaults:` section) - System-wide configuration defaults
3. **Component Configuration** (tarsy.yaml + llm-providers.yaml) - User-defined/override agents, MCP servers, chains, LLM providers
4. **Environment Variables** - Interpolated `{{.VAR}}` values at startup
5. **Per-Interaction API Overrides** - MCP server selection at runtime (transient, not persisted)

### Configuration Override Example

```go
// Go code: builtin.go - Built-in defaults (singleton pattern)
builtin := config.GetBuiltinConfig()
// builtin.Agents["KubernetesAgent"] = {Description: "...", IterationStrategy: "react"}
// builtin.LLMProviders["google-default"] = {Type: "google", Model: "gemini-2.5-pro", ...}
```

```yaml

# tarsy.yaml - System defaults
defaults:
  llm_provider: "google-default"        # Default LLM for all chains
  max_iterations: 20                    # Default max iterations (forces conclusion when reached)
  alert_type: "kubernetes"              # Default alert type for new sessions (optional, defaults to built-in)
  runbook: |                            # Default runbook for new sessions (optional, defaults to built-in)
    # Company-Specific Troubleshooting Guide
    
    ## Investigation Steps
    1. Check monitoring dashboards
    2. Review recent deployments
    3. Contact on-call engineer if needed
    
    ## Internal Resources
    - Runbook wiki: https://wiki.company.com/runbooks
    - Escalation: #oncall-team

# tarsy.yaml - Custom agent (overrides built-in KubernetesAgent)
agents:
  KubernetesAgent:
    custom_instructions: |
      Custom instructions override built-in agent behavior.
    max_iterations: 25                  # Agent-level override

# tarsy.yaml - Chain configuration
agent_chains:
  k8s-deep-analysis:
    alert_types: ["PodCrashLoop"]
    llm_provider: "gemini-2.5-pro"      # Chain-level override
    max_iterations: 15                  # Chain-level override
    stages:
      - name: "Initial Analysis"
        agents:
          - name: "KubernetesAgent"
            max_iterations: 10          # Agent-level override (highest)

# .env - Environment variables
GEMINI_API_KEY=abc123                   # Interpolated into llm-providers.yaml

# API request - Runtime override (transient)
POST /api/sessions/:id/interactions
{
  "message": "Investigate the issue",
  "mcp_server_ids": ["kubernetes-server"],  # Override MCP servers for this interaction
  "native_tool_ids": ["kubectl"]
}

# Effective max_iterations for this stage: 10 (stage-level wins)
# Effective llm_provider: "gemini-2.5-pro" (chain-level)
```

### Environment Variable Interpolation

**Syntax:**
- `{{.VAR_NAME}}` - Environment variable substitution (Go template syntax)

**Examples:**

```yaml
# Environment variables
api_key: {{.GEMINI_API_KEY}}
endpoint: {{.GEMINI_API_ENDPOINT}}

# Nested in arrays
args:
  - --kubeconfig
  - {{.KUBECONFIG}}
  - --namespace
  - {{.K8S_NAMESPACE}}
```

**Implementation:**

Uses Go's `text/template` package for safe environment variable expansion.
Template syntax `{{.VAR}}` avoids collision with literal `$` in regex patterns, passwords, etc.

```go
// pkg/config/envexpand.go

import (
    "bytes"
    "os"
    "text/template"
)

// ExpandEnv expands environment variables using Go templates
// Uses {{.VAR_NAME}} syntax - no collision with $ in regex patterns
func ExpandEnv(data []byte) []byte {
    tmpl, err := template.New("config").Option("missingkey=zero").Parse(string(data))
    if err != nil {
        return data // Pass through on parse error
    }
    
    // Build environment map
    envMap := make(map[string]string)
    for _, env := range os.Environ() {
        if idx := bytes.IndexByte([]byte(env), '='); idx > 0 {
            envMap[env[:idx]] = env[idx+1:]
        }
    }
    
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, envMap); err != nil {
        return data // Pass through on execution error
    }
    return buf.Bytes()
}

// Usage in config loader:
func LoadConfig(path string) (*Config, error) {
    yamlBytes, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    // Expand environment variables (1 line!)
    expanded := ExpandEnv(yamlBytes)
    
    var config Config
    if err := yaml.Unmarshal(expanded, &config); err != nil {
        return nil, err
    }
    
    // Validate after expansion
    if err := validateConfig(&config); err != nil {
        return nil, err
    }
    
    return &config, nil
}
```

**Benefits:**
- ✅ **Stdlib**: Uses `text/template` from Go standard library
- ✅ **Safe**: No collision with `$` in regex patterns, passwords, shell snippets
- ✅ **Explicit**: `{{.VAR}}` syntax is unambiguous
- ✅ **Extensible**: Template system allows future enhancements if needed
- ✅ **No dependencies**: No regex, no parsing logic

**Notes:**
- Missing environment variables expand to empty string (validation catches this)
- Template syntax `{{.VAR}}` avoids collision with `$` in regex patterns and passwords
- Safer than shell-style `${VAR}` which conflicts with literal `$` usage

---

## Configuration Validation

### Validation Rules

**Agent Configuration:**
- ✅ Required fields present (id, name, iteration_strategy, llm_provider)
- ✅ Valid iteration_strategy (react, native_thinking)
- ✅ Max iterations in valid range (1-50)
- ✅ LLM provider exists in registry
- ✅ MCP servers exist in registry
- ✅ No duplicate agent IDs

**Chain Configuration:**
- ✅ Required fields present (id, name, stages)
- ✅ At least one stage defined
- ✅ Stage indices sequential (0, 1, 2...)
- ✅ Agent references valid
- ✅ Parallel config valid (type, success_policy, agent count)
- ✅ Chat agent exists if chat enabled
- ✅ No duplicate chain IDs
- ✅ No circular dependencies

**MCP Server Configuration:**
- ✅ Required fields present (id, name, transport)
- ✅ Valid transport type (stdio, http, sse)
- ✅ Transport-specific fields present (command for stdio, url for http/sse)
- ✅ Valid durations for timeouts and intervals
- ✅ No duplicate server IDs

**LLM Provider Configuration:**
- ✅ Required fields present (id, name, provider, model, api)
- ✅ Valid provider type (google, openai, anthropic, xai)
- ✅ API endpoint is valid URL
- ✅ API key present (after interpolation)
- ✅ Valid parameter ranges (temperature 0-2, tokens > 0)
- ✅ No duplicate provider IDs

### Validation Implementation

```go
// pkg/config/validator.go

type ConfigValidator struct {
    cfg *Config // Reference to the complete configuration
}

// NewValidator creates a validator for the given configuration
func NewValidator(cfg *Config) *ConfigValidator {
    return &ConfigValidator{cfg: cfg}
}

func (v *ConfigValidator) ValidateAll() error {
    // Fail fast - stop at first error
    
    // Validate agents
    if err := v.validateAgents(); err != nil {
        return fmt.Errorf("agent validation failed: %w", err)
    }
    
    // Validate chains (with cross-references)
    if err := v.validateChains(); err != nil {
        return fmt.Errorf("chain validation failed: %w", err)
    }
    
    // Validate MCP servers
    if err := v.validateMCPServers(); err != nil {
        return fmt.Errorf("MCP server validation failed: %w", err)
    }
    
    // Validate LLM providers
    if err := v.validateLLMProviders(); err != nil {
        return fmt.Errorf("LLM provider validation failed: %w", err)
    }
    
    return nil
}

func (v *ConfigValidator) validateChains() error {
    for chainID, chain := range v.cfg.ChainRegistry.GetAll() {
        // Validate alert_types is not empty
        if len(chain.AlertTypes) == 0 {
            return fmt.Errorf("chain %s: alert_types required", chainID)
        }
        
        // Validate stages
        for i, stage := range chain.Stages {
            // Validate agents field (must have at least 1 agent)
            if len(stage.Agents) == 0 {
                return fmt.Errorf("chain %s stage %d: must specify at least one agent in 'agents' array", chainID, i)
            }
            
            // Validate all agents exist
            for _, pa := range stage.Agents {
                if !v.agentExists(pa.Name) {
                    return fmt.Errorf("chain %s stage %d: agent '%s' not found", chainID, i, pa.Name)
                }
            }
            
            // Validate synthesis agent if present
            if stage.Synthesis != nil && stage.Synthesis.Agent != "" {
                if !v.agentExists(stage.Synthesis.Agent) {
                    return fmt.Errorf("chain %s stage %d: synthesis agent '%s' not found", chainID, i, stage.Synthesis.Agent)
                }
            }
        }
        
        // Validate chat agent
        if chain.Chat != nil && chain.Chat.Enabled {
            if chain.Chat.Agent != "" && !v.agentExists(chain.Chat.Agent) {
                return fmt.Errorf("chain %s: chat agent '%s' not found", chainID, chain.Chat.Agent)
            }
        }
        
        // Validate LLM provider references
        if chain.LLMProvider != "" {
            if !v.llmProviderExists(chain.LLMProvider) {
                return fmt.Errorf("chain %s: LLM provider '%s' not found", chainID, chain.LLMProvider)
            }
        }
    }
    
    return nil
}

func (v *ConfigValidator) agentExists(name string) bool {
    _, err := v.cfg.AgentRegistry.Get(name)
    return err == nil
}

func (v *ConfigValidator) llmProviderExists(name string) bool {
    _, err := v.cfg.LLMProviderRegistry.Get(name)
    return err == nil
}
```

### Validation Error Messages

Clear, actionable error messages (fail fast - shows first error only):

```
# Example 1: Agent validation failure
✗ Configuration loading failed:
agent validation failed: agent 'kubernetes-agent': LLM provider 'gemini-invalid' not found

# Example 2: Chain validation failure  
✗ Configuration loading failed:
chain validation failed: chain 'k8s-deep-analysis' stage 1: agent 'invalid-agent' not found

# Example 3: Environment variable missing
✗ Configuration loading failed:
agent validation failed: required environment variable not set: GEMINI_API_KEY
```

**Validation Strategy**: Fail fast - validation stops at the first error and displays a clear, actionable message. Developers fix one issue at a time.

---

## Configuration Loading Strategy

### Go Binary Configuration Loading

The Go orchestrator binary loads configuration files from the filesystem. The config directory path is configurable via command-line flag or environment variable:

```bash
# Three ways to specify config directory:
# 1. Command-line flag (highest priority)
./tarsy --config-dir=/path/to/config

# 2. Environment variable
export CONFIG_DIR=/path/to/config
./tarsy

# 3. Default (if not specified)
./tarsy  # Uses ./deploy/config
```

**Configuration File Deployment by Environment:**

| Environment | Config File Source | How |
|-------------|-------------------|-----|
| **Local Dev (host)** | Host filesystem | Files in `./deploy/config/` directory |
| **Podman-compose** | Mounted or baked | Volume mount: `./deploy/config:/app/config` OR baked into image during build |
| **K8s/OpenShift Dev** | ConfigMap + Secret | Mount ConfigMap for YAML files, Secret for OAuth2 template |
| **Production** | ConfigMap + Secret | User creates ConfigMaps/Secrets, mounts to pod |

**Example Kubernetes Deployment:**

```yaml
# ConfigMap for YAML files
apiVersion: v1
kind: ConfigMap
metadata:
  name: tarsy-config
data:
  tarsy.yaml: |
    agents:
      - id: kubernetes-agent
        name: "Kubernetes Agent"
        # ... agent config
  llm-providers.yaml: |
    llm_providers:
      - id: gemini-thinking
        api:
          endpoint: {{.GEMINI_API_ENDPOINT}}
          api_key: {{.GEMINI_API_KEY}}
        # ... LLM config

---
# Secret for OAuth2 template
apiVersion: v1
kind: Secret
metadata:
  name: tarsy-oauth2
type: Opaque
stringData:
  oauth2-proxy.cfg.template: |
    client_id = "{{OAUTH2_CLIENT_ID}}"
    client_secret = "{{OAUTH2_CLIENT_SECRET}}"
    # ... oauth2 config

---
# Deployment with config mounts
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tarsy-backend
spec:
  template:
    spec:
      containers:
      - name: backend
        image: tarsy-backend:latest
        args:
          - --config-dir=/etc/tarsy/config
        env:
        - name: GEMINI_API_KEY
          valueFrom:
            secretKeyRef:
              name: tarsy-secrets
              key: gemini-api-key
        volumeMounts:
        - name: config
          mountPath: /etc/tarsy/config
          readOnly: true
      volumes:
      - name: config
        projected:
          sources:
          - configMap:
              name: tarsy-config
          - secret:
              name: tarsy-oauth2
```

### Python LLM Service Configuration

The Python LLM service **does not** read configuration files or environment variables directly. Instead:

1. **Go orchestrator** loads and validates all configuration
2. **Go orchestrator** passes LLM provider configuration to Python via gRPC
3. **Python service** receives configuration as gRPC request parameters

This approach:
- Centralizes configuration in Go (single source of truth)
- Simplifies Python service (no file I/O or env var parsing)
- Ensures configuration consistency (validated once in Go)
- Makes Python service stateless (configuration per request)

**Example gRPC Configuration Passing:**

```protobuf
// proto/llm_service.proto

message LLMConfig {
  string provider = 1;           // "google", "openai", "anthropic", "xai", "vertexai"
  string model = 2;              // "gemini-2.0-flash-thinking-exp-1219"
  string api_key_env = 3;        // Environment variable name (e.g., "GOOGLE_API_KEY") - resolved in Python
  string credentials_env = 4;    // Credentials file path env var (e.g., "GOOGLE_APPLICATION_CREDENTIALS") - for VertexAI
  string base_url = 5;           // Optional custom endpoint/base URL
  int32 max_tool_result_tokens = 6;
  map<string, bool> native_tools = 7;  // Google-specific native tools
  string project = 8;            // GCP project (for VertexAI)
  string location = 9;           // GCP location (for VertexAI)
}

message ThinkingRequest {
  string session_id = 1;
  repeated Message messages = 2;
  
  // Deprecated fields (backward compatibility)
  string model = 3 [deprecated = true];
  optional float temperature = 4 [deprecated = true];
  optional int32 max_tokens = 5 [deprecated = true];
  
  reserved 6;  // Reserved for future use
  
  LLMConfig llm_config = 7;      // New: Go passes config per request
}
```

```go
// Go: Load config and pass env var names to Python
func (s *StageExecutor) executeLLMRequest(ctx context.Context, stage *ent.Stage) error {
    // 1. Resolve LLM provider from registry (loaded from config files)
    llmProvider, err := s.config.LLMProviders.Get(stage.LLMProviderID)
    if err != nil {
        return err
    }
    
    // 2. Build gRPC request with LLM config (pass env var names, not values)
    req := &pb.GenerateRequest{
        LlmConfig: &pb.LLMConfig{
            Provider:       llmProvider.Provider,
            Model:          llmProvider.Model,
            ApiKeyEnv:      llmProvider.API.APIKeyEnv,        // Pass env var name (e.g., "GOOGLE_API_KEY")
            CredentialsEnv: llmProvider.API.CredentialsEnv,   // For VertexAI (e.g., "GOOGLE_APPLICATION_CREDENTIALS")
            BaseUrl:        llmProvider.API.Endpoint,
            Project:        llmProvider.VertexAI.Project,     // For VertexAI
            Location:       llmProvider.VertexAI.Location,    // For VertexAI
        },
        Messages: convertMessages(stage.Messages),
        Tools:    convertTools(stage.Tools),
    }
    
    // 3. Call Python LLM service (secrets never sent over wire)
    stream, err := s.llmClient.Generate(ctx, req)
    // ...
}
```

```python
# Python: Receive config from Go, resolve credentials from environment
def Generate(self, request, context):
    # Configuration comes from Go via gRPC request
    llm_config = request.llm_config
    
    # Resolve API key from environment variable name
    api_key = os.getenv(llm_config.api_key_env)
    if not api_key and llm_config.provider != "vertexai":
        raise ValueError(f"Environment variable '{llm_config.api_key_env}' not found")
    
    # Initialize LLM client with resolved credentials
    if llm_config.provider == "google":
        client = genai.Client(
            api_key=api_key,  # Resolved from environment
            http_options={'api_version': 'v1beta'}
        )
        model_name = llm_config.model
    elif llm_config.provider == "openai":
        client = openai.OpenAI(api_key=api_key)  # Resolved from environment
        model_name = llm_config.model
    # ... handle other providers
    
    # Use config parameters
    response = client.generate(
        model=model_name,
        messages=request.messages,
        temperature=llm_config.temperature,
        max_tokens=llm_config.max_tokens,
    )
    # ...
```

**Benefits of This Approach:**
- ✅ Single source of truth (Go owns configuration)
- ✅ Configuration validated once (in Go)
- ✅ Python service remains stateless
- ✅ No file I/O or env var parsing in Python
- ✅ Easier to test Python service (mock gRPC requests)
- ✅ Configuration changes only require Go service restart

---

## Configuration Loading

### Startup Sequence

```go
// cmd/tarsy/main.go

func main() {
    ctx := context.Background()
    
    // Parse command-line flags
    configDir := flag.String("config-dir", 
        getEnv("CONFIG_DIR", "./deploy/config"), 
        "Path to configuration directory")
    flag.Parse()
    
    // Load and validate configuration (all orchestration in config package)
    cfg, err := config.Initialize(ctx, *configDir)
    if err != nil {
        log.Fatal("Failed to initialize configuration", "error", err)
    }
    
    // Continue with service initialization...
    // Use cfg.GetAgent(), cfg.GetChainByAlertType(), etc.
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

### Configuration Loader Implementation

```go
// pkg/config/loader.go

// Config is the umbrella configuration object that encapsulates
// all registries, defaults, and configuration state
type Config struct {
    configDir string // For reference/logging
    
    Defaults            *Defaults       // System-wide defaults (includes alert_type, runbook, execution configs)
    AgentRegistry       *AgentRegistry
    ChainRegistry       *ChainRegistry
    MCPServerRegistry   *MCPServerRegistry
    LLMProviderRegistry *LLMProviderRegistry
}

// Thread-safe accessors for configuration
func (c *Config) GetAgent(name string) (*AgentConfig, error)
func (c *Config) GetChainByAlertType(alertType string) (*ChainConfig, error)
func (c *Config) GetMCPServer(id string) (*MCPServerConfig, error)
func (c *Config) GetLLMProvider(name string) (*LLMProviderConfig, error)

// stats returns configuration statistics for logging/monitoring
func (c *Config) stats() ConfigStats {
    return ConfigStats{
        Agents:       len(c.AgentRegistry.GetAll()),
        Chains:       len(c.ChainRegistry.GetAll()),
        MCPServers:   len(c.MCPServerRegistry.GetAll()),
        LLMProviders: len(c.LLMProviderRegistry.GetAll()),
    }
}

type ConfigStats struct {
    Agents       int
    Chains       int
    MCPServers   int
    LLMProviders int
}

// Initialize loads, validates, and returns ready-to-use configuration
// This is the primary entry point for configuration loading
func Initialize(ctx context.Context, configDir string) (*Config, error) {
    log := slog.With("config_dir", configDir)
    log.Info("Initializing configuration")
    
    // 1. Load configuration files
    cfg, err := load(ctx, configDir)
    if err != nil {
        return nil, fmt.Errorf("failed to load configuration: %w", err)
    }
    
    // 2. Validate all configuration
    if err := validate(cfg); err != nil {
        return nil, fmt.Errorf("configuration validation failed: %w", err)
    }
    
    log.Info("Configuration initialized successfully", "stats", cfg.stats())
    return cfg, nil
}

// load is the internal loader (not exported)
func load(ctx context.Context, configDir string) (*Config, error) {
    loader := &configLoader{
        configDir: configDir,
    }
    
    // 1. Load tarsy.yaml (contains mcp_servers, agents, agent_chains, defaults)
    tarsyConfig, err := loader.loadTarsyYAML()
    if err != nil {
        return nil, fmt.Errorf("failed to load tarsy.yaml: %w", err)
    }
    
    // 2. Load llm-providers.yaml
    llmProviders, err := loader.loadLLMProvidersYAML()
    if err != nil {
        return nil, fmt.Errorf("failed to load llm-providers.yaml: %w", err)
    }
    
    // 3. Merge built-in + user-defined components (user overrides built-in)
    agents := mergeAgents(getBuiltinAgents(), tarsyConfig.Agents)
    mcpServers := mergeMCPServers(getBuiltinMCPServers(), tarsyConfig.MCPServers)
    chains := mergeChains(getBuiltinChains(), tarsyConfig.AgentChains)
    llmProvidersMerged := mergeLLMProviders(getBuiltinLLMProviders(), llmProviders)
    
    // 4. Build registries
    agentRegistry := NewAgentRegistry(agents)
    mcpServerRegistry := NewMCPServerRegistry(mcpServers)
    chainRegistry := NewChainRegistry(chains)
    llmProviderRegistry := NewLLMProviderRegistry(llmProvidersMerged)
    
    // 5. Resolve defaults (YAML overrides built-in)
    defaults := tarsyConfig.Defaults
    if defaults == nil {
        defaults = &Defaults{}
    }
    
    // Apply built-in defaults for any unset values
    builtin := GetBuiltinConfig()
    if defaults.AlertType == "" {
        defaults.AlertType = builtin.DefaultAlertType
    }
    if defaults.Runbook == "" {
        defaults.Runbook = builtin.DefaultRunbook
    }
    
    return &Config{
        configDir:           configDir,
        Defaults:            defaults,
        AgentRegistry:       agentRegistry,
        ChainRegistry:       chainRegistry,
        MCPServerRegistry:   mcpServerRegistry,
        LLMProviderRegistry: llmProviderRegistry,
    }, nil
}

// validate performs comprehensive validation on loaded configuration
func validate(cfg *Config) error {
    validator := NewValidator(cfg)
    return validator.ValidateAll()
}

type configLoader struct {
    configDir string
}

func (l *configLoader) loadYAML(filename string, target interface{}) error {
    path := filepath.Join(l.configDir, filename)
    
    // Read file
    data, err := os.ReadFile(path)
    if err != nil {
        return fmt.Errorf("failed to read %s: %w", filename, err)
    }
    
    // Expand environment variables
    data = ExpandEnv(data)
    
    // Parse YAML
    if err := yaml.Unmarshal(data, target); err != nil {
        return fmt.Errorf("failed to parse %s: %w", filename, err)
    }
    
    return nil
}

// TarsyYAMLConfig represents the complete tarsy.yaml file structure
type TarsyYAMLConfig struct {
    MCPServers  map[string]MCPServerConfig `yaml:"mcp_servers"`
    Agents      map[string]AgentConfig     `yaml:"agents"`
    AgentChains map[string]ChainConfig     `yaml:"agent_chains"`
    Defaults    *Defaults                  `yaml:"defaults"` // Includes alert_type, runbook, and execution defaults
}

func (l *configLoader) loadTarsyYAML() (*TarsyYAMLConfig, error) {
    var config TarsyYAMLConfig
    
    if err := l.loadYAML("tarsy.yaml", &config); err != nil {
        return nil, err
    }
    
    return &config, nil
}

// LLMProvidersYAMLConfig represents the complete llm-providers.yaml file structure
type LLMProvidersYAMLConfig struct {
    LLMProviders map[string]LLMProviderConfig `yaml:"llm_providers"`
}

func (l *configLoader) loadLLMProvidersYAML() (map[string]LLMProviderConfig, error) {
    var config LLMProvidersYAMLConfig
    
    if err := l.loadYAML("llm-providers.yaml", &config); err != nil {
        return nil, err
    }
    
    return config.LLMProviders, nil
}
```

---

## Per-Alert Configuration Overrides

### MCP Selection Override

Allows per-alert customization of MCP servers and tools (already supported in database schema from Phase 2.1).

**API Request:**

```json
POST /api/sessions
{
  "alert_data": "...",
  "chain_id": "k8s-deep-analysis",
  "mcp": {
    "servers": [
      {
        "name": "kubernetes-server",
        "tools": ["kubectl-get", "kubectl-describe"]
      }
    ],
    "native_tools": {
      "google_search": true,
      "code_execution": false
    }
  }
}
```

**Validation:**

```go
// pkg/services/session_service.go

func (s *SessionService) CreateSession(ctx context.Context, req CreateSessionRequest) (*ent.AlertSession, error) {
    // Validate chain exists
    chain, err := s.chainRegistry.Get(req.ChainID)
    if err != nil {
        return nil, fmt.Errorf("invalid chain: %w", err)
    }
    
    // Validate MCP override if provided
    if req.MCP != nil {
        if err := s.validateMCPOverride(req.MCP); err != nil {
            return nil, fmt.Errorf("invalid MCP override: %w", err)
        }
    }
    
    // Create session with MCP override
    session, err := s.client.AlertSession.Create().
        SetChainID(req.ChainID).
        SetAlertData(req.AlertData).
        SetNillableMcpSelection(req.MCP).  // Store override
        Save(ctx)
    
    return session, err
}

func (s *SessionService) validateMCPOverride(mcp *models.MCPSelectionConfig) error {
    // Validate server names exist in registry
    for _, server := range mcp.Servers {
        if _, err := s.mcpRegistry.Get(server.Name); err != nil {
            return fmt.Errorf("MCP server %s: %w", server.Name, err)
        }
        
        // Validate tool names if specified
        if server.Tools != nil {
            // TODO: Validate tool names against server's available tools
        }
    }
    
    return nil
}
```

**Usage During Execution:**

```go
// pkg/orchestrator/stage_executor.go

func (e *StageExecutor) Execute(ctx context.Context, stage *ent.Stage) error {
    session := stage.Edges.Session
    
    // Get chain config
    chain, _ := e.chainRegistry.Get(session.ChainID)
    
    // Get agent config
    agent, _ := e.agentRegistry.Get(stage.AgentName)
    
    // Determine MCP servers to use
    var mcpServers []string
    if session.McpSelection != nil {
        // Use override from session
        for _, server := range session.McpSelection.Servers {
            mcpServers = append(mcpServers, server.Name)
        }
    } else {
        // Use agent defaults
        mcpServers = agent.MCPServers
    }
    
    // Initialize MCP clients
    mcpClients := make(map[string]*mcp.Client)
    for _, serverID := range mcpServers {
        serverConfig, _ := e.mcpRegistry.Get(serverID)
        client, _ := mcp.NewClient(serverConfig)
        mcpClients[serverID] = client
    }
    
    // Execute agent with resolved configuration
    return e.executeAgent(ctx, agent, mcpClients)
}
```

---

## Testing Strategy

### Configuration Validation Tests

```go
// pkg/config/validator_test.go

func TestValidateChainReferences(t *testing.T) {
    tests := []struct {
        name    string
        chain   ChainConfig
        wantErr bool
        errMsg  string
    }{
        {
            name: "valid single agent chain",
            chain: ChainConfig{
                AlertTypes: []string{"PodCrashLoop"},
                Stages: []StageConfig{
                    {
                        Name: "Investigation",
                        Agents: []StageAgentConfig{
                            {Name: "KubernetesAgent"},
                        },
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "invalid agent reference",
            chain: ChainConfig{
                AlertTypes: []string{"PodCrashLoop"},
                Stages: []StageConfig{
                    {
                        Name: "Investigation",
                        Agents: []StageAgentConfig{
                            {Name: "nonexistent-agent"},
                        },
                    },
                },
            },
            wantErr: true,
            errMsg:  "agent not found",
        },
        {
            name: "missing alert_types",
            chain: ChainConfig{
                AlertTypes: []string{},
                Stages: []StageConfig{
                    {
                        Name: "Stage 1",
                        Agents: []StageAgentConfig{
                            {Name: "KubernetesAgent"},
                        },
                    },
                },
            },
            wantErr: true,
            errMsg:  "alert_types required",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            validator := setupTestValidator(t)
            err := validator.validateChain(&tt.chain)
            
            if tt.wantErr {
                assert.Error(t, err)
                assert.Contains(t, err.Error(), tt.errMsg)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}

func TestMCPOverrideValidation(t *testing.T) {
    service := setupTestService(t)
    
    tests := []struct {
        name    string
        mcp     *models.MCPSelectionConfig
        wantErr bool
    }{
        {
            name: "valid server override",
            mcp: &models.MCPSelectionConfig{
                Servers: []models.MCPServerSelection{
                    {Name: "kubernetes-server", Tools: ptr([]string{"kubectl-get"})},
                },
            },
            wantErr: false,
        },
        {
            name: "invalid server name",
            mcp: &models.MCPSelectionConfig{
                Servers: []models.MCPServerSelection{
                    {Name: "nonexistent-server"},
                },
            },
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := service.validateMCPOverride(tt.mcp)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

### Environment Variable Interpolation Tests

```go
// pkg/config/envexpand_test.go

func TestExpandEnv(t *testing.T) {
    tests := []struct {
        name  string
        input string
        env   map[string]string
        want  string
    }{
        {
            name:  "simple substitution {{.VAR}}",
            input: "api_key: {{.API_KEY}}",
            env:   map[string]string{"API_KEY": "secret123"},
            want:  "api_key: secret123",
        },
        {
            name:  "simple substitution $VAR",
            input: "api_key: $API_KEY",
            env:   map[string]string{"API_KEY": "secret123"},
            want:  "api_key: secret123",
        },
        {
            name:  "missing var expands to empty",
            input: "endpoint: {{.ENDPOINT}}",
            env:   map[string]string{},
            want:  "endpoint: ",
        },
        {
            name:  "multiple substitutions",
            input: "url: {{.PROTOCOL}}://{{.HOST}}:{{.PORT}}",
            env: map[string]string{
                "PROTOCOL": "https",
                "HOST":     "example.com",
                "PORT":     "443",
            },
            want: "url: https://example.com:443",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Set up environment
            for k, v := range tt.env {
                t.Setenv(k, v) // Automatic cleanup
            }
            
            result := ExpandEnv([]byte(tt.input))
            assert.Equal(t, tt.want, string(result))
        })
    }
}

// Test validation catches missing required vars
func TestValidationCatchesMissingEnvVars(t *testing.T) {
    yamlContent := `
llm_providers:
  google-default:
    api_key: ${GOOGLE_API_KEY}  # Empty after expansion
`
    
    expanded := ExpandEnv([]byte(yamlContent))
    
    var config Config
    err := yaml.Unmarshal(expanded, &config)
    assert.NoError(t, err) // Unmarshaling succeeds
    
    // Validation catches the empty api_key
    err = validateConfig(&config)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "api_key")
}
```

### Integration Tests

```go
// pkg/config/integration_test.go

func TestConfigurationLoadingEndToEnd(t *testing.T) {
    // Create temporary config directory
    tempDir := t.TempDir()
    
    // Write test configuration files
    writeTestConfig(t, tempDir, "tarsy.yaml", testTarsyYAML)
    writeTestConfig(t, tempDir, "llm-providers.yaml", testLLMProvidersYAML)
    
    // Set required environment variables
    t.Setenv("GEMINI_API_KEY", "test-key")
    
    // Initialize configuration (load + validate)
    cfg, err := config.Initialize(context.Background(), tempDir)
    require.NoError(t, err)
    
    // Validate registries populated
    assert.NotNil(t, cfg.AgentRegistry)
    assert.NotNil(t, cfg.ChainRegistry)
    assert.NotNil(t, cfg.MCPServerRegistry)
    assert.NotNil(t, cfg.LLMProviderRegistry)
    
    // Test agent lookup
    agent, err := cfg.AgentRegistry.Get("kubernetes-agent")
    require.NoError(t, err)
    assert.Equal(t, "Kubernetes Agent", agent.Name)
    
    // Test chain lookup
    chain, err := cfg.ChainRegistry.Get("k8s-deep-analysis")
    require.NoError(t, err)
    assert.Len(t, chain.Stages, 3)
    
    // Note: Validation already done by Initialize(), so cfg is guaranteed valid
}
```

---

## Implementation Checklist

### Phase 2.2: Configuration System
- [ ] Define YAML schemas for all configuration files
  - [ ] tarsy.yaml schema (mcp_servers, agents, agent_chains, defaults)
  - [ ] llm-providers.yaml schema (llm_providers)
- [ ] Implement Go built-in configurations (singleton pattern)
  - [ ] builtin.go: BuiltinConfig struct with all built-in data
  - [ ] builtin.go: GetBuiltinConfig() with sync.Once for thread-safe lazy initialization
  - [ ] builtin.go: initBuiltinConfig() to populate Agents, MCPServers, LLMProviders, ChainDefinitions
  - [ ] builtin.go: MaskingPatterns, PatternGroups, CodeMaskers
  - [ ] builtin.go: DefaultRunbook, DefaultAlertType constants
- [ ] Implement Go enums (type-safe string constants)
  - [ ] enums.go: IterationStrategy (react, react-stage, react-final-analysis, native-thinking, synthesis, synthesis-native-thinking)
  - [ ] enums.go: SuccessPolicy (all, any)
  - [ ] enums.go: TransportType (stdio, http, sse)
  - [ ] enums.go: LLMProviderType (google, openai, anthropic, xai, vertexai)
  - [ ] enums.go: GoogleNativeTool (google_search, code_execution, url_context)
- [ ] Implement Go structs with validation tags (using enum types)
  - [ ] AgentConfig (custom_instructions, mcp_servers, iteration_strategy, max_iterations)
  - [ ] ChainConfig (alert_types, stages, chat, llm_provider, max_iterations, etc.)
  - [ ] StageConfig (name, agents, replicas, success_policy, synthesis, etc.)
  - [ ] StageAgentConfig (name, llm_provider, iteration_strategy, max_iterations, mcp_servers)
  - [ ] SynthesisConfig (agent, iteration_strategy, llm_provider)
  - [ ] ChatConfig (enabled, agent, iteration_strategy, llm_provider, mcp_servers, max_iterations)
  - [ ] MCPServerConfig (transport, instructions, data_masking, summarization)
  - [ ] MaskingConfig, SummarizationConfig
  - [ ] LLMProviderConfig (type, model, api_key_env, max_tool_result_tokens, native_tools)
  - [ ] Defaults (llm_provider, max_iterations, iteration_strategy, success_policy)
  - [ ] TarsyYAMLConfig (root structure with all sections)
  - [ ] LLMProvidersYAMLConfig (root structure)
- [ ] Implement configuration loader
  - [ ] YAML parsing
  - [ ] Environment variable interpolation
  - [ ] Hierarchical resolution
- [ ] Implement in-memory registries
  - [ ] AgentRegistry
  - [ ] ChainRegistry
  - [ ] MCPServerRegistry
  - [ ] LLMProviderRegistry
- [ ] Implement configuration validator
  - [ ] Field validation
  - [ ] Cross-reference validation
  - [ ] Clear error messages
- [ ] Implement MCP override validation
  - [ ] Server existence check
  - [ ] Tool name validation
- [ ] Write configuration tests
  - [ ] Validation tests
  - [ ] Interpolation tests
  - [ ] Loading tests
  - [ ] Integration tests
- [ ] Update proto file for configuration passing
  - [ ] Add LLMConfig message
  - [ ] Update GenerateRequest to include llm_config
  - [ ] Add Tool and other message types
  - [ ] Regenerate Go and Python code
- [ ] Implement configuration passing to Python
  - [ ] Resolve LLM config from registry in Go
  - [ ] Build LLMConfig proto message
  - [ ] Pass config in each gRPC request
  - [ ] Update Python to use config from request
- [ ] Add command-line flags
  - [ ] --config-dir flag
  - [ ] CONFIG_DIR environment variable support
  - [ ] Default to ./config
- [ ] Create example configuration files
  - [ ] deploy/config/tarsy.yaml.example (main config with env var placeholders)
  - [ ] deploy/config/llm-providers.yaml.example (with env var placeholders)
  - [ ] deploy/config/.env.example (all required/optional variables with comments for different environments)
  - [ ] deploy/config/oauth2-proxy.cfg.template
- [ ] Create deployment examples
  - [ ] Kubernetes ConfigMap example
  - [ ] Kubernetes Secret example
  - [ ] Deployment manifest with volume mounts
  - [ ] Podman-compose volume mount example
- [ ] Document configuration system
  - [ ] Configuration file reference
  - [ ] Environment variable reference
  - [ ] Deployment guide (local/podman/k8s)
  - [ ] Configuration loading strategy

---

## Design Decisions

**Built-in + User-defined Configuration**: Built-in agents, MCP servers, LLM providers, and chains defined in Go code (`builtin.go`) using encapsulated singleton pattern (thread-safe with `sync.Once`). Users can add new components or override built-ins via YAML files. User-defined configurations have higher priority.

**Config Umbrella Object**: Single `Config` struct encapsulates all registries, defaults, and configuration state. Provides thread-safe accessors and acts as the single source of truth for configuration at runtime.

**Single-Function Initialization**: `config.Initialize()` handles all loading, validation, and setup internally. Main.go stays thin - just calls Initialize and uses the returned Config. All orchestration logic lives in the config package.

**File-Based Configuration**: Configuration stored in YAML files (not database) for version control, code review, and deployment simplicity.

**Dictionary-Based Structure**: Configuration uses dictionaries (maps) keyed by name/ID, not arrays. Matches old TARSy structure for familiarity and easier lookups.

**In-Memory Registries**: Configuration loaded once at startup into in-memory registries. Configuration changes require service restart (no hot-reload support).

**Strong Validation**: Comprehensive validation on startup with clear error messages. Fail fast if configuration invalid.

**Environment Variable Interpolation**: Uses `text/template` for safe `{{.VAR}}` syntax with no `$` collision in configuration files.

**Environment-Agnostic YAML**: YAML config files work across all environments (local dev, podman, k8s). Environment differences handled via .env files only.

**OAuth2 Proxy Configuration**: Template-based OAuth2 proxy config (same as old TARSy). Template tracked in git, generated file gitignored.

**Configurable Config Directory**: Go binary accepts `--config-dir` flag or `CONFIG_DIR` env var for flexible config file location. Enables different deployment strategies (host filesystem, container mounts, ConfigMaps).

**Python Configuration via gRPC**: Python LLM service receives configuration from Go via gRPC requests (not from files or env vars). Centralizes configuration in Go, simplifies Python service, ensures single source of truth.

**Per-Alert Overrides**: MCP selection can be overridden per alert via API (stored in database, not config files).

**Simplified Stage Configuration**: Always use `agents: []` array for stage configuration (even for single agent). Rationale: Old TARSy had both `agent: "name"` (single) and `agents: [...]` (multiple) with complex mutual exclusivity validation. New TARSy simplifies to always use `agents: [{name: "AgentName"}]` - more consistent, simpler validation, easier to process. Single-agent stages just have 1 item in the array.

**Consolidated Defaults**: All default values (execution configs AND application state defaults) in single `Defaults` struct under `defaults:` YAML section. Includes `alert_type` and `runbook` alongside execution defaults like `llm_provider` and `max_iterations`. Rationale: Better organization, cleaner YAML structure, intuitive grouping of all configurable defaults, simpler Config struct. While `alert_type` and `runbook` don't participate in hierarchical override like execution configs, consolidating them improves maintainability and user experience.

---

## Decided Against

**Pause/Resume Feature**: Not implementing pause/resume functionality for agents that reach max iterations. When `max_iterations` is reached, agents will always force conclusion. Rationale: Simpler implementation, clearer behavior, no state management for paused sessions. Old TARSy had `force_conclusion_at_max_iterations` flag, but new TARSy removes this complexity.

**Hot Reload**: Not implementing configuration hot-reload. Configuration changes require service restart. Rationale: Keep it simple like old TARSy - simpler implementation, clearer deployment process, no partial configuration states, atomic configuration updates.

**Database-Stored Configuration**: Not storing configuration in database. Rationale: Version control, code review, infrastructure-as-code best practices.

**Dynamic Agent Registration**: Not supporting runtime agent registration. All agents defined in configuration files. Rationale: Clear inventory, better validation, simpler architecture.

**Environment Override YAML Files**: Not using environment-specific override YAML files (e.g., `environments/production.yaml`). Rationale: Simpler implementation, follows 12-factor app principles, Kubernetes-native (ConfigMaps/Secrets map directly to environment variables), no merge logic needed.

**Configuration Management API**: No API for reading or writing configuration. File-based only. Rationale: Maintains GitOps workflow, clear audit trail via git, code review for changes, simpler implementation.

**Configuration Versioning**: No version field in configuration files (for now). Rationale: Keep it simple, breaking changes handled manually with documentation, can add versioning later if needed.

**Additional Validation Tools**: No separate CLI tools for validation, diff, dry-run, or export. Rationale: Developers can test in dev environments, service validates on startup and fails safely, keeps tooling minimal.

**JSON Schema Documentation**: No JSON Schema generation for YAML files. Rationale: Comments in YAML files are sufficient, Go struct tags provide runtime validation, simpler maintenance.

---

## Summary of Key Design Points

### ✅ Configuration Files
- **tarsy.yaml**: Main orchestration configuration (~800-1000 lines)
  - `mcp_servers:` - MCP server configs (transport, instructions, data_masking, summarization)
  - `agents:` - Custom agent definitions (custom_instructions, mcp_servers, max_iterations)
  - `agent_chains:` - Multi-stage chains with alert_types (single/parallel/replica execution)
  - `defaults:` - System-wide defaults (llm_provider, max_iterations, alert_type, runbook, etc.)
- **llm-providers.yaml**: LLM provider configurations (~50 lines)
  - `llm_providers:` - Provider configs (type, model, api_key_env, max_tool_result_tokens, native_tools)
- **.env**: Environment-specific values and secrets (gitignored)
- **oauth2-proxy.cfg.template**: OAuth2 proxy template (generates oauth2-proxy.cfg)

### ✅ Key Features
- **Built-in + User-defined**: Built-in agents/MCP/LLM/chains in Go code, user can add or override in YAML
- **YAML-based**: Human-readable, easy to edit, version controlled
- **Dictionary structure**: Components keyed by name/ID (not arrays)
- **Environment variable interpolation**: Uses `text/template` (stdlib) for `{{.VAR}}` syntax (no `$` collision)
- **Environment-agnostic**: Same YAML works across all environments (local, podman, k8s, prod)
- **12-factor app compliance**: Environment-specific config via environment variables
- **Startup validation**: Fail-fast validation on startup with clear error messages
- **File-based only**: No configuration API, changes via git + restart
- **In-memory registries**: Fast lookups, type-safe access
- **Alert-type routing**: Chains specify `alert_types` for request routing
- **Per-interaction overrides**: MCP server selection via API (runtime only, transient)
- **OAuth2 integration**: Template-based OAuth2 proxy configuration
- **Simple examples**: `.example` suffixed files for easy setup
- **Data masking & summarization**: Critical security and performance features for MCP servers

### ✅ Go Implementation
- **Single entry point**: `config.Initialize(ctx, configDir)` - all orchestration encapsulated in config package
- **Type-safe structs**: Go structs with validation tags and enum types for string constants
- **Enums for type safety**: IterationStrategy, SuccessPolicy, TransportType, LLMProviderType, GoogleNativeTool
- **Singleton pattern**: Built-in configs use encapsulated singleton with `sync.Once` (thread-safe, lazy-initialized)
- **Registry pattern**: Thread-safe registries store configuration metadata (not implementations)
- **Clear validation**: Detailed error messages for misconfigurations
- **Environment integration**: `text/template` for safe `{{.VAR}}` substitution (no `$` collision)
- **Phase boundary**: Configuration only - agent execution is Phase 3

---

## Enums (Type-Safe String Fields)

The following string fields should be implemented as enums in Go for type safety:

### Configuration Enums

```go
// pkg/config/enums.go

// IterationStrategy defines available agent iteration strategies
type IterationStrategy string

const (
    IterationStrategyReact               IterationStrategy = "react"
    IterationStrategyReactStage          IterationStrategy = "react-stage"
    IterationStrategyReactFinalAnalysis  IterationStrategy = "react-final-analysis"
    IterationStrategyNativeThinking      IterationStrategy = "native-thinking"
    IterationStrategySynthesis           IterationStrategy = "synthesis"
    IterationStrategySynthesisNativeThinking IterationStrategy = "synthesis-native-thinking"
)

// SuccessPolicy defines success criteria for parallel stages
type SuccessPolicy string

const (
    SuccessPolicyAll SuccessPolicy = "all" // All agents must succeed
    SuccessPolicyAny SuccessPolicy = "any" // At least one agent must succeed (default)
)

// TransportType defines MCP server transport types
type TransportType string

const (
    TransportTypeStdio TransportType = "stdio" // Subprocess communication
    TransportTypeHTTP  TransportType = "http"  // HTTP/HTTPS JSON-RPC
    TransportTypeSSE   TransportType = "sse"   // Server-Sent Events
)

// LLMProviderType defines supported LLM providers
type LLMProviderType string

const (
    LLMProviderTypeOpenAI    LLMProviderType = "openai"
    LLMProviderTypeGoogle    LLMProviderType = "google"
    LLMProviderTypeXAI       LLMProviderType = "xai"
    LLMProviderTypeAnthropic LLMProviderType = "anthropic"
    LLMProviderTypeVertexAI  LLMProviderType = "vertexai"
)

// GoogleNativeTool defines Google/Gemini native tools
type GoogleNativeTool string

const (
    GoogleNativeToolGoogleSearch  GoogleNativeTool = "google_search"
    GoogleNativeToolCodeExecution GoogleNativeTool = "code_execution"
    GoogleNativeToolURLContext    GoogleNativeTool = "url_context"
)
```

### Database/Runtime Enums (from Phase 2.1)

These enums are already defined in the database design but referenced here for completeness:

```go
// pkg/models/enums.go (from Phase 2.1)

// SessionStatus defines alert session processing states
type SessionStatus string

const (
    SessionStatusPending    SessionStatus = "pending"
    SessionStatusInProgress SessionStatus = "in_progress"
    SessionStatusCompleted  SessionStatus = "completed"
    SessionStatusFailed     SessionStatus = "failed"
    SessionStatusCancelled  SessionStatus = "cancelled"
    SessionStatusTimedOut   SessionStatus = "timed_out"
)

// StageStatus defines stage execution states
type StageStatus string

const (
    StageStatusPending   StageStatus = "pending"
    StageStatusActive    StageStatus = "active"
    StageStatusCompleted StageStatus = "completed"
    StageStatusFailed    StageStatus = "failed"
    StageStatusCancelled StageStatus = "cancelled"
    StageStatusTimedOut  StageStatus = "timed_out"
    StageStatusPartial   StageStatus = "partial"
)

// ParallelType defines types of parallel execution
type ParallelType string

const (
    ParallelTypeSingle     ParallelType = "single"      // Non-parallel
    ParallelTypeMultiAgent ParallelType = "multi_agent" // Different agents in parallel
    ParallelTypeReplica    ParallelType = "replica"     // Same agent replicated
)

// LLMInteractionType categorizes LLM interaction purposes
type LLMInteractionType string

const (
    LLMInteractionTypeInvestigation         LLMInteractionType = "investigation"
    LLMInteractionTypeSummarization         LLMInteractionType = "summarization"
    LLMInteractionTypeFinalAnalysis         LLMInteractionType = "final_analysis"
    LLMInteractionTypeForcedConclusion      LLMInteractionType = "forced_conclusion"
    LLMInteractionTypeFinalAnalysisSummary  LLMInteractionType = "final_analysis_summary"
)
```

### Usage in Configuration Structs

Update all string fields to use enum types:

```go
type AgentConfig struct {
    MCPServers         []string          `yaml:"mcp_servers" validate:"required,min=1"`
    CustomInstructions string            `yaml:"custom_instructions"`
    IterationStrategy  IterationStrategy `yaml:"iteration_strategy,omitempty"`
    MaxIterations      *int              `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
}

type StageConfig struct {
    Name          string             `yaml:"name" validate:"required"`
    Agents        []StageAgentConfig `yaml:"agents" validate:"required,min=1,dive"`
    Replicas      int                `yaml:"replicas,omitempty" validate:"omitempty,min=1"`
    SuccessPolicy SuccessPolicy      `yaml:"success_policy,omitempty"`
    MaxIterations *int               `yaml:"max_iterations,omitempty" validate:"omitempty,min=1"`
    MCPServers    []string           `yaml:"mcp_servers,omitempty"`
    Synthesis     *SynthesisConfig   `yaml:"synthesis,omitempty"`
}

type TransportConfig struct {
    Type TransportType `yaml:"type" validate:"required"`
    // ... other fields
}

type LLMProviderConfig struct {
    Type                  LLMProviderType `yaml:"type" validate:"required"`
    Model                 string          `yaml:"model" validate:"required"`
    // ... other fields
}
```

**Benefits:**
- ✅ Compile-time type safety (no typos)
- ✅ IDE autocomplete for valid values
- ✅ Clear API documentation
- ✅ Easy to add helper methods (e.g., `IsParallel()`, `IsTerminal()`)
- ✅ YAML unmarshaling works naturally (Go's yaml package handles string-based enums)

**Implementation Note:**
- Go's `yaml` package automatically unmarshals string values to enum types
- Validation happens at runtime during YAML parsing
- Invalid enum values will cause clear unmarshaling errors

---

## Next Steps

Design phase complete ✅. Ready for implementation:

1. ~~Review questions document~~ ✅ **All questions decided** (`phase2-configuration-system-questions.md`)
2. **Implement configuration loader** ⬅️ NEXT
   - YAML parsing
   - Environment variable interpolation
   - Validation logic (fail-fast)
3. Implement in-memory registries
   - AgentRegistry (stores agent **config metadata**, not implementations)
   - ChainRegistry
   - MCPServerRegistry
   - LLMProviderRegistry
4. Create example configuration files
   - deploy/config/tarsy.yaml.example
   - deploy/config/llm-providers.yaml.example
   - deploy/config/.env.example
   - deploy/config/oauth2-proxy.cfg.template
5. Write comprehensive tests
   - Validation tests
   - Loading tests
   - Integration tests
6. Integrate with existing services

**Important**: Agent instantiation and execution logic (Agent Factory, BaseAgent, etc.) will be implemented in **Phase 3: Agent Framework**. Phase 2.2 only handles configuration storage and validation.
   - SessionService (chain lookup)
   - StageExecutor (agent/MCP resolution)
7. Document configuration system
   - Configuration reference
   - Examples and patterns

---

## References

- [YAML Specification](https://yaml.org/spec/1.2.2/)
- [Go Validator Library](https://github.com/go-playground/validator)
- Old TARSy Configuration: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/config/`
- Design Questions Document: `docs/phase2-configuration-system-questions.md`
