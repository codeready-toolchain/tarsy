/**
 * Session and stage status constants and helpers.
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

export type SessionStatus = (typeof SESSION_STATUS)[keyof typeof SESSION_STATUS];

/** Terminal statuses — session will not change further. */
export const TERMINAL_STATUSES = new Set<string>([
  SESSION_STATUS.COMPLETED,
  SESSION_STATUS.FAILED,
  SESSION_STATUS.CANCELLED,
  SESSION_STATUS.TIMED_OUT,
]);

/** Active statuses — session is still processing. */
export const ACTIVE_STATUSES = new Set<string>([
  SESSION_STATUS.IN_PROGRESS,
  SESSION_STATUS.CANCELLING,
]);

/** Check if a session status is terminal. */
export function isTerminalStatus(status: string): boolean {
  return TERMINAL_STATUSES.has(status);
}

/** Check if a session can be cancelled. */
export function canCancelSession(status: string): boolean {
  return status === SESSION_STATUS.IN_PROGRESS || status === SESSION_STATUS.PENDING;
}

/** Human-readable display name for a status. */
export function getStatusDisplayName(status: string): string {
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
    default:
      return status;
  }
}

/** MUI color for a status (for Chip, Badge, etc.). */
export function getStatusColor(
  status: string,
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
    default:
      return 'default';
  }
}
