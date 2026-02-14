import { memo } from 'react';
import { Alert, Typography } from '@mui/material';
import type { FlowItem } from '../../utils/timelineParser';

interface ErrorItemProps {
  item: FlowItem;
}

/**
 * ErrorItem - renders error timeline events.
 */
function ErrorItem({ item }: ErrorItemProps) {
  return (
    <Alert severity="error" sx={{ my: 1 }}>
      <Typography variant="body2">{item.content}</Typography>
    </Alert>
  );
}

export default memo(ErrorItem);
