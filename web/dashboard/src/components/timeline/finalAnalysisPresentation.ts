import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';
import { STAGE_TYPE } from '../../constants/eventTypes';

export interface FinalAnalysisPresentation {
  label: string;
  emoji: string;
  color: string;
  /** Filled warning-chip text when wrap-up was forced; omitted on a normal finish. */
  wrapUpBadge?: string;
}

/** Human-readable wrap-up reasons; matches backend WrapUpReason.Display(). */
const WRAP_UP_REASON_LABEL: Record<string, string> = {
  max_iterations: 'max iterations',
  time_budget: 'time budget',
};

function wrapUpBadgeLabel(metadata: Record<string, unknown> | undefined): string {
  const raw = typeof metadata?.reason === 'string' ? metadata.reason : '';
  const why = WRAP_UP_REASON_LABEL[raw];
  return why ? `forced — ${why}` : 'forced conclusion';
}

/**
 * Returns context-aware label, emoji, and color for a final_analysis timeline event.
 * Handles synthesis (from metadata), then stage type (chat, action), defaulting to
 * investigation/conclusion. Does NOT handle reflector/memory_extraction — callers
 * branch on that separately since it uses a completely different UI.
 */
export function getFinalAnalysisPresentation(
  metadata: Record<string, unknown> | undefined,
  stageType: string | undefined,
  isForcedConclusion: boolean,
): FinalAnalysisPresentation {
  if ((metadata?.interaction_type as string | undefined) === LLM_INTERACTION_TYPE.SYNTHESIS) {
    return { label: 'SYNTHESIS', emoji: '🔀', color: 'success.main' };
  }

  const wrapUpBadge = isForcedConclusion ? wrapUpBadgeLabel(metadata) : undefined;
  switch (stageType) {
    case STAGE_TYPE.CHAT:
      return { label: 'ANSWER', emoji: '🎯', color: 'success.main', wrapUpBadge };
    case STAGE_TYPE.ACTION:
      return { label: 'RESULT', emoji: '🎯', color: 'success.main', wrapUpBadge };
    case STAGE_TYPE.COMPOSE:
      return { label: 'AMENDED REPORT', emoji: '🎯', color: 'success.main', wrapUpBadge };
    default:
      return { label: 'CONCLUSION', emoji: '🎯', color: 'success.main', wrapUpBadge };
  }
}
