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

interface StageContentProps {
  items: FlowItem[];
  stageId: string;
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

interface ExecutionGroup {
  executionId: string;
  index: number;
  items: FlowItem[];
  status: string;
}

/**
 * Group items by executionId and merge orphaned items (no executionId) into
 * real execution groups when possible. This prevents session-level events
 * (e.g. executive_summary) that land in a stage group without an executionId
 * from creating phantom "agents".
 */
function groupItemsByExecution(items: FlowItem[]): ExecutionGroup[] {
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

  // If there are real execution groups alongside __default__, merge orphaned
  // items into the first real execution so they don't create a phantom agent.
  const defaultItems = groups.get('__default__');
  const realKeys = executionOrder.filter(k => k !== '__default__');
  if (defaultItems && realKeys.length > 0) {
    const firstReal = groups.get(realKeys[0])!;
    firstReal.push(...defaultItems);
    groups.delete('__default__');
    const idx = executionOrder.indexOf('__default__');
    if (idx !== -1) executionOrder.splice(idx, 1);
  }

  return executionOrder.map((execId, index) => ({
    executionId: execId,
    index,
    items: groups.get(execId) || [],
    status: deriveExecutionStatus(groups.get(execId) || []),
  }));
}

/**
 * StageContent — unified renderer for stage items.
 *
 * Groups flow items by execution_id. When there is a single execution
 * (the common single-agent case) the items are rendered directly without
 * any agent-card / tab chrome. When there are multiple executions
 * (parallel agents) the full tabbed interface with agent cards is shown.
 */
const StageContent: React.FC<StageContentProps> = ({
  items,
  stageId: _stageId,
  streamingEvents,
  shouldAutoCollapse,
  onToggleItemExpansion,
  expandAllReasoning = false,
  isItemCollapsible,
  agentProgressStatuses = new Map(),
  onSelectedAgentChange,
}) => {
  const [selectedTab, setSelectedTab] = useState(0);

  // Group items by executionId (merges orphaned items)
  const executions: ExecutionGroup[] = useMemo(() => groupItemsByExecution(items), [items]);

  // Get streaming items grouped by execution
  const streamingByExecution = useMemo(() => {
    const byExec = new Map<string, Array<[string, StreamingItem]>>();
    if (!streamingEvents) return byExec;

    for (const [eventId, event] of streamingEvents) {
      const execId = event.executionId || '__default__';
      if (!byExec.has(execId)) byExec.set(execId, []);
      byExec.get(execId)!.push([eventId, event]);
    }

    // Same merge logic for streaming: if real executions exist, merge __default__
    const defaultStreaming = byExec.get('__default__');
    if (defaultStreaming && byExec.size > 1) {
      // Find first non-default key
      for (const [key] of byExec) {
        if (key !== '__default__') {
          byExec.get(key)!.push(...defaultStreaming);
          byExec.delete('__default__');
          break;
        }
      }
    }

    return byExec;
  }, [streamingEvents]);

  const isMultiAgent = executions.length > 1;

  // Notify parent when selected tab changes
  React.useEffect(() => {
    if (onSelectedAgentChange && executions[selectedTab]) {
      onSelectedAgentChange(executions[selectedTab].executionId);
    }
  }, [selectedTab, executions, onSelectedAgentChange]);

  // ── Shared renderer for a single execution's items ──
  const renderExecutionItems = (execution: ExecutionGroup) => {
    const executionStreamingItems = streamingByExecution.get(execution.executionId) || [];
    const hasDbItems = execution.items.length > 0;
    const hasStreamingItems = executionStreamingItems.length > 0;
    const isFailed = execution.status === 'failed' || execution.status === 'timed_out';

    return (
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
            No reasoning steps available for this agent
          </Typography>
        )}

        {isFailed && (
          <Alert severity="error" sx={{ mt: 2 }}>
            <Typography variant="body2">
              <strong>Execution Failed</strong>
              {(() => {
                const errorItem = execution.items.find(i => i.type === 'error');
                const errMsg = (errorItem?.content) || (execution.items[execution.items.length - 1]?.metadata?.error_message as string);
                return errMsg ? `: ${errMsg}` : '';
              })()}
            </Typography>
          </Alert>
        )}
      </Box>
    );
  };

  // ── Empty state ──
  if (executions.length === 0) {
    // Check for streaming-only content (no completed items yet)
    const allStreamingItems = Array.from(streamingByExecution.values()).flat();
    if (allStreamingItems.length > 0) {
      return (
        <Box>
          {allStreamingItems.map(([key, streamItem]) => (
            <StreamingContentRenderer key={key} item={streamItem} />
          ))}
        </Box>
      );
    }

    return (
      <Alert severity="info">
        <Typography variant="body2">Waiting for agent data...</Typography>
      </Alert>
    );
  }

  // ── Single-agent: render items directly, no tabs/cards ──
  if (!isMultiAgent) {
    return renderExecutionItems(executions[0]);
  }

  // ── Multi-agent: full tabbed interface with agent cards ──
  return (
    <Box>
      {/* Parallel execution header */}
      <Box sx={{ mb: 3, pl: 4, pr: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
          <CallSplit color="secondary" fontSize="small" />
          <Typography variant="caption" color="secondary" fontWeight={600} letterSpacing={0.5}>
            PARALLEL EXECUTION
          </Typography>
          <Chip
            label={`${executions.length} agents`}
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
            const stageName = execution.items[0]?.metadata?.stage_name as string | undefined;
            const label = stageName ? `${stageName} #${tabIndex + 1}` : `Agent ${tabIndex + 1}`;
            const progressStatus = agentProgressStatuses.get(execution.executionId);
            const isTerminalProgress = !progressStatus || ['Completed', 'Failed', 'Cancelled'].includes(progressStatus);
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
      {executions.map((execution, index) => (
        <TabPanel key={execution.executionId} value={selectedTab} index={index}>
          {renderExecutionItems(execution)}
        </TabPanel>
      ))}
    </Box>
  );
};

export default StageContent;
