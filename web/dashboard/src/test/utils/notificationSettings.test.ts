import {
  applyLabelSelection,
  claimSeenId,
  defaultNotificationSettings,
  LABEL_ALL,
  LABEL_ANY,
  LABEL_NONE,
  loadNotificationSettings,
  loadSeenIds,
  mergeLabelSuggestions,
  NOTIFICATION_SEEN_KEY,
  NOTIFICATION_SETTINGS_KEY,
  qualifierParams,
  rememberSeenIds,
  saveNotificationSettings,
} from '../../utils/notificationSettings';
import { REVIEW_STATUS } from '../../types/api';
import { SESSION_STATUS } from '../../constants/sessionStatus';

describe('notification settings', () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('uses defaults when nothing is saved', () => {
    expect(loadNotificationSettings()).toEqual(defaultNotificationSettings());
  });

  it('keeps a saved empty label selection', () => {
    saveNotificationSettings({
      master: true,
      tabCounter: false,
      desktop: true,
      labels: [],
    });
    expect(loadNotificationSettings().labels).toEqual([]);
    expect(loadNotificationSettings().tabCounter).toBe(false);
  });

  it('falls back to defaults when saved settings are unreadable', () => {
    localStorage.setItem(NOTIFICATION_SETTINGS_KEY, '{');
    expect(loadNotificationSettings()).toEqual(defaultNotificationSettings());

    localStorage.setItem(NOTIFICATION_SETTINGS_KEY, JSON.stringify({ master: true }));
    expect(loadNotificationSettings()).toEqual(defaultNotificationSettings());
  });

  it('stores Any label together with No label as All sessions', () => {
    saveNotificationSettings({
      master: true,
      tabCounter: true,
      desktop: true,
      labels: [LABEL_ANY, 'ban', LABEL_NONE],
    });
    expect(loadNotificationSettings().labels).toEqual([LABEL_ALL]);
  });

  describe('applyLabelSelection', () => {
    it('lets Any label replace specific names', () => {
      expect(applyLabelSelection(['ban', 'action'], ['ban', 'action', LABEL_ANY])).toEqual([LABEL_ANY]);
    });

    it('lets All sessions replace the other chips', () => {
      expect(applyLabelSelection(['ban', LABEL_NONE], ['ban', LABEL_NONE, LABEL_ALL])).toEqual([LABEL_ALL]);
    });

    it('lets a specific name replace All sessions', () => {
      expect(applyLabelSelection([LABEL_ALL], [LABEL_ALL, 'ban'])).toEqual(['ban']);
    });

    it('lets a specific name replace Any label and keep No label', () => {
      expect(applyLabelSelection([LABEL_ANY, LABEL_NONE], [LABEL_ANY, LABEL_NONE, 'ban']))
        .toEqual(['ban', LABEL_NONE]);
    });

    it('stacks No label with names', () => {
      expect(applyLabelSelection(['ban'], ['ban', LABEL_NONE])).toEqual(['ban', LABEL_NONE]);
    });

    it('keeps an empty selection empty and drops invalid tokens', () => {
      expect(applyLabelSelection(['ban'], [])).toEqual([]);
      expect(applyLabelSelection([], ['1bad', 'ban', 'ban'])).toEqual(['ban']);
    });
  });

  it('merges catalog names, stored names, and the current selection', () => {
    expect(mergeLabelSuggestions(['watch', 'action'], ['ban'], ['custom'])).toEqual([
      'action',
      'ban',
      'custom',
      'watch',
    ]);
  });

  describe('qualifier query', () => {
    const base = {
      status: SESSION_STATUS.COMPLETED,
      review_status: REVIEW_STATUS.NEEDS_REVIEW,
      page: 1,
      page_size: 100,
      sort_by: 'completed_at',
      sort_order: 'desc' as const,
    };

    it('matches nothing when the field is empty', () => {
      expect(qualifierParams([])).toEqual({ kind: 'empty' });
    });

    it('sends names without a scope', () => {
      expect(qualifierParams(['ban', 'action'])).toEqual({
        kind: 'query',
        params: { ...base, label: 'ban,action' },
      });
    });

    it('sends none, names or none, any label, and all sessions', () => {
      expect(qualifierParams([LABEL_NONE])).toEqual({
        kind: 'query',
        params: { ...base, label_scope: 'none' },
      });
      expect(qualifierParams(['ban', LABEL_NONE])).toEqual({
        kind: 'query',
        params: { ...base, label: 'ban', label_scope: 'names_or_none' },
      });
      expect(qualifierParams([LABEL_ANY])).toEqual({
        kind: 'query',
        params: { ...base, label_scope: 'any' },
      });
      expect(qualifierParams([LABEL_ALL])).toEqual({
        kind: 'query',
        params: base,
      });
      expect(qualifierParams([LABEL_ANY, LABEL_NONE])).toEqual({
        kind: 'query',
        params: base,
      });
    });
  });

  it('claims an id once and drops the oldest half when storage is full', () => {
    expect(claimSeenId('s1')).toBe(true);
    expect(claimSeenId('s1')).toBe(false);

    localStorage.clear();
    const real = Storage.prototype.setItem;
    let failed = false;
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === NOTIFICATION_SEEN_KEY && !failed) {
        failed = true;
        throw new DOMException('quota', 'QuotaExceededError');
      }
      return real.call(this, key, value);
    });

    rememberSeenIds(['a', 'b', 'c', 'd']);
    expect(loadSeenIds()).toEqual(['c', 'd']);
  });
});
