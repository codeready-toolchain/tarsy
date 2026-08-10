import { forwardRef } from 'react';
import { Box, alpha } from '@mui/material';
import type { ReactNode } from 'react';
import CopyButton from './CopyButton';
import CopyLinkButton from './CopyLinkButton';

interface ContentCardProps {
  children: ReactNode;
  maxHeight?: string;
  height?: string;
  copyText?: string;
  /** Deep-link URL shown next to copy when the card is visible (expanded) */
  linkUrl?: string;
}

/**
 * Scrollable grey card used for thoughts and responses.
 * Accepts ref for auto-scroll in streaming contexts.
 * Pass copyText / linkUrl to show action buttons in the top-right corner.
 */
const ContentCard = forwardRef<HTMLDivElement, ContentCardProps>(
  ({ children, maxHeight, height, copyText, linkUrl }, ref) => (
    <Box
      ref={ref}
      sx={(theme) => ({
        position: 'relative',
        bgcolor: theme.palette.action.hover,
        border: '1px solid',
        borderColor: theme.palette.divider,
        borderRadius: 1,
        p: 1.5,
        overflowY: 'auto',
        ...(maxHeight && { maxHeight }),
        ...(height && { height }),
        '&::-webkit-scrollbar': { width: '8px' },
        '&::-webkit-scrollbar-track': { bgcolor: 'transparent' },
        '&::-webkit-scrollbar-thumb': {
          bgcolor: alpha(theme.palette.text.disabled, 0.3),
          borderRadius: '4px',
          '&:hover': { bgcolor: alpha(theme.palette.text.disabled, 0.5) },
        },
      })}
    >
      {(copyText || linkUrl) && (
        <Box
          sx={{
            position: 'sticky',
            top: 0,
            float: 'right',
            zIndex: 1,
            ml: 1,
            display: 'flex',
            alignItems: 'center',
            gap: 0.25,
            color: 'text.secondary',
          }}
        >
          {linkUrl && <CopyLinkButton url={linkUrl} />}
          {copyText && <CopyButton text={copyText} variant="icon" size="small" />}
        </Box>
      )}
      {children}
    </Box>
  ),
);

ContentCard.displayName = 'ContentCard';

export default ContentCard;
