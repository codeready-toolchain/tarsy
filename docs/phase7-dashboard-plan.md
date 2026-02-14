# Phase 7: Dashboard — Detailed Plan

## Overview

Port the old TARSy React dashboard to the new Go-based TARSy backend, preserving all existing UX while adapting to the new API surface and WebSocket event protocol. The trace view (formerly debug/technical tab) moves from an in-page tab to a dedicated page.

### Old TARSy Reference & Hybrid Approach

> **⚠️ CRITICAL: Hybrid Approach — Read This Before Implementing Any Phase**
>
> The old TARSy dashboard is the **authoritative source** for all visual and UX decisions.
> The plan describes **what** each component does; the old code determines **how it looks**.
>
> **What to copy from the old dashboard (visual layer):**
> - Exact text labels, column headers, tooltips, placeholder text, empty-state messages
> - MUI component selection (Paper, Chip, Accordion, Table, etc.) and `sx` prop values
> - Spacing values (mt, mb, px, py, gap)
> - Interaction patterns (hover states, click behavior, animations, transitions)
> - Layout structure (flex directions, widths, alignment)
> - Favicon, page title, AppBar structure, navigation menu
>
> **What to rewrite (data layer):**
> - TypeScript interfaces and types (all new, matching Go backend)
> - API calls and data fetching (Axios client → new Go endpoints)
> - WebSocket event handling (new channel-based protocol)
> - State management hooks and logic
> - Services (auth, API, WebSocket)
>
> **When plan text conflicts with the old dashboard's visual layer → the old dashboard wins.**
> The plan uses API field names (e.g. `author`, `chain_id`) for brevity; display labels
> must match the old dashboard (e.g. "Submitted by", "Agent Chain").
>
> **One deliberate exception:** The old "Debug" / "Technical" tab is rebranded as **"Trace"**
> and promoted to a dedicated page (`/sessions/:id/trace`). This is an intentional UX change,
> not an oversight. All other labels and visual patterns should match the old dashboard exactly.
>
> **Before implementing any component:** Read the corresponding old dashboard component
> first. Copy its visual layer. Then wire the new data layer underneath.

The old TARSy codebase lives at `/home/igels/Projects/AI/tarsy-bot` and is the primary reference for UX parity:

| Area | Path |
|------|------|
| **Dashboard (frontend)** | |
| Dashboard root | `/home/igels/Projects/AI/tarsy-bot/dashboard/` |
| React components | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/components/` |
| Services (API, WS, auth) | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/services/` |
| Hooks | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/hooks/` |
| Contexts | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/contexts/` |
| Types | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/types/` |
| Utils & parsers | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/utils/` |
| Constants | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/constants/` |
| MUI theme | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/theme/` |
| Tests | `/home/igels/Projects/AI/tarsy-bot/dashboard/src/test/` |
| **Backend (Python/FastAPI)** | |
| Backend root | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/` |
| API controllers | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/controllers/` |
| WebSocket controller | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/controllers/websocket_controller.py` |
| Event publisher | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/events/publisher.py` |
| Event models | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/models/event_models.py` |
| History service (queries) | `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/history_service/` |
| Config (chains, agents) | `/home/igels/Projects/AI/tarsy-bot/config/` |

### Goals

1. **Feature parity**: Every user-visible feature from the old dashboard is preserved
2. **Same UX**: Visual design, layout, interactions remain identical (99%+ match). This means exact labels, spacing, component choices, tooltips, empty states, and interaction patterns from the old code. The plan describes what to build; the old code is the spec for how it looks.
3. **New backend**: All data comes from the new Go API; no legacy Python backend dependency
4. **Improved architecture**: Cleaner data flow, better real-time protocol, no blind copying of data logic
5. **Trace view evolution**: Technical/debug view becomes a dedicated "Trace" page (not a tab)

### Non-Goals (Deferred)

- Authentication backend (Phase 9 — dashboard includes full auth UI placeholders with graceful degradation; backend wiring in Phase 9)
- Runbook browsing/fetching (Phase 8.1 — dashboard includes free-text runbook URL input as placeholder)
- Multi-LLM provider display (Phase 8.2)
- Slack integration UI (Phase 8.3)

---

## Prerequisites

The new TARSy backend (Phases 1–6) is complete. The dashboard needs additional backend API endpoints before frontend work can begin. These are grouped as Phase 7.0.

---

## Sub-Phase Breakdown

### Phase 7.0: Backend API Extensions ✅ DONE

**Goal**: Expose all REST endpoints the dashboard requires. This is backend-only Go work — no frontend. Many session-level endpoints already exist (timeline, trace, cancel, chat) — this phase adds the missing list/filter/system endpoints and enriches the existing session detail response.

**Deliverables**:

1. **Session list endpoint** *(new)* — `GET /api/v1/sessions`
   - Pagination (page, page_size)
   - Filters: status (multi), alert_type, chain_id, search (text), date range
   - Sorting: created_at, status, alert_type, author, duration
   - Response: `SessionListItem` DTO with pre-computed stats (token totals, interaction counts, stage counts, chat message count, duration)

2. **Active sessions endpoint** *(new)* — `GET /api/v1/sessions/active`
   - Returns in-progress + pending sessions with progress metadata
   - Used by active alerts panel and queued alerts section

3. **Session detail enrichment** *(existing — enrich)* — Enrich existing `GET /api/v1/sessions/:id`
   - Add computed fields: `chat_enabled`, `chat_id`, `chat_message_count`, `total_stages`, `completed_stages`, `has_parallel_stages`, token totals, `duration_ms`
   - Add `stages` array with stage metadata (id, name, index, status, parallel_type, agent count)

4. **Session summary endpoint** *(new)* — `GET /api/v1/sessions/:id/summary`
   - Lightweight stats: interaction counts, token totals, duration, chain statistics

5. **Filter options endpoint** *(new)* — `GET /api/v1/sessions/filter-options`
   - Returns distinct values for: alert_types, chain_ids, statuses

6. **System endpoints** *(new)*:
   - `GET /api/v1/system/warnings` — System warnings from `SystemWarningsService`
   - `GET /api/v1/system/mcp-servers` — MCP server statuses from `HealthMonitor`
   - `GET /api/v1/system/default-tools` — Default tools configuration

7. **Alert metadata endpoint** *(new)*:
   - `GET /api/v1/alert-types` — Available alert types from config (chain registry)

8. **Progress status events** *(new)* — Two levels of transient WebSocket progress events:
   - **Session-level** (`session.progress` on `sessions` channel): current stage name/index, total stages, active execution count, status text — for the active alerts panel
   - **Execution-level** (`execution.progress` on `session:{id}` channel): per-agent phase + message — for the session detail page parallel agent tabs
   - `ProgressPhase` enum: `investigating`, `gathering_info`, `distilling`, `concluding`, `synthesizing`, `finalizing`
   - Retrofit publishing into: controllers (investigating), tool executor (gathering_info), summarization (distilling), chain orchestrator (concluding, finalizing), parallel executor (synthesizing)

   **Interaction event** *(new)* — `interaction.created` on `session:{id}` channel:
   - Fired when LLM or MCP interaction is saved to DB (interactions are created as complete records)
   - Lightweight notification payload: `{ session_id, stage_id, execution_id, interaction_id, interaction_type: "llm"|"mcp" }`
   - Used by trace view for live updates (event-notification → REST re-fetch pattern)

9. **Health endpoint enrichment** *(existing — enrich)* — Add `version` field to health endpoint response
   - Build-time version string (git tag or build hash)
   - Used by dashboard for version monitoring and footer display

10. **WebSocket route change** *(existing — move)* — Move `/ws` → `/api/v1/ws`
    - All sensitive endpoints under `/api/*` for single oauth2-proxy auth rule
    - Protocol versioning alignment with REST API

**Dependencies**: None (builds on Phase 6 codebase)

---

### Phase 7.1: Dashboard Foundation ✅ DONE

**Goal**: Set up the React project with build tooling, theme, routing, shared layout, and core services (API client, WebSocket).

**Deliverables**:

1. **Project setup** — React 19 + TypeScript + Vite in `web/dashboard/` (existing PoC `dashboard/` directory is removed)
   - Package.json with all dependencies (MUI 7, React Router 7, Axios, date-fns, react-markdown, react-syntax-highlighter, react-json-view-lite)
   - Vite config with dev proxy to Go backend
   - TypeScript types for all API responses and WebSocket events
   - ESLint + Prettier configuration

2. **Theme** — MUI theme matching old dashboard (colors, typography, spacing)

3. **Routing** — React Router with routes:
   - `/` → Dashboard (session list)
   - `/sessions/:id` → Session detail (conversation)
   - `/sessions/:id/trace` → Trace view (dedicated page)
   - `/submit-alert` → Manual alert submission

4. **Shared layout** — `SharedHeader`, `VersionFooter`, `VersionUpdateBanner`, `SystemWarningBanner` (component shells — wired to real data in later sub-phases)

5. **Auth UI** — Full auth placeholders with graceful degradation (Q3.2)
   - `AuthContext` with oauth2-proxy integration
   - Login button (redirects to `/oauth2/sign_in`), user menu (`X-Forwarded-User`), logout button
   - `services/auth.ts` — checks `/oauth2/userinfo`, handles 401 redirects
   - Graceful degradation: no auth elements shown when oauth2-proxy is not configured

6. **API service** — Axios client with:
   - Base URL configuration
   - Retry logic (502/503/504)
   - Error handling (401 → `authService.handleAuthError()`)
   - All endpoint methods typed

7. **WebSocket service** — Adapted for new protocol:
   - Channel subscription model (`sessions`, `session:{id}`)
   - Event type handling (unified `session.status`, `stage.status`, `timeline_event.*`, `stream.chunk`, `session.progress`, `execution.progress`, `chat.created`, `interaction.created`)
   - Reconnect with exponential backoff
   - Auto-catchup on subscribe + `catchup.overflow` handling (full REST reload)
   - Keepalive (ping/pong)

8. **Go static file serving** — Serve dashboard build from Go backend
   - Serve `web/dashboard/dist/` from root `/`
   - SPA fallback (all non-API, non-WS, non-health routes → index.html)
   - API routes (`/api/*`, `/health`) take priority over static files (WebSocket is at `/api/v1/ws`)

**Dependencies**: Phase 7.0 (API endpoints must exist)

---

### Phase 7.2: Session List & Dashboard ✅ DONE

**Goal**: Main dashboard page with active alerts, queued alerts, and historical alert list.

> **Hybrid approach reminder**: `DashboardView` owns the full page layout including the AppBar
> (hamburger menu, logo, "TARSy" title, fancy LIVE/Offline badge, auth elements), filter panel,
> content area, and version footer — exactly matching the old `DashboardView.tsx`. Do NOT use
> a generic `SharedHeader`; copy the old AppBar verbatim.

**Deliverables**:

1. **Dashboard layout** — `DashboardView` with three sections:
   - Active alerts panel (in-progress sessions) — header: "Active Alerts"
   - Queued alerts section (pending sessions) — header: "Queued Alerts"
   - Historical alert list (completed/failed/cancelled/timed_out) — header: "Alert History"

2. **Active alerts panel** — `ActiveAlertsPanel`
   - WebSocket status indicator (Live/Offline chip)
   - Active session cards with progress (chain stage, progress phase)
   - Auto-refresh on WebSocket events
   - Real-time progress via `session.progress` events (session-level, from `sessions` channel)

3. **Queued alerts section** — `QueuedAlertsSection`
   - Pending sessions with wait time
   - Cancel from queue action

4. **Historical alert list** — `HistoricalAlertsList`
   - Table with columns matching old dashboard display labels:
     Status | ∥ (Parallel) | Type | Agent Chain | Submitted by | Time | Duration | Tokens | 💬 (Chat) | Actions
   - Sortable columns: status, alert_type, author, created_at, duration
   - Pagination (page size: **10**/25/50/100 — matching old dashboard)
   - Status badge, parallel stage indicator, chat message count, executive summary hover

5. **Filter panel** — `FilterPanel`
   - Basic filter row (matching old dashboard): Search | Status (multi-select) | Time Range button | Clear All
   - Time Range button opens `TimeRangeModal` dialog with:
     - Quick Select presets: 10m, 1h, 12h, 1d, 7d, 30d
     - Custom Range with date-time pickers (`@mui/x-date-pickers`)
   - Alert type and agent chain filters (were in old dashboard's "Advanced" section — temporarily in basic row or deferred)
   - Filter persistence in localStorage

6. **Real-time list updates** — Subscribe to `sessions` channel
   - New session appears → refresh active/queued
   - Session completes → moves to historical
   - Status changes reflected live

**Dependencies**: Phase 7.1 (foundation), Phase 7.0 (session list + active sessions + filter options endpoints)

---

### Phase 7.3: Alert Submission

**Goal**: Manual alert submission form — enables end-to-end testing of the dashboard (submit alert → watch it appear in session list → view session detail).

**Deliverables**:

1. **Manual alert submission** — `ManualAlertSubmission`
   - Alert type selector (from `GET /api/v1/alert-types`)
   - Alert data input (textarea)
   - Runbook URL input (free-text — Phase 8.1 replaces with browsing/dropdown)
   - MCP server/tool selection (`MCPSelection` component)
   - Submit button → `POST /api/v1/alerts`
   - Redirect to session detail on success

**Dependencies**: Phase 7.1, Phase 7.0 (alert types endpoint, `POST /api/v1/alerts`)

---

### Phase 7.4: Session Detail & Conversation Timeline

**Goal**: Session detail page with conversation timeline, live streaming, and stage progress.

> **Hybrid approach reminder**: The session detail page is the most complex UI in the dashboard.
> Read the old `SessionDetailPageBase.tsx`, `SessionHeader.tsx`, `ConversationTimeline.tsx`,
> `ChainProgressCard.tsx`, and all timeline event renderers before writing any code. Copy the
> exact visual structure, then wire the new data layer.

**Deliverables**:

1. **Session detail page** — `SessionDetailPage`
   - Session header: status badge, cancel button, token usage summary, MCP server summary
   - Segmented control (Conversation | Trace) in header — routes to `/sessions/:id` and `/sessions/:id/trace`
   - Original alert card (collapsible)
   - Final analysis card (collapsible, with markdown rendering)
   - Executive summary section
   - Auto-scroll toggle

2. **Conversation timeline** — `ConversationTimeline`
   - Chat-flow-style rendering of timeline events:
     - `llm_thinking` → Thinking indicator/expandable content
     - `llm_response` → Assistant response bubble (markdown)
     - `llm_tool_call` → Tool call card (server.tool, args, result)
     - `mcp_tool_summary` → Summary indicator
     - `final_analysis` → Final analysis marker
     - `user_question` → User chat message (from follow-up chat)
     - `executive_summary` → Executive summary display
     - `code_execution` → Code execution display
     - `google_search_result` → Search result display
     - `url_context_result` → URL context display
     - `error` → Error display
   - Stage separators showing stage transitions
   - Parallel stage handling (tabs for multi-agent stages)

3. **Live streaming** — Real-time content during active sessions
   - Subscribe to `session:{id}` channel
   - `timeline_event.created` (status=streaming) → create placeholder
   - `stream.chunk` (delta) → append to active event
   - `timeline_event.completed` → finalize content
   - `StreamingContentRenderer` with typewriter effect
   - Typing indicator during streaming

4. **Stage progress** — Visual stage progression
   - Stage progress bar (completed / total)
   - Stage status chips (current, completed, failed)
   - `stage.status` events for live updates
   - `execution.progress` for per-agent granular status text

5. **Markdown rendering** — react-markdown with:
   - Syntax-highlighted code blocks
   - Tables, lists, links
   - Custom renderers matching old dashboard

**Dependencies**: Phase 7.1, Phase 7.0 (enriched session detail + timeline endpoint)

---

### Phase 7.5: Chat Interface

**Goal**: Follow-up chat UI for completed sessions.

**Deliverables**:

1. **Chat panel** — `ChatPanel`
   - Collapsible panel at bottom of session detail
   - "Have follow-up questions?" prompt
   - Chat availability check (session terminal + chat enabled)

2. **Chat input** — `ChatInput`
   - Multiline text input
   - Character counter and limit
   - Send button / keyboard shortcut
   - Cancel button (during active execution)
   - Disabled states (sending, session not terminal)

3. **Chat message list** — `ChatMessageList`
   - User messages (from `user_question` timeline events)
   - Assistant responses (agent timeline events after chat stage)
   - Live streaming of assistant responses
   - Scroll management

4. **Chat state management** — `useChatState` hook
   - Optimistic user message display (from POST response, no `chat.user_message` event in new TARSy)
   - Active execution tracking
   - WebSocket event handling for chat stages (`stage.status`, `timeline_event.*`, `stream.chunk`)
   - Cancel execution support

**Dependencies**: Phase 7.4 (session detail page, streaming infrastructure)

---

### Phase 7.6: Trace View

**Goal**: Dedicated trace page for observability information, replacing the old technical/debug tab. Available for all sessions (active and terminated) with live updates for active sessions.

> **Hybrid approach note**: The trace view is structurally new (dedicated page vs in-page tab),
> but individual interaction cards and detail views should match the old dashboard's
> `TraceTimeline.tsx`, `LLMInteractionCard.tsx`, `MCPInteractionCard.tsx` visual patterns.

**Deliverables**:

1. **Trace page** — `/sessions/:id/trace`
   - Shared session header with segmented control (Conversation | Trace) — same component as conversation view
   - Trace segment active; clicking Conversation navigates back to `/sessions/:id`

2. **Stage/execution hierarchy** — `TraceTimeline`
   - Accordion-based stage list
   - Stage header: status icon, name, parallel badge, agent names, interaction counts
   - Within each stage: execution groups
   - Within each execution: chronological interactions (LLM + MCP)
   - Parallel stages: tabs for each agent execution

3. **Live updates** — Event-notification + REST re-fetch pattern:
   - Subscribe to `session:{id}` channel (already subscribed from page load)
   - On `stage.status` event → re-fetch `GET /sessions/:id/trace` (stage hierarchy changed)
   - On `interaction.created` event → re-fetch `GET /sessions/:id/trace` (new interaction appeared)
   - No streaming or complex state — just re-fetch the full trace tree on each event
   - Works for both active and terminated sessions (terminated sessions simply receive no events)

4. **Interaction cards** — `InteractionCard`
   - LLM interactions: type, model, tokens, duration, expandable detail
   - MCP interactions: server, tool, duration, expandable detail
   - Click to expand → full details loaded from trace API (always returns complete data since interactions are saved as complete records)

5. **LLM interaction detail** — `LLMInteractionDetail`
   - Full reconstructed conversation (system, user, assistant, tool messages)
   - Token breakdown
   - Timing information
   - Model info
   - Request/response metadata
   - Syntax-highlighted JSON for raw request/response

6. **MCP interaction detail** — `MCPInteractionDetail`
   - Server and tool names
   - Arguments (JSON formatted)
   - Tool result (with truncation indicator)
   - Available tools list
   - Timing and error info

7. **Copy functionality** — "Copy Entire Flow" button for chain debugging

**Dependencies**: Phase 7.1, Phase 7.0 (trace endpoints + `interaction.created` event)

---

### Phase 7.7: System Views & Queue Metrics

**Goal**: System status pages and queue metrics enrichment.

**Deliverables**:

1. **MCP server status page** — Dedicated system status view:
   - MCP server health, tool counts, error details
   - Data from `GET /api/v1/system/mcp-servers`
   - Note: Per-session MCP summary in the session header is built in Phase 7.4

2. **Queue metrics enrichment** — Additional queue/pool stats not covered by Phase 7.2:
   - Worker pool info (capacity, active workers from `GET /health`)
   - Queue depth and wait time estimates
   - Note: Active/queued panels with cards and real-time updates are built in Phase 7.2

**Dependencies**: Phase 7.1, Phase 7.0 (system endpoints)

---

### Phase 7.8: Polish & Integration

**Goal**: Final polish, cross-cutting concerns, production readiness.

**Deliverables**:

1. **Error handling** — Global error boundaries, API error display, network error recovery

2. **Loading states** — Skeleton screens, loading spinners, progress indicators

3. **Auto-scroll** — Smart auto-scroll during streaming (user scroll detection, bottom-following)

4. **Responsive design** — Mobile-friendly layouts (following old dashboard's responsive patterns)

5. **Version monitoring** — Wire `VersionUpdateBanner` and `VersionFooter` (components from 7.1)
   - `useVersionMonitor` hook: polls health endpoint, compares `version` to build-time UI version
   - `VersionUpdateBanner` on mismatch → prompts refresh
   - `VersionFooter` shows both UI and backend versions (useful during rolling updates)

6. **System warning banner** — Wire `SystemWarningBanner` (component from 7.1)
   - Polls `/api/v1/system/warnings` periodically
   - Displays active warnings, dismissible per-session

7. **localStorage persistence** — Filters, pagination, sort preferences, panel states

8. **Production build** — Optimized Vite build, asset hashing

**Dependencies**: All previous Phase 7 sub-phases

---

## Dependency Graph

```
Phase 7.0 (Backend APIs)
    │
    ├─→ Phase 7.1 (Foundation)
    │       │
    │       ├─→ Phase 7.2 (Session List)
    │       │
    │       ├─→ Phase 7.3 (Alert Submission)
    │       │
    │       ├─→ Phase 7.4 (Session Detail + Conversation)
    │       │       │
    │       │       └─→ Phase 7.5 (Chat)
    │       │
    │       ├─→ Phase 7.6 (Trace View)
    │       │
    │       └─→ Phase 7.7 (System + Queue Metrics)
    │
    └─→ Phase 7.8 (Polish) — after all above
```

Phases 7.2, 7.3, 7.4, 7.6, and 7.7 can be developed in parallel after 7.1 is complete. Phase 7.3 is prioritized early to enable end-to-end testing. Phase 7.5 depends on 7.4 (shared session detail page). Phase 7.8 is the final pass.

---

## Estimated Scope

| Sub-Phase | Backend (Go) | Frontend (React) | Complexity |
|-----------|-------------|-------------------|------------|
| 7.0 | Heavy | None | Medium-High |
| 7.1 | Light (static serving) | Heavy (setup) | Medium |
| 7.2 | None | Heavy | Medium |
| 7.3 | None | Light-Medium | Low-Medium |
| 7.4 | None | Very Heavy | High |
| 7.5 | None | Medium | Medium |
| 7.6 | None | Heavy | Medium-High |
| 7.7 | None | Light-Medium | Low-Medium |
| 7.8 | Light | Medium | Low-Medium |

**Total**: ~9 sub-phases. Phase 7.0 and 7.1 are foundational. Phase 7.3 (alert submission) is prioritized early for testability. The core UI work is 7.2–7.7.
