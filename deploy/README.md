# TARSy Deployment Guide

TARSy supports three deployment modes, from lightweight local development to production OpenShift clusters.

| Mode | Auth | Best for |
|------|------|----------|
| `make dev` | None | Day-to-day development, debugging |
| `make containers-deploy` | GitHub OAuth | Integration testing, team demos |
| OpenShift | GitHub OAuth + kube-rbac-proxy | Production |

## Prerequisites

All modes require configuration files in `deploy/config/`. Copy the examples first:

```bash
cp deploy/config/.env.example        deploy/config/.env
cp deploy/config/tarsy.yaml.example  deploy/config/tarsy.yaml
cp deploy/config/llm-providers.yaml.example deploy/config/llm-providers.yaml
```

Edit `deploy/config/.env` with your API keys and database credentials. See [deploy/config/README.md](config/README.md) for a full reference of every configuration file and option.

### Toolchain

| Tool | Version | Required by |
|------|---------|-------------|
| Go | 1.25+ | Host dev, container build |
| Python + uv | 3.13+ | Host dev, container build |
| Node.js | 24+ | Host dev (dashboard), container build |
| Podman (or Docker) | -- | All modes (DB in host dev, full stack in container/OpenShift) |

---

## Option 1: Host Development (`make dev`)

Runs each service as a host process. PostgreSQL runs in a container via podman-compose; everything else runs natively.

### 1. Install dependencies

```bash
make setup
```

This installs Go modules, Python (LLM service) dependencies via uv, and dashboard npm packages.

### 2. Start the environment

```bash
make dev
```

This single command:
- Starts PostgreSQL via podman-compose (port 5432)
- Starts the LLM gRPC service (port 50051)
- Builds and starts the Go backend (port 8080)
- Starts the Vite dev server for the dashboard (port 5173)

### 3. Access

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:5173 |
| API | http://localhost:8080 |
| Health | http://localhost:8080/health |

### 4. Stop

Press `Ctrl+C` in the terminal running `make dev`, or from another terminal:

```bash
make dev-stop
```

---

## Option 2: Container Development (`make containers-deploy`)

Runs all four services (PostgreSQL, LLM service, TARSy backend + embedded dashboard, OAuth2 proxy) as containers via podman-compose. The dashboard is served by the Go backend through OAuth2 proxy -- no separate Vite server.

### 1. Create OAuth2 credentials

Register a GitHub OAuth App:
- **Homepage URL**: `http://localhost:8080`
- **Authorization callback URL**: `http://localhost:8080/oauth2/callback`

Then create the env file:

```bash
cp deploy/config/oauth.env.example deploy/config/oauth.env
```

Edit `deploy/config/oauth.env` with your GitHub OAuth App client ID, client secret, and a random cookie secret. Set `GITHUB_ORG` and `GITHUB_TEAM` to restrict access.

### 2. Deploy

```bash
make containers-deploy
```

This builds both container images (`tarsy:dev`, `tarsy-llm:dev`), generates `oauth2-proxy.cfg` from the template, and starts all four containers.

### 3. Access

| Service | URL |
|---------|-----|
| Dashboard | http://localhost:8080 (GitHub login required) |
| Health | http://localhost:8080/health (unauthenticated) |

### 4. Useful commands

| Command | Description |
|---------|-------------|
| `make containers-status` | Show running containers |
| `make containers-logs` | Follow all container logs |
| `make containers-logs-tarsy` | Follow TARSy backend logs only |
| `make containers-redeploy` | Rebuild and restart the TARSy container only |
| `make containers-stop` | Stop all containers |
| `make containers-clean` | Stop containers and remove volumes |
| `make containers-deploy-fresh` | Clean rebuild from scratch |
| `make containers-db-reset` | Reset the database only |

---

## Option 3: OpenShift (`make openshift-deploy`)

Production-like deployment to OpenShift using Kustomize manifests. Uses the same universal container images as Option 2, deployed as a single pod with 4 containers (tarsy, llm-service, oauth2-proxy, kube-rbac-proxy) plus a separate PostgreSQL deployment.

### 1. Prerequisites

- `oc` CLI logged into your OpenShift cluster
- Podman (for building and pushing images to the internal registry)
- Configuration files from the [Prerequisites](#prerequisites) section above

**⚠️ MCP servers**: MCP servers configured for local development won't work on OpenShift. Stdio-based servers (including the built-in `kubernetes-server`) require binaries that are not present in the tarsy container image, and HTTP URLs pointing to `localhost` or `host.containers.internal` are not reachable from the cluster. TARSy will start in a degraded state with warnings visible on the dashboard if configured MCP servers are unreachable. Before deploying, update the MCP server entries in `deploy/config/tarsy.yaml` to use cluster-reachable HTTP endpoints (e.g., `http://mcp-server.tarsy.svc:8888/mcp` for an in-cluster MCP server, or a publicly accessible URL).

### 2. Create cluster config

```bash
cp deploy/openshift.env.example deploy/openshift.env
```

Edit `deploy/openshift.env` with your `GITHUB_ORG` and `GITHUB_TEAM`. `ROUTE_HOST` is auto-detected from the cluster domain if not set.

### 3. Deploy

```bash
# Set required env vars (or source from a file)
export GOOGLE_API_KEY=...
export GITHUB_TOKEN=...
export OAUTH2_CLIENT_ID=...
export OAUTH2_CLIENT_SECRET=...

make openshift-deploy
```

This creates secrets, builds and pushes images to the OpenShift internal registry, applies Kustomize manifests, and restarts deployments.

### 4. Access

```bash
make openshift-urls
```

| Path | Description |
|------|-------------|
| Dashboard | `https://<route-host>/` (GitHub login via oauth2-proxy) |
| API (browser) | `https://<route-host>/api/v1/` |
| API (in-cluster) | `https://tarsy-api.tarsy.svc:8443/api/v1/` (ServiceAccount token via kube-rbac-proxy) |
| Health | `https://<route-host>/health` (unauthenticated) |

### 5. Useful commands

| Command | Description |
|---------|-------------|
| `make openshift-status` | Show pods, services, routes |
| `make openshift-logs` | Show logs from all containers in the tarsy pod |
| `make openshift-redeploy` | Rebuild images and reapply manifests (no secret changes) |
| `make openshift-urls` | Show application URLs |
| `make openshift-db-reset` | Reset PostgreSQL (destructive) |
| `make openshift-clean` | Delete all TARSy resources |
| `make openshift-clean-images` | Delete images from the internal registry |

### 6. Granting programmatic API access

```bash
oc create serviceaccount my-api-client -n my-namespace
oc create clusterrolebinding my-client-tarsy-access \
  --clusterrole=tarsy-api-client \
  --serviceaccount=my-namespace:my-api-client
TOKEN=$(oc create token my-api-client -n my-namespace --duration=8760h)
curl -k -H "Authorization: Bearer $TOKEN" \
  https://tarsy-api.tarsy.svc:8443/api/v1/alerts -d '...'
```

---

## See Also

- [deploy/config/README.md](config/README.md) -- configuration file formats, override priority, and troubleshooting
