import { memo } from 'react';
import type { FlowItem } from '../../utils/timelineParser';
import ErrorCard from './ErrorCard';

interface ErrorItemProps {
  item: FlowItem;
}

function ErrorItem({ item }: ErrorItemProps) {
  return <ErrorCard label="Error" message={item.content} sx={{ my: 2 }} />;
}

export default memo(ErrorItem);
