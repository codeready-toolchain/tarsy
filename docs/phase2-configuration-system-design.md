# Phase 2: Configuration System - Detailed Design

**Status**: ✅ Design Complete - All questions decided  
**Questions Document**: See `phase2-configuration-system-questions.md` for decision rationale  
**Last Updated**: 2026-02-04

## Overview

This document details the configuration system design for the new TARSy implementation. The configuration system manages agent definitions, chain configurations, MCP server registry, and LLM provider settings through YAML files with hierarchical resolution.

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
  - Agent definitions with custom instructions
  - Chain configurations (single/parallel/replica stages)
  - MCP server registry and transport configurations
  - System-wide defaults
  - Environment-agnostic (uses `${VAR}` placeholders for environment-specific values)
  - Users copy to `tarsy.yaml` and customize

- **`deploy/config/llm-providers.yaml.example`**: Example LLM provider configurations (tracked in git, ~50 lines)
  - API endpoints and authentication (via env vars: `${GEMINI_API_KEY}`)
  - Model parameters (can be overridden via env vars: `${LLM_TEMPERATURE:-0.7}`)
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
│     ${ENV_VAR} or $ENV_VAR syntax                        │
│     ${ENV_VAR:-default_value} with defaults              │
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
│  4. Build In-Memory Registries                          │
│     - AgentRegistry                                      │
│     - ChainRegistry                                      │
│     - MCPServerRegistry                                  │
│     - LLMProviderRegistry                                │
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
│  Chain Orchestrator                                      │
│  1. Get chain config from registry                       │
│  2. For each stage:                                      │
│     - Look up agent config from AgentRegistry            │
│     - Look up LLM provider from LLMProviderRegistry      │
│     - Look up MCP servers from MCPServerRegistry         │
│  3. Execute stage with resolved configuration            │
└─────────────────────────────────────────────────────────┘
```

---

## Configuration Files

### 1. Agent Definitions (`tarsy.yaml` - agents section)

Defines available agent types with their default configurations.

**Schema:**

```yaml
# deploy/config/tarsy.yaml

agents:
  - id: kubernetes-agent
    name: "Kubernetes Agent"
    description: "Analyzes Kubernetes cluster state and pod issues"
    
    # Iteration configuration
    iteration_strategy: react  # react | native_thinking
    max_iterations: 20
    
    # LLM configuration
    llm_provider: gemini-thinking  # Reference to llm-providers.yaml
    
    # MCP servers (default set, can be overridden per chain/alert)
    mcp_servers:
      - kubernetes-server
      - prometheus-server
    
    # Prompt configuration
    system_prompt: |
      You are a Kubernetes expert investigating cluster issues.
      Use the available tools to analyze pod states, logs, and metrics.
    
    # Tool selection preferences
    native_tools:
      google_search: true
      code_execution: false
      url_context: false
    
    # Stage-specific behavior (optional)
    stage_config:
      initial_analysis:
        max_iterations: 10
        focus: "Quick assessment of immediate issues"
      
      deep_dive:
        max_iterations: 20
        focus: "Comprehensive root cause analysis"
    
    # Metadata
    enabled: true
    version: "1.0"
    tags:
      - kubernetes
      - infrastructure

  - id: argocd-agent
    name: "ArgoCD Agent"
    description: "Analyzes ArgoCD application state and deployment issues"
    iteration_strategy: react
    max_iterations: 15
    llm_provider: gemini-thinking
    mcp_servers:
      - argocd-server
      - kubernetes-server
    system_prompt: |
      You are an ArgoCD expert investigating application deployment issues.
      Analyze application sync status, health, and recent changes.
    native_tools:
      google_search: true
      code_execution: false
    enabled: true
    version: "1.0"
    tags:
      - argocd
      - deployments

  - id: prometheus-agent
    name: "Prometheus Agent"
    description: "Analyzes metrics and time-series data"
    iteration_strategy: react
    max_iterations: 15
    llm_provider: gemini-thinking
    mcp_servers:
      - prometheus-server
    system_prompt: |
      You are a metrics analysis expert.
      Query Prometheus metrics to understand resource usage patterns and anomalies.
    native_tools:
      google_search: false
      code_execution: true  # For metric calculations
    enabled: true
    version: "1.0"
    tags:
      - metrics
      - observability

  - id: synthesis-agent
    name: "Synthesis Agent"
    description: "Synthesizes findings from multiple parallel agents"
    iteration_strategy: native_thinking
    max_iterations: 5
    llm_provider: gemini-thinking
    mcp_servers: []  # No MCP tools needed
    system_prompt: |
      You are a synthesis expert combining insights from multiple investigations.
      Create a coherent analysis from different perspectives.
    native_tools:
      google_search: false
      code_execution: false
    enabled: true
    version: "1.0"
    tags:
      - synthesis
      - aggregation

  - id: chat-agent
    name: "Chat Agent"
    description: "Handles follow-up questions about completed investigations"
    iteration_strategy: react
    max_iterations: 10
    llm_provider: gemini-thinking
    mcp_servers: []  # Inherits from session
    system_prompt: |
      You are helping a user understand a completed investigation.
      Answer questions clearly and refer to specific findings.
    native_tools:
      google_search: true
      code_execution: false
    enabled: true
    version: "1.0"
    tags:
      - chat
      - support
```

**Go Struct:**

```go
// pkg/config/agent.go

type AgentConfig struct {
    ID          string            `yaml:"id" validate:"required"`
    Name        string            `yaml:"name" validate:"required"`
    Description string            `yaml:"description"`
    
    // Iteration
    IterationStrategy string `yaml:"iteration_strategy" validate:"required,oneof=react native_thinking"`
    MaxIterations     int    `yaml:"max_iterations" validate:"required,min=1,max=50"`
    
    // LLM
    LLMProvider string `yaml:"llm_provider" validate:"required"`
    
    // MCP
    MCPServers []string `yaml:"mcp_servers"`
    
    // Prompts
    SystemPrompt string `yaml:"system_prompt" validate:"required"`
    
    // Native tools
    NativeTools struct {
        GoogleSearch  bool `yaml:"google_search"`
        CodeExecution bool `yaml:"code_execution"`
        URLContext    bool `yaml:"url_context"`
    } `yaml:"native_tools"`
    
    // Stage-specific config (optional)
    StageConfig map[string]struct {
        MaxIterations int    `yaml:"max_iterations"`
        Focus         string `yaml:"focus"`
    } `yaml:"stage_config,omitempty"`
    
    // Metadata
    Enabled bool     `yaml:"enabled"`
    Version string   `yaml:"version"`
    Tags    []string `yaml:"tags,omitempty"`
}

type AgentRegistry struct {
    agents map[string]*AgentConfig
    mu     sync.RWMutex
}

func (r *AgentRegistry) Get(id string) (*AgentConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    agent, exists := r.agents[id]
    if !exists {
        return nil, fmt.Errorf("agent not found: %s", id)
    }
    if !agent.Enabled {
        return nil, fmt.Errorf("agent disabled: %s", id)
    }
    return agent, nil
}
```

---

### 2. Chain Definitions (`tarsy.yaml` - chains section)

Defines multi-stage agent chains for different alert types.

**Schema:**

```yaml
# deploy/config/tarsy.yaml (chains section)

chains:
  - id: k8s-quick-analysis
    name: "Kubernetes Quick Analysis"
    description: "Fast single-agent analysis for routine K8s alerts"
    
    stages:
      - name: "Quick Assessment"
        index: 0
        agent: kubernetes-agent
        execution_mode: single  # single | parallel
    
    # Chat configuration
    chat:
      enabled: true
      agent: chat-agent
    
    # Metadata
    enabled: true
    version: "1.0"
    tags:
      - kubernetes
      - quick

  - id: k8s-deep-analysis
    name: "Kubernetes Deep Analysis"
    description: "Multi-stage comprehensive analysis with parallel investigation"
    
    stages:
      - name: "Initial Analysis"
        index: 0
        agent: kubernetes-agent
        execution_mode: single
        config:
          max_iterations: 10  # Override agent default
      
      - name: "Deep Dive"
        index: 1
        execution_mode: parallel
        parallel_config:
          type: multi_agent  # multi_agent | replica
          success_policy: all  # all | any
          agents:
            - agent: kubernetes-agent
              config:
                max_iterations: 20
            - agent: argocd-agent
            - agent: prometheus-agent
      
      - name: "Synthesis"
        index: 2
        agent: synthesis-agent
        execution_mode: single
    
    chat:
      enabled: true
      agent: chat-agent
    
    enabled: true
    version: "1.0"
    tags:
      - kubernetes
      - comprehensive

  - id: argocd-deployment-analysis
    name: "ArgoCD Deployment Analysis"
    description: "Analyzes ArgoCD application deployment failures"
    
    stages:
      - name: "Deployment Analysis"
        index: 0
        agent: argocd-agent
        execution_mode: single
      
      - name: "Cluster State Check"
        index: 1
        agent: kubernetes-agent
        execution_mode: single
    
    chat:
      enabled: true
      agent: chat-agent
    
    enabled: true
    version: "1.0"
    tags:
      - argocd
      - deployments

  - id: replica-comparison
    name: "Replica Comparison Analysis"
    description: "Runs same agent multiple times for comparison"
    
    stages:
      - name: "Parallel Analysis"
        index: 0
        execution_mode: parallel
        parallel_config:
          type: replica  # Same agent, different replicas
          success_policy: any  # At least one must succeed
          replica_count: 3
          agent: kubernetes-agent
      
      - name: "Synthesis"
        index: 1
        agent: synthesis-agent
        execution_mode: single
    
    chat:
      enabled: true
      agent: chat-agent
    
    enabled: true
    version: "1.0"
    tags:
      - experimental
      - comparison
```

**Go Struct:**

```go
// pkg/config/chain.go

type ChainConfig struct {
    ID          string       `yaml:"id" validate:"required"`
    Name        string       `yaml:"name" validate:"required"`
    Description string       `yaml:"description"`
    Stages      []StageConfig `yaml:"stages" validate:"required,dive"`
    Chat        *ChatConfig  `yaml:"chat,omitempty"`
    Enabled     bool         `yaml:"enabled"`
    Version     string       `yaml:"version"`
    Tags        []string     `yaml:"tags,omitempty"`
}

type StageConfig struct {
    Name          string         `yaml:"name" validate:"required"`
    Index         int            `yaml:"index" validate:"min=0"`
    Agent         string         `yaml:"agent,omitempty"` // For single mode
    ExecutionMode string         `yaml:"execution_mode" validate:"required,oneof=single parallel"`
    ParallelConfig *ParallelConfig `yaml:"parallel_config,omitempty"`
    Config        map[string]interface{} `yaml:"config,omitempty"` // Agent overrides
}

type ParallelConfig struct {
    Type          string `yaml:"type" validate:"required,oneof=multi_agent replica"`
    SuccessPolicy string `yaml:"success_policy" validate:"required,oneof=all any"`
    
    // For multi_agent type
    Agents []ParallelAgentConfig `yaml:"agents,omitempty"`
    
    // For replica type
    ReplicaCount int    `yaml:"replica_count,omitempty" validate:"omitempty,min=2,max=10"`
    Agent        string `yaml:"agent,omitempty"`
}

type ParallelAgentConfig struct {
    Agent  string                 `yaml:"agent" validate:"required"`
    Config map[string]interface{} `yaml:"config,omitempty"`
}

type ChatConfig struct {
    Enabled bool   `yaml:"enabled"`
    Agent   string `yaml:"agent" validate:"required"`
}

type ChainRegistry struct {
    chains map[string]*ChainConfig
    mu     sync.RWMutex
}

func (r *ChainRegistry) Get(id string) (*ChainConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    chain, exists := r.chains[id]
    if !exists {
        return nil, fmt.Errorf("chain not found: %s", id)
    }
    if !chain.Enabled {
        return nil, fmt.Errorf("chain disabled: %s", id)
    }
    return chain, nil
}
```

---

### 3. MCP Server Registry (`tarsy.yaml` - mcp_servers section)

Defines available MCP servers and their configurations.

**Schema:**

```yaml
# deploy/config/tarsy.yaml (mcp_servers section)

mcp_servers:
  - id: kubernetes-server
    name: "Kubernetes MCP Server"
    description: "Provides kubectl and K8s API access"
    
    # Transport configuration
    transport:
      type: stdio  # stdio | http | sse
      command: /usr/local/bin/mcp-kubernetes
      args:
        - --kubeconfig
        - ${KUBECONFIG}
        - --namespace
        - ${K8S_NAMESPACE:-default}
      env:
        KUBECONFIG: ${KUBECONFIG}
        LOG_LEVEL: ${MCP_LOG_LEVEL:-info}
    
    # Available tools (optional - can be discovered at runtime)
    tools:
      - kubectl-get
      - kubectl-describe
      - kubectl-logs
      - kubectl-events
      - k8s-api-query
    
    # Health check
    health_check:
      enabled: true
      interval: 30s
      timeout: 5s
    
    # Timeouts
    timeout:
      startup: 10s
      tool_call: 60s
    
    # Metadata
    enabled: true
    version: "1.0"
    tags:
      - kubernetes
      - infrastructure

  - id: argocd-server
    name: "ArgoCD MCP Server"
    description: "Provides ArgoCD CLI and API access"
    
    transport:
      type: stdio
      command: /usr/local/bin/mcp-argocd
      args:
        - --server
        - ${ARGOCD_SERVER}
      env:
        ARGOCD_SERVER: ${ARGOCD_SERVER}
        ARGOCD_AUTH_TOKEN: ${ARGOCD_AUTH_TOKEN}
    
    tools:
      - argocd-app-get
      - argocd-app-history
      - argocd-app-manifests
      - argocd-app-diff
    
    health_check:
      enabled: true
      interval: 30s
      timeout: 5s
    
    timeout:
      startup: 10s
      tool_call: 60s
    
    enabled: true
    version: "1.0"
    tags:
      - argocd
      - gitops

  - id: prometheus-server
    name: "Prometheus MCP Server"
    description: "Queries Prometheus metrics"
    
    transport:
      type: http
      url: ${PROMETHEUS_SERVER}/mcp
      headers:
        Authorization: "Bearer ${PROMETHEUS_TOKEN}"
    
    tools:
      - prometheus-query
      - prometheus-query-range
      - prometheus-series
      - prometheus-labels
    
    health_check:
      enabled: true
      interval: 60s
      timeout: 10s
    
    timeout:
      startup: 5s
      tool_call: 30s
    
    enabled: true
    version: "1.0"
    tags:
      - metrics
      - observability

  - id: github-server
    name: "GitHub MCP Server"
    description: "Fetches runbooks and documentation from GitHub"
    
    transport:
      type: sse
      url: ${GITHUB_MCP_SERVER}
      headers:
        Authorization: "token ${GITHUB_TOKEN}"
    
    tools:
      - github-get-file
      - github-search-code
      - github-list-issues
    
    health_check:
      enabled: true
      interval: 60s
      timeout: 10s
    
    timeout:
      startup: 5s
      tool_call: 30s
    
    enabled: true
    version: "1.0"
    tags:
      - github
      - documentation
```

**Go Struct:**

```go
// pkg/config/mcp.go

type MCPServerConfig struct {
    ID          string            `yaml:"id" validate:"required"`
    Name        string            `yaml:"name" validate:"required"`
    Description string            `yaml:"description"`
    Transport   TransportConfig   `yaml:"transport" validate:"required"`
    Tools       []string          `yaml:"tools,omitempty"`
    HealthCheck *HealthCheckConfig `yaml:"health_check,omitempty"`
    Timeout     TimeoutConfig     `yaml:"timeout"`
    Enabled     bool              `yaml:"enabled"`
    Version     string            `yaml:"version"`
    Tags        []string          `yaml:"tags,omitempty"`
}

type TransportConfig struct {
    Type    string            `yaml:"type" validate:"required,oneof=stdio http sse"`
    
    // For stdio
    Command string   `yaml:"command,omitempty"`
    Args    []string `yaml:"args,omitempty"`
    Env     map[string]string `yaml:"env,omitempty"`
    
    // For http/sse
    URL     string            `yaml:"url,omitempty"`
    Headers map[string]string `yaml:"headers,omitempty"`
}

type HealthCheckConfig struct {
    Enabled  bool          `yaml:"enabled"`
    Interval time.Duration `yaml:"interval"`
    Timeout  time.Duration `yaml:"timeout"`
}

type TimeoutConfig struct {
    Startup  time.Duration `yaml:"startup"`
    ToolCall time.Duration `yaml:"tool_call"`
}

type MCPServerRegistry struct {
    servers map[string]*MCPServerConfig
    mu      sync.RWMutex
}

func (r *MCPServerRegistry) Get(id string) (*MCPServerConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    server, exists := r.servers[id]
    if !exists {
        return nil, fmt.Errorf("MCP server not found: %s", id)
    }
    if !server.Enabled {
        return nil, fmt.Errorf("MCP server disabled: %s", id)
    }
    return server, nil
}
```

---

### 4. LLM Provider Configuration (`llm-providers.yaml`)

Defines LLM providers and their configurations.

**Schema:**

```yaml
# deploy/config/llm-providers.yaml

llm_providers:
  - id: gemini-thinking
    name: "Google Gemini 2.0 Flash Thinking"
    description: "Gemini with native thinking mode"
    
    provider: google  # google | openai | anthropic | xai
    model: gemini-2.0-flash-thinking-exp-1219
    
    # API configuration
    api:
      endpoint: ${GEMINI_API_ENDPOINT:-https://generativelanguage.googleapis.com/v1beta}
      api_key: ${GEMINI_API_KEY}
      project_id: ${GOOGLE_CLOUD_PROJECT}
    
    # Model parameters
    parameters:
      temperature: 0.7
      max_output_tokens: 8192
      top_p: 0.95
      top_k: 40
    
    # Native tools support
    native_tools:
      google_search:
        enabled: true
        grounding_threshold: 0.3
      code_execution:
        enabled: true
        timeout: 30s
      url_context:
        enabled: false
    
    # Rate limiting
    rate_limit:
      requests_per_minute: 60
      tokens_per_minute: 100000
    
    # Timeouts
    timeout:
      request: 120s
      streaming: 300s
    
    # Retry configuration
    retry:
      max_attempts: 3
      initial_delay: 1s
      max_delay: 10s
      multiplier: 2
    
    # Metadata
    enabled: true
    version: "1.0"
    tags:
      - gemini
      - thinking
      - production

  - id: gemini-standard
    name: "Google Gemini 2.0 Flash"
    description: "Standard Gemini without thinking mode"
    
    provider: google
    model: gemini-2.0-flash-exp
    
    api:
      endpoint: ${GEMINI_API_ENDPOINT:-https://generativelanguage.googleapis.com/v1beta}
      api_key: ${GEMINI_API_KEY}
      project_id: ${GOOGLE_CLOUD_PROJECT}
    
    parameters:
      temperature: 0.7
      max_output_tokens: 8192
      top_p: 0.95
    
    native_tools:
      google_search:
        enabled: true
      code_execution:
        enabled: true
      url_context:
        enabled: false
    
    rate_limit:
      requests_per_minute: 60
      tokens_per_minute: 100000
    
    timeout:
      request: 120s
      streaming: 300s
    
    retry:
      max_attempts: 3
      initial_delay: 1s
      max_delay: 10s
      multiplier: 2
    
    enabled: true
    version: "1.0"
    tags:
      - gemini
      - standard

  - id: openai-gpt4
    name: "OpenAI GPT-4"
    description: "OpenAI GPT-4 for complex analysis"
    
    provider: openai
    model: gpt-4-turbo-preview
    
    api:
      endpoint: ${OPENAI_API_ENDPOINT:-https://api.openai.com/v1}
      api_key: ${OPENAI_API_KEY}
    
    parameters:
      temperature: 0.7
      max_tokens: 4096
      top_p: 0.95
    
    native_tools:
      google_search:
        enabled: false  # Not supported by OpenAI
      code_execution:
        enabled: false
    
    rate_limit:
      requests_per_minute: 100
      tokens_per_minute: 150000
    
    timeout:
      request: 120s
      streaming: 300s
    
    retry:
      max_attempts: 3
      initial_delay: 1s
      max_delay: 10s
      multiplier: 2
    
    enabled: false  # Not used by default
    version: "1.0"
    tags:
      - openai
      - gpt4
```

**Go Struct:**

```go
// pkg/config/llm.go

type LLMProviderConfig struct {
    ID          string              `yaml:"id" validate:"required"`
    Name        string              `yaml:"name" validate:"required"`
    Description string              `yaml:"description"`
    Provider    string              `yaml:"provider" validate:"required,oneof=google openai anthropic xai"`
    Model       string              `yaml:"model" validate:"required"`
    API         APIConfig           `yaml:"api" validate:"required"`
    Parameters  ModelParameters     `yaml:"parameters"`
    NativeTools NativeToolsConfig   `yaml:"native_tools"`
    RateLimit   *RateLimitConfig    `yaml:"rate_limit,omitempty"`
    Timeout     TimeoutConfig       `yaml:"timeout"`
    Retry       RetryConfig         `yaml:"retry"`
    Enabled     bool                `yaml:"enabled"`
    Version     string              `yaml:"version"`
    Tags        []string            `yaml:"tags,omitempty"`
}

type APIConfig struct {
    Endpoint  string `yaml:"endpoint" validate:"required,url"`
    APIKey    string `yaml:"api_key" validate:"required"`
    ProjectID string `yaml:"project_id,omitempty"`
}

type ModelParameters struct {
    Temperature      float64 `yaml:"temperature" validate:"min=0,max=2"`
    MaxOutputTokens  int     `yaml:"max_output_tokens,omitempty" validate:"omitempty,min=1"`
    MaxTokens        int     `yaml:"max_tokens,omitempty" validate:"omitempty,min=1"`
    TopP             float64 `yaml:"top_p,omitempty" validate:"omitempty,min=0,max=1"`
    TopK             int     `yaml:"top_k,omitempty" validate:"omitempty,min=1"`
}

type NativeToolsConfig struct {
    GoogleSearch struct {
        Enabled            bool    `yaml:"enabled"`
        GroundingThreshold float64 `yaml:"grounding_threshold,omitempty"`
    } `yaml:"google_search"`
    
    CodeExecution struct {
        Enabled bool          `yaml:"enabled"`
        Timeout time.Duration `yaml:"timeout,omitempty"`
    } `yaml:"code_execution"`
    
    URLContext struct {
        Enabled bool `yaml:"enabled"`
    } `yaml:"url_context"`
}

type RateLimitConfig struct {
    RequestsPerMinute int `yaml:"requests_per_minute"`
    TokensPerMinute   int `yaml:"tokens_per_minute"`
}

type RetryConfig struct {
    MaxAttempts  int           `yaml:"max_attempts" validate:"min=1,max=10"`
    InitialDelay time.Duration `yaml:"initial_delay"`
    MaxDelay     time.Duration `yaml:"max_delay"`
    Multiplier   float64       `yaml:"multiplier"`
}

type LLMProviderRegistry struct {
    providers map[string]*LLMProviderConfig
    mu        sync.RWMutex
}

func (r *LLMProviderRegistry) Get(id string) (*LLMProviderConfig, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    
    provider, exists := r.providers[id]
    if !exists {
        return nil, fmt.Errorf("LLM provider not found: %s", id)
    }
    if !provider.Enabled {
        return nil, fmt.Errorf("LLM provider disabled: %s", id)
    }
    return provider, nil
}
```

---

### 5. System Defaults (`tarsy.yaml` - defaults section)

System-wide default configurations.

**Schema:**

```yaml
# deploy/config/tarsy.yaml (defaults section)

defaults:
  # Session defaults
  session:
    timeout: 30m
    max_stages: 10
    max_iterations_per_agent: 20
  
  # LLM defaults
  llm:
    default_provider: gemini-thinking
    temperature: 0.7
    max_retries: 3
    streaming_enabled: true
  
  # MCP defaults
  mcp:
    tool_call_timeout: 60s
    health_check_interval: 30s
    max_concurrent_tools: 5
  
  # Worker defaults
  worker:
    poll_interval: 2s
    max_concurrent_sessions: 3
    orphan_timeout: 10m
  
  # Database defaults
  database:
    max_open_connections: 25
    max_idle_connections: 10
    connection_max_lifetime: 1h
    connection_max_idle_time: 15m
  
  # Event retention
  events:
    cleanup_on_completion: true
    ttl_fallback: 7d
  
  # Soft delete retention
  retention:
    soft_delete_after: 90d
```

**Go Struct:**

```go
// pkg/config/defaults.go

type Defaults struct {
    Session  SessionDefaults  `yaml:"session"`
    LLM      LLMDefaults      `yaml:"llm"`
    MCP      MCPDefaults      `yaml:"mcp"`
    Worker   WorkerDefaults   `yaml:"worker"`
    Database DatabaseDefaults `yaml:"database"`
    Events   EventsDefaults   `yaml:"events"`
    Retention RetentionDefaults `yaml:"retention"`
}

type SessionDefaults struct {
    Timeout              time.Duration `yaml:"timeout"`
    MaxStages            int           `yaml:"max_stages"`
    MaxIterationsPerAgent int          `yaml:"max_iterations_per_agent"`
}

type LLMDefaults struct {
    DefaultProvider   string  `yaml:"default_provider"`
    Temperature       float64 `yaml:"temperature"`
    MaxRetries        int     `yaml:"max_retries"`
    StreamingEnabled  bool    `yaml:"streaming_enabled"`
}

type MCPDefaults struct {
    ToolCallTimeout      time.Duration `yaml:"tool_call_timeout"`
    HealthCheckInterval  time.Duration `yaml:"health_check_interval"`
    MaxConcurrentTools   int           `yaml:"max_concurrent_tools"`
}

type WorkerDefaults struct {
    PollInterval         time.Duration `yaml:"poll_interval"`
    MaxConcurrentSessions int          `yaml:"max_concurrent_sessions"`
    OrphanTimeout        time.Duration `yaml:"orphan_timeout"`
}

type DatabaseDefaults struct {
    MaxOpenConnections    int           `yaml:"max_open_connections"`
    MaxIdleConnections    int           `yaml:"max_idle_connections"`
    ConnectionMaxLifetime time.Duration `yaml:"connection_max_lifetime"`
    ConnectionMaxIdleTime time.Duration `yaml:"connection_max_idle_time"`
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

### Resolution Order

1. **System Defaults** (tarsy.yaml - defaults section) - Base configuration
2. **Component Configuration** (tarsy.yaml - agents/chains/mcp_servers sections + llm-providers.yaml)
3. **Environment Override** (optional) - `environments/{env}.yaml`
4. **Environment Variables** - Interpolated values (highest priority)
5. **Per-Alert Override** - MCP selection via API (runtime only)

### Configuration Override Example

```yaml
# tarsy.yaml - defaults section
defaults:
  llm:
    temperature: 0.7

# tarsy.yaml - agents section
agents:
  - id: kubernetes-agent
    llm_provider: gemini-thinking
    # Uses default temperature: 0.7

# tarsy.yaml - chains section (override at stage level)
chains:
  - id: k8s-deep-analysis
    stages:
      - name: "Initial Analysis"
        agent: kubernetes-agent
        config:
          temperature: 0.5  # Override for this stage

# API request (runtime override)
POST /api/sessions
{
  "chain_id": "k8s-deep-analysis",
  "mcp": {
    "servers": [
      {"name": "kubernetes-server", "tools": ["kubectl-get"]}
    ]
  }
}
```

### Environment Variable Interpolation

**Syntax:**
- `${VAR_NAME}` - Required variable (fails if not set)
- `${VAR_NAME:-default}` - Optional with default value

**Examples:**

```yaml
# Required environment variable
api_key: ${GEMINI_API_KEY}

# Optional with default
endpoint: ${GEMINI_API_ENDPOINT:-https://generativelanguage.googleapis.com/v1beta}

# Nested in arrays
args:
  - --kubeconfig
  - ${KUBECONFIG}
  - --namespace
  - ${K8S_NAMESPACE:-default}
```

**Implementation:**

```go
// pkg/config/interpolation.go

func InterpolateEnvVars(data []byte) ([]byte, error) {
    result := string(data)
    
    // Pattern: ${VAR_NAME} or ${VAR_NAME:-default}
    re := regexp.MustCompile(`\$\{([^:}]+)(?::-([^}]+))?\}`)
    
    matches := re.FindAllStringSubmatch(result, -1)
    for _, match := range matches {
        fullMatch := match[0]
        varName := match[1]
        defaultValue := match[2]
        
        // Try to get from environment
        value := os.Getenv(varName)
        
        if value == "" {
            if defaultValue != "" {
                // Use default
                value = defaultValue
            } else {
                // Required but not set
                return nil, fmt.Errorf("required environment variable not set: %s", varName)
            }
        }
        
        result = strings.ReplaceAll(result, fullMatch, value)
    }
    
    return []byte(result), nil
}
```

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
    agents       *AgentRegistry
    chains       *ChainRegistry
    mcpServers   *MCPServerRegistry
    llmProviders *LLMProviderRegistry
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
    for _, chain := range v.chains.GetAll() {
        // Validate stage indices are sequential
        for i, stage := range chain.Stages {
            if stage.Index != i {
                return fmt.Errorf("chain %s: stage indices not sequential", chain.ID)
            }
            
            // Validate agent references
            if stage.ExecutionMode == "single" {
                if _, err := v.agents.Get(stage.Agent); err != nil {
                    return fmt.Errorf("chain %s stage %d: %w", chain.ID, i, err)
                }
            } else if stage.ExecutionMode == "parallel" {
                if stage.ParallelConfig == nil {
                    return fmt.Errorf("chain %s stage %d: parallel_config required for parallel mode", chain.ID, i)
                }
                
                // Validate parallel agents
                if stage.ParallelConfig.Type == "multi_agent" {
                    for _, pa := range stage.ParallelConfig.Agents {
                        if _, err := v.agents.Get(pa.Agent); err != nil {
                            return fmt.Errorf("chain %s stage %d: %w", chain.ID, i, err)
                        }
                    }
                } else if stage.ParallelConfig.Type == "replica" {
                    if _, err := v.agents.Get(stage.ParallelConfig.Agent); err != nil {
                        return fmt.Errorf("chain %s stage %d: %w", chain.ID, i, err)
                    }
                }
            }
        }
        
        // Validate chat agent
        if chain.Chat != nil && chain.Chat.Enabled {
            if _, err := v.agents.Get(chain.Chat.Agent); err != nil {
                return fmt.Errorf("chain %s: chat agent %w", chain.ID, err)
            }
        }
    }
    
    return nil
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
          endpoint: ${GEMINI_API_ENDPOINT}
          api_key: ${GEMINI_API_KEY}
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
  string provider = 1;           // "google", "openai", "anthropic"
  string model = 2;              // "gemini-2.0-flash-thinking-exp-1219"
  string api_key = 3;            // From environment variable (resolved in Go)
  string endpoint = 4;           // API endpoint
  float temperature = 5;
  int32 max_tokens = 6;
  // ... other LLM parameters
}

message GenerateRequest {
  LLMConfig llm_config = 1;      // Go passes config per request
  repeated Message messages = 2;
  repeated Tool tools = 3;
  // ... other request parameters
}
```

```go
// Go: Load config and pass to Python
func (s *StageExecutor) executeLLMRequest(ctx context.Context, stage *ent.Stage) error {
    // 1. Resolve LLM provider from registry (loaded from config files)
    llmProvider, err := s.config.LLMProviders.Get(stage.LLMProviderID)
    if err != nil {
        return err
    }
    
    // 2. Build gRPC request with LLM config
    req := &pb.GenerateRequest{
        LlmConfig: &pb.LLMConfig{
            Provider:    llmProvider.Provider,
            Model:       llmProvider.Model,
            ApiKey:      os.Getenv(llmProvider.API.APIKeyEnv),  // Resolve from env
            Endpoint:    llmProvider.API.Endpoint,
            Temperature: llmProvider.Parameters.Temperature,
            MaxTokens:   llmProvider.Parameters.MaxOutputTokens,
        },
        Messages: convertMessages(stage.Messages),
        Tools:    convertTools(stage.Tools),
    }
    
    // 3. Call Python LLM service
    stream, err := s.llmClient.Generate(ctx, req)
    // ...
}
```

```python
# Python: Receive config from Go, no file reading
def Generate(self, request, context):
    # Configuration comes from Go via gRPC request
    llm_config = request.llm_config
    
    # Initialize LLM client with provided config
    if llm_config.provider == "google":
        client = genai.Client(
            api_key=llm_config.api_key,
            http_options={'api_version': 'v1beta'}
        )
        model_name = llm_config.model
    elif llm_config.provider == "openai":
        client = openai.OpenAI(api_key=llm_config.api_key)
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
    
    // 0. Parse command-line flags
    configDir := flag.String("config-dir", 
        getEnv("CONFIG_DIR", "./deploy/config"), 
        "Path to configuration directory")
    flag.Parse()
    
    log.Info("Loading configuration", "config_dir", *configDir)
    
    // 1. Load configuration from filesystem
    cfg, err := config.Load(ctx, config.LoadOptions{
        ConfigDir: *configDir,
    })
    if err != nil {
        log.Fatal("Failed to load configuration", "error", err)
    }
    
    // 2. Initialize registries
    registries := &config.Registries{
        Agents:       cfg.AgentRegistry,
        Chains:       cfg.ChainRegistry,
        MCPServers:   cfg.MCPServerRegistry,
        LLMProviders: cfg.LLMProviderRegistry,
    }
    
    // 3. Validate configuration
    validator := config.NewValidator(registries)
    if err := validator.ValidateAll(); err != nil {
        log.Fatal("Configuration validation failed", "error", err)
    }
    
    log.Info("Configuration loaded successfully",
        "agents", len(registries.Agents.GetAll()),
        "chains", len(registries.Chains.GetAll()),
        "mcp_servers", len(registries.MCPServers.GetAll()),
        "llm_providers", len(registries.LLMProviders.GetAll()),
    )
    
    // 4. Continue with service initialization...
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

type LoadOptions struct {
    ConfigDir string  // Path to configuration directory (required)
}

type Config struct {
    Defaults         *Defaults
    AgentRegistry    *AgentRegistry
    ChainRegistry    *ChainRegistry
    MCPServerRegistry *MCPServerRegistry
    LLMProviderRegistry *LLMProviderRegistry
}

func Load(ctx context.Context, opts LoadOptions) (*Config, error) {
    loader := &configLoader{
        configDir: opts.ConfigDir,
        env:       opts.Environment,
    }
    
    // Load defaults
    defaults, err := loader.loadDefaults()
    if err != nil {
        return nil, fmt.Errorf("failed to load defaults: %w", err)
    }
    
    // Load agents
    agents, err := loader.loadAgents()
    if err != nil {
        return nil, fmt.Errorf("failed to load agents: %w", err)
    }
    
    // Load chains
    chains, err := loader.loadChains()
    if err != nil {
        return nil, fmt.Errorf("failed to load chains: %w", err)
    }
    
    // Load MCP servers
    mcpServers, err := loader.loadMCPServers()
    if err != nil {
        return nil, fmt.Errorf("failed to load MCP servers: %w", err)
    }
    
    // Load LLM providers
    llmProviders, err := loader.loadLLMProviders()
    if err != nil {
        return nil, fmt.Errorf("failed to load LLM providers: %w", err)
    }
    
    return &Config{
        Defaults:            defaults,
        AgentRegistry:       agents,
        ChainRegistry:       chains,
        MCPServerRegistry:   mcpServers,
        LLMProviderRegistry: llmProviders,
    }, nil
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
    
    // Interpolate environment variables
    data, err = InterpolateEnvVars(data)
    if err != nil {
        return fmt.Errorf("failed to interpolate env vars in %s: %w", filename, err)
    }
    
    // Parse YAML
    if err := yaml.Unmarshal(data, target); err != nil {
        return fmt.Errorf("failed to parse %s: %w", filename, err)
    }
    
    return nil
}

func (l *configLoader) loadAgents() (*AgentRegistry, error) {
    var data struct {
        Agents []AgentConfig `yaml:"agents"`
    }
    
    if err := l.loadYAML("tarsy.yaml", &data); err != nil {
        return nil, err
    }
    
    registry := NewAgentRegistry()
    for _, agent := range data.Agents {
        if err := registry.Register(&agent); err != nil {
            return nil, fmt.Errorf("failed to register agent %s: %w", agent.ID, err)
        }
    }
    
    return registry, nil
}

// Similar implementations for chains, MCP servers, LLM providers...
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
                ID: "test-chain",
                Stages: []StageConfig{
                    {
                        Name:          "Stage 1",
                        Index:         0,
                        Agent:         "kubernetes-agent",
                        ExecutionMode: "single",
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "invalid agent reference",
            chain: ChainConfig{
                ID: "test-chain",
                Stages: []StageConfig{
                    {
                        Name:          "Stage 1",
                        Index:         0,
                        Agent:         "nonexistent-agent",
                        ExecutionMode: "single",
                    },
                },
            },
            wantErr: true,
            errMsg:  "agent not found",
        },
        {
            name: "non-sequential stage indices",
            chain: ChainConfig{
                ID: "test-chain",
                Stages: []StageConfig{
                    {Name: "Stage 1", Index: 0, Agent: "kubernetes-agent", ExecutionMode: "single"},
                    {Name: "Stage 2", Index: 2, Agent: "kubernetes-agent", ExecutionMode: "single"},
                },
            },
            wantErr: true,
            errMsg:  "stage indices not sequential",
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
// pkg/config/interpolation_test.go

func TestInterpolateEnvVars(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        env     map[string]string
        want    string
        wantErr bool
    }{
        {
            name:  "simple substitution",
            input: "api_key: ${API_KEY}",
            env:   map[string]string{"API_KEY": "secret123"},
            want:  "api_key: secret123",
        },
        {
            name:  "substitution with default",
            input: "endpoint: ${ENDPOINT:-https://default.com}",
            env:   map[string]string{},
            want:  "endpoint: https://default.com",
        },
        {
            name:  "override default with env var",
            input: "endpoint: ${ENDPOINT:-https://default.com}",
            env:   map[string]string{"ENDPOINT": "https://custom.com"},
            want:  "endpoint: https://custom.com",
        },
        {
            name:    "required var missing",
            input:   "api_key: ${API_KEY}",
            env:     map[string]string{},
            wantErr: true,
        },
        {
            name:  "multiple substitutions",
            input: "url: ${PROTOCOL}://${HOST}:${PORT}",
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
                os.Setenv(k, v)
                defer os.Unsetenv(k)
            }
            
            result, err := InterpolateEnvVars([]byte(tt.input))
            
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                assert.Equal(t, tt.want, string(result))
            }
        })
    }
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
    os.Setenv("GEMINI_API_KEY", "test-key")
    defer os.Unsetenv("GEMINI_API_KEY")
    
    // Load configuration
    cfg, err := config.Load(context.Background(), config.LoadOptions{
        ConfigDir: tempDir,
    })
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
    
    // Validate cross-references
    validator := config.NewValidator(&config.Registries{
        Agents:       cfg.AgentRegistry,
        Chains:       cfg.ChainRegistry,
        MCPServers:   cfg.MCPServerRegistry,
        LLMProviders: cfg.LLMProviderRegistry,
    })
    err = validator.ValidateAll()
    assert.NoError(t, err)
}
```

---

## Implementation Checklist

### Phase 2.2: Configuration System
- [ ] Define YAML schemas for all configuration files
  - [ ] tarsy.yaml schema (agents, chains, mcp_servers, defaults sections)
  - [ ] llm-providers.yaml schema
- [ ] Implement Go structs with validation tags
  - [ ] AgentConfig
  - [ ] ChainConfig
  - [ ] MCPServerConfig
  - [ ] LLMProviderConfig
  - [ ] Defaults
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

**File-Based Configuration**: Configuration stored in YAML files (not database) for version control, code review, and deployment simplicity.

**In-Memory Registries**: Configuration loaded once at startup into in-memory registries. Configuration changes require service restart (no hot-reload support).

**Strong Validation**: Comprehensive validation on startup with clear error messages. Fail fast if configuration invalid.

**Environment Variable Interpolation**: Supports `${VAR}` and `${VAR:-default}` syntax for secrets and environment-specific values.

**Environment-Agnostic YAML**: YAML config files work across all environments (local dev, podman, k8s). Environment differences handled via .env files only.

**OAuth2 Proxy Configuration**: Template-based OAuth2 proxy config (same as old TARSy). Template tracked in git, generated file gitignored.

**Configurable Config Directory**: Go binary accepts `--config-dir` flag or `CONFIG_DIR` env var for flexible config file location. Enables different deployment strategies (host filesystem, container mounts, ConfigMaps).

**Python Configuration via gRPC**: Python LLM service receives configuration from Go via gRPC requests (not from files or env vars). Centralizes configuration in Go, simplifies Python service, ensures single source of truth.

**Per-Alert Overrides**: MCP selection can be overridden per alert via API (stored in database, not config files).

---

## Decided Against

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
  - Agents: Agent definitions with iteration strategies, LLM providers, MCP servers
  - Chains: Multi-stage agent chains with single/parallel execution
  - MCP Servers: MCP server registry with transport configurations
  - Defaults: System-wide defaults
- **llm-providers.yaml**: LLM provider configurations (~50 lines)
- **.env**: Environment-specific values and secrets (gitignored)
- **oauth2-proxy.cfg.template**: OAuth2 proxy template (generates oauth2-proxy.cfg)

### ✅ Key Features
- **YAML-based**: Human-readable, easy to edit, version controlled
- **Environment variable interpolation**: `${VAR}` and `${VAR:-default}` support
- **Environment-agnostic**: Same YAML works across all environments (local, podman, k8s, prod)
- **12-factor app compliance**: Environment-specific config via environment variables
- **Startup validation**: Fail-fast validation on startup with clear error messages
- **File-based only**: No configuration API, changes via git + restart
- **In-memory registries**: Fast lookups, type-safe access
- **Per-alert overrides**: MCP server selection via API (runtime only)
- **OAuth2 integration**: Template-based OAuth2 proxy configuration
- **Simple examples**: `.example` suffixed files for easy setup

### ✅ Go Implementation
- **Type-safe structs**: Go structs with validation tags
- **Registry pattern**: Thread-safe registries for each config type
- **Clear validation**: Detailed error messages for misconfigurations
- **Environment integration**: Seamless environment variable substitution

---

## Next Steps

Design phase complete ✅. Ready for implementation:

1. ~~Review questions document~~ ✅ **All questions decided** (`phase2-configuration-system-questions.md`)
2. **Implement configuration loader** ⬅️ NEXT
   - YAML parsing
   - Environment variable interpolation
   - Validation logic (fail-fast)
3. Implement in-memory registries
   - AgentRegistry
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
