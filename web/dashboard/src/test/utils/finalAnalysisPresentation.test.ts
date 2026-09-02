import { describe, expect, it } from 'vitest';
import { getFinalAnalysisPresentation } from '../../components/timeline/finalAnalysisPresentation';
import { STAGE_TYPE } from '../../constants/eventTypes';
import { LLM_INTERACTION_TYPE } from '../../constants/interactionTypes';

describe('getFinalAnalysisPresentation', () => {
  it('keeps the stage label and puts wrap-up reason on the badge', () => {
    const maxIter = getFinalAnalysisPresentation(
      { reason: 'max_iterations' },
      STAGE_TYPE.INVESTIGATION,
      true,
    );
    expect(maxIter.label).toBe('CONCLUSION');
    expect(maxIter.color).toBe('success.main');
    expect(maxIter.wrapUpBadge).toBe('forced — max iterations');

    const timeBudgetChat = getFinalAnalysisPresentation(
      { reason: 'time_budget' },
      STAGE_TYPE.CHAT,
      true,
    );
    expect(timeBudgetChat.label).toBe('ANSWER');
    expect(timeBudgetChat.wrapUpBadge).toBe('forced — time budget');

    expect(
      getFinalAnalysisPresentation({ reason: 'time_budget' }, STAGE_TYPE.ACTION, true).wrapUpBadge,
    ).toBe('forced — time budget');
  });

  it('uses a generic badge when reason is missing or unknown', () => {
    expect(getFinalAnalysisPresentation({}, STAGE_TYPE.INVESTIGATION, true).wrapUpBadge).toBe(
      'forced conclusion',
    );
    expect(
      getFinalAnalysisPresentation({ reason: 'unknown' }, STAGE_TYPE.CHAT, true).wrapUpBadge,
    ).toBe('forced conclusion');
  });

  it('omits the badge when the conclusion was not forced', () => {
    const normal = getFinalAnalysisPresentation({}, undefined, false);
    expect(normal.label).toBe('CONCLUSION');
    expect(normal.color).toBe('success.main');
    expect(normal.wrapUpBadge).toBeUndefined();
  });

  it('still labels synthesis from metadata', () => {
    const synthesis = getFinalAnalysisPresentation(
      { interaction_type: LLM_INTERACTION_TYPE.SYNTHESIS },
      STAGE_TYPE.INVESTIGATION,
      true,
    );
    expect(synthesis.label).toBe('SYNTHESIS');
    expect(synthesis.wrapUpBadge).toBeUndefined();
  });
});
