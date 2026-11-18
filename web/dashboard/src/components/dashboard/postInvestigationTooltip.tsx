import { Box, Link, Tooltip } from '@mui/material';
import type { ReactElement } from 'react';
import type { DashboardSessionItem } from '../../types/session.ts';
import { postInvestigationTooltip, type PostInvestigationTooltipContent } from './postInvestigationTooltip.ts';

type TooltipSession = Pick<DashboardSessionItem, 'id' | 'post_investigation_acted' | 'post_investigation_links'>;

function TooltipTitle({ content }: { content: PostInvestigationTooltipContent }) {
  if (content.links.length === 0) {
    return content.status;
  }
  return (
    <Box component="span" sx={{ display: 'flex', flexDirection: 'column', gap: 0.25 }}>
      <span>{content.status}</span>
      {content.links.map((href) => (
        <Link
          key={href}
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          color="inherit"
          underline="always"
          onClick={(event) => event.stopPropagation()}
          sx={{ display: 'block', wordBreak: 'break-all' }}
        >
          {href}
        </Link>
      ))}
    </Box>
  );
}

/** Post-investigation chip tooltip. Artifact URLs are links that open in a new tab. */
export function PostInvestigationTooltip({ session, children }: { session: TooltipSession; children: ReactElement }) {
  return (
    <Tooltip title={<TooltipTitle content={postInvestigationTooltip(session)} />}>
      {children}
    </Tooltip>
  );
}
