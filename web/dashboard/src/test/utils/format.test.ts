/**
 * Tests for format.ts
 *
 * Covers: formatTimestamp, formatDurationMs, formatTokens, formatTokensCompact
 */

import {
  formatTimestamp,
  formatDurationMs,
  formatTokens,
  formatTokensCompact,
  timeAgo,
  liveDuration,
} from '../../utils/format';

// ---------------------------------------------------------------------------
// formatTimestamp
// ---------------------------------------------------------------------------

describe('formatTimestamp', () => {
  it('returns "—" for null', () => {
    expect(formatTimestamp(null)).toBe('—');
  });

  it('returns "—" for undefined', () => {
    expect(formatTimestamp(undefined)).toBe('—');
  });

  it('returns "—" for empty string', () => {
    expect(formatTimestamp('')).toBe('—');
  });

  it('returns "Invalid date" for malformed string', () => {
    expect(formatTimestamp('not-a-date')).toBe('Invalid date');
  });

  it('formats relative time', () => {
    // Use a recent timestamp so formatDistanceToNow produces "less than a minute ago" or similar
    const recent = new Date(Date.now() - 30_000).toISOString();
    const result = formatTimestamp(recent, 'relative');
    expect(result).toContain('ago');
  });

  it('formats absolute time', () => {
    const result = formatTimestamp('2025-06-15T14:30:45Z', 'absolute');
    // format(date, 'PPpp') → something like "Jun 15, 2025 at 2:30:45 PM" (locale-dependent)
    expect(result).toMatch(/Jun|2025|14:30|2:30/);
  });

  it('formats short time', () => {
    const result = formatTimestamp('2025-06-15T14:30:45Z', 'short');
    // format(date, 'MMM dd, HH:mm:ss') → "Jun 15, 14:30:45"
    expect(result).toMatch(/Jun 15/);
  });

  it('formats time-only', () => {
    const result = formatTimestamp('2025-06-15T14:30:45Z', 'time-only');
    // Depends on timezone, but should contain digits and colons
    expect(result).toMatch(/\d{2}:\d{2}:\d{2}/);
  });

  it('formats date-only', () => {
    const result = formatTimestamp('2025-06-15T14:30:45Z', 'date-only');
    // format(date, 'PPP') → something like "June 15th, 2025"
    expect(result).toMatch(/June|Jun|15|2025/);
  });

  it('defaults to relative format', () => {
    const recent = new Date(Date.now() - 60_000).toISOString();
    const result = formatTimestamp(recent);
    expect(result).toContain('ago');
  });
});

// ---------------------------------------------------------------------------
// timeAgo
// ---------------------------------------------------------------------------

describe('timeAgo', () => {
  it('returns "—" for null', () => {
    expect(timeAgo(null)).toBe('—');
  });

  it('returns relative time for valid timestamp', () => {
    const recent = new Date(Date.now() - 120_000).toISOString();
    expect(timeAgo(recent)).toContain('ago');
  });
});

// ---------------------------------------------------------------------------
// formatDurationMs
// ---------------------------------------------------------------------------

describe('formatDurationMs', () => {
  it('returns "—" for null', () => {
    expect(formatDurationMs(null)).toBe('—');
  });

  it('returns "—" for undefined', () => {
    expect(formatDurationMs(undefined)).toBe('—');
  });

  it('returns "—" for negative values', () => {
    expect(formatDurationMs(-100)).toBe('—');
  });

  it('returns "< 1s" for sub-second durations', () => {
    expect(formatDurationMs(0)).toBe('< 1s');
    expect(formatDurationMs(500)).toBe('< 1s');
    expect(formatDurationMs(999)).toBe('< 1s');
  });

  it('formats seconds', () => {
    expect(formatDurationMs(1000)).toBe('1s');
    expect(formatDurationMs(12_000)).toBe('12s');
    expect(formatDurationMs(59_500)).toBe('60s');
  });

  it('formats minutes and seconds', () => {
    expect(formatDurationMs(60_000)).toBe('1m 0s');
    expect(formatDurationMs(150_000)).toBe('2m 30s');
    expect(formatDurationMs(3_599_000)).toBe('59m 59s');
  });

  it('formats hours, minutes, and seconds', () => {
    expect(formatDurationMs(3_600_000)).toBe('1h 0m 0s');
    expect(formatDurationMs(3_910_000)).toBe('1h 5m 10s');
    expect(formatDurationMs(7_200_000)).toBe('2h 0m 0s');
  });
});

// ---------------------------------------------------------------------------
// liveDuration
// ---------------------------------------------------------------------------

describe('liveDuration', () => {
  it('returns "—" for null', () => {
    expect(liveDuration(null)).toBe('—');
  });

  it('returns formatted duration from start to now', () => {
    const twoMinutesAgo = new Date(Date.now() - 120_000).toISOString();
    const result = liveDuration(twoMinutesAgo);
    expect(result).toMatch(/2m|1m|3m/); // approximate due to timing
  });

  it('returns "—" for invalid date', () => {
    expect(liveDuration('not-a-date')).toBe('—');
  });
});

// ---------------------------------------------------------------------------
// formatTokens
// ---------------------------------------------------------------------------

describe('formatTokens', () => {
  it('returns "—" for null', () => {
    expect(formatTokens(null)).toBe('—');
  });

  it('formats with locale separator', () => {
    const result = formatTokens(1234567);
    // Locale-dependent, but should contain digits and separators
    expect(result).toMatch(/1.?234.?567/);
  });

  it('formats small numbers', () => {
    expect(formatTokens(42)).toBe('42');
  });
});

// ---------------------------------------------------------------------------
// formatTokensCompact
// ---------------------------------------------------------------------------

describe('formatTokensCompact', () => {
  it('returns "—" for null', () => {
    expect(formatTokensCompact(null)).toBe('—');
  });

  it('returns raw number for < 1000', () => {
    expect(formatTokensCompact(0)).toBe('0');
    expect(formatTokensCompact(999)).toBe('999');
  });

  it('formats thousands as K', () => {
    expect(formatTokensCompact(1000)).toBe('1.0K');
    expect(formatTokensCompact(1500)).toBe('1.5K');
    expect(formatTokensCompact(12345)).toBe('12.3K');
    expect(formatTokensCompact(999999)).toBe('1000.0K');
  });

  it('formats millions as M', () => {
    expect(formatTokensCompact(1_000_000)).toBe('1.0M');
    expect(formatTokensCompact(1_500_000)).toBe('1.5M');
    expect(formatTokensCompact(10_500_000)).toBe('10.5M');
  });
});
