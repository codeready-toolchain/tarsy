import { Box, Typography, CircularProgress, alpha } from '@mui/material';

interface ProcessingIndicatorProps {
  message?: string;
}

/**
 * ProcessingIndicator Component
 * Shown at the bottom of the timeline when a session is being processed.
 * Displays a pulsing status message (e.g., "Investigating...", "Synthesizing...").
 */
export default function ProcessingIndicator({ 
  message = 'Processing...' 
}: ProcessingIndicatorProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1.5,
        py: 2,
        px: 2,
        mt: 1,
        borderRadius: 1,
        bgcolor: (theme) => alpha(theme.palette.primary.main, 0.04),
        border: '1px solid',
        borderColor: (theme) => alpha(theme.palette.primary.main, 0.12),
        animation: 'processingPulse 2s ease-in-out infinite',
        '@keyframes processingPulse': {
          '0%, 100%': { opacity: 0.7 },
          '50%': { opacity: 1 }
        }
      }}
    >
      <CircularProgress size={16} thickness={5} />
      <Typography
        variant="body2"
        sx={{
          color: 'primary.main',
          fontWeight: 500,
          fontSize: '0.875rem',
        }}
      >
        {message}
      </Typography>
    </Box>
  );
}
