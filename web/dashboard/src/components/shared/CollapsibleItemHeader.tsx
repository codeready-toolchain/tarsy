import { Box, Typography } from '@mui/material';

interface CollapsibleItemHeaderProps {
  headerText: string;
  headerColor: string;
  headerTextTransform?: 'uppercase' | 'none';
  shouldShowCollapsed: boolean;
  collapsedHeaderOpacity: number;
  onToggle?: () => void;
}

/**
 * CollapsibleItemHeader Component
 * Reusable header for collapsible chat flow items
 * Shows ellipsis animation when collapsed
 */
export default function CollapsibleItemHeader({
  headerText,
  headerColor,
  headerTextTransform = 'none',
  shouldShowCollapsed,
  collapsedHeaderOpacity,
  onToggle
}: CollapsibleItemHeaderProps) {
  return (
    <Box 
      sx={{ 
        display: 'flex',
        alignItems: 'center',
        gap: 0,
        cursor: onToggle ? 'pointer' : 'default',
        px: shouldShowCollapsed ? 1 : 0,
        py: 0.5,
        mx: shouldShowCollapsed ? -1 : 0,
        borderRadius: 1,
        transition: 'background-color 0.2s ease',
        '&:hover': onToggle ? {
          bgcolor: 'action.hover'
        } : {}
      }}
      onClick={onToggle}
    >
      <Typography
        className="cfi-dimmable"
        variant="caption"
        sx={{
          fontWeight: 700,
          textTransform: headerTextTransform,
          letterSpacing: 0.5,
          fontSize: '0.75rem',
          color: headerColor,
          opacity: collapsedHeaderOpacity,
          transition: 'opacity 0.2s ease'
        }}
      >
        {headerText}
      </Typography>
      {onToggle && shouldShowCollapsed && (
        <Typography
          className="cfi-ellipsis"
          component="span"
          sx={{
            color: headerColor,
            opacity: collapsedHeaderOpacity,
            transition: 'opacity 0.2s ease',
            fontSize: '0.75rem',
            fontWeight: 700,
            lineHeight: 1,
            ml: 0.15,
            display: 'inline-flex',
            gap: 0
          }}
        >
          <Box component="span" className="cfi-ellipsis-dot" sx={{ display: 'inline-block' }}>.</Box>
          <Box component="span" className="cfi-ellipsis-dot" sx={{ display: 'inline-block' }}>.</Box>
          <Box component="span" className="cfi-ellipsis-dot" sx={{ display: 'inline-block' }}>.</Box>
        </Typography>
      )}
    </Box>
  );
}
