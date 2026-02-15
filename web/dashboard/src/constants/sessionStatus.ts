/**
 * Session, stage, and execution status constants and helpers.
 */

export const SESSION_STATUS = {
  PENDING: 'pending',
  IN_PROGRESS: 'in_progress',
  CANCELLING: 'cancelling',
  COMPLETED: 'completed',
  FAILED: 'failed',
  CANCELLED: 'cancelled',
  TIMED_OUT: 'timed_out',
} as const;

/**
 * Execution / stage status values (shared by both agent_execution and stage
 * entities on the backend). The frontend also derives a 'started' pseudo-status
 * when no items have been persisted yet.
 */
export const EXECUTION_STATUS = {
  PENDING: 'pending',
  ACTIVE: 'active',
  STARTED: 'started', // frontend-only, derived when no items yet
  COMPLETED: 'completed',
  FAILED: 'failed',
  CANCELLED: 'cancelled',
  TIMED_OUT: 'timed_out',
} as const;

export type ExecutionStatus = (typeof EXECUTION_STATUS)[keyof typeof EXECUTION_STATUS];

/** Terminal execution/stage statuses — execution will not change further. */
export const TERMINAL_EXECUTION_STATUSES = new Set<string>([
  EXECUTION_STATUS.COMPLETED,
  EXECUTION_STATUS.FAILED,
  EXECUTION_STATUS.CANCELLED,
  EXECUTION_STATUS.TIMED_OUT,
]);

/** Failed-family statuses (for error display logic). */
export const FAILED_EXECUTION_STATUSES = new Set<string>([
  EXECUTION_STATUS.FAILED,
  EXECUTION_STATUS.TIMED_OUT,
]);

/** Cancelled statuses (terminal but not an error — user-initiated). */
export const CANCELLED_EXECUTION_STATUSES = new Set<string>([
  EXECUTION_STATUS.CANCELLED,
]);

export type SessionStatus = (typeof SESSION_STATUS)[keyof typeof SESSION_STATUS];

/** Terminal statuses — session will not change further. */
export const TERMINAL_STATUSES = new Set<SessionStatus>([
  SESSION_STATUS.COMPLETED,
  SESSION_STATUS.FAILED,
  SESSION_STATUS.CANCELLED,
  SESSION_STATUS.TIMED_OUT,
]);

/** Active statuses — session is still processing. */
export const ACTIVE_STATUSES = new Set<SessionStatus>([
  SESSION_STATUS.IN_PROGRESS,
  SESSION_STATUS.CANCELLING,
]);

/** Check if a session status is terminal. */
export function isTerminalStatus(status: SessionStatus): boolean {
  return TERMINAL_STATUSES.has(status);
}

/** Check if a session can be cancelled. */
export function canCancelSession(status: SessionStatus): boolean {
  return status === SESSION_STATUS.IN_PROGRESS || status === SESSION_STATUS.PENDING;
}

/** Human-readable display name for a status. */
export function getStatusDisplayName(status: SessionStatus): string {
  switch (status) {
    case SESSION_STATUS.PENDING:
      return 'Pending';
    case SESSION_STATUS.IN_PROGRESS:
      return 'In Progress';
    case SESSION_STATUS.CANCELLING:
      return 'Cancelling';
    case SESSION_STATUS.COMPLETED:
      return 'Completed';
    case SESSION_STATUS.FAILED:
      return 'Failed';
    case SESSION_STATUS.CANCELLED:
      return 'Cancelled';
    case SESSION_STATUS.TIMED_OUT:
      return 'Timed Out';
  }
}

/** MUI color for a status (for Chip, Badge, etc.). */
export function getStatusColor(
  status: SessionStatus,
): 'success' | 'error' | 'warning' | 'info' | 'default' {
  switch (status) {
    case SESSION_STATUS.COMPLETED:
      return 'success';
    case SESSION_STATUS.FAILED:
    case SESSION_STATUS.TIMED_OUT:
      return 'error';
    case SESSION_STATUS.IN_PROGRESS:
    case SESSION_STATUS.CANCELLING:
      return 'info';
    case SESSION_STATUS.PENDING:
      return 'warning';
    case SESSION_STATUS.CANCELLED:
      return 'default';
  }
}
