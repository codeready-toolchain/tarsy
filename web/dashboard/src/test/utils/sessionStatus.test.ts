import {
  FAILED_EXECUTION_STATUSES,
  CANCELLED_EXECUTION_STATUSES,
  MAX_CANCEL_REASON_LENGTH,
  cancelReasonRuneCount,
  isCancelReasonOverLimit,
} from '../../constants/sessionStatus';

describe('sessionStatus cancel helpers', () => {
  it('does not treat cancelled as a failed execution status', () => {
    expect(FAILED_EXECUTION_STATUSES.has('failed')).toBe(true);
    expect(FAILED_EXECUTION_STATUSES.has('timed_out')).toBe(true);
    expect(FAILED_EXECUTION_STATUSES.has('cancelled')).toBe(false);
    expect(CANCELLED_EXECUTION_STATUSES.has('cancelled')).toBe(true);
  });

  it('counts unicode runes, not UTF-16 code units', () => {
    expect(cancelReasonRuneCount('😀')).toBe(1);
    expect(isCancelReasonOverLimit('😀'.repeat(MAX_CANCEL_REASON_LENGTH))).toBe(false);
    expect(isCancelReasonOverLimit('😀'.repeat(MAX_CANCEL_REASON_LENGTH + 1))).toBe(true);
  });
});
