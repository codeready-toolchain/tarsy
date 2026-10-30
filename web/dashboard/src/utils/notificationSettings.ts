/**
 * Browser-local review-notification settings and the label selection that
 * feeds both the tab counter and desktop toasts.
 */

import type { DashboardListParams } from '../types/api.ts';
import { REVIEW_STATUS } from '../types/api.ts';
import { SESSION_STATUS } from '../constants/sessionStatus.ts';

export const NOTIFICATION_SETTINGS_KEY = 'tarsy-notification-settings';
export const NOTIFICATION_SEEN_KEY = 'tarsy-notification-seen';

/** Not a valid label token, so it cannot collide with a stored name. */
export const LABEL_ANY = '__any__';
/** Not a valid label token, so it cannot collide with a stored name. */
export const LABEL_NONE = '__none__';

export const LABEL_TOKEN_RE = /^[A-Za-z][A-Za-z0-9_-]*$/;

export const QUALIFIER_PAGE_SIZE = 100;

export interface NotificationSettings {
  master: boolean;
  tabCounter: boolean;
  desktop: boolean;
  labels: string[];
}

export function isLabelName(token: string): boolean {
  return LABEL_TOKEN_RE.test(token);
}

export function defaultNotificationSettings(): NotificationSettings {
  return {
    master: true,
    tabCounter: true,
    desktop: true,
    labels: ['ban', 'action'],
  };
}

export function normalizeLabelSelection(labels: readonly string[]): string[] {
  const names: string[] = [];
  const seen = new Set<string>();
  let any = false;
  let none = false;
  for (const raw of labels) {
    const token = raw.trim();
    if (token === LABEL_ANY) {
      any = true;
      continue;
    }
    if (token === LABEL_NONE) {
      none = true;
      continue;
    }
    if (!isLabelName(token) || seen.has(token)) continue;
    seen.add(token);
    names.push(token);
  }
  // Any label already covers every name, so a stored mix keeps Any label.
  if (any) {
    return none ? [LABEL_ANY, LABEL_NONE] : [LABEL_ANY];
  }
  return none ? [...names, LABEL_NONE] : names;
}

function uniqueSelection(labels: readonly string[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of labels) {
    const token = raw.trim();
    if (token !== LABEL_ANY && token !== LABEL_NONE && !isLabelName(token)) continue;
    if (seen.has(token)) continue;
    seen.add(token);
    out.push(token);
  }
  return out;
}

/**
 * Apply a multi-select change. Any label and specific names replace each other.
 * No label stacks with either. An empty selection stays empty.
 */
export function applyLabelSelection(previous: readonly string[], next: readonly string[]): string[] {
  const prevAny = previous.includes(LABEL_ANY);
  const tokens = uniqueSelection(next);
  const nextAny = tokens.includes(LABEL_ANY);
  const nextNone = tokens.includes(LABEL_NONE);
  const nextNames = tokens.filter((token) => token !== LABEL_ANY && token !== LABEL_NONE);

  if (nextAny && !prevAny) {
    return nextNone ? [LABEL_ANY, LABEL_NONE] : [LABEL_ANY];
  }
  if (prevAny && nextNames.some((name) => !previous.includes(name))) {
    return nextNone ? [...nextNames, LABEL_NONE] : nextNames;
  }
  if (nextAny) {
    return nextNone ? [LABEL_ANY, LABEL_NONE] : [LABEL_ANY];
  }
  return nextNone ? [...nextNames, LABEL_NONE] : nextNames;
}

export function mergeLabelSuggestions(
  catalog: readonly string[],
  stored: readonly string[],
  selected: readonly string[],
): string[] {
  const names = new Set<string>();
  for (const name of [...catalog, ...stored, ...selected]) {
    if (isLabelName(name)) names.add(name);
  }
  return [...names].sort((a, b) => a.localeCompare(b));
}

function isSettings(value: unknown): value is NotificationSettings {
  if (!value || typeof value !== 'object') return false;
  const record = value as Partial<NotificationSettings>;
  return typeof record.master === 'boolean'
    && typeof record.tabCounter === 'boolean'
    && typeof record.desktop === 'boolean'
    && Array.isArray(record.labels)
    && record.labels.every((label) => typeof label === 'string');
}

export function loadNotificationSettings(): NotificationSettings {
  try {
    const raw = localStorage.getItem(NOTIFICATION_SETTINGS_KEY);
    if (!raw) return defaultNotificationSettings();
    const parsed: unknown = JSON.parse(raw);
    if (!isSettings(parsed)) return defaultNotificationSettings();
    return {
      master: parsed.master,
      tabCounter: parsed.tabCounter,
      desktop: parsed.desktop,
      labels: normalizeLabelSelection(parsed.labels),
    };
  } catch {
    return defaultNotificationSettings();
  }
}

export function saveNotificationSettings(settings: NotificationSettings): void {
  try {
    localStorage.setItem(NOTIFICATION_SETTINGS_KEY, JSON.stringify(settings));
  } catch {
    // Quota or private mode — keep the in-memory choice for this page view.
  }
}

export function loadSeenIds(): string[] {
  try {
    const raw = localStorage.getItem(NOTIFICATION_SEEN_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((id): id is string => typeof id === 'string' && id !== '');
  } catch {
    return [];
  }
}

function writeSeenIds(ids: string[]): void {
  const payload = JSON.stringify(ids);
  try {
    localStorage.setItem(NOTIFICATION_SEEN_KEY, payload);
  } catch {
    const kept = ids.slice(Math.ceil(ids.length / 2));
    try {
      localStorage.setItem(NOTIFICATION_SEEN_KEY, JSON.stringify(kept));
    } catch {
      // Still full — leave the previous value in place.
    }
  }
}

export function rememberSeenIds(ids: readonly string[]): void {
  if (ids.length === 0) return;
  const seen = loadSeenIds();
  const known = new Set(seen);
  let changed = false;
  for (const id of ids) {
    if (!id || known.has(id)) continue;
    known.add(id);
    seen.push(id);
    changed = true;
  }
  if (changed) writeSeenIds(seen);
}

/** Claim an id before constructing a notification. False when another tab already stored it. */
export function claimSeenId(id: string): boolean {
  const seen = loadSeenIds();
  if (seen.includes(id)) return false;
  seen.push(id);
  writeSeenIds(seen);
  return loadSeenIds().includes(id);
}

export type QualifierRequest =
  | { kind: 'empty' }
  | { kind: 'query'; params: DashboardListParams };

/** Map the label field onto the session list. An empty field matches nothing. */
export function qualifierParams(labels: readonly string[], page = 1): QualifierRequest {
  const normalized = normalizeLabelSelection(labels);
  const names = normalized.filter((token) => token !== LABEL_ANY && token !== LABEL_NONE);
  const any = normalized.includes(LABEL_ANY);
  const none = normalized.includes(LABEL_NONE);
  if (!any && !none && names.length === 0) return { kind: 'empty' };

  const params: DashboardListParams = {
    status: SESSION_STATUS.COMPLETED,
    review_status: REVIEW_STATUS.NEEDS_REVIEW,
    page,
    page_size: QUALIFIER_PAGE_SIZE,
    sort_by: 'completed_at',
    sort_order: 'desc',
  };
  if (any && none) return { kind: 'query', params };
  if (any) return { kind: 'query', params: { ...params, label_scope: 'any' } };
  if (none && names.length === 0) return { kind: 'query', params: { ...params, label_scope: 'none' } };
  if (none) {
    return {
      kind: 'query',
      params: { ...params, label: names.join(','), label_scope: 'names_or_none' },
    };
  }
  return { kind: 'query', params: { ...params, label: names.join(',') } };
}
