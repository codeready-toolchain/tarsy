import { useState, useEffect, memo, type ReactNode } from 'react';
import { Box, Typography, Collapse, IconButton, alpha } from '@mui/material';
import { ExpandMore, ExpandLess, PsychologyOutlined } from '@mui/icons-material';

interface InsightsCardProps {
  itemId: string;
  title: ReactNode;
  headerExtras?: ReactNode;
  expandAll?: boolean;
  children: ReactNode;
}

/**
 * Shared expandable card for memory/insights items (both preloaded and
 * dynamically recalled). Provides the green-accent shell with
 * PsychologyOutlined icon; callers supply header extras and body content.
 */
function InsightsCard({ itemId, title, headerExtras, expandAll = false, children }: InsightsCardProps) {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => { setExpanded(expandAll); }, [expandAll]);
  const isExpanded = expandAll || expanded;

  return (
    <Box
      data-flow-item-id={itemId}
      sx={(theme) => ({
        ml: 4, my: 0.5, mr: 1,
        border: '1px solid',
        borderColor: alpha(theme.palette.success.main, 0.25),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette.success.main, 0.04),
      })}
    >
      <Box
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75,
          borderRadius: 1.5, transition: 'background-color 0.2s ease',
          ...(!expandAll && {
            cursor: 'pointer',
            '&:hover': { bgcolor: alpha(theme.palette.success.main, 0.1) },
          }),
        })}
        onClick={() => { if (!expandAll) setExpanded((prev) => !prev); }}
      >
        <PsychologyOutlined sx={(theme) => ({ fontSize: 18, color: theme.palette.success.main })} />
        <Typography
          variant="body2"
          sx={{ fontFamily: 'monospace', fontWeight: 500, fontSize: '0.9rem', color: 'text.secondary', flexShrink: 0 }}
        >
          {title}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flex: 1, minWidth: 0 }}>
          {headerExtras}
        </Box>
        <IconButton
          size="small"
          disabled={expandAll}
          sx={{ p: 0.25, flexShrink: 0, ...(expandAll && { opacity: 0.4, cursor: 'default' }) }}
        >
          {isExpanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>
      <Collapse in={isExpanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          {children}
        </Box>
      </Collapse>
    </Box>
  );
}

export default memo(InsightsCard);
