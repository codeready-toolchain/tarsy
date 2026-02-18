# Phase 9: Security and Containerization — Open Questions

Questions where the design departs from old TARSy or involves non-obvious trade-offs.

---

## Q1: Container Architecture — Combined vs Separate Containers for Go + Python

**DECIDED: B — Separate `tarsy` and `llm-service` containers.**

The podman-compose dev environment should mirror the production (OpenShift) topology as closely as possible — that's its primary purpose. In production, each container in the pod has independent restarts, resource limits, health probes, and logs. Keeping the same separation in dev means the same container images and inter-container networking (gRPC over `llm-service:50051`) are tested locally.

Podman-compose (4 containers): oauth2-proxy, tarsy (Go + dashboard), llm-service (Python gRPC), postgres.
Production OpenShift (4 containers in pod + optional DB): oauth2-proxy, kube-rbac-proxy (Phase 10), tarsy, llm-service. Database is either a separate pod or managed (e.g., AWS RDS).

---

## Q2: Nginx Reverse Proxy — Needed or Not?

**DECIDED: A — No nginx. OAuth2-proxy is the entry point.**

Old TARSy needed nginx to merge separate dashboard (nginx container) and backend (Python container) under one port. New TARSy's Go backend serves everything (dashboard + API + WebSocket) on a single port, so oauth2-proxy upstreams directly to tarsy — no nginx needed. In production, OpenShift Routes handle TLS termination and external routing, eliminating CORS entirely (single origin).

---

## Q3: WebSocket Through OAuth2-Proxy — Configuration Approach

**DECIDED: A — WebSocket via oauth2-proxy with `--skip-auth-preflight`.**

OAuth2-proxy validates the cookie on the initial HTTP upgrade request, then proxies the WebSocket connection through to the Go backend. Go backend validates origin via `OriginPatterns`. Same approach as old TARSy. The `--skip-auth-preflight=true` flag ensures CORS preflight OPTIONS requests pass through.

---

## Q4: JWT Bearer Token Support — Include or Defer?

**DECIDED: A — No token support. Deferred entirely to Phase 10 (kube-rbac-proxy).**

Old TARSy had custom JWT infrastructure (RS256 keys, JWKS endpoint, token generation CLI). New TARSy drops all of that — programmatic API access will be handled by `kube-rbac-proxy` in Phase 10, which validates Kubernetes/OpenShift ServiceAccount tokens using native RBAC. No custom JWT code in TARSy at all.

---

## Q5: Health Endpoint Auth — Skip or Protect?

**DECIDED: A — Skip auth for /health, but strip MCP info from the response.**

Health endpoint remains unauthenticated (`skip_auth_routes = ["^/health$"]`) — standard practice for container health checks and K8s probes. However, MCP server names, MCP health details, and system warnings are removed from the `/health` response since server names can be sensitive (warning messages may also reference MCP server names). The `/health` response will only include: `status` (healthy/degraded/unhealthy), `version`, `database`, and `worker_pool`. MCP-specific health data stays behind the authenticated `GET /api/v1/system/mcp-servers` endpoint, which the dashboard already uses.

---

## Q6: Process Management in Container — Shell Script vs Supervisord vs Dedicated Init

**DECIDED: N/A — Resolved by Q1 (separate containers).**

With separate `tarsy` and `llm-service` containers, each has exactly one process. No process manager or entrypoint script needed — standard `CMD` in each Dockerfile. Independent restarts via compose's `restart: on-failure:3`.

---

## Q7: OAuth2-Proxy `api_routes` Configuration

**DECIDED: A — `api_routes = ["^/api/", "^/ws"]`.**

When a user's OAuth cookie expires while the dashboard SPA is still open in the browser, subsequent fetch/XHR calls need a clean 401 (not a 302 redirect to GitHub that fetch follows silently, returning unparseable HTML). With `api_routes`, the dashboard's `AuthService` catches the 401 and automatically redirects the user to re-authenticate — no confusing errors or manual refresh needed. Page-level navigation (initial load, static assets) still gets the standard redirect-to-GitHub flow.

---

## Q8: CORS Configuration — Where to Handle?

**DECIDED: A — Go backend CORS middleware.**

Echo CORS middleware with `DashboardURL`-based origin allowlist, plus localhost variants for dev. This is needed regardless of oauth2-proxy — the Vite dev server (port 5173 → 8080) is a cross-origin scenario. OAuth2-proxy's `--skip-auth-preflight=true` forwards OPTIONS requests to the backend, which responds with proper CORS headers. In production, OpenShift Routes make everything same-origin, so CORS is effectively a no-op there.

---

## Q9: Custom OAuth2 Sign-In Page

**DECIDED: B — Port custom template from old TARSy.**

Copy `config/templates/sign_in.html` and logo from old TARSy, mount as volume in oauth2-proxy container, and set `custom_templates_dir = "/templates"` in the config. Provides branded sign-in experience consistent with old TARSy.

---

## Q10: Vite Dev Server in Container Mode

**DECIDED: A — Container includes pre-built dashboard only.**

Dashboard is built in the Dockerfile's multi-stage build and served by the Go backend. Two dev modes: `make dev` for fast iteration (host-based, no containers, no auth), `make containers-deploy` for full-stack integration testing with OAuth (rebuild container on changes). No Vite dev container in compose.

---

## Q11: Security Headers — Scope and CSP

**DECIDED: A — Basic security headers, no CSP.**

`X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy: camera=(), microphone=(), geolocation=()`. No `Content-Security-Policy` — CSP requires careful tuning for MUI's runtime styles and React's dynamic loading, disproportionate effort for an internal tool. Revisit if TARSy becomes externally accessible.

---

## Q12: `.env` File Strategy — Single vs Separate for OAuth

**DECIDED: A — Separate `oauth.env` for OAuth2-proxy variables.**

`deploy/config/.env` stays as the app-level config (API keys, DB creds). `deploy/config/oauth.env` holds OAuth2-proxy config generation vars (`OAUTH2_CLIENT_ID`, `OAUTH2_CLIENT_SECRET`, `OAUTH2_COOKIE_SECRET`). These are only consumed by the Makefile `oauth2-config` target to generate `oauth2-proxy.cfg` — they never enter the tarsy container's environment.
