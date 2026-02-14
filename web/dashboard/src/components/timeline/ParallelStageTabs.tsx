import React, { useState, useMemo } from 'react';
import { Box, Typography, Chip, Alert, alpha } from '@mui/material';
import {
  CheckCircle,
  Error as ErrorIcon,
  PlayArrow,
  CallSplit,
  CancelOutlined,
} from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import TimelineItem from './TimelineItem';

interface ParallelStagTabsProps {
  items: FlowItem[];
  stageId: string;
  expectedAgentCount: number;
  /** Active streaming events keyed by event_id */
  streamingEvents?: Map<string, StreamingItem & { stageId?: string; executionId?: string }>;
  // Auto-collapse system
  shouldAutoCollapse?: (item: FlowItem) => boolean;
  onToggleItemExpansion?: (item: FlowItem) => void;
  expandAllReasoning?: boolean;
  isItemCollapsible?: (item: FlowItem) => boolean;
  // Per-agent progress
  agentProgressStatuses?: Map<string, string>;
  onSelectedAgentChange?: (executionId: string | null) => void;
}

interface TabPanelProps {
  children?: React.ReactNode;
  index: number;
  value: number;
}

function TabPanel({ children, value, index, ...other }: TabPanelProps) {
  return (
    <div
      role="tabpanel"
      hidden={value !== index}
      id={`reasoning-tabpanel-${index}`}
      aria-labelledby={`reasoning-tab-${index}`}
      {...other}
    >
      {value === index && <Box sx={{ pt: 2 }}>{children}</Box>}
    </div>
  );
}

const getStatusIcon = (status: string) => {
  switch (status) {
    case 'failed':
    case 'timed_out': return <ErrorIcon fontSize="small" />;
    case 'completed': return <CheckCircle fontSize="small" />;
    case 'cancelled': return <CancelOutlined fontSize="small" />;
    default: return <PlayArrow fontSize="small" />;
  }
};

const getStatusColor = (status: string): 'default' | 'success' | 'error' | 'warning' | 'info' => {
  switch (status) {
    case 'completed': return 'success';
    case 'failed':
    case 'timed_out': return 'error';
    case 'cancelled': return 'default';
    default: return 'info';
  }
};

const getStatusLabel = (status: string) => {
  switch (status) {
    case 'completed': return 'Complete';
    case 'failed': return 'Failed';
    case 'timed_out': return 'Timed Out';
    case 'cancelled': return 'Cancelled';
    case 'started': return 'Running';
    default: return status;
  }
};

/**
 * ParallelStageTabs - renders parallel stage execution in a tabbed interface.
 * Groups flow items by execution_id and shows them in separate agent tabs.
 */
const ParallelStageTabs: React.FC<ParallelStagTabsProps> = ({
  items,
  stageId: _stageId,
  expectedAgentCount: _expectedAgentCount,
  streamingEvents,
  shouldAutoCollapse,
  onToggleItemExpansion,
  expandAllReasoning = false,
  isItemCollapsible,
  agentProgressStatuses = new Map(),
  onSelectedAgentChange,
}) => {
  const [selectedTab, setSelectedTab] = useState(0);

  // Group items by executionId
  const executions = useMemo(() => {
    const groups = new Map<string, FlowItem[]>();
    const executionOrder: string[] = [];

    for (const item of items) {
      if (item.type === 'stage_separator') continue;
      const execId = item.executionId || '__default__';
      if (!groups.has(execId)) {
        groups.set(execId, []);
        executionOrder.push(execId);
      }
      groups.get(execId)!.push(item);
    }

    return executionOrder.map((execId, index) => ({
      executionId: execId,
      index,
      items: groups.get(execId) || [],
      // Derive status from items' metadata or default to 'started'
      status: deriveExecutionStatus(groups.get(execId) || []),
    }));
  }, [items]);

  // Get streaming items grouped by execution
  const streamingByExecution = useMemo(() => {
    const byExec = new Map<string, Array<[string, StreamingItem]>>();
    if (!streamingEvents) return byExec;

    for (const [eventId, event] of streamingEvents) {
      const execId = (event as any).executionId || '__default__';
      if (!byExec.has(execId)) byExec.set(execId, []);
      byExec.get(execId)!.push([eventId, event]);
    }
    return byExec;
  }, [streamingEvents]);

  // Notify parent when selected tab changes
  React.useEffect(() => {
    if (onSelectedAgentChange && executions[selectedTab]) {
      onSelectedAgentChange(executions[selectedTab].executionId);
    }
  }, [selectedTab, executions, onSelectedAgentChange]);

  if (executions.length === 0) {
    return (
      <Alert severity="info">
        <Typography variant="body2">Waiting for parallel agent data...</Typography>
      </Alert>
    );
  }

  return (
    <Box>
      {/* Header */}
      <Box sx={{ mb: 3, pl: 4, pr: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
          <CallSplit color="secondary" fontSize="small" />
          <Typography variant="caption" color="secondary" fontWeight={600} letterSpacing={0.5}>
            PARALLEL EXECUTION
          </Typography>
          <Chip
            label={`${executions.length} agent${executions.length > 1 ? 's' : ''}`}
            size="small" color="secondary" variant="outlined"
            sx={{ height: 20, fontSize: '0.7rem' }}
          />
        </Box>

        {/* Agent Cards */}
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          {executions.map((execution, tabIndex) => {
            const isSelected = selectedTab === tabIndex;
            const statusColor = getStatusColor(execution.status);
            const statusIcon = getStatusIcon(execution.status);
            // Derive label from stage name + index, or fallback to "Agent N"
            const stageName = execution.items[0]?.metadata?.stage_name as string | undefined;
            const label = stageName ? `${stageName} #${tabIndex + 1}` : `Agent ${tabIndex + 1}`;
            const progressStatus = agentProgressStatuses.get(execution.executionId);
            const isTerminalProgress = !progressStatus || ['Completed', 'Failed', 'Cancelled'].includes(progressStatus);

            // Derive token data from items metadata if available
            const tokenData = deriveTokenData(execution.items);

            return (
              <Box
                key={execution.executionId}
                onClick={() => setSelectedTab(tabIndex)}
                sx={{
                  flex: 1, minWidth: 180, p: 1.5,
                  border: 2, borderColor: isSelected ? 'secondary.main' : 'divider',
                  borderRadius: 1.5,
                  backgroundColor: isSelected ? (theme) => alpha(theme.palette.secondary.main, 0.08) : 'background.paper',
                  cursor: 'pointer', transition: 'all 0.2s',
                  '&:hover': {
                    borderColor: isSelected ? 'secondary.main' : (theme) => alpha(theme.palette.secondary.main, 0.4),
                    backgroundColor: isSelected ? (theme) => alpha(theme.palette.secondary.main, 0.08) : (theme) => alpha(theme.palette.secondary.main, 0.03),
                  },
                }}
              >
                <Box display="flex" alignItems="center" justifyContent="space-between" mb={0.5}>
                  <Typography variant="body2" fontWeight={600} sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
                    {statusIcon}
                    {label}
                  </Typography>
                </Box>
                <Box display="flex" alignItems="center" gap={1} flexWrap="wrap">
                  <Chip
                    label={getStatusLabel(execution.status)}
                    size="small" color={statusColor}
                    sx={{ height: 18, fontSize: '0.65rem' }}
                  />
                  {progressStatus && !isTerminalProgress && (
                    <Chip
                      label={progressStatus}
                      size="small" color="info" variant="outlined"
                      sx={{ height: 18, fontSize: '0.65rem', fontStyle: 'italic' }}
                    />
                  )}
                </Box>
                {tokenData && (
                  <Box mt={1} display="flex" alignItems="center" gap={0.5}>
                    <Typography variant="body2" sx={{ fontSize: '0.9rem' }}>🪙</Typography>
                    <TokenUsageDisplay tokenData={tokenData} variant="inline" size="small" />
                    <Typography variant="caption" color="text.secondary">tokens</Typography>
                  </Box>
                )}
              </Box>
            );
          })}
        </Box>
      </Box>

      {/* Tab panels */}
      {executions.map((execution, index) => {
        const executionStreamingItems = streamingByExecution.get(execution.executionId) || [];
        const hasDbItems = execution.items.length > 0;
        const hasStreamingItems = executionStreamingItems.length > 0;
        const isFailed = execution.status === 'failed' || execution.status === 'timed_out';

        return (
          <TabPanel key={execution.executionId} value={selectedTab} index={index}>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
              {execution.items.map((item) => (
                <TimelineItem
                  key={item.id}
                  item={item}
                  isAutoCollapsed={shouldAutoCollapse ? shouldAutoCollapse(item) : false}
                  onToggleAutoCollapse={onToggleItemExpansion ? () => onToggleItemExpansion(item) : undefined}
                  expandAll={expandAllReasoning}
                  isCollapsible={isItemCollapsible ? isItemCollapsible(item) : false}
                />
              ))}

              {executionStreamingItems.map(([key, streamItem]) => (
                <StreamingContentRenderer key={key} item={streamItem} />
              ))}

              {!hasDbItems && !hasStreamingItems && (
                <Typography variant="body2" color="text.secondary" sx={{ textAlign: 'center', py: 4 }}>
                  {executions.length > 0
                    ? 'No parallel agent reasoning flows found'
                    : 'No reasoning steps available for this agent'}
                </Typography>
              )}

              {isFailed && (
                <Alert severity="error" sx={{ mt: 2 }}>
                  <Typography variant="body2">
                    <strong>Execution Failed</strong>
                    {(() => {
                      // Try to extract error message from items
                      const errorItem = execution.items.find(i => i.type === 'error');
                      const errMsg = (errorItem?.content) || (execution.items[execution.items.length - 1]?.metadata?.error_message as string);
                      return errMsg ? `: ${errMsg}` : '';
                    })()}
                  </Typography>
                </Alert>
              )}
            </Box>
          </TabPanel>
        );
      })}
    </Box>
  );
};

// Helper: derive execution status from items
function deriveExecutionStatus(items: FlowItem[]): string {
  if (items.length === 0) return 'started';
  const hasError = items.some(i => i.type === 'error');
  const allCompleted = items.every(i => i.status === 'completed' || i.status === 'failed');
  if (hasError) return 'failed';
  if (allCompleted && items.length > 0) return 'completed';
  return 'started';
}

// Helper: derive token data from items metadata
function deriveTokenData(items: FlowItem[]) {
  let inputTokens = 0;
  let outputTokens = 0;
  let found = false;

  for (const item of items) {
    if (item.metadata?.input_tokens) {
      inputTokens += item.metadata.input_tokens as number;
      found = true;
    }
    if (item.metadata?.output_tokens) {
      outputTokens += item.metadata.output_tokens as number;
      found = true;
    }
  }

  if (!found) return null;
  return { input_tokens: inputTokens, output_tokens: outputTokens, total_tokens: inputTokens + outputTokens };
}

export default ParallelStageTabs;
