# Phase 7.8: Polish & Integration — Implementation Plan

## Scope Analysis

Phase 7.8 was originally the "final polish" pass: error handling, loading states, auto-scroll, responsive design, localStorage persistence, and production build. However, **nearly all of these were implemented proactively during Phases 7.1–7.7** as natural parts of each feature.

After comparing every Phase 7.8 deliverable against both the new dashboard codebase and the old dashboard reference, only **one small gap** remains.

| # | Deliverable | Status | Notes |
|---|-------------|--------|-------|
| 1 | Error handling | ✅ COMPLETE | ErrorBoundary, handleAPIError, retryOnTemporaryError, 401 interceptor, WS reconnect, component-level errors |
| 2 | Loading states | ✅ COMPLETE | Skeletons, spinners, InitializingSpinner, ProcessingIndicator, ProgressIndicator, TypingIndicator, Suspense lazy loading |
| 3 | Auto-scroll | ✅ COMPLETE | useAdvancedAutoScroll (MutationObserver, user scroll detection, threshold, debounce, characterData throttle) |
| 4 | Responsive design | ✅ COMPLETE | Matches old dashboard level (both had minimal responsive design) |
| 5 | localStorage persistence | ✅ COMPLETE | Filters, pagination, sort with save/load/clear |
| 6 | Production build | ⚠️ ONE GAP | Vite build + Go serving work, but cache headers missing on Go static serving |

---

## Deliverable 1: Error Handling — COMPLETE

All three layers from the design doc are fully implemented:

### Global error boundaries

- **`ErrorBoundary`** (`components/shared/ErrorBoundary.tsx`): Class component with `getDerivedStateFromError` + `componentDidCatch`. Shows MUI Alert with "Try Again" button, expandable stack trace details, optional gtag reporting. Matches old dashboard exactly.
- **Usage**: Wraps `OriginalAlertCard` content + per-field rendering — same pattern as old dashboard.

### API error display

- **`handleAPIError`** (`services/api.ts`): Extracts user-facing message from `response.data.message`, status codes, or network errors. Falls back to `"An unexpected error occurred"`.
- **Component usage**: `DashboardView` (activeError/historicalError), `TracePage`, `SessionDetailPage`, `InteractionCard`, `ChatPanel`, `MCPServerStatusView` — all show `Alert severity="error"` with dismiss/retry.

### Network error recovery

- **`retryOnTemporaryError`** (`services/api.ts`): Exponential backoff (500ms, 1s, 2s, 4s; cap 5s) on 502/503/504 and network errors. Skips cancelled requests. Used by `getSessions`, `getActiveSessions`, `getFilterOptions`.
- **401 interceptor**: Response interceptor redirects to `authService.handleAuthError()`.
- **WebSocket reconnect** (`services/websocket.ts`): Exponential backoff (200ms → 3s cap), keepalive ping/pong (20s/10s), `catchup.overflow` handling.

**No action needed.**

---

## Deliverable 2: Loading States — COMPLETE

All loading patterns from the design doc are implemented:

### Skeleton screens

- **SessionDetailPage**: `HeaderSkeleton` (circular avatar + text), `AlertCardSkeleton` (two rectangular blocks), `TimelineSkeleton` (3 rows with circular + text). Used as `Suspense` fallbacks for lazy-loaded components.
- **TracePage**: `HeaderSkeleton`, `TraceTimelineSkeleton`. Same Suspense pattern.

### Loading spinners

- **`CircularProgress`** on: ManualAlertForm submit button, MCPSelection loading, ActiveAlertsPanel/HistoricalAlertsList refresh buttons, AuthContext loading check.

### Progress indicators

- **`InitializingSpinner`**: Pulsing ring + shimmer "Initializing investigation…" text.
- **`ProcessingIndicator`**: Bouncing dots + shimmer text ("Processing…", "Investigating…", etc.).
- **`ProgressIndicator`**: Live duration display (ticking for active, final for terminal).
- **`TypingIndicator`**: Bouncing dots with optional icon and message.
- **`StreamingContentRenderer`**: In-progress tool call spinners.

### Lazy loading

- `SessionDetailPage`: `lazy()` for SessionHeader, OriginalAlertCard, FinalAnalysisCard, ConversationTimeline, ChatPanel.
- `TracePage`: `lazy()` for SessionHeader, TraceTimeline.

**No action needed.**

---

## Deliverable 3: Auto-Scroll — COMPLETE

The `useAdvancedAutoScroll` hook is fully implemented in `hooks/useAdvancedAutoScroll.ts`:

- **MutationObserver** on `[data-autoscroll-container]` for DOM changes.
- **User scroll detection**: `wheel`, `pointerdown`/`pointerup`, keyboard (arrows, Page Up/Down, Home, End, Space) events.
- **User interaction TTL**: 1.5s window to distinguish user scrolls from programmatic scrolls.
- **Debounced scroll**: Default 300ms delay.
- **CharacterData throttle**: 500ms for streaming text updates.
- **Smooth scroll with monitor**: `requestAnimationFrame`-based scroll with 2s safety timeout.

### Integration

- **SessionDetailPage**: `autoScrollEnabled` state with toggle switch for active sessions. Enabled when session becomes active, disabled ~2s after terminal. Initial scroll to bottom for active sessions. `data-autoscroll-container` attribute on content Box.
- **StreamingContentRenderer**: Local scroll for thinking blocks via `scrollContainerRef`.

**No action needed.**

---

## Deliverable 4: Responsive Design — COMPLETE

The new dashboard matches the old dashboard's responsive design level, which was minimal (no dedicated mobile layouts):

- **Container padding**: `px: { xs: 1, sm: 2 }` on SystemStatusPage, TracePage, SessionDetailPage, SubmitAlertPage.
- **ManualAlertForm**: `direction={{ xs: 'column', md: 'row' }}`, `flex: { xs: '1 1 100%', sm: '0 1 auto' }}`.
- **ChatInput**: `p: { xs: 1, sm: 2 }`, `fontSize: { xs: '0.875rem', sm: '1rem' }`.
- **VersionUpdateBanner**: `useMediaQuery('(prefers-reduced-motion: reduce)')` to disable pulse animation.
- **viewport meta**: `<meta name="viewport" content="width=device-width, initial-scale=1.0" />` in `index.html`.
- **MUI layout components**: Container, Stack, Box with responsive props throughout.

The old dashboard also had no mobile-specific layouts, breakpoint-driven navigation, or responsive data tables. Both dashboards are desktop-first SRE tools.

**No action needed.**

---

## Deliverable 5: localStorage Persistence — COMPLETE

### Filter, pagination, sort persistence

`utils/filterPersistence.ts` provides the full set:

| Key | Data | Functions |
|-----|------|-----------|
| `tarsy-filters` | SessionFilter (search, status, alert_type, chain_id, dates, date_preset) | `saveFiltersToStorage`, `loadFiltersFromStorage`, `clearFiltersFromStorage` |
| `tarsy-pagination` | PaginationState (page, pageSize, totalPages, totalItems) | `savePaginationToStorage`, `loadPaginationFromStorage` |
| `tarsy-sort` | SortState (field, direction) | `saveSortToStorage`, `loadSortToStorage` |

Plus: `getDefaultFilters()`, `getDefaultPagination()`, `getDefaultSort()`, `mergeWithDefaults()`, `clearAllDashboardState()`.

All operations are wrapped in `try/catch` (quota exceeded or private browsing → silently ignored).

### DashboardView integration

- Filters, pagination, sort initialized from localStorage via `mergeWithDefaults`.
- State saved on every change via `save*ToStorage` calls.

### Deliberate omission: ConversationTimeline expanded items

The old dashboard persisted manually-expanded chat flow items per session (`session-{id}-expanded-items` with 7-day cleanup). The new dashboard does **not** persist these, and this is intentional:

- The new dashboard uses a redesigned auto-collapse system (synthesis stages auto-collapse when terminal, animated collapse for newly completed items, manual overrides are ephemeral).
- The old dashboard's expanded-items persistence was tied to its chat flow item model, which was completely rewritten.
- The new approach is simpler and the auto-collapse behavior is smarter, making persistence less valuable.

**No action needed.**

---

## Deliverable 6: Production Build — ONE GAP

### Already done

- **Vite build**: `tsc -b && vite build` — TypeScript check then Vite production build.
- **Asset hashing**: Vite's defaults handle content-based hashing for JS/CSS bundles.
- **Tree-shaking + minification**: Vite defaults (esbuild).
- **Code splitting**: Automatic on `lazy()` dynamic imports.
- **Version meta tag**: `<meta name="app-version" content="%VITE_APP_VERSION%" />` in `index.html`.
- **Makefile targets**: `dashboard-build`, `dashboard-test`, `dashboard-lint`, `dashboard-dev`, `dashboard-install`.
- **Go static serving**: `SetDashboardDir()` with `/assets/` serving and SPA fallback.

### Gap: Cache headers on Go static file serving

The old dashboard's `nginx.conf` set proper cache headers:
- **Hashed assets** (`/assets/`): `Cache-Control: public, max-age=31536000, immutable` (1 year, safe because filenames include content hashes)
- **index.html**: `Cache-Control: no-cache` (forces revalidation on every visit, picks up new asset hashes)

The Go server currently serves assets via `echo.StaticFS("/assets", assetsFS)` and index.html via the SPA fallback handler — **neither sets cache headers**.

This matters for production: without `immutable` on hashed assets, browsers re-validate on every load. Without `no-cache` on `index.html`, stale cached HTML could reference old asset hashes after a deployment.

---

## Implementation Tasks

### Task 1: Add cache headers to Go static file serving

**File**: `pkg/api/server.go` — `setupDashboardRoutes()`

**Changes**:

1. **Replace `StaticFS` with a custom handler** for `/assets/*` that wraps the file serving with `Cache-Control: public, max-age=31536000, immutable`. Echo v5's `StaticFS` doesn't support custom headers natively, so we need a middleware or wrapper.

2. **Add `Cache-Control: no-cache` to the SPA fallback handler** for `index.html` responses.

3. **Add `Cache-Control: no-cache` to other root files** (favicon.ico, robots.txt) served by the exact-file-match path — these aren't hashed so should also get `no-cache`.

**Approach** — Add an Echo middleware on the `/assets` group that sets the immutable cache header, and set `no-cache` in the SPA fallback handler:

```go
func (s *Server) setupDashboardRoutes() {
    // ... existing validation ...

    dashFS := os.DirFS(s.dashboardDir)

    // Serve hashed Vite assets with aggressive caching.
    // Filenames include content hashes, so immutable is safe.
    assetsFS, err := fs.Sub(dashFS, "assets")
    if err == nil {
        assetsGroup := s.echo.Group("/assets")
        assetsGroup.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
            return func(c *echo.Context) error {
                c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
                return next(c)
            }
        })
        assetsGroup.StaticFS("/", assetsFS)
    }

    // SPA fallback with no-cache on index.html
    s.echo.GET("/*", func(c *echo.Context) error {
        path := c.Request().URL.Path

        if strings.HasPrefix(path, "/api/") || path == "/health" {
            return echo.NewHTTPError(http.StatusNotFound, "not found")
        }

        // Try exact file (favicon, robots.txt, etc.) — no-cache for unhashed files
        relPath := strings.TrimPrefix(path, "/")
        if relPath != "" {
            if info, statErr := fs.Stat(dashFS, relPath); statErr == nil && !info.IsDir() {
                c.Response().Header().Set("Cache-Control", "no-cache")
                return c.FileFS(relPath, dashFS)
            }
        }

        // SPA fallback — always revalidate
        c.Response().Header().Set("Cache-Control", "no-cache")
        return c.FileFS("index.html", dashFS)
    })
}
```

**Test updates** in `pkg/api/dashboard_test.go`:
- Verify `/assets/*` responses include `Cache-Control: public, max-age=31536000, immutable`.
- Verify SPA fallback responses include `Cache-Control: no-cache`.
- Verify root file responses (favicon, robots.txt) include `Cache-Control: no-cache`.

---

## Update Design Doc

After implementation, update `docs/phase7-dashboard-plan.md`:
- Mark Phase 7.8 as ✅ DONE
- Add brief summary of what was done (cache headers) and what was already complete

---

## Summary

Phase 7.8 is essentially already complete. The only actionable work is adding proper HTTP cache headers to the Go static file serving — a small but important production readiness improvement that matches what the old dashboard had in its nginx configuration.

| Task | Scope | Files | Complexity |
|------|-------|-------|------------|
| Cache headers | Go backend | `pkg/api/server.go`, `pkg/api/dashboard_test.go` | Very Low |
| Update design doc | Docs | `docs/phase7-dashboard-plan.md` | Trivial |

**Estimated effort**: ~30 minutes.
