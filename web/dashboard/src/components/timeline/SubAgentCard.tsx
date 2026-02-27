import React, { useState } from 'react';
import { Box, Typography, Chip, Collapse, IconButton, Alert, alpha } from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
  AccountTree,
} from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';
import type { ExecutionOverview } from '../../types/session';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import TimelineItem from './TimelineItem';
import { formatDurationMs } from '../../utils/format';
import {
  EXECUTION_STATUS,
  TERMINAL_EXECUTION_STATUSES,
  FAILED_EXECUTION_STATUSES,
  CANCELLED_EXECUTION_STATUSES,
} from '../../constants/sessionStatus';
import {
  getStageStatusIcon,
  getStageStatusColor,
  getStageStatusDisplayName,
} from '../trace/traceHelpers';

interface SubAgentCardProps {
  executionOverview?: ExecutionOverview;
  items: FlowItem[];
  streamingEvents?: Array<[string, StreamingItem]>;
  executionStatus?: { status: string; stageId: string; agentIndex: number };
  progressStatus?: string;
  shouldAutoCollapse?: (item: FlowItem) => boolean;
  onToggleItemExpansion?: (item: FlowItem) => void;
  expandAllReasoning?: boolean;
  expandAllToolCalls?: boolean;
  isItemCollapsible?: (item: FlowItem) => boolean;
}

const getBorderColor = (status: string): string => {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED: return 'success.main';
    case EXECUTION_STATUS.FAILED:
    case EXECUTION_STATUS.TIMED_OUT: return 'error.main';
    case EXECUTION_STATUS.CANCELLED: return 'grey.400';
    default: return 'info.main';
  }
};

const SubAgentCard: React.FC<SubAgentCardProps> = ({
  executionOverview,
  items,
  streamingEvents = [],
  executionStatus,
  progressStatus,
  shouldAutoCollapse,
  onToggleItemExpansion,
  expandAllReasoning = false,
  expandAllToolCalls = false,
  isItemCollapsible,
}) => {
  const [expanded, setExpanded] = useState(false);

  const eo = executionOverview;
  const effectiveStatus = executionStatus?.status || eo?.status || EXECUTION_STATUS.STARTED;
  const agentName = eo?.agent_name || 'Sub-Agent';
  const task = eo?.task;
  const isFailed = FAILED_EXECUTION_STATUSES.has(effectiveStatus);
  const isCancelled = CANCELLED_EXECUTION_STATUSES.has(effectiveStatus);
  const isRunning = !TERMINAL_EXECUTION_STATUSES.has(effectiveStatus);

  // Dedup: exclude streaming events whose ID matches a completed item.
  // This guards against stale streaming entries that survive when a
  // timeline_event.completed WS payload is truncated (parent_execution_id
  // stripped) and cleanup targets the wrong streaming map.
  const completedIds = React.useMemo(() => new Set(items.map((i) => i.id)), [items]);
  const dedupedStreaming = React.useMemo(
    () => streamingEvents.filter(([key]) => !completedIds.has(key)),
    [streamingEvents, completedIds],
  );

  const hasContent = items.length > 0 || dedupedStreaming.length > 0;

  const tokenData = eo && (eo.input_tokens > 0 || eo.output_tokens > 0)
    ? { input_tokens: eo.input_tokens, output_tokens: eo.output_tokens, total_tokens: eo.total_tokens }
    : null;

  return (
    <Box
      sx={{
        my: 1.5,
        borderLeft: 3,
        borderColor: getBorderColor(effectiveStatus),
        borderRadius: 1,
        bgcolor: (theme) => alpha(theme.palette.grey[500], 0.04),
        border: 1,
        borderRightColor: 'divider',
        borderTopColor: 'divider',
        borderBottomColor: 'divider',
        overflow: 'hidden',
      }}
    >
      {/* Collapsed header — always visible */}
      <Box
        onClick={() => hasContent && setExpanded(!expanded)}
        sx={{
          px: 2,
          py: 1.5,
          display: 'flex',
          alignItems: 'center',
          gap: 1,
          cursor: hasContent ? 'pointer' : 'default',
          '&:hover': hasContent ? { bgcolor: (theme) => alpha(theme.palette.grey[500], 0.06) } : {},
        }}
      >
        <AccountTree sx={{ fontSize: 18, color: 'text.secondary' }} />

        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
            <Typography variant="body2" fontWeight={600} sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
              {getStageStatusIcon(effectiveStatus)}
              {agentName}
            </Typography>
            <Chip
              label={getStageStatusDisplayName(effectiveStatus)}
              size="small"
              color={getStageStatusColor(effectiveStatus)}
              sx={{ height: 18, fontSize: '0.65rem' }}
            />
            {progressStatus && isRunning && (
              <Chip
                label={progressStatus}
                size="small"
                color="info"
                variant="outlined"
                sx={{ height: 18, fontSize: '0.65rem', fontStyle: 'italic' }}
              />
            )}
            {eo?.duration_ms != null && (
              <Typography variant="caption" color="text.secondary">
                {formatDurationMs(eo.duration_ms)}
              </Typography>
            )}
            {tokenData && (
              <TokenUsageDisplay tokenData={tokenData} variant="inline" size="small" />
            )}
          </Box>

          {task && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{
                display: 'block',
                mt: 0.5,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: expanded ? 'normal' : 'nowrap',
                maxWidth: expanded ? 'none' : '100%',
              }}
            >
              {task}
            </Typography>
          )}
        </Box>

        {hasContent && (
          <IconButton size="small" sx={{ ml: 'auto' }}>
            {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
          </IconButton>
        )}
      </Box>

      {/* Expanded content — sub-agent's own timeline */}
      <Collapse in={expanded} timeout={300}>
        <Box sx={{ px: 2, pb: 2, borderTop: 1, borderColor: 'divider' }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0, pt: 1 }}>
            {items.map((item) => (
              <TimelineItem
                key={item.id}
                item={item}
                isAutoCollapsed={shouldAutoCollapse ? shouldAutoCollapse(item) : false}
                onToggleAutoCollapse={onToggleItemExpansion ? () => onToggleItemExpansion(item) : undefined}
                expandAll={expandAllReasoning}
                expandAllToolCalls={expandAllToolCalls}
                isCollapsible={isItemCollapsible ? isItemCollapsible(item) : false}
              />
            ))}

            {dedupedStreaming.map(([key, streamItem]) => (
              <StreamingContentRenderer key={key} item={streamItem} />
            ))}

            {!hasContent && !isRunning && (
              <Typography variant="body2" color="text.secondary" sx={{ textAlign: 'center', py: 2 }}>
                No reasoning steps available
              </Typography>
            )}

            {isFailed && eo?.error_message && (
              <Alert severity="error" sx={{ mt: 1 }}>
                <Typography variant="body2">
                  <strong>Failed</strong>: {eo.error_message}
                </Typography>
              </Alert>
            )}

            {isCancelled && (
              <Alert severity="info" sx={{ mt: 1, bgcolor: 'grey.100', '& .MuiAlert-icon': { color: 'text.secondary' } }}>
                <Typography variant="body2" color="text.secondary">
                  <strong>Cancelled</strong>
                  {eo?.error_message ? `: ${eo.error_message}` : ''}
                </Typography>
              </Alert>
            )}
          </Box>
        </Box>
      </Collapse>
    </Box>
  );
};

export default SubAgentCard;
