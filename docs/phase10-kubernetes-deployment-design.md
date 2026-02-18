# Phase 10: Kubernetes/OpenShift Deployment — Detailed Design

## Overview

Deploy TARSy to OpenShift using Kustomize manifests, replacing old TARSy's multi-deployment architecture with a single-pod, multi-container design. This phase takes the universal container images built in Phase 9 and defines the production Kubernetes/OpenShift resources to run them.

Old TARSy used 3 separate Deployments (backend + oauth2-proxy sidecar, dashboard, database) with 5 Routes. New TARSy consolidates to 1 Deployment (tarsy + oauth2-proxy + kube-rbac-proxy + llm-service in a single pod) with a simpler routing model. The database runs as a separate Deployment with PVC — managed database (RDS, etc.) is supported by simply changing connection parameters.

### Goals

1. **Kustomize manifests** — base + development overlay (matching old TARSy's pattern)
2. **Single-pod Deployment** — 4 containers sharing localhost network
3. **kube-rbac-proxy** — API client authentication via Kubernetes ServiceAccount tokens (replaces old TARSy's JWT)
4. **Routes** — TLS edge termination for browser access; API is internal-only (ClusterIP Service)
5. **ConfigMaps & Secrets** — application config, OAuth, LLM API keys, DB credentials
6. **ImageStreams** — OpenShift internal registry integration
7. **Health probes** — per-container liveness, readiness, startup probes
8. **Build pipeline** — GitHub Actions for quay.io, Makefile for direct OpenShift registry push
9. **Makefile targets** — `openshift-*` workflow for build, push, deploy, status, cleanup

### Non-Goals

- Managed database provisioning (out of scope — configure via connection params)
- Horizontal Pod Autoscaler (TARSy is single-replica by design — queue-based concurrency)
- Service mesh / Istio integration
- Prometheus metrics scraping (Phase 11)
- Multiple overlay environments (staging, production) — start with development

### Environment Parity with podman-compose (Phase 9)

| Aspect | podman-compose (Phase 9) | OpenShift (Phase 10) |
|--------|--------------------------|----------------------|
| oauth2-proxy | Container, :4180 mapped to host :8080 | Sidecar in pod, :4180 |
| kube-rbac-proxy | Not present | Sidecar in pod, :8443 |
| tarsy | Container, :8080 internal | Container in pod, :8080 |
| llm-service | Container, :50051 internal | Container in pod, :50051 |
| postgres | Container, :5432 | Separate Deployment with PVC |
| LLM_SERVICE_ADDR | `llm-service:50051` (compose DNS) | `localhost:50051` (pod-local) |
| TLS | HTTP (local dev) | Edge termination at Route |
| Secrets | `.env` file | K8s Secrets |
| Config files | Bind-mounted from host | ConfigMaps |
| Health probes | Docker healthcheck (TCP/HTTP) | K8s probes (HTTP, gRPC native) |
| oauth2-proxy config | Generated from template + oauth.env | ConfigMap (overlay-specific) |
| oauth2-proxy secrets | Baked into generated config | K8s Secret → env vars |

The same container images are used in both environments — only orchestration-level configuration differs.

---

## Architecture

### Pod Layout

```
┌─ tarsy Deployment (1 replica) ──────────────────────────────────────┐
│                                                                     │
│  ┌────────────────────────┐  ┌──────────────────────────────────┐   │
│  │   oauth2-proxy         │  │   kube-rbac-proxy                │   │
│  │   :4180                │  │   :8443 (TLS)                    │   │
│  │                        │  │                                  │   │
│  │   Browser auth         │  │   API client auth                │   │
│  │   GitHub OAuth         │  │   SA token → TokenReview         │   │
│  │   Cookie session       │  │   SubjectAccessReview (RBAC)     │   │
│  └──────────┬─────────────┘  └────────────────┬─────────────────┘   │
│             │                                 │                     │
│             └────────────┬────────────────────┘                     │
│                          │                                          │
│                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │   tarsy                                                       │  │
│  │   :8080                                                       │  │
│  │                                                               │  │
│  │   Go backend + pre-built dashboard static files               │  │
│  │   REST API, WebSocket, worker pool, event streaming           │  │
│  └───────────────────────┬───────────────────────────────────────┘  │
│                          │                                          │
│                          │ gRPC localhost:50051                     │
│                          ▼                                          │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │   llm-service                                                 │  │
│  │   :50051                                                      │  │
│  │                                                               │  │
│  │   Python gRPC — stateless LLM proxy                           │  │
│  │   Health: gRPC health protocol (SERVING after init)           │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘

┌─ tarsy-database Deployment (1 replica, Recreate strategy) ──────────┐
│   PostgreSQL :5432 with PVC                                         │
│   (or replace with managed DB — only connection params change)      │
└─────────────────────────────────────────────────────────────────────┘
```

### Access Paths

**Browser (user) access:**
```
User → HTTPS Route (edge TLS) → tarsy-web Service :4180
  → oauth2-proxy validates cookie
  → First visit: redirect to GitHub OAuth → callback → set _tarsy_oauth2 cookie
  → Subsequent: proxy with X-Forwarded-User/Email to localhost:8080
  → tarsy serves dashboard / API / WebSocket
```

**API client access (programmatic — replaces old TARSy's JWT):**
```
In-cluster client (with ServiceAccount token)
  → tarsy-api Service :8443
  → kube-rbac-proxy validates SA token (TokenReview API)
  → Checks RBAC authorization (SubjectAccessReview)
  → Proxies to localhost:8080 with X-Remote-User/Groups headers
  → tarsy processes API request
```

**Health check (unauthenticated):**
```
K8s kubelet → tarsy container :8080/health (httpGet probe, bypasses sidecars)
K8s kubelet → llm-service :50051 (gRPC health probe, native K8s 1.24+)
K8s kubelet → oauth2-proxy :4180/ping (httpGet probe)
K8s kubelet → kube-rbac-proxy :8443 (TCP probe)
```

**Internal (pod-local):**
```
tarsy → localhost:50051 (gRPC to llm-service, shared pod network)
tarsy → tarsy-database Service :5432 (K8s DNS)
```

### Comparison with Old TARSy

| Aspect | Old TARSy | New TARSy |
|--------|-----------|-----------|
| Deployments | 3 (backend+oauth2-proxy, dashboard, database) | 2 (tarsy pod, database) |
| Containers per pod | 2 (backend + oauth2-proxy) | 4 (tarsy + llm-service + oauth2-proxy + kube-rbac-proxy) |
| Dashboard | Separate Deployment (nginx) | Built into tarsy container |
| LLM service | Part of backend (Python monolith) | Separate container in pod (gRPC) |
| API auth | JWT (RS256 keys, JWKS endpoint) | kube-rbac-proxy (SA tokens, K8s RBAC) |
| Routes | 5 (health, dashboard, api, oauth, websocket) | 1 (browser via oauth2-proxy; API is internal ClusterIP only) |
| Services | 3 (database, backend, dashboard) | 3 (database, tarsy-web, tarsy-api) |
| ImageStreams | 2 (backend, dashboard) | 2 (tarsy, tarsy-llm) |
| Registry | quay.io (CI) + OpenShift internal (dev) | Same pattern |

---

## 10.1: Kustomize Structure

### Directory Layout

```
deploy/kustomize/
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml
│   ├── tarsy-deployment.yaml          # 4-container pod
│   ├── database-deployment.yaml       # PostgreSQL with PVC
│   ├── persistentvolumeclaims.yaml
│   ├── services.yaml                  # tarsy-web, tarsy-api, tarsy-database
│   ├── routes.yaml                    # browser Route (edge TLS)
│   ├── imagestreams.yaml              # tarsy, tarsy-llm
│   ├── rbac.yaml                      # kube-rbac-proxy RBAC resources
│   └── secrets-template.yaml          # OpenShift Template for secrets
└── overlays/
    └── development/
        ├── kustomization.yaml         # Overlay with configMapGenerator
        ├── tarsy.yaml                 # Main tarsy config (system, agents, MCP servers)
        ├── oauth2-proxy.cfg           # Environment-specific oauth2-proxy config
        ├── llm-providers.yaml         # LLM provider config
        └── templates/
            ├── sign_in.html           # Custom OAuth sign-in page
            └── tarsy-logo.png         # Sign-in logo
```

### Base `kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - namespace.yaml
  - persistentvolumeclaims.yaml
  - database-deployment.yaml
  - tarsy-deployment.yaml
  - services.yaml
  - routes.yaml
  - imagestreams.yaml
  - rbac.yaml

commonLabels:
  app: tarsy

images:
  - name: tarsy
    newTag: latest
  - name: tarsy-llm
    newTag: latest
  - name: postgres
    newName: postgres
    newTag: "17"
  - name: oauth2-proxy
    newName: quay.io/oauth2-proxy/oauth2-proxy
    newTag: latest
  - name: kube-rbac-proxy
    newName: registry.redhat.io/openshift4/ose-kube-rbac-proxy
    newTag: v4.15
```

### Development Overlay `kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: tarsy

resources:
  - ../../base

commonLabels:
  environment: development

configMapGenerator:
  - name: tarsy-app-config
    behavior: create
    files:
      - tarsy.yaml
      - llm-providers.yaml
  - name: oauth2-config
    behavior: create
    files:
      - oauth2-proxy.cfg
  - name: oauth2-templates
    behavior: create
    files:
      - sign_in.html=templates/sign_in.html
      - tarsy-logo.png=templates/tarsy-logo.png
  - name: tarsy-config
    behavior: create
    literals:
      - LOG_LEVEL=DEBUG
      - LLM_SERVICE_ADDR=localhost:50051
      - DASHBOARD_DIR=/app/dashboard
      - CONFIG_DIR=/app/config
      - DB_HOST=tarsy-database
      - DB_NAME=tarsy

images:
  - name: tarsy
    newName: image-registry.openshift-image-registry.svc:5000/tarsy/tarsy
    newTag: dev
  - name: tarsy-llm
    newName: image-registry.openshift-image-registry.svc:5000/tarsy/tarsy-llm
    newTag: dev
```

---

## 10.2: Deployment Manifest

### TARSy Pod (`tarsy-deployment.yaml`)

Single Deployment with 4 containers sharing a network namespace:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tarsy
  namespace: tarsy
spec:
  replicas: 1
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  selector:
    matchLabels:
      component: tarsy
  template:
    metadata:
      labels:
        component: tarsy
    spec:
      terminationGracePeriodSeconds: 60
      serviceAccountName: tarsy
      containers:

        # ── tarsy (Go backend + dashboard) ──────────────────
        - name: tarsy
          image: tarsy:latest
          imagePullPolicy: Always
          ports:
            - containerPort: 8080
          env:
            - name: HOME
              value: /app/data
            - name: DB_PORT
              value: "5432"
            - name: DB_SSLMODE
              value: "disable"
            - name: DB_USER
              valueFrom:
                secretKeyRef:
                  name: database-secret
                  key: username
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: database-secret
                  key: password
            - name: GOOGLE_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: google-api-key
                  optional: true
            - name: GITHUB_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: github-token
            - name: SLACK_BOT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: slack-bot-token
                  optional: true
          envFrom:
            - configMapRef:
                name: tarsy-config
          volumeMounts:
            - name: data
              mountPath: /app/data
            - name: tarsy-app-config
              mountPath: /app/config/tarsy.yaml
              subPath: tarsy.yaml
            - name: tarsy-app-config
              mountPath: /app/config/llm-providers.yaml
              subPath: llm-providers.yaml
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 10
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 2
          startupProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 12
          resources:
            requests:
              memory: "256Mi"
              cpu: "100m"
            limits:
              memory: "1Gi"
              cpu: "1000m"

        # ── llm-service (Python gRPC) ───────────────────────
        - name: llm-service
          image: tarsy-llm:latest
          imagePullPolicy: Always
          ports:
            - containerPort: 50051
          env:
            - name: HOME
              value: /app/data
            - name: GOOGLE_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: google-api-key
                  optional: true
            - name: OPENAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: openai-api-key
                  optional: true
            - name: ANTHROPIC_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: anthropic-api-key
                  optional: true
            - name: XAI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: xai-api-key
                  optional: true
            - name: VERTEX_AI_PROJECT
              valueFrom:
                secretKeyRef:
                  name: tarsy-secrets
                  key: vertex-ai-project
                  optional: true
            - name: GOOGLE_APPLICATION_CREDENTIALS
              value: /app/.gcp/service-account-key.json
          volumeMounts:
            - name: llm-data
              mountPath: /app/data
            - name: gcp-service-account
              mountPath: /app/.gcp
              readOnly: true
          readinessProbe:
            grpc:
              port: 50051
            initialDelaySeconds: 10
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3
          livenessProbe:
            grpc:
              port: 50051
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3
          startupProbe:
            grpc:
              port: 50051
            initialDelaySeconds: 5
            periodSeconds: 5
            failureThreshold: 24
          resources:
            requests:
              memory: "256Mi"
              cpu: "100m"
            limits:
              memory: "2Gi"
              cpu: "1000m"

        # ── oauth2-proxy (browser auth sidecar) ─────────────
        - name: oauth2-proxy
          image: quay.io/oauth2-proxy/oauth2-proxy:latest
          command:
            - oauth2-proxy
            - --config=/config/oauth2-proxy.cfg
            - --skip-auth-preflight=true
          ports:
            - containerPort: 4180
          env:
            - name: OAUTH2_PROXY_CLIENT_ID
              valueFrom:
                secretKeyRef:
                  name: oauth2-proxy-secret
                  key: client-id
            - name: OAUTH2_PROXY_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: oauth2-proxy-secret
                  key: client-secret
            - name: OAUTH2_PROXY_COOKIE_SECRET
              valueFrom:
                secretKeyRef:
                  name: oauth2-proxy-secret
                  key: cookie-secret
          volumeMounts:
            - name: oauth2-config
              mountPath: /config
              readOnly: true
            - name: oauth2-templates
              mountPath: /templates
              readOnly: true
          livenessProbe:
            httpGet:
              path: /ping
              port: 4180
            initialDelaySeconds: 30
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /ping
              port: 4180
            initialDelaySeconds: 10
            periodSeconds: 10
          resources:
            requests:
              memory: "64Mi"
              cpu: "50m"
            limits:
              memory: "512Mi"
              cpu: "500m"

        # ── kube-rbac-proxy (API client auth sidecar) ───────
        - name: kube-rbac-proxy
          image: registry.redhat.io/openshift4/ose-kube-rbac-proxy:v4.15
          args:
            - --secure-listen-address=0.0.0.0:8443
            - --upstream=http://127.0.0.1:8080/
            - --tls-cert-file=/var/run/secrets/serving-cert/tls.crt
            - --tls-private-key-file=/var/run/secrets/serving-cert/tls.key
            - --logtostderr=true
            - --v=2
          ports:
            - containerPort: 8443
          volumeMounts:
            - name: serving-cert
              mountPath: /var/run/secrets/serving-cert
              readOnly: true
          livenessProbe:
            tcpSocket:
              port: 8443
            initialDelaySeconds: 15
            periodSeconds: 30
          readinessProbe:
            tcpSocket:
              port: 8443
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            requests:
              memory: "32Mi"
              cpu: "25m"
            limits:
              memory: "128Mi"
              cpu: "200m"

      volumes:
        - name: data
          emptyDir: {}
        - name: llm-data
          emptyDir: {}
        - name: tarsy-app-config
          configMap:
            name: tarsy-app-config
        - name: oauth2-config
          configMap:
            name: oauth2-config
        - name: oauth2-templates
          configMap:
            name: oauth2-templates
        - name: gcp-service-account
          secret:
            secretName: gcp-service-account-secret
            optional: true
        - name: serving-cert
          secret:
            secretName: tarsy-serving-cert
```

**Design notes:**
- `terminationGracePeriodSeconds: 60` — Go backend needs time for graceful shutdown (in-flight sessions, WebSocket connections)
- `serviceAccountName: tarsy` — required for kube-rbac-proxy's TokenReview/SubjectAccessReview API calls
- `startupProbe` on tarsy and llm-service — separate from liveness to allow slow startup without premature restarts. Tarsy: 5s + 12×5s = 65s window. LLM service: 5s + 24×5s = 125s window (model loading can be slow)
- `emptyDir` for data volumes — writable scratch space for OpenShift's arbitrary UID (HOME=/app/data)
- `tarsy-app-config` — main `tarsy.yaml` config file (system settings, MCP servers). Generated by Makefile from `deploy/config/tarsy.yaml` with environment-specific transforms (dashboard URL). MCP servers use remote HTTP transport per Q6 — the config file's MCP server definitions point to HTTP endpoints, not stdio
- `serving-cert` — TLS certificate for kube-rbac-proxy, provisioned via OpenShift's service serving certificate feature (annotation on Service)
- All containers use the universal images from Phase 9 (non-root, GID 0 permissions, non-privileged ports)

### Database Deployment (`database-deployment.yaml`)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tarsy-database
  namespace: tarsy
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      component: database
  template:
    metadata:
      labels:
        component: database
    spec:
      containers:
        - name: postgres
          image: postgres:17
          env:
            - name: POSTGRES_DB
              valueFrom:
                secretKeyRef:
                  name: database-secret
                  key: database
            - name: POSTGRES_USER
              valueFrom:
                secretKeyRef:
                  name: database-secret
                  key: username
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: database-secret
                  key: password
            - name: PGDATA
              value: /var/lib/postgresql/data/pgdata
          ports:
            - containerPort: 5432
          volumeMounts:
            - name: postgres-data
              mountPath: /var/lib/postgresql/data
          livenessProbe:
            exec:
              command:
                - sh
                - -c
                - pg_isready -U ${POSTGRES_USER}
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            exec:
              command:
                - sh
                - -c
                - pg_isready -U ${POSTGRES_USER}
            initialDelaySeconds: 5
            periodSeconds: 5
      volumes:
        - name: postgres-data
          persistentVolumeClaim:
            claimName: database-data
```

**Design notes:**
- `strategy: Recreate` — PostgreSQL cannot have two instances writing to the same PVC
- Matches old TARSy's database deployment exactly
- Can be replaced entirely by a managed database (RDS, etc.) — just update DB connection env vars on the tarsy container and remove this Deployment + PVC

---

## 10.3: Services

```yaml
apiVersion: v1
kind: Service
metadata:
  name: tarsy-database
  namespace: tarsy
spec:
  selector:
    component: database
  ports:
    - port: 5432
      targetPort: 5432

---
apiVersion: v1
kind: Service
metadata:
  name: tarsy-web
  namespace: tarsy
spec:
  selector:
    component: tarsy
  ports:
    - name: oauth2-proxy
      port: 4180
      targetPort: 4180

---
apiVersion: v1
kind: Service
metadata:
  name: tarsy-api
  namespace: tarsy
  annotations:
    service.beta.openshift.io/serving-cert-secret-name: tarsy-serving-cert
spec:
  selector:
    component: tarsy
  ports:
    - name: kube-rbac-proxy
      port: 8443
      targetPort: 8443
```

**Design notes:**
- `tarsy-web` serves browser traffic through oauth2-proxy on port 4180
- `tarsy-api` serves programmatic API traffic through kube-rbac-proxy on port 8443
- The `service.beta.openshift.io/serving-cert-secret-name` annotation on `tarsy-api` tells OpenShift to automatically generate a TLS certificate and store it in the `tarsy-serving-cert` Secret — this is what kube-rbac-proxy uses for TLS on :8443
- `tarsy-database` points to the database pod (or can be replaced with an ExternalName Service for managed databases)

---

## 10.4: Routes

```yaml
# Browser access — all traffic through oauth2-proxy
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: tarsy
  namespace: tarsy
spec:
  host: {{ROUTE_HOST}}
  to:
    kind: Service
    name: tarsy-web
    weight: 100
  port:
    targetPort: oauth2-proxy
  tls:
    termination: edge
    insecureEdgeTerminationPolicy: Redirect
```

**Design notes:**
- **Single Route** for all browser traffic (dashboard, API, WebSocket, OAuth callbacks, health). Edge TLS: OpenShift terminates TLS and forwards plain HTTP to oauth2-proxy on :4180. Old TARSy had 5 separate path-based routes; new TARSy needs only one because oauth2-proxy handles all path-based routing internally (`skip_auth_routes`, `api_routes`)
- **No API Route** — kube-rbac-proxy is internal-only (ClusterIP Service). In-cluster API clients connect directly to the `tarsy-api` Service. If external API access is needed later, a passthrough TLS Route can be added
- `{{ROUTE_HOST}}` — replaced by Makefile at apply time (e.g., `tarsy.apps.cluster.example.com`)
- `insecureEdgeTerminationPolicy: Redirect` — HTTP → HTTPS redirect

### OAuth2-Proxy Config Differences for Production

The oauth2-proxy config from Phase 9 is used as the base, with production-specific values in the overlay:

```ini
# Key differences from Phase 9 compose config:
upstreams = ["http://localhost:8080/"]     # localhost, not tarsy:8080 (same pod)
redirect_url = "https://{{ROUTE_HOST}}/oauth2/callback"  # HTTPS
cookie_secure = true                        # HTTPS-only cookies
cookie_domains = ["{{ROUTE_HOST}}"]
whitelist_domains = ["{{ROUTE_HOST}}"]
```

The upstream changes from `http://tarsy:8080/` (compose DNS) to `http://localhost:8080/` (pod-local network). All other settings (GitHub org/team, session_cookie_minimal, api_routes, skip_auth_routes, custom templates) remain identical.

**Secrets handling:** In the compose environment (Phase 9), oauth2-proxy secrets (client_id, client_secret, cookie_secret) are baked into the generated config file by the Makefile. In OpenShift, these secrets are **not** in the ConfigMap. Instead, the oauth2-proxy container receives them as environment variables from the K8s Secret (`OAUTH2_PROXY_CLIENT_ID`, `OAUTH2_PROXY_CLIENT_SECRET`, `OAUTH2_PROXY_COOKIE_SECRET`), which override the config file values. The config file has placeholder values for these fields.

---

## 10.5: ConfigMaps

ConfigMaps are generated per-overlay via `configMapGenerator` in the overlay's `kustomization.yaml`:

| ConfigMap | Contents | Source |
|-----------|----------|--------|
| `tarsy-app-config` | `tarsy.yaml`, `llm-providers.yaml` | Overlay files (system/agents/MCP config, LLM providers) |
| `oauth2-config` | `oauth2-proxy.cfg` | Overlay file (env-specific) |
| `oauth2-templates` | `sign_in.html`, `tarsy-logo.png` | Overlay templates/ |
| `tarsy-config` | Env vars: LOG_LEVEL, LLM_SERVICE_ADDR, etc. | Overlay literals |

The config files (`tarsy.yaml`, `llm-providers.yaml`) are synced from `deploy/config/` to the overlay directory by the Makefile before `oc apply`. The `tarsy.yaml` for OpenShift differs from the compose version: `dashboard_url` uses the HTTPS Route host, and MCP servers point to remote HTTP endpoints per Q6.

---

## 10.6: Secrets

### Secrets Template

OpenShift Template for creating all secrets from environment variables (same pattern as old TARSy):

```yaml
apiVersion: template.openshift.io/v1
kind: Template
metadata:
  name: tarsy-secrets
  annotations:
    description: "TARSy secrets — created from environment variables"
parameters:
  - name: NAMESPACE
    value: "tarsy"
  - name: GOOGLE_API_KEY
    description: "Google API key for Gemini"
    required: true
  - name: GITHUB_TOKEN
    description: "GitHub token for runbook access"
    required: true
  - name: OPENAI_API_KEY
    value: ""
  - name: ANTHROPIC_API_KEY
    value: ""
  - name: XAI_API_KEY
    value: ""
  - name: VERTEX_AI_PROJECT
    value: ""
  - name: GOOGLE_SERVICE_ACCOUNT_KEY
    description: "GCP service account JSON (base64)"
    value: ""
  - name: SLACK_BOT_TOKEN
    description: "Slack bot token for notifications"
    value: ""
  - name: DATABASE_PASSWORD
    generate: expression
    from: "[a-zA-Z0-9]{16}"
  - name: DATABASE_USER
    value: "tarsy"
  - name: DATABASE_NAME
    value: "tarsy"
  - name: DATABASE_HOST
    value: "tarsy-database"
  - name: DATABASE_PORT
    value: "5432"
  - name: OAUTH2_CLIENT_ID
    required: true
  - name: OAUTH2_CLIENT_SECRET
    required: true
  - name: OAUTH2_COOKIE_SECRET
    generate: expression
    from: "[a-zA-Z0-9]{32}"
objects:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: tarsy-secrets
      namespace: ${NAMESPACE}
      labels:
        app: tarsy
    type: Opaque
    stringData:
      google-api-key: ${GOOGLE_API_KEY}
      github-token: ${GITHUB_TOKEN}
      openai-api-key: ${OPENAI_API_KEY}
      anthropic-api-key: ${ANTHROPIC_API_KEY}
      xai-api-key: ${XAI_API_KEY}
      vertex-ai-project: ${VERTEX_AI_PROJECT}
      slack-bot-token: ${SLACK_BOT_TOKEN}
  - apiVersion: v1
    kind: Secret
    metadata:
      name: database-secret
      namespace: ${NAMESPACE}
      labels:
        app: tarsy
    type: Opaque
    stringData:
      password: ${DATABASE_PASSWORD}
      username: ${DATABASE_USER}
      database: ${DATABASE_NAME}
      database-url: postgresql://${DATABASE_USER}:${DATABASE_PASSWORD}@${DATABASE_HOST}:${DATABASE_PORT}/${DATABASE_NAME}
  - apiVersion: v1
    kind: Secret
    metadata:
      name: oauth2-proxy-secret
      namespace: ${NAMESPACE}
      labels:
        app: tarsy
    type: Opaque
    stringData:
      client-id: ${OAUTH2_CLIENT_ID}
      client-secret: ${OAUTH2_CLIENT_SECRET}
      cookie-secret: ${OAUTH2_COOKIE_SECRET}
  - apiVersion: v1
    kind: Secret
    metadata:
      name: gcp-service-account-secret
      namespace: ${NAMESPACE}
      labels:
        app: tarsy
    type: Opaque
    data:
      service-account-key.json: ${GOOGLE_SERVICE_ACCOUNT_KEY}
```

**Changes from old TARSy secrets template:**
- **Removed**: `JWT_PUBLIC_KEY_CONTENT` (no JWT in new TARSy — kube-rbac-proxy handles API auth)
- **Removed**: `MCP_KUBECONFIG_CONTENT` (MCP servers use remote HTTP transport in containers — no kubeconfig needed)
- **Added**: `SLACK_BOT_TOKEN` (Phase 8.3 Slack notifications — matches `system.slack.token_env` default in tarsy config)
- **OAuth2 secrets**: `OAUTH2_CLIENT_ID` and `OAUTH2_CLIENT_SECRET` are now required (not optional) since auth is always on in prod
- **DATABASE_HOST**: Default is `tarsy-database` (database Service name in the `tarsy` namespace)

---

## 10.7: ImageStreams

```yaml
apiVersion: image.openshift.io/v1
kind: ImageStream
metadata:
  name: tarsy
  namespace: tarsy
spec: {}

---
apiVersion: image.openshift.io/v1
kind: ImageStream
metadata:
  name: tarsy-llm
  namespace: tarsy
spec: {}
```

Old TARSy had `tarsy-backend` and `tarsy-dashboard` ImageStreams. New TARSy replaces them with `tarsy` (Go backend + dashboard combined) and `tarsy-llm` (Python LLM service).

---

## 10.8: kube-rbac-proxy Configuration

### What It Replaces

Old TARSy used custom JWT infrastructure for programmatic API access:
- RS256 key pair generation
- JWKS endpoint (`/.well-known/jwks.json`)
- Token generation CLI tool
- oauth2-proxy JWT validation (`skip_jwt_bearer_tokens`, `oidc_jwks_url`, `extra_jwt_issuers`)

New TARSy replaces all of that with `kube-rbac-proxy` — zero custom token code.

### How It Works

1. **API client** sends request with `Authorization: Bearer <ServiceAccount-token>`
2. **kube-rbac-proxy** validates the token via Kubernetes TokenReview API
3. **kube-rbac-proxy** checks authorization via SubjectAccessReview against configured RBAC rules
4. If authorized, proxies to `http://127.0.0.1:8080/` with `X-Remote-User` and `X-Remote-Groups` headers
5. **tarsy** processes the request — `extractAuthor()` in `pkg/api/auth.go` picks up the user identity from headers

### RBAC Resources (`rbac.yaml`)

```yaml
# ServiceAccount for the TARSy pod
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tarsy
  namespace: tarsy

---
# ClusterRole granting permission to invoke TokenReview and SubjectAccessReview
# (required by kube-rbac-proxy to validate incoming tokens)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tarsy-kube-rbac-proxy
rules:
  - apiGroups: ["authentication.k8s.io"]
    resources: ["tokenreviews"]
    verbs: ["create"]
  - apiGroups: ["authorization.k8s.io"]
    resources: ["subjectaccessreviews"]
    verbs: ["create"]

---
# Bind the proxy ClusterRole to the TARSy ServiceAccount
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tarsy-kube-rbac-proxy
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tarsy-kube-rbac-proxy
subjects:
  - kind: ServiceAccount
    name: tarsy
    namespace: tarsy

---
# ClusterRole defining the "tarsy-api-access" permission
# API clients need a RoleBinding to this ClusterRole to access TARSy API
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tarsy-api-client
rules:
  - nonResourceURLs: ["/api/*", "/health"]
    verbs: ["get", "post"]
```

### Granting API Access to Clients

To grant a ServiceAccount programmatic access to TARSy's API:

```bash
# Create a ServiceAccount for the API client
oc create serviceaccount my-api-client -n my-namespace

# Bind the tarsy-api-client ClusterRole to it
oc create clusterrolebinding my-client-tarsy-access \
  --clusterrole=tarsy-api-client \
  --serviceaccount=my-namespace:my-api-client

# Get a token for the client
oc create token my-api-client -n my-namespace --duration=8760h
```

The client then uses this token in API requests:

```bash
# From within the cluster (e.g., a CronJob or another pod)
curl -k -H "Authorization: Bearer <token>" \
  https://tarsy-api.tarsy.svc:8443/api/v1/alerts \
  -d '{"alert_type": "test", ...}'
```

### Author Extraction for API Clients

The existing `extractAuthor()` in `pkg/api/auth.go` needs a minor update to also check kube-rbac-proxy headers:

```go
func extractAuthor(c *echo.Context) string {
    // oauth2-proxy headers (browser users)
    if user := c.Request().Header.Get("X-Forwarded-User"); user != "" {
        return user
    }
    if email := c.Request().Header.Get("X-Forwarded-Email"); email != "" {
        return email
    }
    // kube-rbac-proxy headers (API clients)
    if user := c.Request().Header.Get("X-Remote-User"); user != "" {
        return user
    }
    return "api-client"
}
```

This is a minimal change — `X-Remote-User` is set by kube-rbac-proxy with the ServiceAccount identity (e.g., `system:serviceaccount:my-namespace:my-api-client`).

---

## 10.9: Health Probes Summary

| Container | Liveness | Readiness | Startup |
|-----------|----------|-----------|---------|
| tarsy | `httpGet /health :8080` (30s init, 10s interval, 3 failures) | `httpGet /health :8080` (10s init, 5s interval, 2 failures) | `httpGet /health :8080` (5s init, 5s interval, 12 failures = 65s window) |
| llm-service | `grpc :50051` (30s init, 10s interval, 3 failures) | `grpc :50051` (10s init, 5s interval, 3 failures) | `grpc :50051` (5s init, 5s interval, 24 failures = 125s window) |
| oauth2-proxy | `httpGet /ping :4180` (30s init, 30s interval) | `httpGet /ping :4180` (10s init, 10s interval) | — |
| kube-rbac-proxy | `tcpSocket :8443` (15s init, 30s interval) | `tcpSocket :8443` (5s init, 10s interval) | — |

**Key design:**
- **tarsy** uses HTTP `/health` — the minimal, safe endpoint designed in Phase 9 (status + version + db/worker_pool checks only)
- **llm-service** uses native gRPC probes (K8s 1.24+) — the gRPC health service added in Phase 9 reports `SERVING` only after initialization
- **Startup probes** for tarsy and llm-service prevent premature liveness failures during slow initialization (MCP server validation, model loading)
- **oauth2-proxy and kube-rbac-proxy** start fast and don't need startup probes
- Health probes bypass sidecars entirely — kubelet probes directly into each container's port

---

## 10.10: Build Pipeline

### GitHub Actions (CI → quay.io)

Two workflows for automated builds on push to main:

**`.github/workflows/build-and-push-tarsy.yml`:**
```yaml
name: Build & Push TARSy Image

on:
  push:
    branches: [main]
    paths:
      - 'cmd/**'
      - 'pkg/**'
      - 'ent/**'
      - 'proto/**'
      - 'web/dashboard/**'
      - 'go.mod'
      - 'go.sum'
      - 'Dockerfile'
      - '.github/workflows/build-and-push-tarsy.yml'

env:
  IMAGE_REGISTRY: quay.io
  IMAGE_NAME: codeready-toolchain/tarsy
  REGISTRY_USER: "codeready-toolchain+tarsy_push"
  REGISTRY_PASSWORD: ${{ secrets.QUAY_PASSWORD }}

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0

      - name: Set short SHA
        run: echo "COMMIT_SHORT_SHA=$(git rev-parse --short ${GITHUB_SHA})" >> ${GITHUB_ENV}

      - name: Build image
        id: build-image
        uses: redhat-actions/buildah-build@v2
        with:
          image: ${{ env.IMAGE_NAME }}
          tags: latest ${{ env.COMMIT_SHORT_SHA }}
          context: .
          containerfiles: ./Dockerfile
          build-args: VERSION=${{ env.COMMIT_SHORT_SHA }}

      - name: Log into quay.io
        uses: redhat-actions/podman-login@v1
        with:
          registry: ${{ env.IMAGE_REGISTRY }}
          username: ${{ env.REGISTRY_USER }}
          password: ${{ env.REGISTRY_PASSWORD }}

      - name: Push to quay.io
        uses: redhat-actions/push-to-registry@v2
        with:
          image: ${{ steps.build-image.outputs.image }}
          tags: ${{ steps.build-image.outputs.tags }}
          registry: ${{ env.IMAGE_REGISTRY }}
```

**`.github/workflows/build-and-push-llm-service.yml`:**
```yaml
name: Build & Push LLM Service Image

on:
  push:
    branches: [main]
    paths:
      - 'llm-service/**'
      - '.github/workflows/build-and-push-llm-service.yml'

env:
  IMAGE_REGISTRY: quay.io
  IMAGE_NAME: codeready-toolchain/tarsy-llm
  REGISTRY_USER: "codeready-toolchain+tarsy_push"
  REGISTRY_PASSWORD: ${{ secrets.QUAY_PASSWORD }}

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0

      - name: Set short SHA
        run: echo "COMMIT_SHORT_SHA=$(git rev-parse --short ${GITHUB_SHA})" >> ${GITHUB_ENV}

      - name: Build image
        id: build-image
        uses: redhat-actions/buildah-build@v2
        with:
          image: ${{ env.IMAGE_NAME }}
          tags: latest ${{ env.COMMIT_SHORT_SHA }}
          context: ./llm-service
          containerfiles: ./llm-service/Dockerfile

      - name: Log into quay.io
        uses: redhat-actions/podman-login@v1
        with:
          registry: ${{ env.IMAGE_REGISTRY }}
          username: ${{ env.REGISTRY_USER }}
          password: ${{ env.REGISTRY_PASSWORD }}

      - name: Push to quay.io
        uses: redhat-actions/push-to-registry@v2
        with:
          image: ${{ steps.build-image.outputs.image }}
          tags: ${{ steps.build-image.outputs.tags }}
          registry: ${{ env.IMAGE_REGISTRY }}
```

**Key differences from old TARSy CI:**
- **2 workflows** (tarsy + llm-service) instead of 2 (backend + dashboard) — dashboard is baked into tarsy image
- **Path triggers** for tarsy include all Go, dashboard, and proto changes
- Same `redhat-actions/buildah-build` + `push-to-registry` pattern
- Same tagging strategy: `latest` + commit short SHA

### Direct Push to OpenShift Registry (Dev Workflow)

For rapid development iteration, images are pushed directly to the OpenShift internal registry via Makefile targets (see 10.11). This bypasses CI and is equivalent to old TARSy's `make openshift-push-*` workflow.

---

## 10.11: Makefile Targets

Add `make/openshift.mk`:

```makefile
# =============================================================================
# OpenShift Deployment
# =============================================================================

# Colors
GREEN := \033[0;32m
YELLOW := \033[0;33m
RED := \033[0;31m
BLUE := \033[0;34m
NC := \033[0m

# OpenShift variables
OPENSHIFT_NAMESPACE := tarsy
OPENSHIFT_REGISTRY = $(shell oc get route default-route -n openshift-image-registry --template='{{ .spec.host }}' 2>/dev/null || echo "registry.not.found")
TARSY_IMAGE = $(OPENSHIFT_REGISTRY)/$(OPENSHIFT_NAMESPACE)/tarsy
LLM_IMAGE = $(OPENSHIFT_REGISTRY)/$(OPENSHIFT_NAMESPACE)/tarsy-llm
IMAGE_TAG := dev
USE_SKOPEO ?=

# Auto-detect cluster domain
CLUSTER_DOMAIN = $(shell oc get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}' 2>/dev/null)

# Auto-load openshift.env for OpenShift targets
OPENSHIFT_TARGETS := openshift-check openshift-login-registry openshift-create-namespace \
                     openshift-build-tarsy openshift-build-llm openshift-build-all \
                     openshift-push-tarsy openshift-push-llm openshift-push-all \
                     openshift-create-secrets openshift-check-config-files \
                     openshift-apply openshift-deploy openshift-redeploy \
                     openshift-status openshift-urls openshift-logs \
                     openshift-db-reset openshift-clean openshift-clean-images

ifneq ($(filter $(OPENSHIFT_TARGETS),$(MAKECMDGOALS)),)
	ifneq ($(CLUSTER_DOMAIN),)
		ROUTE_HOST := $(OPENSHIFT_NAMESPACE).$(CLUSTER_DOMAIN)
	endif
	-include deploy/openshift.env
	ifndef ROUTE_HOST
		$(error ROUTE_HOST not defined. Set in deploy/openshift.env or ensure oc context is correct)
	endif
endif

# ── Prerequisites ────────────────────────────────────────

.PHONY: openshift-check
openshift-check: ## Check OpenShift login and registry access
	@echo -e "$(BLUE)Checking OpenShift prerequisites...$(NC)"
	@command -v oc >/dev/null 2>&1 || { echo -e "$(RED)oc CLI not found$(NC)"; exit 1; }
	@oc whoami >/dev/null 2>&1 || { echo -e "$(RED)Not logged into OpenShift$(NC)"; exit 1; }
	@[ "$(OPENSHIFT_REGISTRY)" != "registry.not.found" ] || { echo -e "$(RED)Registry not exposed$(NC)"; exit 1; }
	@echo -e "$(GREEN)✓ Logged in as: $$(oc whoami) | Registry: $(OPENSHIFT_REGISTRY)$(NC)"

.PHONY: openshift-login-registry
openshift-login-registry: openshift-check ## Login podman to OpenShift registry
	@podman login --tls-verify=false -u $$(oc whoami) -p $$(oc whoami -t) $(OPENSHIFT_REGISTRY)

.PHONY: openshift-create-namespace
openshift-create-namespace: openshift-check ## Create namespace if needed
	@oc get namespace $(OPENSHIFT_NAMESPACE) >/dev/null 2>&1 || oc create namespace $(OPENSHIFT_NAMESPACE)

# ── Build ────────────────────────────────────────────────

.PHONY: openshift-build-tarsy
openshift-build-tarsy: openshift-login-registry ## Build tarsy image for OpenShift
	@echo -e "$(BLUE)Building tarsy image...$(NC)"
	@podman build -t localhost/tarsy:latest -f Dockerfile .
	@echo -e "$(GREEN)✅ tarsy image built$(NC)"

.PHONY: openshift-build-llm
openshift-build-llm: openshift-login-registry ## Build llm-service image for OpenShift
	@echo -e "$(BLUE)Building llm-service image...$(NC)"
	@podman build -t localhost/tarsy-llm:latest -f llm-service/Dockerfile llm-service/
	@echo -e "$(GREEN)✅ tarsy-llm image built$(NC)"

.PHONY: openshift-build-all
openshift-build-all: openshift-build-tarsy openshift-build-llm ## Build all images

# ── Push ─────────────────────────────────────────────────

.PHONY: openshift-push-tarsy
openshift-push-tarsy: openshift-build-tarsy openshift-create-namespace ## Push tarsy to OpenShift registry
	@podman tag localhost/tarsy:latest $(TARSY_IMAGE):$(IMAGE_TAG)
	@if [ -n "$(USE_SKOPEO)" ]; then \
		podman save localhost/tarsy:latest -o /tmp/tarsy.tar; \
		skopeo copy --dest-tls-verify=false docker-archive:/tmp/tarsy.tar docker://$(TARSY_IMAGE):$(IMAGE_TAG); \
		rm -f /tmp/tarsy.tar; \
	else \
		podman push --tls-verify=false $(TARSY_IMAGE):$(IMAGE_TAG); \
	fi
	@echo -e "$(GREEN)✅ tarsy pushed: $(TARSY_IMAGE):$(IMAGE_TAG)$(NC)"

.PHONY: openshift-push-llm
openshift-push-llm: openshift-build-llm openshift-create-namespace ## Push llm-service to OpenShift registry
	@podman tag localhost/tarsy-llm:latest $(LLM_IMAGE):$(IMAGE_TAG)
	@if [ -n "$(USE_SKOPEO)" ]; then \
		podman save localhost/tarsy-llm:latest -o /tmp/tarsy-llm.tar; \
		skopeo copy --dest-tls-verify=false docker-archive:/tmp/tarsy-llm.tar docker://$(LLM_IMAGE):$(IMAGE_TAG); \
		rm -f /tmp/tarsy-llm.tar; \
	else \
		podman push --tls-verify=false $(LLM_IMAGE):$(IMAGE_TAG); \
	fi
	@echo -e "$(GREEN)✅ tarsy-llm pushed: $(LLM_IMAGE):$(IMAGE_TAG)$(NC)"

.PHONY: openshift-push-all
openshift-push-all: openshift-push-tarsy openshift-push-llm ## Build and push all images

# ── Secrets ──────────────────────────────────────────────

.PHONY: openshift-create-secrets
openshift-create-secrets: openshift-check openshift-create-namespace ## Create secrets from env vars
	@[ -n "$$GOOGLE_API_KEY" ] || { echo -e "$(RED)GOOGLE_API_KEY not set$(NC)"; exit 1; }
	@[ -n "$$GITHUB_TOKEN" ] || { echo -e "$(RED)GITHUB_TOKEN not set$(NC)"; exit 1; }
	@[ -n "$$OAUTH2_CLIENT_ID" ] || { echo -e "$(RED)OAUTH2_CLIENT_ID not set$(NC)"; exit 1; }
	@[ -n "$$OAUTH2_CLIENT_SECRET" ] || { echo -e "$(RED)OAUTH2_CLIENT_SECRET not set$(NC)"; exit 1; }
	@export DATABASE_USER=$${DATABASE_USER:-tarsy}; \
	export DATABASE_NAME=$${DATABASE_NAME:-tarsy}; \
	export DATABASE_HOST=$${DATABASE_HOST:-tarsy-database}; \
	export DATABASE_PORT=$${DATABASE_PORT:-5432}; \
	oc process -f deploy/kustomize/base/secrets-template.yaml \
		-p NAMESPACE=$(OPENSHIFT_NAMESPACE) \
		-p GOOGLE_API_KEY="$$GOOGLE_API_KEY" \
		-p GITHUB_TOKEN="$$GITHUB_TOKEN" \
		-p OPENAI_API_KEY="$$OPENAI_API_KEY" \
		-p ANTHROPIC_API_KEY="$$ANTHROPIC_API_KEY" \
		-p XAI_API_KEY="$$XAI_API_KEY" \
		-p VERTEX_AI_PROJECT="$$VERTEX_AI_PROJECT" \
		-p GOOGLE_SERVICE_ACCOUNT_KEY="$$GOOGLE_SERVICE_ACCOUNT_KEY" \
		-p SLACK_BOT_TOKEN="$$SLACK_BOT_TOKEN" \
		-p DATABASE_USER="$$DATABASE_USER" \
		-p DATABASE_NAME="$$DATABASE_NAME" \
		-p DATABASE_HOST="$$DATABASE_HOST" \
		-p DATABASE_PORT="$$DATABASE_PORT" \
		-p OAUTH2_CLIENT_ID="$$OAUTH2_CLIENT_ID" \
		-p OAUTH2_CLIENT_SECRET="$$OAUTH2_CLIENT_SECRET" \
		$${DATABASE_PASSWORD:+-p DATABASE_PASSWORD="$$DATABASE_PASSWORD"} \
		$${OAUTH2_COOKIE_SECRET:+-p OAUTH2_COOKIE_SECRET="$$OAUTH2_COOKIE_SECRET"} \
		| oc apply -f -
	@echo -e "$(GREEN)✅ Secrets created in $(OPENSHIFT_NAMESPACE)$(NC)"

# ── Config Sync ──────────────────────────────────────────

.PHONY: openshift-check-config-files
openshift-check-config-files: ## Sync config files to overlay directory
	@echo -e "$(BLUE)Syncing config files...$(NC)"
	@mkdir -p deploy/kustomize/overlays/development/templates
	@[ -f deploy/config/tarsy.yaml ] && \
		sed -e 's|http://localhost:5173|https://$(ROUTE_HOST)|g' \
		deploy/config/tarsy.yaml > deploy/kustomize/overlays/development/tarsy.yaml || \
		{ echo -e "$(RED)deploy/config/tarsy.yaml not found$(NC)"; exit 1; }
	@[ -f deploy/config/llm-providers.yaml ] && cp deploy/config/llm-providers.yaml deploy/kustomize/overlays/development/ || \
		{ echo -e "$(RED)deploy/config/llm-providers.yaml not found$(NC)"; exit 1; }
	@[ -d deploy/config/templates ] && cp -r deploy/config/templates/* deploy/kustomize/overlays/development/templates/ || \
		{ echo -e "$(RED)deploy/config/templates/ not found$(NC)"; exit 1; }
	@echo -e "$(BLUE)Generating overlay oauth2-proxy.cfg from template...$(NC)"
	@sed -e 's|http://tarsy:8080/|http://localhost:8080/|g' \
		-e 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' \
		-e 's|{{COOKIE_SECURE}}|true|g' \
		-e 's|{{OAUTH2_PROXY_REDIRECT_URL}}|https://$(ROUTE_HOST)/oauth2/callback|g' \
		-e 's|{{GITHUB_ORG}}|$(GITHUB_ORG)|g' \
		-e 's|{{GITHUB_TEAM}}|$(GITHUB_TEAM)|g' \
		-e 's|{{OAUTH2_CLIENT_ID}}|OVERRIDDEN_BY_ENV|g' \
		-e 's|{{OAUTH2_CLIENT_SECRET}}|OVERRIDDEN_BY_ENV|g' \
		-e 's|{{OAUTH2_COOKIE_SECRET}}|OVERRIDDEN_BY_ENV|g' \
		deploy/config/oauth2-proxy.cfg.template \
		> deploy/kustomize/overlays/development/oauth2-proxy.cfg
	@echo -e "$(GREEN)✅ Config files synced$(NC)"

# ── Apply ────────────────────────────────────────────────

.PHONY: openshift-apply
openshift-apply: openshift-check openshift-check-config-files ## Apply Kustomize manifests
	@echo -e "$(BLUE)Applying manifests (Route Host: $(ROUTE_HOST))...$(NC)"
	@sed -i.bak 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' deploy/kustomize/base/routes.yaml
	@oc apply -k deploy/kustomize/overlays/development/
	@mv deploy/kustomize/base/routes.yaml.bak deploy/kustomize/base/routes.yaml
	@echo -e "$(GREEN)✅ Manifests applied to $(OPENSHIFT_NAMESPACE)$(NC)"

# ── Deploy (full) ────────────────────────────────────────

.PHONY: openshift-deploy
openshift-deploy: openshift-create-secrets openshift-push-all openshift-apply ## Full deployment
	@for d in $$(oc get deployments -n $(OPENSHIFT_NAMESPACE) -o name 2>/dev/null | sed 's|deployment.apps/||'); do \
		oc rollout restart deployment/$$d -n $(OPENSHIFT_NAMESPACE); \
	done
	@echo -e "$(GREEN)✅ Deployed to $(OPENSHIFT_NAMESPACE)$(NC)"

.PHONY: openshift-redeploy
openshift-redeploy: openshift-push-all openshift-apply ## Rebuild and update (no secrets)

# ── Status ───────────────────────────────────────────────

.PHONY: openshift-status
openshift-status: openshift-check ## Show deployment status
	@echo -e "$(BLUE)Namespace: $(OPENSHIFT_NAMESPACE)$(NC)"
	@echo -e "\n$(YELLOW)Pods:$(NC)" && oc get pods -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true
	@echo -e "\n$(YELLOW)Services:$(NC)" && oc get services -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true
	@echo -e "\n$(YELLOW)Routes:$(NC)" && oc get routes -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true

.PHONY: openshift-urls
openshift-urls: openshift-check ## Show application URLs
	@WEB=$$(oc get route tarsy -n $(OPENSHIFT_NAMESPACE) -o jsonpath='{.spec.host}' 2>/dev/null); \
	echo -e "$(BLUE)Dashboard: https://$$WEB$(NC)"; \
	echo -e "$(BLUE)API (browser): https://$$WEB/api/v1/$(NC)"; \
	echo -e "$(BLUE)API (in-cluster): https://tarsy-api.$(OPENSHIFT_NAMESPACE).svc:8443/api/v1/$(NC)"; \
	echo -e "$(BLUE)Health: https://$$WEB/health$(NC)"

.PHONY: openshift-logs
openshift-logs: openshift-check ## Show tarsy pod logs (all containers)
	@oc logs -l component=tarsy -n $(OPENSHIFT_NAMESPACE) --all-containers --tail=50 2>/dev/null || echo "No pods found"

# ── Cleanup ──────────────────────────────────────────────

.PHONY: openshift-clean
openshift-clean: openshift-check ## Delete all TARSy resources
	@printf "Delete all TARSy resources from $(OPENSHIFT_NAMESPACE)? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		sed -i.bak 's|{{ROUTE_HOST}}|$(ROUTE_HOST)|g' deploy/kustomize/base/routes.yaml; \
		oc delete -k deploy/kustomize/overlays/development/ 2>/dev/null || true; \
		mv deploy/kustomize/base/routes.yaml.bak deploy/kustomize/base/routes.yaml; \
		echo -e "$(GREEN)✅ Resources deleted$(NC)";; \
	*) echo "Cancelled";; esac

.PHONY: openshift-clean-images
openshift-clean-images: openshift-check ## Delete images from registry
	@printf "Delete TARSy images from registry? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		oc delete imagestream tarsy -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		oc delete imagestream tarsy-llm -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		echo -e "$(GREEN)✅ Images deleted$(NC)";; \
	*) echo "Cancelled";; esac

# ── Database ─────────────────────────────────────────────

.PHONY: openshift-db-reset
openshift-db-reset: openshift-check ## Reset PostgreSQL (DESTRUCTIVE)
	@printf "DELETE ALL DATABASE DATA? [y/N] "; \
	read REPLY; case "$$REPLY" in [Yy]*) \
		oc scale deployment tarsy-database --replicas=0 -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		oc wait --for=delete pod -l component=database -n $(OPENSHIFT_NAMESPACE) --timeout=60s 2>/dev/null || true; \
		oc delete pvc database-data -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		$(MAKE) openshift-apply 2>/dev/null; \
		oc scale deployment tarsy-database --replicas=1 -n $(OPENSHIFT_NAMESPACE); \
		oc wait --for=condition=available deployment/tarsy-database -n $(OPENSHIFT_NAMESPACE) --timeout=120s; \
		oc rollout restart deployment/tarsy -n $(OPENSHIFT_NAMESPACE) 2>/dev/null || true; \
		echo -e "$(GREEN)✅ Database reset$(NC)";; \
	*) echo "Cancelled";; esac
```

---

## 10.12: Rollout Strategy

### Rolling Update

The tarsy Deployment uses `RollingUpdate` strategy (explicit in the deployment YAML):

```yaml
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
```

`maxUnavailable: 0` ensures the old pod stays running until the new pod is healthy (zero downtime). `maxSurge: 1` creates at most 1 extra pod during rollout.

### Database Migration

TARSy uses **golang-migrate** with versioned `.up.sql` files (generated by Atlas CLI from Ent schema diffs). `runMigrations()` applies all pending migrations automatically on startup before the HTTP server starts:

1. New pod starts → `m.Up()` applies pending `.up.sql` files → schema updated → starts serving
2. Old pod continues serving on the updated schema during rollout
3. Once new pod passes readiness probe, traffic shifts

Migrations can contain any SQL (CREATE, ALTER, DROP) — they are not limited to additive changes. Migration authors must ensure backward compatibility during rolling updates: the old pod's code must tolerate the new schema. Standard pattern: add first, deploy code, remove later.

### Rollback

```bash
# Rollback to previous revision
oc rollout undo deployment/tarsy -n tarsy

# Check rollout history
oc rollout history deployment/tarsy -n tarsy
```

---

## Implementation Plan

### Phase 10.1: Kustomize Base Manifests
1. Create `deploy/kustomize/base/` directory structure
2. Write `namespace.yaml`
3. Write `persistentvolumeclaims.yaml`
4. Write `database-deployment.yaml` (PostgreSQL with PVC)
5. Write `services.yaml` (tarsy-web, tarsy-api, tarsy-database)
6. Write `routes.yaml` (browser Route with ROUTE_HOST placeholder)
7. Write `imagestreams.yaml` (tarsy, tarsy-llm)
8. Write base `kustomization.yaml`

### Phase 10.2: TARSy Deployment Manifest
1. Write `tarsy-deployment.yaml` with 4 containers
2. Configure all env vars, volume mounts, health probes
3. Set resource requests/limits per container
4. Verify manifest syntax with `kubectl apply --dry-run=client`

### Phase 10.3: kube-rbac-proxy RBAC
1. Write `rbac.yaml` (ServiceAccount, ClusterRoles, ClusterRoleBindings)
2. Configure kube-rbac-proxy container args (serving cert, upstream, TLS)
3. Document API client onboarding process

### Phase 10.4: Secrets and ConfigMaps
1. Write `secrets-template.yaml` (OpenShift Template)
2. Create development overlay with `configMapGenerator`
3. Add `openshift.env.example` for environment-specific variables

### Phase 10.5: Author Extraction Update
1. Update `extractAuthor()` in `pkg/api/auth.go` to check `X-Remote-User` header
2. Add unit test for kube-rbac-proxy header extraction

### Phase 10.6: Build Pipeline
1. Create `.github/workflows/build-and-push-tarsy.yml`
2. Create `.github/workflows/build-and-push-llm-service.yml`
3. Configure quay.io repository and robot account

### Phase 10.7: Makefile Targets
1. Create `make/openshift.mk` with all OpenShift workflow targets
2. Create `deploy/openshift.env.example`
3. Update root Makefile include
4. Test full workflow: `make openshift-deploy`

### Phase 10.8: Testing and Validation
1. Deploy to development namespace
2. Verify browser access through oauth2-proxy Route
3. Verify API access through kube-rbac-proxy with SA token
4. Verify health probes (all containers healthy)
5. Verify WebSocket connectivity
6. Verify llm-service gRPC communication (localhost:50051)
7. Test rollout: `make openshift-redeploy`
8. Test rollback: `oc rollout undo`

---

## File Changes Summary

### New Files

| File | Purpose |
|------|---------|
| `deploy/kustomize/base/kustomization.yaml` | Base Kustomize config |
| `deploy/kustomize/base/namespace.yaml` | Namespace definition |
| `deploy/kustomize/base/tarsy-deployment.yaml` | 4-container pod Deployment |
| `deploy/kustomize/base/database-deployment.yaml` | PostgreSQL Deployment |
| `deploy/kustomize/base/persistentvolumeclaims.yaml` | Database PVC |
| `deploy/kustomize/base/services.yaml` | tarsy-web, tarsy-api, tarsy-database |
| `deploy/kustomize/base/routes.yaml` | Browser Route (edge TLS) |
| `deploy/kustomize/base/imagestreams.yaml` | tarsy, tarsy-llm ImageStreams |
| `deploy/kustomize/base/rbac.yaml` | kube-rbac-proxy RBAC resources |
| `deploy/kustomize/base/secrets-template.yaml` | OpenShift Template for secrets |
| `deploy/kustomize/overlays/development/kustomization.yaml` | Dev overlay |
| `deploy/kustomize/overlays/development/templates/` | OAuth sign-in template + logo |
| `deploy/openshift.env.example` | OpenShift env vars template |
| `make/openshift.mk` | OpenShift Makefile targets |
| `.github/workflows/build-and-push-tarsy.yml` | CI: tarsy image |
| `.github/workflows/build-and-push-llm-service.yml` | CI: llm-service image |

### Modified Files

| File | Changes |
|------|---------|
| `pkg/api/auth.go` | Add `X-Remote-User` header check for kube-rbac-proxy |
| `Makefile` | Include `make/openshift.mk` (already includes `make/*.mk`) |
| `.gitignore` | Add `deploy/kustomize/overlays/development/oauth2-proxy.cfg`, `tarsy.yaml`, and overlay config files |

### Unchanged from Phase 9

| File | Why No Changes |
|------|----------------|
| `Dockerfile` | Universal image — same for compose and OpenShift |
| `llm-service/Dockerfile` | Universal image — same for compose and OpenShift |
| `deploy/config/oauth2-proxy.cfg.template` | Template shared by compose and OpenShift (overlay transforms) |
| `llm-service/llm/server.py` | gRPC health service already added in Phase 9 |

---

## Testing Strategy

### Automated (Unit)

- `pkg/api/auth_test.go` — verify `extractAuthor()` returns `X-Remote-User` for API clients, `X-Forwarded-User` for browser users, correct priority order

### Manual Validation

1. **Kustomize syntax**: `kubectl kustomize deploy/kustomize/overlays/development/` renders valid YAML
2. **Full deployment**: `make openshift-deploy` succeeds end-to-end
3. **Browser flow**: HTTPS Route → OAuth login → dashboard loads → API calls work → WebSocket streams events
4. **API client flow**: Create SA → bind ClusterRole → get token → `curl` from within cluster to `tarsy-api` Service with bearer token succeeds
5. **Health probes**: `oc describe pod` shows all containers passing probes
6. **Rollout**: `make openshift-redeploy` → zero-downtime update verified
7. **Rollback**: `oc rollout undo` → previous version restored
8. **Database reset**: `make openshift-db-reset` → clean state, migrations run on restart
9. **Logs**: `make openshift-logs` shows all container output
