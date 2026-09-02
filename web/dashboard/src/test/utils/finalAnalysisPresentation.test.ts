import { describe, expect, it } from 'vitest';
import { getFinalAnalysisPresentation } from '../../components/timeline/finalAnalysisPresentation';
import { STAGE_TYPE } from '../../constants/eventTypes';
import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';

describe('getFinalAnalysisPresentation', () => {
  it('appends the wrap-up reason when metadata has one', () => {
    expect(
      getFinalAnalysisPresentation(
        { reason: 'max_iterations' },
        STAGE_TYPE.INVESTIGATION,
        true,
      ).label,
    ).toBe('CONCLUSION (forced conclusion — max iterations)');
    expect(
      getFinalAnalysisPresentation({ reason: 'time_budget' }, STAGE_TYPE.CHAT, true).label,
    ).toBe('ANSWER (forced conclusion — time budget)');
    expect(
      getFinalAnalysisPresentation({ reason: 'time_budget' }, STAGE_TYPE.ACTION, true).label,
    ).toBe('RESULT (forced conclusion — time budget)');
  });

  it('keeps a generic suffix when reason is missing or unknown', () => {
    expect(getFinalAnalysisPresentation({}, STAGE_TYPE.INVESTIGATION, true).label).toBe(
      'CONCLUSION (forced conclusion)',
    );
    expect(
      getFinalAnalysisPresentation({ reason: 'unknown' }, STAGE_TYPE.CHAT, true).label,
    ).toBe('ANSWER (forced conclusion)');
  });

  it('omits the suffix when the conclusion was not forced', () => {
    expect(getFinalAnalysisPresentation({}, undefined, false).label).toBe('CONCLUSION');
  });

  it('still labels synthesis from metadata', () => {
    expect(
      getFinalAnalysisPresentation(
        { interaction_type: LLM_INTERACTION_TYPE.SYNTHESIS },
        STAGE_TYPE.INVESTIGATION,
        true,
      ).label,
    ).toBe('SYNTHESIS');
  });
});
