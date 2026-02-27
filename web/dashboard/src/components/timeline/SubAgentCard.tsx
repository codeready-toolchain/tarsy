import React, { useState, useEffect } from 'react';
import { Box, Typography, Chip, Collapse, IconButton, Alert, LinearProgress, alpha, keyframes } from '@mui/material';
import {
  ExpandMore,
  ExpandLess,
  Hub,
} from '@mui/icons-material';

const pulse = keyframes`
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.4; transform: scale(0.85); }
`;
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
  getStageStatusColor,
  getStageStatusDisplayName,
} from '../trace/traceHelpers';

interface SubAgentCardProps {
  executionOverview?: ExecutionOverview;
  items: FlowItem[];
  streamingEvents?: Array<[string, StreamingItem]>;
  executionStatus?: { status: string; stageId: string; agentIndex: number };
  progressStatus?: string;
  fallbackAgentName?: string;
  fallbackTask?: string;
  shouldAutoCollapse?: (item: FlowItem) => boolean;
  onToggleItemExpansion?: (item: FlowItem) => void;
  expandAllReasoning?: boolean;
  expandAllToolCalls?: boolean;
  isItemCollapsible?: (item: FlowItem) => boolean;
}

const SubAgentCard: React.FC<SubAgentCardProps> = ({
  executionOverview,
  items,
  streamingEvents = [],
  executionStatus,
  progressStatus,
  fallbackAgentName,
  fallbackTask,
  shouldAutoCollapse,
  onToggleItemExpansion,
  expandAllReasoning = false,
  expandAllToolCalls = false,
  isItemCollapsible,
}) => {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => { setExpanded(expandAllToolCalls); }, [expandAllToolCalls]);

  const eo = executionOverview;
  const effectiveStatus = executionStatus?.status || eo?.status || EXECUTION_STATUS.STARTED;
  const agentName = eo?.agent_name || fallbackAgentName || 'Sub-Agent';
  const task = eo?.task || fallbackTask;
  const isFailed = FAILED_EXECUTION_STATUSES.has(effectiveStatus);
  const isCancelled = CANCELLED_EXECUTION_STATUSES.has(effectiveStatus);
  const isRunning = !TERMINAL_EXECUTION_STATUSES.has(effectiveStatus);

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
      sx={(theme) => ({
        ml: 4, my: 1, mr: 1,
        border: '2px solid',
        borderColor: alpha(theme.palette.secondary.main, 0.5),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette.secondary.main, 0.08),
        boxShadow: `0 1px 3px ${alpha(theme.palette.common.black, 0.08)}`,
        overflow: 'hidden',
      })}
    >
      {isRunning && (
        <LinearProgress variant="indeterminate" sx={{ height: 2, borderRadius: 0 }} />
      )}

      {/* Header — always visible */}
      <Box
        onClick={() => hasContent && setExpanded(!expanded)}
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75, minWidth: 0,
          cursor: hasContent ? 'pointer' : 'default',
          transition: 'background-color 0.2s ease',
          '&:hover': hasContent ? { bgcolor: alpha(theme.palette.secondary.main, 0.12) } : {},
        })}
      >
        <Hub sx={(theme) => ({
          fontSize: 18, flexShrink: 0,
          color: theme.palette.secondary.main,
          ...(isRunning && { animation: `${pulse} 1.5s ease-in-out infinite` }),
        })} />
        <Typography
          variant="body2"
          sx={(theme) => ({ fontWeight: 700, fontSize: '0.9rem', color: theme.palette.secondary.main, whiteSpace: 'nowrap', flexShrink: 0 })}
        >
          Sub-agent
        </Typography>
        <Typography
          variant="body2"
          sx={(theme) => ({ fontWeight: 400, fontSize: '0.9rem', color: theme.palette.secondary.main, whiteSpace: 'nowrap', flexShrink: 0 })}
        >
          {agentName}
        </Typography>
        <Box sx={{ flex: 1 }} />
        {!isRunning && eo?.duration_ms != null && (
          <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.75rem', flexShrink: 0 }}>
            {formatDurationMs(eo.duration_ms)}
          </Typography>
        )}
        <IconButton size="small" sx={{ p: 0.25, flexShrink: 0 }}>
          {expanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      {/* Expanded content */}
      <Collapse in={expanded} timeout={300}>
        <Box sx={{ borderTop: 1, borderColor: 'divider' }}>
          {/* Info bar with badge + tokens */}
          <Box sx={(theme) => ({
            px: 1.5, py: 0.75,
            bgcolor: alpha(theme.palette.secondary.main, 0.04),
            display: 'flex', alignItems: 'center', gap: 1,
          })}>
            <Chip
              label={getStageStatusDisplayName(effectiveStatus)}
              size="small"
              color={getStageStatusColor(effectiveStatus)}
              sx={{ height: 16, fontSize: '0.6rem' }}
            />
            {progressStatus && isRunning && (
              <Chip
                label={progressStatus}
                size="small"
                color="info"
                variant="outlined"
                sx={{ height: 16, fontSize: '0.6rem', fontStyle: 'italic' }}
              />
            )}
            {tokenData && (
              <TokenUsageDisplay tokenData={tokenData} variant="inline" size="small" />
            )}
          </Box>

          {/* Timeline */}
          <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5 }}>
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
