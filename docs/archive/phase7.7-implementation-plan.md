# Phase 7.7: System Views & Version/Warning Wiring — Implementation Plan

## Scope (Expanded from Design Doc)

Phase 7.7 now includes items moved from Phase 7.8 so that 7.8 is purely polish. Queue metrics enrichment was dropped (Q2 — already covered by Phase 7.2, rest belongs to monitoring).

| # | Feature | Source | Status in new dashboard |
|---|---------|--------|------------------------|
| 1 | **Fix HealthResponse type** | Prerequisite | Type doesn't match Go backend |
| 2 | **Version monitoring** (`useVersionMonitor`, `VersionContext`) | Moved from 7.8 | Hook + context don't exist yet |
| 3 | **VersionFooter wiring** | Moved from 7.8 | Shell: shows dashboard version only |
| 4 | **VersionUpdateBanner wiring** | Moved from 7.8 | Shell: returns null |
| 5 | **SystemWarningBanner wiring** (MCP health warnings) | Moved from 7.8 | Shell: returns null |
| 6 | **`app-version` meta tag + build wiring** | Moved from 7.8 | Not configured yet |
| 7 | **Update `App.tsx`** (VersionProvider, layout order) | Moved from 7.8 | No VersionProvider yet |
| 8 | **MCP server status page** | Original 7.7 | New page, backend ready |
| 9 | **Hamburger menu navigation** | Original 7.7 | New menu item for MCP page |

---

## Pre-Implementation: Type Fix

### Fix `HealthResponse` in `types/system.ts`

The current frontend `HealthResponse` type doesn't match the Go backend (`pkg/api/responses.go` + `pkg/queue/types.go` + `pkg/database/health.go`). This must be fixed first since `useVersionMonitor` and queue metrics both depend on it.

**Current (wrong):**
```typescript
export interface HealthResponse {
  // ...
  worker_pool?: {
    max_workers: number;
    active_workers: number;
    pending_sessions: number;
  };
  // database shape is also wrong
}
```

**Corrected (matching Go backend):**
```typescript
export interface DatabaseHealthStatus {
  status: string;
  response_time_ms: number;
  open_connections: number;
  in_use: number;
  idle: number;
  wait_count: number;
  wait_duration_ms: number;
  max_open_conns: number;
}

export interface WorkerHealth {
  id: string;
  status: 'idle' | 'working';
  current_session_id?: string;
  sessions_processed: number;
  last_activity: string;
}

export interface PoolHealth {
  is_healthy: boolean;
  db_reachable: boolean;
  db_error?: string;
  pod_id: string;
  active_workers: number;
  total_workers: number;
  active_sessions: number;
  max_concurrent: number;
  queue_depth: number;
  worker_stats: WorkerHealth[];
  last_orphan_scan: string;
  orphans_recovered: number;
}

export interface MCPHealthStatus {
  server_id: string;
  healthy: boolean;
  last_check: string;
  error?: string;
  tool_count: number;
}

export interface HealthResponse {
  status: string;
  version: string;
  database: DatabaseHealthStatus;
  phase: string;
  configuration: {
    agents: number;
    chains: number;
    mcp_servers: number;
    llm_providers: number;
  };
  worker_pool?: PoolHealth;
  mcp_health?: Record<string, MCPHealthStatus>;
  warnings?: SystemWarning[];
}
```

**Files:** `web/dashboard/src/types/system.ts`

---

## Step 1: `useVersionMonitor` Hook

**New file:** `web/dashboard/src/hooks/useVersionMonitor.ts`

**Port from old dashboard:** `/home/igels/Projects/AI/tarsy-bot/dashboard/src/hooks/useVersionMonitor.ts`

**What to copy (logic):**
- Poll interval: 30s
- Consecutive mismatch threshold: 2 (60s before banner)
- Backend version from `getHealth()` → `health.version`
- Dashboard version check from `index.html` meta tag `app-version`
- Initial mount guard (1s delay before allowing banner)

**What to adapt for new patterns:**
- Import `getHealth` (standalone function) instead of `apiClient.healthCheck()`
- Import `DASHBOARD_VERSION` from `../../config/env.ts`
- Named export: `export function useVersionMonitor(): VersionInfo`
- Interface `VersionInfo` exported from the same file

**Return type:**
```typescript
export interface VersionInfo {
  backendVersion: string | null;
  backendStatus: string;
  dashboardVersionChanged: boolean;
  refresh: () => Promise<void>;
}
```

---

## Step 2: `VersionContext`

**New file:** `web/dashboard/src/contexts/VersionContext.tsx`

**Port from old dashboard:** `/home/igels/Projects/AI/tarsy-bot/dashboard/src/contexts/VersionContext.tsx`

**What to copy:**
- `VersionContext` via `createContext`
- `VersionProvider` component wrapping `useVersionMonitor()`
- `useVersion()` hook with context validation

**What to adapt for new patterns:**
- Named exports: `export function VersionProvider(...)` and `export function useVersion(): VersionInfo`
- Use React 19 context pattern matching `AuthContext.tsx` pattern (check if new dashboard uses `.Provider` or direct `value={}`)

**Note:** New dashboard's `AuthContext.tsx` uses `<AuthContext value={state}>` (React 19 pattern without `.Provider`). The old dashboard uses `<VersionContext.Provider value={versionInfo}>`. We follow the **new dashboard pattern** (React 19).

---

## Step 3: Wire `VersionFooter`

**Edit file:** `web/dashboard/src/components/layout/VersionFooter.tsx`

**Port visual from old dashboard:** `/home/igels/Projects/AI/tarsy-bot/dashboard/src/components/VersionFooter.tsx`

**Copy from old:**
- Layout: `Box` footer with `mt: 4`, `mb: 2`, `py: 2`, `textAlign: 'center'`, `borderTop: '1px solid'`, `borderColor: 'divider'` (already matches)
- Version display logic:
  - Single version if dashboard === agent
  - Separate "Dashboard: X • Agent: Y" if different
  - "Loading version info..." when checking
  - "Agent: unavailable" on error
- `Tooltip` with `Agent status: ${backendStatus}` on hover (new — add)

**What to adapt:**
- Import `useVersion` from `../../contexts/VersionContext.tsx`
- Named export (already is): `export function VersionFooter()`
- Use `DASHBOARD_VERSION` from `../../config/env.ts` (already imported)

---

## Step 4: Wire `VersionUpdateBanner`

**Edit file:** `web/dashboard/src/components/layout/VersionUpdateBanner.tsx`

**Port visual from old dashboard:** `/home/igels/Projects/AI/tarsy-bot/dashboard/src/components/VersionUpdateBanner.tsx`

**Copy from old:**
- Sticky positioning: `position: 'sticky'`, `top: 0`, `zIndex: (theme) => theme.zIndex.appBar + 1`
- Pulse animation: `keyframes` (opacity 1 → 0.85 → 1, 2s), disabled for `prefers-reduced-motion`
- Alert: `severity="warning"`, `icon={<WarningIcon sx={{ fontSize: 28 }} />}`
- Action button: `Button variant="contained" color="warning"` with `RefreshIcon`, label "Refresh Now"
- Title: "New Dashboard Version Available!" (bold, 1.3rem)
- Subtitle: "Refresh now to get the latest updates." (1.05rem)
- `borderRadius: 0`, `py: 2.5`, `px: 3`

**What to adapt:**
- Old takes `show` prop from parent. New should use `useVersion()` hook directly (matches context pattern — the component reads its own state from context, no prop drilling).
- Named export: `export function VersionUpdateBanner()`
- `window.location.reload()` on click

---

## Step 5: Wire `SystemWarningBanner`

**Edit file:** `web/dashboard/src/components/layout/SystemWarningBanner.tsx`

**Port visual from old dashboard:** `/home/igels/Projects/AI/tarsy-bot/dashboard/src/components/SystemWarningBanner.tsx`

**Copy from old:**
- MUI: `Alert`, `AlertTitle`, `Box`, `Collapse`, `IconButton`
- Layout: `Box` with `mb: 2`, one `Alert` per warning
- Alert: `severity="warning"`, `mb: 1`
- Expand/collapse: `IconButton` with `ExpandMoreIcon`, `transform: rotate(180deg)` transition
- Details in `Collapse` with `Box sx={{ mt: 1, fontSize: '0.875rem', opacity: 0.9 }}`
- Title: "System Warning" per alert

**What to adapt:**
- Import `getSystemWarnings` (standalone function) instead of `apiClient.getSystemWarnings()`
- Old uses `warning.warning_id` — new backend returns `warning.id` (our types already have `id`)
- Poll interval: 10s (matching old dashboard)
- Named export: `export function SystemWarningBanner()`
- No props needed (self-contained polling)
- Error handling: silent (don't show fetch errors — warnings are non-critical)

---

## Step 6: Update `App.tsx`

**Edit file:** `web/dashboard/src/App.tsx`

**Port layout order from old dashboard:** The old App wraps everything in `VersionProvider` and places banners above the router.

**New layout:**
```tsx
<ThemeProvider theme={theme}>
  <CssBaseline />
  <VersionProvider>
    <AuthProvider>
      <VersionUpdateBanner />
      <SystemWarningBanner />
      <RouterProvider router={router} />
    </AuthProvider>
  </VersionProvider>
</ThemeProvider>
```

**Key decisions:**
- `VersionProvider` wraps `AuthProvider` (version monitoring doesn't need auth)
- `VersionUpdateBanner` before `SystemWarningBanner` (sticky at top, matching old layout order)
- Both banners inside `AuthProvider` (consistent context availability)

---

## Step 7: Add `app-version` Meta Tag

**Edit file:** `web/dashboard/index.html`

Add meta tag for dashboard version detection:
```html
<meta name="app-version" content="__APP_VERSION__" />
```

**Edit file:** `web/dashboard/vite.config.ts` (if needed)

Add build-time replacement of `__APP_VERSION__` with the actual version. Check if this is already configured. If not, add a Vite plugin or `define` to inject the version.

---

## Step 8: MCP Server Status Page

**New file:** `web/dashboard/src/pages/SystemStatusPage.tsx`
**New file:** `web/dashboard/src/components/system/MCPServerStatusView.tsx`

This is a new page with no old TARSy equivalent. Backend endpoint `GET /api/v1/system/mcp-servers` already exists and returns everything needed. Frontend types (`MCPServerStatus`, `MCPServersResponse`) and API function (`getMCPServers`) already exist in `types/system.ts` and `services/api.ts`.

### `SystemStatusPage.tsx`

Thin page wrapper (same pattern as `DashboardPage.tsx`, `SubmitAlertPage.tsx`):
- `SharedHeader` with title "System Status", back button enabled
- `MCPServerStatusView` component
- `VersionFooter`
- `FloatingSubmitAlertFab`

### `MCPServerStatusView.tsx`

Data fetching + display component showing all MCP servers:

**Data:**
- Fetch `getMCPServers()` on mount
- Optional: poll periodically (e.g. 15s, matching backend health check interval) or manual refresh button

**Layout per server (card or accordion pattern):**
- Server ID (title)
- Health status indicator: green `Chip` for healthy, red `Chip` for unhealthy
- Tool count badge
- Last check time (relative, e.g. "5s ago")
- Error message (if unhealthy) — in `Alert severity="error"`
- Expandable tool list: table or list of tool name + description

**MUI components:** `Paper`, `Chip`, `Accordion`/`AccordionSummary`/`AccordionDetails` (or `Collapse`), `Table`/`TableBody`/`TableRow`/`TableCell` for tool list, `Alert` for errors, `CircularProgress` for loading, `Typography`, `Box`

**Empty state:** "No MCP servers configured" message when servers array is empty.

**Error state:** Alert with retry button if API call fails.

---

## Step 9: Add Route + Hamburger Menu Navigation

### Add route in `App.tsx`

Add route to the `createBrowserRouter` configuration:
```typescript
{
  path: '/system',
  element: <SystemStatusPage />,
},
```

### Add route constant

**Edit file:** `web/dashboard/src/constants/routes.ts`

Add `SYSTEM_STATUS = '/system'` (or equivalent, following existing pattern).

### Add hamburger menu item in `DashboardView.tsx`

**Edit file:** `web/dashboard/src/components/dashboard/DashboardView.tsx`

Add a new `MenuItem` in the existing `<Menu>` component (after the "Manual Alert Submission" item):

```tsx
<MenuItem onClick={handleSystemStatus}>
  <ListItemIcon>
    <DnsIcon fontSize="small" />  {/* or StorageIcon, SettingsIcon */}
  </ListItemIcon>
  <ListItemText>System Status</ListItemText>
</MenuItem>
```

Add handler (same pattern as `handleManualAlertSubmission`):
```typescript
const handleSystemStatus = () => {
  window.open('/system', '_blank', 'noopener,noreferrer');
  handleMenuClose();
};
```

Import the icon: `DnsIcon` from `@mui/icons-material` (or `Storage`, `Settings` — whichever fits best visually).

---

## File Summary

All paths relative to `web/dashboard/src/`.

| Action | File | Description |
|--------|------|-------------|
| **Edit** | `types/system.ts` | Fix HealthResponse + add PoolHealth/DatabaseHealthStatus types |
| **Create** | `hooks/useVersionMonitor.ts` | Port from old dashboard, adapt to new patterns |
| **Create** | `contexts/VersionContext.tsx` | Port from old dashboard, adapt to new patterns |
| **Edit** | `components/layout/VersionFooter.tsx` | Wire to VersionContext, copy visual from old |
| **Edit** | `components/layout/VersionUpdateBanner.tsx` | Wire to VersionContext, copy visual from old |
| **Edit** | `components/layout/SystemWarningBanner.tsx` | Wire to API polling, copy visual from old |
| **Edit** | `App.tsx` | Add VersionProvider, layout order, `/system` route |
| **Edit** | `index.html` | Add app-version meta tag |
| **Maybe** | `vite.config.ts` | __APP_VERSION__ build-time replacement |
| **Create** | `pages/SystemStatusPage.tsx` | New page wrapper for system status |
| **Create** | `components/system/MCPServerStatusView.tsx` | MCP server health + tools display |
| **Edit** | `constants/routes.ts` | Add system status route constant |
| **Edit** | `components/dashboard/DashboardView.tsx` | Add "System Status" hamburger menu item |

---

## Decisions

All questions resolved — see `docs/phase7.7-questions.md` for full decision log.
