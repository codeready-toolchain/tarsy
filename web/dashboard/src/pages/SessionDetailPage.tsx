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
  ToggleButton,
  ToggleButtonGroup,
} from '@mui/material';
import {
  KeyboardDoubleArrowDown,
  Psychology,
  AccountTree,
} from '@mui/icons-material';

import { SharedHeader } from '../components/layout/SharedHeader.tsx';
import { VersionFooter } from '../components/layout/VersionFooter.tsx';
import { FloatingSubmitAlertFab } from '../components/common/FloatingSubmitAlertFab.tsx';
import { useAdvancedAutoScroll } from '../hooks/useAdvancedAutoScroll.ts';

import { getSession, getTimeline, handleAPIError } from '../services/api.ts';
import { websocketService } from '../services/websocket.ts';

import { parseTimelineToFlow } from '../utils/timelineParser.ts';
import type { FlowItem } from '../utils/timelineParser.ts';
import type { SessionDetailResponse, TimelineEvent, StageOverview } from '../types/session.ts';
import type { StreamingItem } from '../components/streaming/StreamingContentRenderer.tsx';
import type {
  TimelineCreatedPayload,
  TimelineCompletedPayload,
  StreamChunkPayload,
  SessionStatusPayload,
  StageStatusPayload,
  SessionProgressPayload,
  ExecutionProgressPayload,
  ExecutionStatusPayload,
} from '../types/events.ts';

import {
  EVENT_TIMELINE_CREATED,
  EVENT_TIMELINE_COMPLETED,
  EVENT_STREAM_CHUNK,
  EVENT_SESSION_STATUS,
  EVENT_STAGE_STATUS,
  EVENT_SESSION_PROGRESS,
  EVENT_EXECUTION_PROGRESS,
  EVENT_EXECUTION_STATUS,
  EVENT_CATCHUP_OVERFLOW,
  TIMELINE_STATUS,
  PHASE_STATUS_MESSAGE,
} from '../constants/eventTypes.ts';

import {
  ACTIVE_STATUSES,
  isTerminalStatus,
  type SessionStatus,
  SESSION_STATUS,
  EXECUTION_STATUS,
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
  sequenceNumber?: number;
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
  // Real-time execution status from execution.status WS events (executionId → {status, stageId}).
  // Higher priority than REST ExecutionOverview for immediate UI updates.
  // stageId is included so StageContent can filter out executions from other stages,
  // preventing phantom agent cards from appearing.
  const [executionStatuses, setExecutionStatuses] = useState<Map<string, { status: string; stageId: string }>>(
    () => new Map(),
  );

  // --- View / navigation ---
  const view = 'reasoning' as const;

  // --- Auto-scroll ---
  const [autoScrollEnabled, setAutoScrollEnabled] = useState(false);
  const disableTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const prevStatusRef = useRef<string | undefined>(undefined);
  const hasPerformedInitialScrollRef = useRef(false);

  // --- Jump navigation ---
  const [expandCounter, setExpandCounter] = useState(0);
  const [collapseCounter, _setCollapseCounter] = useState(0);
  const finalAnalysisRef = useRef<HTMLDivElement>(null);

  // --- Dedup tracking ---
  const knownEventIdsRef = useRef<Set<string>>(new Set());

  // --- Truncation re-fetch debounce ---
  // When truncated WS payloads arrive (content > 8KB), we re-fetch the full
  // timeline from the REST API. Multiple truncated events can arrive in quick
  // succession, so we debounce to avoid hammering the API.
  const truncationRefetchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // --- Streaming metadata ref (synchronous access for completed handler) ---
  // Tracks stageId, executionId, sequenceNumber, createdAt, and original
  // metadata for streaming events. Unlike streamingEvents state (which updates
  // async via React batching), this ref is read/written synchronously, ensuring
  // correct values in the timeline_event.completed handler.
  // createdAt is the timestamp from the timeline_event.created WS event so that
  // the completed TimelineEvent has accurate created_at / updated_at for
  // duration computation (without it, both would be the completion timestamp).
  const streamingMetaRef = useRef<Map<string, {
    eventType: string;
    stageId?: string;
    executionId?: string;
    sequenceNumber: number;
    metadata?: Record<string, unknown> | null;
    createdAt?: string;
  }>>(new Map());

  // applyFreshTimeline separates streaming events from completed events and
  // updates both timelineEvents and streamingEvents accordingly. This avoids
  // placing streaming events (which have empty content in the DB) into
  // timelineEvents where they would render as duplicate empty "Thought..." items
  // alongside the real streaming content in streamingEvents.
  const applyFreshTimeline = useCallback((freshTimeline: TimelineEvent[]) => {
    const completedEvents: TimelineEvent[] = [];
    const ids = new Set<string>();

    for (const ev of freshTimeline) {
      ids.add(ev.id);
      if (ev.status === TIMELINE_STATUS.STREAMING) {
        // Keep streaming events in the streaming system, not in timelineEvents.
        // Ensure metadata ref is populated for the completed handler.
        if (!streamingMetaRef.current.has(ev.id)) {
          streamingMetaRef.current.set(ev.id, {
            eventType: ev.event_type,
            stageId: ev.stage_id || undefined,
            executionId: ev.execution_id || undefined,
            sequenceNumber: ev.sequence_number,
            metadata: ev.metadata,
            createdAt: ev.created_at || undefined,
          });
          // Also add to streamingEvents if not already tracked (covers the
          // case where the REST fetch discovers a streaming event that we
          // missed the WebSocket timeline_event.created for).
          setStreamingEvents((prev) => {
            if (prev.has(ev.id)) return prev;
            const next = new Map(prev);
            next.set(ev.id, {
              eventType: ev.event_type,
              content: ev.content || '',
              stageId: ev.stage_id || undefined,
              executionId: ev.execution_id || undefined,
              sequenceNumber: ev.sequence_number,
              metadata: ev.metadata || undefined,
            });
            return next;
          });
        }
      } else {
        completedEvents.push(ev);
      }
    }

    setTimelineEvents(completedEvents);

    // Clean up streamingEvents: remove entries that are now completed in REST
    // (handles the case where the timeline_event.completed WS message was lost).
    const completedIds = new Set(completedEvents.map(ev => ev.id));
    setStreamingEvents((prev) => {
      let changed = false;
      const next = new Map(prev);
      for (const eventId of prev.keys()) {
        if (completedIds.has(eventId)) {
          next.delete(eventId);
          streamingMetaRef.current.delete(eventId);
          changed = true;
        }
      }
      return changed ? next : prev;
    });

    knownEventIdsRef.current = ids;
  }, []);

  // Debounced re-fetch for truncated events. Coalesces multiple truncation
  // events arriving within 300ms into a single API call.
  const refetchTimelineDebounced = useCallback(() => {
    if (!id) return;
    if (truncationRefetchTimerRef.current) {
      clearTimeout(truncationRefetchTimerRef.current);
    }
    truncationRefetchTimerRef.current = setTimeout(() => {
      truncationRefetchTimerRef.current = null;
      getTimeline(id).then((freshTimeline) => {
        applyFreshTimeline(freshTimeline);
      }).catch((err) => {
        console.warn('Failed to re-fetch timeline after truncated event:', err);
      });
    }, 300);
  }, [id, applyFreshTimeline]);

  // --- Derived ---
  const isActive = session
    ? ACTIVE_STATUSES.has(session.status as SessionStatus) ||
      session.status === SESSION_STATUS.PENDING
    : false;

  const flowItems: FlowItem[] = useMemo(
    () => (session ? parseTimelineToFlow(timelineEvents, session.stages) : []),
    [timelineEvents, session],
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

    // Clear streaming and progress state so stale items don't linger
    setStreamingEvents(new Map());
    streamingMetaRef.current.clear();
    setProgressStatus('Processing...');
    setAgentProgressStatuses(new Map());
    setExecutionStatuses(new Map());

    try {
      const [sessionData, timelineData] = await Promise.all([
        getSession(id),
        getTimeline(id),
      ]);
      setSession(sessionData);

      // Separate events with status "streaming" from completed events.
      // Streaming events have empty content in the DB and must be routed to
      // the streaming system so that:
      // 1. They're rendered by StreamingContentRenderer (which hides empty content)
      // 2. stream.chunk WebSocket events update them (chunks only update streamingEvents)
      // 3. timeline_event.completed properly transitions them to timelineEvents
      const completedEvents: TimelineEvent[] = [];
      const restStreamingItems = new Map<string, ExtendedStreamingItem>();
      const ids = new Set<string>();

      for (const ev of timelineData) {
        ids.add(ev.id);
        if (ev.status === TIMELINE_STATUS.STREAMING) {
          // Route to streaming system
          restStreamingItems.set(ev.id, {
            eventType: ev.event_type,
            content: ev.content || '',
            stageId: ev.stage_id || undefined,
            executionId: ev.execution_id || undefined,
            sequenceNumber: ev.sequence_number,
            metadata: ev.metadata || undefined,
          });
          // Store metadata for the completed handler
          streamingMetaRef.current.set(ev.id, {
            eventType: ev.event_type,
            stageId: ev.stage_id || undefined,
            executionId: ev.execution_id || undefined,
            sequenceNumber: ev.sequence_number,
            metadata: ev.metadata,
            createdAt: ev.created_at || undefined,
          });
        } else {
          completedEvents.push(ev);
        }
      }

      setTimelineEvents(completedEvents);
      setStreamingEvents(restStreamingItems);
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
          const isTruncated = !!(data as Record<string, unknown>).truncated;

          // Truncated payloads only have type, event_id, session_id — re-fetch.
          // The backend truncates NOTIFY payloads exceeding PostgreSQL's ~8KB
          // limit, stripping all fields except routing info and truncated:true.
          if (isTruncated) {
            refetchTimelineDebounced();
            return;
          }

          // Dedup: skip if we already have this event from REST
          if (knownEventIdsRef.current.has(payload.event_id)) {
            return;
          }

          if (payload.status === TIMELINE_STATUS.STREAMING) {
            // Store metadata in synchronous ref for the completed handler.
            // event_type is stored because the completed payload sometimes
            // arrives without it (observed in runtime logs for fast tool calls).
            // createdAt preserves the original creation timestamp so the
            // completed TimelineEvent gets accurate created_at for duration.
            streamingMetaRef.current.set(payload.event_id, {
              eventType: payload.event_type,
              stageId: payload.stage_id,
              executionId: payload.execution_id,
              sequenceNumber: payload.sequence_number,
              metadata: payload.metadata || null,
              createdAt: payload.timestamp,
            });
            // Add to streaming map
            setStreamingEvents((prev) => {
              const next = new Map(prev);
              next.set(payload.event_id, {
                eventType: payload.event_type,
                content: payload.content || '',
                stageId: payload.stage_id,
                executionId: payload.execution_id,
                sequenceNumber: payload.sequence_number,
                metadata: payload.metadata || undefined,
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
          const isTruncated = !!(data as Record<string, unknown>).truncated;

          // Read streaming metadata from synchronous ref (reliable, not
          // subject to React batching). Then clean up both ref and state.
          const meta = streamingMetaRef.current.get(payload.event_id);
          streamingMetaRef.current.delete(payload.event_id);

          // Remove from streaming state
          setStreamingEvents((prev) => {
            if (!prev.has(payload.event_id)) return prev;
            const next = new Map(prev);
            next.delete(payload.event_id);
            return next;
          });

          // ── Truncated payload handling ──────────────────────────
          // The backend truncates NOTIFY payloads that exceed PostgreSQL's
          // ~8KB limit (e.g. tool calls with large results). Truncated
          // payloads only contain type, event_id, session_id, db_event_id,
          // and truncated:true — all other fields are stripped.
          // When detected, re-fetch the full timeline from the REST API.
          if (isTruncated) {
            refetchTimelineDebounced();
            return;
          }

          // ── Full payload handling ──────────────────────────────
          // Add or update in timeline
          if (knownEventIdsRef.current.has(payload.event_id)) {
            // Update existing event in-place (content / status may have changed).
            // Merge metadata: the existing event may have full metadata from
            // timeline_event.created (e.g. tool_name, server_name, arguments),
            // while the completed payload may add new fields (e.g. is_error).
            // Using spread merge preserves both.
            setTimelineEvents((prev) =>
              prev.map((ev) =>
                ev.id === payload.event_id
                    ? {
                      ...ev,
                      content: payload.content,
                      status: payload.status,
                      metadata: (ev.metadata || payload.metadata)
                        ? { ...(ev.metadata || {}), ...(payload.metadata || {}) }
                        : null,
                      updated_at: payload.timestamp,
                    }
                  : ev,
              ),
            );
          } else {
            // New completed event (was only streaming, not in REST data)
            knownEventIdsRef.current.add(payload.event_id);
            // Merge metadata: created event metadata (tool_name, server_name, etc.)
            // is the base, completed event metadata (is_error, etc.) overrides.
            const mergedMetadata = (meta?.metadata || payload.metadata)
              ? { ...(meta?.metadata || {}), ...(payload.metadata || {}) }
              : null;
            setTimelineEvents((prev) => [
              ...prev,
              {
                id: payload.event_id,
                session_id: id,
                stage_id: meta?.stageId ?? null,
                execution_id: meta?.executionId ?? null,
                sequence_number: meta?.sequenceNumber ?? 0,
                event_type: payload.event_type,
                status: payload.status,
                content: payload.content,
                metadata: mergedMetadata,
                created_at: meta?.createdAt ?? payload.timestamp,
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

          // If terminal, re-fetch session + timeline for final fields and
          // authoritative metadata (fixes any metadata merge gaps from streaming)
          if (isTerminalStatus(payload.status as SessionStatus)) {
            Promise.all([
              getSession(id),
              getTimeline(id),
            ]).then(([freshSession, freshTimeline]) => {
              setSession(freshSession);
              applyFreshTimeline(freshTimeline);
            }).catch((err) => {
              console.warn('Failed to re-fetch session/timeline after terminal status:', err);
            });
          }
          return;
        }

        // --- stage.status ---
        if (eventType === EVENT_STAGE_STATUS) {
          const payload = data as unknown as StageStatusPayload;
          setSession((prev) => {
            if (!prev) return prev;
            const stages = prev.stages ?? [];
            const existing = stages.find((s) => s.id === payload.stage_id);
            if (existing) {
              // Update existing stage
              const updatedStages = stages.map((stage) =>
                stage.id === payload.stage_id
                  ? { ...stage, status: payload.status }
                  : stage,
              );
              return { ...prev, stages: updatedStages };
            }
            // New stage not yet in REST data — add a minimal entry only if stage_id is present
            if (!payload.stage_id) {
              return prev;
            }
            const safeIndex = payload.stage_index ?? 0;
            const newStage: StageOverview = {
              id: payload.stage_id,
              stage_name: payload.stage_name || `Stage ${safeIndex + 1}`,
              stage_index: safeIndex,
              status: payload.status,
              parallel_type: null,
              expected_agent_count: 1,
              started_at: payload.timestamp || null,
              completed_at: null,
            };
            return { ...prev, stages: [...stages, newStage] };
          });

          // When a new stage starts, clear per-agent progress and execution
          // status maps from the previous (potentially parallel) stage.  This
          // mirrors old tarsy's pattern of clearing agentProgressStatuses when
          // the parallel parent stage completes — by the time the next stage
          // starts, the previous parallel execution state is no longer relevant.
          if (payload.status === EXECUTION_STATUS.STARTED) {
            setAgentProgressStatuses(new Map());
            setExecutionStatuses(new Map());

            // Re-fetch session detail to get execution overviews (agent names,
            // LLM providers, iteration strategies) for parallel agents.
            getSession(id).then((fresh) => setSession(fresh)).catch((err) => {
              console.warn('Failed to re-fetch session on stage start:', err);
            });
          }
          return;
        }

        // --- session.progress ---
        if (eventType === EVENT_SESSION_PROGRESS) {
          const payload = data as unknown as SessionProgressPayload;
          // Map backend status_text to user-friendly messages
          const raw = (payload.status_text || '').toLowerCase();
          let status = payload.status_text || 'Processing...';
          if (raw.includes('synthesiz')) status = 'Synthesizing...';
          else if (raw.includes('executive summary')) status = 'Finalizing...';
          else if (raw.startsWith('starting stage:')) status = 'Investigating...';
          setProgressStatus(status);

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
          // Map phase to clean display message (e.g. "Investigating...", "Distilling...")
          // matching old tarsy's ProgressStatusMessage. Fall back to raw message if
          // the phase isn't in the map (shouldn't happen, but defensive).
          const phaseMessage = PHASE_STATUS_MESSAGE[payload.phase] || payload.message;
          setAgentProgressStatuses((prev) => {
            const next = new Map(prev);
            next.set(payload.execution_id, phaseMessage);
            return next;
          });
          // Do NOT update session-level progressStatus here.
          // Per-agent progress must stay isolated in agentProgressStatuses so that
          // the "Waiting for other agents..." check in ConversationTimeline works
          // correctly. Session-level status is driven by session.progress events.
          return;
        }

        // --- execution.status ---
        // Real-time per-agent status transitions (active, completed, failed, etc.).
        // Updates executionStatuses map so StageContent can reflect individual
        // agent terminal status without waiting for the entire stage to complete.
        if (eventType === EVENT_EXECUTION_STATUS) {
          const payload = data as unknown as ExecutionStatusPayload;
          setExecutionStatuses((prev) => {
            const next = new Map(prev);
            next.set(payload.execution_id, { status: payload.status, stageId: payload.stage_id });
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
  const sessionStatus = session?.status;
  useEffect(() => {
    if (!sessionStatus) return;

    const previousActive = prevStatusRef.current
      ? ACTIVE_STATUSES.has(prevStatusRef.current as SessionStatus) ||
        prevStatusRef.current === SESSION_STATUS.PENDING
      : false;
    const currentActive =
      ACTIVE_STATUSES.has(sessionStatus as SessionStatus) ||
      sessionStatus === SESSION_STATUS.PENDING;

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
      prevStatusRef.current = sessionStatus;
    }
  }, [sessionStatus]);

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
      <Container maxWidth={false} sx={{ py: 2, px: { xs: 1, sm: 2 } }}>
        <SharedHeader title={headerTitle} showBackButton>
          {/* Session info */}
          {session && !loading && (
            <Typography variant="body2" sx={{ mr: 2, opacity: 0.8, color: 'white' }}>
              {session.stages?.length || 0} stages &bull; {(session.llm_interaction_count ?? 0) + (session.mcp_interaction_count ?? 0)} interactions
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
                    🔄 Auto-scroll
                  </Typography>
                }
                sx={{ m: 0, color: 'inherit' }}
              />
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
            {(session.stages && session.stages.length > 0) || streamingEvents.size > 0 ? (
              <Suspense fallback={<TimelineSkeleton />}>
                <ConversationTimeline
                  items={flowItems}
                  stages={session.stages || []}
                  isActive={isActive}
                  progressStatus={progressStatus}
                  streamingEvents={streamingEvents}
                  agentProgressStatuses={agentProgressStatuses}
                  executionStatuses={executionStatuses}
                  chainId={session.chain_id}
                />
              </Suspense>
            ) : isActive ? (
              <Box
                sx={{
                  py: 8,
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: 3,
                }}
              >
                {/* Pulsing ring spinner */}
                <Box
                  sx={{
                    position: 'relative',
                    width: 64,
                    height: 64,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}
                >
                  <CircularProgress
                    size={56}
                    thickness={2.5}
                    color={session.status === SESSION_STATUS.PENDING ? 'warning' : 'primary'}
                  />
                  <Box
                    sx={{
                      position: 'absolute',
                      width: 64,
                      height: 64,
                      borderRadius: '50%',
                      border: '2px solid',
                      borderColor: session.status === SESSION_STATUS.PENDING
                        ? 'rgba(237, 108, 2, 0.15)'
                        : 'rgba(25, 118, 210, 0.15)',
                      animation: 'init-pulse 2s ease-in-out infinite',
                      '@keyframes init-pulse': {
                        '0%, 100%': { transform: 'scale(1)', opacity: 0.6 },
                        '50%': { transform: 'scale(1.15)', opacity: 0 },
                      },
                    }}
                  />
                </Box>
                {/* Shimmer text */}
                <Typography
                  variant="body1"
                  sx={{
                    fontSize: '1.1rem',
                    fontWeight: 500,
                    fontStyle: 'italic',
                    background: session.status === SESSION_STATUS.PENDING
                      ? 'linear-gradient(90deg, rgba(237,108,2,0.5) 0%, rgba(237,108,2,0.7) 40%, rgba(237,108,2,0.9) 50%, rgba(237,108,2,0.7) 60%, rgba(237,108,2,0.5) 100%)'
                      : 'linear-gradient(90deg, rgba(0,0,0,0.5) 0%, rgba(0,0,0,0.7) 40%, rgba(0,0,0,0.9) 50%, rgba(0,0,0,0.7) 60%, rgba(0,0,0,0.5) 100%)',
                    backgroundSize: '200% 100%',
                    backgroundClip: 'text',
                    WebkitBackgroundClip: 'text',
                    WebkitTextFillColor: 'transparent',
                    animation: 'init-shimmer 3s linear infinite',
                    '@keyframes init-shimmer': {
                      '0%': { backgroundPosition: '200% center' },
                      '100%': { backgroundPosition: '-200% center' },
                    },
                  }}
                >
                  {session.status === SESSION_STATUS.PENDING
                    ? 'Session queued, waiting to start...'
                    : 'Initializing investigation...'}
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
                  This session is missing stage execution data. All sessions should be processed as chains.
                </Typography>
                <Typography variant="caption" color="text.secondary" sx={{ mt: 1, display: 'block' }}>
                  Session: {session.id} &bull; Type: {session.alert_type || 'Unknown'}
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
                collapseCounter={collapseCounter}
              />
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
