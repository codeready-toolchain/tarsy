import { Box, Chip } from '@mui/material';

const chipSx = {
  height: 24,
  fontSize: '0.75rem',
  fontWeight: 500,
} as const;

interface SessionLabelChipsProps {
  labels: string[] | null | undefined;
}

/** Display-only chips for stored session labels. Renders nothing for null or []. */
export function SessionLabelChips({ labels }: SessionLabelChipsProps) {
  if (!labels || labels.length === 0) return null;

  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, alignItems: 'center' }}>
      {labels.map((label) => (
        <Chip key={label} size="small" label={label} variant="outlined" sx={chipSx} />
      ))}
    </Box>
  );
}
