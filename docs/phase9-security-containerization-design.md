# Phase 9: Security and Containerization — Detailed Design

## Overview

Add OAuth2-proxy authentication and containerize TARSy for the podman-compose development environment. This phase replaces old TARSy's 5-container architecture (postgres, oauth2-proxy, backend, dashboard, reverse-proxy) with 4 containers: **oauth2-proxy**, **tarsy** (Go backend + dashboard), **llm-service** (Python gRPC), and **postgres**.

The podman-compose environment mirrors the production (OpenShift) topology: same container images, same inter-container networking, same health probes. The only difference is the orchestrator. In production, Phase 10 adds a `kube-rbac-proxy` sidecar for API client auth.

The key architectural simplification over old TARSy: since new TARSy's Go backend already serves the dashboard statically and exposes all API/WebSocket endpoints on a single port, there is no need for an nginx reverse proxy. OAuth2-proxy upstreams directly to the tarsy container.

### Goals

1. **OAuth2-proxy integration** — GitHub OAuth for browser-based access to dashboard and API
2. **WebSocket origin validation** — replace `InsecureSkipVerify` with configurable origin allowlist
3. **Security headers** — X-Frame-Options, X-Content-Type-Options, CORS
4. **Containerized tarsy** — multi-stage Dockerfile for Go backend + dashboard
5. **Containerized llm-service** — Dockerfile for Python gRPC LLM service
6. **podman-compose orchestration** — 4-service dev environment with health checks, mirroring prod topology
7. **Makefile targets** — container build, deploy, teardown workflow

### Non-Goals

- Kubernetes/OpenShift deployment (Phase 10)
- RBAC kube-proxy for API clients (Phase 10)
- JWT bearer token validation (replaced by rbac-kube-proxy in Phase 10)
- Prometheus metrics (Phase 11)
- mTLS between containers (overkill for dev; OpenShift handles in-pod TLS)

---

## Architecture

### Container Topology (podman-compose)

```
┌──────────────────────────────────────────────────────────┐
│                    User (Browser)                         │
└──────────────────────┬───────────────────────────────────┘
                       │ HTTP :8080
                       ▼
┌──────────────────────────────────────────────────────────┐
│              oauth2-proxy (:4180 internal)                │
│                                                          │
│  - GitHub OAuth provider                                 │
│  - Passes X-Forwarded-User/Email/Groups/Access-Token     │
│  - Skips auth for /health                                │
│  - Returns 401 (not redirect) for /api/* and /ws*        │
│  - WebSocket passthrough (Upgrade + Connection headers)  │
│                                                          │
│  Upstream: http://tarsy:8080                              │
└──────────────────────┬───────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────┐
│                   tarsy (:8080)                           │
│                                                          │
│  Go Backend (single process)                             │
│  - Dashboard static serving (/assets/*, SPA fallback)    │
│  - REST API (/api/v1/*)                                  │
│  - WebSocket (/api/v1/ws)                                │
│  - Health check (/health)                                │
│  - Worker pool, event streaming                          │
│                                                          │
│  LLM_SERVICE_ADDR=llm-service:50051                      │
└─────────┬────────────────────────────────────────────────┘
          │
          │ gRPC :50051 (container network)
          ▼
┌──────────────────────────────────────────────────────────┐
│               llm-service (:50051)                        │
│                                                          │
│  Python gRPC Server (single process)                     │
│  - Stateless LLM proxy                                   │
│  - GoogleNativeProvider, LangChainProvider                │
│  - Health: gRPC reflection or TCP port check             │
└──────────────────────────────────────────────────────────┘
          │
┌─────────▼────────────────────────────────────────────────┐
│                   postgres (:5432)                        │
│  - PostgreSQL 17 Alpine                                  │
│  - Persistent volume                                     │
│  - Health: pg_isready                                    │
└──────────────────────────────────────────────────────────┘
```

### Production Topology (OpenShift, Phase 10)

```
Same containers as above, plus kube-rbac-proxy sidecar:

┌─ Pod ──────────────────────────────────────────────┐
│  oauth2-proxy     (browser auth)                   │
│  kube-rbac-proxy  (API client auth, Phase 10)      │
│  tarsy            (Go backend + dashboard)         │
│  llm-service      (Python gRPC)                    │
└────────────────────────────────────────────────────┘
  + postgres (separate pod or managed DB like AWS RDS)
```

### Request Flows

**Browser access (authenticated):**
```
Browser → :8080 (oauth2-proxy)
  → GitHub OAuth redirect (first time) → callback → set _tarsy_oauth2 cookie
  → Proxy request with X-Forwarded-User/Email to tarsy:8080
  → Go backend serves dashboard or processes API request
```

**WebSocket (authenticated, cookie-based):**
```
Browser → :8080 (oauth2-proxy)
  → Cookie validates → proxy with Upgrade + Connection headers
  → tarsy:8080 /api/v1/ws → WebSocket Accept with origin validation
```

**Health check (unauthenticated):**
```
Monitoring → :8080 (oauth2-proxy) → /health skips auth → tarsy:8080/health
```

**LLM service (inter-container gRPC):**
```
Go backend (tarsy) → llm-service:50051 (gRPC, insecure, compose network)
```

### Comparison with Old TARSy

| Aspect | Old TARSy | New TARSy |
|--------|-----------|-----------|
| Containers | 5 (postgres, oauth2-proxy, backend, dashboard, reverse-proxy) | 4 (postgres, oauth2-proxy, tarsy, llm-service) |
| Nginx | Reverse proxy merging dashboard + oauth2-proxy | Not needed — Go serves everything |
| Dashboard | Separate container (nginx) | Built into tarsy (Go static serving) |
| LLM service | Part of backend (Python monolith) | Separate container (gRPC microservice) |
| OAuth2 upstream | backend:8000 (Python FastAPI) | tarsy:8080 (Go) |
| JWT for API clients | RS256 with JWKS endpoint | Deferred to Phase 10 (kube-rbac-proxy) |
| Entry point | Port 8080 (nginx) | Port 8080 (oauth2-proxy) |

---

## 9.1: OAuth2-Proxy Integration

### OAuth2-Proxy Configuration

Reuse the existing `deploy/config/oauth2-proxy.cfg.template` with modifications for the new architecture:

```ini
# Provider Configuration
provider = "github"
client_id = "{{OAUTH2_CLIENT_ID}}"
client_secret = "{{OAUTH2_CLIENT_SECRET}}"

# Cookie Configuration
cookie_secret = "{{OAUTH2_COOKIE_SECRET}}"
cookie_secure = {{COOKIE_SECURE}}
cookie_httponly = true
cookie_samesite = "lax"
cookie_name = "_tarsy_oauth2"

# Session Configuration
session_cookie_minimal = true

# Network
http_address = "0.0.0.0:4180"

# Upstream: tarsy Go backend (serves dashboard + API + WebSocket)
upstreams = ["http://tarsy:8080/"]

# Redirect URL (replaced by Makefile from env)
redirect_url = "{{OAUTH2_PROXY_REDIRECT_URL}}"

# Email Domain Restriction
# email_domains = ["*"]

# GitHub Organization/Team Restriction (optional)
# github_org = "{{GITHUB_ORG}}"
# github_team = "{{GITHUB_TEAM}}"

# Scopes
scope = "user:email read:org"

# Headers — pass user identity to upstream
set_xauthrequest = true
pass_user_headers = true
pass_access_token = true
pass_authorization_header = true

# Prefix
proxy_prefix = "/oauth2"

# Logging
request_logging = true
auth_logging = true
standard_logging = true

# Skip auth for health endpoint
skip_auth_routes = [
    "^/health$"
]

# API routes return 401 instead of redirect (for XHR/fetch clients)
api_routes = [
    "^/api/",
    "^/ws"
]
```

**Key differences from old TARSy:**

1. **Single upstream** — `http://tarsy:8080/` instead of `http://backend:8000/` — oauth2-proxy proxies everything (dashboard + API) to one service
2. **No JWT skip** — removed `skip_jwt_bearer_tokens`, `oidc_jwks_url`, `extra_jwt_issuers` — JWT-based API auth will be handled by rbac-kube-proxy in Phase 10, not by oauth2-proxy
3. **`api_routes` added** — ensures `/api/*` and `/ws*` get 401 responses instead of HTML redirects, enabling proper frontend error handling

### Config Generation (Makefile)

Add a `deploy/config/oauth2-proxy.cfg` generation target that substitutes placeholders from `deploy/config/oauth.env`:

```makefile
# deploy/config/oauth.env — sourced for variable substitution
# Contents: OAUTH2_CLIENT_ID, OAUTH2_CLIENT_SECRET, OAUTH2_COOKIE_SECRET,
#           OAUTH2_PROXY_REDIRECT_URL, COOKIE_SECURE, GITHUB_ORG, GITHUB_TEAM

.PHONY: oauth2-config
oauth2-config: ## Generate oauth2-proxy.cfg from template
	@if [ ! -f deploy/config/oauth.env ]; then \
		echo "ERROR: deploy/config/oauth.env not found. Copy from oauth.env.example"; \
		exit 1; \
	fi
	@source deploy/config/oauth.env && \
		sed -e "s|{{OAUTH2_CLIENT_ID}}|$${OAUTH2_CLIENT_ID}|g" \
		    -e "s|{{OAUTH2_CLIENT_SECRET}}|$${OAUTH2_CLIENT_SECRET}|g" \
		    -e "s|{{OAUTH2_COOKIE_SECRET}}|$${OAUTH2_COOKIE_SECRET}|g" \
		    -e "s|{{OAUTH2_PROXY_REDIRECT_URL}}|$${OAUTH2_PROXY_REDIRECT_URL}|g" \
		    -e "s|{{COOKIE_SECURE}}|$${COOKIE_SECURE:-false}|g" \
		    deploy/config/oauth2-proxy.cfg.template > deploy/config/oauth2-proxy.cfg
	@echo "Generated deploy/config/oauth2-proxy.cfg"
```

### OAuth Environment File

Add `deploy/config/oauth.env.example`:

```bash
# OAuth2 Proxy Configuration
# Copy to oauth.env and fill in your values:
#   cp oauth.env.example oauth.env

OAUTH2_CLIENT_ID=your-github-oauth-app-client-id
OAUTH2_CLIENT_SECRET=your-github-oauth-app-client-secret
OAUTH2_COOKIE_SECRET=generate-a-32-char-random-string
OAUTH2_PROXY_REDIRECT_URL=http://localhost:8080/oauth2/callback

# Set to "true" for HTTPS deployments, "false" for local dev
COOKIE_SECURE=false

# Optional: restrict to GitHub org/team
# GITHUB_ORG=your-org
# GITHUB_TEAM=your-team
```

### Author Extraction (Already Implemented)

The existing `pkg/api/auth.go` already extracts user identity from oauth2-proxy headers:

```go
// extractAuthor extracts the author from oauth2-proxy headers.
// Priority: X-Forwarded-User > X-Forwarded-Email > "api-client"
func extractAuthor(c *echo.Context) string {
    if user := c.Request().Header.Get("X-Forwarded-User"); user != "" {
        return user
    }
    if email := c.Request().Header.Get("X-Forwarded-Email"); email != "" {
        return email
    }
    return "api-client"
}
```

No changes needed — oauth2-proxy sets `X-Forwarded-User` and `X-Forwarded-Email` via `pass_user_headers = true` and `set_xauthrequest = true`.

### Dashboard Auth (Already Implemented)

The dashboard auth service (`web/dashboard/src/services/auth.ts`) and context (`web/dashboard/src/contexts/AuthContext.tsx`) already handle:

- Checking auth status via protected API call
- Getting user info from `/oauth2/userinfo` endpoint
- Login redirect to `/oauth2/sign_in?rd=<return_url>`
- Logout via `/oauth2/sign_out?rd=<redirect_url>`
- Graceful degradation when oauth2-proxy is not configured

The Vite config (`web/dashboard/vite.config.ts`) already supports container mode with proxy to oauth2-proxy for `/oauth2`, `/api`, and `/health`.

---

## 9.2: WebSocket Origin Validation

### Current State

```go
// pkg/api/handler_ws.go — current (insecure)
conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
    InsecureSkipVerify: true,
})
```

### Design

Replace `InsecureSkipVerify` with an `OriginPatterns` allowlist derived from `system.dashboard_url` config:

```go
// pkg/api/handler_ws.go — updated
func (s *Server) wsHandler(c *echo.Context) error {
    if s.connManager == nil {
        return echo.NewHTTPError(503, "WebSocket not available")
    }

    conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
        OriginPatterns: s.wsOriginPatterns,
    })
    if err != nil {
        return err
    }

    s.connManager.HandleConnection(c.Request().Context(), conn)
    return nil
}
```

### Origin Pattern Resolution

Origin patterns are computed once at server startup from `system.dashboard_url` and an optional `system.allowed_ws_origins` list:

```go
// pkg/api/server.go

func (s *Server) resolveWSOriginPatterns() []string {
    var patterns []string

    // Always allow the configured dashboard URL's origin
    if s.cfg.DashboardURL != "" {
        if u, err := url.Parse(s.cfg.DashboardURL); err == nil {
            patterns = append(patterns, u.Host)
        }
    }

    // Always allow localhost variants for development
    patterns = append(patterns, "localhost:*", "127.0.0.1:*")

    // Additional configured origins
    patterns = append(patterns, s.cfg.AllowedWSOrigins...)

    return patterns
}
```

The `coder/websocket` library's `OriginPatterns` field supports glob patterns (e.g., `localhost:*` matches any port). When the list is empty, **all origins are rejected** — this is the safe default.

### Configuration

```yaml
system:
  dashboard_url: "https://tarsy.example.com"  # Already exists
  allowed_ws_origins: []  # Optional additional origins (rare)
```

Add to `pkg/config/system.go`:

```go
type SystemConfig struct {
    // ... existing fields ...
    AllowedWSOrigins []string `yaml:"allowed_ws_origins"`
}
```

---

## 9.3: Security Headers and CORS

### CORS Middleware

Add Echo CORS middleware to the Go backend. With oauth2-proxy in front, CORS is primarily relevant for development (Vite dev server on port 5173 talking to Go backend on port 8080). In production, oauth2-proxy handles the same-origin flow.

```go
// pkg/api/server.go — in setupRoutes()

s.echo.Use(middleware.CORSWithConfig(middleware.CORSConfig{
    AllowOriginFunc: s.corsAllowOriginFunc(),
    AllowMethods:    []string{http.MethodGet, http.MethodPost, http.MethodOptions},
    AllowHeaders:    []string{"Content-Type", "Accept", "Authorization"},
    AllowCredentials: true,
    MaxAge:           3600,
}))
```

```go
func (s *Server) corsAllowOriginFunc() func(origin string) bool {
    allowed := map[string]bool{
        "http://localhost:5173":  true,  // Vite dev server
        "http://localhost:8080":  true,  // Direct Go backend
        "http://127.0.0.1:5173": true,
        "http://127.0.0.1:8080": true,
    }

    if s.cfg.DashboardURL != "" {
        allowed[s.cfg.DashboardURL] = true
    }

    return func(origin string) bool {
        return allowed[origin]
    }
}
```

### Security Headers Middleware

Add response headers to protect against common attacks:

```go
// pkg/api/middleware.go

func securityHeaders() echo.MiddlewareFunc {
    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c *echo.Context) error {
            h := c.Response().Header()
            h.Set("X-Frame-Options", "DENY")
            h.Set("X-Content-Type-Options", "nosniff")
            h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
            h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
            return next(c)
        }
    }
}
```

Registered in `setupRoutes()`:

```go
s.echo.Use(securityHeaders())
s.echo.Use(middleware.BodyLimit(2 * 1024 * 1024))
s.echo.Use(middleware.CORSWithConfig(...))
```

**Note:** No CSP header in this phase — CSP requires careful tuning for MUI's inline styles and is a maintenance burden. X-Frame-Options + X-Content-Type-Options provide the most impact for minimal effort.

---

## 9.4: Dockerfiles

Two separate Dockerfiles — one per container image. Each container runs a single process.

### Tarsy Dockerfile (Go backend + dashboard)

`Dockerfile` (project root):

```dockerfile
# ─────────────────────────────────────────────────────────
# Stage 1: Build Go binary
# ─────────────────────────────────────────────────────────
FROM docker.io/library/golang:1.25-alpine AS go-builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/
COPY ent/ ent/
COPY proto/ proto/

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /tarsy ./cmd/tarsy

# ─────────────────────────────────────────────────────────
# Stage 2: Build dashboard
# ─────────────────────────────────────────────────────────
FROM docker.io/library/node:24-alpine AS dashboard-builder

WORKDIR /build

COPY web/dashboard/package*.json ./
RUN npm ci --include=dev

COPY web/dashboard/ .

ARG VERSION=dev
ENV VITE_APP_VERSION=${VERSION}

RUN npm run build

# ─────────────────────────────────────────────────────────
# Stage 3: Runtime image
# ─────────────────────────────────────────────────────────
FROM docker.io/library/alpine:3.21 AS runtime

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -g 65532 -S tarsy \
    && adduser -u 65532 -S tarsy -G tarsy -s /sbin/nologin

WORKDIR /app

COPY --from=go-builder /tarsy /app/bin/tarsy
COPY --from=dashboard-builder /build/dist /app/dashboard

RUN mkdir -p /app/config \
    && chown -R tarsy:tarsy /app

USER 65532:65532

ENV HTTP_PORT=8080 \
    DASHBOARD_DIR=/app/dashboard \
    CONFIG_DIR=/app/config \
    LLM_SERVICE_ADDR=llm-service:50051

EXPOSE 8080

CMD ["/app/bin/tarsy", "--config-dir=/app/config", "--dashboard-dir=/app/dashboard"]
```

**Design notes:**
- Alpine runtime (not Python) — the Go binary is statically linked, keeping the image small (~30MB)
- No `tini` needed — single process, Go handles signals natively
- `LLM_SERVICE_ADDR` defaults to `llm-service:50051` (compose service name)

### LLM Service Dockerfile (Python gRPC)

`llm-service/Dockerfile`:

```dockerfile
FROM docker.io/library/python:3.13-slim

RUN pip install --no-cache-dir uv

WORKDIR /app

COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev

COPY . .

RUN groupadd --gid 65532 llm \
    && useradd --uid 65532 --gid 65532 --no-create-home --shell /bin/false llm

USER 65532:65532

EXPOSE 50051

CMD ["/app/.venv/bin/python", "-m", "llm.server"]
```

**Design notes:**
- Single-stage — Python needs the runtime (no compile step to separate)
- Same non-root UID (65532) as tarsy for consistency
- Dependencies cached via `uv sync` layer before copying source

---

## 9.5: podman-compose Orchestration

### Updated `deploy/podman-compose.yml`

```yaml
services:
  postgres:
    image: docker.io/library/postgres:17-alpine
    container_name: tarsy-postgres
    environment:
      POSTGRES_USER: tarsy
      POSTGRES_PASSWORD: tarsy_dev_password
      POSTGRES_DB: tarsy
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./postgres-init:/docker-entrypoint-initdb.d
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tarsy"]
      interval: 5s
      timeout: 5s
      retries: 5

  llm-service:
    build:
      context: ../llm-service
      dockerfile: Dockerfile
    container_name: tarsy-llm
    env_file:
      - ./config/.env
    healthcheck:
      test: ["CMD-SHELL", "python3 -c \"import socket; s=socket.socket(); s.settimeout(2); s.connect(('127.0.0.1',50051)); s.close()\""]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 15s
    restart: on-failure:3

  tarsy:
    build:
      context: ..
      dockerfile: Dockerfile
    container_name: tarsy-app
    env_file:
      - ./config/.env
    environment:
      DB_HOST: postgres
      DB_PORT: "5432"
      DB_USER: tarsy
      DB_PASSWORD: tarsy_dev_password
      DB_NAME: tarsy
      DB_SSLMODE: disable
      LLM_SERVICE_ADDR: llm-service:50051
    volumes:
      - ./config:/app/config:ro
    depends_on:
      postgres:
        condition: service_healthy
      llm-service:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:8080/health >/dev/null 2>&1 || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 5
      start_period: 30s
    restart: on-failure:3

  oauth2-proxy:
    image: quay.io/oauth2-proxy/oauth2-proxy:latest
    container_name: tarsy-oauth2
    command:
      - "oauth2-proxy"
      - "--config=/config/oauth2-proxy.cfg"
      - "--skip-auth-preflight=true"
      - "--custom-templates-dir=/templates"
    ports:
      - "8080:4180"
    volumes:
      - ./config/oauth2-proxy.cfg:/config/oauth2-proxy.cfg:ro
      - ./config/templates:/templates:ro
    depends_on:
      tarsy:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "wget -qO /dev/null http://localhost:4180/ping || exit 1"]
      interval: 10s
      timeout: 5s
      retries: 3

volumes:
  postgres_data:
```

### Key Design Decisions

1. **4 containers, matching prod** — Same images and inter-container networking as the OpenShift pod (minus kube-rbac-proxy, which is Phase 10).

2. **Port mapping** — Only oauth2-proxy exposes port 8080 externally (mapped from 4180). Tarsy, llm-service, and postgres are internal-only, accessible by service name within the compose network.

3. **Health check chain** — postgres + llm-service must be healthy → tarsy starts and becomes healthy → oauth2-proxy starts. This prevents startup race conditions.

4. **gRPC over compose network** — `LLM_SERVICE_ADDR=llm-service:50051` — the Go backend connects to the Python LLM service via the compose network, same as it would connect via `localhost` in an OpenShift pod.

5. **Config mounting** — `deploy/config/` mounted as `/app/config:ro` gives the tarsy container access to `tarsy.yaml`, `.env`, and `llm-providers.yaml`.

6. **env_file on llm-service** — The LLM service needs API keys (e.g., `GOOGLE_API_KEY`) from the `.env` file. It reads env vars at runtime based on the `api_key_env` fields sent via gRPC.

### Development Without OAuth2 (Backward Compatibility)

The existing `make dev` workflow (host-based, no containers) continues to work unchanged. No oauth2-proxy, no containers — just `./bin/tarsy` on localhost. The dashboard's `AuthContext` gracefully degrades when oauth2-proxy is absent (hides auth UI, allows anonymous access).

For container-based dev with auth, use the new `make containers-deploy` target (see 9.6).

---

## 9.6: Makefile Targets

Add `make/containers.mk`:

```makefile
# =============================================================================
# Container Orchestration (podman-compose)
# =============================================================================

COMPOSE := COMPOSE_PROJECT_NAME=tarsy podman compose -f deploy/podman-compose.yml

# ── Build ────────────────────────────────────────────────

.PHONY: containers-build
containers-build: ## Build tarsy and llm-service container images
	@echo -e "$(YELLOW)Building container images...$(NC)"
	@podman build -t tarsy:dev -f Dockerfile .
	@podman build -t tarsy-llm:dev -f llm-service/Dockerfile llm-service/
	@echo -e "$(GREEN)✅ Container images built: tarsy:dev, tarsy-llm:dev$(NC)"

# ── Deploy ───────────────────────────────────────────────

.PHONY: containers-deploy
containers-deploy: oauth2-config ## Deploy all containers (build + start)
	@echo -e "$(YELLOW)Deploying containers...$(NC)"
	@$(COMPOSE) up -d --build
	@echo -e "$(GREEN)✅ Containers deployed$(NC)"
	@echo -e "$(BLUE)  Dashboard: http://localhost:8080$(NC)"
	@echo -e "$(BLUE)  Health:    http://localhost:8080/health$(NC)"

.PHONY: containers-deploy-fresh
containers-deploy-fresh: containers-clean containers-deploy ## Clean rebuild and deploy

.PHONY: containers-redeploy
containers-redeploy: oauth2-config ## Rebuild and restart tarsy container only
	@echo -e "$(YELLOW)Redeploying tarsy container...$(NC)"
	@$(COMPOSE) up -d --build tarsy
	@echo -e "$(GREEN)✅ Tarsy container redeployed$(NC)"

# ── Status ───────────────────────────────────────────────

.PHONY: containers-status
containers-status: ## Show container status
	@$(COMPOSE) ps

.PHONY: containers-logs
containers-logs: ## Follow container logs
	@$(COMPOSE) logs -f

.PHONY: containers-logs-tarsy
containers-logs-tarsy: ## Follow tarsy container logs
	@$(COMPOSE) logs -f tarsy

# ── Stop / Clean ─────────────────────────────────────────

.PHONY: containers-stop
containers-stop: ## Stop all containers
	@$(COMPOSE) down
	@echo -e "$(GREEN)✅ Containers stopped$(NC)"

.PHONY: containers-clean
containers-clean: ## Stop containers and remove volumes
	@$(COMPOSE) down -v
	@echo -e "$(GREEN)✅ Containers and volumes cleaned$(NC)"

.PHONY: containers-db-reset
containers-db-reset: ## Reset database (stop, remove volume, restart)
	@$(COMPOSE) stop postgres
	@$(COMPOSE) rm -f postgres
	@podman volume rm tarsy_postgres_data 2>/dev/null || true
	@$(COMPOSE) up -d postgres
	@echo -e "$(GREEN)✅ Database reset$(NC)"

# ── Config Generation ────────────────────────────────────

.PHONY: oauth2-config
oauth2-config: ## Generate oauth2-proxy.cfg from template + oauth.env
	@if [ ! -f deploy/config/oauth.env ]; then \
		echo -e "$(RED)ERROR: deploy/config/oauth.env not found$(NC)"; \
		echo "  Copy from oauth.env.example:"; \
		echo "    cp deploy/config/oauth.env.example deploy/config/oauth.env"; \
		exit 1; \
	fi
	@set -a && source deploy/config/oauth.env && set +a && \
		sed -e "s|{{OAUTH2_CLIENT_ID}}|$${OAUTH2_CLIENT_ID}|g" \
		    -e "s|{{OAUTH2_CLIENT_SECRET}}|$${OAUTH2_CLIENT_SECRET}|g" \
		    -e "s|{{OAUTH2_COOKIE_SECRET}}|$${OAUTH2_COOKIE_SECRET}|g" \
		    -e "s|{{OAUTH2_PROXY_REDIRECT_URL}}|$${OAUTH2_PROXY_REDIRECT_URL:-http://localhost:8080/oauth2/callback}|g" \
		    -e "s|{{COOKIE_SECURE}}|$${COOKIE_SECURE:-false}|g" \
		    deploy/config/oauth2-proxy.cfg.template > deploy/config/oauth2-proxy.cfg
	@echo -e "$(GREEN)Generated deploy/config/oauth2-proxy.cfg$(NC)"
```

---

## 9.7: Health Check Enhancements

### OAuth2-Proxy Health Path

OAuth2-proxy exposes `/ping` for its own health. The existing `/health` endpoint is skipped by oauth2-proxy auth (`skip_auth_routes`), allowing external monitoring without authentication.

### Tarsy Container Health

The container health check hits `/health` on the Go backend. This endpoint already checks database connectivity, worker pool status, MCP health, and system warnings.

**Change required:** Remove MCP server names and MCP health details from the `/health` response — server names can be sensitive and the endpoint is unauthenticated. The `status` field still reflects MCP health (`degraded` when MCP is unhealthy), but without exposing which servers or their names. MCP-specific details remain behind the authenticated `GET /api/v1/system/mcp-servers` endpoint. System warnings also need to be stripped of MCP server IDs (the `server_id` field on `SystemWarning`).

Before (current):
```json
{
  "status": "degraded",
  "version": "tarsy/a3f8c2d1",
  "database": { ... },
  "worker_pool": { ... },
  "mcp_health": { "kubernetes-server": { "healthy": false, ... } },
  "warnings": [{ "category": "mcp_health", "server_id": "kubernetes-server", ... }]
}
```

After:
```json
{
  "status": "degraded",
  "version": "tarsy/a3f8c2d1",
  "database": { ... },
  "worker_pool": { ... }
}
```

The dashboard's `useVersionMonitor` hook only reads `version` and `status` from `/health` — unaffected by this change.

### LLM Service Health

The llm-service container health check uses a TCP port check on 50051 (gRPC). The `start_period: 15s` covers Python startup + dependency loading.

### Startup Probe Equivalent

The compose `start_period` values allow each container time to become ready:
- llm-service: 15s (Python startup + import time)
- tarsy: 30s (Go startup + MCP server validation)
- oauth2-proxy: no start_period needed (fast startup, depends on tarsy being healthy)

---

## Implementation Plan

### Phase 9.1: WebSocket Origin Validation
1. Add `AllowedWSOrigins` field to system config
2. Add `resolveWSOriginPatterns()` to Server
3. Update `wsHandler` to use `OriginPatterns` instead of `InsecureSkipVerify`
4. Update config loader and validator
5. Add unit tests for origin pattern resolution

### Phase 9.2: Security Headers and CORS
1. Create `securityHeaders()` middleware
2. Add CORS middleware with `DashboardURL`-based origin allowlist
3. Register both middlewares in `setupRoutes()`
4. Add unit tests for middleware

### Phase 9.3: OAuth2-Proxy Configuration
1. Update `deploy/config/oauth2-proxy.cfg.template` with new architecture settings
2. Create `deploy/config/oauth.env.example`
3. Port custom sign-in template from old TARSy to `deploy/config/templates/sign_in.html`
4. Add `oauth2-config` Makefile target
5. Add `.gitignore` entries for generated `oauth2-proxy.cfg` and `oauth.env`

### Phase 9.4: Dockerfiles
1. Create `Dockerfile` at project root (Go backend + dashboard, multi-stage)
2. Create `llm-service/Dockerfile` (Python LLM service)
3. Test local builds: `podman build -t tarsy:dev .` and `podman build -t tarsy-llm:dev llm-service/`
4. Verify both containers start and communicate via gRPC

### Phase 9.5: podman-compose Orchestration
1. Update `deploy/podman-compose.yml` with tarsy, llm-service, and oauth2-proxy services
2. Add `make/containers.mk` with build/deploy/status/clean targets
3. Update root `Makefile` to include `containers.mk`
4. End-to-end test: `make containers-deploy` → browser to localhost:8080 → OAuth login → dashboard

### Phase 9.6: Container Development Workflow
1. Update Vite config for container mode proxy targets (already mostly done)
2. Add container mode support to dashboard `.env.example`
3. Update `deploy/config/README.md` with container setup instructions
4. Verify existing `make dev` still works without containers

---

## File Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `Dockerfile` | Multi-stage build for tarsy container (Go + dashboard) |
| `llm-service/Dockerfile` | Python LLM service container |
| `deploy/config/oauth.env.example` | OAuth2 env var template |
| `deploy/config/templates/sign_in.html` | Custom OAuth2 sign-in page (ported from old TARSy) |
| `make/containers.mk` | Container orchestration Makefile targets |
| `pkg/api/middleware.go` | Security headers middleware |

### Modified Files

| File | Changes |
|------|---------|
| `deploy/podman-compose.yml` | Add tarsy, llm-service, and oauth2-proxy services |
| `deploy/config/oauth2-proxy.cfg.template` | Update for new architecture (single upstream, api_routes) |
| `pkg/api/server.go` | Add CORS, security headers middleware; WebSocket origin patterns |
| `pkg/api/handler_ws.go` | Replace `InsecureSkipVerify` with `OriginPatterns` |
| `pkg/config/system.go` | Add `AllowedWSOrigins` field |
| `pkg/api/handler_health.go` | Strip MCP details and server IDs from unauthenticated response |
| `pkg/config/loader.go` | Load `AllowedWSOrigins` |
| `Makefile` | Include `make/containers.mk` |
| `.gitignore` | Add `oauth2-proxy.cfg`, `oauth.env` |
| `deploy/config/README.md` | Update with container setup docs |

### Unchanged (Already Implemented)

| File | Why No Changes |
|------|----------------|
| `pkg/api/auth.go` | `extractAuthor()` already handles X-Forwarded-* headers |
| `web/dashboard/src/services/auth.ts` | Already handles oauth2-proxy flows |
| `web/dashboard/src/contexts/AuthContext.tsx` | Already supports graceful degradation |
| `web/dashboard/vite.config.ts` | Already supports container mode proxy |
| `web/dashboard/.env.example` | Already documents container mode vars |

---

## Testing Strategy

### Unit Tests

- `pkg/api/middleware_test.go` — security headers applied, correct values
- `pkg/api/server_test.go` — CORS origin allowlist, WebSocket origin patterns
- `pkg/api/handler_ws_test.go` — origin validation accepts allowed, rejects others (if testable without full WS)

### Integration Tests (Manual)

1. **Host dev mode** (`make dev`): Verify dashboard works without oauth2-proxy, auth UI hidden, APIs accessible
2. **Container mode** (`make containers-deploy`):
   - OAuth login redirect works
   - Dashboard loads after authentication
   - API calls pass with cookie
   - WebSocket connects and streams events
   - Health endpoint accessible without auth
   - `make containers-logs-tarsy` shows Go backend logs
   - `make containers-logs` shows all container logs (tarsy + llm-service + oauth2-proxy)
3. **Security headers**: Inspect response headers in browser DevTools

### E2E Tests

Existing e2e tests continue to work unchanged — they run in-process without oauth2-proxy. The e2e test harness uses `StartWithListener` which bypasses the container/proxy layer entirely.
