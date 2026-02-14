/**
 * FilterPanel — search, status, alert type, chain, date range filters.
 *
 * Ported from old dashboard's FilterPanel.tsx.
 * Adapted for new TARSy: no agent_type, date range presets simplified,
 * alert_type/chain_id are single-select strings (not multi-select arrays).
 */

import { useState } from 'react';
import {
  Paper,
  Button,
  Box,
  Typography,
  Chip,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  TextField,
  InputAdornment,
  Divider,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Stack,
} from '@mui/material';
import { Search, Clear, FilterList, CalendarMonth } from '@mui/icons-material';
import { startOfDay, subDays } from 'date-fns';
import { StatusFilter } from './StatusFilter.tsx';
import type { SessionFilter } from '../../types/dashboard.ts';
import type { FilterOptionsResponse } from '../../types/system.ts';
import { hasActiveFilters } from '../../utils/search.ts';

interface FilterPanelProps {
  filters: SessionFilter;
  onFiltersChange: (filters: SessionFilter) => void;
  onClearFilters: () => void;
  filterOptions?: FilterOptionsResponse;
}

/** Date range presets per design doc. */
const DATE_PRESETS = [
  { label: 'Today', value: 'today' },
  { label: 'Last 7 days', value: '7d' },
  { label: 'Last 30 days', value: '30d' },
] as const;

function presetToDateRange(preset: string): { start: string; end: string } {
  const now = new Date();
  const end = now.toISOString();
  switch (preset) {
    case 'today':
      return { start: startOfDay(now).toISOString(), end };
    case '7d':
      return { start: subDays(now, 7).toISOString(), end };
    case '30d':
      return { start: subDays(now, 30).toISOString(), end };
    default:
      return { start: end, end };
  }
}

export function FilterPanel({
  filters,
  onFiltersChange,
  onClearFilters,
  filterOptions,
}: FilterPanelProps) {
  const [dateDialogOpen, setDateDialogOpen] = useState(false);
  const [customStart, setCustomStart] = useState('');
  const [customEnd, setCustomEnd] = useState('');

  const isActive = hasActiveFilters(filters);

  // Count active filter categories
  const activeCount = [
    filters.search.trim().length >= 3 ? 1 : 0,
    filters.status.length > 0 ? 1 : 0,
    filters.alert_type ? 1 : 0,
    filters.chain_id ? 1 : 0,
    filters.start_date || filters.end_date || filters.date_preset ? 1 : 0,
  ].reduce((a, b) => a + b, 0);

  // ── Handlers ──

  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    onFiltersChange({ ...filters, search: e.target.value });
  };

  const handleStatusChange = (statuses: string[]) => {
    onFiltersChange({ ...filters, status: statuses });
  };

  const handleAlertTypeChange = (value: string) => {
    onFiltersChange({ ...filters, alert_type: value });
  };

  const handleChainChange = (value: string) => {
    onFiltersChange({ ...filters, chain_id: value });
  };

  const handlePreset = (preset: string) => {
    const { start, end } = presetToDateRange(preset);
    onFiltersChange({
      ...filters,
      start_date: start,
      end_date: end,
      date_preset: preset,
    });
  };

  const handleCustomDateApply = () => {
    onFiltersChange({
      ...filters,
      start_date: customStart || null,
      end_date: customEnd || null,
      date_preset: null,
    });
    setDateDialogOpen(false);
  };

  const handleClearDateRange = () => {
    onFiltersChange({ ...filters, start_date: null, end_date: null, date_preset: null });
  };

  const openCustomDateDialog = () => {
    // Pre-fill with current values
    setCustomStart(filters.start_date ? filters.start_date.slice(0, 16) : '');
    setCustomEnd(filters.end_date ? filters.end_date.slice(0, 16) : '');
    setDateDialogOpen(true);
  };

  // ── Date range label ──
  const dateLabel = filters.date_preset
    ? DATE_PRESETS.find((p) => p.value === filters.date_preset)?.label ?? filters.date_preset
    : filters.start_date || filters.end_date
      ? 'Custom Range'
      : 'Date Range';

  return (
    <>
      <Paper sx={{ mt: 2, p: 2 }}>
        {/* Header */}
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
          <Typography variant="h6" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <FilterList />
            Filters
            {activeCount > 0 && (
              <Chip label={activeCount} size="small" color="primary" variant="filled" />
            )}
          </Typography>

          {isActive && (
            <Button variant="text" color="secondary" onClick={onClearFilters} startIcon={<Clear />}>
              Clear All
            </Button>
          )}
        </Box>

        {/* Filter Row */}
        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap', alignItems: 'center' }}>
          {/* Search */}
          <Box sx={{ flex: '2 1 300px', minWidth: 200 }}>
            <TextField
              fullWidth
              placeholder="Search alerts (3+ characters)..."
              variant="outlined"
              size="small"
              value={filters.search}
              onChange={handleSearchChange}
              slotProps={{
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <Search fontSize="small" />
                    </InputAdornment>
                  ),
                  endAdornment: filters.search ? (
                    <InputAdornment position="end">
                      <Button
                        size="small"
                        onClick={() => onFiltersChange({ ...filters, search: '' })}
                        sx={{ minWidth: 'auto', p: 0.5 }}
                      >
                        <Clear fontSize="small" />
                      </Button>
                    </InputAdornment>
                  ) : undefined,
                },
              }}
            />
          </Box>

          {/* Status */}
          <Box sx={{ flex: '1 1 200px', minWidth: 150 }}>
            <StatusFilter
              value={filters.status}
              onChange={handleStatusChange}
              options={filterOptions?.statuses}
            />
          </Box>

          {/* Alert Type */}
          {filterOptions && filterOptions.alert_types.length > 0 && (
            <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
              <FormControl fullWidth size="small">
                <InputLabel>Alert Type</InputLabel>
                <Select
                  value={filters.alert_type}
                  label="Alert Type"
                  onChange={(e) => handleAlertTypeChange(e.target.value as string)}
                >
                  <MenuItem value="">
                    <em>All</em>
                  </MenuItem>
                  {filterOptions.alert_types.map((t) => (
                    <MenuItem key={t} value={t}>
                      {t}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            </Box>
          )}

          {/* Chain */}
          {filterOptions && filterOptions.chain_ids.length > 0 && (
            <Box sx={{ flex: '1 1 180px', minWidth: 140 }}>
              <FormControl fullWidth size="small">
                <InputLabel>Chain</InputLabel>
                <Select
                  value={filters.chain_id}
                  label="Chain"
                  onChange={(e) => handleChainChange(e.target.value as string)}
                >
                  <MenuItem value="">
                    <em>All</em>
                  </MenuItem>
                  {filterOptions.chain_ids.map((c) => (
                    <MenuItem key={c} value={c}>
                      {c}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            </Box>
          )}

          {/* Date Range */}
          <Box sx={{ display: 'flex', gap: 1 }}>
            {DATE_PRESETS.map((p) => (
              <Button
                key={p.value}
                variant={filters.date_preset === p.value ? 'contained' : 'outlined'}
                size="small"
                onClick={() =>
                  filters.date_preset === p.value ? handleClearDateRange() : handlePreset(p.value)
                }
                sx={{ whiteSpace: 'nowrap' }}
              >
                {p.label}
              </Button>
            ))}
            <Button
              variant={
                !filters.date_preset && (filters.start_date || filters.end_date)
                  ? 'contained'
                  : 'outlined'
              }
              size="small"
              startIcon={<CalendarMonth />}
              onClick={openCustomDateDialog}
              sx={{ whiteSpace: 'nowrap' }}
            >
              Custom
            </Button>
          </Box>
        </Box>

        {/* Active Filter Chips */}
        {isActive && (
          <Box sx={{ mt: 2 }}>
            <Divider sx={{ mb: 1 }} />
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
              {filters.search.trim().length >= 3 && (
                <Chip
                  label={`Search: "${filters.search}"`}
                  onDelete={() => onFiltersChange({ ...filters, search: '' })}
                  size="small"
                  color="primary"
                  variant="outlined"
                />
              )}
              {filters.status.map((s) => (
                <Chip
                  key={s}
                  label={`Status: ${s}`}
                  onDelete={() =>
                    onFiltersChange({
                      ...filters,
                      status: filters.status.filter((x) => x !== s),
                    })
                  }
                  size="small"
                  variant="outlined"
                />
              ))}
              {filters.alert_type && (
                <Chip
                  label={`Type: ${filters.alert_type}`}
                  onDelete={() => onFiltersChange({ ...filters, alert_type: '' })}
                  size="small"
                  color="info"
                  variant="outlined"
                />
              )}
              {filters.chain_id && (
                <Chip
                  label={`Chain: ${filters.chain_id}`}
                  onDelete={() => onFiltersChange({ ...filters, chain_id: '' })}
                  size="small"
                  color="info"
                  variant="outlined"
                />
              )}
              {(filters.start_date || filters.end_date || filters.date_preset) && (
                <Chip
                  label={`Date: ${dateLabel}`}
                  onDelete={handleClearDateRange}
                  size="small"
                  color="secondary"
                  variant="outlined"
                />
              )}
            </Box>
          </Box>
        )}
      </Paper>

      {/* Custom Date Range Dialog */}
      <Dialog open={dateDialogOpen} onClose={() => setDateDialogOpen(false)}>
        <DialogTitle>Custom Date Range</DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ mt: 1, minWidth: 300 }}>
            <TextField
              label="Start Date"
              type="datetime-local"
              value={customStart}
              onChange={(e) => setCustomStart(e.target.value)}
              fullWidth
              slotProps={{ inputLabel: { shrink: true } }}
            />
            <TextField
              label="End Date"
              type="datetime-local"
              value={customEnd}
              onChange={(e) => setCustomEnd(e.target.value)}
              fullWidth
              slotProps={{ inputLabel: { shrink: true } }}
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDateDialogOpen(false)}>Cancel</Button>
          <Button onClick={handleCustomDateApply} variant="contained">
            Apply
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
