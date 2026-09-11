/**
 * LabelFilter — multi-select dropdown for session label filtering.
 * OR semantics: a session matches if it has any of the selected labels.
 */

import { FormControl, InputLabel, Select, MenuItem, Box } from '@mui/material';
import type { SelectChangeEvent } from '@mui/material';
import { LabelChip } from '../common/SessionLabelChips.tsx';

const CLEAR_VALUE = '';

interface LabelFilterProps {
  value: string[];
  onChange: (labels: string[]) => void;
  options?: string[];
}

export function LabelFilter({ value, onChange, options = [] }: LabelFilterProps) {
  const handleChange = (event: SelectChangeEvent<string[]>) => {
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

  return (
    <FormControl fullWidth size="small">
      <InputLabel id="label-filter-label">Label</InputLabel>
      <Select
        labelId="label-filter-label"
        multiple
        value={value}
        label="Label"
        onChange={handleChange}
        renderValue={(selected) => (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
            {selected.map((label) => (
              <LabelChip
                key={label}
                label={label}
                onDelete={() => onChange(value.filter((x) => x !== label))}
                onMouseDown={(event) => event.stopPropagation()}
              />
            ))}
          </Box>
        )}
        MenuProps={{
          PaperProps: { style: { maxHeight: 48 * 4.5 + 8, width: 250 } },
        }}
      >
        <MenuItem value={CLEAR_VALUE}>All</MenuItem>
        {options.map((label) => (
          <MenuItem key={label} value={label} sx={{ gap: 1 }}>
            <Box sx={{ pointerEvents: 'none', display: 'inline-flex' }}>
              <LabelChip
                label={label}
                variant={value.includes(label) ? 'filled' : 'outlined'}
              />
            </Box>
            {label}
          </MenuItem>
        ))}
      </Select>
    </FormControl>
  );
}
