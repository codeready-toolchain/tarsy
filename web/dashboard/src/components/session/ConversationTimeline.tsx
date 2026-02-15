import { useState, useMemo, useCallback } from 'react';
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
  groupFlowItemsByStage,
  getTimelineStats,
  isFlowItemCollapsible,
  isFlowItemTerminal,
  flowItemsToPlainText,
} from '../../utils/timelineParser';
import { TIMELINE_EVENT_TYPES } from '../../constants/eventTypes';
import StageSeparator from '../timeline/StageSeparator';
import StageContent from '../timeline/StageContent';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import ProcessingIndicator from '../streaming/ProcessingIndicator';
import CopyButton from '../shared/CopyButton';

const TERMINAL_STAGE_STATUSES = new Set(['completed', 'failed', 'timed_out', 'cancelled']);

/** Synthesis stages that are terminal should auto-collapse. */
function shouldAutoCollapseStage(group: StageGroup): boolean {
  const isSynthesis = group.stageName.toLowerCase().includes('synthesis');
  return isSynthesis && TERMINAL_STAGE_STATUSES.has(group.stageStatus);
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
  /** Chain ID for the header display */
  chainId?: string;
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
  chainId,
}: ConversationTimelineProps) {
  // --- Stage collapse (manual overrides + auto-collapse for Synthesis) ---
  const [stageCollapseOverrides, setStageCollapseOverrides] = useState<Map<string, boolean>>(new Map());

  // --- Auto-collapse system ---
  const [expandAllReasoning, setExpandAllReasoning] = useState(false);
  // Manual overrides: items the user has explicitly toggled
  const [manualOverrides, setManualOverrides] = useState<Set<string>>(new Set());

  const shouldAutoCollapse = useCallback(
    (item: FlowItem): boolean => {
      if (manualOverrides.has(item.id)) return false; // user expanded it
      return isFlowItemCollapsible(item) && isFlowItemTerminal(item);
    },
    [manualOverrides],
  );

  const toggleItemExpansion = useCallback((item: FlowItem) => {
    setManualOverrides((prev) => {
      const next = new Set(prev);
      if (next.has(item.id)) {
        next.delete(item.id);
      } else {
        next.add(item.id);
      }
      return next;
    });
  }, []);

  const isItemCollapsible = useCallback(
    (item: FlowItem): boolean => isFlowItemCollapsible(item) && isFlowItemTerminal(item),
    [],
  );

  // --- Stage grouping ---
  const stageGroups = useMemo(() => groupFlowItemsByStage(items, stages), [items, stages]);

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

  if (items.length === 0 && (!streamingEvents || streamingEvents.size === 0)) {
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
      <Box sx={{ p: 3, bgcolor: 'white', minHeight: 200 }} data-autoscroll-container>
        {stageGroups.map((group, index) => {
          // Manual override takes precedence, otherwise auto-collapse Synthesis stages
          const isCollapsed = stageCollapseOverrides.has(group.stageId)
            ? stageCollapseOverrides.get(group.stageId)!
            : shouldAutoCollapseStage(group);

          // Get streaming events for this stage
          const stageStreamingMap = streamingByStage.get(group.stageId);

          return (
            <Box key={group.stageId ? `${group.stageId}-${index}` : `group-${index}`}>
              {/* Stage separator */}
              {group.stageId && (
                <StageSeparator
                  item={{
                    id: `stage-sep-${group.stageId}`,
                    type: 'stage_separator',
                    stageId: group.stageId,
                    content: group.stageName,
                    metadata: {
                      stage_index: group.stageIndex,
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

              {/* Stage items (collapsible) */}
              <Collapse in={!isCollapsed} timeout={400}>
                <StageContent
                  items={group.items}
                  stageId={group.stageId}
                  executionOverviews={stageMap.get(group.stageId)?.executions}
                  streamingEvents={stageStreamingMap}
                  shouldAutoCollapse={shouldAutoCollapse}
                  onToggleItemExpansion={toggleItemExpansion}
                  expandAllReasoning={expandAllReasoning}
                  isItemCollapsible={isItemCollapsible}
                  agentProgressStatuses={agentProgressStatuses}
                />
              </Collapse>
            </Box>
          );
        })}

        {/* Ungrouped streaming events (no stageId), excluding executive_summary */}
        {streamingByStage.get('__ungrouped__') &&
          Array.from(streamingByStage.get('__ungrouped__')!.entries())
            .filter(([, streamItem]) => streamItem.eventType !== TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY)
            .map(([eventId, streamItem]) => (
              <StreamingContentRenderer key={eventId} item={streamItem} />
            ))}

        {/* Processing indicator for active sessions */}
        {isActive && <ProcessingIndicator message={progressStatus || 'Processing...'} />}
      </Box>
    </Card>
  );
}
