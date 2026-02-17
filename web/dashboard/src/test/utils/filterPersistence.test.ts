/**
 * Tests for filterPersistence.ts
 *
 * Covers: defaults, save/load/clear for filters, pagination, sort,
 *         mergeWithDefaults, clearAllDashboardState.
 */

import {
  getDefaultFilters,
  getDefaultPagination,
  getDefaultSort,
  mergeWithDefaults,
  saveFiltersToStorage,
  loadFiltersFromStorage,
  clearFiltersFromStorage,
  savePaginationToStorage,
  loadPaginationFromStorage,
  saveSortToStorage,
  loadSortFromStorage,
  clearAllDashboardState,
} from '../../utils/filterPersistence';
import type { SessionFilter, SortState } from '../../types/dashboard';

// ---------------------------------------------------------------------------
// localStorage mock
// ---------------------------------------------------------------------------

const localStorageMock = (() => {
  let store: Record<string, string> = {};
  return {
    getItem: vi.fn((key: string) => store[key] ?? null),
    setItem: vi.fn((key: string, value: string) => { store[key] = value; }),
    removeItem: vi.fn((key: string) => { delete store[key]; }),
    clear: vi.fn(() => { store = {}; }),
    get length() { return Object.keys(store).length; },
    key: vi.fn((index: number) => Object.keys(store)[index] ?? null),
  };
})();

Object.defineProperty(global, 'localStorage', { value: localStorageMock });

beforeEach(() => {
  localStorageMock.clear();
  vi.clearAllMocks();
});

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

describe('getDefaultFilters', () => {
  it('returns correct default filter shape', () => {
    const defaults = getDefaultFilters();
    expect(defaults).toEqual({
      search: '',
      status: [],
      alert_type: '',
      chain_id: '',
      start_date: null,
      end_date: null,
      date_preset: null,
    });
  });
});

describe('getDefaultPagination', () => {
  it('returns correct default pagination', () => {
    const defaults = getDefaultPagination();
    expect(defaults).toEqual({
      page: 1,
      pageSize: 25,
      totalPages: 1,
      totalItems: 0,
    });
  });
});

describe('getDefaultSort', () => {
  it('returns created_at desc', () => {
    const defaults = getDefaultSort();
    expect(defaults).toEqual({ field: 'created_at', direction: 'desc' });
  });
});

// ---------------------------------------------------------------------------
// mergeWithDefaults
// ---------------------------------------------------------------------------

describe('mergeWithDefaults', () => {
  it('returns defaults when saved is null', () => {
    const defaults = getDefaultFilters();
    expect(mergeWithDefaults(null, defaults)).toEqual(defaults);
  });

  it('merges saved values over defaults', () => {
    const defaults = getDefaultFilters();
    const saved = { search: 'nginx', status: ['completed'] };
    const result = mergeWithDefaults(saved, defaults);
    expect(result.search).toBe('nginx');
    expect(result.status).toEqual(['completed']);
    expect(result.alert_type).toBe(''); // default preserved
  });

  it('saved values win over defaults', () => {
    const defaults = getDefaultSort();
    const saved = { field: 'started_at', direction: 'asc' as const };
    const result = mergeWithDefaults(saved, defaults);
    expect(result.field).toBe('started_at');
    expect(result.direction).toBe('asc');
  });
});

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

describe('filter persistence', () => {
  it('saves and loads filters', () => {
    const filters: SessionFilter = {
      search: 'pod crash',
      status: ['failed'],
      alert_type: 'prometheus',
      chain_id: 'test-chain',
      start_date: '2025-01-15T00:00:00Z',
      end_date: null,
      date_preset: '1d',
    };
    saveFiltersToStorage(filters);
    const loaded = loadFiltersFromStorage();
    expect(loaded).toEqual(filters);
  });

  it('returns null when nothing saved', () => {
    expect(loadFiltersFromStorage()).toBeNull();
  });

  it('returns null for corrupted data', () => {
    localStorageMock.setItem('tarsy-filters', '{invalid json');
    // loadFiltersFromStorage catches parse errors
    expect(loadFiltersFromStorage()).toBeNull();
  });

  it('clears filters', () => {
    saveFiltersToStorage(getDefaultFilters());
    clearFiltersFromStorage();
    expect(loadFiltersFromStorage()).toBeNull();
  });

  it('silently handles storage errors on save', () => {
    localStorageMock.setItem.mockImplementationOnce(() => {
      throw new Error('QuotaExceededError');
    });
    expect(() => saveFiltersToStorage(getDefaultFilters())).not.toThrow();
  });
});

// ---------------------------------------------------------------------------
// Pagination
// ---------------------------------------------------------------------------

describe('pagination persistence', () => {
  it('saves and loads pagination', () => {
    savePaginationToStorage({ page: 3, pageSize: 50 });
    const loaded = loadPaginationFromStorage();
    expect(loaded?.page).toBe(3);
    expect(loaded?.pageSize).toBe(50);
  });

  it('merges with existing pagination on save', () => {
    savePaginationToStorage({ page: 2 });
    savePaginationToStorage({ pageSize: 50 });
    const loaded = loadPaginationFromStorage();
    expect(loaded?.page).toBe(2);
    expect(loaded?.pageSize).toBe(50);
  });

  it('returns null when nothing saved', () => {
    expect(loadPaginationFromStorage()).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// Sort
// ---------------------------------------------------------------------------

describe('sort persistence', () => {
  it('saves and loads sort', () => {
    const sort: SortState = { field: 'started_at', direction: 'asc' };
    saveSortToStorage(sort);
    expect(loadSortFromStorage()).toEqual(sort);
  });

  it('returns null when nothing saved', () => {
    expect(loadSortFromStorage()).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// clearAllDashboardState
// ---------------------------------------------------------------------------

describe('clearAllDashboardState', () => {
  it('clears all storage keys', () => {
    saveFiltersToStorage(getDefaultFilters());
    savePaginationToStorage({ page: 5 });
    saveSortToStorage({ field: 'created_at', direction: 'desc' });

    clearAllDashboardState();

    expect(loadFiltersFromStorage()).toBeNull();
    expect(loadPaginationFromStorage()).toBeNull();
    expect(loadSortFromStorage()).toBeNull();
  });
});
