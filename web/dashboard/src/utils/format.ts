/**
 * Formatting utilities for timestamps, durations, and tokens.
 *
 * All timestamp functions work with RFC3339 / ISO 8601 strings
 * (the format returned by the new Go backend).
 */

import { formatDistanceToNow, format, parseISO, isValid } from 'date-fns';

// ────────────────────────────────────────────────────────────
// Timestamps
// ────────────────────────────────────────────────────────────

type TimestampFormat = 'relative' | 'absolute' | 'short' | 'time-only' | 'date-only';

/**
 * Format an ISO/RFC3339 timestamp string for display.
 *
 * @param isoString - RFC3339 timestamp from the backend (e.g. "2025-12-19T14:30:45Z")
 * @param formatType - Display format
 */
export function formatTimestamp(
  isoString: string | null | undefined,
  formatType: TimestampFormat = 'relative',
): string {
  if (!isoString) return '—';

  const date = parseISO(isoString);
  if (!isValid(date)) return 'Invalid date';

  switch (formatType) {
    case 'relative':
      return formatDistanceToNow(date, { addSuffix: true });
    case 'absolute':
      return format(date, 'PPpp'); // "Dec 19, 2024 at 2:30:45 PM"
    case 'short':
      return format(date, 'MMM dd, HH:mm:ss'); // "Dec 19, 14:30:45"
    case 'time-only':
      return format(date, 'HH:mm:ss'); // "14:30:45"
    case 'date-only':
      return format(date, 'PPP'); // "December 19th, 2024"
    default:
      return date.toLocaleString();
  }
}

/**
 * Relative time string (e.g. "5 minutes ago").
 * Convenience wrapper around formatTimestamp('relative').
 */
export function timeAgo(isoString: string | null | undefined): string {
  return formatTimestamp(isoString, 'relative');
}

// ────────────────────────────────────────────────────────────
// Durations
// ────────────────────────────────────────────────────────────

/**
 * Format a duration in milliseconds to a human-readable string.
 *
 * Examples: "0s", "12s", "2m 30s", "1h 5m 10s"
 */
export function formatDurationMs(durationMs: number | null | undefined): string {
  if (durationMs == null || durationMs < 0) return '—';

  if (durationMs < 1000) return '< 1s';

  if (durationMs < 60_000) {
    return `${Math.round(durationMs / 1000)}s`;
  }

  if (durationMs < 3_600_000) {
    const minutes = Math.floor(durationMs / 60_000);
    const seconds = Math.round((durationMs % 60_000) / 1000);
    return `${minutes}m ${seconds}s`;
  }

  const hours = Math.floor(durationMs / 3_600_000);
  const minutes = Math.floor((durationMs % 3_600_000) / 60_000);
  const seconds = Math.round((durationMs % 60_000) / 1000);
  return `${hours}h ${minutes}m ${seconds}s`;
}

/**
 * Compute a live duration from a start timestamp to now.
 * Returns a formatted string (e.g. "2m 30s").
 */
export function liveDuration(startIso: string | null | undefined): string {
  if (!startIso) return '—';
  const start = parseISO(startIso);
  if (!isValid(start)) return '—';
  return formatDurationMs(Date.now() - start.getTime());
}

// ────────────────────────────────────────────────────────────
// Tokens
// ────────────────────────────────────────────────────────────

/**
 * Format a token count with locale-aware thousands separator.
 */
export function formatTokens(tokens: number | null | undefined): string {
  if (tokens == null) return '—';
  return tokens.toLocaleString();
}

/**
 * Compact token format for tight spaces: "1.5K" for >= 1000, raw number otherwise.
 */
export function formatTokensCompact(tokens: number | null | undefined): string {
  if (tokens == null) return '—';
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(1)}M`;
  }
  if (tokens >= 1_000) {
    return `${(tokens / 1_000).toFixed(1)}K`;
  }
  return String(tokens);
}
