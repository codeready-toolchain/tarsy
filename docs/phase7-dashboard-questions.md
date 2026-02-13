# Phase 7: Dashboard — Open Questions

Questions requiring discussion before implementation. Organized by category. UX-visible changes are marked with **[UX]** — these MUST be decided before the affected sub-phase.

---

## 1. UX-Visible Changes

### Q1.1 ✅ Debug View — Page vs Tab Navigation

**Decision: Segmented control (Conversation | Debug) in the session header.** Routes to separate pages (`/sessions/:id` and `/sessions/:id/debug`) but visually feels like the old tab UX. Both views share the session header. Can revisit if it feels redundant.

Rejected alternatives: simple button navigation (less discoverable), slide-out drawer (too complex for the content volume).

---

### Q1.2 ✅ Debug View URL Pattern

**Decision: `/sessions/:id/debug`** — sub-path of session. Clear hierarchy, session → debug relationship obvious.

Rejected alternative: `/debug/sessions/:id` (breaks session URL grouping).

---

### Q1.3 ✅ Resume Session Feature

**Decision: Drop entirely.** New TARSy has no paused state — the status model is clean without it (pending, in_progress, cancelling, completed, failed, cancelled, timed_out). No Resume button in the UI. Can be added in a future phase if needed.

Rejected alternative: adding pause/resume to the new backend (significant complexity for a rarely-used feature).

---

### Q1.4 ✅ Per-Agent Cancellation

**Decision: Drop per-agent cancel.** Only session-level cancellation in the UI. The old per-agent cancel existed to support the pause/resume flow (users could resume or cancel individual agents). Since pause/resume is dropped (Q1.3), per-agent cancel loses its primary use case. Session-level cancel is sufficient.

Rejected alternatives: adding per-agent cancel to backend (complexity without the pause/resume context), deferring (no demand expected).

---

### Q1.5 ✅ Search Functionality

**Decision: Simple ILIKE now, upgrade to full-text search later if needed.** `GET /api/v1/sessions?search=keyword` with PostgreSQL `ILIKE` on `alert_data` and `final_analysis`. Same API contract works for a future GIN/tsvector upgrade — no frontend changes required.

Rejected alternatives: full-text search upfront (over-engineering for current dataset size).

---

### Q1.6 ✅ Session List — Duration Sorting

**Decision: Compute duration in SQL query.** `ORDER BY (completed_at - started_at)` — no schema change needed. NULL durations (pending/in-progress sessions) sort last.

Rejected alternative: persisting `duration_ms` column (redundant data, requires migration).

---

## 2. Architecture Decisions

### Q2.1 ✅ Dashboard Hosting Strategy

**Decision: Go serves static files (single app).** Go binary serves `web/dashboard/dist/` via Echo's static middleware with SPA fallback. Single container in production.

Key benefits over old TARSy's two-container model:
- **Auth simplified**: oauth2-proxy → one Go upstream (vs. routing to separate UI + API containers)
- **No CORS**: UI and API on same origin — no headers, no route tricks, no cross-origin WebSocket issues
- **Always in sync**: One image contains both API and UI — no version mismatch between containers
- **Dev experience preserved**: Vite dev server (HMR, instant updates) proxies `/api` + `/ws` to Go. Dev and prod hosting are decoupled.

Dev mode: `npm run dev` (Vite port 5173, proxies to Go port 8080) — no auth, no oauth2-proxy.
Prod mode: `oauth2-proxy → Go (serves static + API + WS)` — single upstream.

Rejected alternatives: go:embed (dashboard rebuild forces Go rebuild, slower iteration), separate Nginx (two containers, CORS complexity, matches old model we're improving on).

---

### Q2.2 ✅ Frontend Project Location

**Decision: `web/dashboard/`** — within the tarsy repo. Single repo keeps API types in sync, `web/` prefix cleanly separates frontend from Go code. The existing PoC dashboard (`dashboard/`) will be completely removed.

Rejected alternatives: `dashboard/` top-level (clutters root, PoC removal needed), separate repo (API drift, cross-repo coordination).

---

### Q2.3 ✅ Session Detail — One Rich Endpoint vs Multiple Calls

**Decision: Parallel fetch pattern.** Keep separate endpoints (`GET /sessions/:id`, `GET /sessions/:id/timeline`, `GET /sessions/:id/debug`); frontend fetches in parallel via `Promise.all`. Debug data loaded only when the debug page is visited. Clean API design, no waterfall.

Rejected alternatives: single rich endpoint (over-fetches, complex handler), current separate-but-sequential (unnecessary waterfall).

---

### Q2.4 ✅ Session Status Update on WebSocket Events

**Decision: Hybrid approach.** Optimistic status badge update from WebSocket event (instant feedback), then background re-fetch of `GET /sessions/:id` for full computed data (token counts, stage counts, etc.).

Rejected alternatives: optimistic-only (incomplete data), re-fetch-only (delayed visual feedback).

---

## 3. Feature Scope

### Q3.1 ✅ Alert Types and Runbooks

**Decision: Implement alert-types now, stub runbooks.** `GET /api/v1/alert-types` returns chain-derived alert types. Runbook field in alert form is a free-text URL input (user can paste a URL). When Phase 8.1 adds runbook fetching/GitHub integration, the dropdown/browser replaces the free text.

Rejected alternatives: implementing both now (pulls Phase 8 into Phase 7), hiding runbooks entirely (unnecessary feature regression).

---

### Q3.2 ✅ Auth UI

**Decision: Auth placeholders — do as much UI work as possible now.** Login button (redirects to `/oauth2/sign_in`), user menu (shows user from `X-Forwarded-User` header), logout button (redirects to `/oauth2/sign_out`). Graceful degradation when oauth2-proxy is not configured (no auth elements shown). When Phase 9 adds the backend auth layer, the UI should need minimal or zero changes.

Rejected alternatives: no auth UI (more Phase 9 work), stub-only context (delays UI work unnecessarily).

---

### Q3.3 ✅ Queue Metrics & System Views

**Decision: Full implementation matching old TARSy UX.** This is core Phase 7 scope, not deferred. Includes: active alerts panel (active count, queued count, WebSocket status), queued alerts section (wait time, cancel from queue), MCP server status, system warnings banner, worker pool info. All data comes from `GET /sessions/active`, `GET /health`, `GET /api/v1/system/warnings`, `GET /api/v1/system/mcp-servers`. Any missing backend endpoints go into Phase 7.0.

---

### Q3.4 ✅ Version Monitoring

**Decision: Full version monitoring.** Still relevant with single-app model (Q2.1):
- **Refresh banner**: Browser caches JS bundle. After rolling update, user has stale UI until refresh. Health endpoint returns `version` → dashboard polls → detects mismatch → shows "New version available" banner.
- **Footer versions**: During rolling updates (multiple replicas, OpenShift), user's API calls may hit a different-version replica than the one that served their JS. Footer shows "UI: v1.0 / Backend: v1.1" for debugging. Versions converge once rollout completes.

Backend: add `version` field to health endpoint. Frontend: `useVersionMonitor` hook + `VersionUpdateBanner` + version footer, matching old TARSy UX.

---

## 4. Implementation Approach

### Q4.1 ✅ Component Porting Strategy

**Decision: Hybrid approach.** Copy structure, layout, and MUI component usage from old dashboard (proven visual layer). Rewrite all data fetching, event handling, services, hooks, types, and state management to match the new backend API and WebSocket protocol. TypeScript interfaces are all new.

Rejected alternatives: clean-room rewrite (slower, risk of missing subtle behaviors), blind copy-and-adapt (carries tech debt).

---

### Q4.2 ✅ Testing Strategy

**Decision: Integration tests for data layer + critical components.** Unit tests for `api.ts`, `websocket.ts`, `timelineParser.ts`. Component tests for complex UI (conversation timeline, debug view). Skip E2E browser tests for now — good balance of confidence vs effort.

Rejected alternatives: no tests (no regression safety), full component tests (too much effort), E2E browser tests (slow, brittle, infrastructure overhead).

---

### Q4.3 ✅ Build and CI Integration

**Decision: Makefile targets.** `make dashboard-install`, `make dashboard-build`, `make dashboard-dev` wrapping npm commands. `make build` produces both Go binary and dashboard bundle. Consistent with existing workflow.

Rejected alternative: standalone package.json scripts only (fragmented build, easy to forget).

---

## 5. Data Mapping Details

### Q5.1 ✅ Timeline Event Metadata Structure

**Action item (not a question).** Audit backend code during Phase 7.1 to confirm all metadata fields per event type and define TypeScript discriminated union types. Known fields documented here for reference:

| Event Type | Metadata Fields |
|------------|----------------|
| `llm_tool_call` | `server_name`, `tool_name`, `arguments`, `is_error` (on completion) |
| `llm_thinking` | (none currently) |
| `llm_response` | (none currently) |
| `mcp_tool_summary` | `server_name`, `tool_name` |
| `code_execution` | `language`, `outcome` |
| `google_search_result` | `queries` |
| `url_context_result` | `urls` |
| `final_analysis` | (none currently) |
| `executive_summary` | (none currently) |
| `error` | `error_type` |

---

### Q5.2 ✅ WebSocket Event — Session Context for List Page

**Decision: Re-fetch on status change (Option B).** Same pattern as old TARSy — on `session.status` event from `sessions` channel, re-fetch `GET /sessions/active` for active/queued panels. For historical list, re-fetch only on terminal status changes. Simple, proven, no protocol changes needed. Can optimize with enriched payloads later if frequency becomes an issue.

Rejected alternatives: enriched event payload (protocol change, premature optimization), per-session fetch (N+1 risk).

---

## Decision Log

Track decisions as they're made:

| Question | Decision | Date | Notes |
|----------|----------|------|-------|
| Q1.1 | Option B: Segmented control (Conversation \| Debug) | 2026-02-13 | Can revisit if it feels redundant |
| Q1.2 | Option A: `/sessions/:id/debug` | 2026-02-13 | Clear hierarchy |
| Q1.3 | Option A: Drop pause/resume | 2026-02-13 | No paused state in new TARSy |
| Q1.4 | Option A: Drop per-agent cancel | 2026-02-13 | Was tied to pause/resume flow |
| Q1.5 | Option C: ILIKE now, full-text later | 2026-02-13 | Same API contract either way |
| Q1.6 | Option A: Compute in SQL | 2026-02-13 | No schema change |
| Q2.1 | Option A: Go serves static files | 2026-02-13 | Single app, no CORS, simple auth |
| Q2.2 | Option A: `web/dashboard/` | 2026-02-13 | Single repo, clean separation |
| Q2.3 | Option C: Parallel fetch | 2026-02-13 | Clean API, no waterfall |
| Q2.4 | Option C: Hybrid (optimistic + re-fetch) | 2026-02-13 | Instant feedback + accurate data |
| Q3.1 | Option A: Alert-types now, runbooks as free-text | 2026-02-13 | Phase 8 replaces with full runbook UI |
| Q3.2 | Option B: Auth placeholders with graceful degradation | 2026-02-13 | Max UI now, minimal Phase 9 changes |
| Q3.3 | Full implementation, old TARSy parity | 2026-02-13 | Core Phase 7 scope |
| Q3.4 | Option A: Full version monitoring | 2026-02-13 | Still needed for rolling updates |
| Q4.1 | Option C: Hybrid (copy visual, rewrite data) | 2026-02-13 | Preserve UX, clean data layer |
| Q4.2 | Option D: Data layer + critical component tests | 2026-02-13 | Balance of confidence vs effort |
| Q4.3 | Option A: Makefile targets | 2026-02-13 | Consistent with existing workflow |
| Q5.1 | Action item: audit during Phase 7.1 | 2026-02-13 | Define TS types from backend code |
| Q5.2 | Option B: Re-fetch on status change | 2026-02-13 | Same pattern as old TARSy |
