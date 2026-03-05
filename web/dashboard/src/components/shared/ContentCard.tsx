import { forwardRef } from 'react';
import { Box, alpha } from '@mui/material';
import type { ReactNode } from 'react';

interface ContentCardProps {
  children: ReactNode;
  maxHeight?: string;
  height?: string;
}

/**
 * Scrollable grey card used for thoughts and responses.
 * Accepts ref for auto-scroll in streaming contexts.
 */
const ContentCard = forwardRef<HTMLDivElement, ContentCardProps>(
  ({ children, maxHeight, height }, ref) => (
    <Box
      ref={ref}
      sx={(theme) => ({
        bgcolor: alpha(theme.palette.grey[300], 0.15),
        border: '1px solid',
        borderColor: alpha(theme.palette.grey[400], 0.2),
        borderRadius: 1,
        p: 1.5,
        overflowY: 'auto',
        ...(maxHeight && { maxHeight }),
        ...(height && { height }),
        '&::-webkit-scrollbar': { width: '8px' },
        '&::-webkit-scrollbar-track': { bgcolor: 'transparent' },
        '&::-webkit-scrollbar-thumb': {
          bgcolor: alpha(theme.palette.grey[500], 0.3),
          borderRadius: '4px',
          '&:hover': { bgcolor: alpha(theme.palette.grey[500], 0.5) },
        },
      })}
    >
      {children}
    </Box>
  ),
);

ContentCard.displayName = 'ContentCard';

export default ContentCard;
