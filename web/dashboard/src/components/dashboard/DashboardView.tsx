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
} from '@mui/material';
import { Refresh, Menu as MenuIcon, Send as SendIcon } from '@mui/icons-material';
import { FilterPanel } from './FilterPanel.tsx';
import { ActiveAlertsPanel } from './ActiveAlertsPanel.tsx';
import { HistoricalAlertsList } from './HistoricalAlertsList.tsx';
import { useAuth } from '../../contexts/AuthContext.tsx';
import { LoginButton } from '../auth/LoginButton.tsx';
import { UserMenu } from '../auth/UserMenu.tsx';
import { VersionFooter } from '../layout/VersionFooter.tsx';
import {
  getSessions,
  getActiveSessions,
  getFilterOptions,
  handleAPIError,
} from '../../services/api.ts';
import { websocketService } from '../../services/websocket.ts';
import {
  EVENT_SESSION_STATUS,
  EVENT_SESSION_PROGRESS,
} from '../../constants/eventTypes.ts';
import type { SessionFilter, PaginationState, SortState } from '../../types/dashboard.ts';
import type { DashboardSessionItem, ActiveSessionItem, QueuedSessionItem } from '../../types/session.ts';
import type { DashboardListParams } from '../../types/api.ts';
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
} from '../../utils/filterPersistence.ts';
const REFRESH_THROTTLE_MS = 1000;
const FILTER_DEBOUNCE_MS = 300;

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

  return params;
}

export function DashboardView() {
  const { isAuthenticated, authAvailable } = useAuth();

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

  // ── Refs for stable callbacks & stale-update detection ──
  const refreshTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const activeReconnRef = useRef(false);
  const historicalReconnRef = useRef(false);
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

  // Cleanup throttle timer
  useEffect(() => {
    return () => {
      if (refreshTimeoutRef.current) clearTimeout(refreshTimeoutRef.current);
    };
  }, []);

  // ────────────────────────────────────────────────────────────
  // Data fetching
  // ────────────────────────────────────────────────────────────

  const fetchActiveAlerts = useCallback(async () => {
    try {
      setActiveLoading(true);
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
      setHistoricalLoading(true);
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

  const fetchHistoricalWithRetry = useCallback(async () => {
    if (historicalReconnRef.current) return;
    historicalReconnRef.current = true;
    try {
      await fetchHistoricalAlerts();
    } finally {
      historicalReconnRef.current = false;
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

  // ── Throttled refresh ──
  const throttledRefresh = useCallback(() => {
    if (refreshTimeoutRef.current) clearTimeout(refreshTimeoutRef.current);
    refreshTimeoutRef.current = setTimeout(() => {
      fetchActiveAlerts();
      fetchHistoricalAlerts();
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
    fetchActiveAlerts();
    fetchHistoricalAlerts();

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
    const id = setTimeout(() => {
      fetchHistoricalAlerts();
    }, FILTER_DEBOUNCE_MS);
    return () => clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters]);

  // Pagination / sort changes → immediate refetch
  useEffect(() => {
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

      // session.status → throttled full refresh
      if (type === EVENT_SESSION_STATUS) {
        // Clean progress data for sessions that just completed
        const sessionId = data.session_id as string | undefined;
        if (sessionId) {
          setProgressData((prev) => {
            const next = { ...prev };
            delete next[sessionId];
            return next;
          });
        }
        throttledRefreshRef.current();
      }
    };

    const handleConnectionChange = (connected: boolean) => {
      setWsConnected(connected);
      if (connected) {
        fetchActiveRetryRef.current();
        fetchHistoricalRetryRef.current();
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

  const handleFiltersChange = (newFilters: SessionFilter) => {
    setFilters(newFilters);
    saveFiltersToStorage(newFilters);
    // Reset to page 1 when filters change
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

  // ────────────────────────────────────────────────────────────
  // Render
  // ────────────────────────────────────────────────────────────

  return (
    <Container maxWidth={false} sx={{ px: 2 }}>
      {/* AppBar with dashboard title and live indicator — ported from old dashboard */}
      <AppBar
        position="static"
        elevation={0}
        sx={{
          borderRadius: 1,
          background: 'linear-gradient(135deg, #1976d2 0%, #1565c0 100%)',
          boxShadow: '0 4px 16px rgba(25, 118, 210, 0.3)',
          border: '1px solid rgba(255, 255, 255, 0.1)',
        }}
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
            sx={{
              mr: 2,
              background: 'rgba(255, 255, 255, 0.1)',
              backdropFilter: 'blur(10px)',
              border: '1px solid rgba(255, 255, 255, 0.15)',
              borderRadius: 2,
              transition: 'all 0.2s ease',
              '&:hover': {
                background: 'rgba(255, 255, 255, 0.2)',
                transform: 'translateY(-1px)',
                boxShadow: '0 4px 12px rgba(255, 255, 255, 0.2)',
              },
            }}
          >
            <MenuIcon />
          </IconButton>

          <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
            <Box
              component={RouterLink}
              to="/"
              aria-label="Home"
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: 40,
                height: 40,
                borderRadius: 2,
                background: 'rgba(255, 255, 255, 0.1)',
                backdropFilter: 'blur(10px)',
                border: '1px solid rgba(255, 255, 255, 0.2)',
                boxShadow:
                  '0 4px 12px rgba(0, 0, 0, 0.15), 0 0 20px rgba(255, 255, 255, 0.1)',
                transition: 'all 0.3s ease',
                position: 'relative',
                overflow: 'hidden',
                textDecoration: 'none',
                '&:before': {
                  content: '""',
                  position: 'absolute',
                  top: 0,
                  left: '-100%',
                  width: '100%',
                  height: '100%',
                  background:
                    'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.2), transparent)',
                  animation: 'none',
                },
                '&:hover': {
                  background: 'rgba(255, 255, 255, 0.15)',
                  transform: 'translateY(-2px) scale(1.05)',
                  boxShadow:
                    '0 8px 25px rgba(0, 0, 0, 0.2), 0 0 30px rgba(255, 255, 255, 0.2)',
                  '&:before': {
                    animation: 'shimmer 0.6s ease-out',
                  },
                },
                '&:focus-visible': {
                  outline: '2px solid rgba(255, 255, 255, 0.8)',
                  outlineOffset: '2px',
                },
                '@keyframes shimmer': {
                  '0%': { left: '-100%' },
                  '100%': { left: '100%' },
                },
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
              sx={{
                fontWeight: 600,
                letterSpacing: '-0.5px',
                textShadow: '0 1px 2px rgba(0, 0, 0, 0.1)',
                background:
                  'linear-gradient(45deg, #ffffff 0%, rgba(255, 255, 255, 0.9) 100%)',
                WebkitBackgroundClip: 'text',
                WebkitTextFillColor: 'transparent',
                backgroundClip: 'text',
                color: 'white',
              }}
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
                    backgroundColor: wsConnected ? '#81C784' : '#FF7043',
                    boxShadow: `0 0 6px ${wsConnected ? '#4CAF50' : '#F44336'}`,
                    animation: wsConnected ? 'none' : 'pulse 2s infinite',
                    '@keyframes pulse': {
                      '0%': {
                        opacity: 0.7,
                        transform: 'scale(1)',
                        boxShadow: `0 0 6px ${wsConnected ? '#4CAF50' : '#F44336'}`,
                      },
                      '50%': {
                        opacity: 1,
                        transform: 'scale(1.3)',
                        boxShadow: `0 0 12px ${wsConnected ? '#4CAF50' : '#F44336'}`,
                      },
                      '100%': {
                        opacity: 0.7,
                        transform: 'scale(1)',
                        boxShadow: `0 0 6px ${wsConnected ? '#4CAF50' : '#F44336'}`,
                      },
                    },
                  }}
                />
                <Typography
                  variant="caption"
                  sx={{
                    color: 'white',
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
      </Menu>

      {/* Filter Panel */}
      <FilterPanel
        filters={filters}
        onFiltersChange={handleFiltersChange}
        onClearFilters={handleClearFilters}
        filterOptions={filterOptions}
      />

      {/* Main content area */}
      <Box sx={{ mt: 2 }}>
        {/* Active Alerts Panel */}
        <ActiveAlertsPanel
          activeSessions={activeSessions}
          queuedSessions={queuedSessions}
          progressData={progressData}
          loading={activeLoading}
          error={activeError}
          wsConnected={wsConnected}
          onRefresh={handleRefreshActive}
        />

        {/* Historical Alerts Table */}
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
        />
      </Box>

      {/* Version footer */}
      <VersionFooter />
    </Container>
  );
}
