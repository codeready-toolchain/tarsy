/**
 * FilterPanel — search, status, alert type, agent chain, time range filters.
 *
 * Ported from old dashboard's FilterPanel.tsx.
 * Adapted for new TARSy: no agent_type, alert_type/chain_id are single-select
 * strings (not multi-select arrays). Uses TimeRangeModal for date selection
 * matching old dashboard UX (single "Time Range" button + modal with presets).
 */

import { useState, useEffect, useRef, useCallback } from 'react';
import {
  Paper,
  Button,
  Box,
  Typography,
  Chip,
  TextField,
  InputAdornment,
  Divider,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
} from '@mui/material';
import type { SelectChangeEvent } from '@mui/material';
import { Search, Clear, FilterList } from '@mui/icons-material';
import { format, parseISO } from 'date-fns';
import { StatusFilter } from './StatusFilter.tsx';
import { TimeRangeModal } from './TimeRangeModal.tsx';
import type { SessionFilter } from '../../types/dashboard.ts';
import type { FilterOptionsResponse } from '../../types/system.ts';
import { hasActiveFilters } from '../../utils/search.ts';

interface FilterPanelProps {
  filters: SessionFilter;
  onFiltersChange: (filters: SessionFilter) => void;
  onClearFilters: () => void;
  filterOptions?: FilterOptionsResponse;
}

export function FilterPanel({
  filters,
  onFiltersChange,
  onClearFilters,
  filterOptions,
}: FilterPanelProps) {
  const [timeRangeModalOpen, setTimeRangeModalOpen] = useState(false);

  // Local search input state — avoids re-rendering DashboardView on each keystroke.
  // Only propagates to parent after debounce.
  const [searchInput, setSearchInput] = useState(filters.search);
  const searchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const filtersRef = useRef(filters);
  filtersRef.current = filters;

  useEffect(() => {
    if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
    if (searchInput === filtersRef.current.search) return;

    searchDebounceRef.current = setTimeout(() => {
      onFiltersChange({ ...filtersRef.current, search: searchInput });
      searchDebounceRef.current = null;
    }, 300);

    return () => {
      if (searchDebounceRef.current) clearTimeout(searchDebounceRef.current);
    };
  }, [searchInput, onFiltersChange]);

  // Sync local input when parent clears filters externally (e.g. "Clear All", chip delete)
  useEffect(() => {
    if (filters.search !== searchInput) {
      setSearchInput(filters.search);
    }
    // Only react to parent-driven changes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters.search]);

  const isActive = hasActiveFilters(filters);

  const activeCount = [
    filters.search.trim().length >= 3 ? 1 : 0,
    filters.status.length > 0 ? 1 : 0,
    filters.alert_type ? 1 : 0,
    filters.chain_id ? 1 : 0,
    filters.start_date || filters.end_date || filters.date_preset ? 1 : 0,
    filters.scoring_status ? 1 : 0,
  ].reduce((a, b) => a + b, 0);

  // ── Handlers ──

  const handleSearchChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setSearchInput(e.target.value);
  }, []);

  const handleStatusChange = (statuses: string[]) => {
    onFiltersChange({ ...filters, status: statuses });
  };

  const handleTimeRangeApply = (startDate: Date | null, endDate: Date | null, preset?: string) => {
    onFiltersChange({
      ...filters,
      start_date: startDate ? startDate.toISOString() : null,
      end_date: endDate ? endDate.toISOString() : null,
      date_preset: preset || null,
    });
    setTimeRangeModalOpen(false);
  };

  const handleClearDateRange = () => {
    onFiltersChange({ ...filters, start_date: null, end_date: null, date_preset: null });
  };

  // ── Time range button label (matches old dashboard) ──
  const timeRangeLabel = filters.date_preset
    ? `Range: ${filters.date_preset}`
    : filters.start_date || filters.end_date
      ? 'Custom Range'
      : 'Time Range';

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
        </Box>

        {/* Filter Row */}
        <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap', alignItems: 'center' }}>
          {/* Search */}
          <Box sx={{ flex: '2 1 300px', minWidth: 200 }}>
            <TextField
              fullWidth
              placeholder="Search alerts by type, error message..."
              variant="outlined"
              size="small"
              value={searchInput}
              onChange={handleSearchChange}
              slotProps={{
                input: {
                  startAdornment: (
                    <InputAdornment position="start">
                      <Search fontSize="small" />
                    </InputAdornment>
                  ),
                  endAdornment: searchInput ? (
                    <InputAdornment position="end">
                      <Button
                        size="small"
                        onClick={() => {
                          if (searchDebounceRef.current) {
                            clearTimeout(searchDebounceRef.current);
                            searchDebounceRef.current = null;
                          }
                          setSearchInput('');
                          onFiltersChange({ ...filters, search: '' });
                        }}
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

          {/* Scoring Status */}
          <Box sx={{ flex: '1 1 160px', minWidth: 140 }}>
            <FormControl size="small" fullWidth>
              <InputLabel id="scoring-status-label">Scoring</InputLabel>
              <Select
                labelId="scoring-status-label"
                value={filters.scoring_status}
                label="Scoring"
                onChange={(e: SelectChangeEvent) =>
                  onFiltersChange({ ...filters, scoring_status: e.target.value })
                }
              >
                <MenuItem value="">All</MenuItem>
                <MenuItem value="scored">Scored</MenuItem>
                <MenuItem value="not_scored">Not Scored</MenuItem>
                <MenuItem value="scoring_in_progress">In Progress</MenuItem>
                <MenuItem value="scoring_failed">Failed</MenuItem>
              </Select>
            </FormControl>
          </Box>

          {/* Time Range Button — single button opens modal (matches old dashboard) */}
          <Button
            variant="outlined"
            onClick={() => setTimeRangeModalOpen(true)}
            startIcon={<Search />}
            sx={{ height: 40 }}
          >
            {timeRangeLabel}
          </Button>

          {/* Clear All Button */}
          {isActive && (
            <Button
              variant="text"
              color="secondary"
              onClick={onClearFilters}
              startIcon={<Clear />}
              sx={{ height: 40 }}
            >
              Clear All
            </Button>
          )}
        </Box>

        {/* Active Filter Chips */}
        {isActive && (
          <Box sx={{ mt: 2 }}>
            <Divider sx={{ mb: 1 }} />
            <Typography variant="body2" color="text.secondary" gutterBottom>
              Active Filters ({activeCount}):
            </Typography>
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
                  label={`Alert: ${filters.alert_type}`}
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
              {filters.scoring_status && (
                <Chip
                  label={`Scoring: ${filters.scoring_status.replace(/_/g, ' ')}`}
                  onDelete={() => onFiltersChange({ ...filters, scoring_status: '' })}
                  size="small"
                  color="secondary"
                  variant="outlined"
                />
              )}
              {(filters.start_date || filters.end_date || filters.date_preset) && (
                <Chip
                  label={
                    filters.date_preset
                      ? `Range: ${filters.date_preset}`
                      : filters.start_date && filters.end_date
                        ? `${format(parseISO(filters.start_date), 'MMM d')} - ${format(parseISO(filters.end_date), 'MMM d')}`
                        : filters.start_date
                          ? `From: ${format(parseISO(filters.start_date), 'MMM d, yyyy')}`
                          : `Until: ${format(parseISO(filters.end_date!), 'MMM d, yyyy')}`
                  }
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

      {/* Time Range Modal — ported from old dashboard */}
      <TimeRangeModal
        open={timeRangeModalOpen}
        onClose={() => setTimeRangeModalOpen(false)}
        startDate={filters.start_date ? parseISO(filters.start_date) : null}
        endDate={filters.end_date ? parseISO(filters.end_date) : null}
        onApply={handleTimeRangeApply}
      />
    </>
  );
}
