import { memo } from 'react';
import { Box } from '@mui/material';
import type { FlowItem } from '../../utils/timelineParser';
import CopyLinkButton from '../shared/CopyLinkButton';
import ErrorCard from './ErrorCard';

interface ErrorItemProps {
  item: FlowItem;
  searchTerm?: string;
  linkUrl?: string;
}

function ErrorItem({ item, searchTerm, linkUrl }: ErrorItemProps) {
  return (
    <Box data-flow-item-id={item.id} sx={{ position: 'relative' }}>
      {linkUrl && (
        <Box
          sx={{
            position: 'absolute',
            top: 8,
            right: 8,
            zIndex: 1,
            color: 'text.secondary',
          }}
        >
          <CopyLinkButton url={linkUrl} />
        </Box>
      )}
      <ErrorCard label="Error" message={item.content} sx={{ my: 2 }} searchTerm={searchTerm} />
    </Box>
  );
}

export default memo(ErrorItem);
