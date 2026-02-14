/**
 * SessionDetailPage — conversation timeline view for a single session.
 *
 * Orchestrates:
 * - REST data loading (session detail + timeline events)
 * - WebSocket subscriptions for live streaming + status updates
 * - Streaming content management (Map of active streaming items)
 * - Progress status tracking (session-level + per-agent)
 * - Auto-scroll for active sessions
 * - View toggle (reasoning ↔ trace navigation)
 * - Jump navigation buttons
 * - Loading skeletons, error states, empty states
 */

import { useState, useEffect, useRef, useCallback, useMemo, lazy, Suspense } from 'react';
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
  Switch,
  FormControlLabel,
} from '@mui/material';
import { KeyboardDoubleArrowDown } from '@mui/icons-material';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import { useAdvancedAutoScroll } from '../hooks/useAdvancedAutoScroll.ts';

import { getSession, getTimeline, handleAPIError } from '../services/api.ts';
import { websocketService } from '../services/websocket.ts';

import { parseTimelineToFlow } from '../utils/timelineParser.ts';
import type { FlowItem } from '../utils/timelineParser.ts';
import type { SessionDetailResponse, TimelineEvent } from '../types/session.ts';
import type { StreamingItem } from '../components/streaming/StreamingContentRenderer.tsx';
import type {
  TimelineCreatedPayload,
  TimelineCompletedPayload,
  StreamChunkPayload,
  SessionStatusPayload,
  StageStatusPayload,
  SessionProgressPayload,
  ExecutionProgressPayload,
} from '../types/events.ts';

import {
  EVENT_TIMELINE_CREATED,
  EVENT_TIMELINE_COMPLETED,
  EVENT_STREAM_CHUNK,
  EVENT_SESSION_STATUS,
  EVENT_STAGE_STATUS,
  EVENT_SESSION_PROGRESS,
  EVENT_EXECUTION_PROGRESS,
  EVENT_CATCHUP_OVERFLOW,
  TIMELINE_STATUS,
} from '../constants/eventTypes.ts';

import {
  ACTIVE_STATUSES,
  isTerminalStatus,
  type SessionStatus,
  SESSION_STATUS,
} from '../constants/sessionStatus.ts';

// ────────────────────────────────────────────────────────────
// Lazy-loaded sub-components (matching old dashboard pattern)
// ────────────────────────────────────────────────────────────

const SessionHeader = lazy(() => import('../components/session/SessionHeader.tsx'));
const OriginalAlertCard = lazy(() => import('../components/session/OriginalAlertCard.tsx'));
const FinalAnalysisCard = lazy(() => import('../components/session/FinalAnalysisCard.tsx'));
const ConversationTimeline = lazy(() => import('../components/session/ConversationTimeline.tsx'));

// ────────────────────────────────────────────────────────────
// Skeleton placeholders
// ────────────────────────────────────────────────────────────

function HeaderSkeleton() {
  return (
    <Paper sx={{ p: 3, mb: 2 }}>
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

function AlertCardSkeleton() {
  return (
    <Paper sx={{ p: 3, mb: 2 }}>
      <Skeleton variant="text" width="30%" height={28} sx={{ mb: 2 }} />
      <Box sx={{ display: 'flex', gap: 3 }}>
        <Skeleton variant="rectangular" width="50%" height={200} />
        <Skeleton variant="rectangular" width="50%" height={200} />
      </Box>
    </Paper>
  );
}

function TimelineSkeleton() {
  return (
    <Paper sx={{ p: 3, mb: 2 }}>
      <Skeleton variant="text" width="25%" height={28} sx={{ mb: 2 }} />
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        {[1, 2, 3].map((i) => (
          <Box key={i} sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
            <Skeleton variant="circular" width={32} height={32} />
            <Box sx={{ flex: 1 }}>
              <Skeleton variant="text" width="70%" />
              <Skeleton variant="text" width="40%" />
            </Box>
          </Box>
        ))}
      </Box>
    </Paper>
  );
}

// ────────────────────────────────────────────────────────────
// Extended streaming item (includes routing metadata)
// ────────────────────────────────────────────────────────────

interface ExtendedStreamingItem extends StreamingItem {
  stageId?: string;
  executionId?: string;
}

// ────────────────────────────────────────────────────────────
// Page component
// ────────────────────────────────────────────────────────────

export function SessionDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  // --- Core state ---
  const [session, setSession] = useState<SessionDetailResponse | null>(null);
  const [timelineEvents, setTimelineEvents] = useState<TimelineEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // --- Streaming state ---
  const [streamingEvents, setStreamingEvents] = useState<Map<string, ExtendedStreamingItem>>(
    () => new Map(),
  );

  // --- Progress status ---
  const [progressStatus, setProgressStatus] = useState('Processing...');
  const [agentProgressStatuses, setAgentProgressStatuses] = useState<Map<string, string>>(
    () => new Map(),
  );

  // --- View / navigation ---
  const view = 'reasoning' as const;

  // --- Auto-scroll ---
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(false);
  const disableTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const prevStatusRef = useRef<string | undefined>(undefined);
  const hasPerformedInitialScrollRef = useRef(false);

  // --- Live duration ---
  const [liveDurationMs, setLiveDurationMs] = useState<number | null>(null);

  // --- Jump navigation ---
  const [expandCounter, setExpandCounter] = useState(0);
  const finalAnalysisRef = useRef<HTMLDivElement>(null);

  // --- Dedup tracking ---
  const knownEventIdsRef = useRef<Set<string>>(new Set());

  // --- Derived ---
  const isActive = session
    ? ACTIVE_STATUSES.has(session.status as SessionStatus) ||
      session.status === SESSION_STATUS.PENDING
    : false;

  const flowItems: FlowItem[] = useMemo(
    () => (session ? parseTimelineToFlow(timelineEvents, session.stages) : []),
    [timelineEvents, session?.stages],
  );

  // Header title with session ID suffix (matching old dashboard)
  const headerTitle = session
    ? `AI Reasoning View - ${id?.slice(-8) ?? ''}`
    : 'AI Reasoning View';

  // ────────────────────────────────────────────────────────────
  // REST data loading
  // ────────────────────────────────────────────────────────────

  const loadData = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);
    try {
      const [sessionData, timelineData] = await Promise.all([
        getSession(id),
        getTimeline(id),
      ]);
      setSession(sessionData);
      setTimelineEvents(timelineData);

      // Populate dedup set
      const ids = new Set<string>();
      for (const ev of timelineData) {
        ids.add(ev.id);
      }
      knownEventIdsRef.current = ids;
    } catch (err) {
      setError(handleAPIError(err));
    } finally {
      setLoading(false);
    }
  }, [id]);

  // Initial load
  useEffect(() => {
    loadData();
    // Reset scroll flags on session change
    hasPerformedInitialScrollRef.current = false;
  }, [loadData]);

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

        // --- timeline_event.created ---
        if (eventType === EVENT_TIMELINE_CREATED) {
          const payload = data as unknown as TimelineCreatedPayload;

          // Dedup: skip if we already have this event from REST
          if (knownEventIdsRef.current.has(payload.event_id)) return;

          if (payload.status === TIMELINE_STATUS.STREAMING) {
            // Add to streaming map
            setStreamingEvents((prev) => {
              const next = new Map(prev);
              next.set(payload.event_id, {
                eventType: payload.event_type,
                content: payload.content || '',
                stageId: payload.stage_id,
                executionId: payload.execution_id,
              });
              return next;
            });
          } else {
            // Completed event → add directly to timeline
            knownEventIdsRef.current.add(payload.event_id);
            setTimelineEvents((prev) => [
              ...prev,
              {
                id: payload.event_id,
                session_id: payload.session_id,
                stage_id: payload.stage_id || null,
                execution_id: payload.execution_id || null,
                sequence_number: payload.sequence_number,
                event_type: payload.event_type,
                status: payload.status,
                content: payload.content,
                metadata: payload.metadata || null,
                created_at: payload.timestamp,
                updated_at: payload.timestamp,
              },
            ]);
          }
          return;
        }

        // --- stream.chunk ---
        if (eventType === EVENT_STREAM_CHUNK) {
          const payload = data as unknown as StreamChunkPayload;
          setStreamingEvents((prev) => {
            const existing = prev.get(payload.event_id);
            if (!existing) return prev;
            const next = new Map(prev);
            next.set(payload.event_id, {
              ...existing,
              content: existing.content + payload.delta,
            });
            return next;
          });
          return;
        }

        // --- timeline_event.completed ---
        if (eventType === EVENT_TIMELINE_COMPLETED) {
          const payload = data as unknown as TimelineCompletedPayload;

          // Remove from streaming map
          setStreamingEvents((prev) => {
            if (!prev.has(payload.event_id)) return prev;
            const next = new Map(prev);
            next.delete(payload.event_id);
            return next;
          });

          // Add or update in timeline
          if (knownEventIdsRef.current.has(payload.event_id)) {
            // Update existing event in-place (content / status may have changed)
            setTimelineEvents((prev) =>
              prev.map((ev) =>
                ev.id === payload.event_id
                  ? {
                      ...ev,
                      content: payload.content,
                      status: payload.status,
                      metadata: payload.metadata || ev.metadata,
                      updated_at: payload.timestamp,
                    }
                  : ev,
              ),
            );
          } else {
            // New completed event (was only streaming, not in REST data)
            knownEventIdsRef.current.add(payload.event_id);
            setTimelineEvents((prev) => [
              ...prev,
              {
                id: payload.event_id,
                session_id: id,
                stage_id: null,
                execution_id: null,
                sequence_number: 0, // Will be sorted by parser
                event_type: payload.event_type,
                status: payload.status,
                content: payload.content,
                metadata: payload.metadata || null,
                created_at: payload.timestamp,
                updated_at: payload.timestamp,
              },
            ]);
          }
          return;
        }

        // --- session.status ---
        if (eventType === EVENT_SESSION_STATUS) {
          const payload = data as unknown as SessionStatusPayload;
          setSession((prev) => {
            if (!prev) return prev;
            return { ...prev, status: payload.status };
          });

          // If terminal, re-fetch session for final fields
          if (isTerminalStatus(payload.status as SessionStatus)) {
            getSession(id).then((fresh) => setSession(fresh)).catch(() => {});
          }
          return;
        }

        // --- stage.status ---
        if (eventType === EVENT_STAGE_STATUS) {
          const payload = data as unknown as StageStatusPayload;
          setSession((prev) => {
            if (!prev) return prev;
            const updatedStages = prev.stages.map((stage) =>
              stage.id === payload.stage_id
                ? { ...stage, status: payload.status }
                : stage,
            );
            return { ...prev, stages: updatedStages };
          });
          return;
        }

        // --- session.progress ---
        if (eventType === EVENT_SESSION_PROGRESS) {
          const payload = data as unknown as SessionProgressPayload;
          setProgressStatus(payload.status_text || 'Processing...');

          // Also update stage progress counts on the session
          setSession((prev) => {
            if (!prev) return prev;
            return {
              ...prev,
              current_stage_index: payload.current_stage_index,
              total_stages: payload.total_stages,
            };
          });
          return;
        }

        // --- execution.progress ---
        if (eventType === EVENT_EXECUTION_PROGRESS) {
          const payload = data as unknown as ExecutionProgressPayload;
          setAgentProgressStatuses((prev) => {
            const next = new Map(prev);
            next.set(payload.execution_id, payload.message);
            return next;
          });
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
  }, [id, loadData]);

  // ────────────────────────────────────────────────────────────
  // Auto-scroll lifecycle
  // ────────────────────────────────────────────────────────────

  // Update auto-scroll enabled state when session transitions between active/inactive
  useEffect(() => {
    if (!session) return;

    const previousActive = prevStatusRef.current
      ? ACTIVE_STATUSES.has(prevStatusRef.current as SessionStatus) ||
        prevStatusRef.current === SESSION_STATUS.PENDING
      : false;
    const currentActive =
      ACTIVE_STATUSES.has(session.status as SessionStatus) ||
      session.status === SESSION_STATUS.PENDING;

    // Only update on first load or when crossing active↔inactive boundary
    if (prevStatusRef.current === undefined || previousActive !== currentActive) {
      if (currentActive) {
        // Transitioning to active — enable immediately, clear pending disable
        if (disableTimeoutRef.current) {
          clearTimeout(disableTimeoutRef.current);
          disableTimeoutRef.current = null;
        }
        setAutoScrollEnabled(true);
      } else {
        // Transitioning to inactive — delay disable for final content
        if (disableTimeoutRef.current) {
          clearTimeout(disableTimeoutRef.current);
        }
        disableTimeoutRef.current = setTimeout(() => {
          setAutoScrollEnabled(false);
          disableTimeoutRef.current = null;
        }, 2000);
      }
      prevStatusRef.current = session.status;
    }
  }, [session?.status]);

  // Initial scroll to bottom for active sessions
  useEffect(() => {
    if (
      session &&
      !loading &&
      !hasPerformedInitialScrollRef.current &&
      (ACTIVE_STATUSES.has(session.status as SessionStatus) ||
        session.status === SESSION_STATUS.PENDING)
    ) {
      const timer = setTimeout(() => {
        window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
        hasPerformedInitialScrollRef.current = true;
      }, 500);
      return () => clearTimeout(timer);
    }
  }, [session, loading]);

  // Cleanup disable timeout on unmount
  useEffect(() => {
    return () => {
      if (disableTimeoutRef.current) clearTimeout(disableTimeoutRef.current);
    };
  }, []);

  // Hook up centralized auto-scroll
  useAdvancedAutoScroll({ enabled: autoScrollEnabled });

  // ────────────────────────────────────────────────────────────
  // Live duration timer
  // ────────────────────────────────────────────────────────────

  useEffect(() => {
    if (!isActive || !session?.started_at) {
      setLiveDurationMs(null);
      return;
    }
    const start = new Date(session.started_at).getTime();
    const tick = () => setLiveDurationMs(Date.now() - start);
    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, [isActive, session?.started_at]);

  // ────────────────────────────────────────────────────────────
  // View toggle
  // ────────────────────────────────────────────────────────────

  const handleViewChange = useCallback(
    (newView: 'reasoning' | 'trace') => {
      if (newView === 'trace' && id) {
        navigate(`/sessions/${id}/trace`);
      }
      // 'reasoning' is the current page — no-op
    },
    [id, navigate],
  );

  // ────────────────────────────────────────────────────────────
  // Jump navigation
  // ────────────────────────────────────────────────────────────

  const handleJumpToSummary = useCallback(() => {
    setExpandCounter((prev) => prev + 1);
    setTimeout(() => {
      if (finalAnalysisRef.current) {
        const yOffset = -20;
        const y =
          finalAnalysisRef.current.getBoundingClientRect().top + window.pageYOffset + yOffset;
        window.scrollTo({ top: y, behavior: 'smooth' });
      }
    }, 500);
  }, []);

  // ────────────────────────────────────────────────────────────
  // Auto-scroll toggle handler
  // ────────────────────────────────────────────────────────────

  const handleAutoScrollToggle = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      setAutoScrollEnabled(event.target.checked);
    },
    [],
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

  const hasFinalContent = session?.final_analysis || session?.executive_summary;

  return (
    <>
      <SharedHeader title={headerTitle} showBackButton>
        {/* Session info */}
        {session && !loading && (
          <Typography variant="body2" sx={{ mr: 2, opacity: 0.8, color: 'white' }}>
            {session.stages?.length || 0} stages &bull; {session.llm_interaction_count + session.mcp_interaction_count} interactions
          </Typography>
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

        {/* Auto-scroll toggle — only for active sessions */}
        {session && isActive && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mr: 2 }}>
            <FormControlLabel
              control={
                <Switch
                  checked={autoScrollEnabled}
                  onChange={handleAutoScrollToggle}
                  size="small"
                  color="default"
                />
              }
              label={
                <Typography variant="caption" sx={{ color: 'inherit' }}>
                  Auto-scroll
                </Typography>
              }
              sx={{ m: 0, color: 'inherit' }}
            />
          </Box>
        )}

        {/* Loading spinner */}
        {loading && <CircularProgress size={20} sx={{ color: 'inherit' }} />}
      </SharedHeader>

      <Container maxWidth={false} sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        {/* Loading state */}
        {loading && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
            <HeaderSkeleton />
            <AlertCardSkeleton />
            <TimelineSkeleton />
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
              Failed to load session details
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

        {/* Session content */}
        {session && !loading && (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }} data-autoscroll-container>
            {/* Session Header */}
            <Suspense fallback={<HeaderSkeleton />}>
              <SessionHeader
                session={session}
                view={view}
                onViewChange={handleViewChange}
                liveDurationMs={liveDurationMs}
              />
            </Suspense>

            {/* Original Alert Data */}
            <Suspense fallback={<AlertCardSkeleton />}>
              <OriginalAlertCard alertData={session.alert_data} />
            </Suspense>

            {/* Jump to Summary button */}
            {hasFinalContent && (
              <Box sx={{ display: 'flex', justifyContent: 'center', my: 1.5 }}>
                <Button
                  variant="text"
                  size="medium"
                  onClick={handleJumpToSummary}
                  startIcon={<KeyboardDoubleArrowDown />}
                  endIcon={<KeyboardDoubleArrowDown />}
                  sx={{
                    textTransform: 'none',
                    fontWeight: 600,
                    fontSize: '0.95rem',
                    py: 1,
                    px: 3,
                    color: 'primary.main',
                    '&:hover': { backgroundColor: 'action.hover' },
                  }}
                >
                  {session.executive_summary ? 'Jump to Summary' : 'Jump to Final Analysis'}
                </Button>
              </Box>
            )}

            {/* Conversation Timeline */}
            {session.stages && session.stages.length > 0 ? (
              <Suspense fallback={<TimelineSkeleton />}>
                <ConversationTimeline
                  items={flowItems}
                  stages={session.stages}
                  isActive={isActive}
                  progressStatus={progressStatus}
                  streamingEvents={streamingEvents}
                  agentProgressStatuses={agentProgressStatuses}
                />
              </Suspense>
            ) : isActive ? (
              <Box
                sx={{
                  py: 8,
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: 2,
                }}
              >
                <CircularProgress size={48} />
                <Typography variant="body1" color="text.secondary">
                  Initializing investigation...
                </Typography>
              </Box>
            ) : session.status === SESSION_STATUS.CANCELLED ? (
              <Alert severity="info" sx={{ mb: 2 }}>
                <Typography variant="body2">
                  This session was cancelled before processing started.
                </Typography>
              </Alert>
            ) : (
              <Alert severity="error" sx={{ mb: 2 }}>
                <Typography variant="h6" gutterBottom>
                  Backend Chain Execution Error
                </Typography>
                <Typography variant="body2">
                  This session is missing stage execution data.
                </Typography>
              </Alert>
            )}

            {/* Final AI Analysis */}
            <Suspense fallback={<Skeleton variant="rectangular" height={200} />}>
              <FinalAnalysisCard
                ref={finalAnalysisRef}
                analysis={session.final_analysis}
                summary={session.executive_summary}
                sessionStatus={session.status}
                errorMessage={session.error_message}
                expandCounter={expandCounter}
              />
            </Suspense>
          </Box>
        )}
      </Container>

      {/* Version footer */}
      <VersionFooter />

      {/* Floating Action Button for quick alert submission access */}
      <FloatingSubmitAlertFab />
    </>
  );
}
