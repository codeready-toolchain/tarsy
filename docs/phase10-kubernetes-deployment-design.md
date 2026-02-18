# Phase 10: Kubernetes/OpenShift Deployment — Design (Draft)

> **Status:** Early draft. This document captures the production topology decided during Phase 9 design. Detailed design will be completed after Phase 9 implementation.

## Production Topology (OpenShift)

### Pod Layout

Single pod with 4 containers (+ external/managed database):

```
┌─ TARSy Pod ────────────────────────────────────────────────────┐
│                                                                │
│  ┌──────────────────────┐  ┌────────────────────────────────┐  │
│  │   oauth2-proxy       │  │   kube-rbac-proxy              │  │
│  │   :4180              │  │   :8443                        │  │
│  │                      │  │                                │  │
│  │   Browser auth       │  │   API client auth              │  │
│  │   GitHub OAuth       │  │   SA token validation          │  │
│  │   Cookie-based       │  │   RBAC enforcement             │  │
│  └──────────┬───────────┘  └───────────────┬────────────────┘  │
│             │                              │                   │
│             └──────────┬───────────────────┘                   │
│                        │                                       │
│                        ▼                                       │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │   tarsy                                                 │   │
│  │   :8080                                                 │   │
│  │                                                         │   │
│  │   Go backend + dashboard static files                   │   │
│  │   REST API, WebSocket, worker pool                      │   │
│  └──────────────────────┬──────────────────────────────────┘   │
│                         │                                      │
│                         │ gRPC :50051 (localhost in pod)        │
│                         ▼                                      │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │   llm-service                                           │   │
│  │   :50051                                                │   │
│  │                                                         │   │
│  │   Python gRPC — stateless LLM proxy                     │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                │
└────────────────────────────────────────────────────────────────┘

┌─ Database (separate pod or managed) ───────────────────────────┐
│   PostgreSQL :5432                                             │
│   Options: pod with PVC, AWS RDS, Azure Database, etc.         │
└────────────────────────────────────────────────────────────────┘
```

### Access Paths

**Browser (user) access:**
```
User (Browser) → OpenShift Route (:443, edge TLS)
  → oauth2-proxy (:4180)
  → GitHub OAuth flow → cookie
  → tarsy (:8080) → dashboard / API / WebSocket
```

**API client access (programmatic — replaces old TARSy's JWT):**
```
API client (with K8s/OpenShift ServiceAccount token)
  → OpenShift Route or Service (:8443)
  → kube-rbac-proxy (:8443)
  → Validates SA token against K8s API
  → Enforces RBAC (SubjectAccessReview)
  → tarsy (:8080) → API endpoints
```

Old TARSy used custom JWT infrastructure (RS256 keys, JWKS endpoint, token generation CLI) for programmatic API access. New TARSy replaces all of that with `kube-rbac-proxy` — no custom token code in TARSy itself. API clients authenticate using native Kubernetes ServiceAccount tokens, and access is controlled via standard RBAC (ClusterRoles/RoleBindings). This is simpler, more secure (no key management), and integrates with the existing K8s/OpenShift identity model.

**Internal (pod-local):**
```
tarsy → llm-service:50051 (gRPC, localhost within pod)
tarsy → postgres (K8s Service DNS or managed DB endpoint)
```

### Environment Parity with podman-compose (Phase 9)

| Component | podman-compose (Phase 9) | OpenShift (Phase 10) |
|-----------|--------------------------|----------------------|
| oauth2-proxy | Container, :4180 exposed as :8080 | Sidecar in pod, :4180 |
| kube-rbac-proxy | Not present | Sidecar in pod, :8443 |
| tarsy | Container, :8080 internal | Container in pod, :8080 |
| llm-service | Container, :50051 internal | Container in pod, :50051 |
| postgres | Container, :5432 | Separate pod or managed DB |
| LLM_SERVICE_ADDR | `llm-service:50051` (compose DNS) | `localhost:50051` (pod-local) |

The same container images are used in both environments. The only differences are:
1. **kube-rbac-proxy** — added in prod for API client auth
2. **LLM_SERVICE_ADDR** — `llm-service:50051` in compose (separate network namespaces) vs `localhost:50051` in OpenShift (shared pod network)
3. **TLS** — edge termination at OpenShift Route; HTTP in compose
4. **Secrets** — `.env` file in compose; K8s Secrets in OpenShift
5. **Database** — container in compose; managed or separate pod in OpenShift

---

## Scope (To Be Detailed)

The following will be fully designed when Phase 10 begins:

- **Kustomize manifests** — base + overlays (development, staging, production)
- **Deployment** — pod spec with 4 containers, resource limits, probes
- **Services** — ClusterIP for internal, Route for external
- **ConfigMaps** — tarsy.yaml, oauth2-proxy.cfg, llm-providers.yaml
- **Secrets** — API keys, DB credentials, OAuth client secret
- **Routes/Ingress** — TLS edge termination, path-based routing
- **ImageStreams** — for OpenShift internal registry
- **kube-rbac-proxy configuration** — RBAC rules, SA token validation
- **Health probes** — liveness, readiness, startup per container
- **Build pipeline** — GitHub Actions for image build + push
- **Rollout strategy** — rolling update with health gate
