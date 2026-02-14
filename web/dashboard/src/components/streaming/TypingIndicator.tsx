import { Box, Typography, Paper, alpha } from '@mui/material';
import { Psychology } from '@mui/icons-material';

interface TypingIndicatorProps {
  message?: string;
  showIcon?: boolean;
  size?: 'small' | 'medium' | 'large';
  dotsOnly?: boolean;
}

/**
 * Typing indicator component that shows bouncing dots animation
 * Used to indicate that the AI is actively thinking/processing
 */
function TypingIndicator({ 
  message = "AI is thinking...",
  showIcon = true,
  size = 'medium',
  dotsOnly = false
}: TypingIndicatorProps) {
  const sizeConfig = {
    small: { dotSize: 6, dotSpacing: 8, fontSize: '0.75rem', iconSize: 16, padding: 12 },
    medium: { dotSize: 8, dotSpacing: 12, fontSize: '0.875rem', iconSize: 20, padding: 16 },
    large: { dotSize: 10, dotSpacing: 16, fontSize: '1rem', iconSize: 24, padding: 20 }
  };

  const config = sizeConfig[size];

  const dots = (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: `${config.dotSpacing / 8}rem`, justifyContent: dotsOnly ? 'flex-start' : undefined, py: dotsOnly ? 1 : 0 }}>
      {[0, 1, 2].map((index) => (
        <Box
          key={index}
          sx={{
            width: config.dotSize, height: config.dotSize, borderRadius: '50%',
            bgcolor: 'primary.main', opacity: 0.7,
            animation: `bounce 1.4s ease-in-out infinite`,
            animationDelay: `${index * 0.2}s`,
            '@keyframes bounce': {
              '0%': { transform: 'translateY(0)', opacity: 0.7 },
              '50%': { transform: `translateY(-${config.dotSize / 2}px)`, opacity: 1 },
              '100%': { transform: 'translateY(0)', opacity: 0.7 }
            }
          }}
        />
      ))}
    </Box>
  );

  if (dotsOnly) return dots;

  return (
    <Paper
      elevation={1}
      sx={{
        p: config.padding / 8, mb: 2, borderRadius: 2,
        border: '1px solid', borderColor: 'primary.light',
        bgcolor: (theme) => alpha(theme.palette.primary.main, 0.04),
        display: 'flex', alignItems: 'center', gap: 2, maxWidth: 'fit-content',
        animation: 'breathe 3s ease-in-out infinite',
        '@keyframes breathe': {
          '0%': { boxShadow: `0 0 5px ${alpha('#1976d2', 0.2)}`, transform: 'scale(1)' },
          '50%': { boxShadow: `0 0 20px ${alpha('#1976d2', 0.3)}`, transform: 'scale(1.02)' },
          '100%': { boxShadow: `0 0 5px ${alpha('#1976d2', 0.2)}`, transform: 'scale(1)' }
        }
      }}
    >
      {showIcon && (
        <Psychology sx={{ 
          fontSize: config.iconSize, color: 'primary.main',
          animation: 'think 4s ease-in-out infinite',
          '@keyframes think': {
            '0%': { transform: 'rotate(-3deg)' }, '25%': { transform: 'rotate(3deg)' },
            '50%': { transform: 'rotate(-2deg)' }, '75%': { transform: 'rotate(2deg)' },
            '100%': { transform: 'rotate(-3deg)' }
          }
        }} />
      )}
      <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 1 }}>
        <Typography variant="body2" color="primary.main" sx={{ fontSize: config.fontSize, fontWeight: 500, opacity: 0.8 }}>
          {message}
        </Typography>
        {dots}
      </Box>
    </Paper>
  );
}

export default TypingIndicator;
