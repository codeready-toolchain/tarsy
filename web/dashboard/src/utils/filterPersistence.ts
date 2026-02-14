/**
 * localStorage persistence for dashboard filter, pagination, and sort state.
 */

import type { SessionFilter, PaginationState, SortState } from '../types/dashboard.ts';

// Storage keys
const FILTER_KEY = 'tarsy-filters';
const PAGINATION_KEY = 'tarsy-pagination';
const SORT_KEY = 'tarsy-sort';

// ────────────────────────────────────────────────────────────
// Defaults
// ────────────────────────────────────────────────────────────

export function getDefaultFilters(): SessionFilter {
  return {
    search: '',
    status: [],
    alert_type: '',
    chain_id: '',
    start_date: null,
    end_date: null,
    date_preset: null,
  };
}

export function getDefaultPagination(): PaginationState {
  return {
    page: 1,
    pageSize: 25,
    totalPages: 1,
    totalItems: 0,
  };
}

export function getDefaultSort(): SortState {
  return {
    field: 'created_at',
    direction: 'desc',
  };
}

// ────────────────────────────────────────────────────────────
// Merge helper
// ────────────────────────────────────────────────────────────

/** Merge saved partial state with defaults. Saved values win where present. */
export function mergeWithDefaults<T extends object>(
  saved: Partial<T> | null,
  defaults: T,
): T {
  if (!saved) return defaults;
  return { ...defaults, ...saved };
}

// ────────────────────────────────────────────────────────────
// Filters
// ────────────────────────────────────────────────────────────

export function saveFiltersToStorage(filters: SessionFilter): void {
  try {
    localStorage.setItem(FILTER_KEY, JSON.stringify(filters));
  } catch {
    // quota exceeded or private browsing — silently ignore
  }
}

export function loadFiltersFromStorage(): SessionFilter | null {
  try {
    const raw = localStorage.getItem(FILTER_KEY);
    if (raw) return JSON.parse(raw) as SessionFilter;
  } catch {
    // corrupted data — ignore
  }
  return null;
}

export function clearFiltersFromStorage(): void {
  try {
    localStorage.removeItem(FILTER_KEY);
  } catch {
    // ignore
  }
}

// ────────────────────────────────────────────────────────────
// Pagination
// ────────────────────────────────────────────────────────────

export function savePaginationToStorage(pagination: Partial<PaginationState>): void {
  try {
    const existing = loadPaginationFromStorage() ?? {};
    localStorage.setItem(PAGINATION_KEY, JSON.stringify({ ...existing, ...pagination }));
  } catch {
    // ignore
  }
}

export function loadPaginationFromStorage(): Partial<PaginationState> | null {
  try {
    const raw = localStorage.getItem(PAGINATION_KEY);
    if (raw) return JSON.parse(raw) as Partial<PaginationState>;
  } catch {
    // ignore
  }
  return null;
}

// ────────────────────────────────────────────────────────────
// Sort
// ────────────────────────────────────────────────────────────

export function saveSortToStorage(sort: SortState): void {
  try {
    localStorage.setItem(SORT_KEY, JSON.stringify(sort));
  } catch {
    // ignore
  }
}

export function loadSortFromStorage(): SortState | null {
  try {
    const raw = localStorage.getItem(SORT_KEY);
    if (raw) return JSON.parse(raw) as SortState;
  } catch {
    // ignore
  }
  return null;
}

// ────────────────────────────────────────────────────────────
// Clear all
// ────────────────────────────────────────────────────────────

export function clearAllDashboardState(): void {
  clearFiltersFromStorage();
  try {
    localStorage.removeItem(PAGINATION_KEY);
    localStorage.removeItem(SORT_KEY);
  } catch {
    // ignore
  }
}
