/**
 * Search-related utilities for the dashboard.
 */

import { createElement, type ReactNode } from 'react';
import type { SessionFilter } from '../types/dashboard.ts';

/**
 * Highlight occurrences of `searchTerm` in `text` by wrapping matches in <mark>.
 * Returns an array of ReactNodes suitable for rendering.
 *
 * If searchTerm is empty or text is null, returns the original text.
 */
export function highlightSearchTermNodes(
  text: string | null | undefined,
  searchTerm: string,
): ReactNode[] {
  if (!text || !searchTerm.trim()) return [text ?? ''];

  const escaped = searchTerm.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Split with a capturing group yields alternating [non-match, match, non-match, ...]
  // so odd indices (i % 2 === 1) are always the captured matches.
  const parts = text.split(new RegExp(`(${escaped})`, 'i'));

  return parts.map((part, i) =>
    i % 2 === 1
      ? createElement('mark', { key: i, style: { background: '#fff59d', padding: '0 1px' } }, part)
      : part,
  );
}

/**
 * Check whether any filter field is actively set (i.e. differs from defaults).
 */
export function hasActiveFilters(filters: SessionFilter): boolean {
  return Boolean(
    (filters.search && filters.search.trim().length >= 3) ||
      filters.status.length > 0 ||
      filters.alert_type ||
      filters.chain_id ||
      filters.start_date ||
      filters.end_date ||
      filters.date_preset,
  );
}
