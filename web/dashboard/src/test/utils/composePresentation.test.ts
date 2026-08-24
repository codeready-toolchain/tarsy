import { describe, expect, it } from 'vitest';
import {
  COLLAPSIBLE_STAGE_TYPES,
  STAGE_TYPE,
} from '../../constants/eventTypes';
import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';
import { getFinalAnalysisPresentation } from '../../components/timeline/finalAnalysisPresentation';
import { getInteractionTypeLabel } from '../../components/trace/traceHelpers';
import { FAILED_EXECUTION_STATUSES } from '../../constants/sessionStatus';

describe('compose stage presentation', () => {
  it('labels compose final analysis as AMENDED REPORT and action as RESULT', () => {
    expect(getFinalAnalysisPresentation({}, STAGE_TYPE.COMPOSE, false).label).toBe(
      'AMENDED REPORT',
    );
    expect(getFinalAnalysisPresentation({}, STAGE_TYPE.ACTION, false).label).toBe('RESULT');
  });

  it('includes compose in the collapsible stage set', () => {
    expect(COLLAPSIBLE_STAGE_TYPES.has(STAGE_TYPE.COMPOSE)).toBe(true);
  });

  it('labels composition interaction type', () => {
    expect(getInteractionTypeLabel(LLM_INTERACTION_TYPE.COMPOSITION)).toBe('Composition');
  });

  it('treats failed compose chrome as a failed execution status', () => {
    expect(FAILED_EXECUTION_STATUSES.has('failed')).toBe(true);
  });
});
