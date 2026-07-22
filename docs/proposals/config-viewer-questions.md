# Read-Only Configuration Viewer — Design Questions

**Status:** All decisions made
**Related:** [Design document](config-viewer-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: What is the source of truth for the viewer?

Config is loaded as YAML, env-expanded, merged with builtins, then held in registries. Operators may want either “what files said” or “what the process is running.”

### Option A: Effective in-memory config (post-merge registries)

- **Pro:** Matches runtime behavior; answers “what will agents actually use?”
- **Pro:** Already available on `Server.cfg`; no file I/O or parse round-trip.
- **Pro:** Includes builtins the YAML file might omit.
- **Con:** Not identical to `tarsy.yaml` / `llm-providers.yaml` on disk.
- **Con:** Env templates are already expanded — sanitizer must scrub values carefully.

**Decision:** Option A — effective in-memory config (post-merge registries).

_Considered and rejected: Option B (raw YAML — misses builtins/merges; files already in ConfigMap/git), Option C (both — extra surface for v1)._

---

## Q2: API shape: one endpoint or many?

### Option A: Single `GET /api/v1/system/config`

- **Pro:** One round-trip; simple client; matches “snapshot” mental model.
- **Pro:** Consistent with how operators think about the two YAML files as one deployment config.
- **Con:** Larger payload if all sections always returned.
- **Con:** Harder to evolve per-section caching later (unlikely needed — config is boot-static).

**Decision:** Option A — single `GET /api/v1/system/config` snapshot.

_Considered and rejected: Option B (sectioned endpoints — extra glue for modest payloads), Option C (`?sections=` — unused flexibility for v1)._

---

## Q3: Where does the UI live?

### Option A: Section/tabs on existing System Status page (`/system`)

- **Pro:** Natural home next to MCP health; one “how is this deployment configured and healthy?” place.
- **Pro:** Reuses page shell, nav entry, and mental model.
- **Con:** Page grows; need clear separation so health polling and static config don’t blur.

**Decision:** Option A — tabs/sections on existing System Status page (`/system`).

_Considered and rejected: Option B (new page — splits system concerns), Option C (drawer — wrong density for browsing configs)._

---

## Q4: How should config be presented?

### Option C: Structured primary + “View as YAML/JSON” secondary

- **Pro:** Browse + export without choosing one forever.
- **Con:** Slightly more UI; must guarantee both views use the same sanitized DTO.

**Decision:** Option C — structured primary UI, with YAML/JSON secondary from the same sanitized DTO.

_Considered and rejected: Option A (structured only — no easy export), Option B (YAML/JSON only — harder to navigate large merges)._

---

## Q5: How do we sanitize MCP transport?

This is the critical security decision. After `ExpandEnv`, transport fields may hold live secrets.

### Option A (+ thin B): Allowlist DTO with safe substitutes

- **Pro:** Fail-closed: only fields we deliberately copy appear.
- **Pro:** Clear contract for future fields (token-exchange, etc.).
- **Pro:** Still shows *that* tokens/env were configured via `bearer_token_set`, `env_keys`, and `"***"` for args/url when present.
- **Con:** Operators do not see exact args/url values (by design).

Include: `type`, `command` (as-is, or `"***"` if secret-looking — post-verify guard), `verify_ssl`, `timeout`, `env_keys` (keys only), `bearer_token_set` (bool); omit or `"***"` for `args`/`url`. Never emit raw `Env` values or `BearerToken`. New transport fields denylist-by-default.

**Decision:** Option A with thin B flavor — allowlist DTO as above. Later refined: best-effort heuristic redacts `command` to `"***"` when it looks secret-bearing (see design doc).

_Considered and rejected: Option B alone (heuristic redaction of full structs — easy to forget new fields), Option C (restore templates — dual-source complexity)._

---

## Q6: What sections are in v1?

### Option A: Full effective config (agents, chains, MCP, LLM providers, skills metadata, defaults, queue, system)

- **Pro:** One place for the whole deployment picture (both YAML files’ concerns).
- **Pro:** Avoids a second feature request immediately after ship.
- **Con:** Larger DTO and UI.

**Decision:** Option A — full effective config in the main snapshot. Skills in the snapshot are metadata only; bodies via Q7.

_Considered and rejected: Option B (LLM + MCP only — omits most of tarsy.yaml), Option C (omit skills/system — arbitrary cut)._

---

## Q7: Include skill bodies?

### Option C: Metadata in list; body on demand (`GET …/config/skills/{name}`)

- **Pro:** Best of both when needed — small main snapshot, full body for drill-down.
- **Pro:** Keeps Q6’s full-config snapshot lean while still answering “what did `load_skill` inject?”
- **Con:** Extra endpoint beyond the single snapshot (Q2); acceptable as a nested detail route.

**Decision:** Option C — skills metadata in `GET /api/v1/system/config`; body via `GET /api/v1/system/config/skills/{name}`.

_Considered and rejected: Option A (metadata only — no drill-down when debugging skills), Option B (full bodies in snapshot — large payload / broader exposure)._

---

## Q8: Include agent/MCP instructions?

`custom_instructions` and MCP `instructions` are not API keys, but they often encode org-specific operational guidance.

### Option A: Include them (they are first-class config)

- **Pro:** Accurate picture of agent behavior; primary reason to inspect config.
- **Pro:** Already in YAML that admins deploy; same audience as the dashboard’s auth proxy.
- **Con:** Visible to all authenticated users (same as today for other `/api/v1` data).

**Decision:** Option A — include agent `custom_instructions` and MCP `instructions` as first-class config.

_Considered and rejected: Option B (omit/truncate — guts the useful part), Option C (UI reveal only — security theater if API still returns full text)._

---

## Q9: Distinguish builtins from YAML overrides?

Builtins (`pkg/config/builtin.go`) merge with user YAML. Operators sometimes need to know “did I override this?”

### Option A: Show merged effective values only (no provenance)

- **Pro:** Simple; matches runtime.
- **Con:** Cannot tell builtin vs custom without comparing to docs/source.

**Decision:** Option A — merged effective values only for v1; no provenance tracking.

_Considered and rejected: Option B (source annotations — loader rework), Option C (static builtins section — drifts from running merge)._

---

## Q10: Who can access the config view?

### Option A: Same as other `/api/v1/system/*` — any authenticated user

- **Pro:** Zero new auth machinery; consistent with MCP status today.
- **Pro:** Matches current deployment model (oauth2-proxy at the edge).
- **Con:** No admin-only distinction until session-authorization / RBAC ships.

**Decision:** Option A — any authenticated user; document org-wide visibility. Align with future RBAC when it lands.

_Considered and rejected: Option B (defer until RBAC — blocks useful tool), Option C (one-off admin group — parallel auth that Casbin will replace)._

---

## Q11: Copy/export affordances?

### Option B: “Copy sanitized JSON/YAML” button

- **Pro:** High leverage for support; uses the same DTO the UI shows.
- **Con:** Tiny bit of frontend work.

**Decision:** Option B — copy button on the YAML/JSON secondary view (Q4). No file download in v1.

_Considered and rejected: Option A (browse only — awkward for tickets), Option C (file download — overlaps with copy for most cases)._
