/**
 * Dashboard-local state types for filters, pagination, and sorting.
 *
 * Backend response types live in api.ts, session.ts, system.ts.
 * These types represent UI-only state that drives API queries.
 */

/** Filter state for the session list. */
export interface SessionFilter {
  search: string;
  status: string[];
  alert_type: string;
  chain_id: string;
  start_date: string | null; // RFC3339
  end_date: string | null; // RFC3339
  date_preset: string | null; // '10m' | '1h' | '12h' | '1d' | '7d' | '30d' | null (custom)
}

/** Pagination state tracking both local page and backend totals. */
export interface PaginationState {
  page: number;
  pageSize: number;
  totalPages: number;
  totalItems: number;
}

/** Sort state for the historical sessions table. */
export interface SortState {
  field: string;
  direction: 'asc' | 'desc';
}
