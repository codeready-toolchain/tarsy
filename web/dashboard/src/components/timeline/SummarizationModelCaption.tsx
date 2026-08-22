import { Typography } from '@mui/material';

interface SummarizationModelCaptionProps {
  metadata?: Record<string, unknown> | null;
  opacity?: number;
}

export function summarizationModelLabel(metadata?: Record<string, unknown> | null): string | undefined {
  const model = metadata?.summarization_model;
  if (typeof model !== 'string') return undefined;
  const trimmed = model.trim();
  return trimmed === '' ? undefined : trimmed;
}

/**
 * Quiet monospace model label, matching agent-card model captions.
 * Renders nothing when summarization_model is absent (older events).
 */
export default function SummarizationModelCaption({
  metadata,
  opacity = 1,
}: SummarizationModelCaptionProps) {
  const model = summarizationModelLabel(metadata);
  if (!model) {
    return null;
  }

  return (
    <Typography
      variant="caption"
      color="text.secondary"
      sx={{
        fontFamily: 'monospace',
        fontSize: '0.7rem',
        fontWeight: 400,
        lineHeight: 1.2,
        opacity,
        transition: 'opacity 0.2s ease',
      }}
    >
      {model}
    </Typography>
  );
}
