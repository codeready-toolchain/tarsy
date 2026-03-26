import { type ReactNode } from 'react';
import { Box, Typography, Chip, alpha } from '@mui/material';

export interface ParsedMemory {
  category: string;
  valence: string;
  ageLabel: string;
  content: string;
}

export const CATEGORY_LABEL: Record<string, string> = {
  semantic: 'S',
  episodic: 'E',
  procedural: 'P',
};

export const VALENCE_COLOR: Record<string, 'success' | 'error' | 'default'> = {
  positive: 'success',
  negative: 'error',
  neutral: 'default',
};

interface MemoryCardListProps {
  memories: ParsedMemory[];
  renderContent: (content: string) => ReactNode;
  maxHeight?: number;
}

export function MemoryCardList({ memories, renderContent, maxHeight = 500 }: MemoryCardListProps) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, maxHeight, overflow: 'auto' }}>
      {memories.map((mem, i) => (
        <Box
          key={i}
          sx={(theme) => ({
            display: 'flex', gap: 1, alignItems: 'flex-start',
            p: 1.25, borderRadius: 1,
            bgcolor: '#fff',
            border: `1px solid ${alpha(theme.palette.secondary.main, 0.15)}`,
            ...theme.applyStyles('dark', { bgcolor: 'rgba(255, 255, 255, 0.04)' }),
          })}
        >
          {mem.category && (
            <Chip
              label={CATEGORY_LABEL[mem.category] ?? mem.category.charAt(0).toUpperCase()}
              size="small"
              variant="outlined"
              sx={(theme) => ({
                minWidth: 28, height: 22, fontSize: '0.7rem', fontWeight: 700,
                borderColor: alpha(theme.palette.secondary.main, 0.4),
                color: theme.palette.secondary.dark,
                flexShrink: 0, mt: 0.1,
              })}
            />
          )}
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="body2" sx={{ lineHeight: 1.6, wordBreak: 'break-word' }}>
              {renderContent(mem.content)}
            </Typography>
            {mem.ageLabel && (
              <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem', mt: 0.25, display: 'block' }}>
                {mem.ageLabel}
              </Typography>
            )}
          </Box>
          {mem.valence && (
            <Chip
              label={mem.valence}
              size="small"
              color={VALENCE_COLOR[mem.valence] ?? 'default'}
              variant="outlined"
              sx={{ flexShrink: 0, height: 20, fontSize: '0.65rem', mt: 0.1 }}
            />
          )}
        </Box>
      ))}
    </Box>
  );
}
