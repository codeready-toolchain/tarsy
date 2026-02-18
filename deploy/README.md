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

## Option 3: OpenShift (Phase 10 -- coming soon)

Production deployment to OpenShift/Kubernetes using the same container images from Option 2. This mode adds:

- Kustomize overlays for environment-specific configuration
- `kube-rbac-proxy` sidecar for API client authentication
- OpenShift Routes for ingress

See [docs/phase10-kubernetes-deployment-design.md](../docs/phase10-kubernetes-deployment-design.md) for the full design.

---

## See Also

- [deploy/config/README.md](config/README.md) -- configuration file formats, override priority, and troubleshooting
