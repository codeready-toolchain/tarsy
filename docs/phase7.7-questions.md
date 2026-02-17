# Phase 7.7: System Views & Queue Metrics — Open Questions

## Q1 ✅ MCP Server Status Page

**Decision: Include in 7.7 as a full page.** Backend already fully supports it — `GET /api/v1/system/mcp-servers` returns health status, tool counts, full tool list (name + description), last check time, and error details per server. Frontend types and API function already exist. Purely a frontend page.

Rejected alternatives: minimal table view (backend supports full data, no reason to limit), defer (backend ready now), skip (useful for debugging beyond what SystemWarningBanner shows).

---

## Q2 ✅ Queue Metrics Enrichment

**Decision: Skip.** Queue visibility is already well-covered by Phase 7.2 — `ActiveAlertsPanel` shows active sessions, `QueuedAlertsSection` shows pending sessions with cards and cancel actions. Worker pool capacity, utilization, and detailed queue metrics are operational monitoring concerns, not dashboard UX. Belongs in a future monitoring/observability phase (Prometheus, Grafana), not the user-facing dashboard.

Rejected alternatives: full queue metrics panel (operational monitoring, not end-user UX), lightweight worker pool chip (marginal value), defer (no reason to revisit — Phase 7.2 covers it).

---

## Q3 ✅ Wait Time Estimates

**Decision: Skip entirely.** Not needed. Backend doesn't support it and there's no user demand. Queue position is already visible through Phase 7.2's queued alerts section.

Rejected alternative: adding backend computation + frontend display (unnecessary scope creep, operational concern).

---

## Q4 ✅ Navigation to MCP Status Page

**Decision: Hamburger menu in DashboardView's AppBar.** Consistent with existing navigation pattern — the hamburger menu already has menu items. MCP status is a system-level view accessed from the main dashboard, not needed on every page.

Rejected alternatives: SharedHeader link (too prominent for an ops page), VersionFooter link (too hidden), direct URL only (poor discoverability).
