/**
 * Session deep-link resolution: map ?stage=&event= query params onto
 * loaded stages / FlowItems for expand + scroll focus.
 */

import { sessionDetailPath } from '../constants/routes';
import type { StageOverview } from '../types/session';
import { FLOW_ITEM, type FlowItem } from './timelineParser';

export type DeepLinkScrollTarget =
  | { kind: 'event'; eventId: string }
  | { kind: 'stageSeparator'; stageId: string };

export type DeepLinkResult =
  | { kind: 'none' }
  | { kind: 'miss' }
  | {
      kind: 'stage';
      stageId: string;
      scrollTarget: DeepLinkScrollTarget;
    }
  | {
      kind: 'event';
      stageId: string;
      eventId: string;
      scrollTarget: DeepLinkScrollTarget;
    }
  | {
      kind: 'eventFallback';
      stageId: string;
      scrollTarget: DeepLinkScrollTarget;
    };

function stageExists(stages: StageOverview[], stageId: string): boolean {
  return stages.some((s) => s.id === stageId);
}

/** First non-separator FlowItem for a stage, in parser/timeline order. */
export function firstRealEventInStage(flowItems: FlowItem[], stageId: string): FlowItem | undefined {
  return flowItems.find(
    (item) => item.stageId === stageId && item.type !== FLOW_ITEM.STAGE_SEPARATOR,
  );
}

function stageScrollTarget(flowItems: FlowItem[], stageId: string): DeepLinkScrollTarget {
  const first = firstRealEventInStage(flowItems, stageId);
  if (first) return { kind: 'event', eventId: first.id };
  return { kind: 'stageSeparator', stageId };
}

/**
 * Resolve deep-link query params against loaded session data.
 * Does not touch the DOM — callers emit focusRequest and scroll.
 */
export function resolveDeepLink(
  stageParam: string | null | undefined,
  eventParam: string | null | undefined,
  stages: StageOverview[],
  flowItems: FlowItem[],
): DeepLinkResult {
  const stageRaw = stageParam?.trim() || '';
  const eventRaw = eventParam?.trim() || '';

  if (!stageRaw && !eventRaw) return { kind: 'none' };

  let stageId = stageRaw;
  const eventId = eventRaw;

  // Normalize: event without stage → look up stage from matching FlowItem
  if (!stageId && eventId) {
    const match = flowItems.find(
      (item) => item.id === eventId && item.type !== FLOW_ITEM.STAGE_SEPARATOR,
    );
    if (!match?.stageId) return { kind: 'miss' };
    stageId = match.stageId;
  }

  if (!stageId || !stageExists(stages, stageId)) {
    return { kind: 'miss' };
  }

  const scrollTarget = stageScrollTarget(flowItems, stageId);

  if (!eventId) {
    return { kind: 'stage', stageId, scrollTarget };
  }

  const eventItem = flowItems.find(
    (item) => item.id === eventId && item.type !== FLOW_ITEM.STAGE_SEPARATOR,
  );

  if (!eventItem || eventItem.stageId !== stageId) {
    return { kind: 'eventFallback', stageId, scrollTarget };
  }

  return {
    kind: 'event',
    stageId,
    eventId,
    scrollTarget: { kind: 'event', eventId },
  };
}

/** Absolute URL for clipboard copy of a session deep link. */
export function sessionDeepLinkUrl(
  sessionId: string,
  options: { stage?: string; event?: string },
  origin: string = typeof window !== 'undefined' ? window.location.origin : '',
): string {
  return `${origin}${sessionDetailPath(sessionId, options)}`;
}

export function isOptimisticTimelineId(id: string): boolean {
  return id.startsWith('temp-');
}
