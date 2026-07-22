import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';
import { STAGE_TYPE } from '../../constants/eventTypes';

export interface FinalAnalysisPresentation {
  label: string;
  emoji: string;
  color: string;
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

  const suffix = isForcedConclusion ? ' (⚠️Max Iterations)' : '';
  switch (stageType) {
    case STAGE_TYPE.CHAT:
      return { label: `ANSWER${suffix}`, emoji: '🎯', color: 'success.main' };
    case STAGE_TYPE.ACTION:
      return { label: `RESULT${suffix}`, emoji: '🎯', color: 'success.main' };
    default:
      return { label: `CONCLUSION${suffix}`, emoji: '🎯', color: 'success.main' };
  }
}
