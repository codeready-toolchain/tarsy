/**
 * StatusFilter — multi-select dropdown for session status filtering.
 *
 * Ported from old dashboard. Uses new TARSy statuses (no paused/canceling).
 */

import { FormControl, InputLabel, Select, MenuItem, Chip, Box } from '@mui/material';
import type { SelectChangeEvent } from '@mui/material';
import { SESSION_STATUS, getStatusDisplayName, getStatusColor } from '../../constants/sessionStatus.ts';

const ALL_STATUSES = Object.values(SESSION_STATUS);

interface StatusFilterProps {
  value: string[];
  onChange: (statuses: string[]) => void;
  options?: string[];
}

export function StatusFilter({ value, onChange, options = ALL_STATUSES }: StatusFilterProps) {
  const handleChange = (event: SelectChangeEvent<string[]>) => {
    onChange(event.target.value as string[]);
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
                label={getStatusDisplayName(s as typeof SESSION_STATUS[keyof typeof SESSION_STATUS])}
                size="small"
                color={getStatusColor(s as typeof SESSION_STATUS[keyof typeof SESSION_STATUS])}
                variant="outlined"
              />
            ))}
          </Box>
        )}
        MenuProps={{
          PaperProps: { style: { maxHeight: 48 * 4.5 + 8, width: 250 } },
        }}
      >
        {options.map((status) => (
          <MenuItem key={status} value={status}>
            <Chip
              label={getStatusDisplayName(status as typeof SESSION_STATUS[keyof typeof SESSION_STATUS])}
              size="small"
              color={getStatusColor(status as typeof SESSION_STATUS[keyof typeof SESSION_STATUS])}
              variant={value.includes(status) ? 'filled' : 'outlined'}
              sx={{ mr: 1 }}
            />
            {getStatusDisplayName(status as typeof SESSION_STATUS[keyof typeof SESSION_STATUS])}
          </MenuItem>
        ))}
      </Select>
    </FormControl>
  );
}
