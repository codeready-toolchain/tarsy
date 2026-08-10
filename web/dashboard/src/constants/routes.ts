/**
 * Route path constants.
 */

export const ROUTES = {
  SESSIONS: '/',
  TRIAGE: '/triage',
  SESSION_DETAIL: '/sessions/:id',
  SESSION_TRACE: '/sessions/:id/trace',
  SESSION_SCORING: '/sessions/:id/scoring',
  SUBMIT_ALERT: '/submit-alert',
  SYSTEM_STATUS: '/system',
  USAGE: '/usage',
} as const;

export interface SessionDetailPathOptions {
  stage?: string;
  event?: string;
}

/** Build a session detail path, optionally with deep-link query params. */
export function sessionDetailPath(id: string, options?: SessionDetailPathOptions): string {
  const base = `/sessions/${id}`;
  if (!options?.stage && !options?.event) return base;

  const params = new URLSearchParams();
  if (options.stage) params.set('stage', options.stage);
  if (options.event) params.set('event', options.event);
  const qs = params.toString();
  return qs ? `${base}?${qs}` : base;
}

/** Build a session trace path. */
export function sessionTracePath(id: string): string {
  return `/sessions/${id}/trace`;
}

/** Build a session scoring path. */
export function sessionScoringPath(id: string): string {
  return `/sessions/${id}/scoring`;
}
