/**
 * Pure rules for when a qualifier refresh updates the counter or produces a toast.
 * The watcher supplies document.hidden, permission, and the seen set.
 */

import { EVENT_REVIEW_STATUS, EVENT_SESSION_STATUS } from '../constants/eventTypes.ts';
import { SESSION_STATUS } from '../constants/sessionStatus.ts';

export const SUMMARY_LIMIT = 180;
export const QUALIFIER_MAX_PAGES = 3;

export interface QualifierRow {
  id: string;
  alert_type: string | null;
  labels: string[] | null;
  executive_summary: string | null;
}

export type BrowserNotificationPermission = NotificationPermission | 'unsupported';

export interface ToastDecisionInput {
  /** First result after arming, a label change, or the master switch turning on. */
  baseline: boolean;
  desktop: boolean;
  hidden: boolean;
  permission: BrowserNotificationPermission;
  seen: ReadonlySet<string>;
  rows: readonly QualifierRow[];
}

export interface ToastCandidate {
  id: string;
  title: string;
  body?: string;
}

export function toastTitle(row: QualifierRow): string {
  const labels = (row.labels ?? []).filter((label) => label !== '');
  const base = row.alert_type?.trim() ? row.alert_type.trim() : `Session ${row.id}`;
  return labels.length > 0 ? `${base} · ${labels.join(', ')}` : base;
}

export function toastBody(summary: string | null | undefined): string | undefined {
  if (summary == null) return undefined;
  const collapsed = summary.replace(/\s+/g, ' ').trim();
  if (!collapsed) return undefined;
  if (collapsed.length <= SUMMARY_LIMIT) return collapsed;
  return `${collapsed.slice(0, SUMMARY_LIMIT - 1)}…`;
}

export function selectToastCandidates(input: ToastDecisionInput): ToastCandidate[] {
  if (input.baseline || !input.desktop || !input.hidden || input.permission !== 'granted') {
    return [];
  }
  const candidates: ToastCandidate[] = [];
  for (const row of input.rows) {
    if (!row.id || input.seen.has(row.id)) continue;
    const body = toastBody(row.executive_summary);
    candidates.push(body ? { id: row.id, title: toastTitle(row), body } : { id: row.id, title: toastTitle(row) });
  }
  return candidates;
}

export function shouldRefreshSessionEvent(data: { type?: unknown; status?: unknown }): boolean {
  if (data.type === EVENT_SESSION_STATUS) return data.status === SESSION_STATUS.COMPLETED;
  return data.type === EVENT_REVIEW_STATUS;
}

export function needsAnotherPage(input: {
  baseline: boolean;
  page: number;
  maxPages: number;
  totalPages: number;
  previousTotal: number | null;
  total: number;
  unseenOnFetchedPages: number;
}): boolean {
  if (input.baseline || input.previousTotal == null) return false;
  const delta = input.total - input.previousTotal;
  if (delta <= 0) return false;
  if (input.unseenOnFetchedPages >= delta) return false;
  if (input.page >= input.maxPages || input.page >= input.totalPages) return false;
  return true;
}
