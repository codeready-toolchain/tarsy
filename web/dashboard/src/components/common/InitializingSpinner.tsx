import { Box, CircularProgress, Typography } from '@mui/material';

type ColorVariant = 'primary' | 'warning';

interface InitializingSpinnerProps {
  /** Text shown below the spinner. */
  message?: string;
  /** Color theme: 'primary' (blue) or 'warning' (orange). */
  color?: ColorVariant;
}

const COLORS: Record<ColorVariant, { ring: string; gradient: string }> = {
  primary: {
    ring: 'rgba(25, 118, 210, 0.15)',
    gradient:
      'linear-gradient(90deg, rgba(0,0,0,0.5) 0%, rgba(0,0,0,0.7) 40%, rgba(0,0,0,0.9) 50%, rgba(0,0,0,0.7) 60%, rgba(0,0,0,0.5) 100%)',
  },
  warning: {
    ring: 'rgba(237, 108, 2, 0.15)',
    gradient:
      'linear-gradient(90deg, rgba(237,108,2,0.5) 0%, rgba(237,108,2,0.7) 40%, rgba(237,108,2,0.9) 50%, rgba(237,108,2,0.7) 60%, rgba(237,108,2,0.5) 100%)',
  },
};

/**
 * Pulsing ring spinner with shimmer text.
 * Used while a session is queued or initializing, before the first timeline
 * data arrives.
 */
export default function InitializingSpinner({
  message = 'Initializing investigation...',
  color = 'primary',
}: InitializingSpinnerProps) {
  const palette = COLORS[color];

  return (
    <Box
      sx={{
        py: 8,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 3,
      }}
    >
      {/* Pulsing ring spinner */}
      <Box
        sx={{
          position: 'relative',
          width: 64,
          height: 64,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <CircularProgress size={56} thickness={2.5} color={color} />
        <Box
          sx={{
            position: 'absolute',
            width: 64,
            height: 64,
            borderRadius: '50%',
            border: '2px solid',
            borderColor: palette.ring,
            animation: 'init-pulse 2s ease-in-out infinite',
            '@keyframes init-pulse': {
              '0%, 100%': { transform: 'scale(1)', opacity: 0.6 },
              '50%': { transform: 'scale(1.15)', opacity: 0 },
            },
          }}
        />
      </Box>

      {/* Shimmer text */}
      <Typography
        variant="body1"
        sx={{
          fontSize: '1.1rem',
          fontWeight: 500,
          fontStyle: 'italic',
          background: palette.gradient,
          backgroundSize: '200% 100%',
          backgroundClip: 'text',
          WebkitBackgroundClip: 'text',
          WebkitTextFillColor: 'transparent',
          animation: 'init-shimmer 3s linear infinite',
          '@keyframes init-shimmer': {
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
