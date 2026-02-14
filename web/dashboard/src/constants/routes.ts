/**
 * Route path constants.
 */

export const ROUTES = {
  DASHBOARD: '/',
  SESSION_DETAIL: '/sessions/:id',
  SESSION_TRACE: '/sessions/:id/trace',
  SUBMIT_ALERT: '/submit-alert',
} as const;

/** Build a session detail path. */
export function sessionDetailPath(id: string): string {
  return `/sessions/${id}`;
}

/** Build a session trace path. */
export function sessionTracePath(id: string): string {
  return `/sessions/${id}/trace`;
}
