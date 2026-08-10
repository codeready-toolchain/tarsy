/**
 * Tests for deepLink.ts — session deep-link resolution.
 */

import { sessionDetailPath } from '../../constants/routes';
import {
  firstRealEventInStage,
  isOptimisticTimelineId,
  resolveDeepLink,
  sessionDeepLinkUrl,
} from '../../utils/deepLink';
import {
  FLOW_ITEM,
  parseTimelineToFlow,
  type FlowItem,
} from '../../utils/timelineParser';
import type { StageOverview, TimelineEvent } from '../../types/session';
import { TIMELINE_EVENT_TYPES, TIMELINE_STATUS } from '../../constants/eventTypes';
import { EXECUTION_STATUS } from '../../constants/sessionStatus';

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
    stage_type: 'investigation',
    status: EXECUTION_STATUS.COMPLETED,
    parallel_type: null,
    expected_agent_count: 1,
    started_at: '2025-01-15T10:00:00Z',
    completed_at: '2025-01-15T10:05:00Z',
    ...overrides,
  };
}

function makeFlowItem(overrides: Partial<FlowItem> & { id: string }): FlowItem {
  return {
    type: FLOW_ITEM.THINKING,
    stageId: 'stage-1',
    content: 'content',
    status: TIMELINE_STATUS.COMPLETED,
    timestamp: '2025-01-15T10:00:00Z',
    sequenceNumber: 1,
    ...overrides,
  };
}

describe('sessionDetailPath', () => {
  it('returns bare path without options', () => {
    expect(sessionDetailPath('abc')).toBe('/sessions/abc');
  });

  it('encodes stage only', () => {
    expect(sessionDetailPath('abc', { stage: 's1' })).toBe('/sessions/abc?stage=s1');
  });

  it('encodes stage and event', () => {
    expect(sessionDetailPath('abc', { stage: 's1', event: 'e1' })).toBe(
      '/sessions/abc?stage=s1&event=e1',
    );
  });
});

describe('sessionDeepLinkUrl', () => {
  it('prefixes origin', () => {
    expect(sessionDeepLinkUrl('abc', { stage: 's1' }, 'https://example.com')).toBe(
      'https://example.com/sessions/abc?stage=s1',
    );
  });
});

describe('isOptimisticTimelineId', () => {
  it('detects temp-* ids', () => {
    expect(isOptimisticTimelineId('temp-123')).toBe(true);
    expect(isOptimisticTimelineId('evt-1')).toBe(false);
  });
});

describe('resolveDeepLink', () => {
  const invStage = makeStage({ id: 'stage-inv', stage_index: 0, stage_name: 'Investigation' });
  const chatStage = makeStage({
    id: 'stage-chat',
    stage_index: 1,
    stage_name: 'Chat',
    stage_type: 'chat',
  });
  const emptyStage = makeStage({
    id: 'stage-empty',
    stage_index: 2,
    stage_name: 'Empty',
  });

  const events = [
    makeEvent({ id: 'think-1', stage_id: 'stage-inv', sequence_number: 1 }),
    makeEvent({
      id: 'resp-1',
      stage_id: 'stage-inv',
      sequence_number: 2,
      event_type: TIMELINE_EVENT_TYPES.LLM_RESPONSE,
    }),
    makeEvent({
      id: 'q-1',
      stage_id: 'stage-chat',
      sequence_number: 99,
      event_type: TIMELINE_EVENT_TYPES.USER_QUESTION,
      content: 'Why?',
    }),
    makeEvent({
      id: 'answer-1',
      stage_id: 'stage-chat',
      sequence_number: 1,
      event_type: TIMELINE_EVENT_TYPES.FINAL_ANALYSIS,
      content: 'Because',
    }),
  ];

  const stages = [invStage, chatStage, emptyStage];
  const flowItems = parseTimelineToFlow(events, stages);

  it('returns none when both params absent', () => {
    expect(resolveDeepLink(null, null, stages, flowItems)).toEqual({ kind: 'none' });
    expect(resolveDeepLink('', '', stages, flowItems)).toEqual({ kind: 'none' });
  });

  it('stage link scrolls to first real event', () => {
    const result = resolveDeepLink('stage-inv', null, stages, flowItems);
    expect(result).toEqual({
      kind: 'stage',
      stageId: 'stage-inv',
      scrollTarget: { kind: 'event', eventId: 'think-1' },
    });
  });

  it('chat stage first real event is user question', () => {
    const result = resolveDeepLink('stage-chat', undefined, stages, flowItems);
    expect(result.kind).toBe('stage');
    if (result.kind !== 'stage') return;
    expect(result.scrollTarget).toEqual({ kind: 'event', eventId: 'q-1' });
    expect(firstRealEventInStage(flowItems, 'stage-chat')?.type).toBe(FLOW_ITEM.USER_QUESTION);
  });

  it('stage + valid event focuses that event', () => {
    expect(resolveDeepLink('stage-inv', 'resp-1', stages, flowItems)).toEqual({
      kind: 'event',
      stageId: 'stage-inv',
      eventId: 'resp-1',
      scrollTarget: { kind: 'event', eventId: 'resp-1' },
    });
  });

  it('missing stage is miss', () => {
    expect(resolveDeepLink('nope', null, stages, flowItems)).toEqual({ kind: 'miss' });
  });

  it('valid stage + bad event falls back to stage', () => {
    expect(resolveDeepLink('stage-inv', 'missing-evt', stages, flowItems)).toEqual({
      kind: 'eventFallback',
      stageId: 'stage-inv',
      scrollTarget: { kind: 'event', eventId: 'think-1' },
    });
  });

  it('event without stage resolves stage from item', () => {
    expect(resolveDeepLink(null, 'resp-1', stages, flowItems)).toEqual({
      kind: 'event',
      stageId: 'stage-inv',
      eventId: 'resp-1',
      scrollTarget: { kind: 'event', eventId: 'resp-1' },
    });
  });

  it('event without stage and unknown event is miss', () => {
    expect(resolveDeepLink(null, 'unknown', stages, flowItems)).toEqual({ kind: 'miss' });
  });

  it('event in wrong stage falls back to stage', () => {
    expect(resolveDeepLink('stage-chat', 'think-1', stages, flowItems)).toEqual({
      kind: 'eventFallback',
      stageId: 'stage-chat',
      scrollTarget: { kind: 'event', eventId: 'q-1' },
    });
  });

  it('empty stage scrolls to stage separator', () => {
    expect(resolveDeepLink('stage-empty', null, stages, flowItems)).toEqual({
      kind: 'stage',
      stageId: 'stage-empty',
      scrollTarget: { kind: 'stageSeparator', stageId: 'stage-empty' },
    });
  });

  it('ignores stage separator synthetic ids as events', () => {
    const withSep = [
      makeFlowItem({ id: 'stage-sep-stage-inv', type: FLOW_ITEM.STAGE_SEPARATOR, stageId: 'stage-inv' }),
      makeFlowItem({ id: 'think-1', stageId: 'stage-inv' }),
    ];
    expect(resolveDeepLink(null, 'stage-sep-stage-inv', stages, withSep)).toEqual({ kind: 'miss' });
  });
});
