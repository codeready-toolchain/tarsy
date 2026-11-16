import { describe, expect, it } from 'vitest';
import {
  COLLAPSIBLE_STAGE_TYPES,
  STAGE_TYPE,
} from '../../constants/eventTypes';
import { getFinalAnalysisPresentation } from '../../components/timeline/finalAnalysisPresentation';

describe('post-investigation stage presentation', () => {
  it('labels post-investigation final analysis as RESULT', () => {
    expect(getFinalAnalysisPresentation({}, STAGE_TYPE.POST_INVESTIGATION, false).label).toBe('RESULT');
  });

  it('keeps the wrap-up badge on forced conclusions', () => {
    const p = getFinalAnalysisPresentation({ reason: 'max_iterations' }, STAGE_TYPE.POST_INVESTIGATION, true);
    expect(p.label).toBe('RESULT');
    expect(p.wrapUpBadge).toBe('max iterations');
  });

  it('includes post-investigation in the collapsible stage set', () => {
    expect(COLLAPSIBLE_STAGE_TYPES.has(STAGE_TYPE.POST_INVESTIGATION)).toBe(true);
  });
});
