# Token Exchange — Sketch Questions

**Status:** All decisions made
**Related:** [Sketch document](token-exchange-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: Which auth modes should be supported in the initial implementation?

TARSy could support three auth modes per MCP server: `shared` (current behavior), `forward_user_token` (pass token as-is), and `token_exchange` (RFC 8693 STS exchange). Implementing all three at once increases scope. Token forwarding is simpler but the MCP spec discourages raw token passthrough. Token exchange is more secure but requires STS infrastructure.

### Option A: All three from the start (`shared`, `forward_user_token`, `token_exchange`)

- **Pro:** Maximum flexibility — operators choose per MCP server based on their infrastructure.
- **Pro:** `forward_user_token` is a quick win for the Kubernetes MCP server which already supports it natively.
- **Pro:** `shared` preserves backward compatibility for MCP servers that don't need per-user auth.
- **Con:** Larger initial scope — three code paths to test and maintain.

**Decision:** Option A — all three modes from the start. They're orthogonal, each serves a clear use case, and `forward_user_token` is a thin code path while `shared` already exists.

_Considered and rejected: Option B (`shared` + `token_exchange` only — forces STS infrastructure even when simple token forwarding suffices), Option C (`shared` + `forward_user_token` only — doesn't address IdP token mismatch scenarios, raw forwarding may need replacement later)._

---

## Q2: Where should user tokens be stored between API request time and session execution time?

User tokens arrive at the API layer (HTTP request handler) but sessions are executed asynchronously by workers that claim them from a queue. The token must survive this gap. Storing tokens in the database raises security concerns. Not storing them means they must be passed through the context chain.

### Option A: Store encrypted token in the database (on the session row)

- **Pro:** Token survives any delay — works even if the worker starts minutes later or the API server restarts.
- **Pro:** Simple to implement — add an encrypted column to `alert_sessions` and decrypt at execution time.
- **Pro:** Enables lazy token exchange — exchange happens at MCP call time only for the specific server being invoked, not for all possible servers upfront.
- **Con:** Tokens in the database are a security risk even when encrypted (key management, breach surface).
- **Con:** Need encryption key management (rotation, secure storage).
- **Con:** Token may expire if session sits in the queue for a long time.

**Decision:** Option A — encrypted token in the database. TARSy is single-process, queue delays are typically seconds, and lazy exchange at call time means we only need the original token (not pre-exchanged per-server tokens). Encryption key from env var or Kubernetes Secret. Token is wiped (`SET user_token = NULL`) atomically when the session reaches a terminal state, so it only exists in the DB for the session's active lifecycle. Migrating to Vault is a straightforward follow-up if security requirements evolve.

**Defense-in-depth: hard TTL + periodic janitor.** Terminal-state cleanup is the primary mechanism, but tokens must not persist indefinitely if cleanup fails (stuck sessions, crashed workers, unhandled edge cases):

- **`token_expires_at` field:** Set when storing `user_token`, computed as `now() + TOKEN_TTL`. Default TTL: 30 minutes (well above typical session duration of 2-10 minutes, but bounds the worst case).
- **Periodic janitor:** A background goroutine (`runTokenCleanup`) runs on a configurable interval, executing `UPDATE alert_sessions SET user_token = NULL WHERE user_token IS NOT NULL AND token_expires_at < now()`. Logs each cleanup at `INFO` level with the number of expired tokens cleared.
- **Configuration** (env vars / Kubernetes Secret):
  - `TOKEN_TTL` — hard expiry for stored tokens (default: `30m`)
  - `TOKEN_JANITOR_INTERVAL` — how often the janitor runs (default: `5m`, set to `0` to disable)
- **Terminal-state cleanup still runs first.** The janitor is a safety net, not the primary mechanism. Normal flow: session completes → token wiped immediately. Janitor catches anything that slips through.

_Considered and rejected: Option B (in-memory cache — lost on restart, constrains multi-replica scaling), Option C (external secret store — significantly higher complexity for initial implementation, can be a follow-up), Option D (eager exchange at API time — requires knowing all MCP servers upfront, wastes exchanges for servers that may not be called, doesn't work well with dynamic sub-agent delegation)._

---

## Q3: Should token exchange configuration live inside `TransportConfig` or as a separate section?

Token exchange config (STS URL, client ID/secret, audience, scopes) needs to live somewhere in `tarsy.yaml`. It could be nested inside the transport config (since it's per-server and relates to how connections are made) or as a sibling section at the MCP server level.

### Option B: Sibling section at `MCPServerConfig` level

- **Pro:** Clean separation — transport handles connection mechanics, auth handles identity.
- **Pro:** `MCPServerConfig` already has domain-specific sections (masking, summarization).
- **Con:** `auth_mode` needs to influence transport behavior, creating a cross-reference between sibling config sections.

**Decision:** Option B — auth as a sibling section at `MCPServerConfig` level. Follows the existing pattern of domain-specific sections (`data_masking`, `summarization`). Auth is a cross-cutting concern, not a transport detail.

_Considered and rejected: Option A (inside `TransportConfig` — mixes transport with identity concerns, grows the struct), Option C (flat fields in `TransportConfig` — pollutes with fields that only apply to `token_exchange` mode)._

---

## Q4: Should TARSy perform the token exchange, or delegate it to the MCP server?

The Kubernetes MCP server can do its own STS token exchange (configured via `sts_client_id` etc.). TARSy could either: (a) perform the exchange itself and send the resulting token, or (b) forward the user's original token and let the MCP server exchange it.

### Option C: Support both — configurable per MCP server

- **Pro:** Maximum flexibility — use whichever approach fits each MCP server.
- **Pro:** `forward_user_token` effectively delegates exchange to the MCP server; `token_exchange` does it in TARSy.
- **Con:** Two code paths to maintain.

**Decision:** Option C — naturally falls out of Q1's decision to support both `forward_user_token` and `token_exchange` auth modes. When `forward_user_token` is used, the MCP server handles exchange (if needed). When `token_exchange` is used, TARSy does it. The operator chooses based on their MCP server's capabilities.

_Considered and rejected: Option A (TARSy always exchanges — forces STS config for servers that handle it natively), Option B (always delegate — only works with servers that have built-in STS support)._

---

## Q5: What should happen when per-user auth is configured but no user token is available?

Some sessions may not have a user token — e.g., system-initiated alerts, API clients using ServiceAccount tokens (where there's no OIDC token to forward), or sessions created in dev mode without auth. If an MCP server is configured with `auth_mode: token_exchange` or `forward_user_token`, but no user token exists, TARSy needs a policy.

### Option C: Configurable per MCP server — `fallback_to_shared: true/false`

- **Pro:** Operator chooses the policy per MCP server based on their security requirements.
- **Pro:** Sensitive servers can fail closed while less critical ones fall back gracefully.
- **Con:** One more config knob — adds complexity.

**Decision:** Option C — configurable per MCP server with `fallback_to_shared`, defaulting to `false` (fail closed). Operators explicitly opt into fallback for servers where shared credentials are acceptable. Prevents accidental security downgrades.

_Considered and rejected: Option A (always fail closed — breaks system-initiated sessions and dev mode), Option B (always fall back — silent security downgrade undermines the purpose of per-user auth)._

---

## Q6: Should token refresh be addressed in this sketch or deferred?

User tokens (JWTs) have expiration times, typically 5-15 minutes for access tokens. TARSy sessions can run for several minutes. If the user's token expires mid-session, MCP calls will fail. Token refresh could be handled in the initial implementation or deferred.

### Option A: Defer token refresh — assume tokens are valid for session duration

- **Pro:** Simpler initial implementation.
- **Pro:** Most sessions complete within a few minutes; short-lived tokens are usually sufficient.
- **Pro:** Token exchange itself produces fresh tokens — the exchanged token's lifetime starts at exchange time, not at the original token's issue time.
- **Con:** Long-running sessions or queued sessions may fail if the original token expires before exchange happens.

**Decision:** Option A — defer. Lazy exchange at call time means each exchanged token starts fresh. For typical 2-10 minute sessions this is sufficient. Queue delays causing original token expiry before first exchange is an edge case that can be addressed later with refresh token support or caching.

_Considered and rejected: Option B (refresh token support — significantly more complex, refresh tokens may not be available), Option C (exchange caching with lazy re-exchange — only works while original token is valid, adds caching complexity)._

---

## Q7: How should user tokens flow through the MCP client layer?

The MCP `Client` creates transport connections at initialization time (`InitializeServer`). User tokens need to reach the HTTP request layer. Currently, `bearerTokenTransport` sets a static token on all requests. With per-user auth, the token varies per session.

### Option A: Pass token via Go context

- **Pro:** Idiomatic Go — context carries request-scoped values. No changes to `Client`, `ToolExecutor`, or `ClientFactory` signatures.
- **Pro:** Token flows naturally through `CallTool(ctx, ...)` → `callToolOnce(ctx, ...)` → HTTP request.
- **Pro:** Thread-safe by design — each goroutine has its own context.
- **Con:** Implicit — token is a context value, not visible in type signatures.

**Decision:** Option A — pass via Go context. Standard mechanism for request-scoped values. A context-aware `RoundTripper` extracts the token per HTTP request and handles exchange transparently. Public API (`Client`, `ToolExecutor`, `ClientFactory`) stays unchanged.

_Considered and rejected: Option B (explicit parameter — requires signature changes across `CallTool`, `Execute`, `CreateToolExecutor`, etc., coupling the call chain to auth), Option C (per-client token at creation time — doesn't work because different MCP servers need different tokens after exchange, and exchange must happen at call time per server)._
