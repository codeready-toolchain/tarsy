/**
 * PaginationControls — compact page navigation with range info and page-size selector.
 */

import {
  Box,
  Typography,
  Pagination,
  FormControl,
  Select,
  MenuItem,
} from '@mui/material';
import type { PaginationState } from '../../types/dashboard.ts';

const PAGE_SIZE_OPTIONS = [10, 25, 50, 100];

interface PaginationControlsProps {
  pagination: PaginationState;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
  disabled?: boolean;
}

export function PaginationControls({
  pagination,
  onPageChange,
  onPageSizeChange,
  disabled = false,
}: PaginationControlsProps) {
  const startItem = (pagination.page - 1) * pagination.pageSize + 1;
  const endItem = Math.min(pagination.page * pagination.pageSize, pagination.totalItems);

  const handlePageSizeChange = (newSize: number) => {
    const currentFirstItem = (pagination.page - 1) * pagination.pageSize + 1;
    const newPage = Math.max(1, Math.ceil(currentFirstItem / newSize));
    onPageSizeChange(newSize);
    if (newPage !== pagination.page) {
      onPageChange(newPage);
    }
  };

  if (pagination.totalItems <= Math.min(...PAGE_SIZE_OPTIONS)) {
    return null;
  }

  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        gap: 1.5,
        py: 0.75,
      }}
    >
      <Typography variant="caption" color="text.secondary" sx={{ whiteSpace: 'nowrap' }}>
        {startItem}–{endItem} of {pagination.totalItems.toLocaleString()}
      </Typography>
      <Pagination
        count={pagination.totalPages}
        page={pagination.page}
        onChange={(_, page) => onPageChange(page)}
        color="primary"
        size="small"
        siblingCount={1}
        boundaryCount={1}
        disabled={disabled}
      />
      <FormControl size="small" variant="standard" sx={{ minWidth: 48 }}>
        <Select
          value={pagination.pageSize}
          onChange={(e) => handlePageSizeChange(e.target.value as number)}
          disabled={disabled}
          disableUnderline
          inputProps={{ 'aria-label': 'Results per page' }}
          sx={{ fontSize: '0.75rem', color: 'text.secondary' }}
        >
          {PAGE_SIZE_OPTIONS.map((size) => (
            <MenuItem key={size} value={size} sx={{ fontSize: '0.75rem' }}>
              {size}
            </MenuItem>
          ))}
        </Select>
      </FormControl>
    </Box>
  );
}
