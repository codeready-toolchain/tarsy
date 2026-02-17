/**
 * TracePage — full page orchestrator for the Trace View.
 *
 * Orchestrates:
 * - REST data loading (session detail + trace data)
 * - WebSocket subscriptions for live updates (stage.status, interaction.created, session.status)
 * - Re-fetch trace debounced on event-notification
 * - View toggle (reasoning ↔ trace navigation)
 * - Loading skeletons, error states, empty states
 *
 * Much simpler than SessionDetailPage — no streaming, no auto-scroll, no chat.
 */

import { useState, useEffect, useRef, useCallback, lazy, Suspense } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  Container,
  Box,
  Alert,
  Typography,
  Skeleton,
  Paper,
  CircularProgress,
  Button,
  ToggleButton,
  ToggleButtonGroup,
} from '@mui/material';
import {
  Psychology,
  AccountTree,
} from '@mui/icons-material';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';

import { getSession, getTrace, handleAPIError } from '../services/api.ts';
import { websocketService } from '../services/websocket.ts';

import type { SessionDetailResponse } from '../types/session.ts';
import type { TraceListResponse } from '../types/trace.ts';
import type {
  SessionStatusPayload,
  StageStatusPayload,
  InteractionCreatedPayload,
} from '../types/events.ts';

import {
  EVENT_SESSION_STATUS,
  EVENT_STAGE_STATUS,
  EVENT_INTERACTION_CREATED,
  EVENT_CATCHUP_OVERFLOW,
} from '../constants/eventTypes.ts';

import {
  ACTIVE_STATUSES,
  isTerminalStatus,
  SESSION_STATUS,
  type SessionStatus,
} from '../constants/sessionStatus.ts';

// ────────────────────────────────────────────────────────────
// Lazy-loaded sub-components
// ────────────────────────────────────────────────────────────

const SessionHeader = lazy(() => import('../components/session/SessionHeader.tsx'));
const TraceTimeline = lazy(() => import('../components/trace/TraceTimeline.tsx'));

// ────────────────────────────────────────────────────────────
// Skeleton placeholders
// ────────────────────────────────────────────────────────────

function HeaderSkeleton() {
  return (
    <Paper sx={{ p: 3 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
        <Skeleton variant="circular" width={40} height={40} />
        <Box sx={{ flex: 1 }}>
          <Skeleton variant="text" width="60%" height={32} />
          <Skeleton variant="text" width="40%" height={20} />
        </Box>
        <Skeleton variant="text" width={100} height={24} />
      </Box>
    </Paper>
  );
}

function TraceTimelineSkeleton() {
  return (
    <Paper sx={{ p: 3, mb: 2 }}>
      <Skeleton variant="text" width="25%" height={28} sx={{ mb: 2 }} />
      <Skeleton variant="rectangular" height={6} sx={{ borderRadius: 3, mb: 3 }} />
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {[1, 2, 3].map((i) => (
          <Box key={i}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mb: 1 }}>
              <Skeleton variant="circular" width={32} height={32} />
              <Skeleton variant="text" width="50%" height={24} />
              <Skeleton variant="rounded" width={80} height={22} />
            </Box>
            <Skeleton variant="rectangular" height={60} sx={{ borderRadius: 1, ml: 5 }} />
          </Box>
        ))}
      </Box>
    </Paper>
  );
}

// ────────────────────────────────────────────────────────────
// Page component
// ────────────────────────────────────────────────────────────

export function TracePage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  // --- Core state ---
  const [session, setSession] = useState<SessionDetailResponse | null>(null);
  const [traceData, setTraceData] = useState<TraceListResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // --- View ---
  const view = 'trace' as const;

  // --- Debounce timer for re-fetching trace on WS events ---
  const refetchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // --- Derived ---
  const isActive = session
    ? ACTIVE_STATUSES.has(session.status as SessionStatus) ||
      session.status === SESSION_STATUS.PENDING
    : false;

  // Header title
  const headerTitle = session
    ? `Trace View - ${id?.slice(-8) ?? ''}`
    : 'Trace View';

  // ────────────────────────────────────────────────────────────
  // REST data loading
  // ────────────────────────────────────────────────────────────

  const loadData = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);

    try {
      const [sessionData, traceResult] = await Promise.all([
        getSession(id),
        getTrace(id),
      ]);
      setSession(sessionData);
      setTraceData(traceResult);
    } catch (err) {
      setError(handleAPIError(err));
    } finally {
      setLoading(false);
    }
  }, [id]);

  // Initial load
  useEffect(() => {
    loadData();
  }, [loadData]);

  // ────────────────────────────────────────────────────────────
  // Debounced trace re-fetch (event-notification + REST pattern)
  // ────────────────────────────────────────────────────────────

  const refetchTraceDebounced = useCallback(() => {
    if (!id) return;
    if (refetchTimerRef.current) {
      clearTimeout(refetchTimerRef.current);
    }
    refetchTimerRef.current = setTimeout(() => {
      refetchTimerRef.current = null;
      getTrace(id)
        .then((freshTrace) => {
          setTraceData(freshTrace);
        })
        .catch((err) => {
          console.warn('Failed to re-fetch trace:', err);
        });
    }, 300);
  }, [id]);

  // Cleanup timer on unmount
  useEffect(() => {
    return () => {
      if (refetchTimerRef.current) {
        clearTimeout(refetchTimerRef.current);
      }
    };
  }, []);

  // ────────────────────────────────────────────────────────────
  // WebSocket subscription
  // ────────────────────────────────────────────────────────────

  useEffect(() => {
    if (!id) return;

    websocketService.connect();

    const handler = (data: Record<string, unknown>) => {
      try {
        const eventType = data.type as string | undefined;
        if (!eventType) return;

        // --- catchup.overflow → full reload ---
        if (eventType === EVENT_CATCHUP_OVERFLOW) {
          loadData();
          return;
        }

        // --- stage.status → re-fetch trace ---
        if (eventType === EVENT_STAGE_STATUS) {
          const payload = data as unknown as StageStatusPayload;
          // Update session stages in-place for immediate UI feedback
          setSession((prev) => {
            if (!prev) return prev;
            const stages = prev.stages ?? [];
            const existing = stages.find((s) => s.id === payload.stage_id);
            if (existing) {
              const updatedStages = stages.map((stage) =>
                stage.id === payload.stage_id
                  ? { ...stage, status: payload.status }
                  : stage,
              );
              return { ...prev, stages: updatedStages };
            }
            return prev;
          });
          refetchTraceDebounced();
          return;
        }

        // --- interaction.created → re-fetch trace ---
        if (eventType === EVENT_INTERACTION_CREATED) {
          // Just re-fetch — the trace endpoint returns the full hierarchy
          void (data as unknown as InteractionCreatedPayload);
          refetchTraceDebounced();
          return;
        }

        // --- session.status ---
        if (eventType === EVENT_SESSION_STATUS) {
          const payload = data as unknown as SessionStatusPayload;
          setSession((prev) => {
            if (!prev) return prev;
            return { ...prev, status: payload.status };
          });

          // Terminal status → re-fetch both for final authoritative data
          if (isTerminalStatus(payload.status as SessionStatus)) {
            Promise.all([getSession(id), getTrace(id)])
              .then(([freshSession, freshTrace]) => {
                setSession(freshSession);
                setTraceData(freshTrace);
              })
              .catch((err) => {
                console.warn('Failed to re-fetch after terminal status:', err);
              });
          }
          return;
        }
      } catch {
        // Ignore malformed WS payloads
      }
    };

    const unsubscribe = websocketService.subscribeToChannel(`session:${id}`, handler);

    return () => {
      unsubscribe();
    };
  }, [id, loadData, refetchTraceDebounced]);

  // ────────────────────────────────────────────────────────────
  // View toggle
  // ────────────────────────────────────────────────────────────

  const handleViewChange = useCallback(
    (newView: 'reasoning' | 'trace') => {
      if (newView === 'reasoning' && id) {
        navigate(`/sessions/${id}`);
      }
      // 'trace' is the current page — no-op
    },
    [id, navigate],
  );

  // ────────────────────────────────────────────────────────────
  // Retry
  // ────────────────────────────────────────────────────────────

  const handleRetry = useCallback(() => {
    loadData();
  }, [loadData]);

  // ────────────────────────────────────────────────────────────
  // Render
  // ────────────────────────────────────────────────────────────

  return (
    <>
      <Container maxWidth={false} sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <SharedHeader title={headerTitle} showBackButton>
          {/* Session info */}
          {session && !loading && (
            <Typography variant="body2" sx={{ mr: 2, opacity: 0.8, color: 'white' }}>
              {session.stages?.length || 0} stages &bull;{' '}
              {(session.llm_interaction_count ?? 0) + (session.mcp_interaction_count ?? 0)} interactions
            </Typography>
          )}

          {/* Reasoning / Trace view toggle */}
          {session && !loading && (
            <ToggleButtonGroup
              value={view}
              exclusive
              onChange={(_, newView) => newView && handleViewChange(newView)}
              size="small"
              sx={{
                mr: 2,
                bgcolor: 'rgba(255,255,255,0.1)',
                borderRadius: 3,
                padding: 0.5,
                border: '1px solid rgba(255,255,255,0.2)',
                '& .MuiToggleButton-root': {
                  color: 'rgba(255,255,255,0.8)',
                  border: 'none',
                  borderRadius: 2,
                  px: 2,
                  py: 1,
                  minWidth: 100,
                  fontWeight: 500,
                  fontSize: '0.875rem',
                  textTransform: 'none',
                  transition: 'all 0.2s ease-in-out',
                  '&:hover': {
                    bgcolor: 'rgba(255,255,255,0.15)',
                    color: 'rgba(255,255,255,0.95)',
                    transform: 'translateY(-1px)',
                  },
                  '&.Mui-selected': {
                    bgcolor: 'rgba(255,255,255,0.25)',
                    color: '#fff',
                    fontWeight: 600,
                    boxShadow: '0 2px 8px rgba(0,0,0,0.2)',
                    '&:hover': {
                      bgcolor: 'rgba(255,255,255,0.3)',
                    },
                  },
                },
              }}
            >
              <ToggleButton value="reasoning">
                <Psychology sx={{ mr: 0.5, fontSize: 18 }} />
                Reasoning
              </ToggleButton>
              <ToggleButton value="trace">
                <AccountTree sx={{ mr: 0.5, fontSize: 18 }} />
                Trace
              </ToggleButton>
            </ToggleButtonGroup>
          )}

          {/* Live updates indicator */}
          {session && isActive && !loading && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mr: 2 }}>
              <CircularProgress size={14} sx={{ color: 'inherit' }} />
              <Typography variant="caption" sx={{ color: 'inherit', fontSize: '0.75rem' }}>
                Live
              </Typography>
            </Box>
          )}

          {/* Loading spinner */}
          {loading && <CircularProgress size={20} sx={{ color: 'inherit' }} />}
        </SharedHeader>

        <Box sx={{ mt: 2 }}>
          {/* Loading state */}
          {loading && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              <HeaderSkeleton />
              <TraceTimelineSkeleton />
            </Box>
          )}

          {/* Error state */}
          {error && !loading && (
            <Alert
              severity="error"
              sx={{ mb: 2 }}
              action={
                <Button color="inherit" size="small" onClick={handleRetry}>
                  Retry
                </Button>
              }
            >
              <Typography variant="body1" gutterBottom>
                Failed to load trace data
              </Typography>
              <Typography variant="body2">{error}</Typography>
            </Alert>
          )}

          {/* Empty state */}
          {!session && !loading && !error && (
            <Alert severity="warning" sx={{ mt: 2 }}>
              <Typography variant="body1">
                Session not found or no longer available
              </Typography>
            </Alert>
          )}

          {/* Trace content */}
          {session && traceData && !loading && (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {/* Session Header (same as SessionDetailPage) */}
              <Suspense fallback={<HeaderSkeleton />}>
                <SessionHeader session={session} />
              </Suspense>

              {/* Trace Timeline */}
              <Suspense fallback={<TraceTimelineSkeleton />}>
                <TraceTimeline traceData={traceData} session={session} />
              </Suspense>
            </Box>
          )}
        </Box>
      </Container>

      {/* Version footer */}
      <VersionFooter />

      {/* Floating Action Button for quick alert submission access */}
      <FloatingSubmitAlertFab />
    </>
  );
}
