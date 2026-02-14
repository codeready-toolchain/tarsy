/**
 * Animation styles for chat flow items
 * Extracted to avoid duplication across multiple item types
 */

import type { SxProps, Theme } from '@mui/material';

/**
 * Fade collapse animation styles applied to collapsible items
 * Includes hover effects for dimmed elements and ellipsis animation
 */
export const FADE_COLLAPSE_ANIMATION: SxProps<Theme> = {
  animation: 'fadeCollapse 0.6s ease-out',
  '@keyframes fadeCollapse': {
    '0%': { opacity: 1 },
    '50%': { opacity: 0.3 },
    '100%': { opacity: 1 },
  },
  // Remove dimming on hover (restore visual emphasis)
  '&:hover .cfi-dimmable': { opacity: 1 },
  '&:hover .cfi-ellipsis': { opacity: 1 },
  '&:hover .cfi-ellipsis-dot': { animation: 'cfi-ellipsis-wave 0.8s ease-in-out' },
  '&:hover .cfi-ellipsis-dot:nth-of-type(1)': { animationDelay: '0s' },
  '&:hover .cfi-ellipsis-dot:nth-of-type(2)': { animationDelay: '0.15s' },
  '&:hover .cfi-ellipsis-dot:nth-of-type(3)': { animationDelay: '0.3s' },
  '@keyframes cfi-ellipsis-wave': {
    '0%, 60%, 100%': { transform: 'translateY(0)' },
    '30%': { transform: 'translateY(-4px)' },
  },
};

/**
 * Base styles for emoji icon container
 */
export const EMOJI_ICON_STYLES: SxProps<Theme> = {
  fontSize: '1.1rem',
  lineHeight: '1.5',
  flexShrink: 0,
  display: 'flex',
  alignItems: 'center',
  height: '1.5rem',
  transition: 'opacity 0.2s ease'
};
