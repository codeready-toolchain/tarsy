import { EVENT_REVIEW_STATUS, EVENT_SESSION_PROGRESS, EVENT_SESSION_STATUS } from '../../constants/eventTypes';
import { SESSION_STATUS } from '../../constants/sessionStatus';
import {
  needsAnotherPage,
  selectToastCandidates,
  shouldRefreshSessionEvent,
  SUMMARY_LIMIT,
  toastBody,
  toastTitle,
  type QualifierRow,
} from '../../utils/notificationDecision';
import { faviconBadgeText, tabTitle } from '../../utils/tabIndicator';

function row(overrides: Partial<QualifierRow> = {}): QualifierRow {
  return {
    id: 's1',
    alert_type: 'oom-kill',
    labels: ['ban', 'action'],
    executive_summary: 'Disk filled on the node.',
    ...overrides,
  };
}

function decide(overrides: Partial<Parameters<typeof selectToastCandidates>[0]> = {}) {
  return selectToastCandidates({
    baseline: false,
    desktop: true,
    hidden: true,
    permission: 'granted',
    seen: new Set(),
    rows: [row()],
    ...overrides,
  });
}

describe('notification decision', () => {
  it('toasts a new match only while the tab is hidden and desktop notifications are allowed', () => {
    expect(decide()).toEqual([{
      id: 's1',
      title: 'oom-kill · ban, action',
      body: 'Disk filled on the node.',
    }]);
    expect(decide({ baseline: true })).toEqual([]);
    expect(decide({ hidden: false })).toEqual([]);
    expect(decide({ desktop: false })).toEqual([]);
    expect(decide({ permission: 'denied' })).toEqual([]);
    expect(decide({ permission: 'default' })).toEqual([]);
    expect(decide({ permission: 'unsupported' })).toEqual([]);
    expect(decide({ seen: new Set(['s1']) })).toEqual([]);
    expect(decide({ rows: [] })).toEqual([]);
  });

  it('identifies a session when the alert type is missing and omits an empty summary', () => {
    expect(toastTitle(row({ alert_type: null, labels: ['ban'] }))).toBe('Session s1 · ban');
    expect(toastTitle(row({ alert_type: '   ', labels: [] }))).toBe('Session s1');
    expect(toastTitle(row({ labels: null }))).toBe('oom-kill');
    expect(toastBody(null)).toBeUndefined();
    expect(toastBody('  \n  ')).toBeUndefined();
    expect(toastBody('line\n\nnext')).toBe('line next');

    const long = 'x'.repeat(SUMMARY_LIMIT + 20);
    const body = toastBody(long);
    expect(body).toHaveLength(SUMMARY_LIMIT);
    expect(body?.endsWith('…')).toBe(true);
  });

  it('wakes on a completed session or any review change', () => {
    expect(shouldRefreshSessionEvent({ type: EVENT_SESSION_STATUS, status: SESSION_STATUS.COMPLETED })).toBe(true);
    expect(shouldRefreshSessionEvent({ type: EVENT_SESSION_STATUS, status: SESSION_STATUS.FAILED })).toBe(false);
    expect(shouldRefreshSessionEvent({ type: EVENT_SESSION_PROGRESS })).toBe(false);
    expect(shouldRefreshSessionEvent({ type: EVENT_REVIEW_STATUS })).toBe(true);
  });

  it('fetches another page only when the count grew past the unseen ids already in hand', () => {
    const common = {
      baseline: false,
      page: 1,
      maxPages: 3,
      totalPages: 4,
      previousTotal: 10,
      total: 160,
      unseenOnFetchedPages: 100,
    };
    expect(needsAnotherPage(common)).toBe(true);
    expect(needsAnotherPage({ ...common, baseline: true })).toBe(false);
    expect(needsAnotherPage({ ...common, unseenOnFetchedPages: 150 })).toBe(false);
    expect(needsAnotherPage({ ...common, total: 12, unseenOnFetchedPages: 2 })).toBe(false);
    expect(needsAnotherPage({ ...common, page: 3 })).toBe(false);
    expect(needsAnotherPage({ ...common, previousTotal: null })).toBe(false);
  });

  it('shows the real total in the title and 99+ on the icon', () => {
    expect(tabTitle(null)).toBe('TARSy');
    expect(tabTitle(0)).toBe('(0) TARSy');
    expect(tabTitle(120)).toBe('(120) TARSy');
    expect(faviconBadgeText(99)).toBe('99');
    expect(faviconBadgeText(100)).toBe('99+');
  });
});
