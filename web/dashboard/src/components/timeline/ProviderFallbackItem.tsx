import { memo } from 'react';
import { Alert, Box, Chip, Typography } from '@mui/material';
import { SwapHoriz } from '@mui/icons-material';
import type { FlowItem } from '../../utils/timelineParser';

interface ProviderFallbackItemProps {
  item: FlowItem;
}

function ProviderFallbackItem({ item }: ProviderFallbackItemProps) {
  const meta = item.metadata || {};
  const from = (meta.original_provider as string) || '?';
  const to = (meta.fallback_provider as string) || '?';
  const reason = meta.reason as string | undefined;
  const attempt = meta.attempt as number | undefined;
  const droppedTools = meta.native_tools_dropped as string[] | undefined;

  return (
    <Alert
      severity="warning"
      icon={<SwapHoriz fontSize="small" />}
      sx={{ my: 1 }}
    >
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 0.5 }}>
        <Typography variant="body2" component="span" sx={{ fontWeight: 600 }}>
          Provider fallback:
        </Typography>
        <Chip label={from} size="small" variant="outlined" sx={{ fontFamily: 'monospace', height: 22 }} />
        <Typography variant="body2" component="span" color="text.secondary">
          &rarr;
        </Typography>
        <Chip label={to} size="small" variant="outlined" sx={{ fontFamily: 'monospace', height: 22 }} />
        {reason && (
          <Typography variant="body2" component="span" color="text.secondary">
            &mdash; {reason}
          </Typography>
        )}
        {attempt != null && (
          <Typography variant="caption" component="span" color="text.secondary">
            (attempt {attempt})
          </Typography>
        )}
      </Box>
      {droppedTools && droppedTools.length > 0 && (
        <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
          Native tools dropped: {droppedTools.join(', ')}
        </Typography>
      )}
    </Alert>
  );
}

export default memo(ProviderFallbackItem);
