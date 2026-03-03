# Session Authorization — Multi-User Visibility Controls

**Status:** Sketch complete — ready for detailed design

## Problem

All authenticated TARSy users see all sessions in the dashboard. There is no authorization layer — if you can log in, you can see every session submitted by every user, including their alert data, analysis results, chat history, and tool call details.

This is problematic for multi-team deployments where:
- Different teams investigate different types of incidents and shouldn't see each other's data
- Session data may contain sensitive information (security investigations, customer data, credentials)
- Audit and compliance requirements demand that access is scoped to what users need to see
- Shared environments (e.g., a central TARSy instance serving multiple teams) need tenant isolation

## Goal

Add project-based authorization to TARSy using Casbin (the same RBAC engine used by ArgoCD) so that users only see sessions in projects they have access to. The solution leverages OIDC group claims already forwarded by oauth2-proxy and follows the ArgoCD AppProject model.

## How It Relates to Token Exchange

This sketch is one half of TARSy's multi-tenant story. The other half is [token exchange](token-exchange-sketch.md).

| Concern | Token exchange | This sketch (session authorization) |
|---|---|---|
| **Controls** | What a user can **do** through MCP tools | What a user can **see** in the dashboard and API |
| **Mechanism** | Per-user credentials propagated to MCP servers | Project-based RBAC with Casbin |
| **Enforcement** | At MCP call time (RoundTripper) | At API layer (middleware) |
| **Identity source** | Bearer token from auth proxy | OIDC group claims from auth proxy |

Both features share the same identity extraction point (`pkg/api/auth.go`) and both add fields to `ent/schema/alertsession.go` — token exchange adds `user_token` (encrypted), this sketch adds `project`. They can be implemented independently but together provide full multi-tenancy: users operate under their own permissions **and** only see sessions they're authorized to access.

### Current state

- **`author` field** exists on `AlertSession`, set from `X-Forwarded-User` at submission time
- **No project/team/namespace fields** on sessions
- **No RBAC or authorization system** — all authenticated users see all sessions
- **Auth is external** — oauth2-proxy / kube-rbac-proxy provide identity but not authorization
- **OIDC group claims** (`X-Forwarded-Groups`) are already forwarded by oauth2-proxy but unused

## Key Concepts

### Projects (authorization boundary)

A **project** is TARSy's authorization boundary — equivalent to ArgoCD's `AppProject`. Every session belongs to exactly one project.

A project is **not** a team. It's more flexible:
- One team can work across multiple projects (e.g., SRE has `prod-incidents` and `security-investigations`)
- One project can be accessible to multiple teams (e.g., shared `incident-response` project)
- Projects can represent workstreams, environments, products — not just organizational units

Projects are defined in `tarsy.yaml` and referenced by Casbin policies.

### Casbin RBAC

TARSy uses [Casbin](https://github.com/casbin/casbin) for RBAC — the same in-process policy engine used by ArgoCD. No external authorization service needed.

**Model** (hardcoded in TARSy, not configurable):

```ini
[request_definition]
r = sub, resource, action, obj

[policy_definition]
p = sub, resource, action, obj, eft

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && r.resource == p.resource && r.action == p.action && globMatch(r.obj, p.obj)
```

**Policies** (configured in `tarsy.yaml`):

```
# Group mappings: OIDC group → Casbin role
g, team-sre, role:sre
g, team-platform, role:platform
g, platform-admins, role:admin

# Permissions: role, resource, action, project/*, effect
p, role:sre, sessions, *, prod-incidents/*, allow
p, role:sre, sessions, *, security-investigations/*, allow
p, role:platform, sessions, *, prod-incidents/*, allow
p, role:admin, sessions, *, */*, allow
```

**Evaluation:** On every API request, TARSy calls `enforcer.Enforce(user, "sessions", action, "project/session_id")`. Sub-millisecond, in-process.

### How sessions get their project

- **Dashboard users with one project:** Auto-selected (no extra UX step)
- **Dashboard users with multiple projects:** Project selector in the UI before submitting
- **API clients:** Specify `"project": "prod-incidents"` in the alert payload. Casbin validates the caller has `create` permission on sessions in that project.
- **System-initiated sessions:** Require `project` in the payload, or fall back to a configured default project

### Where authorization is enforced

Authorization is enforced at the **API layer** — the security boundary. Every session-related endpoint is protected:

| Endpoint | Action | Authorization check |
|---|---|---|
| `POST /api/v1/alerts` | `create` | Caller has `create` on `project/*` |
| `GET /api/v1/sessions` | `get` | Filter to sessions in authorized projects |
| `GET /api/v1/sessions/:id` | `get` | Session's project is authorized |
| `GET /api/v1/sessions/:id/timeline` | `get` | Session's project is authorized |
| `POST /api/v1/sessions/:id/chat/messages` | `update` | Session's project is authorized |
| `GET /api/v1/sessions/:id/chat` | `get` | Session's project is authorized |
| `DELETE /api/v1/sessions/:id` | `delete` | Session's project is authorized |
| `GET /api/v1/ws` | `get` | Filter events to authorized projects, or reject with 403 (see hard gate) |

### WebSocket authorization

WebSocket events are filtered server-side before broadcasting — unauthorized events never reach the client. This uses the same Casbin enforcer as REST endpoints.

**Hard gate requirement:** When `authorization.enabled: true`, the `/api/v1/ws` handler must enforce authorization from day one. Either:
1. Filter events per-connection using the same Casbin enforcer (preferred — ship with REST authZ), or
2. Reject connections with HTTP 403 and log `"WebSocket connections rejected: authorization enabled but WebSocket filtering not yet implemented"` until filtering lands.

Leaving the WebSocket endpoint open while REST is locked down would leak session metadata (titles, statuses, project names) to unauthorized users.

### Configuration shape

```yaml
authorization:
  enabled: true
  scopes: "groups"                           # OIDC claim to extract groups from
  policy_default: ""                         # default role for authenticated users (empty = no access)
  projects:
    - prod-incidents
    - security-investigations
    - developer-support
  policy: |
    # Group mappings (OIDC groups → roles)
    g, team-sre, role:sre
    g, team-platform, role:platform
    g, platform-admins, role:admin

    # Project access
    p, role:sre, sessions, *, prod-incidents/*, allow
    p, role:sre, sessions, *, security-investigations/*, allow
    p, role:platform, sessions, *, prod-incidents/*, allow
    p, role:admin, sessions, *, */*, allow
```

### Identity info available today

| Source | Header | Used for |
|---|---|---|
| oauth2-proxy | `X-Forwarded-User` | User identity (existing `author` field) |
| oauth2-proxy | `X-Forwarded-Email` | User identity (fallback) |
| oauth2-proxy | `X-Forwarded-Groups` | Group membership → Casbin role resolution |
| kube-rbac-proxy | `X-Remote-User` | API client identity |
| kube-rbac-proxy | `X-Remote-Group` | API client groups |

### Header trust

The identity headers listed above are **only trustworthy when set by a trusted reverse proxy** (oauth2-proxy, kube-rbac-proxy). TARSy's deployment model guarantees this: the proxies run in the same container as TARSy, only the proxy ports are exposed, and clients have no network path to reach TARSy directly. This means header spoofing is not possible without compromising the pod itself.

**Operator responsibility:** The ingress must strip client-supplied identity headers (`X-Forwarded-User`, `X-Forwarded-Groups`, etc.) before forwarding to the proxy, so the proxy always sets them from scratch with validated values. For nginx-ingress this is `proxy_set_header X-Forwarded-User "";` etc.

### Key integration points

| Component | File | Change |
|---|---|---|
| Auth extraction | `pkg/api/auth.go` | Extract groups from `X-Forwarded-Groups` / `X-Remote-Group` |
| Alert handler | `pkg/api/handler_alert.go` | Validate `project` field, enforce `create` permission |
| Session handler | `pkg/api/handler_session.go` | Filter session list by authorized projects |
| Session detail | `pkg/api/handler_session.go` | Check `get` permission on session's project |
| Chat handler | `pkg/api/handler_chat.go` | Check `update` permission on session's project |
| WebSocket | `pkg/api/handler_ws.go` | Filter events by authorized projects, or 403 gate (same release as REST authZ) |
| Config | `pkg/config/` | New `AuthorizationConfig` with Casbin policy |
| Casbin enforcer | new `pkg/authz/` | Load model + policies, expose `Enforce()` / `GetAuthorizedProjects()` |
| DB schema | `ent/schema/alertsession.go` | Add `project` field (required, indexed) |
| Dashboard | `web/dashboard/` | Project selector for multi-project users, project filter in session list |
| API types | `pkg/api/` or `pkg/models/` | Add `project` to `SubmitAlertRequest` |

## What Is Out of Scope

- **Per-user tool filtering** — which MCP tools a user can invoke (handled by MCP Gateway or future feature)
- **Per-user MCP credentials** — covered by [token-exchange-sketch.md](token-exchange-sketch.md)
- **Data-level masking per user** — masking sensitive fields in session data based on user role
- **Multi-tenant data isolation** — full database-level tenant separation (row-level security, separate schemas)
- **User management UI** — managing users, groups, and permissions within TARSy (users/groups come from the IdP)
- **Public/shared session visibility** — deferred; cross-team sharing is achievable via Casbin policies (grant multiple roles access to the same project)
- **Fine-grained action permissions** — initial implementation uses `*` for actions; per-action permissions (e.g., "can view but not delete") can be added later using the same Casbin model
