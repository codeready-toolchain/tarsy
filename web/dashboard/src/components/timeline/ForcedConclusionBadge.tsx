import { Typography } from '@mui/material';

/**
 * Inline wrap-up marker next to CONCLUSION / ANSWER / RESULT.
 * Same caption scale as timeline headers; warning color only on this phrase
 * so it does not blend into the green title or turn into a chip.
 */
export default function ForcedConclusionBadge({ label }: { label: string }) {
  return (
    <Typography
      variant="caption"
      sx={(theme) => ({
        fontWeight: 700,
        fontSize: '0.75rem',
        letterSpacing: 0.5,
        textTransform: 'uppercase',
        color: 'warning.dark',
        flexShrink: 0,
        ...theme.applyStyles('dark', { color: 'warning.main' }),
      })}
    >
      (⚠️ {label})
    </Typography>
  );
}
