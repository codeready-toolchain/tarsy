import { Box, Typography } from '@mui/material';

interface ProcessingIndicatorProps {
  message?: string;
  centered?: boolean;
}

/**
 * ProcessingIndicator Component
 * Animated bouncing dots with shimmer text effect.
 * Shown at the bottom of the timeline when a session is being processed.
 */
export default function ProcessingIndicator({
  message = 'Processing...',
  centered = false,
}: ProcessingIndicatorProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 1.5,
        ...(centered ? { py: 4, justifyContent: 'center' } : { mt: 2 }),
        opacity: 0.7,
      }}
    >
      <Box
        sx={{
          display: 'flex',
          gap: 0.5,
          alignItems: 'center',
          height: 20,
          '& > div': {
            width: 6,
            height: 6,
            borderRadius: '50%',
            bgcolor: 'rgba(0, 0, 0, 0.6)',
            animation: 'bounce-wave 1.4s ease-in-out infinite',
          },
          '& > div:nth-of-type(2)': {
            animationDelay: '0.2s',
          },
          '& > div:nth-of-type(3)': {
            animationDelay: '0.4s',
          },
          '@keyframes bounce-wave': {
            '0%, 60%, 100%': {
              transform: 'translateY(0)',
            },
            '30%': {
              transform: 'translateY(-8px)',
            },
          },
        }}
      >
        <Box />
        <Box />
        <Box />
      </Box>
      <Typography
        variant="body1"
        sx={{
          fontSize: '1.1rem',
          fontWeight: 500,
          fontStyle: 'italic',
          background:
            'linear-gradient(90deg, rgba(0,0,0,0.5) 0%, rgba(0,0,0,0.7) 40%, rgba(0,0,0,0.9) 50%, rgba(0,0,0,0.7) 60%, rgba(0,0,0,0.5) 100%)',
          backgroundSize: '200% 100%',
          backgroundClip: 'text',
          WebkitBackgroundClip: 'text',
          WebkitTextFillColor: 'transparent',
          animation: 'shimmer-subtle 3s linear infinite',
          '@keyframes shimmer-subtle': {
            '0%': { backgroundPosition: '200% center' },
            '100%': { backgroundPosition: '-200% center' },
          },
        }}
      >
        {message}
      </Typography>
    </Box>
  );
}
