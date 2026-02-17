/**
 * Tests for search.ts
 *
 * Covers: highlightSearchTermNodes, hasActiveFilters
 */

import { isValidElement, type ReactElement } from 'react';
import { highlightSearchTermNodes, hasActiveFilters } from '../../utils/search';
import type { SessionFilter } from '../../types/dashboard';

// ---------------------------------------------------------------------------
// highlightSearchTermNodes
// ---------------------------------------------------------------------------

describe('highlightSearchTermNodes', () => {
  it('returns original text when searchTerm is empty', () => {
    const result = highlightSearchTermNodes('hello world', '');
    expect(result).toEqual(['hello world']);
  });

  it('returns original text when searchTerm is whitespace', () => {
    const result = highlightSearchTermNodes('hello', '   ');
    expect(result).toEqual(['hello']);
  });

  it('returns empty string for null text', () => {
    const result = highlightSearchTermNodes(null, 'search');
    expect(result).toEqual(['']);
  });

  it('returns empty string for undefined text', () => {
    const result = highlightSearchTermNodes(undefined, 'search');
    expect(result).toEqual(['']);
  });

  it('wraps matching text in mark elements', () => {
    const result = highlightSearchTermNodes('hello world', 'world');
    // split('(world)', 'i') → ['hello ', 'world', ''] — 3 parts
    expect(result).toHaveLength(3);
    expect(result[0]).toBe('hello ');
    expect(isValidElement(result[1])).toBe(true);
    const markEl = result[1] as ReactElement;
    expect(markEl.type).toBe('mark');
    expect((markEl as ReactElement<{ children: string }>).props.children).toBe('world');
  });

  it('applies correct mark styles', () => {
    const result = highlightSearchTermNodes('test match here', 'match');
    const markEl = result[1] as ReactElement<{ style: Record<string, string> }>;
    expect(markEl.props.style).toEqual({ background: '#fff59d', padding: '0 1px' });
  });

  it('is case-insensitive', () => {
    const result = highlightSearchTermNodes('Hello WORLD', 'world');
    expect(result).toHaveLength(3); // 'Hello ', <mark>WORLD</mark>, ''
    const markEl = result[1] as ReactElement<{ children: string }>;
    expect(markEl.props.children).toBe('WORLD');
  });

  it('highlights multiple occurrences', () => {
    const result = highlightSearchTermNodes('foo bar foo baz foo', 'foo');
    // Split with capturing group: ['', 'foo', ' bar ', 'foo', ' baz ', 'foo', '']
    const markElements = result.filter((node) => isValidElement(node));
    expect(markElements).toHaveLength(3);
  });

  it('escapes regex special characters in search term', () => {
    const result = highlightSearchTermNodes('price is $100.00 total', '$100.00');
    const marks = result.filter((node) => isValidElement(node));
    expect(marks).toHaveLength(1);
    expect((marks[0] as ReactElement<{ children: string }>).props.children).toBe('$100.00');
  });

  it('handles search term at start of text', () => {
    const result = highlightSearchTermNodes('nginx pod crashed', 'nginx');
    // split → ['', 'nginx', ' pod crashed'] — index 0 is empty, index 1 is mark
    expect(result[0]).toBe('');
    expect(isValidElement(result[1])).toBe(true);
    expect((result[1] as ReactElement<{ children: string }>).props.children).toBe('nginx');
  });

  it('handles search term at end of text', () => {
    const result = highlightSearchTermNodes('pod is healthy', 'healthy');
    // The last non-empty element should be a mark or followed by empty string
    const marks = result.filter((node) => isValidElement(node));
    expect(marks).toHaveLength(1);
  });

  it('returns original text when no match found', () => {
    const result = highlightSearchTermNodes('hello world', 'xyz');
    expect(result).toEqual(['hello world']);
  });
});

// ---------------------------------------------------------------------------
// hasActiveFilters
// ---------------------------------------------------------------------------

describe('hasActiveFilters', () => {
  const defaultFilters: SessionFilter = {
    search: '',
    status: [],
    alert_type: '',
    chain_id: '',
    start_date: null,
    end_date: null,
    date_preset: null,
  };

  it('returns false for default filters', () => {
    expect(hasActiveFilters(defaultFilters)).toBe(false);
  });

  it('returns true when search has 3+ characters', () => {
    expect(hasActiveFilters({ ...defaultFilters, search: 'pod' })).toBe(true);
  });

  it('returns false when search has < 3 characters', () => {
    expect(hasActiveFilters({ ...defaultFilters, search: 'po' })).toBe(false);
  });

  it('ignores whitespace-only short search', () => {
    expect(hasActiveFilters({ ...defaultFilters, search: '  ' })).toBe(false);
  });

  it('returns true when status is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, status: ['completed'] })).toBe(true);
  });

  it('returns true when alert_type is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, alert_type: 'prometheus' })).toBe(true);
  });

  it('returns true when chain_id is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, chain_id: 'test-chain' })).toBe(true);
  });

  it('returns true when start_date is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, start_date: '2025-01-15T00:00:00Z' })).toBe(true);
  });

  it('returns true when end_date is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, end_date: '2025-01-15T23:59:59Z' })).toBe(true);
  });

  it('returns true when date_preset is set', () => {
    expect(hasActiveFilters({ ...defaultFilters, date_preset: '1d' })).toBe(true);
  });

  it('returns true for multiple active filters', () => {
    expect(
      hasActiveFilters({
        ...defaultFilters,
        search: 'nginx crash',
        status: ['failed'],
        alert_type: 'prometheus',
      }),
    ).toBe(true);
  });
});
