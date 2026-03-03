# Session Authorization — Sketch Questions

**Status:** All decisions made
**Related:** [Sketch document](session-authorization-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: What level of authorization granularity is needed?

This is the foundational question. The answer determines complexity, configuration burden, and how much infrastructure is needed. Each level builds on the previous — you can start simple and evolve.

### Option C: Full RBAC with Casbin policy engine

- **Pro:** Maximum flexibility — define arbitrary policies like "group X can see sessions of alert_type Y in chain Z".
- **Pro:** Battle-tested — Casbin is used by ArgoCD, proven at scale.
- **Pro:** Policy changes don't require code changes — update policy section in config.
- **Pro:** Extensible to fine-grained actions later (e.g., "can delete sessions", "can use chat").
- **Pro:** Surprisingly lightweight — Casbin is in-process, no external service, sub-millisecond evaluation. ArgoCD's full RBAC is essentially: load model + load policies + call `enforcer.Enforce()` per request.
- **Con:** New dependency (Casbin library).
- **Con:** Operators need to learn Casbin policy syntax (mitigated by ArgoCD precedent in K8s ecosystem).

**Decision:** Option C — full RBAC with Casbin from the start. ArgoCD demonstrates this is low-effort to implement (hardcoded model, config-based policies, single `Enforce()` call per request) while providing maximum flexibility. The K8s ecosystem familiarity with Casbin policy syntax (via ArgoCD) reduces the learning curve for operators.

_Considered and rejected: Option A (author-only — no team collaboration), Option B (team-based without RBAC — would need to be replaced when policy complexity grows), Option D (start with B, design for C — unnecessary indirection since Casbin itself is lightweight enough to start with)._

---

## Q2: How should sessions be scoped for authorization?

Each session needs an authorization scope so Casbin can control who sees what. Since multiple teams may use the same alert types and chains, `alert_type` alone is not a sufficient scope. The solution is a first-class **project** concept — like ArgoCD's `AppProject`.

A project is an **authorization boundary**, not an organizational structure. It's more flexible than "team":
- One team can work across multiple projects (e.g., SRE has `prod-incidents` and `security-investigations`)
- One project can be accessible to multiple teams (e.g., shared `incident-response` project)
- Projects can represent workstreams, environments, products — not just teams

### Option E: First-class `project` field on sessions (ArgoCD AppProject model)

- **Pro:** Clean model — mirrors ArgoCD exactly. Sessions belong to a project, Casbin policies control access per project.
- **Pro:** Decoupled from alert_type — both teams can use `SecurityInvestigation` under different projects without seeing each other's sessions.
- **Pro:** Casbin resource scope is `project/session_id`, same pattern as ArgoCD's `project/app`.
- **Pro:** More flexible than "team" — projects can span teams or subdivide a team's work.
- **Pro:** Works for both dashboard users and API clients — project is always explicit, validated by Casbin.
- **Con:** New required field on `AlertSession` and in the alert submission API.
- **Con:** Dashboard needs a project selector for users with access to multiple projects.

**How the project is set:**

- **Dashboard users with one project:** Auto-selected (no extra UX step).
- **Dashboard users with multiple projects:** Project selector in the UI — user picks which project the session belongs to before submitting.
- **API clients:** Specify `"project": "prod-incidents"` in the alert payload. Casbin validates the caller has `create` permission on sessions in that project.
- **System-initiated sessions (no caller identity):** Require a `project` in the payload, or fall back to a configured default project.

**Example Casbin policies:**

```
# Group mappings (OIDC groups → roles)
g, team-sre, role:sre
g, team-platform, role:platform
g, platform-admins, role:admin

# Project access (role, resource, action, project/*, effect)
p, role:sre, sessions, *, prod-incidents/*, allow
p, role:sre, sessions, *, security-investigations/*, allow
p, role:platform, sessions, *, prod-incidents/*, allow       # shared project
p, role:admin, sessions, *, */*, allow                        # admins see all
```

**Decision:** Option E — first-class `project` field on sessions, following the ArgoCD AppProject model. Dashboard shows a project selector for multi-project users. API clients specify project explicitly, validated by Casbin. Casbin policies use `project/*` as the resource scope.

_Considered and rejected: Option A (store author's OIDC groups — doesn't support "both teams use same alert_type" case), Option B (derive from alert_type — fails when multiple teams share alert types), Option C (resolve dynamically — complex lookups), Option D (hybrid — unnecessary complexity when a first-class project is cleaner)._

---

## Q3: Should TARSy use an existing authorization library or custom filtering?

Given Q1's decision for full RBAC, the question is which library to use.

### Option B: Casbin (in-process policy engine)

- **Pro:** Battle-tested — used by ArgoCD, Harbor, many K8s projects.
- **Pro:** Policy defined in config — operators can change policies without code changes.
- **Pro:** Supports RBAC, ABAC, and custom matchers — grows with requirements.
- **Pro:** Tiny library — in-process, no external service, sub-millisecond evaluation.
- **Pro:** Familiar to K8s ecosystem operators (ArgoCD precedent).
- **Con:** New dependency (~20k GitHub stars, actively maintained, but still a dependency).
- **Con:** Learning curve for Casbin model/policy syntax (mitigated by ArgoCD precedent and online editor).

**Decision:** Option B — Casbin. Natural consequence of Q1's decision for full RBAC. In-process, lightweight, and the ArgoCD precedent makes the policy syntax familiar to K8s operators.

_Considered and rejected: Option A (custom filtering — would mean reimplementing what Casbin already does), Option C (external service like OpenFGA/Ory Keto — overkill, requires separate deployment and adds network latency)._

---

## Q4: Where does authorization config live?

Authorization needs configuration: Casbin model (hardcoded), policies (group mappings + permissions), and project definitions.

### Option A: In `tarsy.yaml` under a new `authorization` section

- **Pro:** Consistent — all TARSy config in one place.
- **Pro:** Supports template variables (e.g., `{{.ADMIN_GROUPS}}`).
- **Pro:** Validated at startup alongside other config.
- **Con:** Requires restart on config change (same as all other tarsy.yaml config).

**Decision:** Option A — keep in `tarsy.yaml` for consistency. Authorization config is small and changes infrequently. ArgoCD's ConfigMap approach is equivalent.

_Considered and rejected: Option B (separate policy file — inconsistent with how all other TARSy config works), Option C (tarsy.yaml with external file override — two code paths for loading config, unnecessary complexity)._

---

## Q5: How should the WebSocket be authorized?

The WebSocket (`GET /api/v1/ws`) streams real-time session events. Currently, all connected clients receive events for all sessions. With authorization, clients should only receive events for sessions they can see.

### Option A: Filter events server-side before broadcasting

- **Pro:** Secure — unauthorized events never reach the client.
- **Pro:** Transparent to the frontend — it just receives fewer events.
- **Con:** Requires checking authorization for every event broadcast, which adds overhead.
- **Con:** The event publisher needs access to the authorization context (user identity + groups).

**Decision:** Option A — filter server-side for correctness. The WebSocket endpoint **must not** be left unprotected when REST authorization is enabled — this would leak session metadata (titles, statuses, project names) to unauthorized users via real-time events.

**Hard gate:** When `authorization.enabled: true`, the `/api/v1/ws` handler must either:
1. Apply the same Casbin enforcer to filter events per-connection (preferred — ship in the same release as REST authZ), **or**
2. Reject WebSocket connections with HTTP 403 and a clear log message (`"WebSocket connections rejected: authorization enabled but WebSocket filtering not yet implemented"`) until filtering is implemented.

Option 2 is acceptable as a temporary gate during development, but must not ship to production without a clear deprecation timeline. The default posture is fail-closed: if in doubt, reject the connection.

_Considered and rejected: Option B (client subscribes to specific IDs — client could manipulate subscriptions to access unauthorized sessions), Option C (defer — inconsistent UX, users see real-time updates for sessions they can't access), phased rollout without a gate (leaks session metadata via WebSocket while REST is locked down)._

---

## Q6: Should there be a "shared" / "public" visibility option for sessions?

Some sessions might be useful for everyone (e.g., postmortems, reference investigations). Should sessions support explicit visibility overrides?

### Option C: Defer — not needed for initial implementation

- **Pro:** Reduces scope.
- **Pro:** Can be added later if the need arises.
- **Pro:** Cross-team sharing is already achievable via Casbin policies — grant multiple roles access to the same project, or create a shared project.

**Decision:** Option C — defer. Casbin policies already support cross-team sharing by granting multiple roles access to a project. No special "public" flag needed. Admins see everything by default.

_Considered and rejected: Option A (no sharing ever — too rigid), Option B (per-session public flag — risk of accidental exposure, extra field/UI for a rare use case)._
