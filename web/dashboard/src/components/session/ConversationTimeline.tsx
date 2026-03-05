import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import {
  Box,
  Typography,
  Chip,
  Collapse,
  Card,
  CardContent,
  Button,
} from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
} from '@mui/icons-material';
import type { FlowItem, TimelineStats, StageGroup } from '../../utils/timelineParser';
import type { StageOverview } from '../../types/session';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import {
  FLOW_ITEM,
  groupFlowItemsByStage,
  getTimelineStats,
  isFlowItemCollapsible,
  isFlowItemTerminal,
  flowItemsToPlainText,
} from '../../utils/timelineParser';
import { TIMELINE_EVENT_TYPES, STAGE_TYPE } from '../../constants/eventTypes';
import StageSeparator from '../timeline/StageSeparator';
import StageContent from '../timeline/StageContent';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import ProcessingIndicator from '../streaming/ProcessingIndicator';
import CopyButton from '../shared/CopyButton';
import InitializingSpinner from '../common/InitializingSpinner';
import { TERMINAL_EXECUTION_STATUSES } from '../../constants/sessionStatus';

/**
 * Synthesis stages auto-collapse only when the session is no longer active
 * AND the stage itself has reached a terminal status.
 * While the session is streaming, synthesis stays expanded so the user
 * can watch the reasoning flow in real time.
 */
function shouldAutoCollapseStage(group: StageGroup, isSessionActive: boolean): boolean {
  const isCollapsible = group.stageType === STAGE_TYPE.SYNTHESIS || group.stageType === STAGE_TYPE.EXEC_SUMMARY;
  if (!isCollapsible) return false;
  if (isSessionActive) return false;
  return TERMINAL_EXECUTION_STATUSES.has(group.stageStatus);
}

interface ConversationTimelineProps {
  /** Flat list of FlowItems (from parseTimelineToFlow) */
  items: FlowItem[];
  /** Stage overviews from session detail */
  stages: StageOverview[];
  /** Whether the session is actively processing */
  isActive: boolean;
  /** Processing status message for the indicator */
  progressStatus?: string;
  /** Active streaming events keyed by event_id */
  streamingEvents?: Map<string, StreamingItem & { stageId?: string; executionId?: string }>;
  /** Per-agent progress statuses */
  agentProgressStatuses?: Map<string, string>;
  /** Real-time execution statuses from execution.status WS events (executionId → {status, stageId, agentIndex}) */
  executionStatuses?: Map<string, { status: string; stageId: string; agentIndex: number }>;
  /** Sub-agent streaming events (events with parent_execution_id) */
  subAgentStreamingEvents?: Map<string, StreamingItem & { stageId?: string; executionId?: string }>;
  /** Sub-agent execution statuses (events with parent_execution_id) */
  subAgentExecutionStatuses?: Map<string, { status: string; stageId: string; agentIndex: number }>;
  /** Sub-agent progress statuses (events with parent_execution_id) */
  subAgentProgressStatuses?: Map<string, string>;
  /** Chain ID for the header display */
  chainId?: string;
  /** Whether a chat stage is currently in progress (session may be terminal) */
  chatStageInProgress?: boolean;
  /** Set of stage IDs that are chat stages (for suppressing auto-collapse) */
  chatStageIds?: Set<string>;
}

/**
 * ConversationTimeline - main container for the session reasoning flow.
 *
 * Responsibilities:
 * - Groups items by stage (via groupFlowItemsByStage)
 * - Renders stage separators with collapse/expand
 * - Delegates stage content to StageContent (unified single/parallel rendering)
 * - Manages auto-collapse system (per-item tracking with manual overrides)
 * - Shows stats chips (thoughts, tool calls, errors, etc.)
 * - Supports copy-all-flow
 * - Shows ProcessingIndicator for active sessions
 * - Renders streaming events at the bottom of the appropriate stage
 */
export default function ConversationTimeline({
  items,
  stages,
  isActive,
  progressStatus,
  streamingEvents,
  agentProgressStatuses,
  executionStatuses,
  subAgentStreamingEvents,
  subAgentExecutionStatuses,
  subAgentProgressStatuses,
  chainId,
  chatStageInProgress,
  chatStageIds,
}: ConversationTimelineProps) {
  // --- Selected agent tracking (for per-agent ProcessingIndicator message) ---
  const [selectedAgentExecutionId, setSelectedAgentExecutionId] = useState<string | null>(null);
  const handleSelectedAgentChange = useCallback((executionId: string | null) => {
    setSelectedAgentExecutionId(executionId);
  }, []);

  // --- Stage collapse (manual overrides + auto-collapse for Synthesis) ---
  const [stageCollapseOverrides, setStageCollapseOverrides] = useState<Map<string, boolean>>(new Map());

  // --- Auto-collapse system ---
  const [expandAllReasoning, setExpandAllReasoning] = useState(false);
  const [expandAllToolCalls, setExpandAllToolCalls] = useState(false);
  // Manual overrides: items the user has explicitly toggled
  const [manualOverrides, setManualOverrides] = useState<Set<string>>(new Set());

  // --- Animated collapse for newly completed items ---
  // When items transition from streaming to completed, they should first render
  // expanded and then smoothly collapse (300ms MUI Collapse animation).
  // Without this, items would be born in the collapsed state with no animation.
  // On initial page load (bulk REST data), items start collapsed immediately.
  const initialLoadDoneRef = useRef(false);
  const prevItemIdsRef = useRef<Set<string>>(new Set());
  const [animatingCollapseIds, setAnimatingCollapseIds] = useState<Set<string>>(new Set());

  useEffect(() => {
    const currentIds = new Set(items.map(i => i.id));

    if (!initialLoadDoneRef.current) {
      if (items.length > 0) {
        // First batch of data from REST — collapse immediately, no animation
        initialLoadDoneRef.current = true;
        prevItemIdsRef.current = currentIds;
      }
      return;
    }

    // Find newly added items that are collapsible and already terminal
    const newCollapsible = new Set<string>();
    for (const item of items) {
      if (
        !prevItemIdsRef.current.has(item.id) &&
        isFlowItemCollapsible(item) &&
        isFlowItemTerminal(item)
      ) {
        newCollapsible.add(item.id);
      }
    }

    prevItemIdsRef.current = currentIds;

    if (newCollapsible.size > 0) {
      setAnimatingCollapseIds(newCollapsible);
    }
  }, [items]);

  // Clear animating IDs after a brief delay so the MUI Collapse transition plays.
  // The 50ms gap ensures the expanded state is painted before collapsing.
  useEffect(() => {
    if (animatingCollapseIds.size === 0) return;
    const timer = setTimeout(() => setAnimatingCollapseIds(new Set()), 50);
    return () => clearTimeout(timer);
  }, [animatingCollapseIds]);

  const shouldAutoCollapse = useCallback(
    (item: FlowItem): boolean => {
      if (manualOverrides.has(item.id)) return false; // user expanded it
      if (animatingCollapseIds.has(item.id)) return false; // grace period for animation
      // Don't auto-collapse final_analysis in chat stages — it's the answer
      // the user asked for and should always be visible.
      if (item.type === FLOW_ITEM.FINAL_ANALYSIS && item.stageId && chatStageIds?.has(item.stageId)) return false;
      return isFlowItemCollapsible(item) && isFlowItemTerminal(item);
    },
    [manualOverrides, animatingCollapseIds, chatStageIds],
  );

  const toggleItemExpansion = useCallback((itemId: string) => {
    setManualOverrides((prev) => {
      const next = new Set(prev);
      if (next.has(itemId)) {
        next.delete(itemId);
      } else {
        next.add(itemId);
      }
      return next;
    });
  }, []);

  const isItemCollapsible = useCallback(
    (item: FlowItem): boolean => {
      if (item.type === FLOW_ITEM.FINAL_ANALYSIS && item.stageId && chatStageIds?.has(item.stageId)) return false;
      return isFlowItemCollapsible(item) && isFlowItemTerminal(item);
    },
    [chatStageIds],
  );

  // --- Stage grouping ---
  // Group items by stage, then append empty groups for backend stages that
  // have no items yet (e.g. synthesis stage just started). This ensures stage
  // separators are visible immediately, and the ProcessingIndicator appears
  // under the correct stage instead of the previous one.
  const stageGroups = useMemo(() => {
    const groupsFromItems = groupFlowItemsByStage(items, stages);
    const existingStageIds = new Set(groupsFromItems.map(g => g.stageId).filter(Boolean));

    const emptyGroups: StageGroup[] = [];
    for (const stage of stages) {
      if (stage.id && !existingStageIds.has(stage.id)) {
        emptyGroups.push({
          stageId: stage.id,
          stageName: stage.stage_name,
          stageIndex: stage.stage_index,
          stageType: stage.stage_type,
          stageStatus: stage.status,
          isParallel: stage.parallel_type != null && stage.parallel_type !== '' && stage.parallel_type !== 'none',
          expectedAgentCount: stage.expected_agent_count || 1,
          items: [],
        });
      }
    }

    if (emptyGroups.length === 0) return groupsFromItems;
    return [...groupsFromItems, ...emptyGroups].sort((a, b) => a.stageIndex - b.stageIndex);
  }, [items, stages]);

  // --- Stats ---
  const stats: TimelineStats = useMemo(() => getTimelineStats(items, stages), [items, stages]);

  // --- Copy ---
  const plainText = useMemo(() => flowItemsToPlainText(items), [items]);

  // --- Stage lookup (for execution overviews) ---
  const stageMap = useMemo(() => {
    const map = new Map<string, StageOverview>();
    for (const s of stages) map.set(s.id, s);
    return map;
  }, [stages]);

  // --- Streaming events grouping ---
  const streamingByStage = useMemo(() => {
    if (!streamingEvents || streamingEvents.size === 0)
      return new Map<string, Map<string, StreamingItem & { stageId?: string; executionId?: string }>>();
    const byStage = new Map<string, Map<string, StreamingItem & { stageId?: string; executionId?: string }>>();
    for (const [eventId, event] of streamingEvents) {
      const stageKey = event.stageId || '__ungrouped__';
      if (!byStage.has(stageKey)) byStage.set(stageKey, new Map());
      byStage.get(stageKey)!.set(eventId, event);
    }
    return byStage;
  }, [streamingEvents]);

  // --- Ungrouped streaming entries (for Virtuoso footer) ---
  const ungroupedStreamingEntries = useMemo(() => {
    const ungrouped = streamingByStage.get('__ungrouped__');
    if (!ungrouped) return [];
    return Array.from(ungrouped.entries())
      .filter(([, streamItem]) => streamItem.eventType !== TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY);
  }, [streamingByStage]);

  // --- Processing indicator display status ---
  const showProcessingIndicator = isActive || !!chatStageInProgress;

  const displayStatus = useMemo(() => {
    let status = progressStatus || 'Processing...';

    if (chatStageInProgress && !isActive) {
      status = 'Processing...';
    }

    if (!selectedAgentExecutionId && agentProgressStatuses && agentProgressStatuses.size === 1) {
      const singleAgentStatus = agentProgressStatuses.values().next().value;
      if (singleAgentStatus) status = singleAgentStatus;
    }

    if (selectedAgentExecutionId) {
      const agentStatus = agentProgressStatuses?.get(selectedAgentExecutionId);
      if (agentStatus) {
        status = agentStatus;
      }

      const wsEntry = executionStatuses?.get(selectedAgentExecutionId);
      const isSelectedTerminal = (() => {
        if (wsEntry && TERMINAL_EXECUTION_STATUSES.has(wsEntry.status)) return true;
        for (const stage of stages) {
          const eo = stage.executions?.find(e => e.execution_id === selectedAgentExecutionId);
          if (eo && TERMINAL_EXECUTION_STATUSES.has(eo.status)) return true;
        }
        return false;
      })();

      if (isSelectedTerminal) {
        const stageId = wsEntry?.stageId
          || stages.find(s => s.executions?.some(e => e.execution_id === selectedAgentExecutionId))?.id;

        if (stageId) {
          const othersRunning =
            (executionStatuses ? Array.from(executionStatuses.entries()).some(
              ([id, entry]) =>
                id !== selectedAgentExecutionId &&
                entry.stageId === stageId &&
                !TERMINAL_EXECUTION_STATUSES.has(entry.status),
            ) : false) ||
            (stages.find(s => s.id === stageId)?.executions?.some(
              e => e.execution_id !== selectedAgentExecutionId &&
                !TERMINAL_EXECUTION_STATUSES.has(e.status),
            ) ?? false);

          if (othersRunning) {
            status = 'Waiting for other agents...';
          }
        }
      }
    }

    return status;
  }, [progressStatus, chatStageInProgress, isActive, selectedAgentExecutionId, agentProgressStatuses, executionStatuses, stages]);

  if (items.length === 0 && (!streamingEvents || streamingEvents.size === 0)) {
    // Session is active but no timeline items have arrived yet — show the
    // same pulsing ring spinner used by SessionDetailPage so there is no
    // jarring visual gap between "Initializing investigation..." and the
    // first real data appearing with an "Investigating..." progress status.
    if (isActive) {
      return <InitializingSpinner />;
    }
    return (
      <Box sx={{ textAlign: 'center', py: 6 }}>
        <Typography variant="body2" color="text.secondary">
          No reasoning steps available for this session
        </Typography>
      </Box>
    );
  }

  return (
    <Card>
      {/* Card header with chain ID, expand/collapse, and copy */}
      <CardContent sx={{ pb: 0, bgcolor: 'grey.50', borderBottom: 1, borderColor: 'divider' }}>
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            flexWrap: 'wrap',
            gap: 1,
          }}
        >
          <Typography variant="h6" color="primary.main">
            Chain: {chainId || '—'}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Button
              variant="outlined"
              size="small"
              startIcon={expandAllReasoning ? <ExpandLess /> : <ExpandMore />}
              onClick={() => {
                setExpandAllReasoning((v) => !v);
                setManualOverrides(new Set());
              }}
            >
              {expandAllReasoning ? 'Collapse All Reasoning' : 'Expand All Reasoning'}
            </Button>
            <Button
              variant="outlined"
              size="small"
              startIcon={expandAllToolCalls ? <ExpandLess /> : <ExpandMore />}
              onClick={() => setExpandAllToolCalls((v) => !v)}
            >
              {expandAllToolCalls ? 'Collapse All Tools' : 'Expand All Tools'}
            </Button>
            <CopyButton
              text={plainText}
              variant="button"
              buttonVariant="outlined"
              size="small"
              label="Copy Chat Flow"
            />
          </Box>
        </Box>

        {/* Stats chips bar */}
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.75, mt: 1.5, alignItems: 'center' }}>
          {stats.totalStages > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.totalStages} stages`}
              color="primary"
            />
          )}
          {stats.completedStages > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.completedStages} completed`}
              color="success"
            />
          )}
          {stats.failedStages > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.failedStages} failed`}
              color="error"
            />
          )}
          {stats.toolCallCount > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.successfulToolCalls ?? stats.toolCallCount}/${stats.toolCallCount} tool calls`}
              color={stats.successfulToolCalls === stats.toolCallCount ? 'success' : 'warning'}
            />
          )}
          {stats.thoughtCount > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.thoughtCount} thoughts`}
            />
          )}
          {stats.finalAnswerCount > 0 && (
            <Chip
              size="small"
              variant="outlined"
              label={`${stats.finalAnswerCount} analyses`}
              color="success"
            />
          )}
        </Box>
      </CardContent>

      {/* Blue "AI Reasoning Flow" bar */}
      <Box
        sx={{
          bgcolor: '#e3f2fd',
          py: 1.5,
          px: 3,
          mt: 1,
          borderTop: '2px solid #1976d2',
          borderBottom: '1px solid #bbdefb',
        }}
      >
        <Typography
          variant="subtitle2"
          sx={{
            fontWeight: 600,
            color: '#1565c0',
            fontSize: '0.9rem',
            letterSpacing: 0.3,
          }}
        >
          💬 AI Reasoning Flow
        </Typography>
      </Box>

      {/* Content area */}
      <Box sx={{ px: 3, pt: 3, pb: 5, bgcolor: 'white', minHeight: 200 }} data-autoscroll-container>
        {stageGroups.map((group) => {
          const isCollapsed = stageCollapseOverrides.has(group.stageId)
            ? stageCollapseOverrides.get(group.stageId)!
            : shouldAutoCollapseStage(group, isActive);
          const stageStreamingMap = streamingByStage.get(group.stageId);

          return (
            <Box key={group.stageId || `group-${group.stageIndex}`}>
              {group.stageId && (
                <StageSeparator
                  item={{
                    id: `stage-sep-${group.stageId}`,
                    type: 'stage_separator',
                    stageId: group.stageId,
                    content: group.stageName,
                    metadata: {
                      stage_index: group.stageIndex,
                      stage_type: group.stageType,
                      stage_status: group.stageStatus,
                    },
                    status: group.stageStatus,
                    timestamp: '',
                    sequenceNumber: 0,
                  }}
                  isCollapsed={isCollapsed}
                  onToggleCollapse={() => {
                    setStageCollapseOverrides((prev) => {
                      const next = new Map(prev);
                      next.set(group.stageId, !isCollapsed);
                      return next;
                    });
                  }}
                />
              )}

              <Collapse in={!isCollapsed} timeout={400}>
                <StageContent
                  items={group.items}
                  stageId={group.stageId}
                  executionOverviews={stageMap.get(group.stageId)?.executions}
                  streamingEvents={stageStreamingMap}
                  shouldAutoCollapse={shouldAutoCollapse}
                  onToggleItemExpansion={toggleItemExpansion}
                  expandAllReasoning={expandAllReasoning}
                  expandAllToolCalls={expandAllToolCalls}
                  isItemCollapsible={isItemCollapsible}
                  agentProgressStatuses={agentProgressStatuses}
                  executionStatuses={executionStatuses}
                  subAgentStreamingEvents={subAgentStreamingEvents}
                  subAgentExecutionStatuses={subAgentExecutionStatuses}
                  subAgentProgressStatuses={subAgentProgressStatuses}
                  onSelectedAgentChange={handleSelectedAgentChange}
                />
              </Collapse>
            </Box>
          );
        })}

        {/* Ungrouped streaming events (no stageId) */}
        {ungroupedStreamingEntries.map(([eventId, streamItem]) => (
          <StreamingContentRenderer key={eventId} item={streamItem} />
        ))}

        {/* Processing indicator */}
        {showProcessingIndicator && <ProcessingIndicator message={displayStatus} />}
      </Box>
    </Card>
  );
}
