/**
 * Route path constants.
 */

export const ROUTES = {
  DASHBOARD: '/',
  SESSION_DETAIL: '/sessions/:id',
  SESSION_TRACE: '/sessions/:id/trace',
  SESSION_SCORING: '/sessions/:id/scoring',
  SUBMIT_ALERT: '/submit-alert',
  SYSTEM_STATUS: '/system',
} as const;

/** Build a session detail path. */
export function sessionDetailPath(id: string): string {
  return `/sessions/${id}`;
}

/** Build a session trace path. */
export function sessionTracePath(id: string): string {
  return `/sessions/${id}/trace`;
}

/** Build a session scoring path. */
export function sessionScoringPath(id: string): string {
  return `/sessions/${id}/scoring`;
}
