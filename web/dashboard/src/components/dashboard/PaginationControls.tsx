/**
 * PaginationControls — page size selector, page navigation, jump-to-page.
 *
 * Ported from old dashboard's PaginationControls.tsx.
 * Page size options: 25/50/100 (matching new backend defaults).
 */

import { useState } from 'react';
import {
  Box,
  Typography,
  Pagination,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  TextField,
} from '@mui/material';
import type { PaginationState } from '../../types/dashboard.ts';

const PAGE_SIZE_OPTIONS = [25, 50, 100];

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
  const [jumpToPage, setJumpToPage] = useState('');

  const startItem = (pagination.page - 1) * pagination.pageSize + 1;
  const endItem = Math.min(pagination.page * pagination.pageSize, pagination.totalItems);

  const handleJumpToPageChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    if (value === '' || /^\d+$/.test(value)) {
      setJumpToPage(value);
    }
  };

  const handleJumpSubmit = () => {
    const num = parseInt(jumpToPage, 10);
    if (num >= 1 && num <= pagination.totalPages) {
      onPageChange(num);
    }
    setJumpToPage('');
  };

  const handleJumpKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') handleJumpSubmit();
  };

  const handlePageSizeChange = (newSize: number) => {
    // Keep current position when changing page size
    const currentFirstItem = (pagination.page - 1) * pagination.pageSize + 1;
    const newPage = Math.max(1, Math.ceil(currentFirstItem / newSize));
    onPageSizeChange(newSize);
    if (newPage !== pagination.page) {
      onPageChange(newPage);
    }
  };

  // Don't render if all items fit on one page with the smallest page size
  if (pagination.totalItems <= Math.min(...PAGE_SIZE_OPTIONS)) {
    return null;
  }

  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        mt: 2,
        flexWrap: 'wrap',
        gap: 2,
      }}
    >
      {/* Results Info + Page Size */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, minWidth: 200 }}>
        <Typography variant="body2" color="text.secondary">
          Showing {startItem.toLocaleString()}–{endItem.toLocaleString()} of{' '}
          {pagination.totalItems.toLocaleString()} results
        </Typography>

        <FormControl size="small" sx={{ minWidth: 80 }}>
          <InputLabel>Per Page</InputLabel>
          <Select
            value={pagination.pageSize}
            label="Per Page"
            onChange={(e) => handlePageSizeChange(e.target.value as number)}
            disabled={disabled}
          >
            {PAGE_SIZE_OPTIONS.map((size) => (
              <MenuItem key={size} value={size}>
                {size}
              </MenuItem>
            ))}
          </Select>
        </FormControl>
      </Box>

      {/* Pagination Navigation */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
        {/* Jump to Page (for large result sets) */}
        {pagination.totalPages > 10 && (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Typography variant="body2" color="text.secondary">
              Go to:
            </Typography>
            <TextField
              size="small"
              value={jumpToPage}
              onChange={handleJumpToPageChange}
              onKeyDown={handleJumpKeyDown}
              onBlur={handleJumpSubmit}
              placeholder={String(pagination.page)}
              disabled={disabled}
              sx={{ width: 70, '& .MuiOutlinedInput-input': { textAlign: 'center' } }}
            />
            <Typography variant="body2" color="text.secondary">
              of {pagination.totalPages}
            </Typography>
          </Box>
        )}

        <Pagination
          count={pagination.totalPages}
          page={pagination.page}
          onChange={(_, page) => onPageChange(page)}
          color="primary"
          size="small"
          showFirstButton
          showLastButton
          siblingCount={1}
          boundaryCount={1}
          disabled={disabled}
        />
      </Box>
    </Box>
  );
}
