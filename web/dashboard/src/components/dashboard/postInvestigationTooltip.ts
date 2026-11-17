import type { DashboardSessionItem } from '../../types/session.ts';

const ACTED = 'Post-investigation agent acted';
const RAN = 'Post-investigation agents ran — no action taken';

/** Status sentence, then artifact URLs. The session's own dashboard link is omitted. */
export function postInvestigationTooltip(
  session: Pick<DashboardSessionItem, 'id' | 'post_investigation_acted' | 'post_investigation_links'>,
): string {
  const status = session.post_investigation_acted ? ACTED : RAN;
  const links = artifactLinks(session.id, session.post_investigation_links);
  if (links.length === 0) {
    return status;
  }
  return [status, ...links].join('\n');
}

function artifactLinks(sessionId: string, links: string[] | null | undefined): string[] {
  if (!links || links.length === 0) {
    return [];
  }
  return links.filter((link) => !isOwnSessionLink(link, sessionId));
}

/** True when the URL path is this session, with or without a trailing slash. */
function isOwnSessionLink(link: string, sessionId: string): boolean {
  let path: string;
  try {
    path = new URL(link).pathname.replace(/\/+$/, '');
  } catch {
    return false;
  }
  return path.endsWith(`/sessions/${sessionId}`);
}
