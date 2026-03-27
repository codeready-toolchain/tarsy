import { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import {
  Box,
  Typography,
  Collapse,
  Card,
  CardContent,
  Button,
  IconButton,
  Tooltip,
} from '@mui/material';
import { alpha } from '@mui/material/styles';
import {
  ExpandMore,
  ExpandLess,
  UnfoldMore,
  KeyboardDoubleArrowDown,
  Forum,
} from '@mui/icons-material';
import type { FlowItem, StageGroup } from '../../utils/timelineParser';
import type { StageOverview } from '../../types/session';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import {
  FLOW_ITEM,
  groupFlowItemsByStage,
  isFlowItemCollapsible,
  isFlowItemTerminal,
  flowItemsToPlainText,
} from '../../utils/timelineParser';
import { TIMELINE_EVENT_TYPES, STAGE_TYPE, COLLAPSIBLE_STAGE_TYPES } from '../../constants/eventTypes';
import StageSeparator from '../timeline/StageSeparator';
import StageContent from '../timeline/StageContent';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import ProcessingIndicator from '../streaming/ProcessingIndicator';
import CopyButton from '../shared/CopyButton';
import { SessionSearchBar } from './SessionSearchBar';
import InitializingSpinner from '../common/InitializingSpinner';
import { TERMINAL_EXECUTION_STATUSES, SCORING_STATUS_MESSAGE } from '../../constants/sessionStatus';

/**
 * Synthesis stages auto-collapse only when the session is no longer active
 * AND the stage itself has reached a terminal status.
 * While the session is streaming, synthesis stays expanded so the user
 * can watch the reasoning flow in real time.
 */
function shouldAutoCollapseStage(group: StageGroup, isSessionActive: boolean): boolean {
  const isCollapsible = !!group.stageType && COLLAPSIBLE_STAGE_TYPES.has(group.stageType);
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
  /** Live scoring status from session.score_updated WS events (e.g. "memorizing") */
  scoringStatus?: string | null;
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
  /** Search bar props (only rendered when onSearchChange is provided, i.e. terminal sessions) */
  searchMatchCount?: number;
  currentSearchMatchIndex?: number;
  onSearchChange?: (term: string) => void;
  onNextSearchMatch?: () => void;
  onPrevSearchMatch?: () => void;
  /** Whether a chat stage is currently in progress (session may be terminal) */
  chatStageInProgress?: boolean;
  /** Set of stage IDs that are chat stages (for suppressing auto-collapse) */
  chatStageIds?: Set<string>;
  /** Search term for in-session search (highlights + auto-expand) */
  searchTerm?: string;
  /** Jump-to-summary: shows button in header when provided */
  onJumpToSummary?: () => void;
  /** Whether the summary is an executive summary (true) or final analysis (false) */
  hasExecutiveSummary?: boolean;
  /** Start with the entire timeline collapsed (for terminal sessions opened directly) */
  defaultCollapsed?: boolean;
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
  scoringStatus,
  streamingEvents,
  agentProgressStatuses,
  executionStatuses,
  subAgentStreamingEvents,
  subAgentExecutionStatuses,
  subAgentProgressStatuses,
  searchMatchCount,
  currentSearchMatchIndex,
  onSearchChange,
  onNextSearchMatch,
  onPrevSearchMatch,
  chatStageInProgress,
  chatStageIds,
  searchTerm,
  onJumpToSummary,
  hasExecutiveSummary,
  defaultCollapsed,
}: ConversationTimelineProps) {
  // --- Whole-timeline collapse (for terminal sessions opened directly) ---
  const [timelineCollapsed, setTimelineCollapsed] = useState(defaultCollapsed ?? false);

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

  const shouldAutoCollapse = useCallback(
    (item: FlowItem): boolean => {
      if (manualOverrides.has(item.id)) return false; // user expanded it
      // Don't auto-collapse final_analysis in chat stages — it's the answer
      // the user asked for and should always be visible.
      if (item.type === FLOW_ITEM.FINAL_ANALYSIS && item.stageId && chatStageIds?.has(item.stageId)) return false;
      return isFlowItemCollapsible(item) && isFlowItemTerminal(item);
    },
    [manualOverrides, chatStageIds],
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

  // --- Search-driven auto-expand ---
  const searchExpandedStagesRef = useRef<Set<string>>(new Set());
  const searchExpandedItemsRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    // Revert previously search-expanded stages/items
    if (searchExpandedStagesRef.current.size > 0) {
      setStageCollapseOverrides((prev) => {
        const next = new Map(prev);
        for (const id of searchExpandedStagesRef.current) next.delete(id);
        return next;
      });
      searchExpandedStagesRef.current = new Set();
    }
    if (searchExpandedItemsRef.current.size > 0) {
      setManualOverrides((prev) => {
        const next = new Set(prev);
        for (const id of searchExpandedItemsRef.current) next.delete(id);
        return next;
      });
      searchExpandedItemsRef.current = new Set();
    }

    if (!searchTerm?.trim()) return;

    const lower = searchTerm.toLowerCase();
    const matchingStageIds = new Set<string>();
    const matchingItemIds = new Set<string>();

    for (const item of items) {
      if (item.content && item.content.toLowerCase().includes(lower)) {
        if (item.stageId) matchingStageIds.add(item.stageId);
        if (isFlowItemCollapsible(item) && isFlowItemTerminal(item)) {
          matchingItemIds.add(item.id);
        }
      }
    }

    if (matchingStageIds.size > 0) {
      setStageCollapseOverrides((prev) => {
        const next = new Map(prev);
        for (const id of matchingStageIds) next.set(id, false);
        return next;
      });
      searchExpandedStagesRef.current = matchingStageIds;
    }
    if (matchingItemIds.size > 0) {
      setManualOverrides((prev) => {
        const next = new Set(prev);
        for (const id of matchingItemIds) next.add(id);
        return next;
      });
      searchExpandedItemsRef.current = matchingItemIds;
    }
  }, [searchTerm, items]);

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

  // --- Ungrouped streaming entries ---
  const ungroupedStreamingEntries = useMemo(() => {
    const ungrouped = streamingByStage.get('__ungrouped__');
    if (!ungrouped) return [];
    return Array.from(ungrouped.entries())
      .filter(([, streamItem]) => streamItem.eventType !== TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY);
  }, [streamingByStage]);

  // --- Processing indicator display status ---
  const scoringInProgress = useMemo(
    () => stageGroups.some(g => g.stageType === STAGE_TYPE.SCORING && !TERMINAL_EXECUTION_STATUSES.has(g.stageStatus)),
    [stageGroups],
  );
  const showProcessingIndicator = isActive || !!chatStageInProgress || scoringInProgress;

  const displayStatus = useMemo(() => {
    let status = progressStatus || 'Processing...';

    if (scoringInProgress) {
      status = (scoringStatus && scoringStatus in SCORING_STATUS_MESSAGE)
        ? SCORING_STATUS_MESSAGE[scoringStatus]
        : SCORING_STATUS_MESSAGE.in_progress;
    } else if (chatStageInProgress && !isActive) {
      status = 'Processing...';
    }

    if (!scoringInProgress && !selectedAgentExecutionId && agentProgressStatuses && agentProgressStatuses.size === 1) {
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
  }, [progressStatus, scoringStatus, scoringInProgress, chatStageInProgress, isActive, selectedAgentExecutionId, agentProgressStatuses, executionStatuses, stages]);

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
      {/* Accordion header — matches ChatPanel / FinalAnalysisCard pattern */}
      <Box
        onClick={() => setTimelineCollapsed((v) => !v)}
        sx={(theme) => ({
          p: 2.5,
          display: 'flex',
          alignItems: 'center',
          cursor: 'pointer',
          bgcolor: !timelineCollapsed
            ? alpha(theme.palette.primary.main, 0.06)
            : alpha(theme.palette.primary.main, 0.03),
          transition: 'all 0.3s ease-in-out',
          borderBottom: !timelineCollapsed ? `1px solid ${theme.palette.divider}` : 'none',
          '&:hover': {
            bgcolor: alpha(theme.palette.primary.main, 0.08),
          },
        })}
      >
        <Box
          sx={(theme) => ({
            width: 40,
            height: 40,
            borderRadius: '50%',
            bgcolor: alpha(theme.palette.primary.main, 0.15),
            border: '2px solid',
            borderColor: 'primary.main',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            mr: 2,
            flexShrink: 0,
          })}
        >
          <Forum sx={{ fontSize: 24, color: 'primary.main' }} />
        </Box>

        <Box sx={{ flex: 1 }}>
          <Typography variant="h6" sx={{ fontWeight: 600, fontSize: '1rem', color: 'text.primary' }}>
            AI Reasoning Flow
          </Typography>
          <Typography variant="body2" sx={{ color: 'text.secondary', fontSize: '0.85rem' }}>
            {timelineCollapsed
              ? `${stageGroups.length} ${stageGroups.length === 1 ? 'stage' : 'stages'} · Click to expand`
              : `${stageGroups.length} ${stageGroups.length === 1 ? 'stage' : 'stages'}`}
          </Typography>
        </Box>

        <IconButton
          size="small"
          onClick={(e) => {
            e.stopPropagation();
            setTimelineCollapsed((v) => !v);
          }}
          sx={(theme) => ({
            bgcolor: alpha(theme.palette.primary.main, 0.12),
            '&:hover': { bgcolor: alpha(theme.palette.primary.main, 0.22) },
          })}
        >
          {timelineCollapsed ? <UnfoldMore /> : <ExpandLess />}
        </IconButton>
      </Box>

      <Collapse in={!timelineCollapsed} timeout={400}>
        {/* Toolbar: search + expand/collapse + copy */}
        <CardContent sx={{ pb: 1, pt: 1, bgcolor: 'action.hover', borderBottom: 1, borderColor: 'divider' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            {onSearchChange && (
              <SessionSearchBar
                matchCount={searchMatchCount ?? 0}
                currentMatchIndex={currentSearchMatchIndex ?? 0}
                onSearchChange={onSearchChange}
                onNextMatch={onNextSearchMatch ?? (() => {})}
                onPrevMatch={onPrevSearchMatch ?? (() => {})}
                variant="inline"
              />
            )}
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexShrink: 0, ml: 'auto' }}>
              {onJumpToSummary && (
                <Tooltip title={hasExecutiveSummary ? 'Jump to Summary' : 'Jump to Final Analysis'}>
                  <Button
                    variant="outlined"
                    size="small"
                    onClick={onJumpToSummary}
                    endIcon={<KeyboardDoubleArrowDown sx={{ fontSize: '0.95rem' }} />}
                    sx={{ textTransform: 'none', whiteSpace: 'nowrap', color: 'primary.main' }}
                  >
                    {hasExecutiveSummary ? 'Summary' : 'Final Analysis'}
                  </Button>
                </Tooltip>
              )}
              <Button
                variant="outlined"
                size="small"
                startIcon={expandAllReasoning ? <ExpandLess /> : <ExpandMore />}
                onClick={() => {
                  setExpandAllReasoning((v) => !v);
                  setManualOverrides(new Set());
                }}
                sx={{ textTransform: 'none', whiteSpace: 'nowrap' }}
              >
                {expandAllReasoning ? 'Collapse Reasoning' : 'Expand Reasoning'}
              </Button>
              <Button
                variant="outlined"
                size="small"
                startIcon={expandAllToolCalls ? <ExpandLess /> : <ExpandMore />}
                onClick={() => setExpandAllToolCalls((v) => !v)}
                sx={{ textTransform: 'none', whiteSpace: 'nowrap' }}
              >
                {expandAllToolCalls ? 'Collapse Tools' : 'Expand Tools'}
              </Button>
              <CopyButton
                text={plainText}
                variant="icon"
                size="small"
                tooltip="Copy chat flow"
              />
            </Box>
          </Box>
        </CardContent>

        {/* Content area */}
        <Box sx={{ px: 3, pt: 3, pb: 5, bgcolor: 'background.paper', minHeight: 200 }} data-autoscroll-container>
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
                  searchTerm={searchTerm}
                />
              </Collapse>
            </Box>
          );
        })}

        {/* Ungrouped streaming events (no stageId) */}
        {ungroupedStreamingEntries.map(([eventId, streamItem]) => (
          <Collapse key={eventId} in={!streamItem.collapsing} timeout={300}>
            <StreamingContentRenderer item={streamItem} />
          </Collapse>
        ))}

        {/* Processing indicator */}
        {showProcessingIndicator && <ProcessingIndicator message={displayStatus} />}
      </Box>
      </Collapse>
    </Card>
  );
}
