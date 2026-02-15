/**
 * Timeline Parser
 * Converts TimelineEvent[] from the REST API into FlowItem[] for UI rendering.
 * Handles stage grouping, parallel detection, stats computation, and dedup helpers.
 */

import type { TimelineEvent, StageOverview } from '../types/session';
import { TIMELINE_EVENT_TYPES, TIMELINE_STATUS } from '../constants/eventTypes';

// --- Types ---

export type FlowItemType =
  | 'thinking'
  | 'response'
  | 'tool_call'
  | 'tool_summary'
  | 'error'
  | 'final_analysis'
  | 'executive_summary'
  | 'user_question'
  | 'code_execution'
  | 'search_result'
  | 'url_context'
  | 'stage_separator';

export interface FlowItem {
  id: string;
  type: FlowItemType;
  stageId?: string;
  executionId?: string;
  content: string;
  metadata?: Record<string, unknown>;
  status: string;
  timestamp: string;
  sequenceNumber: number;
  isParallelStage?: boolean;
}

export interface StageGroup {
  stageId: string;
  stageName: string;
  stageIndex: number;
  stageStatus: string;
  isParallel: boolean;
  expectedAgentCount: number;
  items: FlowItem[];
}

export interface TimelineStats {
  totalStages: number;
  completedStages: number;
  failedStages: number;
  thoughtCount: number;
  toolCallCount: number;
  successfulToolCalls: number;
  toolSummaryCount: number;
  responseCount: number;
  analysisCount: number;
  finalAnswerCount: number;
  errorCount: number;
  nativeToolCount: number;
  userQuestionCount: number;
}

// --- Event type mapping ---

const EVENT_TYPE_MAP: Record<string, FlowItemType> = {
  [TIMELINE_EVENT_TYPES.LLM_THINKING]: 'thinking',
  [TIMELINE_EVENT_TYPES.LLM_RESPONSE]: 'response',
  [TIMELINE_EVENT_TYPES.LLM_TOOL_CALL]: 'tool_call',
  [TIMELINE_EVENT_TYPES.MCP_TOOL_SUMMARY]: 'tool_summary',
  [TIMELINE_EVENT_TYPES.FINAL_ANALYSIS]: 'final_analysis',
  [TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY]: 'executive_summary',
  [TIMELINE_EVENT_TYPES.USER_QUESTION]: 'user_question',
  [TIMELINE_EVENT_TYPES.CODE_EXECUTION]: 'code_execution',
  [TIMELINE_EVENT_TYPES.GOOGLE_SEARCH_RESULT]: 'search_result',
  [TIMELINE_EVENT_TYPES.URL_CONTEXT_RESULT]: 'url_context',
  [TIMELINE_EVENT_TYPES.NATIVE_THINKING]: 'thinking',
  [TIMELINE_EVENT_TYPES.ERROR]: 'error',
};

// --- Core parsing ---

/**
 * Convert a single TimelineEvent into a FlowItem.
 */
function eventToFlowItem(event: TimelineEvent, stageMap: Map<string, StageOverview>): FlowItem {
  const type = EVENT_TYPE_MAP[event.event_type] || 'response';
  const stage = event.stage_id ? stageMap.get(event.stage_id) : undefined;
  const isParallel = stage?.parallel_type != null && stage.parallel_type !== '' && stage.parallel_type !== 'none';

  return {
    id: event.id,
    type,
    stageId: event.stage_id || undefined,
    executionId: event.execution_id || undefined,
    content: event.content,
    metadata: event.metadata || undefined,
    status: event.status,
    timestamp: event.created_at,
    sequenceNumber: event.sequence_number,
    isParallelStage: isParallel || undefined,
  };
}

/**
 * Parse TimelineEvent[] + StageOverview[] into a flat FlowItem[] with stage separators.
 * Events are sorted by sequence_number. Stage separators are inserted at stage_id boundaries.
 */
export function parseTimelineToFlow(
  events: TimelineEvent[],
  stages: StageOverview[]
): FlowItem[] {
  if (events.length === 0) return [];

  const stageMap = new Map<string, StageOverview>();
  for (const stage of stages) {
    stageMap.set(stage.id, stage);
  }

  // Sort by stage index first (so all events for a stage stay together),
  // then by sequence number within each stage.
  // Events without a stage_id are placed at the end (e.g. executive_summary).
  const sorted = [...events].sort((a, b) => {
    const stageA = a.stage_id ? stageMap.get(a.stage_id) : undefined;
    const stageB = b.stage_id ? stageMap.get(b.stage_id) : undefined;
    const indexA = stageA?.stage_index ?? Number.MAX_SAFE_INTEGER;
    const indexB = stageB?.stage_index ?? Number.MAX_SAFE_INTEGER;
    if (indexA !== indexB) return indexA - indexB;
    return a.sequence_number - b.sequence_number;
  });

  const result: FlowItem[] = [];
  let currentStageId: string | null = null;

  for (const event of sorted) {
    // Insert stage separator when stage changes
    if (event.stage_id && event.stage_id !== currentStageId) {
      currentStageId = event.stage_id;
      const stage = stageMap.get(event.stage_id);
      if (stage) {
        result.push({
          id: `stage-sep-${stage.id}`,
          type: 'stage_separator',
          stageId: stage.id,
          content: stage.stage_name,
          metadata: {
            stage_index: stage.stage_index,
            stage_status: stage.status,
            parallel_type: stage.parallel_type,
            expected_agent_count: stage.expected_agent_count,
            started_at: stage.started_at,
            completed_at: stage.completed_at,
          },
          status: stage.status,
          timestamp: stage.started_at || event.created_at,
          sequenceNumber: event.sequence_number - 0.5, // Before first event in stage
          isParallelStage: stage.parallel_type != null && stage.parallel_type !== '' && stage.parallel_type !== 'none' ? true : undefined,
        });
      }
    }

    result.push(eventToFlowItem(event, stageMap));
  }

  return result;
}

// --- Stage grouping ---

/**
 * Group FlowItems by stage for rendering with stage collapse/expand.
 * Returns an array of StageGroups. Items without a stageId go into a synthetic "ungrouped" group.
 */
export function groupFlowItemsByStage(
  items: FlowItem[],
  stages: StageOverview[]
): StageGroup[] {
  const stageMap = new Map<string, StageOverview>();
  for (const stage of stages) {
    stageMap.set(stage.id, stage);
  }

  const groups: StageGroup[] = [];
  let currentGroup: StageGroup | null = null;

  for (const item of items) {
    if (item.type === 'stage_separator') {
      // Start a new group
      const stage = item.stageId ? stageMap.get(item.stageId) : undefined;
      currentGroup = {
        stageId: item.stageId || '',
        stageName: item.content,
        stageIndex: stage?.stage_index ?? groups.length,
        stageStatus: stage?.status || '',
        isParallel: item.isParallelStage || false,
        expectedAgentCount: (item.metadata?.expected_agent_count as number) || 1,
        items: [],
      };
      groups.push(currentGroup);
      continue;
    }

    if (currentGroup && item.stageId === currentGroup.stageId) {
      currentGroup.items.push(item);
    } else if (item.stageId && (!currentGroup || item.stageId !== currentGroup.stageId)) {
      // New stage without separator (shouldn't happen normally, but handle gracefully)
      const stage = stageMap.get(item.stageId);
      currentGroup = {
        stageId: item.stageId,
        stageName: stage?.stage_name || 'Unknown Stage',
        stageIndex: stage?.stage_index ?? groups.length,
        stageStatus: stage?.status || '',
        isParallel: !!item.isParallelStage,
        expectedAgentCount: stage?.expected_agent_count || 1,
        items: [item],
      };
      groups.push(currentGroup);
    } else if (currentGroup) {
      // Item belongs to current group (no stageId but we're in a group)
      currentGroup.items.push(item);
    } else {
      // Orphaned item, create ungrouped bucket
      currentGroup = {
        stageId: '',
        stageName: 'Pre-stage',
        stageIndex: -1,
        stageStatus: '',
        isParallel: false,
        expectedAgentCount: 1,
        items: [item],
      };
      groups.push(currentGroup);
    }
  }

  return groups;
}

/**
 * Group parallel stage items by execution_id for tab rendering.
 */
export function groupByExecutionId(items: FlowItem[]): Map<string, FlowItem[]> {
  const map = new Map<string, FlowItem[]>();
  for (const item of items) {
    const key = item.executionId || '__default__';
    const group = map.get(key);
    if (group) {
      group.push(item);
    } else {
      map.set(key, [item]);
    }
  }
  return map;
}

// --- Stats ---

/**
 * Compute timeline statistics for header chips.
 */
export function getTimelineStats(items: FlowItem[], stages: StageOverview[]): TimelineStats {
  const stats: TimelineStats = {
    totalStages: stages.length,
    completedStages: stages.filter(s => s.status === 'completed').length,
    failedStages: stages.filter(s => s.status === 'failed' || s.status === 'timed_out').length,
    thoughtCount: 0,
    toolCallCount: 0,
    successfulToolCalls: 0,
    toolSummaryCount: 0,
    responseCount: 0,
    analysisCount: 0,
    finalAnswerCount: 0,
    errorCount: 0,
    nativeToolCount: 0,
    userQuestionCount: 0,
  };

  for (const item of items) {
    switch (item.type) {
      case 'thinking': stats.thoughtCount++; break;
      case 'tool_call':
        stats.toolCallCount++;
        if (item.status === 'completed') stats.successfulToolCalls++;
        break;
      case 'tool_summary': stats.toolSummaryCount++; break;
      case 'response': stats.responseCount++; break;
      case 'final_analysis':
        stats.analysisCount++;
        stats.finalAnswerCount++;
        break;
      case 'executive_summary': stats.analysisCount++; break;
      case 'error': stats.errorCount++; break;
      case 'code_execution':
      case 'search_result':
      case 'url_context': stats.nativeToolCount++; break;
      case 'user_question': stats.userQuestionCount++; break;
    }
  }

  return stats;
}

// --- Collapse helpers ---

/** Types that support auto-collapse (thinking, tool_summary, final_analysis). */
const COLLAPSIBLE_TYPES: Set<FlowItemType> = new Set(['thinking', 'tool_summary', 'final_analysis']);

/**
 * Whether a FlowItem type supports auto-collapse behavior.
 */
export function isFlowItemCollapsible(item: FlowItem): boolean {
  return COLLAPSIBLE_TYPES.has(item.type);
}

/**
 * Whether a FlowItem is in a terminal (non-streaming) status.
 */
export function isFlowItemTerminal(item: FlowItem): boolean {
  return item.status !== TIMELINE_STATUS.STREAMING;
}

// --- Copy helpers ---

/**
 * Generate a plain-text representation of the chat flow for clipboard.
 */
export function flowItemsToPlainText(items: FlowItem[]): string {
  const lines: string[] = [];

  for (const item of items) {
    switch (item.type) {
      case 'stage_separator':
        lines.push(`\n--- Stage: ${item.content} ---\n`);
        break;
      case 'thinking':
        lines.push(`[Thought]\n${item.content}\n`);
        break;
      case 'response':
        lines.push(`[Response]\n${item.content}\n`);
        break;
      case 'tool_call': {
        const toolName = item.metadata?.tool_name || 'unknown';
        const serverName = item.metadata?.server_name || '';
        lines.push(`[Tool Call: ${serverName ? `${serverName}.` : ''}${toolName}]\n${item.content}\n`);
        break;
      }
      case 'tool_summary':
        lines.push(`[Tool Summary]\n${item.content}\n`);
        break;
      case 'final_analysis':
        lines.push(`[Final Analysis]\n${item.content}\n`);
        break;
      case 'executive_summary':
        lines.push(`[Executive Summary]\n${item.content}\n`);
        break;
      case 'user_question':
        lines.push(`[User Question]\n${item.content}\n`);
        break;
      case 'error':
        lines.push(`[Error]\n${item.content}\n`);
        break;
      case 'code_execution':
        lines.push(`[Code Execution]\n${item.content}\n`);
        break;
      case 'search_result':
        lines.push(`[Search Result]\n${item.content}\n`);
        break;
      case 'url_context':
        lines.push(`[URL Context]\n${item.content}\n`);
        break;
    }
  }

  return lines.join('\n').trim();
}

/**
 * isReActResponse checks whether content looks like a raw ReAct-formatted LLM
 * response (containing Thought:/Action: or Final Answer: markers). These are
 * redundant when the backend has already created properly-typed events for the
 * individual sections. Used by TimelineItem to hide such llm_response events.
 */
export function isReActResponse(content: string): boolean {
  if (!content) return false;
  // Must contain "Thought:" AND either "Action:" or "Final Answer:" to be
  // considered ReAct format. A single marker is not sufficient — it could be
  // regular text that happens to contain one of these words.
  const hasThought = content.includes('Thought:');
  const hasAction = content.includes('Action:');
  const hasFinalAnswer = content.includes('Final Answer:');
  return hasThought && (hasAction || hasFinalAnswer);
}
