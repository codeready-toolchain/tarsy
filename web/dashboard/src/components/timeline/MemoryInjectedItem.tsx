import { useMemo } from 'react';
import { Box, Typography, Chip, alpha } from '@mui/material';
import CopyButton from '../shared/CopyButton';
import InsightsCard from './InsightsCard';
import { MemoryCardList, type ParsedMemory } from './MemoryCardList';
import { highlightSearchTermNodes } from '../../utils/search';
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

interface MemoryInjectedItemProps {
  item: FlowItem;
  expandAll?: boolean;
  searchTerm?: string;
}

function MemoryInjectedItem({ item, expandAll = false, searchTerm }: MemoryInjectedItemProps) {
  const count = (item.metadata?.count as number) || 0;
  const memories = useMemo(() => parseMemoryLines(item.content || ''), [item.content]);

  const headerExtras = count > 0 ? (
    <Chip
      label={count}
      size="small"
      sx={(theme) => ({
        height: 20, fontSize: '0.75rem',
        bgcolor: alpha(theme.palette.success.main, 0.15),
        color: theme.palette.success.dark,
      })}
    />
  ) : undefined;

  return (
    <InsightsCard
      itemId={item.id}
      title="Past Investigation Insights"
      headerExtras={headerExtras}
      expandAll={expandAll}
    >
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 1 }}>
        <Typography variant="caption" color="text.secondary">
          Lessons applied from previous investigations
        </Typography>
        <CopyButton text={item.content || ''} variant="icon" size="small" tooltip="Copy memory content" />
      </Box>
      {memories.length > 0 ? (
        <MemoryCardList
          memories={memories}
          renderContent={(content) => searchTerm ? highlightSearchTermNodes(content, searchTerm) : content}
        />
      ) : (
        <Typography variant="caption" color="text.secondary" sx={{ fontStyle: 'italic' }}>
          No insights available
        </Typography>
      )}
    </InsightsCard>
  );
}

export default MemoryInjectedItem;
