import { useState } from 'react';
import {
  Chip,
  Popover,
  Card,
  Box,
  Typography,
  Divider,
} from '@mui/material';
import type { SxProps, Theme } from '@mui/material/styles';
import { Summarize } from '@mui/icons-material';
import ReactMarkdown from 'react-markdown';
import { remarkPlugins, executiveSummaryMarkdownStyles } from '../../utils/markdownComponents.tsx';

interface SummaryTooltipProps {
  summary: string;
}

export function SummaryTooltip({ summary }: SummaryTooltipProps) {
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null);

  if (!summary || summary.trim().length === 0) return null;

  return (
    <>
      <Chip
        label="Summary"
        size="small"
        variant="outlined"
        color="primary"
        onMouseEnter={(e) => setAnchorEl(e.currentTarget)}
        onMouseLeave={() => setAnchorEl(null)}
        onFocus={(e) => setAnchorEl(e.currentTarget)}
        onBlur={() => setAnchorEl(null)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') { setAnchorEl(null); e.currentTarget.blur(); }
        }}
        onClick={(e) => e.stopPropagation()}
        tabIndex={0}
        aria-label="Show executive summary"
        sx={{
          cursor: 'pointer',
          height: 24,
          fontSize: '0.75rem',
          fontWeight: 500,
          transition: 'all 0.2s ease-in-out',
          '&:hover': {
            backgroundColor: 'text.secondary',
            color: 'background.paper',
            borderColor: 'text.secondary',
          },
        }}
      />
      <Popover
        sx={{ pointerEvents: 'none' }}
        open={Boolean(anchorEl)}
        anchorEl={anchorEl}
        anchorOrigin={{ vertical: 'top', horizontal: 'left' }}
        transformOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        onClose={() => setAnchorEl(null)}
        disableRestoreFocus
        disableScrollLock
      >
        <Card sx={{ maxWidth: 500, p: 2.5, boxShadow: 3 }}>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
            <Summarize color="primary" />
            <Typography
              variant="subtitle1"
              sx={{ fontWeight: 600, color: 'primary.main' }}
            >
              Executive Summary
            </Typography>
          </Box>
          <Divider sx={{ mb: 1.5 }} />
          <Box sx={executiveSummaryMarkdownStyles as SxProps<Theme>}>
            <ReactMarkdown remarkPlugins={remarkPlugins} skipHtml>{summary}</ReactMarkdown>
          </Box>
        </Card>
      </Popover>
    </>
  );
}
