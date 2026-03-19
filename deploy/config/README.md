# TARSy Configuration

This directory contains configuration files for TARSy's configuration system.

## Quick Start

1. **Copy example files** to create your configuration:

```bash
cd deploy/config
cp tarsy.yaml.example tarsy.yaml
cp llm-providers.yaml.example llm-providers.yaml
cp .env.example .env
```

2. **Edit `.env`** with your actual values:
   - API keys (Google, OpenAI, Anthropic, xAI)
   - Database credentials
   - Service ports
   - Kubeconfig path

3. **Customize `tarsy.yaml`** (optional):
   - Add custom agents
   - Define custom chains
   - Override built-in configurations
   - Add custom MCP servers
   - Add agent skills (see [Agent Skills](#agent-skills) below)

4. **Customize `llm-providers.yaml`** (optional):
   - Add additional LLM providers
   - Override built-in providers
   - Configure custom endpoints

5. **Start TARSy**:

```bash
cd ../..  # Back to project root
go run cmd/tarsy/main.go
```

## File Descriptions

### Configuration Files (User-Created)

These files are **gitignored** and contain your actual configuration:

- **`tarsy.yaml`** - Main configuration (agents, chains, MCP servers, defaults)
- **`llm-providers.yaml`** - LLM provider configurations
- **`skills/`** - Agent skill definitions (see [Agent Skills](#agent-skills))
- **`.env`** - Environment variables and secrets
- **`oauth2-proxy.cfg`** - Generated OAuth2 proxy configuration (if using auth)

### Example Files (Tracked in Git)

These files are **tracked in git** and serve as templates:

- **`tarsy.yaml.example`** - Example main configuration with comments
- **`llm-providers.yaml.example`** - Example LLM provider configurations
- **`.env.example`** - Example environment variables
- **`oauth2-proxy.cfg.template`** - OAuth2 proxy template (uses `{{VAR}}` placeholders)
- **`README.md`** - This file

## Configuration File Format

### tarsy.yaml

Main configuration file containing:

- **`defaults:`** - System-wide default values
- **`mcp_servers:`** - MCP server configurations
- **`agents:`** - Custom agent definitions (or overrides), including optional `skills` and `required_skills`
- **`agent_chains:`** - Multi-stage agent chain definitions

```yaml
defaults:
  llm_provider: "google-default"
  max_iterations: 20
  fallback_providers:
    - provider: "gemini-3.1-pro"
      backend: "google-native"

mcp_servers:
  kubernetes-server:
    transport:
      type: "stdio"
      command: "npx"
      args: ["kubernetes-mcp-server"]

agents:
  custom-agent:
    mcp_servers: ["kubernetes-server"]
    custom_instructions: "..."

agent_chains:
  my-chain:
    alert_types: ["MyAlert"]
    stages:
      - name: "investigation"
        agents:
          - name: "custom-agent"
```

### llm-providers.yaml

LLM provider configurations:

```yaml
llm_providers:
  gemini-2.5-flash:
    type: google
    model: gemini-2.5-flash
    api_key_env: GOOGLE_API_KEY
    max_tool_result_tokens: 950000
    native_tools:
      google_search: true
```

### .env

Environment variables:

```bash
# LLM API Keys
GOOGLE_API_KEY=your-api-key

# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=tarsy
DB_PASSWORD=password
DB_NAME=tarsy

# Service
HTTP_PORT=8080
GRPC_ADDR=localhost:50051
```

## Environment Variable Interpolation

Both `tarsy.yaml` and `llm-providers.yaml` support environment variable interpolation using Go templates:

- **Syntax**: `{{.VAR_NAME}}` (Go template syntax)
- **Example**: `api_key: {{.GOOGLE_API_KEY}}`
- **Important**: Literal `$` characters (passwords, regexes, etc.) are preserved as-is
- **Missing variables**: Expand to empty string (validation will catch required fields)

## Built-in Configuration

TARSy includes built-in configurations that work out-of-the-box:

### Built-in Agents

- **KubernetesAgent** - Kubernetes troubleshooting
- **ChatAgent** - Follow-up conversations
- **SynthesisAgent** - Synthesizes parallel investigations

### Built-in MCP Servers

- **kubernetes-server** - Kubernetes MCP server (stdio transport)

### Built-in LLM Providers

- **google-default** - Gemini 2.5 Flash
- **openai-default** - GPT-4o
- **anthropic-default** - Claude Sonnet 4
- **xai-default** - Grok Beta
- **vertexai-default** - Claude Sonnet 4 on Vertex AI

### Built-in Chains

- **kubernetes** - Single-stage Kubernetes analysis

You can override any built-in configuration by defining the same name/ID in your YAML files.

## Agent Skills

Agent Skills provide modular, reusable domain knowledge that agents can load on-demand during investigations. Skills follow the industry-standard `SKILL.md` format.

### Directory Structure

```
deploy/config/
├── tarsy.yaml
├── llm-providers.yaml
└── skills/
    ├── pod-troubleshooting/
    │   └── SKILL.md
    ├── resource-management/
    │   └── SKILL.md
    └── networking-diagnostics/
        └── SKILL.md
```

### SKILL.md Format

Each `SKILL.md` has YAML frontmatter (name, description) and a Markdown body:

```markdown
---
name: pod-troubleshooting
description: >
  Kubernetes pod troubleshooting procedures: crash loops, OOMKills,
  image pull errors, and readiness probe failures.
---

## Pod Troubleshooting Guide
- Check pod events for crash reasons
- Inspect container logs for error patterns
...
```

### Agent Skill Configuration

By default, **all discovered skills are available to all agents**. Use optional fields to customize:

```yaml
agents:
  # No skill fields → sees all skills (default)
  KubernetesAgent:
    mcp_servers: [kubernetes-server]

  # Explicit allowlist → sees only these skills
  NetworkAgent:
    skills: [networking-diagnostics, pod-troubleshooting]
    mcp_servers: [kubernetes-server]

  # Required skills → injected into system prompt (always present)
  InfraAgent:
    required_skills: [pod-troubleshooting]
    skills: [pod-troubleshooting, resource-management, networking-diagnostics]
    mcp_servers: [kubernetes-server]

  # Empty list → no skills (disables skill loading)
  SimpleAgent:
    skills: []
```

- **`skills`** — Optional allowlist. `nil` (omitted) = all skills available. `[]` = no skills. `[a, b]` = only these.
- **`required_skills`** — Skills injected directly into the system prompt. Excluded from the on-demand catalog.

Skills are loaded at startup from `<configDir>/skills/*/SKILL.md`. If no `skills/` directory exists, the skill system is inactive. For detailed design, see [ADR-0012: Agent Skills](../../docs/adr/0012-agent-skills.md).

## Configuration Override Priority

Configuration values are resolved in this order (highest priority first):

1. **Per-Interaction API Overrides** - Runtime overrides via API
2. **Environment Variables** - `${VAR}` expanded at startup
3. **Component Configuration** - Your YAML files (agents, chains, etc.)
4. **System Defaults** - `defaults:` section in tarsy.yaml
5. **Built-in Defaults** - Go code built-in values

Example:
```yaml
# Built-in: max_iterations = 20 (Go code)
defaults:
  max_iterations: 25  # Override to 25

agent_chains:
  my-chain:
    max_iterations: 15  # Chain-level: 15
    stages:
      - name: "stage1"
        agents:
          - name: "MyAgent"
            max_iterations: 10  # Agent-level: 10 (highest priority)
            type: orchestrator  # Override agent type for this stage only
```

Effective max_iterations for this agent: **10** (agent-level wins)

The `type` field at the stage-agent level lets you promote an agent to a different role (e.g., `orchestrator`) within a specific chain without modifying its global agent definition.

## Deployment

For step-by-step deployment instructions (host dev, container dev, OpenShift), see [deploy/README.md](../README.md).

## Configuration Validation

TARSy validates all configuration on startup with clear error messages:

```
✗ Configuration loading failed:
chain validation failed: chain 'my-chain' stage 1: agent 'invalid-agent' not found
```

Validation checks:
- Required fields present
- Cross-references valid (chains → agents, agents → MCP servers, etc.)
- Skill references valid (agent `skills` and `required_skills` exist in SkillRegistry)
- Fallback providers valid (provider exists, backend valid, credentials set)
- Value ranges correct
- Environment variables set

## Troubleshooting

### Configuration not found

```
Error: failed to load tarsy.yaml: configuration file not found
```

**Solution**: Copy `tarsy.yaml.example` to `tarsy.yaml`

### Missing environment variable

```
Error: LLM provider validation failed: llm_provider 'google-default': environment variable GOOGLE_API_KEY is not set
```

**Solution**: Set the variable in `.env` file

### Invalid reference

```
Error: chain validation failed: chain 'my-chain' stage 0: agent 'unknown-agent' not found
```

**Solution**: Check agent name spelling or define the agent in `tarsy.yaml`

### YAML syntax error

```
Error: failed to parse tarsy.yaml: invalid YAML syntax
```

**Solution**: Check YAML indentation and syntax (use a YAML validator)

## Support

For issues or questions:
1. Check the design document for detailed explanations
2. Review example files for correct syntax
3. Validate YAML files using online YAML validators
4. Check logs for detailed error messages
