import { Box, Chip, alpha } from '@mui/material';
import type { ChipProps } from '@mui/material';

const chipSx = {
  height: 24,
  fontSize: '0.75rem',
  fontWeight: 500,
  '& .MuiChip-label': { px: 0.75 },
} as const;

/** Names that usually mean "human must intervene / page / restrict". */
const URGENT_LABELS = new Set(['action', 'ban', 'suspend', 'page', 'escalate']);

/** Names that usually mean "keep an eye on it". */
const WATCH_LABELS = new Set(['watch', 'monitor']);

/** Closable / no remaining work — same muted grey as the no-action wrench chip. */
const QUIET_LABELS = new Set(['noise', 'fp', 'false_positive']);

function labelChipColor(label: string): NonNullable<ChipProps['color']> {
  const key = label.toLowerCase();
  if (URGENT_LABELS.has(key)) return 'error';
  if (WATCH_LABELS.has(key)) return 'info';
  return 'default';
}

function isQuietLabel(label: string): boolean {
  return QUIET_LABELS.has(label.toLowerCase());
}

interface SessionLabelChipsProps {
  labels: string[] | null | undefined;
}

/** Display-only chips for stored session labels. Renders nothing for null or []. */
export function SessionLabelChips({ labels }: SessionLabelChipsProps) {
  if (!labels || labels.length === 0) return null;

  return (
    <Box sx={{ display: 'inline-flex', flexWrap: 'wrap', gap: 0.375, alignItems: 'center', flexShrink: 0 }}>
      {labels.map((label) => {
        const color = labelChipColor(label);
        const quiet = isQuietLabel(label);
        return (
          <Chip
            key={label}
            size="small"
            label={label}
            variant="outlined"
            color={color}
            data-muted={quiet ? 'true' : undefined}
            sx={(theme) => ({
              ...chipSx,
              ...(quiet && {
                borderColor: theme.palette.grey[400],
                color: theme.palette.grey[400],
              }),
              ...(!quiet && color !== 'default' && {
                backgroundColor: alpha(theme.palette[color].main, 0.1),
                borderColor: alpha(theme.palette[color].main, 0.4),
              }),
            })}
          />
        );
      })}
    </Box>
  );
}
