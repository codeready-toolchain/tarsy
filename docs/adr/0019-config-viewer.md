# ADR-0019: Read-Only Configuration Viewer

**Status:** Implemented
**Date:** 2026-07-22

## Overview

Add a read-only view of TARSy’s **effective** configuration to the dashboard — agents, chains, MCP servers, LLM providers, skills metadata, and system settings — similar in spirit to the System Status page.

The goal is operator visibility: “what is this instance actually running?” without opening configmaps or SSH. **Secrets must never appear** in the API response or UI. Credential *names* (e.g. `api_key_env: GOOGLE_API_KEY`) are fine; credential *values* are not.

Before this feature there was no config export API. Closest relatives:

| Existing | What it shows |
|----------|----------------|
| `GET /api/v1/system/mcp-servers` | Runtime MCP health + tool catalog |
| `GET /api/v1/system/default-tools` | MCP IDs + native tool flags for an alert type |
| `GET /api/v1/alert-types` | Alert type → chain mapping |
| Config stats (in-process) | Counts only (not HTTP) |

This fills the gap between operational health and full config introspection.

## Design Principles

1. **Secrets never leave the process.** Field-level sanitization on the API boundary; do not rely on UI-only filtering.
2. **Effective config, not file archaeology.** Show what the running process uses after builtins merge + validation.
3. **Reuse System Status patterns.** Same auth boundary (`/api/v1/*`), page shell, API client conventions.
4. **Fail closed on transport.** Allowlist DTO; secret-bearing fields omitted or replaced with safe substitutes.
5. **Keep `/health` untouched.** Config dumps must not hang off the unauthenticated probe endpoint.
6. **Surgical scope.** Read-only snapshot; no edit, reload, or diff-against-disk in v1.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Source of truth | Effective in-memory config (post-merge registries) | Matches runtime behavior; already on the server; includes builtins YAML may omit. Rejected: raw YAML (misses merges), both (extra surface for v1). |
| Q2 | API shape | Single `GET /api/v1/system/config` snapshot (+ skill detail from Q7) | One round-trip; matches “snapshot” mental model. Rejected: sectioned endpoints / `?sections=` (unused flexibility). |
| Q3 | UI location | Tabs on existing `/system` page | Natural home next to MCP health; reuses page shell and nav. Rejected: new page (splits concerns), drawer (wrong density). |
| Q4 | Presentation | Structured primary + YAML/JSON secondary | Browse + export from the same sanitized DTO. Rejected: structured-only (no export), YAML/JSON-only (harder to navigate). |
| Q5 | MCP transport sanitization | Allowlist DTO with safe substitutes | Fail-closed; new fields denylist-by-default. Rejected: heuristic redaction of full structs (easy to miss fields), restore templates (dual-source complexity). |
| Q6 | v1 sections | Full effective config | One place for the whole deployment picture. Rejected: LLM+MCP only / omit skills-system (arbitrary cuts). |
| Q7 | Skill bodies | Metadata in snapshot; body on demand | Keeps main payload lean while still answering “what did `load_skill` inject?” Rejected: metadata-only (no drill-down), full bodies in snapshot (large/broader exposure). |
| Q8 | Agent/MCP instructions | Include as first-class config | Accurate picture of agent behavior; already in deployed YAML. Rejected: omit/truncate, UI-only reveal (security theater). |
| Q9 | Builtin vs YAML provenance | Merged effective values only | Simple; matches runtime. Rejected: source annotations (loader rework), static builtins section (drifts from merge). |
| Q10 | Who can access | Any authenticated user (same as other system APIs) | Zero new auth; matches oauth2-proxy model. Revisit when session-authorization / Casbin lands. |
| Q11 | Copy/export | Copy sanitized JSON/YAML button; no file download | High leverage for support; same DTO as UI. Rejected: browse-only, file download. |

### Post-verify decisions

1. **`base_url` / transport `url`** — sanitize to scheme/host/port/path; strip userinfo, query, and fragment (ExpandEnv can embed credentials).
2. **`config_dir`** — omit (low operator value).
3. **YAML secondary view** — serialize the sanitized DTO in the dashboard (`js-yaml`).

## Architecture

```
┌─────────────────┐     GET /api/v1/system/config      ┌──────────────────┐
│  Dashboard      │ ─────────────────────────────────► │  API handler     │
│  Config tab     │ ◄──── sanitized JSON DTO ───────── │                  │
└────────┬────────┘                                    └────────┬─────────┘
         │                                                      │
         │  GET …/config/skills/:name                           ▼
         │  (skill body on demand)                     ┌──────────────────┐
         └────────────────────────────────────────────►│  SkillRegistry   │
                                                       │  (metadata+body) │
                                                       └────────┬─────────┘
                                                                ▲
                                                       ┌────────┴─────────┐
                                                       │  Sanitizer /     │
                                                       │  DTO builder     │
                                                       └────────┬─────────┘
                                                                │
                                                                ▼
                                                       ┌──────────────────┐
                                                       │  Effective       │
                                                       │  config          │
                                                       │  (registries)    │
                                                       └──────────────────┘
```

Snapshot responses go through the sanitizer/DTO builder. The skill-detail route returns registry metadata + body (no transport sanitization needed).

### Why not dump internal config as JSON?

After load, env expansion runs on **entire YAML bytes** before unmarshal, so any field that used `{{.VAR_NAME}}` may hold a live secret. A naive marshal of transport config can leak:

- `bearer_token` (literal token)
- `env` map values
- `args` / `url` that embedded secrets

LLM/system configs already store **env var names** (`api_key_env`, `token_env`) — those are safe. The danger is concentrated in MCP transport (and theoretically any string field that an operator templated with a secret).

### Transport sanitization

Build an **allowlist DTO** (fail-closed). New transport fields are denylist-by-default until explicitly added. Future token-exchange fields (see [token-exchange sketch](../proposals/token-exchange-sketch.md)) must stay off the allowlist until reviewed.

| Field | Emitted as |
|-------|------------|
| `type` | as-is |
| `command` | as-is, or `"***"` if it looks secret-bearing (see below) |
| `verify_ssl`, `timeout` | as-is |
| `env` | `env_keys` only (keys, no values) |
| `bearer_token` | `bearer_token_set: bool` only |
| `args` | omit, or `["***"]` when present |
| `url` | sanitized: scheme/host/port/path only (userinfo, query, fragment stripped) |

Never emit raw env values or bearer tokens.

**`command` guard (best-effort):** Env expansion can expand templates in `command` too. Normal practice keeps `command` as a literal binary (e.g. `npx`) and puts secrets in `args` / `url` / `bearer_token`. Default: show `command` as-is. **If the value looks secret-bearing, replace the entire `command` with `"***"`.**

Heuristic is intentionally narrow (sanitizer-local, not the general masking pipelines). Flag as secret-looking when any of:

- Known credential prefixes / shapes (e.g. `ghp_`, `gho_`, `github_pat_`, `xoxb-`, `xoxp-`, `sk-`, `AKIA`, `Bearer `)
- JWT-like (`eyJ…` with two dots)
- Long high-entropy token-like substrings atypical for a binary path (e.g. ≥32 contiguous `[A-Za-z0-9_\-+/=]` chars)

This is defense in depth, not a guarantee — operators still must not put secret templates in `command`. False positives redact to `"***"`; that is acceptable.

### Data flow

1. Handler reads the server’s effective config (nil-guard registries for partial test configs).
2. Builds a response DTO with an explicit allowlist of fields.
3. MCP transport uses the sanitized shape above.
4. Registry maps are emitted with **sorted keys** for stable JSON.
5. Skill list includes metadata only; body fetched on demand.
6. Dashboard fetches the snapshot once on mount (boot-static; no polling) and renders.
7. Empty / nil registries → empty objects `{}` in the response (not `null`).

### Encoding conventions

- JSON field names: **snake_case** (match existing system handlers).
- Duration fields: emit **duration strings** (e.g. `"40m"`, `"5s"`), matching how operators write YAML — not raw nanoseconds.
- Response DTOs live with the API handlers, not in the config package (keep HTTP concerns out of config loading).

## Core Concepts

### Effective configuration

What the process has after:

1. Read `tarsy.yaml` + `llm-providers.yaml`
2. Env expansion on raw bytes
3. Merge with builtins
4. Build registries + validate

This is what agents/chains/MCP actually use. It is **not** a byte-identical copy of the YAML files on disk. Provenance (builtin vs YAML override) is **not** tracked in v1.

### Sanitized view DTO

A dedicated response type (not internal config structs):

**Included in snapshot:**

| Section | Notes |
|---------|-------|
| `defaults` | Includes `llm_provider`, iterations, backend, fallbacks, scoring, success_policy, alert_type, runbook, alert_masking, orchestrator, **memory** (incl. embedding with `api_key_env` name only) |
| `queue` | All worker/poll/timeout fields; durations as strings |
| `system` | GitHub, Slack, Runbooks, Retention, dashboard URL, allowed WS origins — env **names** only for tokens |
| `agents` | Full agent fields — **no** `llm_provider` on agents |
| `chains` | Full chain shape including stages, chat, scoring, overrides |
| `mcp_servers` | Sanitized transport + instructions + masking/summarization |
| `llm_providers` | Full provider fields including env names + sanitized `base_url` |
| `skills` | Metadata only: `name`, `description` |

**Agent fields:**

- `type`, `description`, `mcp_servers`, `custom_instructions`, `llm_backend`, `max_iterations`, `native_tools`, `orchestrator`, `skills`, `required_skills`
- Provider selection lives on defaults / chains / stage overrides, not on agents.

**LLM provider fields:**

- `type`, `model`, `api_key_env`, `credentials_env`, `project_env`, `location_env`, `base_url`, `max_tool_result_tokens`, `native_tools`
- `base_url` uses the same URL sanitization as MCP transport `url`.

**Excluded from the snapshot (not unavailable):**

- DB credentials, process env values, expanded transport secrets
- Skill markdown **bodies** — omitted from the main snapshot; viewable via `GET /api/v1/system/config/skills/:name`
- `config_dir` (runtime path; low value for operators)

### Secret surface

| Location | Risk if dumped raw | Safe representation |
|----------|--------------------|---------------------|
| LLM / GitHub / Slack / embedding env names | Low (name only) | Show as-is |
| Transport `BearerToken` | **High** | `bearer_token_set` only |
| Transport `Env` values | **High** | `env_keys` only |
| Transport `Args` | **Medium–High** | `["***"]` when present |
| Transport `URL` | **Medium–High** | Sanitized origin/path |
| Transport `Command` | Low in practice; medium if misused | as-is, or `"***"` via heuristic |
| LLM `BaseURL` | **Medium** if templated with secrets | Same URL sanitization |
| Skill `Body` | Ops-sensitive | On-demand detail endpoint |
| Agent/MCP instructions, defaults runbook | Ops-sensitive | **Include** (first-class config) |

### Auth model

Same as other `/api/v1/system/*` routes: behind oauth2-proxy in production, **no app-level RBAC**. Any authenticated dashboard user can see the config view. Document this org-wide visibility. Revisit when session-authorization / Casbin lands; v1 does not invent a one-off admin gate.

## UI

Extend the existing **System Status** page (`/system`) with tabs:

| Tab | Content |
|-----|---------|
| **MCP Health** | Existing MCP server status (polled) |
| **Configuration** | New config viewer (fetch once) |

Configuration tab:

- **Primary:** structured, browsable sections (agents, chains, MCP, LLM providers, skills, defaults/queue/system)
- **Secondary:** “View as YAML/JSON” from the same sanitized DTO
- **Copy** button on the YAML/JSON view
- Skill rows: metadata in the list; expand/fetch body via detail endpoint when requested
- Loading / error / empty states matching MCP health
- No file download in v1

## API

```
GET /api/v1/system/config
GET /api/v1/system/config/skills/:name
```

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

Missing skill → **404**. Same auth boundary as the snapshot. Bodies are loaded at boot into the skill registry; no extra disk I/O at request time.

**Not included anywhere:** raw `.env`, database DSN, process environment, unredacted transport secrets.

## Future Considerations

- Builtin vs YAML provenance annotations
- App-level RBAC / admin-only gating (align when session-authorization lands)
- File download of sanitized config
- Allowlisting future transport fields (e.g. token-exchange) after security review
