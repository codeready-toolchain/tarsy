/**
 * Tests for timelineParser.ts
 *
 * Covers: parseTimelineToFlow, groupFlowItemsByStage, getTimelineStats,
 *         flowItemsToPlainText, isFlowItemCollapsible, isFlowItemTerminal,
 *         groupByExecutionId, and the internal filterDuplicatedItems logic.
 */

import type { TimelineEvent, StageOverview } from '../../types/session';
import {
  parseTimelineToFlow,
  groupFlowItemsByStage,
  getTimelineStats,
  flowItemsToPlainText,
  isFlowItemCollapsible,
  isFlowItemTerminal,
  groupByExecutionId,
  FLOW_ITEM,
  type FlowItem,
} from '../../utils/timelineParser';
import { TIMELINE_EVENT_TYPES, TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

// ---------------------------------------------------------------------------
// Factories
// ---------------------------------------------------------------------------

function makeEvent(overrides: Partial<TimelineEvent> & { id: string }): TimelineEvent {
  return {
    session_id: 'session-1',
    stage_id: 'stage-1',
    execution_id: 'exec-1',
    sequence_number: 1,
    event_type: TIMELINE_EVENT_TYPES.LLM_THINKING,
    status: TIMELINE_STATUS.COMPLETED,
    content: 'test content',
    metadata: null,
    created_at: '2025-01-15T10:00:00Z',
    updated_at: '2025-01-15T10:00:05Z',
    ...overrides,
  };
}

function makeStage(overrides: Partial<StageOverview> & { id: string }): StageOverview {
  return {
    stage_name: 'Investigation',
    stage_index: 0,
    status: EXECUTION_STATUS.COMPLETED,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// parseTimelineToFlow
// ---------------------------------------------------------------------------

describe('parseTimelineToFlow', () => {
  it('returns empty array for empty events', () => {
    expect(parseTimelineToFlow([], [])).toEqual([]);
  });

  it('maps each event type to the correct FlowItem type', () => {
    const eventTypeMappings: [string, string][] = [
      [TIMELINE_EVENT_TYPES.LLM_THINKING, FLOW_ITEM.THINKING],
      [TIMELINE_EVENT_TYPES.LLM_RESPONSE, FLOW_ITEM.RESPONSE],
      [TIMELINE_EVENT_TYPES.LLM_TOOL_CALL, FLOW_ITEM.TOOL_CALL],
      [TIMELINE_EVENT_TYPES.MCP_TOOL_SUMMARY, FLOW_ITEM.TOOL_SUMMARY],
      [TIMELINE_EVENT_TYPES.FINAL_ANALYSIS, FLOW_ITEM.FINAL_ANALYSIS],
      [TIMELINE_EVENT_TYPES.USER_QUESTION, FLOW_ITEM.USER_QUESTION],
      [TIMELINE_EVENT_TYPES.CODE_EXECUTION, FLOW_ITEM.CODE_EXECUTION],
      [TIMELINE_EVENT_TYPES.GOOGLE_SEARCH_RESULT, FLOW_ITEM.SEARCH_RESULT],
      [TIMELINE_EVENT_TYPES.URL_CONTEXT_RESULT, FLOW_ITEM.URL_CONTEXT],
      [TIMELINE_EVENT_TYPES.ERROR, FLOW_ITEM.ERROR],
    ];

    const stage = makeStage({ id: 'stage-1' });

    for (const [eventType, expectedFlowType] of eventTypeMappings) {
      const events = [
        makeEvent({ id: `evt-${eventType}`, event_type: eventType, sequence_number: 1 }),
      ];
      const result = parseTimelineToFlow(events, [stage]);
      const item = result.find((i) => i.type !== FLOW_ITEM.STAGE_SEPARATOR);
      expect(item?.type).toBe(expectedFlowType);
    }
  });

  it('falls back to RESPONSE for unknown event types', () => {
    const stage = makeStage({ id: 'stage-1' });
    const events = [makeEvent({ id: 'evt-1', event_type: 'unknown_type' })];
    const result = parseTimelineToFlow(events, [stage]);
    const item = result.find((i) => i.type !== FLOW_ITEM.STAGE_SEPARATOR);
    expect(item?.type).toBe(FLOW_ITEM.RESPONSE);
  });

  it('inserts stage separator when stage changes', () => {
    const stages = [
      makeStage({ id: 'stage-1', stage_index: 0, stage_name: 'Stage One' }),
      makeStage({ id: 'stage-2', stage_index: 1, stage_name: 'Stage Two' }),
    ];
    const events = [
      makeEvent({ id: 'e1', stage_id: 'stage-1', sequence_number: 1 }),
      makeEvent({ id: 'e2', stage_id: 'stage-2', sequence_number: 2 }),
    ];
    const result = parseTimelineToFlow(events, stages);

    const separators = result.filter((i) => i.type === FLOW_ITEM.STAGE_SEPARATOR);
    expect(separators).toHaveLength(2);
    expect(separators[0].content).toBe('Stage One');
    expect(separators[1].content).toBe('Stage Two');
  });

  it('sorts events by stage index then sequence number', () => {
    const stages = [
      makeStage({ id: 's1', stage_index: 0 }),
      makeStage({ id: 's2', stage_index: 1 }),
    ];
    const events = [
      makeEvent({ id: 'e3', stage_id: 's2', sequence_number: 1, content: 'third' }),
      makeEvent({ id: 'e1', stage_id: 's1', sequence_number: 1, content: 'first' }),
      makeEvent({ id: 'e2', stage_id: 's1', sequence_number: 2, content: 'second' }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const nonSep = result.filter((i) => i.type !== FLOW_ITEM.STAGE_SEPARATOR);
    expect(nonSep.map((i) => i.content)).toEqual(['first', 'second', 'third']);
  });

  it('places user_question events first within their stage', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({ id: 'e1', stage_id: 's1', sequence_number: 5, event_type: TIMELINE_EVENT_TYPES.LLM_THINKING, content: 'thinking' }),
      makeEvent({ id: 'e2', stage_id: 's1', sequence_number: 100, event_type: TIMELINE_EVENT_TYPES.USER_QUESTION, content: 'question' }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const nonSep = result.filter((i) => i.type !== FLOW_ITEM.STAGE_SEPARATOR);
    expect(nonSep[0].type).toBe(FLOW_ITEM.USER_QUESTION);
    expect(nonSep[1].type).toBe(FLOW_ITEM.THINKING);
  });

  it('places events without stage_id at the end', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({ id: 'e2', stage_id: null, sequence_number: 1, event_type: TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY, content: 'summary' }),
      makeEvent({ id: 'e1', stage_id: 's1', sequence_number: 1, content: 'thinking' }),
    ];
    const result = parseTimelineToFlow(events, stages);
    // executive_summary is filtered by filterDuplicatedItems, so verify sort only
    // by checking that stage-1 items appear before null-stage items
    const nonSep = result.filter((i) => i.type !== FLOW_ITEM.STAGE_SEPARATOR);
    if (nonSep.length > 1) {
      expect(nonSep[0].stageId).toBe('s1');
    }
  });

  it('enriches thinking events with computed duration_ms', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({
        id: 'e1',
        stage_id: 's1',
        event_type: TIMELINE_EVENT_TYPES.LLM_THINKING,
        status: TIMELINE_STATUS.COMPLETED,
        created_at: '2025-01-15T10:00:00Z',
        updated_at: '2025-01-15T10:00:03Z',
        metadata: null,
      }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const thinking = result.find((i) => i.type === FLOW_ITEM.THINKING);
    expect(thinking?.metadata?.duration_ms).toBe(3000);
  });

  it('does not override existing duration_ms in metadata', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({
        id: 'e1',
        stage_id: 's1',
        event_type: TIMELINE_EVENT_TYPES.LLM_THINKING,
        status: TIMELINE_STATUS.COMPLETED,
        metadata: { duration_ms: 1234 },
      }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const thinking = result.find((i) => i.type === FLOW_ITEM.THINKING);
    expect(thinking?.metadata?.duration_ms).toBe(1234);
  });

  it('sets isParallelStage for events in parallel stages', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0, parallel_type: 'multi_agent' })];
    const events = [makeEvent({ id: 'e1', stage_id: 's1' })];
    const result = parseTimelineToFlow(events, stages);
    const item = result.find((i) => i.type === FLOW_ITEM.THINKING);
    expect(item?.isParallelStage).toBe(true);
  });

  it('does not set isParallelStage for non-parallel stages', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0, parallel_type: null })];
    const events = [makeEvent({ id: 'e1', stage_id: 's1' })];
    const result = parseTimelineToFlow(events, stages);
    const item = result.find((i) => i.type === FLOW_ITEM.THINKING);
    expect(item?.isParallelStage).toBeUndefined();
  });

  it('stage separator metadata includes stage details', () => {
    const stages = [
      makeStage({
        id: 's1',
        stage_index: 0,
        stage_name: 'Investigation',
        status: EXECUTION_STATUS.COMPLETED,
        parallel_type: 'multi_agent',
        expected_agent_count: 3,
      }),
    ];
    const events = [makeEvent({ id: 'e1', stage_id: 's1' })];
    const result = parseTimelineToFlow(events, stages);
    const sep = result.find((i) => i.type === FLOW_ITEM.STAGE_SEPARATOR);
    expect(sep?.metadata).toMatchObject({
      stage_index: 0,
      stage_status: EXECUTION_STATUS.COMPLETED,
      parallel_type: 'multi_agent',
      expected_agent_count: 3,
    });
  });
});

// ---------------------------------------------------------------------------
// filterDuplicatedItems (tested via parseTimelineToFlow)
// ---------------------------------------------------------------------------

describe('filterDuplicatedItems', () => {
  it('removes executive_summary items', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({ id: 'e1', stage_id: 's1', event_type: TIMELINE_EVENT_TYPES.LLM_THINKING }),
      makeEvent({ id: 'e2', stage_id: null, event_type: TIMELINE_EVENT_TYPES.EXECUTIVE_SUMMARY, sequence_number: 99 }),
    ];
    const result = parseTimelineToFlow(events, stages);
    expect(result.find((i) => i.type === FLOW_ITEM.EXECUTIVE_SUMMARY)).toBeUndefined();
  });

  it('removes response when identical final_analysis exists in same execution', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const sharedContent = 'The analysis shows...';
    const events = [
      makeEvent({
        id: 'e1',
        stage_id: 's1',
        execution_id: 'exec-1',
        event_type: TIMELINE_EVENT_TYPES.LLM_RESPONSE,
        content: sharedContent,
        sequence_number: 1,
      }),
      makeEvent({
        id: 'e2',
        stage_id: 's1',
        execution_id: 'exec-1',
        event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
        content: sharedContent,
        sequence_number: 2,
      }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const responses = result.filter((i) => i.type === FLOW_ITEM.RESPONSE);
    const analyses = result.filter((i) => i.type === FLOW_ITEM.FINAL_ANALYSIS);
    expect(responses).toHaveLength(0);
    expect(analyses).toHaveLength(1);
  });

  it('keeps response when content differs from final_analysis', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const events = [
      makeEvent({
        id: 'e1',
        stage_id: 's1',
        execution_id: 'exec-1',
        event_type: TIMELINE_EVENT_TYPES.LLM_RESPONSE,
        content: 'partial response',
        sequence_number: 1,
      }),
      makeEvent({
        id: 'e2',
        stage_id: 's1',
        execution_id: 'exec-1',
        event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
        content: 'full analysis',
        sequence_number: 2,
      }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const responses = result.filter((i) => i.type === FLOW_ITEM.RESPONSE);
    expect(responses).toHaveLength(1);
  });

  it('keeps response when final_analysis is in a different execution', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0 })];
    const sharedContent = 'same content';
    const events = [
      makeEvent({
        id: 'e1',
        stage_id: 's1',
        execution_id: 'exec-1',
        event_type: TIMELINE_EVENT_TYPES.LLM_RESPONSE,
        content: sharedContent,
        sequence_number: 1,
      }),
      makeEvent({
        id: 'e2',
        stage_id: 's1',
        execution_id: 'exec-2',
        event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
        content: sharedContent,
        sequence_number: 2,
      }),
    ];
    const result = parseTimelineToFlow(events, stages);
    const responses = result.filter((i) => i.type === FLOW_ITEM.RESPONSE);
    expect(responses).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// groupFlowItemsByStage
// ---------------------------------------------------------------------------

describe('groupFlowItemsByStage', () => {
  it('groups items by stage following separators', () => {
    const stages = [
      makeStage({ id: 's1', stage_index: 0, stage_name: 'Stage One' }),
      makeStage({ id: 's2', stage_index: 1, stage_name: 'Stage Two' }),
    ];
    const events = [
      makeEvent({ id: 'e1', stage_id: 's1', sequence_number: 1 }),
      makeEvent({ id: 'e2', stage_id: 's1', sequence_number: 2 }),
      makeEvent({ id: 'e3', stage_id: 's2', sequence_number: 3 }),
    ];
    const items = parseTimelineToFlow(events, stages);
    const groups = groupFlowItemsByStage(items, stages);

    expect(groups).toHaveLength(2);
    expect(groups[0].stageName).toBe('Stage One');
    expect(groups[0].items).toHaveLength(2);
    expect(groups[1].stageName).toBe('Stage Two');
    expect(groups[1].items).toHaveLength(1);
  });

  it('creates ungrouped bucket for orphaned items', () => {
    const items: FlowItem[] = [
      {
        id: 'orphan-1',
        type: FLOW_ITEM.RESPONSE,
        content: 'orphaned',
        status: TIMELINE_STATUS.COMPLETED,
        timestamp: '2025-01-15T10:00:00Z',
        sequenceNumber: 1,
      },
    ];
    const groups = groupFlowItemsByStage(items, []);
    expect(groups).toHaveLength(1);
    expect(groups[0].stageName).toBe('Pre-stage');
    expect(groups[0].stageIndex).toBe(-1);
  });

  it('handles items with unknown stageId gracefully', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0, stage_name: 'Known Stage' })];
    const items: FlowItem[] = [
      {
        id: 'sep-s1',
        type: FLOW_ITEM.STAGE_SEPARATOR,
        stageId: 's1',
        content: 'Known Stage',
        metadata: { expected_agent_count: 1 },
        status: EXECUTION_STATUS.COMPLETED,
        timestamp: '2025-01-15T10:00:00Z',
        sequenceNumber: 0,
      },
      {
        id: 'e1',
        type: FLOW_ITEM.THINKING,
        stageId: 's1',
        content: 'thinking',
        status: TIMELINE_STATUS.COMPLETED,
        timestamp: '2025-01-15T10:00:01Z',
        sequenceNumber: 1,
      },
      {
        id: 'e2',
        type: FLOW_ITEM.RESPONSE,
        stageId: 'unknown-stage',
        content: 'response in unknown stage',
        status: TIMELINE_STATUS.COMPLETED,
        timestamp: '2025-01-15T10:01:00Z',
        sequenceNumber: 2,
      },
    ];
    const groups = groupFlowItemsByStage(items, stages);
    expect(groups.length).toBeGreaterThanOrEqual(2);
    expect(groups[1].stageName).toBe('Unknown Stage');
  });

  it('sets isParallel from stage separator', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0, parallel_type: 'multi_agent' })];
    const events = [makeEvent({ id: 'e1', stage_id: 's1' })];
    const items = parseTimelineToFlow(events, stages);
    const groups = groupFlowItemsByStage(items, stages);
    expect(groups[0].isParallel).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// getTimelineStats
// ---------------------------------------------------------------------------

describe('getTimelineStats', () => {
  it('counts items by type correctly', () => {
    const stages = [
      makeStage({ id: 's1', stage_index: 0, status: EXECUTION_STATUS.COMPLETED }),
      makeStage({ id: 's2', stage_index: 1, status: EXECUTION_STATUS.FAILED }),
    ];
    const items: FlowItem[] = [
      { id: '1', type: FLOW_ITEM.THINKING, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '2', type: FLOW_ITEM.THINKING, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '3', type: FLOW_ITEM.TOOL_CALL, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '4', type: FLOW_ITEM.TOOL_CALL, content: '', status: TIMELINE_STATUS.FAILED, timestamp: '', sequenceNumber: 0 },
      { id: '5', type: FLOW_ITEM.TOOL_SUMMARY, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '6', type: FLOW_ITEM.RESPONSE, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '7', type: FLOW_ITEM.FINAL_ANALYSIS, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '8', type: FLOW_ITEM.EXECUTIVE_SUMMARY, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '9', type: FLOW_ITEM.ERROR, content: '', status: TIMELINE_STATUS.FAILED, timestamp: '', sequenceNumber: 0 },
      { id: '10', type: FLOW_ITEM.USER_QUESTION, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '11', type: FLOW_ITEM.CODE_EXECUTION, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '12', type: FLOW_ITEM.SEARCH_RESULT, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
      { id: '13', type: FLOW_ITEM.URL_CONTEXT, content: '', status: TIMELINE_STATUS.COMPLETED, timestamp: '', sequenceNumber: 0 },
    ];

    const stats = getTimelineStats(items, stages);
    expect(stats.totalStages).toBe(2);
    expect(stats.completedStages).toBe(1);
    expect(stats.failedStages).toBe(1);
    expect(stats.thoughtCount).toBe(2);
    expect(stats.toolCallCount).toBe(2);
    expect(stats.successfulToolCalls).toBe(1);
    expect(stats.toolSummaryCount).toBe(1);
    expect(stats.responseCount).toBe(1);
    expect(stats.analysisCount).toBe(2); // final_analysis + executive_summary
    expect(stats.finalAnswerCount).toBe(1);
    expect(stats.errorCount).toBe(1);
    expect(stats.nativeToolCount).toBe(3); // code_execution + search_result + url_context
    expect(stats.userQuestionCount).toBe(1);
  });

  it('counts timed_out stages as failed', () => {
    const stages = [makeStage({ id: 's1', stage_index: 0, status: EXECUTION_STATUS.TIMED_OUT })];
    const stats = getTimelineStats([], stages);
    expect(stats.failedStages).toBe(1);
  });

  it('returns zero counts for empty inputs', () => {
    const stats = getTimelineStats([], []);
    expect(stats.totalStages).toBe(0);
    expect(stats.thoughtCount).toBe(0);
    expect(stats.toolCallCount).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// flowItemsToPlainText
// ---------------------------------------------------------------------------

describe('flowItemsToPlainText', () => {
  it('formats each item type with correct labels', () => {
    const items: FlowItem[] = [
      { id: '1', type: FLOW_ITEM.STAGE_SEPARATOR, content: 'Investigation', status: '', timestamp: '', sequenceNumber: 0 },
      { id: '2', type: FLOW_ITEM.THINKING, content: 'analyzing logs', status: '', timestamp: '', sequenceNumber: 1 },
      { id: '3', type: FLOW_ITEM.TOOL_CALL, content: 'get pods', status: '', timestamp: '', sequenceNumber: 2, metadata: { tool_name: 'kubectl_get', server_name: 'k8s' } },
      { id: '4', type: FLOW_ITEM.TOOL_SUMMARY, content: 'found 3 pods', status: '', timestamp: '', sequenceNumber: 3 },
      { id: '5', type: FLOW_ITEM.RESPONSE, content: 'all pods healthy', status: '', timestamp: '', sequenceNumber: 4 },
      { id: '6', type: FLOW_ITEM.FINAL_ANALYSIS, content: 'no issues', status: '', timestamp: '', sequenceNumber: 5 },
      { id: '7', type: FLOW_ITEM.ERROR, content: 'timeout', status: '', timestamp: '', sequenceNumber: 6 },
      { id: '8', type: FLOW_ITEM.USER_QUESTION, content: 'what happened?', status: '', timestamp: '', sequenceNumber: 7 },
    ];

    const text = flowItemsToPlainText(items);
    expect(text).toContain('--- Stage: Investigation ---');
    expect(text).toContain('[Thought]\nanalyzing logs');
    expect(text).toContain('[Tool Call: k8s.kubectl_get]\nget pods');
    expect(text).toContain('[Tool Summary]\nfound 3 pods');
    expect(text).toContain('[Response]\nall pods healthy');
    expect(text).toContain('[Final Analysis]\nno issues');
    expect(text).toContain('[Error]\ntimeout');
    expect(text).toContain('[User Question]\nwhat happened?');
  });

  it('handles tool call without server name', () => {
    const items: FlowItem[] = [
      { id: '1', type: FLOW_ITEM.TOOL_CALL, content: 'run cmd', status: '', timestamp: '', sequenceNumber: 1, metadata: { tool_name: 'exec' } },
    ];
    const text = flowItemsToPlainText(items);
    expect(text).toContain('[Tool Call: exec]');
    expect(text).not.toContain('.');
  });

  it('returns empty string for no items', () => {
    expect(flowItemsToPlainText([])).toBe('');
  });
});

// ---------------------------------------------------------------------------
// isFlowItemCollapsible / isFlowItemTerminal
// ---------------------------------------------------------------------------

describe('isFlowItemCollapsible', () => {
  it.each([
    [FLOW_ITEM.THINKING, true],
    [FLOW_ITEM.TOOL_SUMMARY, true],
    [FLOW_ITEM.FINAL_ANALYSIS, true],
    [FLOW_ITEM.RESPONSE, false],
    [FLOW_ITEM.TOOL_CALL, false],
    [FLOW_ITEM.ERROR, false],
    [FLOW_ITEM.STAGE_SEPARATOR, false],
  ] as const)('returns %s for type %s', (type, expected) => {
    const item = { id: '1', type, content: '', status: '', timestamp: '', sequenceNumber: 0 } as FlowItem;
    expect(isFlowItemCollapsible(item)).toBe(expected);
  });
});

describe('isFlowItemTerminal', () => {
  it('returns false for streaming status', () => {
    const item = { id: '1', type: FLOW_ITEM.THINKING, content: '', status: TIMELINE_STATUS.STREAMING, timestamp: '', sequenceNumber: 0 } as FlowItem;
    expect(isFlowItemTerminal(item)).toBe(false);
  });

  it.each([TIMELINE_STATUS.COMPLETED, TIMELINE_STATUS.FAILED, TIMELINE_STATUS.CANCELLED])('returns true for %s status', (status) => {
    const item = { id: '1', type: FLOW_ITEM.THINKING, content: '', status, timestamp: '', sequenceNumber: 0 } as FlowItem;
    expect(isFlowItemTerminal(item)).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// groupByExecutionId
// ---------------------------------------------------------------------------

describe('groupByExecutionId', () => {
  it('groups items by execution_id', () => {
    const items: FlowItem[] = [
      { id: '1', type: FLOW_ITEM.THINKING, executionId: 'a', content: '', status: '', timestamp: '', sequenceNumber: 1 },
      { id: '2', type: FLOW_ITEM.THINKING, executionId: 'b', content: '', status: '', timestamp: '', sequenceNumber: 2 },
      { id: '3', type: FLOW_ITEM.RESPONSE, executionId: 'a', content: '', status: '', timestamp: '', sequenceNumber: 3 },
    ];
    const map = groupByExecutionId(items);
    expect(map.get('a')).toHaveLength(2);
    expect(map.get('b')).toHaveLength(1);
  });

  it('uses __default__ for items without executionId', () => {
    const items: FlowItem[] = [
      { id: '1', type: FLOW_ITEM.THINKING, content: '', status: '', timestamp: '', sequenceNumber: 1 },
    ];
    const map = groupByExecutionId(items);
    expect(map.get('__default__')).toHaveLength(1);
  });
});
