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
import type { ExecutionOverview } from '../../types/session';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import TimelineItem from './TimelineItem';
import {
  EXECUTION_STATUS,
  TERMINAL_EXECUTION_STATUSES,
  FAILED_EXECUTION_STATUSES,
} from '../../constants/sessionStatus';

interface StageContentProps {
  items: FlowItem[];
  stageId: string;
  /** Execution overviews from the session detail API */
  executionOverviews?: ExecutionOverview[];
  /** Active streaming events keyed by event_id */
  streamingEvents?: Map<string, StreamingItem & { stageId?: string; executionId?: string }>;
  // Auto-collapse system
  shouldAutoCollapse?: (item: FlowItem) => boolean;
  onToggleItemExpansion?: (item: FlowItem) => void;
  expandAllReasoning?: boolean;
  isItemCollapsible?: (item: FlowItem) => boolean;
  // Per-agent progress
  agentProgressStatuses?: Map<string, string>;
  /** Real-time execution statuses from execution.status WS events (executionId → status).
   *  Higher priority than REST ExecutionOverview for immediate UI updates. */
  executionStatuses?: Map<string, string>;
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

const getExecutionErrorMessage = (items: FlowItem[]): string => {
  const errorItem = items.find(i => i.type === 'error');
  return errorItem?.content || (items[items.length - 1]?.metadata?.error_message as string) || '';
};

const getStatusIcon = (status: string) => {
  switch (status) {
    case EXECUTION_STATUS.FAILED:
    case EXECUTION_STATUS.TIMED_OUT: return <ErrorIcon fontSize="small" />;
    case EXECUTION_STATUS.COMPLETED: return <CheckCircle fontSize="small" />;
    case EXECUTION_STATUS.CANCELLED: return <CancelOutlined fontSize="small" />;
    default: return <PlayArrow fontSize="small" />;
  }
};

const getStatusColor = (status: string): 'default' | 'success' | 'error' | 'warning' | 'info' => {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED: return 'success';
    case EXECUTION_STATUS.FAILED:
    case EXECUTION_STATUS.TIMED_OUT: return 'error';
    case EXECUTION_STATUS.CANCELLED: return 'default';
    default: return 'info';
  }
};

const getStatusLabel = (status: string) => {
  switch (status) {
    case EXECUTION_STATUS.COMPLETED: return 'Complete';
    case EXECUTION_STATUS.FAILED: return 'Failed';
    case EXECUTION_STATUS.TIMED_OUT: return 'Timed Out';
    case EXECUTION_STATUS.CANCELLED: return 'Cancelled';
    case EXECUTION_STATUS.STARTED: return 'Running';
    default: return status;
  }
};

// Helper: derive execution status from items
function deriveExecutionStatus(items: FlowItem[]): string {
  if (items.length === 0) return EXECUTION_STATUS.STARTED;
  const hasError = items.some(
    i => i.type === 'error' || FAILED_EXECUTION_STATUSES.has(i.status || ''),
  );
  const allTerminal = items.every(i => TERMINAL_EXECUTION_STATUSES.has(i.status || ''));
  if (hasError) return EXECUTION_STATUS.FAILED;
  if (allTerminal && items.length > 0) return EXECUTION_STATUS.COMPLETED;
  return EXECUTION_STATUS.STARTED;
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
  executionOverviews,
  streamingEvents,
  shouldAutoCollapse,
  onToggleItemExpansion,
  expandAllReasoning = false,
  isItemCollapsible,
  agentProgressStatuses = new Map(),
  executionStatuses,
  onSelectedAgentChange,
}) => {
  const [selectedTab, setSelectedTab] = useState(0);

  // Group items by executionId (merges orphaned items)
  const executions: ExecutionGroup[] = useMemo(() => groupItemsByExecution(items), [items]);

  // Lookup execution overview by executionId
  const execOverviewMap = useMemo(() => {
    const map = new Map<string, ExecutionOverview>();
    if (executionOverviews) {
      for (const eo of executionOverviews) {
        map.set(eo.execution_id, eo);
      }
    }
    return map;
  }, [executionOverviews]);

  // Get streaming items grouped by execution
  const streamingByExecution = useMemo(() => {
    const byExec = new Map<string, Array<[string, StreamingItem]>>();
    if (!streamingEvents) return byExec;

    for (const [eventId, event] of streamingEvents) {
      const execId = event.executionId || '__default__';
      if (!byExec.has(execId)) byExec.set(execId, []);
      byExec.get(execId)!.push([eventId, event]);
    }

    // Merge __default__ into the first real execution from item grouping
    const defaultStreaming = byExec.get('__default__');
    const primaryExecId = executions[0]?.executionId;
    if (defaultStreaming && primaryExecId && primaryExecId !== '__default__') {
      if (!byExec.has(primaryExecId)) byExec.set(primaryExecId, []);
      byExec.get(primaryExecId)!.push(...defaultStreaming);
      byExec.delete('__default__');
    }

    return byExec;
  }, [streamingEvents, executions]);

  // ── Merge completed executions with streaming-only agents and overview-only agents ──
  // This ensures the tabbed UI appears immediately when parallel agents start
  // streaming or when the execution overview arrives, rather than waiting for
  // timeline items to complete.
  const mergedExecutions = useMemo(() => {
    const allExecIds = new Set(executions.map(e => e.executionId));

    // Agents that are streaming but have no completed items yet
    const streamOnlyGroups: ExecutionGroup[] = [];
    for (const execId of streamingByExecution.keys()) {
      if (execId !== '__default__' && !allExecIds.has(execId)) {
        streamOnlyGroups.push({
          executionId: execId,
          index: executions.length + streamOnlyGroups.length,
          items: [],
          status: EXECUTION_STATUS.STARTED,
        });
        allExecIds.add(execId);
      }
    }

    // Agents known from execution overviews but not yet in items or streaming
    const overviewGroups: ExecutionGroup[] = [];
    if (executionOverviews && executionOverviews.length > 0) {
      for (const eo of executionOverviews) {
        if (!allExecIds.has(eo.execution_id)) {
          overviewGroups.push({
            executionId: eo.execution_id,
            index: executions.length + streamOnlyGroups.length + overviewGroups.length,
            items: [],
            status: eo.status,
          });
          allExecIds.add(eo.execution_id);
        }
      }
    }

    return [...executions, ...streamOnlyGroups, ...overviewGroups];
  }, [executions, streamingByExecution, executionOverviews]);

  // Detect multi-agent from BOTH completed items and active streaming events
  // so the tabbed interface appears immediately, not only after items complete.
  const isMultiAgent = mergedExecutions.length > 1;

  // Notify parent when selected tab changes
  React.useEffect(() => {
    if (onSelectedAgentChange && mergedExecutions[selectedTab]) {
      onSelectedAgentChange(mergedExecutions[selectedTab].executionId);
    }
  }, [selectedTab, mergedExecutions, onSelectedAgentChange]);

  // Check if any parallel agent is still running (for "Waiting for other agents...")
  const hasOtherActiveAgents = useMemo(() => {
    if (!isMultiAgent) return false;
    return mergedExecutions.some((exec) => {
      const wsStatus = executionStatuses?.get(exec.executionId);
      const eo = execOverviewMap.get(exec.executionId);
      const status = wsStatus || eo?.status || exec.status;
      return !TERMINAL_EXECUTION_STATUSES.has(status);
    });
  }, [isMultiAgent, mergedExecutions, execOverviewMap, executionStatuses]);

  // ── Shared renderer for a single execution's items ──
  const renderExecutionItems = (execution: ExecutionGroup) => {
    const executionStreamingItems = streamingByExecution.get(execution.executionId) || [];
    const hasDbItems = execution.items.length > 0;
    const hasStreamingItems = executionStreamingItems.length > 0;

    // Prefer real-time WS status > REST execution overview > item-derived status
    const eo = execOverviewMap.get(execution.executionId);
    const wsStatus = executionStatuses?.get(execution.executionId);
    const effectiveStatus = wsStatus || eo?.status || execution.status;
    const isFailed = FAILED_EXECUTION_STATUSES.has(effectiveStatus);
    const isExecutionActive = !TERMINAL_EXECUTION_STATUSES.has(effectiveStatus);
    const errorMessage = eo?.error_message || getExecutionErrorMessage(execution.items);
    // This agent is done but others are still working
    const isWaitingForOthers = !isExecutionActive && hasOtherActiveAgents;

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

        {!hasDbItems && !hasStreamingItems && !isExecutionActive && (
          <Typography variant="body2" color="text.secondary" sx={{ textAlign: 'center', py: 4 }}>
            No reasoning steps available for this agent
          </Typography>
        )}

        {isFailed && (() => {
          return (
            <Alert severity="error" sx={{ mt: 2 }}>
              <Typography variant="body2">
                <strong>Execution Failed</strong>
                {errorMessage ? `: ${errorMessage}` : ''}
              </Typography>
            </Alert>
          );
        })()}

        {isWaitingForOthers && !isFailed && (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{
              mt: 2,
              fontStyle: 'italic',
              textAlign: 'center',
              opacity: 0.7,
            }}
          >
            Waiting for other agents...
          </Typography>
        )}
      </Box>
    );
  };

  // ── Empty state ──
  if (mergedExecutions.length === 0) {
    // Check for streaming-only content (no completed items yet, single agent)
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
    return renderExecutionItems(mergedExecutions[0]);
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
            label={`${mergedExecutions.length} agents`}
            size="small" color="secondary" variant="outlined"
            sx={{ height: 20, fontSize: '0.7rem' }}
          />
        </Box>

        {/* Agent Cards */}
        <Box sx={{ display: 'flex', gap: 1.5, flexWrap: 'wrap' }}>
          {mergedExecutions.map((execution, tabIndex) => {
            const isSelected = selectedTab === tabIndex;
            const eo = execOverviewMap.get(execution.executionId);
            const cardWsStatus = executionStatuses?.get(execution.executionId);
            const cardEffectiveStatus = cardWsStatus || eo?.status || execution.status;
            const statusColor = getStatusColor(cardEffectiveStatus);
            const statusIcon = getStatusIcon(cardEffectiveStatus);
            const label = eo?.agent_name || `Agent ${tabIndex + 1}`;
            const progressStatus = agentProgressStatuses.get(execution.executionId);
            const isTerminalProgress = !progressStatus
              || TERMINAL_EXECUTION_STATUSES.has(cardEffectiveStatus);
            // Prefer API-level token stats, fall back to deriving from item metadata
            const tokenData = eo
              ? { input_tokens: eo.input_tokens, output_tokens: eo.output_tokens, total_tokens: eo.total_tokens }
              : deriveTokenData(execution.items);
            const hasTokens = tokenData && (tokenData.input_tokens > 0 || tokenData.output_tokens > 0);

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
                  {eo?.llm_provider && (
                    <Typography variant="caption" color="text.secondary" sx={{ fontFamily: 'monospace' }}>
                      {eo.llm_provider}
                    </Typography>
                  )}
                  {eo?.iteration_strategy && (
                    <Typography variant="caption" color="text.secondary">
                      {eo.iteration_strategy.replace(/_/g, '-')}
                    </Typography>
                  )}
                  <Chip
                    label={getStatusLabel(cardEffectiveStatus)}
                    size="small" color={statusColor}
                    sx={{ height: 18, fontSize: '0.65rem' }}
                  />
                  {progressStatus && !isTerminalProgress ? (
                    <Chip
                      label={progressStatus}
                      size="small" color="info" variant="outlined"
                      sx={{ height: 18, fontSize: '0.65rem', fontStyle: 'italic' }}
                    />
                  ) : isTerminalProgress && hasOtherActiveAgents && TERMINAL_EXECUTION_STATUSES.has(cardEffectiveStatus) ? (
                    <Chip
                      label="Waiting for other agents..."
                      size="small" color="default" variant="outlined"
                      sx={{ height: 18, fontSize: '0.65rem', fontStyle: 'italic', opacity: 0.7 }}
                    />
                  ) : null}
                </Box>
                {/* Show streaming activity count when no execution overview yet */}
                {!eo && !hasTokens && (() => {
                  const streamCount = (streamingByExecution.get(execution.executionId) || []).length;
                  const itemCount = execution.items.length;
                  const total = streamCount + itemCount;
                  if (total > 0) {
                    return (
                      <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
                        {streamCount > 0 ? `${total} event${total > 1 ? 's' : ''} (${streamCount} streaming)` : `${total} event${total > 1 ? 's' : ''}`}
                      </Typography>
                    );
                  }
                  return null;
                })()}
                {hasTokens && tokenData && (
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
      {mergedExecutions.map((execution, index) => (
        <TabPanel key={execution.executionId} value={selectedTab} index={index}>
          {renderExecutionItems(execution)}
        </TabPanel>
      ))}
    </Box>
  );
};

export default StageContent;
