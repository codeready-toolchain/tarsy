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
  scoring_status: string;
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

/** Active dashboard tab. */
export type DashboardTab = 'sessions' | 'triage';

/** Filter state for the triage view. */
export interface TriageFilter {
  assignee: 'all' | 'mine' | 'unassigned';
}

/** Persisted Usage page filters (time window + optional drill-down). */
export interface UsagePageFilters {
  /** Preset value (e.g. '7d', 'mtd'); null means a custom start/end. */
  date_preset: string | null;
  start_date: string | null; // RFC3339, used when date_preset is null
  end_date: string | null; // RFC3339, used when date_preset is null
  alert_type: string | null;
  chain_id: string | null;
  rank_by?: 'cost' | 'tokens';
}
