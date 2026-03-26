import { useState, useEffect, useMemo } from 'react';
import { Box, Typography, Collapse, IconButton, Chip, alpha } from '@mui/material';
import { ExpandMore, ExpandLess, PsychologyOutlined } from '@mui/icons-material';
import CopyButton from '../shared/CopyButton';
import { MemoryCardList, type ParsedMemory } from './MemoryCardList';
import type { FlowItem } from '../../utils/timelineParser';

const MEMORY_LINE_RE = /^-\s*\[([^,\]]+),\s*([^,\]]+)(?:,\s*([^\]]+))?\]\s*(.+)$/;

function parseMemoryLines(raw: string): ParsedMemory[] {
  if (!raw) return [];
  const results: ParsedMemory[] = [];
  for (const line of raw.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const m = MEMORY_LINE_RE.exec(trimmed);
    if (m) {
      results.push({ category: m[1].trim(), valence: m[2].trim(), ageLabel: m[3]?.trim() ?? '', content: m[4].trim() });
    } else if (trimmed.startsWith('- ')) {
      results.push({ category: '', valence: '', ageLabel: '', content: trimmed.slice(2) });
    } else {
      results.push({ category: '', valence: '', ageLabel: '', content: trimmed });
    }
  }
  return results;
}

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

  const memories = useMemo(() => parseMemoryLines(item.content || ''), [item.content]);

  return (
    <Box
      data-flow-item-id={item.id}
      sx={(theme) => ({
        ml: 4, my: 1, mr: 1,
        border: '2px solid',
        borderColor: alpha(theme.palette.secondary.main, 0.5),
        borderRadius: 1.5,
        bgcolor: alpha(theme.palette.secondary.main, 0.08),
        boxShadow: `0 1px 3px ${alpha(theme.palette.common.black, 0.08)}`,
      })}
    >
      <Box
        sx={(theme) => ({
          display: 'flex', alignItems: 'center', gap: 1, px: 1.5, py: 0.75,
          cursor: 'pointer', borderRadius: 1.5, transition: 'background-color 0.2s ease',
          '&:hover': { bgcolor: alpha(theme.palette.secondary.main, 0.2) },
        })}
        onClick={() => {
          if (expandAll) return;
          setExpanded((prev) => !prev);
        }}
      >
        <PsychologyOutlined sx={(theme) => ({ fontSize: 18, color: theme.palette.secondary.main })} />
        <Typography variant="body2" sx={(theme) => ({ fontFamily: 'monospace', fontWeight: 600, fontSize: '0.9rem', color: theme.palette.secondary.main })}>
          Past Investigation Insights
        </Typography>
        {count > 0 && (
          <Chip label={count} size="small" sx={(theme) => ({ height: 20, fontSize: '0.75rem', bgcolor: alpha(theme.palette.secondary.main, 0.15), color: theme.palette.secondary.dark })} />
        )}
        <Box sx={{ flex: 1 }} />
        <IconButton size="small" sx={{ p: 0.25 }}>
          {isExpanded ? <ExpandLess fontSize="small" /> : <ExpandMore fontSize="small" />}
        </IconButton>
      </Box>

      <Collapse in={isExpanded}>
        <Box sx={{ px: 1.5, pb: 1.5, pt: 0.5, borderTop: 1, borderColor: 'divider' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
            <Typography variant="caption" color="text.secondary">
              Lessons applied from previous investigations
            </Typography>
            <CopyButton text={item.content || ''} variant="icon" size="small" tooltip="Copy memory content" />
          </Box>
          {memories.length > 0 ? (
            <MemoryCardList
              memories={memories}
              renderContent={(content) => highlightText(content, searchTerm || '')}
            />
          ) : (
            <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>No insights available</Typography>
          )}
        </Box>
      </Collapse>
    </Box>
  );
}

export default MemoryInjectedItem;
