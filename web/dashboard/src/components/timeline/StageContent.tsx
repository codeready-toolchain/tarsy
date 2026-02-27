import React, { useState, useMemo } from 'react';
import { Box, Typography, Chip, Alert, alpha } from '@mui/material';
import {
  CheckCircle,
  Error as ErrorIcon,
  PlayArrow,
  CallSplit,
  CancelOutlined,
} from '@mui/icons-material';
import { FLOW_ITEM, type FlowItem } from '../../utils/timelineParser';
import type { ExecutionOverview } from '../../types/session';
import type { StreamingItem } from '../streaming/StreamingContentRenderer';
import StreamingContentRenderer from '../streaming/StreamingContentRenderer';
import TokenUsageDisplay from '../shared/TokenUsageDisplay';
import TimelineItem from './TimelineItem';
import SubAgentCard from './SubAgentCard';
import {
  EXECUTION_STATUS,
  TERMINAL_EXECUTION_STATUSES,
  FAILED_EXECUTION_STATUSES,
  CANCELLED_EXECUTION_STATUSES,
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
  expandAllToolCalls?: boolean;
  isItemCollapsible?: (item: FlowItem) => boolean;
  // Per-agent progress
  agentProgressStatuses?: Map<string, string>;
  /** Real-time execution statuses from execution.status WS events (executionId → {status, stageId}).
   *  Higher priority than REST ExecutionOverview for immediate UI updates.
   *  stageId is used to filter out executions belonging to other stages.
   *  agentIndex (1-based) preserves chain config ordering for deterministic tab order. */
  executionStatuses?: Map<string, { status: string; stageId: string; agentIndex: number }>;
  /** Sub-agent streaming events (events with parent_execution_id) */
  subAgentStreamingEvents?: Map<string, StreamingItem & { stageId?: string; executionId?: string }>;
  /** Sub-agent execution statuses (events with parent_execution_id) */
  subAgentExecutionStatuses?: Map<string, { status: string; stageId: string; agentIndex: number }>;
  /** Sub-agent progress statuses (events with parent_execution_id) */
  subAgentProgressStatuses?: Map<string, string>;
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
  const errorItem = items.find(i => i.type === FLOW_ITEM.ERROR);
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

// Helper: derive execution status from items.
// IMPORTANT: Timeline item status "completed" means "streaming finished for this item",
// NOT "the execution is done". Between LLM iterations all existing items can be
// "completed" while the agent is still running. Only trust a final_analysis item
// as a definitive completion signal.
function deriveExecutionStatus(items: FlowItem[]): string {
  if (items.length === 0) return EXECUTION_STATUS.STARTED;
  const hasError = items.some(
    i => i.type === FLOW_ITEM.ERROR || FAILED_EXECUTION_STATUSES.has(i.status || ''),
  );
  if (hasError) return EXECUTION_STATUS.FAILED;
  // A final_analysis item is the definitive signal that the agent finished.
  const hasFinalAnalysis = items.some(i => i.type === FLOW_ITEM.FINAL_ANALYSIS);
  if (hasFinalAnalysis) return EXECUTION_STATUS.COMPLETED;
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
    if (item.type === FLOW_ITEM.STAGE_SEPARATOR) continue;
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
  expandAllToolCalls = false,
  isItemCollapsible,
  agentProgressStatuses = new Map(),
  executionStatuses,
  subAgentStreamingEvents,
  subAgentExecutionStatuses,
  subAgentProgressStatuses,
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

  // Build set of sub-agent execution IDs and overview map (from REST data)
  const { subAgentIds, subAgentOverviewMap } = useMemo(() => {
    const ids = new Set<string>();
    const overviews = new Map<string, ExecutionOverview>();
    if (executionOverviews) {
      for (const eo of executionOverviews) {
        if (eo.sub_agents) {
          for (const sub of eo.sub_agents) {
            ids.add(sub.execution_id);
            overviews.set(sub.execution_id, sub);
          }
        }
      }
    }
    return { subAgentIds: ids, subAgentOverviewMap: overviews };
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

    // Agents known only from execution.status WS events (e.g. "active" arrives
    // before any items, streaming, or REST overview data).
    // Filter by stageId to avoid creating phantom agents from other stages'
    // executions (executionStatuses is a global map across all stages).
    const statusOnlyGroups: ExecutionGroup[] = [];
    if (executionStatuses) {
      for (const [execId, execStatus] of executionStatuses) {
        if (!allExecIds.has(execId) && execStatus.stageId === _stageId) {
          statusOnlyGroups.push({
            executionId: execId,
            index: executions.length + streamOnlyGroups.length + overviewGroups.length + statusOnlyGroups.length,
            items: [],
            status: execStatus.status || EXECUTION_STATUS.STARTED,
          });
          allExecIds.add(execId);
        }
      }
    }

    // Filter out sub-agent executions — they render inside SubAgentCard, not as tabs
    const merged = [...executions, ...streamOnlyGroups, ...overviewGroups, ...statusOnlyGroups]
      .filter(e => !subAgentIds.has(e.executionId));

    // Sort by agent_index (1-based, from chain config) for deterministic tab order.
    // Resolve agent_index from REST execution overviews or real-time WS statuses.
    merged.sort((a, b) => {
      const indexA = execOverviewMap.get(a.executionId)?.agent_index
        ?? executionStatuses?.get(a.executionId)?.agentIndex
        ?? Number.MAX_SAFE_INTEGER;
      const indexB = execOverviewMap.get(b.executionId)?.agent_index
        ?? executionStatuses?.get(b.executionId)?.agentIndex
        ?? Number.MAX_SAFE_INTEGER;
      return indexA - indexB;
    });

    return merged;
  }, [executions, streamingByExecution, executionOverviews, executionStatuses, execOverviewMap, subAgentIds]);

  // Detect multi-agent from BOTH completed items and active streaming events
  // so the tabbed interface appears immediately, not only after items complete.
  const isMultiAgent = mergedExecutions.length > 1;

  // Notify parent when selected tab changes (parallel stages only).
  // Non-parallel stages clear the selection so the "Waiting for other agents..."
  // logic in ConversationTimeline doesn't use a stale agent ID from a previous
  // parallel stage.
  React.useEffect(() => {
    if (!onSelectedAgentChange) return;
    if (isMultiAgent && mergedExecutions[selectedTab]) {
      onSelectedAgentChange(mergedExecutions[selectedTab].executionId);
    } else if (!isMultiAgent) {
      onSelectedAgentChange(null);
    }
  }, [selectedTab, mergedExecutions, onSelectedAgentChange, isMultiAgent]);

  // Group sub-agent streaming events by execution ID
  const subAgentStreamingByExec = useMemo(() => {
    const byExec = new Map<string, Array<[string, StreamingItem]>>();
    if (!subAgentStreamingEvents) return byExec;
    for (const [eventId, event] of subAgentStreamingEvents) {
      const execId = event.executionId || '';
      if (!execId) continue;
      if (!byExec.has(execId)) byExec.set(execId, []);
      byExec.get(execId)!.push([eventId, event]);
    }
    return byExec;
  }, [subAgentStreamingEvents]);

  // Group sub-agent timeline items (from REST) by execution ID
  const subAgentItemsByExec = useMemo(() => {
    const byExec = new Map<string, FlowItem[]>();
    for (const group of executions) {
      if (subAgentIds.has(group.executionId)) {
        byExec.set(group.executionId, group.items);
      }
    }
    return byExec;
  }, [executions, subAgentIds]);

  // Check if any parallel agent is still running (for "Waiting for other agents...")
  const hasOtherActiveAgents = useMemo(() => {
    if (!isMultiAgent) return false;
    const result = mergedExecutions.some((exec) => {
      const wsStatus = executionStatuses?.get(exec.executionId)?.status;
      const eo = execOverviewMap.get(exec.executionId);
      const status = wsStatus || eo?.status || exec.status;
      return !TERMINAL_EXECUTION_STATUSES.has(status);
    });
    return result;
  }, [isMultiAgent, mergedExecutions, execOverviewMap, executionStatuses]);

  // Try to extract execution_id from a dispatch_agent tool result content
  const extractDispatchExecId = (content: string): string | null => {
    try {
      const parsed = JSON.parse(content);
      if (parsed?.execution_id) return parsed.execution_id;
    } catch { /* not JSON, ignore */ }
    return null;
  };

  // Check if a FlowItem is a dispatch_agent tool result (mcp_tool_summary)
  const isDispatchToolResult = (item: FlowItem): boolean => {
    return item.type === FLOW_ITEM.TOOL_SUMMARY
      && item.metadata?.server_name === 'orchestrator'
      && item.metadata?.tool_name === 'dispatch_agent';
  };

  // ── Shared renderer for a single execution's items ──
  const renderExecutionItems = (execution: ExecutionGroup) => {
    const executionStreamingItems = streamingByExecution.get(execution.executionId) || [];
    const hasDbItems = execution.items.length > 0;
    const hasStreamingItems = executionStreamingItems.length > 0;

    // Prefer real-time WS status > REST execution overview > item-derived status
    const eo = execOverviewMap.get(execution.executionId);
    const wsStatus = executionStatuses?.get(execution.executionId)?.status;
    const effectiveStatus = wsStatus || eo?.status || execution.status;
    const isFailed = FAILED_EXECUTION_STATUSES.has(effectiveStatus);
    const isCancelled = CANCELLED_EXECUTION_STATUSES.has(effectiveStatus);
    const isExecutionActive = !TERMINAL_EXECUTION_STATUSES.has(effectiveStatus);
    const errorMessage = eo?.error_message || getExecutionErrorMessage(execution.items);

    // Track which sub-agents have been rendered inline (anchored to dispatch tool calls)
    const renderedSubAgents = new Set<string>();

    // Build list of elements with sub-agent cards interleaved after dispatch_agent results
    const elements: React.ReactNode[] = [];
    for (const item of execution.items) {
      elements.push(
        <TimelineItem
          key={item.id}
          item={item}
          isAutoCollapsed={shouldAutoCollapse ? shouldAutoCollapse(item) : false}
          onToggleAutoCollapse={onToggleItemExpansion ? () => onToggleItemExpansion(item) : undefined}
          expandAll={expandAllReasoning}
          expandAllToolCalls={expandAllToolCalls}
          isCollapsible={isItemCollapsible ? isItemCollapsible(item) : false}
        />,
      );

      // After a dispatch_agent tool result, render the matching SubAgentCard
      if (isDispatchToolResult(item)) {
        const subExecId = extractDispatchExecId(item.content);
        if (subExecId && (subAgentOverviewMap.has(subExecId) || subAgentItemsByExec.has(subExecId) || subAgentStreamingByExec.has(subExecId))) {
          renderedSubAgents.add(subExecId);
          elements.push(
            <SubAgentCard
              key={`sub-${subExecId}`}
              executionOverview={subAgentOverviewMap.get(subExecId)}
              items={subAgentItemsByExec.get(subExecId) || []}
              streamingEvents={subAgentStreamingByExec.get(subExecId)}
              executionStatus={subAgentExecutionStatuses?.get(subExecId)}
              progressStatus={subAgentProgressStatuses?.get(subExecId)}
              shouldAutoCollapse={shouldAutoCollapse}
              onToggleItemExpansion={onToggleItemExpansion}
              expandAllReasoning={expandAllReasoning}
              expandAllToolCalls={expandAllToolCalls}
              isItemCollapsible={isItemCollapsible}
            />,
          );
        }
      }
    }

    // Render any sub-agents not anchored to a dispatch tool call (e.g. REST data loaded
    // before tool call items arrive, or tool call metadata doesn't match)
    const allSubAgentExecIds = new Set([
      ...subAgentOverviewMap.keys(),
      ...subAgentItemsByExec.keys(),
      ...subAgentStreamingByExec.keys(),
    ]);
    for (const subExecId of allSubAgentExecIds) {
      if (!renderedSubAgents.has(subExecId)) {
        elements.push(
          <SubAgentCard
            key={`sub-${subExecId}`}
            executionOverview={subAgentOverviewMap.get(subExecId)}
            items={subAgentItemsByExec.get(subExecId) || []}
            streamingEvents={subAgentStreamingByExec.get(subExecId)}
            executionStatus={subAgentExecutionStatuses?.get(subExecId)}
            progressStatus={subAgentProgressStatuses?.get(subExecId)}
            shouldAutoCollapse={shouldAutoCollapse}
            onToggleItemExpansion={onToggleItemExpansion}
            expandAllReasoning={expandAllReasoning}
            expandAllToolCalls={expandAllToolCalls}
            isItemCollapsible={isItemCollapsible}
          />,
        );
      }
    }

    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
        {elements}

        {executionStreamingItems.map(([key, streamItem]) => (
          <StreamingContentRenderer key={key} item={streamItem} />
        ))}

        {!hasDbItems && !hasStreamingItems && !isExecutionActive && allSubAgentExecIds.size === 0 && (
          <Typography variant="body2" color="text.secondary" sx={{ textAlign: 'center', py: 4 }}>
            No reasoning steps available for this agent
          </Typography>
        )}

        {isFailed && (
          <Alert severity="error" sx={{ mt: 2 }}>
            <Typography variant="body2">
              <strong>Execution Failed</strong>
              {errorMessage ? `: ${errorMessage}` : ''}
            </Typography>
          </Alert>
        )}

        {isCancelled && (
          <Alert severity="info" sx={{ mt: 2, bgcolor: 'grey.100', '& .MuiAlert-icon': { color: 'text.secondary' } }}>
            <Typography variant="body2" color="text.secondary">
              <strong>Execution Cancelled</strong>
              {errorMessage ? `: ${errorMessage}` : ''}
            </Typography>
          </Alert>
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
            const cardWsStatus = executionStatuses?.get(execution.executionId)?.status;
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
                  {eo?.llm_backend && (
                    <Typography variant="caption" color="text.secondary">
                      {eo.llm_backend}
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
                      label="Waiting..."
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
