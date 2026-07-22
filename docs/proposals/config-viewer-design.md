# Read-Only Configuration Viewer

**Status:** Final — decisions recorded in [config-viewer-questions.md](config-viewer-questions.md)

## Overview

Add a read-only view of TARSy’s **effective** configuration to the dashboard — agents, chains, MCP servers, LLM providers, skills metadata, and system settings — similar in spirit to today’s System Status page.

The goal is operator visibility: “what is this instance actually running?” without opening configmaps or SSH. **Secrets must never appear** in the API response or UI. Credential *names* (e.g. `api_key_env: GOOGLE_API_KEY`) are fine; credential *values* are not.

Today there is no config export API. Closest relatives:

| Existing | What it shows |
|----------|----------------|
| `GET /api/v1/system/mcp-servers` | Runtime MCP health + tool catalog |
| `GET /api/v1/system/default-tools` | MCP IDs + native tool flags for an alert type |
| `GET /api/v1/alert-types` | Alert type → chain mapping (sibling under `/api/v1`, not under `/system/`) |
| `Config.Stats()` | In-process counts only (not HTTP) |

This feature fills the gap between operational health and full config introspection.

## Design Principles

1. **Secrets never leave the process.** Field-level sanitization on the API boundary; do not rely on UI-only filtering.
2. **Effective config, not file archaeology.** Show what the running process uses after builtins merge + validation.
3. **Reuse System Status patterns.** Same auth boundary (`/api/v1/*`), page shell, API client conventions.
4. **Fail closed on transport.** Allowlist DTO; secret-bearing fields omitted or replaced with safe substitutes.
5. **Keep `/health` untouched.** Config dumps must not hang off the unauthenticated probe endpoint.
6. **Surgical scope.** Read-only snapshot; no edit, reload, or diff-against-disk in v1.

## Architecture / How It Works

```
┌─────────────────┐     GET /api/v1/system/config      ┌──────────────────┐
│  Dashboard      │ ─────────────────────────────────► │  API handler     │
│  Config tab     │ ◄──── sanitized JSON DTO ───────── │  (pkg/api)       │
└────────┬────────┘                                    └────────┬─────────┘
         │                                                      │
         │  GET …/config/skills/:name                           ▼
         │  (skill body on demand)                     ┌──────────────────┐
         └────────────────────────────────────────────►│  SkillRegistry   │
                                                       │  (metadata+body) │
                                                       └──────────────────┘
                                                                ▲
                                                       ┌────────┴─────────┐
                                                       │  Sanitizer /     │
                                                       │  DTO builder     │
                                                       │  (snapshot path) │
                                                       └────────┬─────────┘
                                                                │
                                                                ▼
                                                       ┌──────────────────┐
                                                       │  config.Config   │
                                                       │  (registries)    │
                                                       └──────────────────┘
```

Snapshot responses go through the sanitizer/DTO builder. The skill-detail route returns registry metadata + body (no transport sanitization needed).

### Why not dump `config.Config` as JSON?

After load, `ExpandEnv` runs on **entire YAML bytes** before unmarshal, so any field that used `{{.VAR_NAME}}` may hold a live secret. A naive marshal of `TransportConfig` can leak:

- `bearer_token` (literal token)
- `env` map values
- `args` / `url` that embedded secrets

LLM/system configs already store **env var names** (`api_key_env`, `token_env`) — those are safe. The danger is concentrated in MCP transport (and theoretically any string field that an operator templated with a secret).

### Transport sanitization (decided)

Build an **allowlist DTO** (fail-closed). New transport fields are denylist-by-default until explicitly added.

| Field | Emitted as |
|-------|------------|
| `type` | as-is |
| `command` | as-is, or `"***"` if it looks secret-bearing (see below) |
| `verify_ssl`, `timeout` | as-is |
| `env` | `env_keys` only (keys, no values) |
| `bearer_token` | `bearer_token_set: bool` only |
| `args`, `url` | omit, or `"***"` / `["***"]` when present |

Never emit raw `Env` values or `BearerToken`. Future token-exchange fields (see [token-exchange sketch](token-exchange-sketch.md)) must stay off the allowlist until reviewed.

**`command` guard (best-effort):** `ExpandEnv` can expand templates in `command` too. Normal practice keeps `command` as a literal binary (e.g. `npx`) and puts secrets in `args` / `url` / `bearer_token`. Default: show `command` as-is. **If the value looks secret-bearing, replace the entire `command` with `"***"`.**

Heuristic is intentionally narrow and lives in the sanitizer (not wired through `pkg/masking` tool-result pipelines). Flag as secret-looking when any of:

- Known credential prefixes / shapes (e.g. `ghp_`, `gho_`, `github_pat_`, `xoxb-`, `xoxp-`, `sk-`, `AKIA`, `Bearer `)
- JWT-like (`eyJ…` with two dots)
- Long high-entropy token-like substrings atypical for a binary path (e.g. ≥32 contiguous `[A-Za-z0-9_\-+/=]` chars)

This is defense in depth, not a guarantee — operators still must not put secret templates in `command`. False positives (rare path names that look like tokens) redact to `"***"`; that is acceptable. Unit-test both the normal `npx` case and a planted secret-in-command case.

### Data flow

1. Handler reads `s.cfg` (always set in production via `NewServer`; nil-guard registries like `defaultToolsHandler` for partial test configs).
2. Builds a response DTO with an explicit allowlist of fields (full effective config).
3. MCP transport uses the sanitized shape above.
4. Registry maps are emitted with **sorted keys** for stable JSON (same practice as `mcpServersHandler` / `alertTypesHandler`).
5. Skill list includes metadata only; body fetched on demand.
6. Dashboard fetches the snapshot once on mount (boot-static; no polling) and renders.
7. Empty / nil registries → empty objects `{}` in the response (not `null`).

### Encoding conventions

- JSON field names: **snake_case** (match existing system handlers).
- Echo routes use `:name` path params (e.g. `/system/config/skills/:name`); docs may show `{name}` interchangeably.
- `time.Duration` fields (queue, retention `event_ttl` / `cleanup_interval`, orchestrator timeouts, runbook `cache_ttl`): **do not** marshal raw `time.Duration` (nanoseconds). Emit **duration strings** (e.g. `"40m"`, `"5s"`) in the DTO, matching how operators write YAML.
- Response DTOs live in `pkg/api/handler_system.go` (or an adjacent helper), following the existing system-handler pattern — not in `pkg/config`.

## Core Concepts

### Effective configuration

What the process has after:

1. Read `tarsy.yaml` + `llm-providers.yaml`
2. `ExpandEnv` on raw bytes
3. Merge with builtins
4. Build registries + validate

This is what agents/chains/MCP actually use. It is **not** a byte-identical copy of the YAML files on disk. Provenance (builtin vs YAML override) is **not** tracked in v1.

### Sanitized view DTO

A dedicated response type (not the internal `config.*` structs):

**Included in snapshot:**

| Section | Source | Notes |
|---------|--------|-------|
| `defaults` | `Config.Defaults` | Includes `llm_provider`, iterations, backend, fallbacks, scoring, success_policy, alert_type, runbook, alert_masking, orchestrator, **memory** (incl. embedding with `api_key_env` name only) |
| `queue` | `Config.Queue` | All worker/poll/timeout fields; durations as strings |
| `system` | GitHub, Slack, Runbooks, Retention, `DashboardURL`, `AllowedWSOrigins` | Env **names** only for tokens; runbooks: `repo_url`, `cache_ttl`, `allowed_domains` |
| `agents` | `AgentRegistry` | Full `AgentConfig` fields (see below) — **no** `llm_provider` on agents |
| `chains` | `ChainRegistry` | Full chain shape including stages, chat, scoring, overrides |
| `mcp_servers` | `MCPServerRegistry` | Sanitized transport + instructions + masking/summarization |
| `llm_providers` | `LLMProviderRegistry` | Full provider fields including env names + `base_url` (as-is) |
| `skills` | `SkillRegistry` | Metadata only: `name`, `description` |

**Agent fields** (`AgentConfig` — correct shape):

- `type`, `description`, `mcp_servers`, `custom_instructions`, `llm_backend`, `max_iterations`, `native_tools`, `orchestrator`, `skills`, `required_skills`
- Provider selection is **not** on agents; it lives on defaults / chains / stage overrides.

**LLM provider fields:**

- `type`, `model`, `api_key_env`, `credentials_env`, `project_env`, `location_env`, `base_url`, `max_tool_result_tokens`, `native_tools`
- `base_url` is shown as-is. Putting secrets in URLs is an anti-pattern; we do not special-case redaction for it.

**Excluded from the snapshot (not unavailable):**

- DB credentials (never on `Config`), process env values, expanded transport secrets
- Skill markdown **bodies** — omitted from the main snapshot to keep it lean; still viewable in the UI via expand/drill-down, which calls `GET /api/v1/system/config/skills/:name`
- `config_dir` (runtime path; low value for operators)

### Secret surface

| Location | Risk if dumped raw | Safe representation |
|----------|--------------------|---------------------|
| `LLMProviderConfig.APIKeyEnv` / `CredentialsEnv` / etc. | Low (name only) | Show as-is |
| `GitHubConfig.TokenEnv`, `SlackConfig.TokenEnv` | Low (name only) | Show as-is |
| Embedding `APIKeyEnv` under defaults.memory | Low (name only) | Show as-is |
| `TransportConfig.BearerToken` | **High** | `bearer_token_set` only |
| `TransportConfig.Env` values | **High** | `env_keys` only |
| `TransportConfig.Args` / `URL` | **Medium–High** | `"***"` when present |
| `TransportConfig.Command` | Low in practice; medium if misused | as-is, or `"***"` via best-effort heuristic |
| `LLMProviderConfig.BaseURL` | Low (secrets-in-URL is anti-pattern) | Show as-is |
| Skill `Body` | Ops-sensitive | On-demand detail endpoint |
| Agent `CustomInstructions`, MCP `Instructions`, Defaults.`Runbook` | Ops-sensitive | **Include** (first-class config) |

### Auth model

Same as other `/api/v1/system/*` routes: behind oauth2-proxy in production, **no app-level RBAC today**. Any authenticated dashboard user can see the config view. Document this org-wide visibility. Revisit when session-authorization / Casbin lands; v1 does not invent a one-off admin gate.

`Server.cfg` is always non-nil in production (`config.Initialize` → `api.NewServer`). Handlers should still nil-guard individual registries for test harnesses that construct partial `Config` values.

## UI

Extend the existing **System Status** page (`/system`) with tabs:

| Tab | Content |
|-----|---------|
| **MCP Health** | Existing `MCPServerStatusView` (15s poll) |
| **Configuration** | New config viewer (fetch once) |

Use MUI `Tabs` (already used in trace/`JsonDisplay`) or `ToggleButtonGroup` (dashboard main) — either fits; prefer MUI `Tabs` for two peer views on this page.

Configuration tab:

- **Primary:** structured, browsable sections (agents, chains, MCP, LLM providers, skills, defaults/queue/system)
- **Secondary:** “View as YAML/JSON” from the same sanitized DTO — JSON via `JSON.stringify`; YAML via `js-yaml` (`yaml.dump`) in the dashboard
- **Copy** button via existing `CopyButton` (`components/shared/CopyButton.tsx`) on the YAML/JSON view
- Skill rows: metadata in the list; expand/fetch body via detail endpoint when requested
- Loading / error / empty states matching `MCPServerStatusView`
- No file download in v1

## API

```
GET /api/v1/system/config
GET /api/v1/system/config/skills/:name
```

Register both next to existing system routes in `pkg/api/server.go`.

### Snapshot response (illustrative)

```json
{
  "defaults": {
    "llm_provider": "google-default",
    "max_iterations": 10,
    "llm_backend": "google-native",
    "memory": {
      "embedding": {
        "provider": "google",
        "model": "…",
        "api_key_env": "GOOGLE_API_KEY",
        "dimensions": 768
      }
    }
  },
  "queue": {
    "worker_count": 5,
    "poll_interval": "5s",
    "session_timeout": "40m"
  },
  "system": {
    "github": { "token_env": "GITHUB_TOKEN" },
    "slack": { "enabled": true, "token_env": "SLACK_BOT_TOKEN", "channel": "C…" },
    "runbooks": {
      "repo_url": "…",
      "cache_ttl": "1m",
      "allowed_domains": ["github.com", "raw.githubusercontent.com"]
    },
    "retention": {
      "session_retention_days": 30,
      "event_ttl": "168h",
      "cleanup_interval": "1h"
    },
    "dashboard_url": "https://…",
    "allowed_ws_origins": []
  },
  "agents": {
    "KubernetesAgent": {
      "type": "…",
      "description": "…",
      "mcp_servers": ["kubernetes-server"],
      "custom_instructions": "…",
      "llm_backend": "…",
      "max_iterations": 10,
      "skills": null,
      "required_skills": [],
      "native_tools": {},
      "orchestrator": null
    }
  },
  "chains": {
    "kubernetes-agent": {
      "alert_types": ["KubernetesError"],
      "description": "…",
      "stages": [],
      "llm_provider": "google-default"
    }
  },
  "mcp_servers": {
    "kubernetes-server": {
      "transport": {
        "type": "stdio",
        "command": "npx",
        "args": ["***"],
        "env_keys": ["KUBECONFIG"],
        "bearer_token_set": false
      },
      "instructions": "...",
      "data_masking": { "...": "..." },
      "summarization": { "...": "..." }
    }
  },
  "llm_providers": {
    "google-default": {
      "type": "google",
      "model": "…",
      "api_key_env": "GOOGLE_API_KEY",
      "credentials_env": "",
      "project_env": "",
      "location_env": "",
      "base_url": "",
      "max_tool_result_tokens": 150000,
      "native_tools": { "...": true }
    }
  },
  "skills": {
    "example-skill": { "name": "…", "description": "…" }
  }
}
```

### Skill detail

```
GET /api/v1/system/config/skills/:name
→ { "name": "…", "description": "…", "body": "…" }
```

Map `ErrSkillNotFound` → **404** (same pattern as memory handlers). Same auth boundary as the snapshot. Bodies are loaded at boot into `SkillRegistry`; no extra disk I/O at request time.

**Not included anywhere:** raw `.env`, database DSN, process environment, unredacted transport secrets.

## Implementation Plan

Ship as **one PR**: backend API + dashboard UI + tests together. No staged rollout.

### Backend

1. Add a sanitizer/DTO builder in `pkg/api` (keep `pkg/config` free of HTTP concerns).
2. `GET /api/v1/system/config` + `GET /api/v1/system/config/skills/:name` next to existing system handlers in `server.go`.
3. Unit tests proving: bearer tokens, env values, and secret-bearing args/url never appear; env **names** and `env_keys` do; normal `command` (e.g. `npx`) is preserved; secret-looking `command` becomes `"***"`; `custom_instructions` / MCP `instructions` are present; durations are strings; map keys are sorted; nil registries → `{}`; missing skill → 404.
4. Extend e2e helpers as needed for the new endpoints.

### Dashboard

1. Types in `types/system.ts` + `getSystemConfig()` / `getSystemConfigSkill(name)` in `services/api.ts`.
2. Tabs on `SystemStatusPage` (MCP Health | Configuration); config view under `components/system/`.
3. Add `js-yaml` (+ types); structured sections + JSON/YAML secondary view + `CopyButton`; skill body fetch on expand.
4. Light API client tests.

### Out of scope for this PR (follow-ups)

- Builtin vs YAML provenance annotations
- App-level RBAC / admin-only gating (align when session-authorization lands)
- File download of sanitized config

## Decisions summary

| # | Decision |
|---|----------|
| Q1 | Effective in-memory config (post-merge registries) |
| Q2 | Single `GET /api/v1/system/config` snapshot (+ skill detail route from Q7) |
| Q3 | Tabs/sections on existing `/system` page |
| Q4 | Structured primary + YAML/JSON secondary |
| Q5 | Allowlist transport DTO with safe substitutes |
| Q6 | Full effective config in v1 |
| Q7 | Skills metadata in snapshot; body on demand |
| Q8 | Include agent/MCP instructions |
| Q9 | No builtin/override provenance in v1 |
| Q10 | Any authenticated user (same as other system APIs) |
| Q11 | Copy sanitized JSON/YAML button; no file download |

## Post-verify decisions

1. **`base_url`** — show as-is (secrets-in-URL is an anti-pattern; no special redaction).
2. **`config_dir`** — omit (low operator value).
3. **YAML secondary view** — use `js-yaml` in the dashboard to serialize the sanitized DTO.
