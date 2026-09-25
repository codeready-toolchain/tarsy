/**
 * StatusFilter — multi-select dropdown for session status filtering.
 *
 * Ported from old dashboard. Uses new TARSy statuses (no paused/canceling).
 */

import { FormControl, InputLabel, Select, MenuItem, Chip, Box } from '@mui/material';
import type { SelectChangeEvent } from '@mui/material';
import { SESSION_STATUS, getStatusDisplayName, getStatusColor } from '../../constants/sessionStatus.ts';
import type { SessionStatus } from '../../constants/sessionStatus.ts';

const ALL_STATUSES = Object.values(SESSION_STATUS);
const CLEAR_VALUE = '';

function asStatus(status: string): SessionStatus {
  return status as SessionStatus;
}

interface StatusFilterProps {
  value: string[];
  onChange: (statuses: string[]) => void;
  options?: string[];
}

export function StatusFilter({ value, onChange, options = ALL_STATUSES }: StatusFilterProps) {
  const handleChange = (event: SelectChangeEvent<string[]>) => {
    // MUI autofill can pass a comma-separated string instead of string[];
    // normalize to string[] so renderValue's .map always works.
    const raw = event.target.value;
    const normalized: string[] =
      typeof raw === 'string'
        ? raw.split(',').map((s) => s.trim()).filter(Boolean)
        : raw;
    if (normalized.includes(CLEAR_VALUE)) {
      onChange([]);
      return;
    }
    onChange(normalized);
  };

  const removeStatus = (status: string) => {
    onChange(value.filter((s) => s !== status));
  };

  return (
    <FormControl fullWidth size="small">
      <InputLabel id="status-filter-label">Status</InputLabel>
      <Select
        labelId="status-filter-label"
        multiple
        value={value}
        label="Status"
        onChange={handleChange}
        renderValue={(selected) => (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
            {selected.map((s) => (
              <Chip
                key={s}
                label={getStatusDisplayName(asStatus(s))}
                size="small"
                color={getStatusColor(asStatus(s))}
                variant="outlined"
                onDelete={() => removeStatus(s)}
                onMouseDown={(event) => event.stopPropagation()}
              />
            ))}
          </Box>
        )}
        MenuProps={{
          slotProps: {
            paper: { style: { maxHeight: 48 * 4.5 + 8, width: 250 } },
          },
        }}
      >
        <MenuItem value={CLEAR_VALUE}>All</MenuItem>
        {options.map((status) => (
          <MenuItem key={status} value={status} sx={{ gap: 1 }}>
            <Chip
              label={getStatusDisplayName(asStatus(status))}
              size="small"
              color={getStatusColor(asStatus(status))}
              variant={value.includes(status) ? 'filled' : 'outlined'}
              sx={{ pointerEvents: 'none' }}
            />
            {getStatusDisplayName(asStatus(status))}
          </MenuItem>
        ))}
      </Select>
    </FormControl>
  );
}
