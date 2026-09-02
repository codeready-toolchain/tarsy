import { Chip } from '@mui/material';
import { WarningAmber } from '@mui/icons-material';

/**
 * High-contrast wrap-up marker for timeline headers. Filled so it does not
 * wash out as caption text on light or dark backgrounds.
 */
export default function ForcedConclusionBadge({ label }: { label: string }) {
  return (
    <Chip
      size="small"
      color="warning"
      variant="filled"
      icon={<WarningAmber />}
      label={label}
      sx={{
        height: 22,
        fontWeight: 700,
        fontSize: '0.7rem',
        '& .MuiChip-icon': { fontSize: '1rem' },
      }}
    />
  );
}
