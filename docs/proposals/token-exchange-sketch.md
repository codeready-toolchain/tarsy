# Token Exchange — Per-User MCP Credentials

**Status:** Sketch complete — ready for detailed design

## Problem

TARSy currently uses **shared, system-wide credentials** for all MCP server connections. The bearer token, env vars, and kubeconfig are resolved once from `tarsy.yaml` at load time. Every session — regardless of which user initiated it — uses the same MCP credentials. User identity (`author`) is stored on sessions but never propagated to MCP tools.

This means:
- A user with read-only Kubernetes RBAC bindings gets the same cluster access as an admin
- Audit logs on downstream systems (e.g., Kubernetes audit) attribute all actions to TARSy's shared ServiceAccount, not the actual user
- There is no way to enforce least-privilege per user — everyone gets TARSy's full permissions
- Compliance requirements around traceability and accountability are not met

## Goal

Enable TARSy to propagate the **user's identity** to MCP servers so that downstream tools operate under the user's own permissions rather than shared credentials. The primary mechanism is **OAuth2 Token Exchange** (RFC 8693) via a **Security Token Service (STS)**, where TARSy exchanges the user's incoming token for a narrowly-scoped token suitable for each MCP server.

## How It Relates to Session Authorization

This sketch is one half of TARSy's multi-tenant story. The other half is [session authorization](session-authorization-sketch.md).

| Concern | This sketch (token exchange) | Session authorization |
|---|---|---|
| **Controls** | What a user can **do** through MCP tools | What a user can **see** in the dashboard and API |
| **Mechanism** | Per-user credentials propagated to MCP servers | Project-based RBAC with Casbin |
| **Enforcement** | At MCP call time (RoundTripper) | At API layer (middleware) |
| **Identity source** | Bearer token from auth proxy | OIDC group claims from auth proxy |

Both features share the same identity extraction point (`pkg/api/auth.go`) and both add fields to `ent/schema/alertsession.go` — this sketch adds `user_token` (encrypted), session authorization adds `project`. They can be implemented independently but together provide full multi-tenancy: users operate under their own permissions **and** only see sessions they're authorized to access.

## How It Relates to the Existing System

### Current auth flow

```
User (browser/API client)
  → oauth2-proxy / kube-rbac-proxy (validates identity)
  → TARSy API (extracts author from X-Forwarded-User / X-Remote-User)
  → Session created with author field
  → Worker claims session
  → MCP ClientFactory creates Client + ToolExecutor
  → MCP calls use shared bearer_token from tarsy.yaml
```

### Trusted-proxy boundary

Token exchange elevates the stakes for header trust: a forged bearer token doesn't just spoof identity — it grants MCP tool access as another user.

TARSy's deployment model guarantees header integrity: the auth proxies (oauth2-proxy, kube-rbac-proxy) run in the same container as TARSy, only the proxy ports are exposed, and clients have no network path to reach TARSy directly. Header spoofing is not possible without compromising the pod itself. The same guarantee applies to the [session authorization sketch](session-authorization-sketch.md), which depends on the same headers for Casbin role resolution.

**Operator responsibility:** The ingress must strip client-supplied identity/forwarding headers (`X-Forwarded-User`, `X-Forwarded-Groups`, `Authorization`, etc.) before forwarding to the proxy, so the proxy always sets them from scratch with validated values.

### Proposed flow with token exchange

```
User (browser/API client)
  → oauth2-proxy / kube-rbac-proxy (validates identity, sets trusted headers)
  → TARSy API (extracts author AND user's bearer token from trusted proxy only)
  → Session created with author + encrypted user token in DB
  → Worker claims session, decrypts user token
  → MCP ClientFactory creates Client (user token in Go context)
  → For each MCP server call, context-aware RoundTripper checks auth_mode:
      token_exchange:       exchange user token → server-specific token via STS
      forward_user_token:   pass user token directly as Authorization header
      shared:               use static bearer_token from config (existing behavior)
  → Session reaches terminal state → encrypted token wiped from DB
```

### Chat flow (no token storage needed)

```
User sends POST /sessions/:id/chat/messages
  → TARSy API extracts fresh bearer token from the trusted proxy request
  → Token placed in Go context
  → ChatMessageExecutor uses token directly for MCP calls
  → No DB storage needed — user is actively making requests
```

### Key integration points

| Component | File | Change |
|---|---|---|
| Auth extraction | `pkg/api/auth.go` | Capture bearer token in addition to author name |
| Alert handler | `pkg/api/handler_alert.go` | Encrypt and store user token on session row |
| Chat handler | `pkg/api/handler_chat.go` | Pass user token via context (no storage) |
| MCP server config | `pkg/config/mcp.go` | Add `AuthConfig` section to `MCPServerConfig` |
| Config types | `pkg/config/types.go` | Define `AuthConfig`, `TokenExchangeConfig` structs |
| MCP transport | `pkg/mcp/transport.go` | Context-aware `RoundTripper` that handles all auth modes |
| MCP client | `pkg/mcp/client.go` | No API changes — token flows via context |
| MCP client factory | `pkg/mcp/client_factory.go` | No API changes — token flows via context |
| MCP executor | `pkg/mcp/executor.go` | No API changes — token flows via context |
| Token exchange | new `pkg/mcp/tokenexchange/` | RFC 8693 STS client implementation |
| Token encryption | new `pkg/crypto/` or similar | AES-GCM encrypt/decrypt for DB storage |
| Session executor | `pkg/queue/executor.go` | Decrypt token, place in context before MCP calls |
| Session cleanup | `pkg/queue/executor.go` | Wipe token on terminal status |
| Token janitor | `pkg/queue/` or `cmd/` | Background goroutine to clear expired tokens (defense-in-depth) |
| DB schema | `ent/schema/alertsession.go` | Add encrypted `user_token` and `token_expires_at` fields |

## Key Concepts

### Auth modes (per MCP server)

Each MCP server in `tarsy.yaml` declares how it handles authentication via an `auth` section:

- **`shared`** (default, current behavior) — uses the static `bearer_token` from transport config. No per-user identity. This is the implicit mode when no `auth` section is present.
- **`forward_user_token`** — forwards the user's incoming bearer token directly to the MCP server. The MCP server (e.g., Kubernetes MCP server with `require_oauth=true`) validates and uses it.
- **`token_exchange`** — TARSy exchanges the user's token at an STS endpoint for a new, server-specific token. Recommended when the user's IdP token isn't directly usable by the MCP server.
- **`mcp_gateway`** — TARSy connects to an [MCP Gateway](https://github.com/kagenti/mcp-gateway) that aggregates multiple backend MCP servers behind a single endpoint. TARSy forwards the user's token; the gateway handles per-backend token exchange, Vault credential lookup, and identity-based tool filtering. See [MCP Gateway integration](#mcp-gateway-integration) below.

### Fallback policy

When per-user auth is configured (`forward_user_token` or `token_exchange`) but no user token is available (system-initiated sessions, dev mode), the `fallback_to_shared` setting controls behavior:

- **`false`** (default) — fail closed. MCP call is rejected with an error. Prevents accidental security downgrades.
- **`true`** — fall back to shared credentials. Useful for MCP servers where shared access is acceptable as a fallback.

### Token storage (alert sessions only)

- User's bearer token is captured at the API boundary (`submitAlertHandler`)
- Encrypted with AES-GCM using a key from env var / Kubernetes Secret
- Stored on the session row with a `token_expires_at` timestamp (default: `now() + 30m`)
- Decrypted by the worker when it claims the session
- **Wiped atomically** (`SET user_token = NULL, token_expires_at = NULL`) when the session reaches a terminal state (completed, failed, timed out, cancelled)
- Token only exists in the DB for the session's active lifecycle (typically seconds to minutes)

**Defense-in-depth: periodic janitor.** A background goroutine (`runTokenCleanup`) runs on a configurable interval (default: every 5 minutes) and clears any tokens past their hard TTL:

```sql
UPDATE alert_sessions
SET user_token = NULL, token_expires_at = NULL
WHERE user_token IS NOT NULL AND token_expires_at < now()
```

This catches stuck sessions, crashed workers, or any edge case where terminal-state cleanup doesn't fire. Configuration via env vars: `TOKEN_TTL` (default `30m`), `TOKEN_JANITOR_INTERVAL` (default `5m`, `0` to disable).

Chat messages do not use stored tokens — the user is actively making requests, so their fresh token arrives with each HTTP call.

### Token flow through the MCP client layer

User tokens flow via **Go context** — the idiomatic mechanism for request-scoped values:

1. Worker decrypts token (alert) or handler extracts token (chat)
2. Token is placed in the Go context via a context key
3. Context flows through `ToolExecutor.Execute(ctx, ...)` → `Client.CallTool(ctx, ...)` → `callToolOnce(ctx, ...)` → MCP SDK → HTTP request
4. A context-aware `RoundTripper` (replacing the current static `bearerTokenTransport`) extracts the token from the context and:
   - **`shared`**: ignores context, uses static token from config
   - **`forward_user_token`**: sets `Authorization: Bearer <user-token>`
   - **`token_exchange`**: exchanges the user token at the STS endpoint, sets `Authorization: Bearer <exchanged-token>`
   - **`mcp_gateway`**: sets `Authorization: Bearer <user-token>` (same as `forward_user_token` — the gateway handles exchange internally)

No changes to `Client`, `ToolExecutor`, or `ClientFactory` public APIs.

### Token exchange flow (RFC 8693)

```
RoundTripper                  STS Endpoint (e.g., Keycloak)
  |                                    |
  |  POST /token                       |
  |  grant_type=token-exchange         |
  |  subject_token=<user's JWT>        |
  |  subject_token_type=access_token   |
  |  audience=<mcp-server-audience>    |
  |  scope=<requested-scopes>          |
  |  client_id=<tarsy-client-id>       |
  |  client_secret=<tarsy-secret>      |
  |                                    |
  |  ← 200 OK                          |
  |    access_token=<new-token>        |
  |    token_type=Bearer               |
  |    expires_in=300                  |
```

Exchange happens **lazily at MCP call time** — only for the specific server being invoked. This avoids pre-exchanging tokens for servers that may never be called during a session.

For the STS client implementation, we can either use `github.com/zitadel/oidc/v4` (which provides a ready-made RFC 8693 token exchange client that works with any compliant IdP) or a lightweight custom implementation — the exchange is a single `POST /token` with specific form parameters.

### Configuration shape

```yaml
mcp_servers:
  kubernetes-server:
    transport:
      type: "http"
      url: "https://kubernetes-mcp-server:8443/mcp"
      timeout: 90
      verify_ssl: false
    auth:                              # NEW — sibling to transport, data_masking, summarization
      mode: "token_exchange"
      fallback_to_shared: false        # default: fail closed when no user token
      token_exchange:
        sts_url: "https://keycloak.example.com/realms/tarsy/protocol/openid-connect/token"
        client_id: "tarsy-kube-exchange"
        client_secret: "{{.KUBE_STS_CLIENT_SECRET}}"
        audience: "kubernetes-mcp-server"
        scopes: ["openid"]
    data_masking:
      enabled: true

  monitoring-server:
    transport:
      type: "http"
      url: "https://monitoring-mcp:8443/mcp"
    auth:
      mode: "forward_user_token"       # pass user token directly
      fallback_to_shared: true         # OK to use shared creds for monitoring

  docs-server:
    transport:
      type: "http"
      url: "https://docs-mcp:8080/mcp"
      bearer_token: "{{.DOCS_TOKEN}}"
    # No auth section → defaults to "shared" mode (existing behavior)

  # MCP Gateway — single endpoint aggregating multiple backend servers.
  # In a gateway-only environment, this is the ONLY entry in mcp_servers.
  # All backend servers (Kubernetes, monitoring, etc.) are behind the gateway.
  # Chain configs reference this single server ID: mcp_servers: ["mcp-gateway"]
  mcp-gateway:
    transport:
      type: "http"
      url: "https://mcp-gateway.example.com/mcp"
      timeout: 90
    auth:
      mode: "mcp_gateway"
      fallback_to_shared: false
```

### Kubernetes MCP server compatibility

The Kubernetes MCP server already supports both relevant approaches:

- **Token forwarding:** When `Authorization: Bearer <token>` is present, creates a derived Kubernetes client using that token. Kubernetes RBAC applies per-user.
- **STS token exchange:** Configured via `sts_client_id`, `sts_client_secret`, etc. Exchanges incoming token for a cluster-specific token.

With `forward_user_token`, TARSy passes the user's token and the kube MCP server uses it directly (or exchanges it internally). With `token_exchange`, TARSy exchanges first and sends the result. The operator picks based on their infrastructure.

### MCP Gateway integration

The [MCP Gateway](https://github.com/kagenti/mcp-gateway) is an Envoy-based gateway that aggregates multiple backend MCP servers behind a single MCP endpoint. When deployed, the gateway offloads auth complexity from TARSy entirely:

```
User → TARSy (forwards user token) → MCP Gateway (single /mcp endpoint)
                                        ├→ Token exchange (RFC 8693) per backend
                                        ├→ Vault credential lookup (PATs, API keys)
                                        ├→ Identity-based tool filtering
                                        └→ Routes tool calls to correct backend
```

**How it works from TARSy's perspective:**

- The gateway appears as a **single MCP server** in `tarsy.yaml` — one config entry, one connection
- `tools/list` returns all tools from all backends, with **prefixed names** (e.g., `kubernetes_get_pods`, `monitoring_get_alerts`) instead of the usual `server.tool` convention
- TARSy forwards the user's bearer token via `Authorization` header; the gateway's AuthPolicy (Kuadrant) handles per-backend token exchange, Vault lookups, and tool-level authorization
- The gateway manages backend sessions internally — TARSy only sees one session

**What the gateway provides that TARSy doesn't need to do:**

| Concern | Without gateway | With gateway |
|---|---|---|
| Token exchange per backend | TARSy (`token_exchange` mode) | Gateway (Kuadrant AuthPolicy) |
| Vault/PAT credentials | Out of scope | Gateway (Vault integration) |
| Per-user tool filtering | Out of scope | Gateway (`x-authorized-tools` header) |
| Multi-backend routing | TARSy connects to each server | Gateway routes internally |

**What TARSy still handles:**

- Capturing the user's bearer token at the API boundary
- Storing/forwarding the token (same as other auth modes)
- `fallback_to_shared` policy when no user token is available

**Tool naming:** With the gateway, tools arrive with gateway-assigned prefixes (e.g., `kubernetes_get_pods`). The LLM sees and calls these prefixed names. TARSy's `ToolExecutor` treats the gateway as a single server — tool routing is the gateway's responsibility, transparent to TARSy.

**Trade-offs vs direct `token_exchange`:**

- **Pro:** Offloads all per-backend auth complexity (exchange, Vault, filtering) to infrastructure
- **Pro:** Supports heterogeneous auth (OAuth, PATs, API keys) across backends without TARSy changes
- **Pro:** Centralized policy enforcement and audit logging at the gateway layer
- **Con:** Additional infrastructure to deploy and manage (Envoy, Kuadrant, gateway controller)
- **Con:** Single point of failure for all MCP traffic
- **Con:** Less control from TARSy's side — auth decisions are delegated

Both approaches (`token_exchange` for direct connections and `mcp_gateway` for gateway deployments) can coexist — some MCP servers can be reached directly while others go through a gateway.

### Token refresh

Deferred. Token exchange produces fresh tokens at exchange time (not the original token's issue time). For typical session durations (2-10 minutes), this is sufficient. If long-running sessions or queue delays become a problem, refresh token support can be added as a follow-up.

## What Is Out of Scope

- **Per-user tool filtering / RBAC within TARSy** — this sketch covers credential propagation only, not which tools a user can see (the MCP Gateway handles this externally when deployed)
- **Credential store / Vault integration within TARSy** — storing per-user API keys, PATs, or non-OAuth credentials inside TARSy. The MCP Gateway handles this externally via Vault integration.
- **OAuth2 Authorization Code flow** — TARSy receives tokens from the auth proxy, it doesn't initiate OAuth flows with users
- **Token refresh** — deferred; lazy exchange at call time provides sufficient freshness for typical sessions
- **MCP Gateway deployment/operation** — this sketch covers TARSy's integration with the gateway, not how to deploy or configure the gateway itself
