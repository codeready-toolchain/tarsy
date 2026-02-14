import { Box, Typography } from '@mui/material';
import { ExpandLess } from '@mui/icons-material';

interface CollapseButtonProps {
  onClick: () => void;
}

/**
 * CollapseButton Component
 * Reusable button for collapsing expanded content in chat flow items
 */
export default function CollapseButton({ onClick }: CollapseButtonProps) {
  return (
    <Box
      onClick={onClick}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 0.5,
        mt: 0.5,
        cursor: 'pointer',
        opacity: 0.6,
        '&:hover': {
          opacity: 1
        }
      }}
    >
      <ExpandLess fontSize="small" />
      <Typography variant="caption" sx={{ fontSize: '0.75rem' }}>
        Collapse
      </Typography>
    </Box>
  );
}
