import { memo } from 'react';
import { Box } from '@mui/material';
import type { FlowItem } from '../../utils/timelineParser';
import ErrorCard from './ErrorCard';

interface ErrorItemProps {
  item: FlowItem;
  searchTerm?: string;
}

function ErrorItem({ item, searchTerm }: ErrorItemProps) {
  return (
    <Box data-flow-item-id={item.id}>
      <ErrorCard label="Error" message={item.content} sx={{ my: 2 }} searchTerm={searchTerm} />
    </Box>
  );
}

export default memo(ErrorItem);
