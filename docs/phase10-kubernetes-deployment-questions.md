# Phase 10: Kubernetes/OpenShift Deployment — Open Questions

Questions where the design departs from old TARSy, involves non-obvious trade-offs, or has multiple valid approaches.

---

## Q1: Pod Topology — Single Pod vs Separate Deployments

**Context:** Old TARSy used 3 separate Deployments (backend+oauth2-proxy, dashboard, database). New TARSy's design doc proposes a single Deployment with 4 containers (tarsy, llm-service, oauth2-proxy, kube-rbac-proxy) + a separate database Deployment.

**Option A — Single Pod (4 containers)**
All application containers in one pod, sharing `localhost` network. LLM service reached at `localhost:50051`. One Deployment to manage.

Pros:
- Simplest networking — `localhost` for inter-container communication (no Service DNS, no network policies)
- Matches podman-compose topology (minus kube-rbac-proxy) — tested locally
- Single rollout — all containers update atomically
- Sidecar pattern is standard K8s practice for auth proxies

Cons:
- Can't scale tarsy and llm-service independently (but TARSy is single-replica anyway)
- Any container crash restarts the entire pod (but with individual container restarts via `restartPolicy`)
- LLM service memory spike affects tarsy's resource limits

**Option B — Separate Deployments (tarsy pod + llm-service pod)**
tarsy (+ oauth2-proxy + kube-rbac-proxy) in one Deployment, llm-service in another. Communicate via K8s Service DNS.

Pros:
- Independent scaling (irrelevant for TARSy)
- Independent resource limits and OOM handling
- LLM service can restart without affecting tarsy

Cons:
- More complex networking — need a separate Service for llm-service gRPC
- `LLM_SERVICE_ADDR` would be `llm-service:50051` in prod (matches compose but diverges from the "pod-local" simplicity)
- Two Deployments to manage, two rollouts to coordinate
- gRPC over network is slower than localhost (negligible for this use case)

**Recommendation:** A. Single pod is simpler, matches the sidecar pattern, and TARSy doesn't need independent scaling. The resource concern is addressed by setting individual container resource limits within the pod spec.

---

## Q2: API Client Route — External or Internal Only?

**Context:** kube-rbac-proxy provides API client auth via ServiceAccount tokens. The question is whether external (non-cluster) clients need access, or only in-cluster pods/jobs.

**Option A — Internal only (ClusterIP Service, no Route)**
kube-rbac-proxy is accessible only within the cluster via the `tarsy-api` ClusterIP Service. External API clients are not supported — only in-cluster ServiceAccounts can authenticate.

Pros:
- Simpler — no external Route for API
- No TLS passthrough Route complexity
- ServiceAccount tokens don't leave the cluster (more secure)
- All external access goes through oauth2-proxy (single entry point)

Cons:
- External systems (CI pipelines, monitoring, external tools) can't call TARSy API programmatically without being in-cluster

**Option B — External Route (TLS passthrough)**
Separate Route with TLS passthrough to kube-rbac-proxy. External clients can use long-lived SA tokens (created via `oc create token --duration`) to call the API.

Pros:
- External CI/CD pipelines and monitoring tools can submit alerts
- Matches the "replaces JWT" promise — old TARSy's JWT tokens were used externally
- Flexibility for future integrations

Cons:
- SA tokens exposed outside cluster (security concern, but tokens are scoped and revocable)
- Requires separate Route hostname (can't mix edge + passthrough TLS on one Route)
- More complex Makefile (ROUTE_HOST_API variable)

**Recommendation:** B. Old TARSy's JWT was used by external alerting systems to submit alerts programmatically. The same capability needs to exist in new TARSy, and kube-rbac-proxy with an external Route is the replacement.

---

## Q3: Image Registry Strategy

**Context:** Old TARSy used quay.io (via GitHub Actions CI) for "official" images and the OpenShift internal registry (via `make openshift-push-*`) for rapid dev iteration.

**Option A — Dual registry (quay.io + OpenShift internal)**
GitHub Actions builds on push to main → pushes to quay.io. Developers use `make openshift-push-*` to push directly to OpenShift internal registry during development. Production/staging pulls from quay.io, development overlay pulls from internal registry.

Pros:
- Same pattern as old TARSy (proven)
- CI images are versioned and available outside the cluster
- Dev workflow is fast (no CI wait)

Cons:
- Two image sources to manage
- Overlay needs different image references per environment

**Option B — quay.io only**
All images go through quay.io. Dev builds are pushed to quay.io with `dev-<sha>` tags via CI or manually.

Pros:
- Single source of truth for images
- No OpenShift internal registry dependency

Cons:
- Slower dev iteration (push to external registry)
- Requires quay.io credentials on dev machines

**Recommendation:** A. Dual registry matches the proven old TARSy workflow. Fast dev iteration via internal registry, versioned CI images via quay.io.

---

## Q4: Overlay Strategy — Development Only or Multiple?

**Context:** Old TARSy had only a `development` overlay. Production deployment was not yet established.

**Option A — Development overlay only (start here)**
Single `overlays/development/` matching old TARSy. Add staging/production overlays when those environments exist.

Pros:
- Minimal initial scope
- No premature abstraction for environments that don't exist yet
- Matches old TARSy

Cons:
- Have to add more overlays later

**Option B — Development + production overlays from the start**
Pre-create `overlays/production/` with production-appropriate values (higher resources, different image source, stricter security).

Pros:
- Design for production from day one
- Forces consideration of env-specific differences

Cons:
- Production overlay will be untested until a production environment exists
- May need significant revision when production requirements are known

**Recommendation:** A. Start with development only. The base manifests are designed to be environment-agnostic; adding a production overlay later is straightforward (change namespace, image refs, resource limits, config values).

---

## Q5: Secrets Management — OpenShift Template vs Alternatives

**Context:** Old TARSy used an OpenShift Template (`secrets-template.yaml`) processed via `oc process` to create secrets from environment variables. Other approaches exist (SealedSecrets, External Secrets Operator, manual `oc create secret`).

**Option A — OpenShift Template (keep old TARSy pattern)**
`oc process -f secrets-template.yaml -p KEY=value | oc apply -f -`

Pros:
- Proven pattern (old TARSy)
- Self-documenting (template lists all parameters with descriptions)
- Auto-generation for passwords/cookie secrets (`generate: expression`)
- Works offline (no external operator dependencies)

Cons:
- OpenShift-specific (not portable to vanilla K8s)
- Secrets are in env vars during `oc process` invocation

**Option B — SealedSecrets**
Encrypt secrets client-side, commit encrypted manifests to git. SealedSecrets controller decrypts in-cluster.

Pros:
- Secrets can be safely committed to git (encrypted)
- GitOps-friendly

Cons:
- Requires SealedSecrets controller installed in cluster
- Additional operational complexity
- Key rotation management

**Option C — External Secrets Operator (ESO)**
Secrets synced from external vault (AWS Secrets Manager, HashiCorp Vault, etc.).

Pros:
- Enterprise-grade secret management
- Centralized secret rotation

Cons:
- Requires ESO + external vault infrastructure
- Over-engineered for current TARSy deployment

**Recommendation:** A. OpenShift Template is proven, simple, and sufficient. Migrate to SealedSecrets or ESO if TARSy moves to GitOps or multi-environment at scale.

---

## Q6: MCP Server Access in Containers

**Context:** TARSy's MCP client connects to MCP servers. Servers using `stdio` transport spawn a subprocess (e.g., `kubectl` for the Kubernetes MCP server). Servers using `http`/`sse` transport connect over the network. In production, stdio-transport MCP servers need their binaries and credentials available inside the tarsy container.

**Option A — Kubeconfig volume mount only (current approach)**
Mount the MCP kubeconfig as a Secret volume. The Kubernetes MCP server's stdio binary (`kubectl`) must be available in the tarsy container image. Other stdio MCP servers would also need their binaries in the image.

Pros:
- Simple — no additional infrastructure
- Matches old TARSy pattern (kubeconfig was mounted the same way)

Cons:
- Adding new MCP servers with stdio transport requires rebuilding the tarsy image with their binaries
- `kubectl` binary adds size to the image

**Option B — Remote MCP servers only**
In production, all MCP servers use `http`/`sse` transport. No stdio MCP servers in containers. Kubernetes MCP access provided by a remote MCP server Deployment (separate pod running an MCP server with HTTP transport).

Pros:
- Cleaner separation — tarsy doesn't need MCP server binaries
- MCP servers can be independently updated/scaled
- Better security boundary

Cons:
- More infrastructure to manage (separate Deployment per MCP server)
- Network latency for MCP calls

**Option C — Defer to Phase 11 (MCP server deployment strategy)**
Include kubeconfig mount in Phase 10 for backward compatibility, but defer the comprehensive MCP server deployment strategy to a later phase.

Pros:
- Phase 10 stays focused on core deployment
- Buys time to evaluate MCP server landscape

Cons:
- May ship with incomplete MCP support in prod

**Recommendation:** C. Mount the kubeconfig secret for the Kubernetes MCP server (matching old TARSy), but defer broader MCP server deployment strategy. The `kubectl` binary may need to be added to the tarsy Dockerfile if stdio transport is used in prod.

---

## Q7: Database Migration Strategy

**Context:** TARSy uses Ent ORM's auto-migration (`Schema.Create()`) which runs on every startup. This is additive-only (add tables, add columns, add indexes — never drops or renames).

**Option A — Auto-migration on startup (current approach)**
Every tarsy container startup runs `Schema.Create()`. Rolling updates are safe because Ent only adds — old code ignores new columns.

Pros:
- Zero manual intervention
- Already implemented and tested
- Safe for rolling updates (additive changes only)

Cons:
- Can't do destructive schema changes (drop column, rename table) without manual work
- Migration runs on every restart (fast — Ent checks existing schema first)
- If migration fails, pod never becomes ready (which is actually the correct behavior)

**Option B — Separate migration Job**
Run migrations as a K8s Job before the Deployment rollout. tarsy container assumes schema is correct.

Pros:
- Migration and serving are decoupled
- Can run destructive migrations with more control
- Clearer rollback semantics

Cons:
- Adds complexity (Job definition, ordering, failure handling)
- Must coordinate Job completion with Deployment rollout
- Over-engineered for additive-only Ent migrations

**Recommendation:** A. Auto-migration on startup is simple, proven, and safe for Ent's additive model. A migration Job is unnecessary overhead when the ORM handles schema evolution automatically.

---

## Q8: Container Resource Limits

**Context:** Resource requests and limits need to balance container performance against cluster capacity. Old TARSy had: backend 512Mi/1Gi (memory), 500m/1000m (CPU); oauth2-proxy 128Mi/512Mi, 100m/500m; dashboard 128Mi/512Mi, 100m/500m.

**Option A — Conservative limits (as in design doc)**
| Container | Request (mem/cpu) | Limit (mem/cpu) |
|-----------|-------------------|-----------------|
| tarsy | 512Mi / 500m | 1Gi / 1000m |
| llm-service | 512Mi / 500m | 2Gi / 1000m |
| oauth2-proxy | 128Mi / 100m | 512Mi / 500m |
| kube-rbac-proxy | 64Mi / 50m | 128Mi / 200m |

Pros:
- Based on old TARSy experience
- llm-service gets 2Gi limit (Python + LLM SDK can be memory-hungry)
- kube-rbac-proxy is lightweight (64Mi request is typical)

Cons:
- May need tuning after observing real usage
- Total pod: ~1.2Gi request, ~3.7Gi limit

**Option B — Higher limits for llm-service**
Same as A but llm-service gets 1Gi/4Gi (memory) for larger model contexts.

Pros:
- More headroom for LLM operations
- Prevents OOM during large prompt processing

Cons:
- Higher resource reservation

**Recommendation:** A initially, with monitoring. Adjust based on actual pod resource usage in the development environment. The llm-service limit can be increased in the overlay if needed.

---

## Q9: Namespace Strategy

**Context:** Old TARSy used `tarsy-dev` namespace for development. Production namespace was not defined.

**Option A — Single namespace per environment (tarsy-dev, tarsy-staging, tarsy-prod)**
Each environment gets its own namespace. Kustomize overlays set the namespace.

Pros:
- Clean isolation between environments
- Standard K8s pattern
- RBAC scoped per namespace

Cons:
- CRDs and ClusterRoles are cluster-scoped (shared)

**Option B — Single shared namespace (tarsy) with labels**
All environments in one namespace, distinguished by labels and name prefixes.

Pros:
- Fewer namespaces to manage

Cons:
- No isolation between environments
- Risk of cross-environment interference

**Recommendation:** A. One namespace per environment is the standard and safer approach. Development overlay uses `tarsy-dev` (matching old TARSy).

---

## Q10: Serving Certificate for kube-rbac-proxy

**Context:** kube-rbac-proxy requires TLS certificates for its HTTPS listener (:8443). Multiple approaches exist for certificate provisioning.

**Option A — OpenShift service serving certificates**
Annotate the `tarsy-web` Service with `service.beta.openshift.io/serving-cert-secret-name: tarsy-serving-cert`. OpenShift automatically generates and rotates TLS certificates in the named Secret.

Pros:
- Zero manual certificate management
- Automatic rotation
- OpenShift-native (well-tested pattern)

Cons:
- OpenShift-specific (not portable to vanilla K8s)
- Certificate is signed by the cluster's service CA (internal only, which is fine for our use case)

**Option B — Manual certificate (cert-manager or self-signed)**
Use cert-manager to issue certificates, or generate self-signed certificates manually.

Pros:
- Portable to vanilla K8s
- More control over certificate parameters

Cons:
- Additional infrastructure (cert-manager) or manual management
- cert-manager is overkill for an internal service certificate

**Recommendation:** A. OpenShift service serving certificates are the simplest and most reliable approach for an OpenShift deployment. If vanilla K8s support is needed later, switch to cert-manager.
