/**
 * DashboardView — main dashboard orchestrator.
 *
 * Owns all dashboard state: active/historical sessions, filters, pagination,
 * sort, filter options, WebSocket connection. Fetches via API client, subscribes
 * to the `sessions` WebSocket channel, persists UI state in localStorage.
 *
 * Ported from old dashboard's DashboardView.tsx. Adapted for new TARSy:
 * - Single `getSessions()` API with query params (not separate filtered/unfiltered)
 * - Active sessions response has separate active[] / queued[] arrays
 * - `session.status` (unified) and `session.progress` events
 * - RFC3339 timestamps, new type names, no agent_type
 */

import { useState, useEffect, useRef, useCallback } from 'react';
import type { MouseEvent } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import {
  Container,
  AppBar,
  Toolbar,
  Typography,
  Box,
  Tooltip,
  CircularProgress,
  IconButton,
  Menu,
  MenuItem,
  ListItemIcon,
  ListItemText,
  ToggleButtonGroup,
  ToggleButton,
} from '@mui/material';
import {
  Refresh,
  Menu as MenuIcon,
  Send as SendIcon,
  Dns as DnsIcon,
  DarkMode as DarkModeIcon,
  LightMode as LightModeIcon,
} from '@mui/icons-material';
import { FilterPanel } from './FilterPanel.tsx';
import { ActiveAlertsPanel } from './ActiveAlertsPanel.tsx';
import { HistoricalAlertsList } from './HistoricalAlertsList.tsx';
import { TriageView } from './TriageView.tsx';
import { CompleteReviewModal } from './CompleteReviewModal.tsx';
import { EditFeedbackModal } from './EditFeedbackModal.tsx';
import { useAuth } from '../../contexts/AuthContext.tsx';
import { useColorScheme } from '@mui/material/styles';
import { appBarSx, glassIconButtonSx, logoBoxSx, titleSx, themeToggleSx, glassToggleGroupSx } from '../../theme/headerStyles';
import { LoginButton } from '../auth/LoginButton.tsx';
import { UserMenu } from '../auth/UserMenu.tsx';
import { VersionFooter } from '../layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../common/FloatingSubmitAlertFab.tsx';
import {
  getSessions,
  getActiveSessions,
  getFilterOptions,
  getTriageGroup,
  updateReview,
  handleAPIError,
} from '../../services/api.ts';
import { websocketService } from '../../services/websocket.ts';
import {
  EVENT_SESSION_STATUS,
  EVENT_SESSION_PROGRESS,
  EVENT_SESSION_SCORE_UPDATED,
  EVENT_REVIEW_STATUS,
} from '../../constants/eventTypes.ts';
import type { SessionFilter, PaginationState, SortState, DashboardTab, TriageFilter } from '../../types/dashboard.ts';
import type { DashboardSessionItem, ActiveSessionItem, QueuedSessionItem } from '../../types/session.ts';
import { REVIEW_ACTION, REVIEW_MODAL_MODE, getReviewModalMode } from '../../types/api.ts';
import type { ReviewModalMode } from '../../types/api.ts';
import type { DashboardListParams, TriageGroup, TriageGroupKey, TriageGroupParams, UpdateReviewResponse } from '../../types/api.ts';
import type { FilterOptionsResponse } from '../../types/system.ts';
import type { SessionProgressPayload } from '../../types/events.ts';
import {
  saveFiltersToStorage,
  loadFiltersFromStorage,
  savePaginationToStorage,
  loadPaginationFromStorage,
  saveSortToStorage,
  loadSortFromStorage,
  getDefaultFilters,
  getDefaultPagination,
  getDefaultSort,
  mergeWithDefaults,
  saveDashboardTab,
  loadDashboardTab,
  saveTriageFilters,
  loadTriageFilters,
  getDefaultTriageFilters,
} from '../../utils/filterPersistence.ts';
const REFRESH_THROTTLE_MS = 1000;
const FILTER_DEBOUNCE_MS = 300;
const TRIAGE_GROUPS: TriageGroupKey[] = ['investigating', 'needs_review', 'in_progress', 'reviewed'];

const EMPTY_TRIAGE: Record<TriageGroupKey, TriageGroup | null> = {
  investigating: null, needs_review: null, in_progress: null, reviewed: null,
};

/**
 * Build query params from the current filter + pagination + sort state.
 */
function buildQueryParams(
  filters: SessionFilter,
  pagination: PaginationState,
  sort: SortState,
): DashboardListParams {
  const params: DashboardListParams = {
    page: pagination.page,
    page_size: pagination.pageSize,
    sort_by: sort.field,
    sort_order: sort.direction,
  };

  if (filters.search.trim().length >= 3) {
    params.search = filters.search.trim();
  }
  if (filters.status.length > 0) {
    params.status = filters.status.join(',');
  }
  if (filters.alert_type) {
    params.alert_type = filters.alert_type;
  }
  if (filters.chain_id) {
    params.chain_id = filters.chain_id;
  }
  if (filters.start_date) {
    params.start_date = filters.start_date;
  }
  if (filters.end_date) {
    params.end_date = filters.end_date;
  }
  if (filters.scoring_status) {
    params.scoring_status = filters.scoring_status;
  }

  return params;
}

export function DashboardView() {
  const { isAuthenticated, authAvailable, user } = useAuth();
  const { mode, setMode } = useColorScheme();
  const toggleColorMode = () => setMode(mode === 'light' ? 'dark' : 'light');

  // ── Navigation menu state ──
  const [menuAnchorEl, setMenuAnchorEl] = useState<null | HTMLElement>(null);

  const handleMenuOpen = (event: MouseEvent<HTMLElement>) => {
    setMenuAnchorEl(event.currentTarget);
  };
  const handleMenuClose = () => {
    setMenuAnchorEl(null);
  };
  const handleManualAlertSubmission = () => {
    window.open('/submit-alert', '_blank', 'noopener,noreferrer');
    handleMenuClose();
  };
  const handleSystemStatus = () => {
    window.open('/system', '_blank', 'noopener,noreferrer');
    handleMenuClose();
  };

  // ── Active sessions state ──
  const [activeSessions, setActiveSessions] = useState<ActiveSessionItem[]>([]);
  const [queuedSessions, setQueuedSessions] = useState<QueuedSessionItem[]>([]);
  const [activeLoading, setActiveLoading] = useState(true);
  const [activeError, setActiveError] = useState<string | null>(null);

  // ── Historical sessions state ──
  const [historicalSessions, setHistoricalSessions] = useState<DashboardSessionItem[]>([]);
  const [historicalLoading, setHistoricalLoading] = useState(true);
  const [historicalError, setHistoricalError] = useState<string | null>(null);

  // ── Progress data from WebSocket ──
  const [progressData, setProgressData] = useState<Record<string, SessionProgressPayload>>({});

  // ── Filter / pagination / sort state (persisted) ──
  const [filters, setFilters] = useState<SessionFilter>(() =>
    mergeWithDefaults(loadFiltersFromStorage(), getDefaultFilters()),
  );
  const [pagination, setPagination] = useState<PaginationState>(() =>
    mergeWithDefaults(loadPaginationFromStorage(), getDefaultPagination()),
  );
  const [sortState, setSortState] = useState<SortState>(() =>
    mergeWithDefaults(loadSortFromStorage(), getDefaultSort()),
  );
  const [filterOptions, setFilterOptions] = useState<FilterOptionsResponse | undefined>();

  // ── WebSocket connection status ──
  const [wsConnected, setWsConnected] = useState(false);

  // ── Tab state (persisted) ──
  const [activeTab, setActiveTab] = useState<DashboardTab>(loadDashboardTab);

  // ── Triage state ──
  const [triageGroups, setTriageGroups] = useState<Record<TriageGroupKey, TriageGroup | null>>({ ...EMPTY_TRIAGE });
  const [triageLoading, setTriageLoading] = useState(false);
  const [triageError, setTriageError] = useState<string | null>(null);
  const [triageFilters, setTriageFilters] = useState<TriageFilter>(() =>
    mergeWithDefaults(loadTriageFilters(), getDefaultTriageFilters()),
  );

  // ── Refs for stable callbacks & stale-update detection ──
  const refreshTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const filterDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const activeReconnRef = useRef(false);
  const historicalReconnRef = useRef(false);
  const mountedRef = useRef(false); // suppress effect-based fetches on first render
  const triageFilterInitRef = useRef(true); // skip first triage-filter effect (initial fetch handles it)
  const filtersRef = useRef(filters);
  const paginationRef = useRef(pagination);
  const sortRef = useRef(sortState);

  useEffect(() => {
    filtersRef.current = filters;
  }, [filters]);
  useEffect(() => {
    paginationRef.current = pagination;
  }, [pagination]);
  useEffect(() => {
    sortRef.current = sortState;
  }, [sortState]);

  // Cleanup timers
  useEffect(() => {
    return () => {
      if (refreshTimeoutRef.current) clearTimeout(refreshTimeoutRef.current);
      if (filterDebounceRef.current) clearTimeout(filterDebounceRef.current);
    };
  }, []);

  // ────────────────────────────────────────────────────────────
  // Data fetching
  // ────────────────────────────────────────────────────────────

  const fetchActiveAlerts = useCallback(async () => {
    try {
      setActiveError(null);
      const data = await getActiveSessions();
      setActiveSessions(data.active);
      setQueuedSessions(data.queued);
    } catch (err) {
      setActiveError(handleAPIError(err));
    } finally {
      setActiveLoading(false);
    }
  }, []);

  const fetchHistoricalAlerts = useCallback(async () => {
    // Capture at request time for stale detection
    const reqFilters = { ...filtersRef.current };
    const reqPage = paginationRef.current.page;
    const reqPageSize = paginationRef.current.pageSize;
    const reqSort = { ...sortRef.current };

    try {
      setHistoricalError(null);

      const params = buildQueryParams(
        reqFilters,
        { ...paginationRef.current },
        reqSort,
      );
      const data = await getSessions(params);

      // Only update state if nothing changed during the request
      const filtersOk = JSON.stringify(filtersRef.current) === JSON.stringify(reqFilters);
      const pageOk =
        paginationRef.current.page === reqPage &&
        paginationRef.current.pageSize === reqPageSize;
      const sortOk =
        sortRef.current.field === reqSort.field &&
        sortRef.current.direction === reqSort.direction;

      if (filtersOk && pageOk && sortOk) {
        setHistoricalSessions(data.sessions);
        setPagination((prev) => ({
          ...prev,
          totalItems: data.pagination.total_items,
          totalPages: data.pagination.total_pages,
          page: data.pagination.page,
        }));
      }
    } catch (err) {
      setHistoricalError(handleAPIError(err));
    } finally {
      setHistoricalLoading(false);
    }
  }, []);

  // ── Reconnect-aware fetching (with retry via API client) ──

  const fetchActiveWithRetry = useCallback(async () => {
    if (activeReconnRef.current) return;
    activeReconnRef.current = true;
    try {
      await fetchActiveAlerts();
    } finally {
      activeReconnRef.current = false;
    }
  }, [fetchActiveAlerts]);

  const historicalPendingRef = useRef(false);
  const fetchHistoricalWithRetry = useCallback(async () => {
    if (historicalReconnRef.current) {
      historicalPendingRef.current = true;
      return;
    }
    historicalReconnRef.current = true;
    try {
      await fetchHistoricalAlerts();
      if (historicalPendingRef.current) {
        historicalPendingRef.current = false;
        await fetchHistoricalAlerts();
      }
    } finally {
      historicalReconnRef.current = false;
      historicalPendingRef.current = false;
    }
  }, [fetchHistoricalAlerts]);

  // Stable refs for WS handler callbacks
  const fetchActiveRetryRef = useRef(fetchActiveWithRetry);
  const fetchHistoricalRetryRef = useRef(fetchHistoricalWithRetry);
  useEffect(() => {
    fetchActiveRetryRef.current = fetchActiveWithRetry;
  }, [fetchActiveWithRetry]);
  useEffect(() => {
    fetchHistoricalRetryRef.current = fetchHistoricalWithRetry;
  }, [fetchHistoricalWithRetry]);

  // ── Triage data fetching ──
  const activeTabRef = useRef(activeTab);
  useEffect(() => {
    activeTabRef.current = activeTab;
  }, [activeTab]);

  const triageFiltersRef = useRef(triageFilters);
  useEffect(() => {
    triageFiltersRef.current = triageFilters;
  }, [triageFilters]);

  const userEmailRef = useRef(user?.email);
  useEffect(() => {
    userEmailRef.current = user?.email;
  }, [user?.email]);

  const triageGroupsRef = useRef(triageGroups);
  useEffect(() => {
    triageGroupsRef.current = triageGroups;
  }, [triageGroups]);

  const triageRequestIdRef = useRef(0);

  const buildTriageParams = useCallback((groupKey?: TriageGroupKey): TriageGroupParams => {
    const assignee =
      triageFiltersRef.current.assignee === 'mine' ? (userEmailRef.current || undefined) :
      triageFiltersRef.current.assignee === 'unassigned' ? '' :
      undefined;
    const params: TriageGroupParams = {};
    if (assignee !== undefined) params.assignee = assignee;
    if (groupKey) {
      const current = triageGroupsRef.current[groupKey];
      if (current) params.page_size = current.page_size;
    }
    return params;
  }, []);

  const fetchAllTriageGroups = useCallback(async () => {
    const requestId = ++triageRequestIdRef.current;
    try {
      setTriageLoading(true);
      setTriageError(null);
      const results = await Promise.all(
        TRIAGE_GROUPS.map(g => getTriageGroup(g, buildTriageParams(g))),
      );
      if (requestId !== triageRequestIdRef.current) return;
      setTriageGroups({
        investigating: results[0],
        needs_review: results[1],
        in_progress: results[2],
        reviewed: results[3],
      });
    } catch (err) {
      if (requestId !== triageRequestIdRef.current) return;
      setTriageError(handleAPIError(err));
    } finally {
      if (requestId === triageRequestIdRef.current) {
        setTriageLoading(false);
      }
    }
  }, [buildTriageParams]);

  const fetchSingleTriageGroup = useCallback(async (groupKey: TriageGroupKey, extraParams?: Partial<TriageGroupParams>) => {
    try {
      const params = { ...buildTriageParams(groupKey), ...extraParams };
      const data = await getTriageGroup(groupKey, params);
      setTriageGroups(prev => ({ ...prev, [groupKey]: data }));
    } catch (err) {
      setTriageError(handleAPIError(err));
    }
  }, [buildTriageParams]);

  const fetchAllTriageGroupsRef = useRef(fetchAllTriageGroups);
  useEffect(() => {
    fetchAllTriageGroupsRef.current = fetchAllTriageGroups;
  }, [fetchAllTriageGroups]);

  // ── Throttled refresh (sessions + triage together) ──
  const throttledRefresh = useCallback(() => {
    if (refreshTimeoutRef.current) clearTimeout(refreshTimeoutRef.current);
    refreshTimeoutRef.current = setTimeout(() => {
      fetchActiveAlerts();
      fetchHistoricalAlerts();
      if (activeTabRef.current === 'triage') {
        fetchAllTriageGroupsRef.current();
      }
      refreshTimeoutRef.current = null;
    }, REFRESH_THROTTLE_MS);
  }, [fetchActiveAlerts, fetchHistoricalAlerts]);

  const throttledRefreshRef = useRef(throttledRefresh);
  useEffect(() => {
    throttledRefreshRef.current = throttledRefresh;
  }, [throttledRefresh]);

  // ────────────────────────────────────────────────────────────
  // Initial load + filter options
  // ────────────────────────────────────────────────────────────

  useEffect(() => {
    mountedRef.current = true;
    fetchActiveAlerts();
    fetchHistoricalAlerts();
    if (activeTabRef.current === 'triage') {
      fetchAllTriageGroups();
    }

    (async () => {
      try {
        const options = await getFilterOptions();
        setFilterOptions(options);
      } catch {
        // Continue without filter options — dropdowns will be empty
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ────────────────────────────────────────────────────────────
  // Filter changes → debounced refetch
  // ────────────────────────────────────────────────────────────

  useEffect(() => {
    // Skip on mount — the initial effect above already fetched
    if (!mountedRef.current) return;
    if (filterDebounceRef.current) clearTimeout(filterDebounceRef.current);
    filterDebounceRef.current = setTimeout(() => {
      fetchHistoricalAlerts();
      filterDebounceRef.current = null;
    }, FILTER_DEBOUNCE_MS);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters]);

  // Pagination / sort changes → immediate refetch
  useEffect(() => {
    // Skip on mount — the initial effect above already fetched
    if (!mountedRef.current) return;
    // Skip if this was triggered by handleFiltersChange resetting page to 1
    // (the debounced filter effect will handle the fetch instead)
    if (filterResetPageRef.current) {
      filterResetPageRef.current = false;
      return;
    }
    fetchHistoricalAlerts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pagination.page, pagination.pageSize, sortState.field, sortState.direction]);

  // ────────────────────────────────────────────────────────────
  // WebSocket subscription
  // ────────────────────────────────────────────────────────────

  useEffect(() => {
    const handleSessionEvent = (data: Record<string, unknown>) => {
      const type = data.type as string | undefined;

      // session.progress → update local progress map
      if (type === EVENT_SESSION_PROGRESS) {
        const payload = data as unknown as SessionProgressPayload;
        setProgressData((prev) => ({ ...prev, [payload.session_id]: payload }));
        return;
      }

      // session.status → throttled refresh (sessions + triage together)
      if (type === EVENT_SESSION_STATUS) {
        const sessionId = data.session_id as string | undefined;
        if (sessionId) {
          setProgressData((prev) => {
            const next = { ...prev };
            delete next[sessionId];
            return next;
          });
        }
        throttledRefreshRef.current();
        return;
      }

      // session.score_updated → throttled refresh to pick up spinner / final score
      if (type === EVENT_SESSION_SCORE_UPDATED) {
        throttledRefreshRef.current();
        return;
      }

      // review.status → refresh both tabs so cross-tab data stays in sync
      if (type === EVENT_REVIEW_STATUS) {
        fetchHistoricalRetryRef.current();
        fetchAllTriageGroupsRef.current();
        return;
      }
    };

    const handleConnectionChange = (connected: boolean) => {
      setWsConnected(connected);
      if (connected) {
        fetchActiveRetryRef.current();
        fetchHistoricalRetryRef.current();
        fetchAllTriageGroupsRef.current();
      }
    };

    const unsubChannel = websocketService.subscribeToChannel('sessions', handleSessionEvent);
    const unsubConn = websocketService.onConnectionChange(handleConnectionChange);

    websocketService.connect();
    setWsConnected(websocketService.isConnected);

    return () => {
      unsubChannel();
      unsubConn();
    };
  }, []);

  // ────────────────────────────────────────────────────────────
  // Handler callbacks for child components
  // ────────────────────────────────────────────────────────────

  const filterResetPageRef = useRef(false); // flag to suppress pagination effect during filter-driven page reset

  const handleFiltersChange = (newFilters: SessionFilter) => {
    setFilters(newFilters);
    saveFiltersToStorage(newFilters);
    // Reset to page 1 when filters change. Flag so the pagination/sort effect
    // ignores this state change (the debounced filter effect handles the fetch).
    filterResetPageRef.current = true;
    setPagination((prev) => ({ ...prev, page: 1 }));
    savePaginationToStorage({ page: 1 });
  };

  const handleClearFilters = () => {
    const defaults = getDefaultFilters();
    setFilters(defaults);
    saveFiltersToStorage(defaults);
    const defaultPagination = getDefaultPagination();
    setPagination(defaultPagination);
    savePaginationToStorage(defaultPagination);
  };

  const handlePageChange = (newPage: number) => {
    setPagination((prev) => ({ ...prev, page: newPage }));
    savePaginationToStorage({ page: newPage });
  };

  const handlePageSizeChange = (newPageSize: number) => {
    const firstItem = (pagination.page - 1) * pagination.pageSize + 1;
    const newPage = Math.max(1, Math.ceil(firstItem / newPageSize));
    setPagination((prev) => ({ ...prev, pageSize: newPageSize, page: newPage }));
    savePaginationToStorage({ pageSize: newPageSize, page: newPage });
  };

  const handleSortChange = (field: string) => {
    const direction =
      sortState.field === field && sortState.direction === 'asc' ? 'desc' : 'asc';
    const newSort: SortState = { field, direction };
    setSortState(newSort);
    saveSortToStorage(newSort);
  };

  const handleRefreshActive = () => fetchActiveAlerts();
  const handleRefreshHistorical = () => fetchHistoricalAlerts();

  const handleWebSocketRetry = () => {
    websocketService.connect();
  };

  // ── Tab switching ──

  const handleTabChange = (_: React.MouseEvent<HTMLElement>, newValue: DashboardTab | null) => {
    if (!newValue) return;
    setActiveTab(newValue);
    saveDashboardTab(newValue);
    if (newValue === 'triage') {
      fetchAllTriageGroups();
    }
  };

  // ── Triage filter / action callbacks ──

  const handleTriageFiltersChange = (newFilters: TriageFilter) => {
    setTriageFilters(newFilters);
    saveTriageFilters(newFilters);
  };

  // Refetch when triage filters change (skip first run — initial fetch handles it)
  useEffect(() => {
    if (triageFilterInitRef.current) {
      triageFilterInitRef.current = false;
      return;
    }
    if (activeTabRef.current === 'triage') {
      fetchAllTriageGroups();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [triageFilters]);

  const checkReviewResults = (resp: UpdateReviewResponse) => {
    const failures = resp.results.filter((r) => !r.success);
    if (failures.length > 0) {
      const msg = failures.length === 1
        ? `Failed for session ${failures[0].session_id}: ${failures[0].error}`
        : `Failed for ${failures.length} sessions: ${failures.map((f) => f.error).join('; ')}`;
      throw new Error(msg);
    }
  };

  const handleBulkTriageClaim = async (sessionIds: string[]) => {
    try {
      const resp = await updateReview({ session_ids: sessionIds, action: REVIEW_ACTION.CLAIM });
      checkReviewResults(resp);
      fetchAllTriageGroups();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleBulkTriageUnclaim = async (sessionIds: string[]) => {
    try {
      const resp = await updateReview({ session_ids: sessionIds, action: REVIEW_ACTION.UNCLAIM });
      checkReviewResults(resp);
      fetchAllTriageGroups();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleBulkTriageComplete = async (sessionIds: string[], qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    try {
      const resp = await updateReview({
        session_ids: sessionIds,
        action: REVIEW_ACTION.COMPLETE,
        quality_rating: qualityRating,
        action_taken: actionTaken,
        investigation_feedback: investigationFeedback,
      });
      checkReviewResults(resp);
      fetchAllTriageGroups();
      fetchHistoricalAlerts();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleBulkTriageReopen = async (sessionIds: string[]) => {
    try {
      const resp = await updateReview({ session_ids: sessionIds, action: REVIEW_ACTION.REOPEN });
      checkReviewResults(resp);
      fetchAllTriageGroups();
      fetchHistoricalAlerts();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleTriageClaim = (sessionId: string) => handleBulkTriageClaim([sessionId]);
  const handleTriageUnclaim = (sessionId: string) => handleBulkTriageUnclaim([sessionId]);
  const handleTriageReopen = (sessionId: string) => handleBulkTriageReopen([sessionId]);

  const handleTriageComplete = async (sessionId: string, qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    try {
      const resp = await updateReview({
        session_ids: [sessionId],
        action: REVIEW_ACTION.COMPLETE,
        quality_rating: qualityRating,
        action_taken: actionTaken,
        investigation_feedback: investigationFeedback,
      });
      checkReviewResults(resp);
      fetchAllTriageGroups();
      fetchHistoricalAlerts();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleTriageUpdateFeedback = async (sessionId: string, qualityRating: string, actionTaken: string, investigationFeedback: string) => {
    try {
      const resp = await updateReview({
        session_ids: [sessionId],
        action: REVIEW_ACTION.UPDATE_FEEDBACK,
        quality_rating: qualityRating || undefined,
        action_taken: actionTaken || undefined,
        investigation_feedback: investigationFeedback || undefined,
      });
      checkReviewResults(resp);
      fetchAllTriageGroups();
      fetchHistoricalAlerts();
    } catch (err) {
      setTriageError(err instanceof Error ? err.message : handleAPIError(err));
      throw err;
    }
  };

  const handleTriagePageChange = useCallback((group: TriageGroupKey, page: number) => {
    fetchSingleTriageGroup(group, { page });
  }, [fetchSingleTriageGroup]);

  const handleTriagePageSizeChange = useCallback((group: TriageGroupKey, pageSize: number) => {
    fetchSingleTriageGroup(group, { page: 1, page_size: pageSize });
  }, [fetchSingleTriageGroup]);

  // ── Session-level review (from historical list) ──

  const [reviewTarget, setReviewTarget] = useState<{
    session: DashboardSessionItem;
    mode: ReviewModalMode;
  } | null>(null);
  const [reviewLoading, setReviewLoading] = useState(false);
  const [reviewError, setReviewError] = useState<string | null>(null);

  const handleSessionReviewClick = useCallback((session: DashboardSessionItem) => {
    const mode = getReviewModalMode(session.review_status);
    setReviewTarget({ session, mode });
    setReviewError(null);
  }, []);

  const handleSessionReviewComplete = useCallback(async (qualityRating: string, actionTaken?: string, investigationFeedback?: string) => {
    if (!reviewTarget) return;
    const targetSessionId = reviewTarget.session.id;
    try {
      setReviewLoading(true);
      setReviewError(null);
      const resp = await updateReview({
        session_ids: [targetSessionId],
        action: REVIEW_ACTION.COMPLETE,
        quality_rating: qualityRating,
        action_taken: actionTaken,
        investigation_feedback: investigationFeedback,
      });
      checkReviewResults(resp);
      setReviewTarget(null);
      fetchHistoricalAlerts();
      fetchAllTriageGroups();
    } catch (err) {
      const msg = err instanceof Error ? err.message : handleAPIError(err);
      setReviewError(msg);
    } finally {
      setReviewLoading(false);
    }
  }, [reviewTarget, fetchHistoricalAlerts, fetchAllTriageGroups]);

  const handleSessionReviewSave = useCallback(async (qualityRating: string, actionTaken: string, investigationFeedback: string) => {
    if (!reviewTarget) return;
    const targetSessionId = reviewTarget.session.id;
    try {
      setReviewLoading(true);
      setReviewError(null);
      const resp = await updateReview({
        session_ids: [targetSessionId],
        action: REVIEW_ACTION.UPDATE_FEEDBACK,
        quality_rating: qualityRating || undefined,
        action_taken: actionTaken || undefined,
        investigation_feedback: investigationFeedback || undefined,
      });
      checkReviewResults(resp);
      setReviewTarget(null);
      fetchHistoricalAlerts();
      fetchAllTriageGroups();
    } catch (err) {
      const msg = err instanceof Error ? err.message : handleAPIError(err);
      setReviewError(msg);
    } finally {
      setReviewLoading(false);
    }
  }, [reviewTarget, fetchHistoricalAlerts, fetchAllTriageGroups]);

  // ────────────────────────────────────────────────────────────
  // Render
  // ────────────────────────────────────────────────────────────

  return (
    <Container maxWidth={false} sx={{ px: 2 }}>
      {/* AppBar with dashboard title and live indicator — ported from old dashboard */}
      <AppBar
        position="static"
        elevation={0}
        sx={(theme) => ({ ...appBarSx(theme) })}
      >
        <Toolbar>
          {/* Navigation Menu */}
          <IconButton
            id="navigation-menu-button"
            size="large"
            edge="start"
            color="inherit"
            aria-label="menu"
            onClick={handleMenuOpen}
            sx={{ mr: 2, ...glassIconButtonSx }}
          >
            <MenuIcon />
          </IconButton>

          <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
            <Box
              component={RouterLink}
              to="/"
              aria-label="Home"
              sx={{
                ...logoBoxSx,
              }}
            >
              <img
                src="/tarsy-logo.png"
                alt="TARSy logo"
                style={{
                  height: '28px',
                  width: 'auto',
                  borderRadius: '3px',
                  filter: 'drop-shadow(0 2px 4px rgba(0, 0, 0, 0.1))',
                }}
              />
            </Box>
            <Typography
              variant="h5"
              component="div"
              sx={titleSx}
            >
              TARSy
            </Typography>
          </Box>

          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 2,
              flexGrow: 1,
              justifyContent: 'flex-end',
            }}
          >
            {/* WebSocket Retry Button - only show when disconnected */}
            {!wsConnected && (
              <Tooltip title="Retry WebSocket connection">
                <IconButton
                  size="small"
                  onClick={handleWebSocketRetry}
                  sx={{
                    color: 'inherit',
                    '&:hover': {
                      backgroundColor: 'rgba(255, 255, 255, 0.1)',
                    },
                  }}
                >
                  <Refresh fontSize="small" />
                </IconButton>
              </Tooltip>
            )}

            {/* Loading indicator */}
            {(activeLoading || historicalLoading) && (
              <Tooltip title="Loading data...">
                <CircularProgress size={18} sx={{ color: 'inherit' }} />
              </Tooltip>
            )}

            {/* Sessions / Triage toggle */}
            <ToggleButtonGroup
              value={activeTab}
              exclusive
              onChange={handleTabChange}
              size="small"
              aria-label="Dashboard tabs"
              sx={{ mr: 2, ...glassToggleGroupSx }}
            >
              <ToggleButton value="sessions">Sessions</ToggleButton>
              <ToggleButton value="triage">Triage</ToggleButton>
            </ToggleButtonGroup>

            {/* Dark / Light mode toggle */}
            <Tooltip title={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}>
              <IconButton
                size="small"
                onClick={toggleColorMode}
                aria-label={mode === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
                sx={themeToggleSx}
              >
                {mode === 'dark' ? <LightModeIcon fontSize="small" /> : <DarkModeIcon fontSize="small" />}
              </IconButton>
            </Tooltip>

            {/* Connection Status Indicator - Fancy LIVE / Offline badge */}
            <Tooltip
              title={
                wsConnected
                  ? 'Connected - Real-time updates active'
                  : 'Disconnected - Use manual refresh buttons or retry connection'
              }
            >
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.5,
                  py: 0.6,
                  borderRadius: 3,
                  background: wsConnected
                    ? 'linear-gradient(135deg, rgba(76, 175, 80, 0.2), rgba(139, 195, 74, 0.2))'
                    : 'linear-gradient(135deg, rgba(244, 67, 54, 0.2), rgba(255, 87, 51, 0.2))',
                  border: `2px solid ${wsConnected ? 'rgba(76, 175, 80, 0.6)' : 'rgba(244, 67, 54, 0.6)'}`,
                  minWidth: 'fit-content',
                  boxShadow: wsConnected
                    ? '0 4px 20px rgba(76, 175, 80, 0.4), 0 0 15px rgba(76, 175, 80, 0.2)'
                    : '0 4px 20px rgba(244, 67, 54, 0.4), 0 0 15px rgba(244, 67, 54, 0.2)',
                  backdropFilter: 'blur(10px)',
                  transition: 'all 0.3s ease',
                  position: 'relative',
                  '&:hover': {
                    transform: 'translateY(-1px)',
                    boxShadow: wsConnected
                      ? '0 6px 25px rgba(76, 175, 80, 0.5), 0 0 20px rgba(76, 175, 80, 0.3)'
                      : '0 6px 25px rgba(244, 67, 54, 0.5), 0 0 20px rgba(244, 67, 54, 0.3)',
                  },
                }}
              >
                <Box
                  sx={{
                    width: 7,
                    height: 7,
                    borderRadius: '50%',
                    backgroundColor: wsConnected ? 'success.light' : 'error.light',
                    boxShadow: (theme) => `0 0 6px ${wsConnected ? theme.palette.success.main : theme.palette.error.main}`,
                    animation: wsConnected ? 'none' : 'pulse 2s infinite',
                    '@keyframes pulse': {
                      '0%': {
                        opacity: 0.7,
                        transform: 'scale(1)',
                      },
                      '50%': {
                        opacity: 1,
                        transform: 'scale(1.3)',
                      },
                      '100%': {
                        opacity: 0.7,
                        transform: 'scale(1)',
                      },
                    },
                  }}
                />
                <Typography
                  variant="caption"
                  sx={{
                    color: 'common.white',
                    fontWeight: 600,
                    fontSize: '0.7rem',
                    letterSpacing: '0.8px',
                    textTransform: 'uppercase',
                    textShadow: '0 1px 2px rgba(0, 0, 0, 0.3)',
                  }}
                >
                  {wsConnected ? 'Live' : 'Offline'}
                </Typography>
              </Box>
            </Tooltip>

            {/* Authentication Elements */}
            {authAvailable && !isAuthenticated && <LoginButton size="medium" />}
            {authAvailable && isAuthenticated && <UserMenu />}
          </Box>
        </Toolbar>
      </AppBar>

      {/* Navigation Menu */}
      <Menu
        id="navigation-menu"
        anchorEl={menuAnchorEl}
        open={Boolean(menuAnchorEl)}
        onClose={handleMenuClose}
        MenuListProps={{
          'aria-labelledby': 'navigation-menu-button',
        }}
      >
        <MenuItem onClick={handleManualAlertSubmission}>
          <ListItemIcon>
            <SendIcon fontSize="small" />
          </ListItemIcon>
          <ListItemText>Manual Alert Submission</ListItemText>
        </MenuItem>
        <MenuItem onClick={handleSystemStatus}>
          <ListItemIcon>
            <DnsIcon fontSize="small" />
          </ListItemIcon>
          <ListItemText>System Status</ListItemText>
        </MenuItem>
      </Menu>

      {/* Sessions tab content */}
      {activeTab === 'sessions' && (
        <>
          <FilterPanel
            filters={filters}
            onFiltersChange={handleFiltersChange}
            onClearFilters={handleClearFilters}
            filterOptions={filterOptions}
          />

          <Box sx={{ mt: 2 }}>
            <ActiveAlertsPanel
              activeSessions={activeSessions}
              queuedSessions={queuedSessions}
              progressData={progressData}
              loading={activeLoading}
              error={activeError}
              wsConnected={wsConnected}
              onRefresh={handleRefreshActive}
            />

            <HistoricalAlertsList
              sessions={historicalSessions}
              loading={historicalLoading}
              error={historicalError}
              filters={filters}
              filteredCount={pagination.totalItems}
              sortState={sortState}
              pagination={pagination}
              onRefresh={handleRefreshHistorical}
              onSortChange={handleSortChange}
              onPageChange={handlePageChange}
              onPageSizeChange={handlePageSizeChange}
              onReviewClick={handleSessionReviewClick}
            />
          </Box>

          {/* Review modals for session-level review */}
          <CompleteReviewModal
            open={reviewTarget?.mode === REVIEW_MODAL_MODE.COMPLETE}
            onClose={() => { setReviewTarget(null); setReviewError(null); }}
            onComplete={handleSessionReviewComplete}
            loading={reviewLoading}
            error={reviewError}
            title={reviewTarget?.session.alert_type ? `Review: ${reviewTarget.session.alert_type}` : undefined}
            executiveSummary={reviewTarget?.session.executive_summary}
            assignee={reviewTarget?.session.assignee}
            feedbackEdited={reviewTarget?.session.feedback_edited}
          />
          <EditFeedbackModal
            open={reviewTarget?.mode === REVIEW_MODAL_MODE.EDIT}
            onClose={() => { setReviewTarget(null); setReviewError(null); }}
            onSave={handleSessionReviewSave}
            loading={reviewLoading}
            error={reviewError}
            initialQualityRating={reviewTarget?.session.quality_rating ?? ''}
            initialActionTaken={reviewTarget?.session.action_taken ?? ''}
            initialInvestigationFeedback={reviewTarget?.session.investigation_feedback ?? ''}
            executiveSummary={reviewTarget?.session.executive_summary}
            assignee={reviewTarget?.session.assignee}
            feedbackEdited={reviewTarget?.session.feedback_edited}
          />
        </>
      )}

      {/* Triage tab content */}
      {activeTab === 'triage' && (
        <TriageView
          groups={triageGroups}
          loading={triageLoading}
          error={triageError}
          filters={triageFilters}
          onFiltersChange={handleTriageFiltersChange}
          onRefresh={fetchAllTriageGroups}
          onClaim={handleTriageClaim}
          onUnclaim={handleTriageUnclaim}
          onComplete={handleTriageComplete}
          onReopen={handleTriageReopen}
          onUpdateFeedback={handleTriageUpdateFeedback}
          onBulkClaim={handleBulkTriageClaim}
          onBulkComplete={handleBulkTriageComplete}
          onBulkUnclaim={handleBulkTriageUnclaim}
          onBulkReopen={handleBulkTriageReopen}
          onPageChange={handleTriagePageChange}
          onPageSizeChange={handleTriagePageSizeChange}
        />
      )}

      {/* Version footer */}
      <VersionFooter />

      {/* Floating Action Button for quick alert submission access */}
      <FloatingSubmitAlertFab />
    </Container>
  );
}
