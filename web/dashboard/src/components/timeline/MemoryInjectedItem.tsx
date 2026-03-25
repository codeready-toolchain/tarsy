import { useState, useEffect, useMemo } from 'react';
import { Box, Typography, Collapse, IconButton, Chip, alpha } from '@mui/material';
import { ExpandMore, ExpandLess, PsychologyOutlined } from '@mui/icons-material';
import CopyButton from '../shared/CopyButton';
import type { FlowItem } from '../../utils/timelineParser';

function highlightText(text: string, term: string) {
  if (!term) return text;
  const escaped = term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const parts = text.split(new RegExp(`(${escaped})`, 'gi'));
  if (parts.length === 1) return text;
  return parts.map((part, i) =>
    part.toLowerCase() === term.toLowerCase()
      ? <mark key={i} style={{ background: '#ffe082', borderRadius: 2 }}>{part}</mark>
      : part,
  );
}

interface MemoryInjectedItemProps {
  item: FlowItem;
  expandAll?: boolean;
  searchTerm?: string;
}

function MemoryInjectedItem({ item, expandAll = false, searchTerm }: MemoryInjectedItemProps) {
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    setExpanded(expandAll);
  }, [expandAll]);
  const isExpanded = expandAll || expanded;

  const count = (item.metadata?.count as number) || 0;

  const memoryLines = useMemo(() => {
    if (!item.content) return [];
    return item.content.split('\n').filter((line) => line.trim().length > 0);
  }, [item.content]);

  return (
    <Box
      data-flow-item-id={item.id}
      sx={(theme) => ({
        ml: 4, my: 1, mr: 1,
        border: '2px solid',
        borderColor: alpha(theme.palette.warning.main, 0.5),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette.warning.main, 0.08),
        boxShadow: `0 1px 3px ${alpha(theme.palette.common.black, 0.08)}`,
      })}
    >
      <Box
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75,
          cursor: 'pointer', borderRadius: 1.5, transition: 'background-color 0.2s ease',
          '&:hover': { bgcolor: alpha(theme.palette.warning.main, 0.2) },
        })}
        onClick={() => {
          if (expandAll) return;
          setExpanded((prev) => !prev);
        }}
      >
        <PsychologyOutlined sx={(theme) => ({ fontSize: 18, color: theme.palette.warning.main })} />
        <Typography variant="body2" sx={(theme) => ({ fontFamily: 'monospace', fontWeight: 600, fontSize: '0.9rem', color: theme.palette.warning.main })}>
          Pre-loaded Memories
        </Typography>
        {count > 0 && (
          <Chip label={count} size="small" sx={(theme) => ({ height: 20, fontSize: '0.75rem', bgcolor: alpha(theme.palette.warning.main, 0.15), color: theme.palette.warning.dark })} />
        )}
        <Box sx={{ flex: 1 }} />
        <IconButton size="small" sx={{ p: 0.25 }}>
          {isExpanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 0.5 }}>
            <Typography variant="caption" color="text.secondary">
              Injected into system prompt from past investigations
            </Typography>
            <CopyButton text={item.content || ''} variant="icon" size="small" tooltip="Copy memory content" />
          </Box>
          {memoryLines.length > 0 ? (
            <Box sx={(theme) => ({
              maxHeight: 400, overflow: 'auto',
              p: 1.5, borderRadius: 1,
              bgcolor: '#fff',
              border: `1px solid ${theme.palette.divider}`,
              fontSize: '0.85rem',
              ...theme.applyStyles('dark', {
                bgcolor: 'rgba(255, 255, 255, 0.06)',
              }),
            })}>
              {memoryLines.map((line, i) => (
                <Typography key={i} variant="body2" sx={{ fontFamily: 'monospace', fontSize: '0.82rem', lineHeight: 1.6, py: 0.25 }}>
                  {highlightText(line, searchTerm || '')}
                </Typography>
              ))}
            </Box>
          ) : (
            <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>No memories injected</Typography>
          )}
        </Box>
      </Collapse>
    </Box>
  );
}

export default MemoryInjectedItem;
