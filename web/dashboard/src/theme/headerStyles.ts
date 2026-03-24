import { alpha, type Theme } from '@mui/material/styles';
import type { SxProps } from '@mui/system';

export function appBarSx(theme: Theme) {
  const primary = theme.palette.primary;
  return {
    borderRadius: 1,
    background: `linear-gradient(135deg, ${primary.main} 0%, ${primary.dark} 100%)`,
    boxShadow: `0 4px 16px ${alpha(primary.main, 0.3)}`,
    border: '1px solid rgba(255, 255, 255, 0.1)',
    ...theme.applyStyles('dark', {
      background: 'linear-gradient(135deg, #1a2332 0%, #0d1b2a 100%)',
      boxShadow: `0 4px 16px ${alpha(theme.palette.common.black, 0.4)}`,
    }),
  } as const;
}

export const glassIconButtonSx: SxProps<Theme> = {
  background: 'rgba(255, 255, 255, 0.1)',
  backdropFilter: 'blur(10px)',
  border: '1px solid rgba(255, 255, 255, 0.15)',
  borderRadius: 2,
  transition: 'all 0.2s ease',
  '&:hover': {
    background: 'rgba(255, 255, 255, 0.2)',
    transform: 'translateY(-1px)',
    boxShadow: '0 4px 12px rgba(255, 255, 255, 0.2)',
  },
};

export const logoBoxSx: SxProps<Theme> = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 40,
  height: 40,
  borderRadius: 2,
  background: 'rgba(255, 255, 255, 0.1)',
  backdropFilter: 'blur(10px)',
  border: '1px solid rgba(255, 255, 255, 0.2)',
  boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15), 0 0 20px rgba(255, 255, 255, 0.1)',
  transition: 'all 0.3s ease',
  position: 'relative',
  overflow: 'hidden',
  textDecoration: 'none',
  '&:before': {
    content: '""',
    position: 'absolute',
    top: 0,
    left: '-100%',
    width: '100%',
    height: '100%',
    background: 'linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.2), transparent)',
    animation: 'none',
  },
  '&:hover': {
    background: 'rgba(255, 255, 255, 0.15)',
    transform: 'translateY(-2px) scale(1.05)',
    boxShadow: '0 8px 25px rgba(0, 0, 0, 0.2), 0 0 30px rgba(255, 255, 255, 0.2)',
    '&:before': {
      animation: 'shimmer 0.6s ease-out',
    },
  },
  '&:focus-visible': {
    outline: '2px solid rgba(255, 255, 255, 0.8)',
    outlineOffset: '2px',
  },
  '@keyframes shimmer': {
    '0%': { left: '-100%' },
    '100%': { left: '100%' },
  },
};

export const titleSx: SxProps<Theme> = {
  fontWeight: 600,
  letterSpacing: '-0.5px',
  textShadow: '0 1px 2px rgba(0, 0, 0, 0.1)',
  background: 'linear-gradient(45deg, #ffffff 0%, rgba(255, 255, 255, 0.9) 100%)',
  WebkitBackgroundClip: 'text',
  WebkitTextFillColor: 'transparent',
  backgroundClip: 'text',
  color: 'white',
};

export const themeToggleSx: SxProps<Theme> = {
  color: 'inherit',
  ...glassIconButtonSx,
};

export const glassToggleGroupSx: SxProps<Theme> = {
  bgcolor: 'rgba(255,255,255,0.1)',
  borderRadius: 3,
  padding: 0.5,
  border: '1px solid rgba(255,255,255,0.2)',
  '& .MuiToggleButton-root': {
    color: 'rgba(255,255,255,0.8)',
    border: 'none',
    borderRadius: 2,
    px: 2,
    py: 0.5,
    minWidth: 100,
    fontWeight: 500,
    fontSize: '0.875rem',
    textTransform: 'none',
    transition: 'all 0.2s ease-in-out',
    '&:hover': {
      bgcolor: 'rgba(255,255,255,0.15)',
      color: 'rgba(255,255,255,0.95)',
    },
    '&.Mui-selected': {
      bgcolor: 'rgba(255,255,255,0.25)',
      color: '#fff',
      fontWeight: 600,
      boxShadow: '0 2px 8px rgba(0,0,0,0.2)',
      '&:hover': {
        bgcolor: 'rgba(255,255,255,0.3)',
      },
    },
  },
};
