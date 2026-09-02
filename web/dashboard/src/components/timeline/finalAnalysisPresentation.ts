import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';
import { STAGE_TYPE } from '../../constants/eventTypes';

export interface FinalAnalysisPresentation {
  label: string;
  emoji: string;
  color: string;
}

/** Human-readable wrap-up reasons; matches backend WrapUpReason.Display(). */
const WRAP_UP_REASON_LABEL: Record<string, string> = {
  max_iterations: 'max iterations',
  time_budget: 'time budget',
};

function forcedConclusionSuffix(metadata: Record<string, unknown> | undefined): string {
  const raw = typeof metadata?.reason === 'string' ? metadata.reason : '';
  const why = WRAP_UP_REASON_LABEL[raw];
  if (why) {
    return ` (forced conclusion — ${why})`;
  }
  return ' (forced conclusion)';
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

  const suffix = isForcedConclusion ? forcedConclusionSuffix(metadata) : '';
  switch (stageType) {
    case STAGE_TYPE.CHAT:
      return { label: `ANSWER${suffix}`, emoji: '🎯', color: 'success.main' };
    case STAGE_TYPE.ACTION:
      return { label: `RESULT${suffix}`, emoji: '🎯', color: 'success.main' };
    case STAGE_TYPE.COMPOSE:
      return { label: `AMENDED REPORT${suffix}`, emoji: '🎯', color: 'success.main' };
    default:
      return { label: `CONCLUSION${suffix}`, emoji: '🎯', color: 'success.main' };
  }
}
